import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["handoutsOverlay", "handoutsSource", "handoutsFields", "handoutsTabFields", "handoutsTabText", "handoutsMessage", "handoutsSplitFit", "handoutsPdf", "handoutsDownload", "handoutsClose"]);
p.node("handoutsOverlay").hidden = true;
p.node("handoutsSource").tag = "textarea";
const requests = [];
globalThis.fetch = async (url, init) => { requests.push([url, init && init.method]); return { ok: true, status: 200, json: async () => ({ session: "s1" }), blob: async () => new Blob(["%PDF"]), text: async () => "" }; };
globalThis.requestAnimationFrame = () => 0;
globalThis.setInterval = () => 0; globalThis.clearInterval = () => {};
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;
const { createHandoutsPanel } = await import("../web/assets/static/dist/handouts.js");

const cards = [
  { id: 1, listId: 1, kind: "question", rank: "a0", desc: "? [Раздаточный материал: (img pic.png)]\nЧто?\n! А", handoutMeta: null },
  { id: 2, listId: 1, kind: "question", rank: "a1", desc: "? Без раздатки\n! Б", handoutMeta: null },
  { id: 3, listId: 1, kind: "question", rank: "a2", desc: "? [Раздаточный материал: текст]\nВопрос\n! В", handoutMeta: "columns: 2" },
];
const list = { id: 1, title: "Тур 1", rank: "a", groupId: null };
const scope = { list, grouped: false, group: null, lists: [list], cards, title: "Тур 1" };

test("opening writes the .hndt of the scope into the editor, stages its images once and renders the preview; closing persists edited settings", async () => {
  const board = fakeBoard({ name: "Доска", cards });
  const panel = createHandoutsPanel(board, { appendImages: async (fd, _cards, wanted) => new Set(wanted), cardAttachments: async () => [] });
  panel.open(scope);
  assert.equal(p.node("handoutsOverlay").hidden, false);
  assert.equal(p.node("handoutsSource").value, "for_question: 1\ncolumns: 3\n\nimage: pic.png\n---\nfor_question: 3\ncolumns: 2\n\nтекст");
  assert.equal(p.node("handoutsMessage").textContent, "");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(requests.map((r) => r[0]), ["/api/handouts/stage", "/api/handouts/pdf"], "the image is staged once and the preview renders on open");
  // The editor changes question 3's layout; leaving writes it to the card — and
  // the default it filled in for question 1, which had none, becomes that card's.
  p.node("handoutsSource").value = p.node("handoutsSource").value.replace("columns: 2", "columns: 4\nfont_size: 12");
  p.node("handoutsClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(board.writes.map((w) => [w[0], w[2], w[3].handout_meta_enc]), [["patch", "/api/cards/1", "e:columns: 3"], ["patch", "/api/cards/3", "e:columns: 4\nfont_size: 12"]]);
  assert.equal(cards[2].handoutMeta, "columns: 4\nfont_size: 12");
  assert.equal(p.node("handoutsOverlay").hidden, true);
});

// A tour with no раздатка has nothing to generate, so the ⋯ does not offer the
// row at all rather than opening an empty editor and explaining itself.
test("a list without handouts is not offered the panel", () => {
  const panel = createHandoutsPanel(fakeBoard(), { appendImages: async () => new Set() });
  assert.equal(panel.offered({ ...scope, cards: [cards[1]] }), false);
  assert.equal(panel.offered(scope), true);
  assert.equal(panel.label(scope), "Вёрстка раздаток");
  assert.equal(panel.label({ ...scope, grouped: true }), "Вёрстка раздаток (вся группа)");
});

// Поля is a form over the same text: what it changes lands in the .hndt that is
// generated and saved, and the text view shows it.
test("Поля opens first and writes its edits into the .hndt", async () => {
  const board = fakeBoard({ name: "Доска", cards });
  const panel = createHandoutsPanel(board, { appendImages: async () => new Set(), cardAttachments: async () => [] });
  panel.open(scope);
  assert.equal(p.node("handoutsFields").hidden, false);
  assert.equal(p.node("handoutsSource").hidden, true);
  const [question, columns, rows] = p.node("handoutsFields").querySelectorAll("input");
  assert.deepEqual([question.value, columns.value, rows.value], ["1", "3", ""]);
  columns.value = "2"; columns.fire("input");
  rows.value = "5"; rows.fire("input");
  // The second block's alignment switch, По левому краю. Its settings are the
  // ones the first test saved on card 3.
  const segs = p.node("handoutsFields").querySelectorAll(".seg-btn");
  segs[7].fire("click");
  assert.equal(p.node("handoutsSource").value,
    "for_question: 1\ncolumns: 2\nrows: 5\n\nimage: pic.png\n---\nfor_question: 3\ncolumns: 4\nfont_size: 12\nno_center: 1\n\nтекст");
  p.node("handoutsTabText").fire("click");
  assert.equal(p.node("handoutsFields").hidden, true);
  assert.equal(p.node("handoutsSource").hidden, false);
  p.node("handoutsClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
});

// The preamble is a card of its own kind on top of the list: the panel makes it
// when the document gains a ///preamble block, and opens with it next time.
test("a preamble added in the panel becomes a card on top of the list and opens with it", async () => {
  const board = fakeBoard({ name: "Доска", cards: cards.map((c) => ({ ...c })) });
  const panel = createHandoutsPanel(board, { appendImages: async () => new Set(), cardAttachments: async () => [] });
  panel.open({ ...scope, cards: board.state.cards });
  const add = p.node("handoutsFields").querySelectorAll("button").find((b) => b.textContent === "+ Общие настройки");
  add.fire("click");
  assert.ok(p.node("handoutsSource").value.startsWith("///preamble\n---\n"));
  p.node("handoutsSource").value = p.node("handoutsSource").value.replace("///preamble", "///preamble\n/// для всего тура\nfont_size: 16");
  p.node("handoutsClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  const created = board.writes.filter((w) => w[0] === "create");
  assert.deepEqual(created.map((w) => [w[2], w[3].kind, w[3].description_enc]),
    [["/api/lists/1/cards", "handouts_preamble", "e:///preamble\n/// для всего тура\nfont_size: 16"]]);
  assert.ok(created[0][3].rank < "a0", "it goes above the first card");
  panel.open({ ...scope, cards: board.cardsOf(1) });
  assert.ok(p.node("handoutsSource").value.startsWith("///preamble\n/// для всего тура\nfont_size: 16\n---\nfor_question: 1"));
  p.node("handoutsClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(board.writes.filter((w) => w[0] === "create").length, 1, "an unchanged preamble is not written again");
});
