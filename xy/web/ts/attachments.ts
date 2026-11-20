// attachments.ts — the board's attachment kernel: the decrypted list/URL
// caches (LRU object URLs keyed id:rev), upload with opt-in WebP recompression,
// replace-in-place, delete, paste-to-attach, download with offline fallback,
// and the image lightbox. A create(deps) factory like the board's other
// kernels; board.ts supplies the popup menu, the open-card accessor and the
// timeline surface, and wires the result into carddetail's seams.
import { overlayStack } from "./overlaystack.js";
import { modal } from "./modal.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { sealPayload } from "./eventpayload.js";
import { xySync } from "./sync.js";
import type { DataKey } from "./crypto.js";
import type { MenuItem } from "./timeline.js";
import { icon, iconed } from "./icons_gen.js";
import S from "./i18nstrings.js";
import { fileType, isXyimage, rasterOf, XYIMAGE_EXT, XYIMAGE_MIME, XYIMAGE_SNIFF_LEN } from "./xyimagefile.js";

const { fetchJSON, jpatch, jdelete, el, errMsg } = xyApp;

// ---- attachment caches (preview image resolution) ----
// A card's attachment list changes on upload/delete, so it's cached per card and
// invalidated by loadAttachments (which every mutation path already calls).
// Attachment *bytes* are immutable for a given id, so a decrypted object URL is
// memoized for the page's lifetime — reopening a preview then costs no network,
// no decrypt. The URL cache is LRU-capped; evicted URLs are revoked.
// Replacing an attachment (handleReplaceAttachment) keeps its id and bumps its
// rev, so both caches key on id+rev — otherwise a replaced screenshot would keep
// serving the bytes it replaced for the rest of the page's life.
export interface Attachment {
  id: number;
  filename: string;
  mime: string;
  size: number;
  rev?: number;
  lossless?: boolean;
  is_excerpt?: boolean;
  [key: string]: unknown;
}
export type NamedAttachment = Attachment & { name: string };

// gatherTargets maps wanted image names to the first attachment bearing each
// name, in card order — shared by resolveImages (object URLs for previews) and
// the handouts request assembly (multipart blobs). Names are compared in NFC,
// because macOS and iOS hand over file names with letters such as a short i decomposed while the
// text has them composed; the key is the text's spelling, which is what the
// server looks the picture up by.
export function gatherTargets(lists: ReadonlyArray<ReadonlyArray<NamedAttachment>>, wanted: ReadonlySet<string>): Map<string, NamedAttachment> {
  const byNFC = new Map([...wanted].map((w) => [w.normalize("NFC"), w]));
  const targets = new Map<string, NamedAttachment>();
  for (const atts of lists) {
    for (const att of atts) {
      const name = att.name && byNFC.get(att.name.normalize("NFC"));
      if (name && !targets.has(name)) targets.set(name, att);
    }
  }
  return targets;
}

const KIB = 1024;
const MIB = KIB * KIB;
const WEBP_QUALITY = 0.7; // the opt-in re-encode's quality
// How long a download's object URL lives; the click has started the save by then.
const DOWNLOAD_URL_TTL_MS = 10000;

export function humanSize(n: number): string {
  if (n < KIB) return n + " B";
  if (n < MIB) return (n / KIB).toFixed(1) + " KB";
  return (n / MIB).toFixed(1) + " MB";
}

export function extFromMime(m: string): string {
  const map: Record<string, string> = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/bmp": "bmp", "image/svg+xml": "svg" };
  if (map[m]) return map[m];
  const sub = (m || "").split("/")[1];
  return sub ? sub.replace(/[^a-z0-9]+/gi, "") : "png";
}

export // withExt drops any extension the user typed and forces the one that matches the
// stored format (webp when compressing, else the source image's type), so the
// filename never claims a type the bytes aren't.
function withExt(name: string, ext: string): string {
  const base = name.replace(/\.[^./\\]+$/, "").trim();
  return `${base || S.attachments.name.defaultStem()}.${ext}`;
}

// nameStemEnd is where the name ends and its extension begins — what a rename
// box should have selected, so that typing over the suggestion keeps the ".png"
// (#83). A name that is all extension (".gitignore") has no stem to spare, and
// one with no dot at all is a stem throughout: both select whole.
export function nameStemEnd(name: string): number {
  const dot = name.lastIndexOf(".");
  return dot > 0 ? dot : name.length;
}

