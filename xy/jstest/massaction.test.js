import { test } from "node:test";
import assert from "node:assert/strict";
import { xyMass } from "../web/assets/static/dist/massaction.js";

const { allSelected, toggleAll, toggleOne, prune, ordered, cardCount, runSummary } = xyMass;

test("a list header reports all-selected only when it has cards and all are picked", () => {
  assert.equal(allSelected(new Set([1, 2]), [1, 2]), true);
  assert.equal(allSelected(new Set([1]), [1, 2]), false);
  assert.equal(allSelected(new Set([1, 2]), []), false, "an empty list has nothing to deselect");
});

test("select-all takes the whole list and leaves other lists' picks alone", () => {
  const selected = new Set([9]); // picked in another list
  const next = toggleAll(selected, [1, 2]);
  assert.deepEqual([...next].sort((a, b) => a - b), [1, 2, 9]);
});

test("select-all on an already-full list deselects exactly that list", () => {
  const next = toggleAll(new Set([1, 2, 9]), [1, 2]);
  assert.deepEqual([...next], [9]);
});

test("toggling one card flips it either way", () => {
  assert.deepEqual([...toggleOne(new Set(), 3)], [3]);
  assert.deepEqual([...toggleOne(new Set([3]), 3)], []);
});

test("picks whose card is gone are pruned, not left to fail forever", () => {
  assert.deepEqual([...prune(new Set([1, 2, 3]), [{ id: 1 }, { id: 3 }])], [1, 3]);
});

test("a bulk action runs in board order, not in the order you clicked", () => {
  const cards = [{ id: 5 }, { id: 2 }, { id: 8 }];
  assert.deepEqual(ordered(new Set([8, 5]), cards).map((c) => c.id), [5, 8]);
});

test("counts decline like Russian", () => {
  assert.equal(cardCount(1), "1 карточка");
  assert.equal(cardCount(3), "3 карточки");
  assert.equal(cardCount(7), "7 карточек");
  assert.equal(cardCount(11), "11 карточек");
  assert.equal(cardCount(21), "21 карточка");
});

test("a partly-failed run says so, and says the failures stayed selected", () => {
  assert.equal(runSummary(5, 0), "Готово: 5 карточек.");
  const s = runSummary(48, 2);
  assert.ok(s.includes("48"), s);
  assert.ok(s.includes("2"), s);
  assert.ok(s.includes("отмеченными"), s);
});
