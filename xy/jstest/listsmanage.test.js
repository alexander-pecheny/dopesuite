import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { unitsOf } = await import("../web/assets/static/dist/listsmanage.js");

test("unitsOf folds each run of grouped lists into one unit and leaves the rest as singletons", () => {
  const lists = [
    { id: 1, groupId: null, rank: "a" }, { id: 2, groupId: 5, rank: "b" }, { id: 3, groupId: 5, rank: "c" },
    { id: 4, groupId: null, rank: "d" }, { id: 5, groupId: 6, rank: "e" },
  ];
  assert.deepEqual(unitsOf(lists).map((u) => [u.kind, u.key, u.lists.map((l) => l.id)]), [
    ["list", "l1", [1]], ["group", "g5", [2, 3]], ["list", "l4", [4]], ["group", "g6", [5]],
  ]);
});
