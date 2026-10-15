import {test} from "node:test";
import assert from "node:assert/strict";
import {
  allocateEven,
  buildDraft,
  evenShares,
  paidLeft,
  payerOrder,
  unclaimed,
} from "./dist/txform.js";

// The editor's model is the browser's copy of spliff/domain/split, and it has
// to agree with it minor unit for minor unit: the server stores amounts, so
// whatever the form computes is what everybody in the Group reads afterwards.
// These are the same hand-computed cases the Go package is checked against.

function rows(...pairs) {
  return pairs.map(([member, minor]) => ({member, minor}));
}

function state(over) {
  return {
    totalMinor: 1000,
    members: [1, 2, 3],
    payments: rows([1, 1000]),
    shares: rows([1, 334], [2, 333], [3, 333]),
    ...over,
  };
}

test("payerOrder puts the biggest Payment first, ties by join order", () => {
  // 2 and 3 paid the same, so the earlier joiner comes first; 1 paid least.
  assert.deepEqual(payerOrder(state({payments: rows([3, 500], [1, 300], [2, 500])})), [2, 3, 1]);
  // A row with nothing in it is not a payer at all.
  assert.deepEqual(payerOrder(state({payments: rows([2, 100], [3, null], [0, null])})), [2]);
});

test("an even split gives the odd cent to the payer, not to the first name", () => {
  assert.deepEqual(allocateEven(1000, [1, 2, 3], [2]), [333, 334, 333]);
  assert.deepEqual(allocateEven(1000, [1, 2, 3], []), [334, 333, 333]);
  assert.deepEqual(allocateEven(100, [1, 2, 3], [3, 1]), [33, 33, 34]);
  assert.deepEqual(allocateEven(1000, [1, 2, 3, 4, 5, 6], [5, 2]), [167, 167, 167, 166, 167, 166]);
  assert.deepEqual(allocateEven(900, [1, 2, 3], [1]), [300, 300, 300]);
  assert.deepEqual(allocateEven(1234, [7], [7]), [1234]);
});

test("an even split always adds up to the whole total", () => {
  for (const total of [1, 7, 99, 100, 1000, 1001, 123457]) {
    for (const n of [1, 2, 3, 4, 5, 7, 11]) {
      const members = Array.from({length: n}, (_, i) => i + 1);
      const sum = allocateEven(total, members, [members[n - 1]]).reduce((a, b) => a + b, 0);
      assert.deepEqual(sum, total, `${total} across ${n}`);
    }
  }
});

// "Split evenly" writes amounts into the rows that are on the page, so what it
// answers is aligned with them: a row nobody is picked in gets nothing, and the
// person who paid most still gets the odd minor unit.
test("evenShares fills the rows that name somebody, and only those", () => {
  const st = state({
    payments: rows([2, 1000]),
    shares: rows([1, null], [0, null], [2, null], [3, null]),
  });
  assert.deepEqual(evenShares(st), [333, 0, 334, 333]);
});

test("evenShares on an empty table answers nothing at all", () => {
  assert.deepEqual(evenShares(state({shares: rows([0, null])})), [0]);
  assert.deepEqual(evenShares(state({shares: []})), []);
});

// The line under "Who paid" is this number in words: what the total still has
// no payer for, negative when the payments overshoot it.
test("paidLeft is what the total still has no payer for", () => {
  assert.deepEqual(paidLeft(state()), 0);
  assert.deepEqual(paidLeft(state({payments: rows([1, 400])})), 600);
  assert.deepEqual(paidLeft(state({payments: rows([1, 400], [2, 900])})), -300);
  assert.deepEqual(paidLeft(state({payments: rows([1, 600], [2, 400])})), 0);
  // The row a table always ends with says nothing and counts for nothing.
  assert.deepEqual(paidLeft(state({payments: rows([1, 1000], [0, null])})), 0);
});

test("the rows become the Payments and Shares as they are typed", () => {
  const result = buildDraft(state({
    payments: rows([1, 600], [3, 400]),
    shares: rows([2, 250], [3, 250]),
  }));
  assert.deepEqual(result.error, undefined);
  assert.deepEqual(result.draft.payments, [{member_id: 1, minor: 600}, {member_id: 3, minor: 400}]);
  assert.deepEqual(result.draft.shares, [{member_id: 2, minor: 250}, {member_id: 3, minor: 250}]);
  // What no Share claims stays Unclaimed, which is a normal state.
  assert.deepEqual(unclaimed(1000, result.draft.shares), 500);
});

// Claiming your own part of somebody else's bill is no longer a mode: it is a
// row with your name on it, and the rows already there are left alone.
test("a row added to an existing bill leaves the other shares alone", () => {
  const result = buildDraft(state({shares: rows([1, 300], [2, 250])}));
  assert.deepEqual(result.draft.shares, [{member_id: 1, minor: 300}, {member_id: 2, minor: 250}]);
  assert.deepEqual(unclaimed(1000, result.draft.shares), 450);
});

test("an empty row is the table's own, and never an error", () => {
  const result = buildDraft(state({
    payments: rows([1, 1000], [0, null]),
    shares: rows([2, 1000], [0, null], [3, null]),
  }));
  assert.deepEqual(result.error, undefined);
  assert.deepEqual(result.draft.shares, [{member_id: 2, minor: 1000}]);
});

test("a bill with no payer, or one that does not add up, is refused", () => {
  assert.deepEqual(buildDraft(state({payments: []})).error, "no_payer");
  assert.deepEqual(buildDraft(state({payments: rows([0, null])})).error, "no_payer");
  assert.deepEqual(buildDraft(state({payments: rows([1, 900])})).error, "payments_mismatch");
  assert.deepEqual(buildDraft(state({shares: rows([1, 600], [2, 600])})).error, "shares_overdraw");
});

test("an amount against nobody is a row somebody meant to finish", () => {
  assert.deepEqual(buildDraft(state({shares: rows([0, 250])})).error, "no_person");
  assert.deepEqual(buildDraft(state({payments: rows([0, 1000])})).error, "no_person");
});

test("the same person twice is refused rather than added up", () => {
  assert.deepEqual(buildDraft(state({payments: rows([1, 500], [1, 500])})).error, "duplicate_member");
  assert.deepEqual(buildDraft(state({shares: rows([2, 100], [2, 100])})).error, "duplicate_member");
});

test("a negative amount is refused on either side", () => {
  assert.deepEqual(buildDraft(state({payments: rows([1, 1100], [2, -100])})).error, "negative_amount");
  assert.deepEqual(buildDraft(state({shares: rows([2, -1])})).error, "negative_amount");
});
