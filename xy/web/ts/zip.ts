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

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(data: Uint8Array): number {
  let c = 0xffffffff;
  for (let i = 0; i < data.length; i++) c = CRC_TABLE[(c ^ data[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

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
const DOS_DATE = (0 << 9) | (1 << 5) | 1;
const UTF8_FLAG = 1 << 11;

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
  v.setUint32(o, central ? 0x02014b50 : 0x04034b50, true); o += 4;
  if (central) { v.setUint16(o, 20, true); o += 2; } // version made by
  v.setUint16(o, 20, true); o += 2; // version needed
  v.setUint16(o, UTF8_FLAG, true); o += 2;
  v.setUint16(o, e.method, true); o += 2;
  v.setUint16(o, 0, true); o += 2; // mod time
  v.setUint16(o, DOS_DATE, true); o += 2;
  v.setUint32(o, e.crc, true); o += 4;
  v.setUint32(o, e.compressed.length, true); o += 4;
  v.setUint32(o, e.rawSize, true); o += 4;
  v.setUint16(o, e.nameBytes.length, true); o += 2;
  v.setUint16(o, 0, true); o += 2; // extra
  if (central) {
    v.setUint16(o, 0, true); o += 2; // comment
    v.setUint16(o, 0, true); o += 2; // disk
    v.setUint16(o, 0, true); o += 2; // internal attrs
    v.setUint32(o, 0, true); o += 4; // external attrs
    v.setUint32(o, e.offset, true); o += 4;
  }
  return o;
}

// zipWrite packs the entries in order. compress names the entries worth
// deflating — attachment bytes are mostly already-compressed image formats,
// where deflate costs time and saves nothing.
export async function zipWrite(entries: ZipEntry[], compress: (name: string) => boolean): Promise<Uint8Array<ArrayBuffer>> {
  const enc = new TextEncoder();
  const baked: Baked[] = [];
  for (const e of entries) {
    const deflate = compress(e.name) && e.data.length > 0;
    const compressed = deflate ? await deflateRaw(e.data) : e.data;
    baked.push({
      nameBytes: enc.encode(e.name),
      method: deflate ? 8 : 0,
      crc: crc32(e.data),
      compressed,
      rawSize: e.data.length,
      offset: 0,
    });
  }
  const localSize = baked.reduce((n, e) => n + 30 + e.nameBytes.length + e.compressed.length, 0);
  const centralSize = baked.reduce((n, e) => n + 46 + e.nameBytes.length, 0);
  const total = localSize + centralSize + 22;
  if (total >= 0xffffffff) throw new Error(S.chgk.zip.tooLarge());
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
  // end of central directory
  v.setUint32(o, 0x06054b50, true);
  v.setUint16(o + 8, baked.length, true);
  v.setUint16(o + 10, baked.length, true);
  v.setUint32(o + 12, centralSize, true);
  v.setUint32(o + 16, centralStart, true);
  return out;
}

// ---- reader ----

// zipRead parses a whole archive into memory. Directories (trailing "/") are
// skipped; a bad checksum or an unsupported feature throws.
export async function zipRead(data: Uint8Array<ArrayBuffer>): Promise<ZipEntry[]> {
  const v = new DataView(data.buffer, data.byteOffset, data.byteLength);
  // EOCD: scan back past a possible archive comment (up to 64K).
  let eocd = -1;
  for (let i = data.length - 22; i >= 0 && i >= data.length - 22 - 0xffff; i--) {
    if (v.getUint32(i, true) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd < 0) throw new Error(S.chgk.zip.notZip());
  const count = v.getUint16(eocd + 10, true);
  let o = v.getUint32(eocd + 16, true);
  const dec = new TextDecoder();
  const out: ZipEntry[] = [];
  for (let i = 0; i < count; i++) {
    if (v.getUint32(o, true) !== 0x02014b50) throw new Error(S.chgk.zip.corrupt());
    const method = v.getUint16(o + 10, true);
    const crc = v.getUint32(o + 16, true);
    const compSize = v.getUint32(o + 20, true);
    const rawSize = v.getUint32(o + 24, true);
    const nameLen = v.getUint16(o + 28, true);
    const extraLen = v.getUint16(o + 30, true);
    const commentLen = v.getUint16(o + 32, true);
    const offset = v.getUint32(o + 42, true);
    const name = dec.decode(data.subarray(o + 46, o + 46 + nameLen));
    o += 46 + nameLen + extraLen + commentLen;
    if (name.endsWith("/") && rawSize === 0) continue;
    if (method !== 0 && method !== 8) throw new Error(S.chgk.zip.methodUnsupported(String(method)));
    if (compSize === 0xffffffff || rawSize === 0xffffffff) throw new Error(S.chgk.zip.zip64Unsupported());
    // The local header's name/extra lengths may differ from the central copy.
    if (v.getUint32(offset, true) !== 0x04034b50) throw new Error(S.chgk.zip.corrupt());
    const dataAt = offset + 30 + v.getUint16(offset + 26, true) + v.getUint16(offset + 28, true);
    const compressed = data.slice(dataAt, dataAt + compSize);
    const raw = method === 8 ? await inflateRaw(compressed) : compressed;
    if (raw.length !== rawSize || crc32(raw) !== crc) throw new Error(S.chgk.zip.fileCorrupt(name));
    out.push({ name, data: raw });
  }
  return out;
}

export const xyZip = { zipWrite, zipRead, _crc32: crc32 };
