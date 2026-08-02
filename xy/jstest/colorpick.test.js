import { test } from "node:test";
import assert from "node:assert/strict";
import { LABEL_COLORS, labelFill, labelInk, labelName, normalizeHex, textOn } from "../web/assets/static/dist/colorpick.js";

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

// The palette is a list of NAMES now, and the hex a name paints lives in CSS —
// one per theme, because no single colour clears the card on both. What this
// module owes the caller is the mapping: name in, custom property out.
test("a palette name resolves to its custom property", () => {
  assert.ok(LABEL_COLORS.includes("teal"));
  assert.equal(labelName("teal"), "teal");
  assert.equal(labelFill("teal"), "var(--label-teal)");
  assert.equal(labelInk("teal"), "var(--label-teal-ink)");
});

// Boards created before the palette was named stored the picker's hex. Those
// eight are the same eight hues, so they are read as the name they always were
// and no board needs migrating.
test("a label stored as one of the old palette hexes reads as its name", () => {
  assert.equal(labelName("#77bb79"), "green");
  assert.equal(labelName("#674292"), "purple");
  assert.equal(labelFill("#EBD697"), "var(--label-yellow)");
});

// A hex somebody typed into the custom field, which no longer exists, is
// snapped onto the palette so it gets a colour that reads on both themes.
test("a hand-typed hex snaps to the nearest palette name", () => {
  assert.equal(labelName("#ff0000"), "red");
  assert.equal(labelName("#0000ff"), "blue");
  assert.equal(labelName("#00ffff"), "cyan");
  assert.equal(labelFill("#123456"), "var(--label-blue)");
});

// Hue is what decides, so a washed-out blue still finds blue rather than the
// paler name whose rung it happens to sit closest to.
test("snapping follows hue, not how saturated the colour is", () => {
  assert.equal(labelName("#4a88cc"), "blue");
});

// No hue at all is the one case hue cannot answer.
test("anything without chroma is gray, and an empty value has no name", () => {
  for (const bg of ["#000000", "#ffffff", "#808080", "#bfc1c3"]) {
    assert.equal(labelName(bg), "gray", bg);
  }
  assert.equal(labelName(""), "");
  assert.equal(labelFill(""), "var(--muted)");
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
