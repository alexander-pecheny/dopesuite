// xyimagefonts.ts — the uploaded fonts an Edited Image's text is set in
// (ADR-0025). A Font Family is one zip of TTF or OTF Faces, named by the
// SHA-256 of its bytes; a text box names the Face it is set in by that hash
// and the Face's path inside the zip.
//
// Every Face goes by a family name of our own making (faceFamily), in the
// browser's FontFace set, in the overlay SVG and in typst alike, so an upload
// that calls itself Noto Sans, or two uploads that call themselves the same,
// never stand in for each other.

import { zipRead, zipWrite, type ZipEntry } from "./zip.js";

export interface FamilyFace {
  path: string; // the file inside the zip
  style: string; // what the font calls this Face: «Bold Italic»
  weight: number; // for ordering the Faces in a picker
  italic: boolean;
}

export interface FontFamily {
  hash: string; // SHA-256 of the zip, hex
  name: string; // what the font calls its family: «Inter»
  faces: FamilyFace[];
}

// FaceRef is a text box's font, as its Recipe keeps it.
export interface FaceRef {
  zip: string;
  face: string;
  family: string; // faceFamily(zip, face)
  name: string; // the Family's name, for the picker and for messages
  style: string;
}

// What the board offers the editor: its live Families, their bytes, and,
// where the person may add one, an upload. The lab page holds them in memory.
export interface FontSource {
  families(): FontFamily[];
  zip(hash: string): Promise<Uint8Array<ArrayBuffer>>;
  upload?(files: File[]): Promise<FontFamily>;
}

export class FontRefused extends Error {
  constructor(readonly reason: "woff" | "variable" | "collection" | "notfont" | "empty" | "toobig", readonly file: string) {
    super(`font ${file}: ${reason}`);
  }
}

// ---- reading a Face ----

const TAG_TRUE = 0x74727565; // "true", an old Mac TrueType
const TAG_OTTO = 0x4f54544f; // "OTTO", CFF outlines
const TAG_TTCF = 0x74746366; // "ttcf", a collection
const TAG_WOFF = 0x774f4646; // "wOFF"
const TAG_WOF2 = 0x774f4632; // "wOF2"
const SFNT_V1 = 0x00010000;
// The offsets of the sfnt tables this reads (OpenType spec): the table
// directory, a name table and its records, and the OS/2 fields.
const NUM_TABLES_AT = 4;
const TABLE_DIR_AT = 12;
const TABLE_RECORD = 16;
const TABLE_OFFSET_AT = 8;
const TABLE_LENGTH_AT = 12;
const NAME_COUNT_AT = 2;
const NAME_STRINGS_AT = 4;
const NAME_RECORD = 12;
const NAME_RECORDS_AT = 6;
const RECORD_LANG_AT = 4;
const RECORD_ID_AT = 6;
const RECORD_LENGTH_AT = 8;
const RECORD_OFFSET_AT = 10;
const UTF16_UNIT = 2;
const TAG_LEN = 4;
const BYTE_BITS = 8;
const PLATFORM_MAC = 1;
const PLATFORM_WIN = 3;
const LANG_EN_US = 0x409;
const NAME_FAMILY = 1;
const NAME_STYLE = 2;
const NAME_TYPO_FAMILY = 16;
const NAME_TYPO_STYLE = 17;
const OS2_WEIGHT_AT = 4;
const OS2_SELECTION_AT = 62;
const ITALIC_BIT = 1;
const REGULAR_WEIGHT = 400;
const MIB = 1024 * 1024;
const MAX_FACE_MIB = 20; // ADR-0025: about 20 MB a Face
export const MAX_FACE_BYTES = MAX_FACE_MIB * MIB;
// How well a name record suits us: English Windows, other Windows, Mac.
const RANK_WIN_EN = 3;
const RANK_WIN = 2;
const RANK_MAC = 1;

interface FaceInfo { family: string; style: string; weight: number; italic: boolean }

function tables(v: DataView): Map<string, { at: number; len: number }> {
  const out = new Map<string, { at: number; len: number }>();
  const count = v.getUint16(NUM_TABLES_AT);
  for (let i = 0; i < count; i++) {
    const r = TABLE_DIR_AT + i * TABLE_RECORD;
    if (r + TABLE_RECORD > v.byteLength) break;
    const tag = String.fromCharCode(...new Uint8Array(v.buffer, v.byteOffset + r, TAG_LEN));
    out.set(tag, { at: v.getUint32(r + TABLE_OFFSET_AT), len: v.getUint32(r + TABLE_LENGTH_AT) });
  }
  return out;
}

