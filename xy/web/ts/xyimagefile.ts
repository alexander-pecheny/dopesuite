// xyimagefile.ts — the .xyimage container in the browser (ADR-0024): the
// Recipe, the pictures, the vector overlays and the raster rendition, framed
// like PNG. chgksuite/xyimage is the other implementation; both read
// chgksuite/xyimage/testdata/fixture.*, so the two cannot drift.
//
//   "XYIMG" 0x00 | u8 format version
//   chunks: [4-byte ASCII type][u32 big-endian length][bytes]
//     RCPE the Recipe (JSON, first) · IMAG a picture · OVER an overlay · RAST the raster
//
// An unknown chunk whose type starts with an uppercase letter is refused as
// made by a newer xy; a lowercase one is skipped.

const MAGIC = [0x58, 0x59, 0x49, 0x4d, 0x47, 0x00]; // "XYIMG\0"
export const XYIMAGE_VERSION = 1;
// An Edited Image is stored under this mime, so everything that asks "is it a
// picture?" says yes; what it is, is told by its bytes.
export const XYIMAGE_MIME = "image/x-xyimage";
export const XYIMAGE_EXT = "xyimage";
// How many leading bytes isXyimage needs to see.
export const XYIMAGE_SNIFF_LEN = MAGIC.length;
const TYPE_LEN = 4;
const LENGTH_LEN = 4;
const HEADER_LEN = MAGIC.length + 1;
const UPPER_A = 0x41;
const UPPER_Z = 0x5a;

export interface XyimageParts {
  recipe: string; // the Recipe's JSON, as written
  images: Uint8Array[];
  overlays: string[];
  raster: Uint8Array | null;
}

export class XyimageError extends Error {
  constructor(readonly reason: "not" | "newer" | "corrupt") { super(`xyimage: ${reason}`); }
}

// isXyimage reports whether bytes start like an Edited Image. The name is
// never asked: a renamed file is still one, and a file merely called
// .xyimage is not.
export function isXyimage(b: Uint8Array): boolean {
  return b.length >= MAGIC.length && MAGIC.every((m, i) => b[i] === m);
}

// fileType is a picked file's mime. A browser knows nothing of .xyimage and
// reports it as "" (or as octet-stream), which would leave an Edited Image
// picked from disk out of everything that takes pictures.
export function fileType(f: { name: string; type: string }): string {
  return f.name.toLowerCase().endsWith("." + XYIMAGE_EXT) ? XYIMAGE_MIME : f.type;
}

const enc = new TextEncoder();
const dec = new TextDecoder();

function typeOf(b: Uint8Array, at: number): string {
  return String.fromCharCode(...b.subarray(at, at + TYPE_LEN));
}

export function parseXyimage(b: Uint8Array): XyimageParts {
  if (!isXyimage(b)) throw new XyimageError("not");
  if (b.length < HEADER_LEN) throw new XyimageError("corrupt");
  if (b[MAGIC.length] !== XYIMAGE_VERSION) throw new XyimageError("newer");
  const view = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const out: XyimageParts = { recipe: "", images: [], overlays: [], raster: null };
  let seenRecipe = false;
  for (let at = HEADER_LEN; at < b.length;) {
    if (at + TYPE_LEN + LENGTH_LEN > b.length) throw new XyimageError("corrupt");
    const t = typeOf(b, at);
    const n = view.getUint32(at + TYPE_LEN);
    const start = at + TYPE_LEN + LENGTH_LEN;
    if (start + n > b.length) throw new XyimageError("corrupt");
    const data = b.slice(start, start + n);
    if (!seenRecipe && t !== "RCPE") throw new XyimageError("corrupt");
    take(out, t, data);
    seenRecipe = true;
    at = start + n;
  }
  return out;
}

const TAKERS: Record<string, (out: XyimageParts, data: Uint8Array) => void> = {
  RCPE: (out, data) => { out.recipe = dec.decode(data); },
  IMAG: (out, data) => { out.images.push(data); },
  OVER: (out, data) => { out.overlays.push(dec.decode(data)); },
  RAST: (out, data) => { out.raster = data; },
};

function take(out: XyimageParts, t: string, data: Uint8Array): void {
  const taker = TAKERS[t];
  if (taker) { taker(out, data); return; }
  if (t.charCodeAt(0) >= UPPER_A && t.charCodeAt(0) <= UPPER_Z) throw new XyimageError("newer");
}

export function encodeXyimage(p: XyimageParts): Uint8Array {
  const chunks: [string, Uint8Array][] = [["RCPE", enc.encode(p.recipe)]];
  for (const im of p.images) chunks.push(["IMAG", im]);
  for (const ov of p.overlays) chunks.push(["OVER", enc.encode(ov)]);
  if (p.raster) chunks.push(["RAST", p.raster]);
  const size = chunks.reduce((a, [, d]) => a + TYPE_LEN + LENGTH_LEN + d.length, HEADER_LEN);
  const out = new Uint8Array(size);
  const view = new DataView(out.buffer);
  out.set(MAGIC, 0);
  out[MAGIC.length] = XYIMAGE_VERSION;
  let at = HEADER_LEN;
  for (const [t, d] of chunks) {
    for (let i = 0; i < TYPE_LEN; i++) out[at + i] = t.charCodeAt(i);
    view.setUint32(at + TYPE_LEN, d.length);
    out.set(d, at + TYPE_LEN + LENGTH_LEN);
    at += TYPE_LEN + LENGTH_LEN + d.length;
  }
  return out;
}

// fontZipsOf is the hashes of the Font Family zips an Edited Image's text is
// set in (its Recipe's bake.fonts, ADR-0025), which an export must send along.
export function fontZipsOf(b: Uint8Array): string[] {
  const bake = (JSON.parse(parseXyimage(b).recipe) as { bake?: { fonts?: { zip?: unknown }[] } }).bake;
  return [...new Set((bake?.fonts ?? []).map((f) => f.zip).filter((z): z is string => typeof z === "string"))];
}

// rasterOf is the raster rendition of an Edited Image, which is what every
// place that merely shows a picture (the board, the card preview, the lightbox) draws.
export function rasterOf(b: Uint8Array): Uint8Array | null {
  return parseXyimage(b).raster;
}
