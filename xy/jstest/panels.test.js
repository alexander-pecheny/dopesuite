import { test } from "node:test";
import assert from "node:assert/strict";
import { boardMenu, listMenu, listScope, registerPanel, resetPanels } from "../web/assets/static/dist/panels.js";

const lists = [
  { id: 1, title: "Тур 1", rank: "a", groupId: 5 },
  { id: 2, title: "Тур 2", rank: "b", groupId: 5 },
  { id: 3, title: "Разное", rank: "c", groupId: null },
];
const board = {
  cardsOf: (id) => [{ id: id * 10, listId: id }, { id: id * 10 + 1, listId: id }],
  listsInGroup: (gid) => lists.filter((l) => l.groupId === gid),
  groupById: (gid) => (gid === 5 ? { id: 5, name: "Пакет" } : undefined),
};

test("a list scope is the list, or its whole group in board order, under the group's name", () => {
  const solo = listScope(board, lists[2]);
  assert.equal(solo.title, "Разное");
  assert.equal(solo.group, null);
  assert.deepEqual(solo.cards.map((c) => c.id), [30, 31]);
  const grouped = listScope(board, lists[1]);
  assert.equal(grouped.title, "Пакет");
  assert.deepEqual(grouped.lists.map((l) => l.id), [1, 2]);
  assert.deepEqual(grouped.cards.map((c) => c.id), [10, 11, 20, 21]);
});

test("the menus render the registry as data, in registration order, gated by offered()", () => {
  resetPanels();
  const log = [];
  registerPanel(
    { id: "export", menu: "list", icon: "file-down", label: (s) => `Экспорт${s.group ? " группы" : ""}`, open: (s) => log.push("export:" + s.title) },
    { id: "rename", menu: "board", icon: "pencil", label: "Переименовать доску", title: "Изменить название", open: () => log.push("rename") },
    { id: "preview-group", menu: "list", icon: "eye", label: "Предпросмотр всей группы", offered: (s) => !!s.group, open: () => log.push("pg") },
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
