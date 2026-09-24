// The two panels that render into the shell: «Счётчик авторов» and «Список
// тестеров». A fake Board, the DOM shim and the shell over a fake modal.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, fakeNode, fakeStack, installDOM } from "./dom.js";

const p = installDOM(["panelOverlay", "panelBody", "panelMessage", "panelClose"]);
p.node("panelOverlay").hidden = true;
const title = fakeNode("h2", { className: "appearance-modal-title" });
const dialog = fakeNode("div", { attrs: { role: "dialog" } });
dialog.append(title);
p.node("panelOverlay").append(dialog);
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "enc:" + s;
const { createModal } = await import("../web/assets/static/dist/modal.js");
const { createPanelShell } = await import("../web/assets/static/dist/panels.js");
const { createAuthorCountPanel } = await import("../web/assets/static/dist/authorcount.js");
const { createTesterList } = await import("../web/assets/static/dist/testerlist.js");

const stack = fakeStack();
const shell = createPanelShell(createModal("panel", { byId: p.byId, stack }), { title, body: p.node("panelBody") });
const copied = [];
const copyPlain = async (t) => { copied.push(t); };

const cards = [
  { id: 1, listId: 1, kind: "question", rank: "a", desc: "? Раз\n! А\n@ Иван Иванов" },
  { id: 2, listId: 1, kind: "question", rank: "b", desc: "? Два\n! Б\n@ Иван Иванов, Пётр Петров" },
  { id: 3, listId: 1, kind: "question", rank: "c", desc: "? Три\n! В\n@ Пётр Петров" },
];
const scope = { list: { id: 1, title: "Тур 1", rank: "a", groupId: null }, grouped: false, group: null, lists: [], cards, numbers: ["1", "2", "3"], title: "Тур 1" };

test("the author count renders one row per author with the 1/n share, into the shell", () => {
  const panel = createAuthorCountPanel(shell, { copyPlain });
  assert.equal(panel.menu, "list");
  panel.open(scope);
  assert.equal(title.text, "Счётчик авторов");
  assert.equal(dialog.attrs["aria-label"], "Счётчик авторов");
  assert.equal(p.node("panelOverlay").hidden, false);
  const rows = p.node("panelBody").querySelectorAll("tr").map((tr) => tr.kids.map((td) => td.text));
  assert.deepEqual(rows, [
    ["Автор", "Вопросов", "Доля", "Номера"],
    ["Иван Иванов", "2", "50%", "1, 2"],
    ["Пётр Петров", "2", "50%", "2, 3"],
    ["Всего", "4", "100%", ""],
  ]);
  const upTo = p.node("panelBody").querySelector("input[placeholder=номер]");
  assert.equal(upTo.value, "3");
  upTo.value = "1";
  upTo.fire("input");
  const after = p.node("panelBody").querySelectorAll("tr").map((tr) => tr.kids.map((td) => td.text));
  assert.deepEqual(after[1], ["Иван Иванов", "1", "100%", "1"]);
  p.node("panelBody").querySelector("button").fire("click");
  assert.equal(copied[0], "Иван Иванов\t1\t100%\t1\nВсего\t1\t100%\t");
});

