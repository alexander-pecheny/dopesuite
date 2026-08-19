import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["handoutsOverlay", "handoutsSource", "handoutsMessage", "handoutsGenerate", "handoutsSplitFit", "handoutsPdf", "handoutsDownload", "handoutsClose"]);
p.node("handoutsOverlay").hidden = true;
p.node("handoutsSource").tag = "textarea";
const requests = [];
globalThis.fetch = async (url, init) => { requests.push([url, init && init.method]); return { ok: true, status: 200, json: async () => ({ session: "s1" }), blob: async () => new Blob(["%PDF"]), text: async () => "" }; };
globalThis.setInterval = () => 0; globalThis.clearInterval = () => {};
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;
const { createHandoutsPanel } = await import("../web/assets/static/dist/handouts.js");

const cards = [
  { id: 1, listId: 1, kind: "question", rank: "a", desc: "? [Раздаточный материал: (img pic.png)]\nЧто?\n! А", handoutMeta: null },
  { id: 2, listId: 1, kind: "question", rank: "b", desc: "? Без раздатки\n! Б", handoutMeta: null },
  { id: 3, listId: 1, kind: "question", rank: "c", desc: "? [Раздаточный материал: текст]\nВопрос\n! В", handoutMeta: "columns: 2" },
];
const scope = { list: { id: 1, title: "Тур 1", rank: "a", groupId: null }, grouped: false, group: null, lists: [], cards, title: "Тур 1" };

test("opening writes the .hndt of the scope into the editor and pre-stages its images; closing persists edited settings", async () => {
  const board = fakeBoard({ name: "Доска", cards });
  const panel = createHandoutsPanel(board, { appendImages: async (fd, _cards, wanted) => new Set(wanted) });
  panel.open(scope);
  assert.equal(p.node("handoutsOverlay").hidden, false);
  assert.equal(p.node("handoutsSource").value, "for_question: 1\ncolumns: 3\n\nimage: pic.png\n---\nfor_question: 3\ncolumns: 2\n\nтекст");
  assert.equal(p.node("handoutsMessage").textContent, "");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(requests.map((r) => r[0]), ["/api/handouts/stage"], "the referenced image is staged once on open");
  // The editor changes question 3's layout; leaving writes it to the card — and
  // the default it filled in for question 1, which had none, becomes that card's.
  p.node("handoutsSource").value = p.node("handoutsSource").value.replace("columns: 2", "columns: 4\nfont_size: 12");
  p.node("handoutsClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(board.writes.map((w) => [w[0], w[2], w[3].handout_meta_enc]), [["patch", "/api/cards/1", "e:columns: 3"], ["patch", "/api/cards/3", "e:columns: 4\nfont_size: 12"]]);
  assert.equal(cards[2].handoutMeta, "columns: 4\nfont_size: 12");
  assert.equal(p.node("handoutsOverlay").hidden, true);
});

test("a list without handouts opens with an empty source and says so", () => {
  const panel = createHandoutsPanel(fakeBoard(), { appendImages: async () => new Set() });
  panel.open({ ...scope, cards: [cards[1]] });
  assert.equal(p.node("handoutsSource").value, "");
  assert.equal(p.node("handoutsMessage").textContent, "В списке нет вопросов с раздаточным материалом.");
  assert.equal(panel.label(scope), "Генерация раздаток");
  assert.equal(panel.label({ ...scope, grouped: true }), "Генерация раздаток (вся группа)");
});
