import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import {describeHistory, memberNames} from "./dist/history.js";

const names = new Map([[1, "pecheny"], [2, "stas"], [3, "Vlada"]]);

function snap(fields) {
  return JSON.stringify({
    description: "Dinner", day: "2026-10-03", currency: "EUR", total_minor: 6000,
    payments: [{member_id: 2, minor: 6000}],
    shares: [{member_id: 1, minor: 3000}, {member_id: 3, minor: 3000}],
    ...fields,
  });
}

// A new bill is said in full, so the others can check it without opening it.
Deno.test("an added transaction says its total, who paid and who it was for", () => {
  assertEquals(describeHistory({kind: "created", before: "", after: snap({})}, names), [
    "total 60.00 EUR",
    "paid: stas 60.00",
    "for: pecheny 30.00, Vlada 30.00",
  ]);
});

Deno.test("an edit names each person whose amount moved", () => {
  const after = snap({
    total_minor: 9000,
    payments: [{member_id: 2, minor: 9000}],
    shares: [{member_id: 1, minor: 3000}, {member_id: 3, minor: 4000}, {member_id: 2, minor: 2000}],
  });
  assertEquals(describeHistory({kind: "edited", before: snap({}), after}, names), [
    "total: 60.00 EUR → 90.00 EUR",
    "stas paid: 60.00 EUR → 90.00 EUR",
    "Vlada's share: 30.00 EUR → 40.00 EUR",
    "stas's share: nothing → 20.00 EUR",
  ]);
});

Deno.test("a share that goes away leaves the rest unclaimed", () => {
  const after = snap({shares: [{member_id: 1, minor: 3000}]});
  assertEquals(describeHistory({kind: "edited", before: snap({}), after}, names), [
    "Vlada's share: 30.00 EUR → nothing",
    "unclaimed: 0.00 EUR → 30.00 EUR",
  ]);
});

Deno.test("a deleted transaction is said from the snapshot before it", () => {
  const lines = describeHistory({kind: "deleted", before: snap({}), after: ""}, names);
  assertEquals(lines[0], "total 60.00 EUR");
});

Deno.test("a person no longer in the group is still somebody", () => {
  const after = snap({payments: [{member_id: 9, minor: 6000}]});
  assertEquals(describeHistory({kind: "edited", before: snap({}), after}, names), [
    "stas paid: 60.00 EUR → nothing",
    "Somebody paid: nothing → 60.00 EUR",
  ]);
});

Deno.test("a photo says nothing past its verb", () => {
  assertEquals(describeHistory({kind: "photo_added", before: "", after: snap({})}, names), []);
});

// Somebody who has left is still named on what they were part of, and the
// name says they have left.
Deno.test("a Former Member keeps their name, marked as left", () => {
  const shown = memberNames([{id: 1, name: "pecheny"}, {id: 2, name: "stas"}], [{id: 3, name: "Vlada"}]);
  assertEquals(describeHistory({kind: "created", before: "", after: snap({})}, shown), [
    "total 60.00 EUR",
    "paid: stas 60.00",
    "for: pecheny 30.00, Vlada (left) 30.00",
  ]);
});