test("the tester list names the people who saw more than half a tour, unless the tour declared", async () => {
  const board = fakeBoard({
    lists: [scope.list],
    cards: cards.map((c) => ({ ...c })),
    sessions: [
      { id: 9, meta: JSON.stringify({ key: "a", title: "Тест А", testers: [{ text: "Аня" }, { text: "Боря" }] }) },
      { id: 8, meta: JSON.stringify({ key: "b", title: "Тест Б", testers: [{ text: "Вера" }, { text: "Аня" }] }) },
    ],
    cardSessions: [{ cardId: 1, sessionId: 9 }, { cardId: 2, sessionId: 9 }, { cardId: 3, sessionId: 8 }],
  });
  // Боря came late to Тест А and missed question 2; Гоша saw question 3 in
  // another pool and was added to it by hand.
  board.state.cards[1].seen = JSON.stringify({ absent: { a: ["Боря"] } });
  board.state.cards[2].seen = JSON.stringify({ extra: [{ text: "Гоша", type: "player" }] });
  const tl = createTesterList(board, shell, { copyPlain });
  // Counted per person: Аня was at both tests and saw all three.
  assert.deepEqual([...tl.tourPicked(scope.list)], ["Аня"], "Аня 3 of 3; Боря, Вера, Гоша 1 each");
  tl.panel.open(scope);
  assert.equal(title.text, "Список тестеров");
  // One row per test, most questions first, each with its people under it; the
  // people who were at no test come after, one by one.
  const titles = p.node("panelBody").querySelectorAll(".sess-title").map((n) => n.text);
  assert.deepEqual(titles, ["Тест А", "Аня", "Боря", "Тест Б", "Аня", "Вера", "Гоша"]);
  assert.deepEqual(p.node("panelBody").querySelectorAll("summary").map((n) => n.text), ["Аня, Боря", "Аня, Вера"]);
  assert.equal(p.node("panelBody").querySelector(".section-label").text, "Видели вне тестов");
  const metas = p.node("panelBody").querySelectorAll(".sess-meta").filter((n) => n.tag !== "summary").map((n) => n.text);
  assert.deepEqual(metas, ["2 из 3", "3 из 3", "1 из 3", "1 из 3", "3 из 3", "1 из 3", "1 из 3"]);
  const boxes = () => p.node("panelBody").querySelectorAll("input[type=checkbox]");
  // Only Аня saw more than half, so both tests are partly ticked.
  assert.deepEqual(boxes().map((b) => b.checked), [false, true, false, false, true, false, false]);
  assert.deepEqual(boxes().map((b) => !!b.indeterminate), [true, false, false, true, false, false, false]);
  const line = p.node("panelBody").querySelector(".sess-invite");
  assert.equal(line.text, "Вопросы тестировали: Аня.");
  // Ticking a test ticks everyone who was there, by name.
  boxes()[3].checked = true;
  boxes()[3].fire("change");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(board.writes.map((w) => [w[1], w[2], w[3].list_id]), [["setTourDeclaration", "/api/boards/7/tour-declaration", 1]]);
  assert.deepEqual(board.state.tourDeclarations[0].names.map((t) => t.text), ["Аня", "Вера"]);
  assert.deepEqual(boxes().map((b) => b.checked), [false, true, false, true, true, true, false]);
  // Ticking Гоша, who never sat a test, declares by name too.
  boxes()[6].checked = true;
  boxes()[6].fire("change");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual([...tl.tourPicked(scope.list)].sort(), ["Аня", "Вера", "Гоша"], "declared beats the custom");
  assert.equal(line.text, "Вопросы тестировали: Аня, Вера, Гоша.");
  // Unticking a test takes off everyone who was there, even one also at another test.
  boxes()[0].checked = false;
  boxes()[0].fire("change");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual([...tl.tourPicked(scope.list)].sort(), ["Вера", "Гоша"]);
  assert.deepEqual(boxes().map((b) => !!b.indeterminate)[3], true, "Тест Б is now partly ticked");
});

test("a tour declared by sessions before v26 reads as everyone who was at them", () => {
  const board = fakeBoard({
    lists: [scope.list],
    cards,
    sessions: [{ id: 8, meta: JSON.stringify({ key: "b", title: "Тест Б", testers: [{ text: "Вера" }] }) }],
    cardSessions: [{ cardId: 3, sessionId: 8 }],
    tourTesters: [{ listId: 1, groupId: null, sessionId: 8 }],
  });
  const tl = createTesterList(board, shell, { copyPlain });
  assert.deepEqual([...tl.tourPicked(scope.list)], ["Вера"]);
});
