package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typstdoc"
)

// Export turns the client-supplied chgksuite "4s" source (the list's decrypted
// card descriptions concatenated in board order) plus its referenced images into a
// .docx (chgk/docx) or a .pdf (chgk/typstdoc — the same document, laid out by typst
// to look like the docx) — entirely in-process, no Python. This is one of the two
// places plaintext briefly reaches the server (a tolerated risk); nothing is written
// to disk: both are built in memory and streamed, and typst itself runs as a wasm
// module whose filesystem is a map.

// maxExportRequest bounds the whole multipart export upload (4s source + images)
// so a single request can't exhaust memory during parsing.
const maxExportRequest = 64 << 20

// exportPDFTimeout bounds a typst run. A package is one compile (unlike split_fit,
// which binary-searches), so this is generous.
const exportPDFTimeout = 120 * time.Second

// normalizeNewlines collapses CRLF/CR to LF. Browsers normalize textarea/text
// field values to CRLF in multipart/form-data, which would otherwise leave a
// stray \r on every line — breaking the 4s parser and the .hndt "---" block
// separators (server parsers split on \n).
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// safeImageName reduces a client-supplied filename to a path-free base name,
// rejecting empties and traversal attempts. Images in the 4s source are
// referenced by base name, so this is what the (img …) directives look for.
func safeImageName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

// headerSafeName strips characters that could break out of the quoted-string
// value of a Content-Disposition header (quotes, backslashes, control bytes).
func headerSafeName(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, name)
}

// exportRequest is what both export endpoints take: the 4s source, the name to
// hand back, and the images the source references (multipart "img" parts, keyed by
// base name).
type exportRequest struct {
	source string
	name   string
	device string // "" (desktop) or "mobile" — PDF export only
	images map[string][]byte
}

// readExportRequest parses and validates the multipart body shared by the docx and
// PDF exports. ext is stripped off the client-supplied filename, since the handler
// appends its own.
func (s *server) readExportRequest(w http.ResponseWriter, r *http.Request, ext string) (exportRequest, bool) {
	req, _, ok := s.readExportForm(w, r, ext)
	return req, ok
}

// readExportForm is readExportRequest plus the raw form, for the pack export —
// it reads fields (formats, hndt) the single-format endpoints have no use for.
func (s *server) readExportForm(w http.ResponseWriter, r *http.Request, ext string) (exportRequest, *memForm, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxExportRequest)
	form, err := readMultipart(r, maxExportRequest)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return exportRequest{}, nil, false
	}
	source := normalizeNewlines(form.Value("source"))
	if strings.TrimSpace(source) == "" {
		httpError(w, http.StatusBadRequest, "empty source")
		return exportRequest{}, nil, false
	}
	name := headerSafeName(safeImageName(form.Value("filename")))
	if name == "" {
		name = "export"
	}
	req := exportRequest{
		source: source,
		name:   strings.TrimSuffix(name, ext),
		device: form.Value("device"),
		images: map[string][]byte{},
	}
	for _, f := range form.Files("img") {
		if base := safeImageName(f.Filename); base != "" {
			req.images[base] = f.Data
		}
	}
	return req, form, true
}

// attrChar reports whether r may appear unescaped in an RFC 5987 ext-value.
func attrChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case strings.ContainsRune("!#$&+-.^_`|~", r):
		return true
	}
	return false
}

// contentDisposition builds the attachment header for a download name. A
// quoted-string filename is latin-1, so a Cyrillic list name sent that way
// arrives as mojibake (issue #43); RFC 6266's filename* carries the real name in
// UTF-8, with the ASCII-folded filename= left behind for anything that ignores it.
func contentDisposition(name string) string {
	var ascii, ext strings.Builder
	needsExt := false
	for _, r := range name {
		if r < 0x80 {
			ascii.WriteRune(r)
		} else {
			ascii.WriteByte('_')
			needsExt = true
		}
		if attrChar(r) {
			ext.WriteRune(r)
			continue
		}
		needsExt = true
		for _, b := range []byte(string(r)) {
			fmt.Fprintf(&ext, "%%%02X", b)
		}
	}
	h := `attachment; filename="` + ascii.String() + `"`
	if needsExt {
		h += "; filename*=UTF-8''" + ext.String()
	}
	return h
}

// serveDownload streams a generated file as an attachment. Nothing about it is
// cacheable: it is the user's plaintext, in a format anyone can read.
func serveDownload(w http.ResponseWriter, b []byte, filename, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

func (s *server) handleExportDocx(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	req, ok := s.readExportRequest(w, r, ".docx")
	if !ok {
		return
	}
	b, err := docx.Export(fsource.Parse(req.source, "chgk"), req.images, docx.Options{})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "docx export failed: "+err.Error())
		return
	}
	serveDownload(w, b, req.name+".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
}

// handleExportPDF is the same export in the other format: the same 4s source and
// the same images, laid out by typst (internal/chgk/typstdoc) to look like the
// docx. It runs on the shared in-process typst (wasm) pool, so — like handouts —
// the plaintext stays in memory.
func (s *server) handleExportPDF(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	req, ok := s.readExportRequest(w, r, ".pdf")
	if !ok {
		return
	}
	ts, err := s.typesetter()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "typst unavailable: "+err.Error())
		return
	}
	device := typstdoc.Desktop
	if req.device == "mobile" {
		device = typstdoc.Mobile
	}
	ctx, cancel := context.WithTimeout(r.Context(), exportPDFTimeout)
	defer cancel()
	b, err := typstdoc.Export(ctx, fsource.Parse(req.source, "chgk"), req.images, ts, typstdoc.Options{Device: device})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			httpError(w, http.StatusGatewayTimeout, "typst timed out")
			return
		}
		httpError(w, http.StatusInternalServerError, "pdf export failed: "+err.Error())
		return
	}
	serveDownload(w, b, req.name+".pdf", "application/pdf")
}
