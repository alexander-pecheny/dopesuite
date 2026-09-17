import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
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

Deno.test("payerOrder puts the biggest Payment first, ties by join order", () => {
  // 2 and 3 paid the same, so the earlier joiner comes first; 1 paid least.
  assertEquals(payerOrder(state({payments: rows([3, 500], [1, 300], [2, 500])})), [2, 3, 1]);
  // A row with nothing in it is not a payer at all.
  assertEquals(payerOrder(state({payments: rows([2, 100], [3, null], [0, null])})), [2]);
});

Deno.test("an even split gives the odd cent to the payer, not to the first name", () => {
  assertEquals(allocateEven(1000, [1, 2, 3], [2]), [333, 334, 333]);
  assertEquals(allocateEven(1000, [1, 2, 3], []), [334, 333, 333]);
  assertEquals(allocateEven(100, [1, 2, 3], [3, 1]), [33, 33, 34]);
  assertEquals(allocateEven(1000, [1, 2, 3, 4, 5, 6], [5, 2]), [167, 167, 167, 166, 167, 166]);
  assertEquals(allocateEven(900, [1, 2, 3], [1]), [300, 300, 300]);
  assertEquals(allocateEven(1234, [7], [7]), [1234]);
});

Deno.test("an even split always adds up to the whole total", () => {
  for (const total of [1, 7, 99, 100, 1000, 1001, 123457]) {
    for (const n of [1, 2, 3, 4, 5, 7, 11]) {
      const members = Array.from({length: n}, (_, i) => i + 1);
      const sum = allocateEven(total, members, [members[n - 1]]).reduce((a, b) => a + b, 0);
      assertEquals(sum, total, `${total} across ${n}`);
    }
  }
});

// "Split evenly" writes amounts into the rows that are on the page, so what it
// answers is aligned with them: a row nobody is picked in gets nothing, and the
// person who paid most still gets the odd minor unit.
Deno.test("evenShares fills the rows that name somebody, and only those", () => {
  const st = state({
    payments: rows([2, 1000]),
    shares: rows([1, null], [0, null], [2, null], [3, null]),
  });
  assertEquals(evenShares(st), [333, 0, 334, 333]);
});

Deno.test("evenShares on an empty table answers nothing at all", () => {
  assertEquals(evenShares(state({shares: rows([0, null])})), [0]);
  assertEquals(evenShares(state({shares: []})), []);
});

// The line under "Who paid" is this number in words: what the total still has
// no payer for, negative when the payments overshoot it.
Deno.test("paidLeft is what the total still has no payer for", () => {
  assertEquals(paidLeft(state()), 0);
  assertEquals(paidLeft(state({payments: rows([1, 400])})), 600);
  assertEquals(paidLeft(state({payments: rows([1, 400], [2, 900])})), -300);
  assertEquals(paidLeft(state({payments: rows([1, 600], [2, 400])})), 0);
  // The row a table always ends with says nothing and counts for nothing.
  assertEquals(paidLeft(state({payments: rows([1, 1000], [0, null])})), 0);
});

Deno.test("the rows become the Payments and Shares as they are typed", () => {
  const result = buildDraft(state({
    payments: rows([1, 600], [3, 400]),
    shares: rows([2, 250], [3, 250]),
  }));
  assertEquals(result.error, undefined);
  assertEquals(result.draft.payments, [{member_id: 1, minor: 600}, {member_id: 3, minor: 400}]);
  assertEquals(result.draft.shares, [{member_id: 2, minor: 250}, {member_id: 3, minor: 250}]);
  // What no Share claims stays Unclaimed, which is a normal state.
  assertEquals(unclaimed(1000, result.draft.shares), 500);
});

// Claiming your own part of somebody else's bill is no longer a mode: it is a
// row with your name on it, and the rows already there are left alone.
Deno.test("a row added to an existing bill leaves the other shares alone", () => {
  const result = buildDraft(state({shares: rows([1, 300], [2, 250])}));
  assertEquals(result.draft.shares, [{member_id: 1, minor: 300}, {member_id: 2, minor: 250}]);
  assertEquals(unclaimed(1000, result.draft.shares), 450);
});

Deno.test("an empty row is the table's own, and never an error", () => {
  const result = buildDraft(state({
    payments: rows([1, 1000], [0, null]),
    shares: rows([2, 1000], [0, null], [3, null]),
  }));
  assertEquals(result.error, undefined);
  assertEquals(result.draft.shares, [{member_id: 2, minor: 1000}]);
});

Deno.test("a bill with no payer, or one that does not add up, is refused", () => {
  assertEquals(buildDraft(state({payments: []})).error, "no_payer");
  assertEquals(buildDraft(state({payments: rows([0, null])})).error, "no_payer");
  assertEquals(buildDraft(state({payments: rows([1, 900])})).error, "payments_mismatch");
  assertEquals(buildDraft(state({shares: rows([1, 600], [2, 600])})).error, "shares_overdraw");
});

Deno.test("an amount against nobody is a row somebody meant to finish", () => {
  assertEquals(buildDraft(state({shares: rows([0, 250])})).error, "no_person");
  assertEquals(buildDraft(state({payments: rows([0, 1000])})).error, "no_person");
});

Deno.test("the same person twice is refused rather than added up", () => {
  assertEquals(buildDraft(state({payments: rows([1, 500], [1, 500])})).error, "duplicate_member");
  assertEquals(buildDraft(state({shares: rows([2, 100], [2, 100])})).error, "duplicate_member");
});

Deno.test("a negative amount is refused on either side", () => {
  assertEquals(buildDraft(state({payments: rows([1, 1100], [2, -100])})).error, "negative_amount");
  assertEquals(buildDraft(state({shares: rows([2, -1])})).error, "negative_amount");
});
