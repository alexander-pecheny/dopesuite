// attachments.ts — the board's attachment kernel: the decrypted list/URL
// caches (LRU object URLs keyed id:rev), upload with opt-in WebP recompression,
// replace-in-place, delete, paste-to-attach, download with offline fallback,
// and the image lightbox. A create(deps) factory like the board's other
// kernels; board.ts supplies the popup menu, the open-card accessor and the
// timeline surface, and wires the result into carddetail's seams.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import type { DataKey } from "./crypto.js";
import type { MenuItem } from "./timeline.js";

const { fetchJSON, jpatch, jdelete, el } = xyApp;
const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

function byId<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`missing #${id}`);
  return node as T;
}

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
  filename_enc: string;
  mime: string;
  size: number;
  rev?: number;
  lossless?: boolean;
  is_excerpt?: boolean;
  [key: string]: unknown;
}
export type NamedAttachment = Attachment & { name: string };

export interface AttachmentsDeps {
  mustDK(): DataKey;
  openCardId(): number | null;
  popupMenu(anchor: HTMLElement, items: MenuItem[]): void;
  timeline: {
    load(cardId: number): Promise<void>;
    setAttachments(list: NamedAttachment[]): void;
  };
}

export function create(deps: AttachmentsDeps) {

const attListCache = new Map<number, NamedAttachment[]>(); // cardId → [{ ...att, name }]
const attUrlCache = new Map<string, string>();  // id:rev → decrypted object URL
const ATT_URL_CACHE_MAX = 64;

// cardAttachments lists one card's attachments with their filenames decrypted.
async function cardAttachments(cardId: number, refresh = false): Promise<NamedAttachment[]> {
  if (refresh) attListCache.delete(cardId);
  const hit = attListCache.get(cardId);
  if (hit) return hit;
  let atts: Attachment[];
  try { atts = (await fetchJSON(`/api/cards/${cardId}/attachments`)) as Attachment[]; } catch (_) { return []; }
  const out = await Promise.all(atts.map(async (att) => {
    let name = "";
    try { name = await xyCrypto.decField(deps.mustDK(), att.filename_enc); } catch (_) {}
    return { ...att, name };
  }));
  attListCache.set(cardId, out);
  return out;
}

// attachmentUrl decrypts one attachment into an object URL, reading its
// ciphertext through the offline IndexedDB mirror (so a reload doesn't
// re-download) and memoizing the result.
async function attachmentUrl(att: Attachment): Promise<string> {
  const rev = att.rev || 0;
  const key = `${att.id}:${rev}`;
  const hit = attUrlCache.get(key);
  if (hit) { attUrlCache.delete(key); attUrlCache.set(key, hit); return hit; } // LRU touch
  let cipher: Uint8Array<ArrayBuffer>;
  // The offline mirror is keyed by id alone; a stale rev there means the bytes
  // are the ones a replace superseded, so refetch instead of trusting them.
  const cached = await xySync.getAttachment(att.id).catch(() => null);
  if (cached && (cached.rev || 0) === rev) {
    cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
  } else {
    const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error("не удалось скачать вложение");
    cipher = new Uint8Array(await res.arrayBuffer());
    try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher, rev }); } catch (_) {}
  }
  const plain = await xyCrypto.decBytes(deps.mustDK(), cipher);
  const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
  attUrlCache.set(key, url);
  for (const stale of [...attUrlCache.keys()].slice(0, attUrlCache.size - ATT_URL_CACHE_MAX)) {
    URL.revokeObjectURL(attUrlCache.get(stale) || "");
    attUrlCache.delete(stale);
  }
  return url;
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
  const targets = new Map<string, NamedAttachment>(); // name → attachment (first match wins, in card order)
  for (const atts of lists) {
    for (const att of atts) {
      if (att.name && wanted.has(att.name) && !targets.has(att.name)) targets.set(att.name, att);
    }
  }
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
// attachments — the choices offered by the handout image picker (Поля view).
let cardImageNames: string[] = [];