function nameString(v: DataView, platform: number, at: number, len: number): string {
  const bytes = new Uint8Array(v.buffer, v.byteOffset + at, Math.max(0, Math.min(len, v.byteLength - at)));
  if (platform !== PLATFORM_WIN) return String.fromCharCode(...bytes);
  let s = "";
  for (let i = 0; i + 1 < bytes.length; i += UTF16_UNIT) s += String.fromCharCode((bytes[i] << BYTE_BITS) | bytes[i + 1]);
  return s;
}

// names reads the name table: for each name id, the English Windows string
// where there is one, else any Windows one, else the Mac one.
function recordRank(platform: number, lang: number): number {
  if (platform === PLATFORM_WIN) return lang === LANG_EN_US ? RANK_WIN_EN : RANK_WIN;
  return platform === PLATFORM_MAC ? RANK_MAC : 0;
}

function names(v: DataView, t: { at: number; len: number }): Map<number, string> {
  const count = v.getUint16(t.at + NAME_COUNT_AT);
  const strings = t.at + v.getUint16(t.at + NAME_STRINGS_AT);
  const rank = new Map<number, number>();
  const out = new Map<number, string>();
  for (let i = 0; i < count; i++) {
    const r = t.at + NAME_RECORDS_AT + i * NAME_RECORD;
    if (r + NAME_RECORD > v.byteLength) break;
    const platform = v.getUint16(r);
    const id = v.getUint16(r + RECORD_ID_AT);
    const score = recordRank(platform, v.getUint16(r + RECORD_LANG_AT));
    if (!score || score <= (rank.get(id) ?? 0)) continue;
    rank.set(id, score);
    out.set(id, nameString(v, platform, strings + v.getUint16(r + RECORD_OFFSET_AT), v.getUint16(r + RECORD_LENGTH_AT)));
  }
  return out;
}

// readFace reads what a font file says of itself, refusing what typst cannot
// set: WOFF (typst reads neither), collections, and variable fonts (typst
// sets only their default instance).
export function readFace(bytes: Uint8Array, file: string): FaceInfo {
  if (bytes.length < TABLE_DIR_AT) throw new FontRefused("notfont", file);
  const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const tag = v.getUint32(0);
  if (tag === TAG_WOFF || tag === TAG_WOF2) throw new FontRefused("woff", file);
  if (tag === TAG_TTCF) throw new FontRefused("collection", file);
  if (tag !== SFNT_V1 && tag !== TAG_OTTO && tag !== TAG_TRUE) throw new FontRefused("notfont", file);
  const t = tables(v);
  if (t.has("fvar")) throw new FontRefused("variable", file);
  return describe(v, t, file);
}

function describe(v: DataView, t: Map<string, { at: number; len: number }>, file: string): FaceInfo {
  const name = t.get("name");
  const n = name ? names(v, name) : new Map<number, string>();
  const os2 = t.get("OS/2");
  const weight = os2 ? v.getUint16(os2.at + OS2_WEIGHT_AT) : REGULAR_WEIGHT;
  const italic = os2 ? (v.getUint16(os2.at + OS2_SELECTION_AT) & ITALIC_BIT) !== 0 : false;
  const stem = file.replace(/\.[^.]+$/, "");
  return {
    family: (n.get(NAME_TYPO_FAMILY) || n.get(NAME_FAMILY) || stem).trim(),
    style: (n.get(NAME_TYPO_STYLE) || n.get(NAME_STYLE) || "Regular").trim(),
    weight, italic,
  };
}

// ---- building a Family ----

const FONT_FILE = /\.(ttf|otf)$/i;
const ZIP_FILE = /\.zip$/i;

function baseName(path: string): string { return path.slice(path.lastIndexOf("/") + 1); }

async function sha256Hex(b: Uint8Array<ArrayBuffer>): Promise<string> {
  const d = new Uint8Array(await crypto.subtle.digest("SHA-256", b));
  return [...d].map((x) => x.toString(HEX).padStart(BYTE_HEX, "0")).join("");
}

// faceFiles is every TTF or OTF the person picked, whether loose or inside a
// zip, by its bare name. Anything else in a zip (a licence, a readme, a woff
// for the web) is left behind.
async function faceFiles(files: File[]): Promise<ZipEntry[]> {
  const out: ZipEntry[] = [];
  for (const f of files) {
    const data = new Uint8Array(await f.arrayBuffer());
    if (ZIP_FILE.test(f.name)) {
      for (const e of await zipRead(data)) if (FONT_FILE.test(e.name) && !baseName(e.name).startsWith(".")) out.push({ name: baseName(e.name), data: e.data });
    } else out.push({ name: baseName(f.name), data });
  }
  return out;
}