// The card editor's static elements the kernel writes to and listens on.
export { XYIMAGE_MIME };

export interface AttachmentsUI {
  message: HTMLElement;
  // The create-image button: a blank Edited Image on the open card.
  createImage?: HTMLElement;
  list: HTMLElement;
  upload: HTMLElement;
  file: HTMLInputElement;
  compress: HTMLInputElement;
  cardOverlay: HTMLElement;
  pasteForm: HTMLElement;
  pasteName: HTMLInputElement;
  pasteCompress: HTMLInputElement;
}

export interface AttachmentsDeps {
  ui: AttachmentsUI;
  mustDK(): DataKey;
  openCardId(): number | null;
  popupMenu(anchor: HTMLElement, items: MenuItem[]): void;
  timeline: {
    load(cardId: number): Promise<void>;
    setAttachments(list: NamedAttachment[]): void;
  };
  // An image pasted while the caret was in the comment box still uploads as a
  // card attachment, but ALSO joins the pending comment (its ref rides the
  // encrypted payload).
  onCommentImage?(attId: number): void;
}

type EditorStart = { file?: Uint8Array; picture?: { blob: Blob; name: string } };

export function create(deps: AttachmentsDeps) {
const ui = deps.ui;

const attListCache = new Map<number, NamedAttachment[]>(); // cardId → [{ ...att, name }]
const attBlobCache = new Map<string, Blob>();   // id:rev → decrypted bytes
const attUrlCache = new Map<string, string>();  // id:rev → object URL over those bytes
const ATT_CACHE_MAX = 64;

// cardAttachments lists one card's attachments with their filenames decrypted.
async function cardAttachments(cardId: number, refresh = false): Promise<NamedAttachment[]> {
  if (refresh) attListCache.delete(cardId);
  const hit = attListCache.get(cardId);
  if (hit) return hit;
  let atts: Attachment[];
  try { atts = (await fetchJSON(`/api/cards/${cardId}/attachments`)) as Attachment[]; } catch (_) { return []; }
  const out = await Promise.all(atts.map(async (att) => {
    let name = "";
    try { name = await xyCrypto.decField(deps.mustDK(), att.filename); } catch (_) {}
    return { ...att, name };
  }));
  attListCache.set(cardId, out);
  return out;
}

// attachmentBlob decrypts one attachment, reading its ciphertext through the
// offline IndexedDB mirror (so a reload doesn't re-download) and memoizing the
// result. Callers that want the BYTES take them from here rather than fetching
// the object URL below: the CSP's connect-src 'self' forbids fetching a blob:.
async function attachmentBlob(att: Attachment): Promise<Blob> {
  const rev = att.rev || 0;
  const key = `${att.id}:${rev}`;
  const hit = attBlobCache.get(key);
  if (hit) { attBlobCache.delete(key); attBlobCache.set(key, hit); return hit; } // LRU touch
  let cipher: Uint8Array<ArrayBuffer>;
  // The offline mirror is keyed by id alone; a stale rev there means the bytes
  // are the ones a replace superseded, so refetch instead of trusting them.
  const cached = await xySync.getAttachment(att.id).catch(() => null);
  if (cached && (cached.rev || 0) === rev) {
    cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
  } else {
    const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error(S.attachments.error.blobDownloadFailed());
    cipher = new Uint8Array(await res.arrayBuffer());
    try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher, rev }); } catch (_) {}
  }
  const plain = await xyCrypto.decBytes(deps.mustDK(), cipher);
  const blob = new Blob([plain], { type: att.mime });
  attBlobCache.set(key, blob);
  for (const stale of [...attBlobCache.keys()].slice(0, attBlobCache.size - ATT_CACHE_MAX)) {
    URL.revokeObjectURL(attUrlCache.get(stale) || "");
    attUrlCache.delete(stale);
    attBlobCache.delete(stale);
  }
  return blob;
}

