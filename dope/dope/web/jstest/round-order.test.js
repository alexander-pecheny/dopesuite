import {test} from "node:test";
import assert from "node:assert/strict";

const {swapSlots} = await import("./dist/round-order-dialog.js");
const {followSchemeOrder} = await import("./dist/bout-page.js");

test("a swap trades the two бои's places, заходы, venues and times, and nothing else", () => {
  const a = {code: "g-1", label: "A1", wave: 1, venue: "ауд. 308", time: "10:00", seats: ["Трубачеев"], locked: false};
  const b = {code: "g-2", label: "A2", wave: 1, venue: "ауд. 310", time: "10:00", seats: ["Королёв"], locked: false};
  const c = {code: "g-3", label: "A3", wave: 2, venue: "ауд. 308", time: "11:00", seats: ["Федосеев"], locked: false};
  const round = {title: "Круг 1", bouts: [a, b, c]};
  swapSlots(round, a, c);
  assert.deepEqual(round.bouts.map((bout) => bout.label), ["A3", "A2", "A1"]);
  assert.deepEqual([c.wave, c.venue, c.time], [1, "ауд. 308", "10:00"]);
  assert.deepEqual([a.wave, a.venue, a.time], [2, "ауд. 308", "11:00"]);
  assert.deepEqual(a.seats, ["Трубачеев"], "the бой keeps its seats");
});

test("the page's scheme takes the fresher scheme's order in place, keeping its own objects", () => {
  const first = {code: "g-1", round: 1, wave: 1, venue: 1, slots: [{label: "x"}]};
  const third = {code: "g-3", round: 1, wave: 2, venue: 1};
  const matches = [first, {code: "g-2", round: 1, wave: 1, venue: 2}, third];
  const scheme = {stages: [{code: "g", matches}]};
  followSchemeOrder(scheme, JSON.stringify({stages: [{code: "g", matches: [
    {code: "g-3", wave: 1, venue: 1}, {code: "g-2", wave: 1, venue: 2}, {code: "g-1", wave: 2, venue: 1},
  ]}]}));
  assert.equal(scheme.stages[0].matches, matches, "the same array");
  assert.deepEqual(matches.map((m) => m.code), ["g-3", "g-2", "g-1"]);
  assert.equal(matches[2], first, "the same бой object");
  assert.deepEqual([first.wave, third.wave], [2, 1]);
  assert.deepEqual(first.slots, [{label: "x"}]);
  // A scheme that does not match the page's stage is left alone.
  followSchemeOrder(scheme, {stages: [{code: "g", matches: [{code: "g-1"}]}]});
  assert.deepEqual(matches.map((m) => m.code), ["g-3", "g-2", "g-1"]);
});
