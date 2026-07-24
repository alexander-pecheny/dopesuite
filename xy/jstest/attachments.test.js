import { test } from "node:test";
import assert from "node:assert/strict";
import { extFromMime, gatherTargets, humanSize, withExt } from "../web/assets/static/dist/attachments.js";

test("gatherTargets picks the first attachment per wanted name, in card order", () => {
  const lists = [
    [{ id: 1, name: "a.png" }, { id: 2, name: "b.png" }],
    [{ id: 3, name: "a.png" }, { id: 4, name: "" }, { id: 5, name: "c.png" }],
  ];
  const targets = gatherTargets(lists, new Set(["a.png", "c.png", "missing.png"]));
  assert.deepEqual([...targets.keys()].sort(), ["a.png", "c.png"]);
  assert.equal(targets.get("a.png").id, 1);
  assert.equal(targets.get("c.png").id, 5);
});

test("humanSize scales through B/KB/MB", () => {
  assert.equal(humanSize(512), "512 B");
  assert.equal(humanSize(2048), "2.0 KB");
  assert.equal(humanSize(3 * 1024 * 1024), "3.0 MB");
});

test("extFromMime maps known types and sanitizes the rest", () => {
  assert.equal(extFromMime("image/jpeg"), "jpg");
  assert.equal(extFromMime("image/svg+xml"), "svg");
  assert.equal(extFromMime("image/x-weird!"), "xweird");
  assert.equal(extFromMime(""), "png");
});

test("withExt replaces any typed extension with the stored format's", () => {
  assert.equal(withExt("схема.jpeg", "webp"), "схема.webp");
  assert.equal(withExt("noext", "png"), "noext.png");
  assert.equal(withExt("  ", "webp"), "вставка.webp");
});
