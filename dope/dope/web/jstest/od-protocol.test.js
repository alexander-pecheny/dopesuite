import {test} from "node:test";
import assert from "node:assert/strict";
import * as od from "./dist/od-protocol.js";

globalThis.window = {};
globalThis.document = {activeElement: null};

test("parseTourComp reads lists, compact strings and the default", () => {
  assert.deepEqual(od.parseTourComp([15, "15", 0]), [15, 15]);
  assert.deepEqual(od.parseTourComp("12*3"), [12, 12, 12]);
  assert.deepEqual(od.parseTourComp("15, 15 ,x,10"), [15, 15, 10]);
  assert.deepEqual(od.parseTourComp(undefined), [15]);
  assert.deepEqual(od.tourLengthsOf({stages: [{config: {tourComp: "2*2"}}]}), [2, 2]);
});

test("parseState pads and trims the grid to the scheme and drops retired fields", () => {
  const scheme = {teams: [{name: "A"}, {name: "B", city: "X"}]};
  const state = od.parseState({entries: [[1, "2", -1, 9], "junk"], completed: [1], answers: [], finished: true}, scheme, 3);
  assert.deepEqual(state.teams, [{name: "A", city: ""}, {name: "B", city: "X"}]);
  assert.deepEqual(state.entries, [[1, 2], [0, 0], [0, 0]]);
  assert.equal(od.parseState({}, {nTeams: 3}, 1).teams.length, 3);
  assert.deepEqual(state.completed, [true, false, false]);
  assert.deepEqual(state.shootoutRounds, []);
  assert.equal("answers" in state, false);
  assert.equal("finished" in state, false);
});

test("normalizeShootoutRound recovers entries from legacy marks and drops unknown teams", () => {
  const round = od.normalizeShootoutRound({teams: [3, 7, 7, "x"], answers: [["right", ""], ["", "right"]]});
  assert.deepEqual(round.teams, [3, 7]);
  assert.deepEqual(round.entries, [[3, 0], [0, 7]]);
  assert.deepEqual(round.answers, [["right", ""], ["", "right"]]);
  assert.deepEqual(round.completed, [true, true]);
  const empty = od.normalizeShootoutRound({teams: [1, 2]});
  assert.deepEqual(empty.entries, [[0, 0]]);
  assert.deepEqual(empty.completed, [false]);
});

// Worked example, two tours of two questions, three teams numbered 1–3:
// q1 taken by 1 and 2, q2 by 1 alone, q3 by everyone, q4 not played.
const played = od.parseState({
  teams: [{name: "A", number: 1}, {name: "B", number: 2}, {name: "C", number: 3}],
  entries: [[1, 2, 0], [1, 0, 0], [1, 2, 3], [0, 0, 0]],
  completed: [true, true, true, false],
}, {}, 4);

test("rows: totals, tour sums, the rating and the places", () => {
  const rows = od.rows(played, [2, 2]);
  assert.deepEqual(rows.map((r) => [r.index, r.total, r.tourSums, r.rating, r.place]), [
    [0, 3, [2, 1], 2 + 3 + 1, "1"],
    [1, 2, [1, 1], 2 + 1, "2"],
    [2, 1, [0, 1], 1, "3"],
  ]);
});

test("places stay blank before anything is marked", () => {
  const fresh = od.parseState({teams: [{number: 1}, {number: 2}]}, {}, 2);
  assert.deepEqual(od.rows(fresh, [2]).map((r) => r.place), ["", ""]);
});

test("a shootout breaks a tie on the total; a team that skipped a round ranks below one that played it", () => {
  const tied = od.parseState({
    teams: [{number: 1}, {number: 2}, {number: 3}],
    entries: [[1, 2, 3]],
    completed: [true],
    shootoutRounds: [{teams: [1, 2], entries: [[0, 2]], completed: [true]}],
  }, {}, 1);
  const rows = od.rows(tied, [1]);
  assert.deepEqual(rows.map((r) => [r.index, r.place]), [[1, "1"], [0, "2"], [2, "3"]]);
  assert.deepEqual(od.shootoutTiebreakForTeam(tied, 2), [-1]);
});