async function loadAttachments(cardId: number): Promise<void> {
  const box = byId("attachments");
  cardImageNames = [];
  // Always refetch: this runs on card open and after every upload/delete, so it
  // doubles as the invalidation point for the preview's attachment-list cache.
  const list = await cardAttachments(cardId, true);
  // Built off-DOM and swapped in one go — see loadTimeline on the scroll jump.
  const frag = document.createDocumentFragment();
  for (const att of list) {
    const name = att.name || "файл";
    const isImage = (att.mime || "").startsWith("image/");
    if (isImage) cardImageNames.push(name);
    // Images open in the lightbox (save via right-click there); other files download.
    const row = el("div", { class: "attach-row" },
      el("button", { class: "attach-name", type: "button", text: `📎 ${name}`, onclick: () => { void (isImage ? viewAttachment(att, name) : download(att, name)); } }),
      att.is_excerpt ? el("span", { class: "tl-badge", text: "выписка" }) : null,
      el("span", { class: "attach-size", text: humanSize(att.size) }),
      el("button", {
        class: "attach-del", type: "button", title: "Действия с вложением", text: "⋯", "aria-haspopup": "true",
        onclick: (e: Event) => attachMenu(e.currentTarget as HTMLElement, att, name),
      }),
    );
    frag.append(row);
  }
  box.replaceChildren(frag);
  deps.timeline.setAttachments(list);
}

function attachMenu(anchor: HTMLElement, att: NamedAttachment, name: string): void {
  deps.popupMenu(anchor, [
    { label: "🔄 Заменить", onClick: () => pickReplacement(att, name) },
    { label: "🗑 Удалить", onClick: () => { void removeAttachment(att, name); } },
    {
      label: "Выписка", checked: !!att.is_excerpt,
      onClick: () => { void attachAction(async () => {
        await jpatch(`/api/attachments/${att.id}`, { is_excerpt: !att.is_excerpt });
      }); },
    },
  ]);
}

