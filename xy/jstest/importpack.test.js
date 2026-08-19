import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, fakeNode, installDOM } from "./dom.js";

const p = installDOM(["importPickOverlay", "importPickForm", "importFile", "importSplitTours", "importPickCancel", "importOverlay", "importTitle", "importSource", "importPreview", "importCount", "importCommit", "importClose"]);
for (const id of ["importPickOverlay", "importOverlay"]) p.node(id).hidden = true;
p.node("importPickForm").reset = () => {};
// The server's parse of the upload, then the lists and cards the client posts.
const posts = [];
let parsed = { name: "Пакет", source: "", images: [] };
globalThis.fetch = async (url, init) => {
  if (String(url).endsWith("/api/import/parse")) return { ok: true, status: 200, json: async () => parsed, text: async () => "" };
  posts.push([String(url), init && init.body ? JSON.parse(init.body) : null]);
  return { ok: true, status: 200, json: async () => ({ id: 100 + posts.length }), text: async () => "" };
};
globalThis.prompt = () => "Импорт";
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;
const { xySync } = await import("../web/assets/static/dist/sync.js");
xySync.requireOnline = () => true;
const { createImportPanel } = await import("../web/assets/static/dist/importpack.js");

const board = fakeBoard({ lists: [{ id: 1, title: "Тур 1", rank: "a0", groupId: null }] });
const panel = createImportPanel(board, () => fakeNode("div"));
const settle = () => new Promise((r) => setTimeout(r, 10));

test("a .4s imports straight into one new list, one card per block, kinds by their fields", async () => {
  parsed = { name: "Пакет", source: "### Тур\n\n? Раз?\n! А\n\n# просто мета\n\n? Два?\n! Б\n", images: [] };
  panel.open();
  assert.equal(p.node("importPickOverlay").hidden, false);
  p.node("importFile").files = [{ name: "pack.4s" }];
  p.node("importSplitTours").checked = false;
  p.node("importPickForm").fire("submit");
  await settle();
  assert.equal(p.node("importPickOverlay").hidden, true);
  assert.deepEqual(posts.map((x) => x[0]), ["/api/boards/7/lists", "/api/lists/101/cards", "/api/lists/101/cards", "/api/lists/101/cards", "/api/lists/101/cards"]);
  assert.equal(posts[0][1].title_enc, "e:Импорт");
  assert.deepEqual(posts.slice(1).map((x) => x[1].kind), ["heading", "question", "meta", "question"]);
  assert.equal(board.state.lists.length, 2);
  assert.equal(board.state.cards.length, 4);
  assert.ok(board.state.lists[1].rank > "a0", "the new list lands after the last");
  assert.equal(globalThis.__alerts.at(-1), "Импортировано: 4 карточек, 0 изображений.");
});

test("split by tours makes one list per «## …» section and links them into a group, then reloads", async () => {
  posts.length = 0;
  parsed = { name: "Пакет", source: "## Тур 1\n\n? Раз?\n! А\n\n## Тур 2\n\n? Два?\n! Б\n", images: [] };
  panel.open();
  p.node("importFile").files = [{ name: "pack.4s" }];
  p.node("importSplitTours").checked = true;
  p.node("importPickForm").fire("submit");
  await settle();
  const lists = posts.filter((x) => x[0] === "/api/boards/7/lists").map((x) => x[1].title_enc);
  assert.deepEqual(lists, ["e:Тур 1", "e:Тур 2"]);
  const group = posts.find((x) => x[0] === "/api/boards/7/list-groups");
  assert.equal(group[1].name_enc, "e:Импорт");
  assert.equal(board.reloads, 1);
});
