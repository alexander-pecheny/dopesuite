import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { unitsOf } = await import("../web/assets/static/dist/listsmanage.js");
const { sortLabels } = await import("../web/assets/static/dist/labelsedit.js");
const { boardOrder } = await import("../web/assets/static/dist/dragrank.js");

test("unitsOf folds each run of grouped lists into one unit and leaves the rest as singletons", () => {
  const lists = [
    { id: 1, groupId: null, rank: "a" }, { id: 2, groupId: 5, rank: "b" }, { id: 3, groupId: 5, rank: "c" },
    { id: 4, groupId: null, rank: "d" }, { id: 5, groupId: 6, rank: "e" },
  ];
  assert.deepEqual(unitsOf(lists).map((u) => [u.kind, u.key, u.lists.map((l) => l.id)]), [
    ["list", "l1", [1]], ["group", "g5", [2, 3]], ["list", "l4", [4]], ["group", "g6", [5]],
  ]);
});

test("sortLabels: most recently used first, never-used last, those alphabetically descending", () => {
  const labels = [{ id: 1, name: "б" }, { id: 2, name: "а" }, { id: 3, name: "в" }, { id: 4, name: "г" }];
  const cardLabels = [{ cardId: 10, labelId: 1 }, { cardId: 30, labelId: 2 }, { cardId: 20, labelId: 1 }];
  assert.deepEqual(sortLabels(labels, cardLabels).map((l) => l.name), ["а", "б", "г", "в"]);
});

test("boardOrder reads down the board: lists by rank, cards by rank within", () => {
  const lists = [{ id: 1, rank: "b" }, { id: 2, rank: "a" }];
  const cards = [{ id: 10, listId: 1, rank: "b" }, { id: 11, listId: 1, rank: "a" }, { id: 20, listId: 2, rank: "z" }];
  assert.deepEqual(boardOrder(lists, cards).map((c) => c.id), [20, 11, 10]);
});
