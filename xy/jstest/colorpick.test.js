import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeHex } from "../web/assets/static/dist/colorpick.js";

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
