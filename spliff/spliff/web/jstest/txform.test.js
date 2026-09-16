import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import {
  allocateByPercent,
  allocateEven,
  buildDraft,
  paidLeft,
  payerOrder,
  singlePayer,
  unclaimed,
} from "./dist/txform.js";

// The editor's model is the browser's copy of spliff/domain/split, and it has
// to agree with it minor unit for minor unit: the server stores amounts, so
// whatever the form computes is what everybody in the Group reads afterwards.
// These are the same hand-computed cases the Go package is checked against.

function state(over) {
  return {
    mode: "even",
    totalMinor: 1000,
    members: [1, 2, 3],
    paid: new Map([[1, 1000]]),
    chosen: [1, 2, 3],
    percents: new Map(),
    exact: new Map(),
    me: 1,
    payee: 0,
    payer: 0,
    ...over,
  };
}

Deno.test("payerOrder puts the biggest Payment first, ties by join order", () => {
  // 2 and 3 paid the same, so the earlier joiner comes first; 1 paid least.
  assertEquals(payerOrder(state({paid: new Map([[3, 500], [1, 300], [2, 500]])})), [2, 3, 1]);
  // Somebody who paid nothing is not a payer at all.
  assertEquals(payerOrder(state({paid: new Map([[2, 100]])})), [2]);
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

Deno.test("a percentage split follows the largest remainder before the order", () => {
  const pct = (...values) => new Map(values.map(([id, v]) => [id, v]));
  assertEquals(
    allocateByPercent(1000, [1, 2], pct([1, "33.33"], [2, "66.67"]), [1]),
    [333, 667],
  );
  assertEquals(
    allocateByPercent(1000, [1, 2, 3, 4], pct([1, "25"], [2, "25"], [3, "25"], [4, "25"]), [4]),
    [250, 250, 250, 250],
  );
  // Percentages that do not reach 100 leave the rest Unclaimed — a normal
  // state, not an error.
  assertEquals(allocateByPercent(1000, [1], pct([1, "50"]), [1]), [500]);
  assertEquals(allocateByPercent(1000, [1, 2], pct([1, "0"], [2, "100"]), [1]), [0, 1000]);
});

Deno.test("a percentage split past 100 is refused", () => {
  const pct = (...values) => new Map(values.map(([id, v]) => [id, v]));
  assertEquals(allocateByPercent(1000, [1, 2], pct([1, "60"], [2, "60"]), []), null);
  assertEquals(allocateByPercent(1000, [1], pct([1, "-1"]), []), null);
});

// "A paid for B" and settling up ARE one person handing the whole amount over,
// so the form picks the payer rather than asking for an amount — and the model
// reads the pick, not the (empty, unaskable) amount fields.
Deno.test("simple and settlement put the whole of it on one person", () => {
  for (const mode of ["simple", "settlement"]) {
    const result = buildDraft(state({mode, payer: 1, payee: 2, paid: new Map()}));
    assertEquals(result.error, undefined);
    assertEquals(result.draft.payments, [{member_id: 1, minor: 1000}]);
    assertEquals(result.draft.shares, [{member_id: 2, minor: 1000}]);
  }
});

// The picker cannot half-fill a field, so a single-payer mode never reports a
// mismatch: it is either picked, for the whole of it, or not picked at all.
Deno.test("a single-payer mode ignores what was typed into the amount fields", () => {
  const typed = new Map([[2, 400], [3, 600]]);
  const result = buildDraft(state({mode: "simple", payer: 1, payee: 2, paid: typed}));
  assertEquals(result.draft.payments, [{member_id: 1, minor: 1000}]);
  // And what was typed is still there, for the mode that asks for it again.
  assertEquals(typed.get(3), 600);
});

Deno.test("singlePayer names the two modes whose shape is one payer", () => {
  assertEquals(["simple", "settlement"].map(singlePayer), [true, true]);
  assertEquals(["even", "percent", "exact", "claim"].map(singlePayer), [false, false, false, false]);
});

// The line under "Who paid" is this number in words: what the total still has
// no payer for, negative when the payments overshoot it.
Deno.test("paidLeft is what the total still has no payer for", () => {
  assertEquals(paidLeft(state({paid: new Map([[1, 1000]])})), 0);
  assertEquals(paidLeft(state({paid: new Map([[1, 400]])})), 600);
  assertEquals(paidLeft(state({paid: new Map([[1, 400], [2, 900]])})), -300);
  // A picker is all-or-nothing.
  assertEquals(paidLeft(state({mode: "simple", payer: 0, paid: new Map()})), 1000);
  assertEquals(paidLeft(state({mode: "simple", payer: 2, paid: new Map()})), 0);
});

Deno.test("claim your part sets only your own share and leaves the rest unclaimed", () => {
  const result = buildDraft(state({mode: "claim", me: 1, exact: new Map([[1, 400]])}));
  assertEquals(result.draft.shares, [{member_id: 1, minor: 400}]);
  assertEquals(unclaimed(1000, result.draft.shares), 600);
});

// A Claim on somebody ELSE's bill must not throw away the Shares they and
// everybody else already have: the form disables the other rows, and the model
// keeps whatever is in them.
Deno.test("a claim on an existing bill leaves the other shares alone", () => {
  const result = buildDraft(state({
    mode: "claim", me: 2, exact: new Map([[1, 300], [2, 250]]),
  }));
  assertEquals(result.draft.shares, [{member_id: 1, minor: 300}, {member_id: 2, minor: 250}]);
  assertEquals(unclaimed(1000, result.draft.shares), 450);
});

Deno.test("exact amounts are taken as typed, and what is left stays unclaimed", () => {
  const result = buildDraft(state({mode: "exact", exact: new Map([[2, 250], [3, 250]])}));
  assertEquals(result.draft.shares, [{member_id: 2, minor: 250}, {member_id: 3, minor: 250}]);
  assertEquals(unclaimed(1000, result.draft.shares), 500);
});

Deno.test("every mode refuses what the server would refuse", () => {
  assertEquals(buildDraft(state({paid: new Map()})).error, "no_payer");
  assertEquals(buildDraft(state({paid: new Map([[1, 900]])})).error, "payments_mismatch");
  assertEquals(buildDraft(state({mode: "even", chosen: []})).error, "no_members");
  assertEquals(buildDraft(state({mode: "settlement", payer: 0})).error, "no_payer");
  assertEquals(buildDraft(state({mode: "settlement", payer: 1, payee: 0})).error, "no_payee");
  assertEquals(
    buildDraft(state({mode: "exact", exact: new Map([[1, 700], [2, 700]])})).error,
    "shares_overdraw",
  );
  assertEquals(
    buildDraft(state({
      mode: "percent",
      percents: new Map([[1, "60"], [2, "60"], [3, "0"]]),
    })).error,
    "bad_percent",
  );
});

Deno.test("an even split through buildDraft drops the zero shares", () => {
  const result = buildDraft(state({
    mode: "even",
    totalMinor: 2,
    chosen: [1, 2, 3],
    paid: new Map([[1, 2]]),
  }));
  // Two cents across three: two people get one, the third gets none and is not
  // written down at all.
  assertEquals(result.draft.shares.length, 2);
  assertEquals(result.draft.shares.reduce((sum, e) => sum + e.minor, 0), 2);
});
