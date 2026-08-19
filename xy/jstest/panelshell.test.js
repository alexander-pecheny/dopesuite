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

test("the tester list names those who saw more than half a tour, unless the tour declared", async () => {
  const board = fakeBoard({
    lists: [scope.list],
    cards,
    sessions: [
      { id: 9, meta: JSON.stringify({ title: "Тест А", testers: [{ text: "Аня" }, { text: "Боря" }] }) },
      { id: 8, meta: JSON.stringify({ title: "Тест Б", testers: [{ text: "Вера" }] }) },
    ],
    cardSessions: [{ cardId: 1, sessionId: 9 }, { cardId: 2, sessionId: 9 }, { cardId: 3, sessionId: 8 }],
  });
  const tl = createTesterList(board, shell, { copyPlain });
  assert.deepEqual([...tl.tourPicked(scope.list)], [9], "Тест А saw 2 of 3; Тест Б only 1");
  tl.panel.open(scope);
  assert.equal(title.text, "Список тестеров");
  const boxes = p.node("panelBody").querySelectorAll("input[type=checkbox]");
  assert.deepEqual(boxes.map((b) => b.checked), [true, false]);
  const line = p.node("panelBody").querySelector(".sess-invite");
  assert.equal(line.text, "Вопросы тестировали: Аня, Боря.");
  // Ticking Тест Б declares the pair for this tour, on the board.
  boxes[1].checked = true;
  boxes[1].fire("change");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(board.writes.map((w) => [w[1], w[2], w[3].session_ids]), [["setTourTesters", "/api/boards/7/tour-testers", [9, 8]]]);
  assert.deepEqual([...tl.tourPicked(scope.list)].sort(), [8, 9], "declared beats the custom");
  assert.equal(line.text, "Вопросы тестировали: Аня, Боря, Вера.");
});