async function attachAction(fn: () => Promise<unknown>): Promise<void> {
  const msg = byId("cardMessage");
  if (!xySync.requireOnline("Правка вложений доступна только онлайн.", msg)) return;
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

// pickReplacement re-shoots an attachment in place: the id (and so its выписка
// flag, its position, and every card that names the file) survives, only the
// bytes change. The compress choice is inherited from what the original stored.
function pickReplacement(att: NamedAttachment, name: string): void {
  const picker = el("input", { type: "file" }) as HTMLInputElement;
  picker.addEventListener("change", () => {
    const file = picker.files && picker.files[0];
    if (!file) return;
    void attachAction(async () => {
      const msg = byId("cardMessage");
      msg.textContent = "Шифрование…";
      const lossless = !!att.lossless;
      let bytes: Uint8Array<ArrayBuffer>, mime: string;
      if (lossless) { bytes = new Uint8Array(await file.arrayBuffer()); mime = file.type || "application/octet-stream"; }
      else ({ bytes, mime } = await recompressToWebp(file));
      const key = deps.mustDK();
      const fd = new FormData();
      fd.append("meta", JSON.stringify({
        filename_enc: await xyCrypto.encField(key, name),
        mime, lossless,
        event_payload_enc: await xyCrypto.encField(key, JSON.stringify({ file: name })),
      }));
      fd.append("blob", new Blob([await xyCrypto.encBytes(key, bytes)], { type: "application/octet-stream" }), "blob");
      const res = await fetch(`/api/attachments/${att.id}`, { method: "PUT", credentials: "same-origin", body: fd });
      if (!res.ok) throw new Error((await res.text()) || "ошибка замены");
    });
  });
  picker.click();
}

function humanSize(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
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
  const blob = await new Promise<Blob | null>((res) => canvas.toBlob(res, "image/webp", 0.7));
  if (!blob) return { bytes: new Uint8Array(await file.arrayBuffer()), mime: file.type };
  return { bytes: new Uint8Array(await blob.arrayBuffer()), mime: "image/webp" };
}

// uploadAttachment encrypts `file` under the saved name and POSTs it to the open
// card. Attachments are stored AS UPLOADED by default; re-encoding to WebP q70
// (lossless=false) is opt-in, because the exports no longer need it: docx and PDF
// both re-encode each picture for the size it is drawn at (imgconv.ForExport), so
// throwing away the original on the way in bought nothing but a worse original.
// Online-only — callers must gate on xySync.isOnline(). Refreshes list+timeline.
async function uploadAttachment(file: File, lossless: boolean, name: string): Promise<void> {
  const oc = deps.openCardId();
  if (!file || oc == null) return;
  const msg = byId("cardMessage");
  msg.textContent = "Шифрование…";
  let bytes: Uint8Array<ArrayBuffer>, mime: string;
  if (lossless) { bytes = new Uint8Array(await file.arrayBuffer()); mime = file.type || "application/octet-stream"; }
  else ({ bytes, mime } = await recompressToWebp(file));
  const key = deps.mustDK();
  const cipher = await xyCrypto.encBytes(key, bytes);
  const fd = new FormData();
  fd.append("meta", JSON.stringify({
    filename_enc: await xyCrypto.encField(key, name),
    mime, lossless,
    event_payload_enc: await xyCrypto.encField(key, JSON.stringify({ file: name })),
  }));
  fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
  const res = await fetch(`/api/cards/${oc}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
  if (!res.ok) throw new Error((await res.text()) || "ошибка загрузки");
  msg.textContent = "";
  await loadAttachments(oc);
  await deps.timeline.load(oc);
}

byId("attachUpload").addEventListener("click", async () => {
  const input = byId<HTMLInputElement>("attachFile");
  const file = input.files && input.files[0];
  if (!file || deps.openCardId() == null) return;
  if (!xySync.requireOnline("Загрузка вложений доступна только онлайн.", byId("cardMessage"))) return;
  const compress = byId<HTMLInputElement>("attachCompress").checked;
  try {
    await uploadAttachment(file, !compress, file.name);
    input.value = "";
    byId<HTMLInputElement>("attachCompress").checked = false;
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
});

// ---- paste-to-attach ----
// Pasting an image while a saved card is open captures it, then asks for a
// filename + whether to WebP-compress (off by default, like the file picker)
// before encrypting and uploading it as an attachment.
let pastedFile: File | null = null;
const pasteOverlay = byId("pasteOverlay");
const cardOverlay = byId("cardOverlay");

function extFromMime(m: string): string {
  const map: Record<string, string> = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/bmp": "bmp", "image/svg+xml": "svg" };
  if (map[m]) return map[m];
  const sub = (m || "").split("/")[1];
  return sub ? sub.replace(/[^a-z0-9]+/gi, "") : "png";
}

// withExt drops any extension the user typed and forces the one that matches the
// stored format (webp when compressing, else the source image's type), so the
// filename never claims a type the bytes aren't.
function withExt(name: string, ext: string): string {
  const base = name.replace(/\.[^./\\]+$/, "").trim();
  return `${base || "вставка"}.${ext}`;
}

function closePasteModal(): void { pasteOverlay.hidden = true; pastedFile = null; }

document.addEventListener("paste", (e) => {
  // Only intercept image pastes while a persisted card is open (attachments need
  // a real card id); leave plain-text paste into the editor/comment box alone.
  if (deps.openCardId() == null || cardOverlay.hidden) return;
  const items = e.clipboardData && e.clipboardData.items;
  if (!items) return;
  let file: File | null = null;
  for (const it of items) {
    if (it.kind === "file" && it.type.startsWith("image/")) { file = it.getAsFile(); break; }
  }
  if (!file) return;
  e.preventDefault();
  pastedFile = file;
  const nameInput = byId<HTMLInputElement>("pasteName");
  // Clipboard images usually arrive as the generic "image.png"; offer a friendlier
  // default the user can overwrite.
  nameInput.value = (file.name && file.name !== "image.png") ? file.name : `вставка.${extFromMime(file.type)}`;
  byId<HTMLInputElement>("pasteCompress").checked = false;
  pasteOverlay.hidden = false;
  nameInput.focus();
  nameInput.select();
});

byId("pasteForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!pastedFile) return;
  const msg = byId("cardMessage");
  const file = pastedFile;
  const compress = byId<HTMLInputElement>("pasteCompress").checked;
  const name = withExt(byId<HTMLInputElement>("pasteName").value, compress ? "webp" : extFromMime(file.type));
  closePasteModal();
  if (!xySync.requireOnline("Загрузка вложений доступна только онлайн.", msg)) return;
  try {
    await uploadAttachment(file, !compress, name);
  } catch (err) { msg.textContent = errMsg(err); }
});

byId("pasteCancel").addEventListener("click", closePasteModal);
pasteOverlay.addEventListener("pointerdown", (e) => { if (e.target === pasteOverlay) closePasteModal(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !pasteOverlay.hidden) closePasteModal(); });

// viewAttachment shows an image attachment in an in-page lightbox (zoom/pan,
// right-click → save still works since it's a real <img> on the blob URL).
// attachmentUrl already handles the offline mirror + memoizes the object URL,
// so the URL stays alive for the page's lifetime.
async function viewAttachment(att: NamedAttachment, name: string): Promise<void> {
  try {
    openLightbox(await attachmentUrl(att), name || att.name || "");
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
}

// ---- image lightbox ----------------------------------------------------------
// A single reused overlay: click the image (or wheel) to zoom toward the cursor,
// drag to pan while zoomed, click the backdrop / × / Escape to close.
interface LightboxEls { overlay: HTMLElement; img: HTMLImageElement; closeBtn: HTMLElement }
let lbEls: LightboxEls | null = null, lbScale = 1, lbTx = 0, lbTy = 0, lbBaseW = 0, lbBaseH = 0, lbDragged = false;
const LB_MIN = 1, LB_MAX = 8;

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

function ensureLightbox(): LightboxEls {
  if (lbEls) return lbEls;
  const img = el("img", { class: "img-lb-img", alt: "" }) as HTMLImageElement;
  const closeBtn = el("button", { class: "img-lb-close", type: "button", title: "Закрыть", text: "×" });
  const overlay = el("div", { class: "img-lb", role: "dialog", "aria-label": "Просмотр изображения", hidden: true }, img, closeBtn);
  document.body.append(overlay);
  lbEls = { overlay, img, closeBtn };

  closeBtn.addEventListener("click", closeLightbox);
  // Backdrop click closes; a click on the image toggles fit ↔ 2× toward the point.
  overlay.addEventListener("click", (e) => { if (e.target === overlay) closeLightbox(); });
  img.addEventListener("click", (e) => {
    e.stopPropagation();
    if (lbDragged) { lbDragged = false; return; } // a pan gesture isn't a zoom toggle
    lbZoomTo(lbScale > 1 ? 1 : 2.5, e.clientX, e.clientY);
  });
  img.addEventListener("wheel", (e) => { e.preventDefault(); lbZoomTo(lbScale * (e.deltaY < 0 ? 1.2 : 1 / 1.2), e.clientX, e.clientY); }, { passive: false });
  img.addEventListener("dblclick", (e) => { e.preventDefault(); lbZoomTo(1, e.clientX, e.clientY); });

  // Drag to pan while zoomed.
  let drag: { x: number; y: number; tx: number; ty: number } | null = null;
  img.addEventListener("pointerdown", (e) => {
    if (lbScale <= 1) return;
    drag = { x: e.clientX, y: e.clientY, tx: lbTx, ty: lbTy };
    lbDragged = false;
    img.setPointerCapture(e.pointerId);
  });
  img.addEventListener("pointermove", (e) => {
    if (!drag) return;
    if (Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) > 3) lbDragged = true;
    lbTx = drag.tx + (e.clientX - drag.x); lbTy = drag.ty + (e.clientY - drag.y); lbApply();
  });
  const endDrag = (): void => { drag = null; };
  img.addEventListener("pointerup", endDrag);
  img.addEventListener("pointercancel", endDrag);
  return lbEls;
}

function openLightbox(url: string, name: string): void {
  const { overlay, img } = ensureLightbox();
  lbScale = 1; lbTx = 0; lbTy = 0;
  img.alt = name;
  img.onload = () => { lbBaseW = img.clientWidth; lbBaseH = img.clientHeight; lbApply(); };
  img.src = url;
  overlay.hidden = false;
  document.addEventListener("keydown", lbOnKey);
}

function closeLightbox(): void {
  if (!lbEls || lbEls.overlay.hidden) return;
  lbEls.overlay.hidden = true;
  lbEls.img.removeAttribute("src"); // don't hold a big decoded bitmap while closed
  document.removeEventListener("keydown", lbOnKey);
}

function lbOnKey(e: KeyboardEvent): void { if (e.key === "Escape") { e.preventDefault(); closeLightbox(); } }

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
      if (!res.ok) throw new Error("не удалось скачать");
      cipher = new Uint8Array(await res.arrayBuffer());
      try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher }); } catch (_) {}
    } catch (netErr) {
      const cached = await xySync.getAttachment(att.id);
      if (!cached) throw new Error("вложение недоступно офлайн");
      cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
    }
    const plain = await xyCrypto.decBytes(deps.mustDK(), cipher);
    const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
    const a = el("a", { href: url, download: name });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
}

async function removeAttachment(att: NamedAttachment, name: string): Promise<void> {
  if (!confirm(`Удалить вложение «${name}»?`)) return;
  if (!xySync.requireOnline("Удаление вложений доступно только онлайн.", byId("cardMessage"))) return;
  try {
    const ev = await xyCrypto.encField(deps.mustDK(), JSON.stringify({ file: name }));
    await jdelete(`/api/attachments/${att.id}?event_payload_enc=${encodeURIComponent(ev)}`);
    const oc = deps.openCardId();
    if (oc != null) {
      await loadAttachments(oc);
      await deps.timeline.load(oc);
    }
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
}


  return {
    cardAttachments,
    attachmentUrl,
    resolveImages,
    load: loadAttachments,
    imageNames: (): string[] => cardImageNames,
    clearImageNames: (): void => { cardImageNames = []; },
    upload: uploadAttachment,
    download,
    openLightbox,
  };
}

export type Attachments = ReturnType<typeof create>;
