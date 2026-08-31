// The label filter's decisions: which cards survive все/любая/ни одной, and
// that filtering a list never renumbers what is left.
import { test } from "node:test";
import assert from "node:assert/strict";

const { xyLabelFilter } = await import("../web/assets/static/dist/labelfilter.js");
const { matches, filterActive, shownCards } = xyLabelFilter;

// A card's labels as the filter sees them: every assignment, scoped to a
// Playing or not (the board's dots show only the unscoped ones).
const on = (...ids) => new Set(ids);

test("no labels picked is no filter, whatever the mode", () => {
  for (const mode of ["all", "any", "none"]) {
    assert.equal(filterActive({ mode, labels: [] }), false, mode);
    assert.equal(matches({ mode, labels: [] }, on()), true, mode);
  }
});

test("«все» wants every picked label on the card", () => {
  const f = { mode: "all", labels: [1, 2] };
  assert.equal(matches(f, on(1, 2)), true);
  assert.equal(matches(f, on(1, 2, 9)), true, "extra labels are none of the filter's business");
  assert.equal(matches(f, on(1)), false);
  assert.equal(matches(f, on()), false);
});

test("«любая» wants at least one", () => {
  const f = { mode: "any", labels: [1, 2] };
  assert.equal(matches(f, on(2)), true);
  assert.equal(matches(f, on(1, 2)), true);
  assert.equal(matches(f, on(9)), false);
  assert.equal(matches(f, on()), false);
});

test("«ни одной» wants none of them, and is the mirror of «любая»", () => {
  const f = { mode: "none", labels: [1, 2] };
  assert.equal(matches(f, on(9)), true);
  assert.equal(matches(f, on()), true);
  assert.equal(matches(f, on(2)), false);
  for (const set of [on(), on(1), on(9), on(1, 2)]) {
    assert.notEqual(matches({ mode: "any", labels: [1, 2] }, set), matches(f, set), "any and none partition");
  }
});

test("one label picked makes «все» and «любая» the same filter", () => {
  for (const set of [on(), on(1), on(1, 5), on(5)]) {
    assert.equal(matches({ mode: "all", labels: [1] }, set), matches({ mode: "any", labels: [1] }, set));
  }
});

// shownCards is what a list draws: the survivors, each keeping the number it
// has on the unfiltered board. A filtered тур reads 1, 4, 7 — never 1, 2, 3.
test("survivors keep the numbers they have on the whole board", () => {
  const cards = [{ id: 10 }, { id: 11 }, { id: 12 }, { id: 13 }];
  const numbers = ["1", "2", "3", "4"];
  const got = shownCards(cards, numbers, (c) => c.id === 10 || c.id === 13);
  assert.deepEqual(got.cards.map((c) => c.id), [10, 13]);
  assert.deepEqual(got.numbers, ["1", "4"]);
});

test("an inactive filter hands back the list untouched", () => {
  const cards = [{ id: 10 }, { id: 11 }];
  const numbers = ["1", "2"];
  const got = shownCards(cards, numbers, null);
  assert.equal(got.cards, cards, "the same array, so render does no work it need not");
  assert.equal(got.numbers, numbers);
});
