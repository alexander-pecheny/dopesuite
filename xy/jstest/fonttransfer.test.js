// Bringing Font Families along with copied pictures (ADR-0025): every
// Transfer and every Bundle apply puts the ones a board lacks on it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
const { copyFamilies } = await import("../web/assets/static/dist/fonttransfer.js");

const posted = [];
let nextId = 70;
globalThis.fetch = async (url, init) => {
  posted.push(String(url));
  return { ok: true, json: async () => ({ id: ++nextId, size: 3 }) };
};
const { dk } = await xyCrypto.createBoardKeys("target");
const fam = (hash, name) => ({ hash, name, faces: [{ path: "a.ttf", style: "Regular", weight: 400, italic: false }] });
const A = "a".repeat(64), B = "b".repeat(64), C = "c".repeat(64);
const source = {
  family: (h) => ({ [A]: fam(A, "Inter"), [B]: fam(B, "Lora") })[h],
  zip: async (h) => (h === B ? Promise.reject(new Error("gone")) : new Uint8Array([1, 2, 3])),
};

test("a Family the target lacks is stored there, one it holds is not, one the source lacks is reported", async () => {
  posted.length = 0;
  const has = new Set([C]);
  // No `stored` callback: the copy must happen regardless (an optional call
  // once swallowed the upload with its argument).
  const missing = await copyFamilies([A, B, C], source, { boardId: 9, dk, has });
  assert.deepEqual(posted, ["/api/boards/9/fonts"]);
  assert.deepEqual(missing, ["Lora"]);
  assert.ok(has.has(A));
});