// attachmentUrl is the same bytes as a URL, for the DOM (previews, lightbox,
// downloads). It is revoked with the blob it points at, by the eviction above.
// An Edited Image is drawn as its raster rendition: an <img> cannot read the
// container, and the raster is what docx gets, so the preview cannot drift
// from the export.
async function attachmentUrl(att: Attachment): Promise<string> {
  const key = `${att.id}:${att.rev || 0}`;
  const blob = await attachmentBlob(att);
  let url = attUrlCache.get(key);
  if (!url) { url = URL.createObjectURL(await displayBlob(blob)); attUrlCache.set(key, url); }
  return url;
}

async function displayBlob(blob: Blob): Promise<Blob> {
  const head = new Uint8Array(await blob.slice(0, XYIMAGE_SNIFF_LEN).arrayBuffer());
  if (!isXyimage(head)) return blob;
  const raster = rasterOf(new Uint8Array(await blob.arrayBuffer()));
  return raster ? new Blob([new Uint8Array(raster)], { type: "image/png" }) : blob;
}

// imageBlob hands one named image on a card to a caller that needs its bytes —
// the clipboard. Null when the card has no attachment by that name.
async function imageBlob(cardId: number, name: string): Promise<Blob | null> {
  const att = gatherTargets([await cardAttachments(cardId)], new Set([name])).get(name);
  return att ? await attachmentBlob(att) : null;
}

// resolveImages maps each wanted image name → a decrypted object URL, scanning
// the cards' attachments (online only — mirrors the docx export's image
// gathering). Attachment lists and image bodies are fetched in parallel, and
// `onImage` fires per image as it lands so callers can fill placeholders in
// progressively instead of waiting for the slowest one. Missing names simply
// stay a placeholder (see renderRich).
async function resolveImages(cards: ReadonlyArray<{ id: number }>, wanted: Set<string>, onImage?: (name: string, url: string) => void): Promise<Map<string, string>> {
  const map = new Map<string, string>();
  if (!wanted.size || !xySync.isOnline()) return map;
  const lists = await Promise.all(cards.map((c) => cardAttachments(c.id)));
  const targets = gatherTargets(lists, wanted);
  await Promise.all([...targets].map(async ([name, att]) => {
    try {
      const url = await attachmentUrl(att);
      map.set(name, url);
      if (onImage) onImage(name, url);
    } catch (_) {}
  }));
  return map;
}


// ---- attachments ----
// cardImageNames holds the decrypted filenames of the open card's image
// attachments — the choices offered by the handout image picker (the Fields view).
let cardImageNames: string[] = [];

async function loadAttachments(cardId: number): Promise<void> {
  const box = ui.list;
  cardImageNames = [];
  // Always refetch: this runs on card open and after every upload/delete, so it
  // doubles as the invalidation point for the preview's attachment-list cache.
  const list = await cardAttachments(cardId, true);
  // Built off-DOM and swapped in one go — see loadTimeline on the scroll jump.
  const frag = document.createDocumentFragment();
  for (const att of list) {
    const name = att.name || S.attachments.list.fallbackName();
    const isImage = (att.mime || "").startsWith("image/");
    if (isImage) cardImageNames.push(name);
    // Images open in the lightbox (save via right-click there); other files download.
    const row = el("div", { class: "attach-row" },
      el("button", { class: "attach-name", type: "button", onclick: () => { void (isImage ? viewAttachment(att, name) : download(att, name)); } }, ...iconed("paperclip", name)),
      att.is_excerpt ? el("span", { class: "tl-badge", text: S.attachments.list.excerptBadge() }) : null,
      el("span", { class: "attach-size", text: humanSize(att.size) }),
      el("button", {
        class: "attach-del", type: "button", title: S.attachments.list.actionsTitle(), "aria-haspopup": "true",
        onclick: (e: Event) => attachMenu(e.currentTarget as HTMLElement, att, name),
      }, icon("ellipsis")),
    );
    frag.append(row);
  }
  box.replaceChildren(frag);
  deps.timeline.setAttachments(list);
}

