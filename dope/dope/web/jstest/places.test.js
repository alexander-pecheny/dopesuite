// The pages rank a document the moment a host types, before the server
// answers, so each keeps a mirror of its Protocol's ranking (ADR-0011,
// ADR-0028 §6). testdata/places.json holds every document of the fixture fest
// with the places the server's scorer deals; it is written by
// `go test ./domain/fixture -update-places`, and this test holds each mirror to
// it. A mirror that drifts from the server fails here, not on a fest day.
import {assertEquals} from "jsr:@std/assert";
import * as od from "./dist/od-protocol.js";
import * as ksi from "./dist/ksi-protocol.js";
import * as multi from "./dist/multi-protocol.js";
import * as hamsa from "./dist/hamsa-protocol.js";
import * as ek from "./dist/ek-protocol.js";
import cases from "./testdata/places.json" with {type: "json"};


// placeOf turns a page's place label into the server's number: a shared place
// "2–3" is the mean of the places it covers, and no label is no place.
function placeOf(label) {
  if (typeof label === "number") return label;
  if (!label) return 0;
  const [lo, hi] = String(label).split("–").map(Number);
  return hi ? (lo + hi) / 2 : lo;
}

// byIndex lays ranked rows out in slot order.
function byIndex(rows, count, place) {
  const out = new Array(count).fill(0);
  for (const row of rows) out[row.index] = placeOf(place(row));
  return out;
}

const mirrors = {
  od(c) {
    const lengths = od.tourLengthsOf(c.scheme);
    const state = od.parseState(c.state, c.scheme, lengths.reduce((a, n) => a + n, 0));
    return byIndex(od.rows(state, lengths), c.seats.length, (row) => row.place);
  },
  ksi(c) {
    const rules = ksi.rulesOf(c.scheme);
    const state = ksi.parseState(c.state, rules, ksi.schemeParticipants(c.scheme));
    return byIndex(ksi.rankedResultRows(state, rules, String), c.seats.length, (row) => row.placeText);
  },
  multi(c) {
    const rules = multi.rulesOf(c.scheme);
    const state = multi.parseState(c.state, rules, multi.schemeParticipants(c.scheme));
    return byIndex(multi.rankedResultRows(state, rules, String), c.seats.length, (row) => row.placeText);
  },
  hamsa(c) {
    return hamsa.placesFor(hamsa.parseState(c.state, c.seats), c.seats);
  },
};

// ranking compares two sets of places by what they decide, not by how a tie
// is written: the server stores ОД's shared 15th–16th as 15 and Хамса's as
// 15.5, a page writes "15–16". Each seat becomes one plus the number of seats
// placed strictly better; a seat with no place stays 0.
function ranking(places) {
  return places.map((p) => (p ? 1 + places.filter((q) => q && q < p).length : 0));
}

// The ЭК family's page draws the server's places but reckons Σ and Σ+ from
// the document while a write is in flight.
const VALUES = [10, 20, 30, 40, 50];

for (const c of cases.filter((c) => c.totals)) {
  Deno.test(`${c.format} ${c.bout}: the page scores the sheet as the server does`, () => {
    const state = ek.parseState(c.state, c.seats, c.themes);
    const scored = c.seats.map((id) => ek.scoreSection(state.sections.get(id), VALUES));
    assertEquals(scored.map((s) => s.total), c.totals);
    assertEquals(scored.map((s) => s.plus), c.plus);
  });
}

for (const c of cases.filter((c) => !c.totals)) {
  Deno.test(`${c.format} ${c.bout}: the page ranks as the server does`, () => {
    assertEquals(ranking(mirrors[c.format](c)), ranking(c.places));
  });
}
