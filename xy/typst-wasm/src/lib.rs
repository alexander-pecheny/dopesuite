//! typst compiled to WASI, exposing a raw linear-memory ABI to the Go host.
//!
//! ABI (all pointers are offsets into the guest's linear memory):
//!
//!   alloc(len) -> ptr                     host writes bytes straight into guest memory
//!   dealloc(ptr, len)                     host frees a buffer it was handed back
//!   add_font(ptr, len)                    call once per process; a file may hold several faces
//!   add_file(name_ptr, name_len, ptr, len)  an image the source reads; once per generation
//!   reset_files()                         drop the images, keep the fonts
//!   compile(src_ptr, src_len, want_pdf) -> u64   (ptr << 32) | len of the result buffer
//!
//! Result buffer: [0] = 1 on success / 0 on failure, [1..5] = page count (u32 LE),
//! [5..] = the PDF bytes on success, a UTF-8 error message on failure. No base64,
//! no JSON: the payload is already bytes, and split_fit calls this in a loop.

use std::collections::HashMap;
use std::sync::Mutex;

use typst::diag::{FileError, FileResult};
use typst::foundations::{Bytes, Datetime, Duration};
use typst::syntax::{FileId, RootedPath, Source, VirtualPath, VirtualRoot};
use typst::text::{Font, FontBook};
use typst::utils::LazyHash;
use typst::{Library, LibraryExt, World};
use typst_layout::PagedDocument;

/// State that outlives a single compile: the fonts (parsed once — this is the
/// expensive part) and the images of the generation in flight.
struct State {
    fonts: Vec<Font>,
    book: LazyHash<FontBook>,
    library: LazyHash<Library>,
    files: HashMap<String, Bytes>,
}

static STATE: Mutex<Option<State>> = Mutex::new(None);

fn with_state<R>(f: impl FnOnce(&mut State) -> R) -> R {
    let mut guard = STATE.lock().unwrap();
    let state = guard.get_or_insert_with(|| State {
        fonts: Vec::new(),
        book: LazyHash::new(FontBook::new()),
        library: LazyHash::new(Library::builder().build()),
        files: HashMap::new(),
    });
    f(state)
}

/// world borrows the state for one compile. typst's World is its filesystem, so
/// every lookup below is a HashMap hit — nothing reaches a real file.
struct MemWorld<'a> {
    state: &'a State,
    main: Source,
}

impl World for MemWorld<'_> {
    fn library(&self) -> &LazyHash<Library> {
        &self.state.library
    }

    fn book(&self) -> &LazyHash<FontBook> {
        &self.state.book
    }

    fn main(&self) -> FileId {
        self.main.id()
    }

    fn source(&self, id: FileId) -> FileResult<Source> {
        if id == self.main.id() {
            Ok(self.main.clone())
        } else {
            Err(FileError::NotFound(id.vpath().as_rootless_path().into()))
        }
    }

    fn file(&self, id: FileId) -> FileResult<Bytes> {
        // The .typ refers to its images by bare name, which is how they are keyed.
        let path = id.vpath().as_rootless_path();
        let name = path.file_name().and_then(|n| n.to_str()).unwrap_or_default();
        self.state
            .files
            .get(name)
            .cloned()
            .ok_or_else(|| FileError::NotFound(path.into()))
    }

    fn font(&self, index: usize) -> Option<Font> {
        self.state.fonts.get(index).cloned()
    }

    fn today(&self, _offset: Option<Duration>) -> Option<Datetime> {
        // No clock: WASI would need a host call, and a deterministic render is
        // worth more to us than a real date (nothing in a handout prints one).
        Datetime::from_ymd(1970, 1, 1)
    }
}

// ---- memory helpers ----

#[no_mangle]
pub extern "C" fn alloc(len: u32) -> u32 {
    let mut buf = Vec::<u8>::with_capacity(len as usize);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr as u32
}

/// # Safety
/// ptr/len must come from a previous alloc (or a buffer this module returned).
#[no_mangle]
pub unsafe extern "C" fn dealloc(ptr: u32, len: u32) {
    drop(Vec::from_raw_parts(ptr as *mut u8, len as usize, len as usize));
}

unsafe fn slice<'a>(ptr: u32, len: u32) -> &'a [u8] {
    std::slice::from_raw_parts(ptr as *const u8, len as usize)
}

/// pack hands a buffer back to the host as (ptr << 32) | len, leaking it; the host
/// reads it out of linear memory and calls dealloc.
fn pack(mut buf: Vec<u8>) -> u64 {
    buf.shrink_to_fit();
    let ptr = buf.as_mut_ptr() as u64;
    let len = buf.len() as u64;
    std::mem::forget(buf);
    (ptr << 32) | len
}

fn result_buf(ok: bool, pages: u32, payload: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(5 + payload.len());
    out.push(ok as u8);
    out.extend_from_slice(&pages.to_le_bytes());
    out.extend_from_slice(payload);
    out
}

// ---- exports ----

/// # Safety
/// ptr/len must describe a font file the host wrote into guest memory.
#[no_mangle]
pub unsafe extern "C" fn add_font(ptr: u32, len: u32) {
    let data = Bytes::new(slice(ptr, len).to_vec());
    with_state(|s| {
        // One file can carry several faces (a collection).
        for face in Font::iter(data) {
            s.fonts.push(face);
        }
        s.book = LazyHash::new(FontBook::from_fonts(&s.fonts));
    });
}

/// # Safety
/// Both pointer/length pairs must describe buffers in guest memory.
#[no_mangle]
pub unsafe extern "C" fn add_file(name_ptr: u32, name_len: u32, ptr: u32, len: u32) {
    let name = String::from_utf8_lossy(slice(name_ptr, name_len)).into_owned();
    let data = Bytes::new(slice(ptr, len).to_vec());
    with_state(|s| {
        s.files.insert(name, data);
    });
}

/// Drops the images but keeps the parsed fonts, which are the costly part.
#[no_mangle]
pub extern "C" fn reset_files() {
    with_state(|s| s.files.clear());
}

/// # Safety
/// src_ptr/src_len must describe the .typ source in guest memory.
#[no_mangle]
pub unsafe extern "C" fn compile(src_ptr: u32, src_len: u32, want_pdf: u32) -> u64 {
    let src = String::from_utf8_lossy(slice(src_ptr, src_len)).into_owned();
    let out = with_state(|state| {
        if state.fonts.is_empty() {
            return result_buf(false, 0, b"no fonts loaded");
        }
        let vpath = match VirtualPath::new("source.typ") {
            Ok(v) => v,
            Err(e) => return result_buf(false, 0, format!("bad path: {e}").as_bytes()),
        };
        let id = FileId::new(RootedPath::new(VirtualRoot::Project, vpath));
        let world = MemWorld {
            state,
            main: Source::new(id, src),
        };

        let doc = match typst::compile::<PagedDocument>(&world).output {
            Ok(doc) => doc,
            Err(diags) => {
                let msg = diags
                    .iter()
                    .map(|d| d.message.to_string())
                    .collect::<Vec<_>>()
                    .join("; ");
                return result_buf(false, 0, msg.as_bytes());
            }
        };
        let pages = doc.pages().len() as u32;
        if want_pdf == 0 {
            // split_fit's binary search only needs the page count; skip the PDF.
            return result_buf(true, pages, &[]);
        }
        match typst_pdf::pdf(&doc, &typst_pdf::PdfOptions::default()) {
            Ok(pdf) => result_buf(true, pages, &pdf),
            Err(diags) => result_buf(false, pages, format!("pdf: {diags:?}").as_bytes()),
        }
    });
    pack(out)
}
