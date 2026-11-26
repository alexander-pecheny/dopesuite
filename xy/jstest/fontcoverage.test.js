import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { printable, unprintable, describe } = await import("../web/assets/static/dist/chgk/fontcoverage.js");
const { renderRich } = await import("../web/assets/static/dist/chgk/preview.js");

// U+E000 is private use: it means whatever one font says, and no face the PDF
// carries has it. Everything else here some face has.
const BOX = "";

test("the scripts the PDF falls back to are printable", () => {
  for (const ch of ["ё", "օ", "ა", "ש", "中", "한", "≈", "😀", "𓀀"]) assert.ok(printable(ch), ch);
});

test("unprintable names a box once each, and never an invisible character", () => {
  assert.deepEqual(unprintable(`a${BOX}б${BOX}‍️`), [BOX]);
  assert.equal(describe([BOX]), `${BOX} (U+E000)`);
});

test("the preview marks a character the PDF cannot show", () => {
  const frag = renderRich(`слово ${BOX} и _курсив${BOX}_`, new Map());
  const marked = frag.querySelectorAll(".pv-unprintable");
  assert.equal(marked.length, 2);
  assert.match(marked[0].title, /U\+E000/);
});