function attachMenu(anchor: HTMLElement, att: NamedAttachment, name: string): void {
  const edit: MenuItem[] = (att.mime || "").startsWith("image/")
    ? [{ icon: icon("pencil"), label: S.attachments.menu.edit(), onClick: () => { void editImage(att, name); } }]
    : [];
  deps.popupMenu(anchor, [
    ...edit,
    { icon: icon("replace"), label: S.attachments.menu.replace(), onClick: () => pickReplacement(att, name) },
    { icon: icon("trash-2"), label: S.attachments.menu.delete(), onClick: () => { void removeAttachment(att, name); } },
    {
      label: S.attachments.menu.excerpt(), checked: !!att.is_excerpt,
      onClick: () => { void attachAction(async () => {
        await jpatch(`/api/attachments/${att.id}`, { is_excerpt: !att.is_excerpt });
      }); },
    },
  ]);
}

async function attachAction(fn: () => Promise<unknown>): Promise<void> {
  const msg = ui.message;
  if (!xySync.requireOnline(S.attachments.status.offlineEdit(), msg)) return;
  try {
    await fn();
    msg.textContent = "";
    const oc = deps.openCardId();
    if (oc != null) {
      await loadAttachments(oc);
      await deps.timeline.load(oc);
    }
  } catch (err) { msg.textContent = errMsg(err); }
}

// pickReplacement re-shoots an attachment in place: the id (and so its excerpt
// flag, its position, and every card that names the file) survives, only the
// bytes change. The compress choice is inherited from what the original stored.
function pickReplacement(att: NamedAttachment, name: string): void {
  const picker = el("input", { type: "file" }) as HTMLInputElement;
  picker.addEventListener("change", () => {
    const file = picker.files && picker.files[0];
    if (!file) return;
    void attachAction(async () => {
      const msg = ui.message;
      msg.textContent = S.attachments.status.encrypting();
      const { bytes, mime, lossless } = await storedBytes(file, !!att.lossless);
      await putReplacement(att, name, bytes, mime, lossless);
    });
  });
  picker.click();
}

// putReplacement writes new bytes over an attachment, keeping its id.
async function putReplacement(att: NamedAttachment, name: string, bytes: Uint8Array<ArrayBuffer>, mime: string, lossless: boolean): Promise<void> {
  const key = deps.mustDK();
  const fd = new FormData();
  fd.append("meta", JSON.stringify({
    filename: await xyCrypto.encField(key, name),
    mime, lossless,
    event_payload: await sealPayload(key, "attach_replace", { file: name }),
  }));
  fd.append("blob", new Blob([await xyCrypto.encBytes(key, bytes)], { type: "application/octet-stream" }), "blob");
  const res = await fetch(`/api/attachments/${att.id}`, { method: "PUT", credentials: "same-origin", body: fd });
  if (!res.ok) throw new Error((await res.text()) || S.attachments.error.replaceFailed());
}

// ---- the image editor (ADR-0024) ----

// freeName is a name for a new Edited Image that no attachment on the card
// already has: a name is what (img …) finds a picture by, so two of the same
// name would leave the second one unreachable.
async function freeName(cardId: number, stem: string): Promise<string> {
  const taken = new Set((await cardAttachments(cardId)).map((a) => a.name.normalize("NFC")));
  for (let n = 1; ; n++) {
    const name = `${n === 1 ? stem : `${stem} ${n}`}.${XYIMAGE_EXT}`;
    if (!taken.has(name.normalize("NFC"))) return name;
  }
}

// editImage opens the editor on an image attachment. An Edited Image is
// reopened and saved back over itself; any other picture is the start of a
// NEW Edited Image beside it, and the original stays as it was: nothing in the
// card's text is rewritten, so the editor picks the new one by hand.
async function editImage(att: NamedAttachment, name: string): Promise<void> {
  if (!xySync.requireOnline(S.attachments.status.offlineEdit(), ui.message)) return;
  const cardId = deps.openCardId();
  if (cardId == null) return;
  try {
    const blob = await attachmentBlob(att);
    const bytes = new Uint8Array(await blob.arrayBuffer());
    if (isXyimage(bytes)) {
      await openImageEditor({ file: bytes }, (out) => putReplacement(att, name, out, XYIMAGE_MIME, true));
      return;
    }
    let target: string | null = null;
    await openImageEditor({ picture: { blob, name } }, async (out) => {
      target ??= await freeName(cardId, name.slice(0, nameStemEnd(name)) || name);
      await saveNewImage(cardId, target, out);
    });
  } catch (err) { ui.message.textContent = errMsg(err); }
}

