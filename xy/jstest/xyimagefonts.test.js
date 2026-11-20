// Font Families (ADR-0025): what a Face says of itself, the zip a Family is
// stored as, and the name a Face goes by.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildFamily, faceFamily, readFace, FontRefused } from "../web/assets/static/dist/xyimagefonts.js";
import { zipWrite } from "../web/assets/static/dist/zip.js";

const asset = (name) => new Uint8Array(readFileSync(new URL(`../../chgksuite/handout/assets/${name}`, import.meta.url)));
const regular = asset("NotoSans-Regular.ttf");
const boldItalic = asset("NotoSans-BoldItalic.ttf");
const file = (bytes, name) => new File([bytes], name);

test("a Face reads its family, style, weight and slant", () => {
  assert.deepEqual(readFace(boldItalic, "x.ttf"), { family: "Noto Sans", style: "Bold Italic", weight: 700, italic: true });
  assert.deepEqual(readFace(regular, "x.ttf"), { family: "Noto Sans", style: "Regular", weight: 400, italic: false });
});

test("what typst cannot set is refused", () => {
  const head = (tag) => { const b = new Uint8Array(64); b.set([...tag].map((c) => c.charCodeAt(0))); return b; };
  for (const [bytes, reason] of [[head("wOF2"), "woff"], [head("ttcf"), "collection"], [head("%PDF"), "notfont"]]) {
    assert.throws(() => readFace(bytes, "f"), (e) => e instanceof FontRefused && e.reason === reason);
  }
});

test("the same Faces make the same zip, however they were picked", async () => {
  const loose = await buildFamily([file(boldItalic, "NotoSans-BoldItalic.ttf"), file(regular, "NotoSans-Regular.ttf")]);
  const zipped = await zipWrite([
    { name: "fonts/NotoSans-Regular.ttf", data: regular },
    { name: "OFL.txt", data: new TextEncoder().encode("licence") },
    { name: "fonts/NotoSans-BoldItalic.ttf", data: boldItalic },
  ], () => true);
  const fromZip = await buildFamily([file(zipped, "noto.zip")]);
  assert.equal(fromZip.family.hash, loose.family.hash);
  assert.equal(loose.family.name, "Noto Sans");
  assert.deepEqual(loose.family.faces.map((f) => f.style), ["Regular", "Bold Italic"]);
  assert.match(loose.family.hash, /^[0-9a-f]{64}$/);
});

test("a Face's name is its zip's and its path's, never its own", () => {
  const a = faceFamily("ab".repeat(32), "Inter-Bold.otf");
  assert.match(a, /^xyf-[0-9a-f]{16}-[0-9a-f]{8}$/);
  assert.notEqual(a, faceFamily("ab".repeat(32), "Inter-Regular.otf"));
  assert.notEqual(a, faceFamily("cd".repeat(32), "Inter-Bold.otf"));
});
