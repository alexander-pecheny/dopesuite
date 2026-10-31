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

// A Division is ranked within itself: given the rows one зачёт takes, places
// are dealt afresh among those and every other row is left blank. What a
// question was worth — the rating — is still reckoned over the whole field,
// because everyone played it (ADR-0020).
test("a Division is ranked among its own rows, and the rating still counts everyone", () => {
  const all = od.rows(played, [2, 2]);
  const school = od.rows(played, [2, 2], [1, 2]);
  assert.deepEqual(school.map((r) => [r.index, r.total, r.rating, r.place]), [
    [1, 2, 2 + 1, "1"],
    [2, 1, 1, "2"],
  ]);
  // The rating of a team is what it was in the undivided table.
  for (const row of school) {
    assert.equal(row.rating, all.find((r) => r.index === row.index).rating);
  }
});

test("places blank out for every row a Division does not take", () => {
  const stats = od.questionStats(played, 4);
  const totals = played.teams.map((_, i) => od.sumRow(stats, i));
  assert.deepEqual(od.placesFor(played, stats, totals), ["1", "2", "3"]);
  assert.deepEqual(od.placesFor(played, stats, totals, [0, 2]), ["1", "", "2"]);
});

test("a tie inside a Division is shared, the way it is in the whole table", () => {
  const tied = od.parseState({
    teams: [
      {name: "A", number: 1, flags: ["Школ"]},
      {name: "B", number: 2, flags: ["Школ", "Студ"]},
      {name: "C", number: 3},
    ],
    entries: [[1, 2, 0], [0, 0, 3]],
    completed: [true, true],
  }, {}, 2);
  // Whole field: C also has one, so all three share first.
  assert.deepEqual(od.rows(tied, [2]).map((r) => r.place), ["1–3", "1–3", "1–3"]);
  // The школьный зачёт is A and B alone, and they share first between them.
  assert.deepEqual(od.rows(tied, [2], [0, 1]).map((r) => [r.index, r.place]), [[0, "1–2"], [1, "1–2"]]);
});

test("a synch fest's venue sheets flatten into one, and a venue's sheet shares the document's arrays", () => {
  const raw = {
    teams: [
      {name: "Альфа", number: 1, venue: 7, venueNumber: 1},
      {name: "Бета", number: 2, venue: 7, venueNumber: 2},
      {name: "Гамма", number: 3, venue: 9, venueNumber: 1},
    ],
    venues: {"7": {entries: [[2, 0], [1, 0]], completed: [true, false]}, "9": {entries: [[1], [1]], completed: [false, true]}},
  };
  const state = od.parseState(raw, {}, 2);
  assert.ok(od.isSynch(state));
  const flat = od.flattenVenues(state);
  assert.deepEqual(flat.completed, [true, true]);
  assert.deepEqual(flat.entries, [[2, 0, 0], [3, 0, 0]]);

  const minsk = od.venueSheet(state, 7);
  assert.deepEqual(minsk.teams.map((team) => [team.name, team.number]), [["Альфа", 1], ["Бета", 2]]);
  minsk.entries[1][1] = 2;
  assert.equal(state.venues["7"].entries[1][1], 2);
  assert.equal(od.isSynch(od.parseState({teams: []}, {}, 1)), false);
});

test("an accepted спорный counts as taken, a pending one waits beside the score, a rejected one is a miss", () => {
  const state = od.parseState({
    teams: [{name: "A", number: 1}, {name: "B", number: 2}, {name: "C", number: 3}],
    entries: [[1, 0, 0], [0, 0, 0]],
    completed: [true, true],
    contested: [
      {question: 0, number: 2, status: "accepted"},
      {question: 1, number: 2, status: "pending"},
      {question: 1, number: 3, status: "rejected"},
      {question: 1, number: 0, status: "accepted"},
    ],
  }, {}, 2);
  assert.equal(state.contested.length, 3);
  assert.deepEqual(od.rows(state, [2]).map((r) => [r.index, r.total]), [[0, 1], [1, 1], [2, 0]]);
  assert.equal(od.pendingCount(state, 1), 1);
  assert.equal(od.pendingCount(state, 1, 0, 1), 0);
  assert.equal(od.withPending(1, 1), "1+1");
  assert.equal(od.contestedAt(state, 2, 1)?.status, "rejected");
});

test("the same answer from two teams is one ruling, case and spacing aside", () => {
  const groups = od.groupContested([
    {question: 0, number: 1, answer: "Пушкин", status: "pending"},
    {question: 0, number: 2, answer: " пушкин ", status: "pending"},
    {question: 1, number: 1, answer: "Пушкин", status: "accepted"},
  ]);
  assert.deepEqual(groups.map((g) => [g.question, g.numbers, g.status]), [[0, [1, 2], "pending"], [1, [1], "accepted"]]);
});

test("a venue's sheet names its спорные by the venue's numbers", () => {
  const state = od.parseState({
    teams: [{name: "Альфа", number: 4, venue: 7, venueNumber: 1}, {name: "Гамма", number: 5, venue: 9, venueNumber: 1}],
    venues: {"7": {entries: [[0]], completed: [true]}, "9": {entries: [[0]], completed: [true]}},
    contested: [{question: 0, number: 4, status: "pending"}, {question: 0, number: 5, status: "pending"}],
  }, {}, 1);
  const sheet = od.venueSheet(state, 7);
  assert.deepEqual(sheet.contested, [{question: 0, number: 1, status: "pending"}]);
  assert.equal(sheet.teams[0].festNumber, 4);
});

test("a спорный on a question the team took anyway waits for nothing", () => {
  const state = od.parseState({
    teams: [{name: "A", number: 1}],
    entries: [[1]],
    completed: [true],
    contested: [{question: 0, number: 1, status: "pending"}],
  }, {}, 1);
  assert.equal(od.pendingCount(state, 0), 0);
});