// A new Edited Image is uploaded on its first save and replaced on each one
// after, so saving twice leaves one attachment, not two.
const savedNew = new Map<string, NamedAttachment>();

async function saveNewImage(cardId: number, name: string, out: Uint8Array<ArrayBuffer>): Promise<void> {
  const key = `${cardId}:${name}`;
  const prev = savedNew.get(key);
  if (prev) { await putReplacement(prev, name, out, XYIMAGE_MIME, true); return; }
  const id = await uploadAttachment(new File([out], name, { type: XYIMAGE_MIME }), true, name);
  const made = (await cardAttachments(cardId)).find((a) => a.id === id);
  if (made) savedNew.set(key, made);
}

async function createImage(): Promise<void> {
  if (!xySync.requireOnline(S.attachments.status.offlineUpload(), ui.message)) return;
  const cardId = deps.openCardId();
  if (cardId == null) return;
  let target: string | null = null;
  try {
    await openImageEditor({}, async (out) => {
      target ??= await freeName(cardId, S.attachments.editor.defaultName());
      await saveNewImage(cardId, target, out);
    });
  } catch (err) { ui.message.textContent = errMsg(err); }
}

ui.createImage?.addEventListener("click", () => { void createImage(); });

// openImageEditor puts the editor over the card, full screen, on the overlay
// stack: back, Escape (once the editor has let go of its selection) and its ✕
// close it, and closing with unsaved changes asks first. After every save the
// card's attachments and timeline are reloaded.
async function openImageEditor(start: EditorStart, save: (out: Uint8Array<ArrayBuffer>) => Promise<void>): Promise<void> {
  const { createEditor } = await import("./xyimageeditor.js");
  const root = el("div");
  const overlay = el("div", { class: "xyed-overlay", role: "dialog", "aria-label": S.attachments.editor.title() }, root);
  document.body.append(overlay);
  const editor = await createEditor(root, {
    ...start,
    onSave: async (out) => {
      await save(new Uint8Array(out));
      const oc = deps.openCardId();
      if (oc != null) { await loadAttachments(oc); await deps.timeline.load(oc); }
    },
    onClose: () => overlayStack.pop(),
  });
  overlayStack.open({
    el: overlay,
    close: () => { editor.destroy(); overlay.remove(); },
    confirm: async () => !editor.dirty() || window.confirm(S.attachments.editor.discard()),
  });
}


// storedBytes is what an upload stores: the file as it is, or, when the user
// asked for compression, a WebP of it. An Edited Image is never re-encoded:
// its raster is only one of its parts.
async function storedBytes(file: File, lossless: boolean): Promise<{ bytes: Uint8Array<ArrayBuffer>; mime: string; lossless: boolean }> {
  const type = fileType(file);
  if (!lossless && type !== XYIMAGE_MIME) return { ...await recompressToWebp(file), lossless };
  return { bytes: new Uint8Array(await file.arrayBuffer()), mime: type || "application/octet-stream", lossless: true };
}

// recompressToWebp re-encodes an image File to WebP q70. Opt-in (see
// uploadAttachment): the default is to store what the user uploaded.
async function recompressToWebp(file: File): Promise<{ bytes: Uint8Array<ArrayBuffer>; mime: string }> {
  if (!file.type.startsWith("image/")) return { bytes: new Uint8Array(await file.arrayBuffer()), mime: file.type || "application/octet-stream" };
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
  const blob = await new Promise<Blob | null>((res) => canvas.toBlob(res, "image/webp", WEBP_QUALITY));
  if (!blob) return { bytes: new Uint8Array(await file.arrayBuffer()), mime: file.type };
  return { bytes: new Uint8Array(await blob.arrayBuffer()), mime: "image/webp" };
}

