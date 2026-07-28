import { test } from "node:test";
import assert from "node:assert/strict";
import { LABEL_COLORS, normalizeHex, textOn } from "../web/assets/static/dist/colorpick.js";

test("normalizeHex canonicalises what a person types", () => {
  assert.equal(normalizeHex("#4A88CC"), "#4a88cc");
  assert.equal(normalizeHex(" 4a88cc "), "#4a88cc");
  assert.equal(normalizeHex("4a8"), "#44aa88");
  assert.equal(normalizeHex("#4a8"), "#44aa88");
});

test("normalizeHex rejects anything that is not a colour yet", () => {
  for (const bad of ["", "#", "12345", "#gggggg", "rebeccapurple", "#1234567"]) {
    assert.equal(normalizeHex(bad), "", bad);
  }
});

// The palette is only usable because the ink follows the fill; a fixed white is
// what forced the previous palette to be uniformly dark.
const lum = (hex) => {
  const [r, g, b] = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((u) => (u <= 0.04045 ? u / 12.92 : ((u + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a, b) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

test("every palette colour clears WCAG AA against the ink textOn picks", () => {
  for (const bg of LABEL_COLORS) {
    const ratio = contrast(bg, textOn(bg));
    assert.ok(ratio >= 4.5, `${bg} on ${textOn(bg)} is only ${ratio.toFixed(2)}:1`);
  }
});

test("textOn puts dark ink on light fills and light ink on dark ones", () => {
  assert.equal(textOn("#ebd697"), "#080a0d");
  assert.equal(textOn("#674292"), "#fdfdfd");
  assert.equal(textOn("#ffffff"), "#080a0d");
  assert.equal(textOn("#000000"), "#fdfdfd");
});

test("textOn returns nothing for a colour it cannot read", () => {
  assert.equal(textOn(""), "");
  assert.equal(textOn("nope"), "");
});
