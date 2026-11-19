// The .xyimage container has two implementations — web/ts/xyimagefile.ts and
// Go's chgksuite/xyimage. Both are held to chgksuite/xyimage/testdata/fixture.*,
// written by `go test ./xyimage -update` in chgksuite/.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { encodeXyimage, parseXyimage, isXyimage, rasterOf, XyimageError } from "../web/assets/static/dist/xyimagefile.js";
import fixture from "../../chgksuite/xyimage/testdata/fixture.json" with { type: "json" };

const fileBytes = new Uint8Array(readFileSync(new URL("../../chgksuite/xyimage/testdata/fixture.xyimage", import.meta.url)));
const b64 = (s) => new Uint8Array(Buffer.from(s, "base64"));
const parts = {
  recipe: fixture.recipe,
  images: fixture.images.map(b64),
  overlays: fixture.overlays,
  raster: b64(fixture.raster),
};

test("the browser writes byte for byte what Go writes", () => {
  assert.deepEqual(encodeXyimage(parts), fileBytes);
});

test("the browser reads what Go wrote", () => {
  const got = parseXyimage(fileBytes);
  assert.equal(got.recipe, fixture.recipe);
  assert.deepEqual(got.images, parts.images);
  assert.deepEqual(got.overlays, fixture.overlays);
  assert.deepEqual(rasterOf(fileBytes), parts.raster);
  assert.ok(isXyimage(fileBytes));
  assert.ok(!isXyimage(parts.raster), "a PNG is not an Edited Image");
});

function withChunk(type, data) {
  const extra = new Uint8Array(8 + data.length);
  for (let i = 0; i < 4; i++) extra[i] = type.charCodeAt(i);
  new DataView(extra.buffer).setUint32(4, data.length);
  extra.set(data, 8);
  const out = new Uint8Array(fileBytes.length + extra.length);
  out.set(fileBytes);
  out.set(extra, fileBytes.length);
  return out;
}

test("an optional chunk is skipped and an unknown critical one refused", () => {
  assert.equal(parseXyimage(withChunk("thmb", new Uint8Array([1]))).recipe, fixture.recipe);
  assert.throws(() => parseXyimage(withChunk("FONT", new Uint8Array([1]))), (e) => e instanceof XyimageError && e.reason === "newer");
});

test("a newer framing and a truncated file are refused", () => {
  const newer = fileBytes.slice();
  newer[6] = 2;
  assert.throws(() => parseXyimage(newer), (e) => e.reason === "newer");
  assert.throws(() => parseXyimage(fileBytes.slice(0, fileBytes.length - 3)), (e) => e.reason === "corrupt");
});
