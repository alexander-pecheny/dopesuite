import { test } from "node:test";
import assert from "node:assert/strict";
import { boardMenu, listMenu, listNumbers, listScope, registerPanel, resetPanels } from "../web/assets/static/dist/panels.js";

const lists = [
  { id: 1, title: "Тур 1", rank: "a", groupId: 5 },
  { id: 2, title: "Тур 2", rank: "b", groupId: 5 },
  { id: 3, title: "Разное", rank: "c", groupId: null },
];
const board = {
  cardsOf: (id) => [{ id: id * 10, listId: id, kind: "question", desc: "" }, { id: id * 10 + 1, listId: id, kind: id === 3 ? "note" : "question", desc: "" }],
  listsInGroup: (gid) => lists.filter((l) => l.groupId === gid),
  groupById: (gid) => (gid === 5 ? { id: 5, name: "Пакет" } : undefined),
};

test("a list scope is the list, or its whole group in board order, under the group's name", () => {
  const solo = listScope(board, lists[2]);
  assert.equal(solo.title, "Разное");
  assert.equal(solo.group, null);
  assert.equal(solo.grouped, false);
  assert.equal(listScope(board, lists[1]).grouped, true);
  assert.deepEqual(solo.cards.map((c) => c.id), [30, 31]);
  const grouped = listScope(board, lists[1]);
  assert.equal(grouped.title, "Пакет");
  assert.deepEqual(grouped.lists.map((l) => l.id), [1, 2]);
  assert.deepEqual(grouped.cards.map((c) => c.id), [10, 11, 20, 21]);
});

test("a group numbers its questions as one run; listNumbers is a list's slice of it", () => {
  assert.deepEqual(listScope(board, lists[1]).numbers, ["1", "2", "3", "4"]);
  assert.deepEqual(listNumbers(board, lists[1]), ["3", "4"]);
  assert.deepEqual(listNumbers(board, lists[0]), ["1", "2"]);
  assert.deepEqual(listScope(board, lists[2]).numbers, ["1", null]);
  assert.deepEqual(listNumbers(board, lists[2]), ["1", null]);
});

test("the menus render the registry as data, in registration order, gated by offered()", () => {
  resetPanels();
  const log = [];
  registerPanel(
    { id: "export", menu: "list", icon: "file-down", label: (s) => `Экспорт${s.grouped ? " группы" : ""}`, open: (s) => log.push("export:" + s.title) },
    { id: "rename", menu: "board", icon: "pencil", label: "Переименовать доску", title: "Изменить название", open: () => log.push("rename") },
    { id: "preview-group", menu: "list", icon: "eye", label: "Предпросмотр всей группы", offered: (s) => s.grouped, open: () => log.push("pg") },
  );
  assert.deepEqual(boardMenu().map((i) => [i.id, i.label, i.title]), [["rename", "Переименовать доску", "Изменить название"]]);
  const solo = listMenu(listScope(board, lists[2]));
  assert.deepEqual(solo.map((i) => i.label), ["Экспорт"]);
  const grouped = listMenu(listScope(board, lists[0]));
  assert.deepEqual(grouped.map((i) => i.label), ["Экспорт группы", "Предпросмотр всей группы"]);
  grouped[0].onClick();
  boardMenu()[0].onClick();
  assert.deepEqual(log, ["export:Пакет", "rename"]);
  assert.throws(() => registerPanel({ id: "rename", menu: "board", icon: "pencil", label: "x", open() {} }), /twice/);
});

test("a panel that opens a cluster carries its divider into both menus", () => {
  resetPanels();
  registerPanel(
    { id: "a", menu: "board", icon: "pencil", label: "A", open() {} },
    { id: "b", menu: "board", icon: "pencil", label: "B", divider: true, open() {} },
    { id: "c", menu: "list", icon: "pencil", label: "C", open() {} },
    { id: "d", menu: "list", icon: "pencil", label: "D", divider: true, open() {} },
  );
  assert.deepEqual(boardMenu().map((i) => [i.id, i.divider]), [["a", undefined], ["b", true]]);
  const scope = listScope(board, lists[2]);
  assert.deepEqual(listMenu(scope).map((i) => [i.id, i.divider]), [["c", undefined], ["d", true]]);
});

// The rule belongs to the cluster, not to the entry that happens to open it —
// otherwise hiding a conditional cluster head silently merges its cluster into
// the one above, and the grouping quietly rots as entries come and go.
test("a hidden cluster head hands its rule to the next entry that survives", () => {
  resetPanels();
  let head = false;
  registerPanel(
    { id: "a", menu: "list", icon: "pencil", label: "A", open() {} },
    { id: "b", menu: "list", icon: "pencil", label: "B", divider: true, offered: () => head, open() {} },
    { id: "c", menu: "list", icon: "pencil", label: "C", open() {} },
  );
  const scope = listScope(board, lists[2]);
  assert.deepEqual(listMenu(scope).map((i) => [i.id, i.divider]), [["a", undefined], ["c", true]]);
  head = true;
  assert.deepEqual(listMenu(scope).map((i) => [i.id, i.divider]), [["a", undefined], ["b", true], ["c", undefined]]);
});

test("a menu never opens with a rule, however much of its first cluster is hidden", () => {
  resetPanels();
  registerPanel(
    { id: "a", menu: "list", icon: "pencil", label: "A", offered: () => false, open() {} },
    { id: "b", menu: "list", icon: "pencil", label: "B", divider: true, open() {} },
  );
  assert.deepEqual(listMenu(listScope(board, lists[2])).map((i) => [i.id, i.divider]), [["b", undefined]]);
});

// The role arrives with the snapshot, after the ☰ is first built — so the board
// menu has to be honest about `offered` AND be asked again once it changes.
test("the board menu honours offered, and answers again when the answer changes", () => {
  resetPanels();
  let role = "editor";
  registerPanel(
    { id: "rename", menu: "board", icon: "pencil", label: "Переименовать", open() {} },
    { id: "delete", menu: "board", icon: "trash-2", label: "Удалить доску", offered: () => role === "owner", open() {} },
  );
  assert.deepEqual(boardMenu().map((i) => i.id), ["rename"], "an editor is not offered the delete");
  role = "owner";
  assert.deepEqual(boardMenu().map((i) => i.id), ["rename", "delete"]);
});
