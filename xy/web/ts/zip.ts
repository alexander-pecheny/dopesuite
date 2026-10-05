import S from "./i18nstrings.js";

// zip.ts — a minimal zip writer/reader for Board Bundles (ADR-0013).
//
// Just enough of the format for our own artifact: UTF-8 names, store or
// deflate per entry (deflate via the native CompressionStream — the CSP allows
// no zip library, and needs none), no zip64, no encryption, no streaming. The
// reader is deliberately strict: it reads what the writer writes, plus any
// ordinary zip a user re-packed by hand after inspecting one.

export interface ZipEntry {
  name: string;
  data: Uint8Array<ArrayBuffer>;
}

// ---- crc32 (the zip checksum; IEEE, reflected) ----

const BYTE_VALUES = 256;
const BITS_PER_BYTE = 8;
const BYTE_MASK = 0xff;
const CRC_POLY = 0xedb88320;
const U32_ALL_ONES = 0xffffffff;

const CRC_TABLE = (() => {
  const t = new Uint32Array(BYTE_VALUES);
  for (let n = 0; n < BYTE_VALUES; n++) {
    let c = n;
    for (let k = 0; k < BITS_PER_BYTE; k++) c = c & 1 ? CRC_POLY ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(data: Uint8Array): number {
  let c = U32_ALL_ONES;
  for (let i = 0; i < data.length; i++) c = CRC_TABLE[(c ^ data[i]) & BYTE_MASK] ^ (c >>> BITS_PER_BYTE);
  return (c ^ U32_ALL_ONES) >>> 0;
}

// ---- record layout ----

const SIG_LOCAL = 0x04034b50;
const SIG_CENTRAL = 0x02014b50;
const SIG_EOCD = 0x06054b50;
const U16_SIZE = 2;
const U32_SIZE = 4;
const ZIP_VERSION = 20; // 2.0: deflate, directories
const METHOD_STORE = 0;
const METHOD_DEFLATE = 8;
const LOCAL_HEADER_SIZE = 30;
const CENTRAL_HEADER_SIZE = 46;
const EOCD_SIZE = 22;
const MAX_COMMENT = 0xffff;
// Field offsets inside a central directory record.
const CEN_METHOD = 10;
const CEN_CRC = 16;
const CEN_COMP_SIZE = 20;
const CEN_RAW_SIZE = 24;
const CEN_NAME_LEN = 28;
const CEN_EXTRA_LEN = 30;
const CEN_COMMENT_LEN = 32;
const CEN_LOCAL_OFFSET = 42;
// Field offsets inside a local header.
const LOC_NAME_LEN = 26;
const LOC_EXTRA_LEN = 28;
// Field offsets inside the end of central directory record.
const EOCD_DISK_COUNT = 8;
const EOCD_TOTAL_COUNT = 10;
const EOCD_DIR_SIZE = 12;
const EOCD_DIR_OFFSET = 16;

// ---- pipe bytes through a (de)compression stream ----

async function pipe(data: Uint8Array<ArrayBuffer>, stream: { readable: ReadableStream<Uint8Array>; writable: WritableStream<BufferSource> }): Promise<Uint8Array<ArrayBuffer>> {
  const blob = new Blob([data]);
  const out = await new Response(blob.stream().pipeThrough(stream)).arrayBuffer();
  return new Uint8Array(out);
}

const deflateRaw = (d: Uint8Array<ArrayBuffer>): Promise<Uint8Array<ArrayBuffer>> => pipe(d, new CompressionStream("deflate-raw"));
const inflateRaw = (d: Uint8Array<ArrayBuffer>): Promise<Uint8Array<ArrayBuffer>> => pipe(d, new DecompressionStream("deflate-raw"));

// ---- writer ----

// A fixed DOS timestamp (1980-01-01): the bundle's own exported_at lives in
// board.json, and identical input bytes should produce identical zip bytes.
const DOS_YEAR_SHIFT = 9;
const DOS_MONTH_SHIFT = 5;
const DOS_DATE = (0 << DOS_YEAR_SHIFT) | (1 << DOS_MONTH_SHIFT) | 1;
const UTF8_FLAG_BIT = 11;
const UTF8_FLAG = 1 << UTF8_FLAG_BIT;

interface Baked {
  nameBytes: Uint8Array;
  method: number;
  crc: number;
  compressed: Uint8Array<ArrayBuffer>;
  rawSize: number;
  offset: number;
}

function putHeader(v: DataView, at: number, e: Baked, central: boolean): number {
  let o = at;
  const u16 = (x: number): void => { v.setUint16(o, x, true); o += U16_SIZE; };
  const u32 = (x: number): void => { v.setUint32(o, x, true); o += U32_SIZE; };
  u32(central ? SIG_CENTRAL : SIG_LOCAL);
  if (central) u16(ZIP_VERSION); // version made by
  u16(ZIP_VERSION); // version needed
  u16(UTF8_FLAG);
  u16(e.method);
  u16(0); // mod time
  u16(DOS_DATE);
  u32(e.crc);
  u32(e.compressed.length);
  u32(e.rawSize);
  u16(e.nameBytes.length);
  u16(0); // extra
  if (central) {
    u16(0); // comment
    u16(0); // disk
    u16(0); // internal attrs
    u32(0); // external attrs
    u32(e.offset);
  }
  return o;
}

async function bake(entries: ZipEntry[], compress: (name: string) => boolean): Promise<Baked[]> {
  const enc = new TextEncoder();
  const baked: Baked[] = [];
  for (const e of entries) {
    const deflate = compress(e.name) && e.data.length > 0;
    const compressed = deflate ? await deflateRaw(e.data) : e.data;
    baked.push({
      nameBytes: enc.encode(e.name),
      method: deflate ? METHOD_DEFLATE : METHOD_STORE,
      crc: crc32(e.data),
      compressed,
      rawSize: e.data.length,
      offset: 0,
    });
  }
  return baked;
}

function putEOCD(v: DataView, o: number, count: number, centralSize: number, centralStart: number): void {
  v.setUint32(o, SIG_EOCD, true);
  v.setUint16(o + EOCD_DISK_COUNT, count, true);
  v.setUint16(o + EOCD_TOTAL_COUNT, count, true);
  v.setUint32(o + EOCD_DIR_SIZE, centralSize, true);
  v.setUint32(o + EOCD_DIR_OFFSET, centralStart, true);
}

// zipWrite packs the entries in order. compress names the entries worth
// deflating — attachment bytes are mostly already-compressed image formats,
// where deflate costs time and saves nothing.
export async function zipWrite(entries: ZipEntry[], compress: (name: string) => boolean): Promise<Uint8Array<ArrayBuffer>> {
  const baked = await bake(entries, compress);
  const localSize = baked.reduce((n, e) => n + LOCAL_HEADER_SIZE + e.nameBytes.length + e.compressed.length, 0);
  const centralSize = baked.reduce((n, e) => n + CENTRAL_HEADER_SIZE + e.nameBytes.length, 0);
  const total = localSize + centralSize + EOCD_SIZE;
  if (total >= U32_ALL_ONES) throw new Error(S.chgk.zip.tooLarge());
  const out = new Uint8Array(total);
  const v = new DataView(out.buffer);
  let o = 0;
  for (const e of baked) {
    e.offset = o;
    o = putHeader(v, o, e, false);
    out.set(e.nameBytes, o); o += e.nameBytes.length;
    out.set(e.compressed, o); o += e.compressed.length;
  }
  const centralStart = o;
  for (const e of baked) {
    o = putHeader(v, o, e, true);
    out.set(e.nameBytes, o); o += e.nameBytes.length;
  }
  putEOCD(v, o, baked.length, centralSize, centralStart);
  return out;
}

// ---- reader ----

// findEOCD scans back past a possible archive comment (up to 64K).
function findEOCD(data: Uint8Array, v: DataView): number {
  for (let i = data.length - EOCD_SIZE; i >= 0 && i >= data.length - EOCD_SIZE - MAX_COMMENT; i--) {
    if (v.getUint32(i, true) === SIG_EOCD) return i;
  }
  throw new Error(S.chgk.zip.notZip());
}

interface CentralRecord {
  name: string;
  method: number;
  crc: number;
  compSize: number;
  rawSize: number;
  offset: number;
  next: number;
}

function readCentral(data: Uint8Array, v: DataView, o: number, dec: TextDecoder): CentralRecord {
  if (v.getUint32(o, true) !== SIG_CENTRAL) throw new Error(S.chgk.zip.corrupt());
  const nameLen = v.getUint16(o + CEN_NAME_LEN, true);
  const extraLen = v.getUint16(o + CEN_EXTRA_LEN, true);
  const commentLen = v.getUint16(o + CEN_COMMENT_LEN, true);
  const nameAt = o + CENTRAL_HEADER_SIZE;
  return {
    name: dec.decode(data.subarray(nameAt, nameAt + nameLen)),
    method: v.getUint16(o + CEN_METHOD, true),
    crc: v.getUint32(o + CEN_CRC, true),
    compSize: v.getUint32(o + CEN_COMP_SIZE, true),
    rawSize: v.getUint32(o + CEN_RAW_SIZE, true),
    offset: v.getUint32(o + CEN_LOCAL_OFFSET, true),
    next: nameAt + nameLen + extraLen + commentLen,
  };
}

async function readEntryData(data: Uint8Array<ArrayBuffer>, v: DataView, r: CentralRecord): Promise<Uint8Array<ArrayBuffer>> {
  if (r.method !== METHOD_STORE && r.method !== METHOD_DEFLATE) throw new Error(S.chgk.zip.methodUnsupported(String(r.method)));
  if (r.compSize === U32_ALL_ONES || r.rawSize === U32_ALL_ONES) throw new Error(S.chgk.zip.zip64Unsupported());
  // The local header's name/extra lengths may differ from the central copy.
  if (v.getUint32(r.offset, true) !== SIG_LOCAL) throw new Error(S.chgk.zip.corrupt());
  const dataAt = r.offset + LOCAL_HEADER_SIZE + v.getUint16(r.offset + LOC_NAME_LEN, true) + v.getUint16(r.offset + LOC_EXTRA_LEN, true);
  const compressed = data.slice(dataAt, dataAt + r.compSize);
  const raw = r.method === METHOD_DEFLATE ? await inflateRaw(compressed) : compressed;
  if (raw.length !== r.rawSize || crc32(raw) !== r.crc) throw new Error(S.chgk.zip.fileCorrupt(r.name));
  return raw;
}

// zipRead parses a whole archive into memory. Directories (trailing "/") are
// skipped; a bad checksum or an unsupported feature throws.
export async function zipRead(data: Uint8Array<ArrayBuffer>): Promise<ZipEntry[]> {
  const v = new DataView(data.buffer, data.byteOffset, data.byteLength);
  const eocd = findEOCD(data, v);
  const count = v.getUint16(eocd + EOCD_TOTAL_COUNT, true);
  let o = v.getUint32(eocd + EOCD_DIR_OFFSET, true);
  const dec = new TextDecoder();
  const out: ZipEntry[] = [];
  for (let i = 0; i < count; i++) {
    const r = readCentral(data, v, o, dec);
    o = r.next;
    if (r.name.endsWith("/") && r.rawSize === 0) continue;
    out.push({ name: r.name, data: await readEntryData(data, v, r) });
  }
  return out;
}

export const xyZip = { zipWrite, zipRead, _crc32: crc32 };
