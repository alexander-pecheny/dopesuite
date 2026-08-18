import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/widgets.js";

globalThis.window = {};

test("markNameOverflow flags only cells whose name is clipped", () => {
  function cell(scrollWidth, clientWidth) {
    const classes = new Set();
    return {
      classList: {toggle: (c, on) => (on ? classes.add(c) : classes.delete(c))},
      has: (c) => classes.has(c),
      querySelector: () => ({scrollWidth, clientWidth}),
    };
  }
  const clipped = cell(100, 50);
  const fits = cell(40, 50);
  const root = {querySelectorAll: () => [clipped, fits]};
  T.markNameOverflow(root, {cellSelector: ".c", nameSelector: ".n", truncatedClass: "trunc"});
  assert.ok(clipped.has("trunc"), "name wider than its cell is flagged");
  assert.ok(!fits.has("trunc"), "name within 1px is not flagged");
});
