import { test } from "node:test";
import assert from "node:assert/strict";
import { xyApp } from "../web/assets/static/app.js";

const { deriveTitle } = xyApp;

test("short text is returned as-is", () => {
  assert.equal(deriveTitle("Короткий вопрос"), "Короткий вопрос");
});

test("empty text falls back to placeholder", () => {
  assert.equal(deriveTitle(""), "(пусто)");
  assert.equal(deriveTitle("   \n  "), "(пусто)");
});

test("flows across lines instead of stopping at the first", () => {
  // handout-first question: the first line is uninformative on its own
  const desc = "Раздаточный материал:\nфото\nКакой город изображён на снимке?";
  const out = deriveTitle(desc);
  assert.ok(out.includes("Какой город"), out);
  assert.ok(!out.includes("\n"), "no newlines in preview");
});

test("collapses runs of whitespace to single spaces", () => {
  assert.equal(deriveTitle("a\n\n\tb   c"), "a b c");
});

test("truncates long text at a word boundary with an ellipsis", () => {
  const long = "слово ".repeat(40).trim();
  const out = deriveTitle(long, 30);
  assert.ok(out.endsWith("…"), out);
  assert.ok(out.length <= 31, out);
  assert.ok(!/\sслов$/.test(out), "should not cut mid-word: " + out);
});
