// Reordering a list field's rows (reorderable.ts): the list arithmetic and
// where a dragged tag lands among tags that wrap onto several lines.
import { test } from "node:test";
import assert from "node:assert/strict";
import { moveIndex, readingAfter } from "../web/assets/static/dist/reorderable.js";

test("moveIndex moves one item and keeps the rest in order", () => {
  assert.deepEqual(moveIndex(["a", "b", "c", "d"], 0, 2), ["b", "c", "a", "d"]);
  assert.deepEqual(moveIndex(["a", "b", "c", "d"], 3, 1), ["a", "d", "b", "c"]);
  assert.deepEqual(moveIndex(["a", "b"], 1, 1), ["a", "b"]);
});

test("a dragged tag lands in reading order across wrapped lines", () => {
  // Two lines of two tags, each 40 wide and 20 tall.
  const tags = [
    { left: 0, top: 0, width: 40, height: 20 }, { left: 50, top: 0, width: 40, height: 20 },
    { left: 0, top: 30, width: 40, height: 20 }, { left: 50, top: 30, width: 40, height: 20 },
  ];
  assert.equal(readingAfter(tags, 10, 10), 0); // before the first
  assert.equal(readingAfter(tags, 80, 10), 2); // end of line one: before line two
  assert.equal(readingAfter(tags, 30, 40), 3); // past the third, on line two
  assert.equal(readingAfter(tags, 95, 40), null); // the very end
});