// uploadAttachment encrypts `file` under the saved name and POSTs it to the open
// card. Attachments are stored AS UPLOADED by default; re-encoding to WebP q70
// (lossless=false) is opt-in, because the exports no longer need it: docx and PDF
// both re-encode each picture for the size it is drawn at (imgconv.ForExport), so
// throwing away the original on the way in bought nothing but a worse original.
// Online-only — callers must gate on xySync.isOnline(). Refreshes list+timeline.
async function uploadAttachment(file: File, keepAsIs: boolean, name: string): Promise<number | null> {
  const oc = deps.openCardId();
  if (!file || oc == null) return null;
  const msg = ui.message;
  msg.textContent = S.attachments.status.encrypting();
  const { bytes, mime, lossless } = await storedBytes(file, keepAsIs);
  const key = deps.mustDK();
  const cipher = await xyCrypto.encBytes(key, bytes);
  const fd = new FormData();
  fd.append("meta", JSON.stringify({
    filename: await xyCrypto.encField(key, name),
    mime, lossless,
    event_payload: await sealPayload(key, "attach_add", { file: name }),
  }));
  fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
  const res = await fetch(`/api/cards/${oc}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
  if (!res.ok) throw new Error((await res.text()) || S.attachments.error.uploadFailed());
  const made = (await res.json().catch(() => null)) as { id?: number } | null;
  msg.textContent = "";
  await loadAttachments(oc);
  await deps.timeline.load(oc);
  return made?.id ?? null;
}

ui.upload.addEventListener("click", async () => {
  const input = ui.file;
  const file = input.files && input.files[0];
  if (!file || deps.openCardId() == null) return;
  if (!xySync.requireOnline(S.attachments.status.offlineUpload(), ui.message)) return;
  const compress = ui.compress.checked;
  try {
    await uploadAttachment(file, !compress, file.name);
    input.value = "";
    ui.compress.checked = false;
  } catch (err) { ui.message.textContent = errMsg(err); }
});

// ---- paste-to-attach ----
// Pasting an image while a saved card is open captures it, then asks for a
// filename + whether to WebP-compress (off by default, like the file picker)
// before encrypting and uploading it as an attachment.
let pastedFile: File | null = null;
let pasteForComment = false;
const pasteModal = modal("paste");

document.addEventListener("paste", (e) => {
  // Only intercept image pastes while a persisted card is open (attachments need
  // a real card id); leave plain-text paste into the editor/comment box alone.
  if (deps.openCardId() == null || ui.cardOverlay.hidden) return;
  const items = e.clipboardData && e.clipboardData.items;
  if (!items) return;
  let file: File | null = null;
  for (const it of items) {
    if (it.kind === "file" && it.type.startsWith("image/")) { file = it.getAsFile(); break; }
  }
  if (!file) return;
  e.preventDefault();
  pastedFile = file;
  pasteForComment = document.activeElement?.id === "commentInput";
  const nameInput = ui.pasteName;
  // Clipboard images usually arrive as the generic "image.png"; offer a friendlier
  // default the user can overwrite.
  nameInput.value = (file.name && file.name !== "image.png") ? file.name : S.attachments.name.pasteDefault(extFromMime(file.type));
  ui.pasteCompress.checked = false;
  pasteModal.open({ onClose: () => { pastedFile = null; } });
  nameInput.focus();
  nameInput.setSelectionRange(0, nameStemEnd(nameInput.value));
});

ui.pasteForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!pastedFile) return;
  const msg = ui.message;
  const file = pastedFile;
  const forComment = pasteForComment;
  const compress = ui.pasteCompress.checked;
  const name = withExt(ui.pasteName.value, compress ? "webp" : extFromMime(file.type));
  pasteModal.close();
  if (!xySync.requireOnline(S.attachments.status.offlineUpload(), msg)) return;
  try {
    const id = await uploadAttachment(file, !compress, name);
    if (forComment && id != null) deps.onCommentImage?.(id);
  } catch (err) { msg.textContent = errMsg(err); }
});

// viewAttachment shows an image attachment in an in-page lightbox (zoom/pan,
// right-click → save still works since it's a real <img> on the blob URL).
// attachmentUrl already handles the offline mirror + memoizes the object URL,
// so the URL stays alive for the page's lifetime.
async function viewAttachment(att: NamedAttachment, name: string): Promise<void> {
  try {
    openLightbox(await attachmentUrl(att), name || att.name || "");
  } catch (err) { ui.message.textContent = errMsg(err); }
}

// ---- image lightbox ----------------------------------------------------------
// A single reused overlay: click the image (or wheel) to zoom toward the cursor,
// drag to pan while zoomed, click the backdrop / × / Escape to close.
interface LightboxEls { overlay: HTMLElement; img: HTMLImageElement; closeBtn: HTMLElement }
let lbEls: LightboxEls | null = null, lbScale = 1, lbTx = 0, lbTy = 0, lbBaseW = 0, lbBaseH = 0, lbDragged = false;
const LB_MIN = 1, LB_MAX = 8;
const LB_CLICK_ZOOM = 2.5; // a click on a fitted image zooms to this
const LB_WHEEL_STEP = 1.2; // one wheel notch scales by this
const LB_DRAG_SLOP_PX = 3; // movement below this is still a click, not a pan

function lbApply(): void {
  if (!lbEls) return;
  lbEls.img.style.transform = `translate(${lbTx}px, ${lbTy}px) scale(${lbScale})`;
  lbEls.img.classList.toggle("img-lb-zoomed", lbScale > 1);
}

// Rescale toward a screen point, keeping whatever is under the cursor fixed.
function lbZoomTo(next: number, cx: number, cy: number): void {
  if (!lbEls) return;
  next = Math.min(LB_MAX, Math.max(LB_MIN, next));
  const r = lbEls.img.getBoundingClientRect();
  const fx = (cx - r.left) / r.width, fy = (cy - r.top) / r.height; // point under cursor, 0..1
  const nw = lbBaseW * next, nh = lbBaseH * next;
  const cont = lbEls.overlay.getBoundingClientRect();
  const centerX = cont.left + cont.width / 2, centerY = cont.top + cont.height / 2;
  lbScale = next;
  // rect.left = centerX - nw/2 + tx  ⇒  solve tx so the fractional point stays put.
  lbTx = (cx - fx * nw) - (centerX - nw / 2);
  lbTy = (cy - fy * nh) - (centerY - nh / 2);
  if (next === 1) { lbTx = 0; lbTy = 0; }
  lbApply();
}

// Backdrop click closes; a click on the image toggles fit ↔ 2.5× toward the point.
function wireLightboxZoom(overlay: HTMLElement, img: HTMLImageElement): void {
  overlay.addEventListener("click", (e) => { if (e.target === overlay) closeLightbox(); });
  img.addEventListener("click", (e) => {
    e.stopPropagation();
    if (lbDragged) { lbDragged = false; return; } // a pan gesture isn't a zoom toggle
    lbZoomTo(lbScale > 1 ? 1 : LB_CLICK_ZOOM, e.clientX, e.clientY);
  });
  img.addEventListener("wheel", (e) => { e.preventDefault(); lbZoomTo(lbScale * (e.deltaY < 0 ? LB_WHEEL_STEP : 1 / LB_WHEEL_STEP), e.clientX, e.clientY); }, { passive: false });
  img.addEventListener("dblclick", (e) => { e.preventDefault(); lbZoomTo(1, e.clientX, e.clientY); });
}

// Drag to pan while zoomed.
function wireLightboxPan(img: HTMLImageElement): void {
  let drag: { x: number; y: number; tx: number; ty: number } | null = null;
  img.addEventListener("pointerdown", (e) => {
    if (lbScale <= 1) return;
    drag = { x: e.clientX, y: e.clientY, tx: lbTx, ty: lbTy };
    lbDragged = false;
    img.setPointerCapture(e.pointerId);
  });
  img.addEventListener("pointermove", (e) => {
    if (!drag) return;
    if (Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) > LB_DRAG_SLOP_PX) lbDragged = true;
    lbTx = drag.tx + (e.clientX - drag.x); lbTy = drag.ty + (e.clientY - drag.y); lbApply();
  });
  const endDrag = (): void => { drag = null; };
  img.addEventListener("pointerup", endDrag);
  img.addEventListener("pointercancel", endDrag);
}

function ensureLightbox(): LightboxEls {
  if (lbEls) return lbEls;
  const img = el("img", { class: "img-lb-img", alt: "" }) as HTMLImageElement;
  const closeBtn = el("button", { class: "img-lb-close", type: "button", title: S.attachments.lightbox.close(), "aria-label": S.attachments.lightbox.close() }, icon("x"));
  const overlay = el("div", { class: "img-lb", role: "dialog", "aria-label": S.attachments.lightbox.dialogLabel(), hidden: true }, img, closeBtn);
  document.body.append(overlay);
  lbEls = { overlay, img, closeBtn };
  closeBtn.addEventListener("click", closeLightbox);
  wireLightboxZoom(overlay, img);
  wireLightboxPan(img);
  return lbEls;
}

function openLightbox(url: string, name: string): void {
  const { overlay, img } = ensureLightbox();
  lbScale = 1; lbTx = 0; lbTy = 0;
  img.alt = name;
  img.onload = () => { lbBaseW = img.clientWidth; lbBaseH = img.clientHeight; lbApply(); };
  img.src = url;
  overlay.hidden = false;
  overlayStack.open({ el: overlay, close: hideLightbox });
}

function hideLightbox(): void {
  if (!lbEls || lbEls.overlay.hidden) return;
  lbEls.overlay.hidden = true;
  lbEls.img.removeAttribute("src"); // don't hold a big decoded bitmap while closed
}

function closeLightbox(): void { overlayStack.pop(); }

// Inline (img …) preview images open in the same lightbox. Delegated so it
// covers every preview surface (card, list, import) without wiring each <img>.
document.addEventListener("click", (e) => {
  const img = e.target instanceof Element ? e.target.closest<HTMLImageElement>("img.pv-img") : null;
  if (img && img.getAttribute("src")) openLightbox(img.src, img.alt || "");
});

async function download(att: NamedAttachment, name: string): Promise<void> {
  try {
    // Prefer the network; fall back to a previously-cached copy when offline.
    let cipher: Uint8Array<ArrayBuffer>;
    try {
      const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
      if (!res.ok) throw new Error(S.attachments.error.downloadFailed());
      cipher = new Uint8Array(await res.arrayBuffer());
      try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher }); } catch (_) {}
    } catch (netErr) {
      const cached = await xySync.getAttachment(att.id);
      if (!cached) throw new Error(S.attachments.error.offlineUnavailable());
      cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
    }
    const plain = await xyCrypto.decBytes(deps.mustDK(), cipher);
    const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
    const a = el("a", { href: url, download: name });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), DOWNLOAD_URL_TTL_MS);
  } catch (err) { ui.message.textContent = errMsg(err); }
}

async function removeAttachment(att: NamedAttachment, name: string): Promise<void> {
  if (!confirm(S.attachments.confirm.remove(name))) return;
  if (!xySync.requireOnline(S.attachments.status.offlineDelete(), ui.message)) return;
  try {
    const ev = await sealPayload(deps.mustDK(), "attach_remove", { file: name });
    await jdelete(`/api/attachments/${att.id}?event_payload=${encodeURIComponent(ev)}`);
    const oc = deps.openCardId();
    if (oc != null) {
      await loadAttachments(oc);
      await deps.timeline.load(oc);
    }
  } catch (err) { ui.message.textContent = errMsg(err); }
}


  // appendImages resolves each wanted image to its decrypted bytes and appends it
  // to fd as an "img" part — the export pack's and the handouts' upload. The
  // cards' attachment lists are fetched in parallel, and the matched image
  // bodies too. Returns the set of resolved names so the caller can prompt about
  // any still missing.
  async function appendImages(fd: FormData, cards: ReadonlyArray<{ id: number }>, wanted: Set<string>): Promise<Set<string>> {
    const found = new Set<string>();
    if (!wanted.size) return found;
    const lists = await Promise.all(cards.map((c) => cardAttachments(c.id)));
    const targets = gatherTargets(lists, wanted);
    await Promise.all([...targets].map(async ([name, att]) => {
      try {
        const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
        if (!res.ok) return;
        const plain = await xyCrypto.decBytes(deps.mustDK(), new Uint8Array(await res.arrayBuffer()));
        fd.append("img", new Blob([plain], { type: att.mime }), name);
        found.add(name);
      } catch (_) {}
    }));
    return found;
  }

  return {
    cardAttachments,
    attachmentUrl,
    resolveImages,
    appendImages,
    imageBlob,
    load: loadAttachments,
    imageNames: (): string[] => cardImageNames,
    clearImageNames: (): void => { cardImageNames = []; },
    upload: uploadAttachment,
    download,
    openLightbox,
  };
}

export type Attachments = ReturnType<typeof create>;
