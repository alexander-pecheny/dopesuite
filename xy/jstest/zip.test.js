import { test } from "node:test";
import assert from "node:assert/strict";
import { xyZip } from "../web/assets/static/dist/zip.js";

const { zipWrite, zipRead, _crc32 } = xyZip;

const enc = new TextEncoder();
const bytes = (s) => enc.encode(s);

test("crc32 matches the reference value", () => {
  // python: zlib.crc32(b"123456789") == 0xCBF43926
  assert.equal(_crc32(bytes("123456789")), 0xcbf43926);
});

test("round-trips stored and deflated entries", async () => {
  const entries = [
    { name: "board.json", data: bytes('{"format":"xy.board.v1"}') },
    { name: "attachments/1-раздатка.png", data: new Uint8Array([137, 80, 78, 71, 0, 1, 2, 3]) },
    { name: "attachments/empty.bin", data: new Uint8Array(0) },
  ];
  const zipped = await zipWrite(entries, (name) => name.endsWith(".json"));
  const back = await zipRead(zipped);
  assert.equal(back.length, 3);
  for (let i = 0; i < entries.length; i++) {
    assert.equal(back[i].name, entries[i].name);
    assert.deepEqual([...back[i].data], [...entries[i].data]);
  }
});

test("deflate actually compresses repetitive data", async () => {
  const data = bytes("вопрос ".repeat(5000));
  const zipped = await zipWrite([{ name: "a.json", data }], () => true);
  assert.ok(zipped.length < data.length / 2, `${zipped.length} vs ${data.length}`);
});

test("identical input produces identical bytes", async () => {
  const entries = [{ name: "a", data: bytes("hello") }];
  const a = await zipWrite(entries, () => true);
  const b = await zipWrite(entries, () => true);
  assert.deepEqual([...a], [...b]);
});

test("rejects garbage and corruption", async () => {
  await assert.rejects(() => zipRead(bytes("not a zip at all")), /не zip/);
  const zipped = await zipWrite([{ name: "a.txt", data: bytes("hello world") }], () => false);
  // flip a byte inside the stored payload
  const broken = zipped.slice();
  broken[35] ^= 0xff;
  await assert.rejects(() => zipRead(broken), /повреждённый/);
});

test("skips directory entries from hand-repacked archives", async () => {
  // simulate a directory row by writing an entry named "dir/" with no data
  const zipped = await zipWrite([
    { name: "dir/", data: new Uint8Array(0) },
    { name: "dir/f.txt", data: bytes("x") },
  ], () => false);
  const back = await zipRead(zipped);
  assert.deepEqual(back.map((e) => e.name), ["dir/f.txt"]);
});
