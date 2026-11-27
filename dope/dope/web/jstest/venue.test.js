import {test} from "node:test";
import assert from "node:assert/strict";

globalThis.window = {};
globalThis.document = {createElement: () => ({}), activeElement: null};

const {boutWhere, venueShort, venuesShort} = await import("./dist/venue.js");

// A bout's head names its venue's room when its game's map gives one, and
// its number when not (ADR-0037).
test("a bout's head names its venue's room, or its number when it has none", () => {
  const mapped = {number: 1, title: "ауд. 308"};
  assert.equal(boutWhere(mapped, "18:00"), "ауд. 308 18:00");
  assert.equal(boutWhere({number: 2, title: ""}, ""), "пл. 2");
  assert.equal(boutWhere({number: 2, title: ""}, "9:40"), "пл. 2 9:40");
  assert.equal(venueShort(3), "пл. 3");
  assert.equal(boutWhere(null, "18:00"), "");
});

test("a group's head names its rooms, or its numbers when one has no room", () => {
  const a = {number: 1, title: "ауд. 308"};
  assert.equal(venuesShort([a, {number: 2, title: "ауд. 310"}]), "ауд. 308, ауд. 310");
  assert.equal(venuesShort([{number: 2, title: ""}, a]), "пл. 1, 2");
  assert.equal(venuesShort([]), "");
});