// buildFamily turns what was picked (font files, or a zip of them) into a
// Family: a zip of our own making, its Faces stored uncompressed in name
// order, so the same Faces always make the same zip and the same hash.
export async function buildFamily(files: File[]): Promise<{ family: FontFamily; zip: Uint8Array<ArrayBuffer> }> {
  const entries = (await faceFiles(files)).sort((a, b) => Number(a.name > b.name) - Number(a.name < b.name));
  const seen = new Set<string>();
  const faces: FamilyFace[] = [];
  const kept: ZipEntry[] = [];
  let name = "";
  for (const e of entries) {
    if (seen.has(e.name)) continue;
    seen.add(e.name);
    if (e.data.length > MAX_FACE_BYTES) throw new FontRefused("toobig", e.name);
    const info = readFace(e.data, e.name);
    name ||= info.family;
    faces.push({ path: e.name, style: info.style, weight: info.weight, italic: info.italic });
    kept.push(e);
  }
  if (!faces.length) throw new FontRefused("empty", files[0]?.name ?? "");
  const zip = await zipWrite(kept, () => false);
  return { family: { hash: await sha256Hex(zip), name, faces: sortFaces(faces) }, zip };
}

// readFamily describes a Family zip as it stands, for one that arrives already
// zipped (in a .4s export's fonts/ folder): its hash is of these very bytes,
// which the pictures set in it name.
export async function readFamily(zip: Uint8Array<ArrayBuffer>): Promise<FontFamily> {
  const faces: FamilyFace[] = [];
  let name = "";
  for (const e of await zipRead(zip)) {
    if (!FONT_FILE.test(e.name)) continue;
    const info = readFace(e.data, e.name);
    name ||= info.family;
    faces.push({ path: e.name, style: info.style, weight: info.weight, italic: info.italic });
  }
  if (!faces.length) throw new FontRefused("empty", "");
  return { hash: await sha256Hex(zip), name, faces: sortFaces(faces) };
}

export function sortFaces(faces: FamilyFace[]): FamilyFace[] {
  return [...faces].sort((a, b) => Number(a.italic) - Number(b.italic) || a.weight - b.weight || a.style.localeCompare(b.style));
}

// ---- naming and loading a Face ----

const FNV_OFFSET = 0x811c9dc5;
const FNV_PRIME = 0x01000193;
const HASH_PREFIX = 16;
const HEX = 16;
const U32_HEX = 8;
const BYTE_HEX = 2;

function fnv1a(s: string): string {
  let h = FNV_OFFSET;
  for (const b of new TextEncoder().encode(s)) h = Math.imul(h ^ b, FNV_PRIME) >>> 0;
  return h.toString(HEX).padStart(U32_HEX, "0");
}

// faceFamily is the name a Face goes by everywhere: its zip's hash and its
// path, so nothing an upload calls itself can collide with anything else.
export function faceFamily(zip: string, path: string): string {
  return `xyf-${zip.slice(0, HASH_PREFIX)}-${fnv1a(path)}`;
}

export function faceRef(fam: FontFamily, face: FamilyFace): FaceRef {
  return { zip: fam.hash, face: face.path, family: faceFamily(fam.hash, face.path), name: fam.name, style: face.style };
}

// The bytes of every Face this page has loaded, by family; and the zips they
// came out of, read once.
const loaded = new Map<string, Promise<Uint8Array<ArrayBuffer>>>();
const zips = new Map<string, Promise<ZipEntry[]>>();

// loadFace makes a Face available to the page under its family name and
// hands back its bytes, which the browser's own raster embeds.
export function loadFace(src: FontSource, ref: FaceRef): Promise<Uint8Array<ArrayBuffer>> {
  let p = loaded.get(ref.family);
  if (!p) {
    p = (async () => {
      let entries = zips.get(ref.zip);
      if (!entries) {
        entries = src.zip(ref.zip).then((z) => zipRead(new Uint8Array(z)));
        zips.set(ref.zip, entries);
      }
      const e = (await entries).find((x) => x.name === ref.face);
      if (!e) throw new FontRefused("notfont", ref.face);
      const face = new FontFace(ref.family, e.data.slice(0));
      document.fonts.add(await face.load());
      return e.data;
    })();
    p.catch(() => loaded.delete(ref.family));
    loaded.set(ref.family, p);
  }
  return p;
}

// memoryFonts is a FontSource that keeps what is uploaded for the life of the
// page: the lab, which has no board to keep them on.
export function memoryFonts(): FontSource {
  const fams: FontFamily[] = [];
  const bytes = new Map<string, Uint8Array<ArrayBuffer>>();
  return {
    families: () => fams,
    zip: async (hash) => {
      const z = bytes.get(hash);
      if (!z) throw new Error(`no font ${hash}`);
      return z;
    },
    async upload(files) {
      const { family, zip } = await buildFamily(files);
      bytes.set(family.hash, zip);
      const at = fams.findIndex((f) => f.name === family.name);
      if (at >= 0) fams.splice(at, 1);
      fams.push(family);
      return family;
    },
  };
}
