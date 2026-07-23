import {test} from "node:test";
import assert from "node:assert/strict";
import {parseClipboard, coerceValue, invertColumn} from "./dist/entry-model.js";

test("parseClipboard splits rows by newline and cols by tab", () => {
  assert.deepEqual(parseClipboard("1\t2\t3\n4\t5\t6"), [["1", "2", "3"], ["4", "5", "6"]]);
});

test("parseClipboard normalizes CRLF/CR and drops a single trailing blank line", () => {
  assert.deepEqual(parseClipboard("a\r\nb\r\n"), [["a"], ["b"]]);
  assert.deepEqual(parseClipboard("a\rb"), [["a"], ["b"]]);
  // a lone empty string stays (not a multi-line trailing blank)
  assert.deepEqual(parseClipboard(""), [[""]]);
});

const teams = [
  {label: "Альфа", number: 1},
  {label: "Бета", number: 2},
];

test("coerceValue passes bare integers through", () => {
  assert.equal(coerceValue("42", teams), 42);
  assert.equal(coerceValue("  7 ", teams), 7);
});

test("coerceValue resolves a team label to its number, case-insensitively", () => {
  assert.equal(coerceValue("бета", teams), 2);
  assert.equal(coerceValue("АЛЬФА", teams), 1);
});

test("coerceValue returns 0 for blank or unknown cells", () => {
  assert.equal(coerceValue("", teams), 0);
  assert.equal(coerceValue("   ", teams), 0);
  assert.equal(coerceValue("Гамма", teams), 0);
});

test("invertColumn returns the ascending complement packed into teamCount slots", () => {
  // teams numbered 1..4; column currently has team 2 → complement {1,3,4}
  assert.deepEqual(invertColumn([2, 0, 0, 0], [1, 2, 3, 4], 4), [1, 3, 4, 0]);
});

test("invertColumn ignores zeros/dupes when computing what is present", () => {
  assert.deepEqual(invertColumn([3, 0, 3, 0], [1, 2, 3, 4], 4), [1, 2, 4, 0]);
});

test("invertColumn of a fully-placed column is the empty column", () => {
  assert.deepEqual(invertColumn([1, 2, 3, 4], [1, 2, 3, 4], 4), [0, 0, 0, 0]);
});

test("invertColumn returns null when the invert equals the current column (no-op)", () => {
  // empty column, no team numbers → complement is empty → same as current
  assert.equal(invertColumn([0, 0, 0, 0], [], 4), null);
});
