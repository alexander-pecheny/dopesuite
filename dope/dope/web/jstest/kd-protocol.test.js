import {test} from "node:test";
import assert from "node:assert/strict";
import * as kd from "./dist/kd-protocol.js";

globalThis.window = {};
globalThis.document = {activeElement: null};

// Last year's printed cards (VIII Octobearfest): card 61 at 11 tables reads
// 6, 11, 5, 10, 4, 9, 3, 8, 2, 7, 1, 6; at 13 tables 9, 13, 4, 8, 12, 3.
test("kdTable follows the printed route cards", () => {
  assert.deepEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12].map((t) => kd.kdTable(61, t, 11)), [6, 11, 5, 10, 4, 9, 3, 8, 2, 7, 1, 6]);
  assert.deepEqual([1, 2, 3, 4, 5, 6].map((t) => kd.kdTable(61, t, 13)), [9, 13, 4, 8, 12, 3]);
  assert.deepEqual([1, 2, 3].map((t) => kd.kdTable(62, t, 11)), [7, 1, 6]);
  // Cards 1…n are the jokers: one table all game.
  assert.deepEqual([1, 5, 9].map((t) => kd.kdTable(4, t, 11)), [4, 4, 4]);
  assert.equal(kd.kdTable(0, 1, 11), 0);
});

test("two cards share a table at most once in n tours of a prime n", () => {
  const n = 11;
  for (let a = 1; a <= 6 * n; a++) {
    for (let b = a + 1; b <= 6 * n; b++) {
      let met = 0;
      for (let t = 1; t <= 9; t++) if (kd.kdTable(a, t, n) === kd.kdTable(b, t, n)) met++;
      assert.ok(met <= 1, `cards ${a} and ${b} met ${met} times`);
    }
  }
});

test("standings sum the tables on the card and break ties on full tours", () => {
  // Two tours of two questions, three tables. Table 1 takes everything, table
  // 2 one question a tour, table 3 nothing.
  const state = {
    teams: [{name: "1", city: "", number: 1}, {name: "2", city: "", number: 2}, {name: "3", city: "", number: 3}],
    entries: [[1, 2], [1], [1, 2], [1]],
    completed: [true, true, true, true],
    shootoutRounds: [],
    players: [
      {card: 1, name: "Joker One"},   // table 1, table 1: 2 + 2
      {card: 4, name: "Mover"},       // table 1, then 2: 2 + 1
      {card: 5, name: "Other"},       // table 2, then 3: 1 + 0
      {card: 2, name: "Joker Two"},   // table 2, table 2: 1 + 1
    ],
  };
  const rows = kd.standings(state, [2, 2], 3);
  assert.deepEqual(rows.map((r) => [r.player.name, r.place, r.total, r.tables, r.tours, r.best]), [
    ["Joker One", "1", 4, [1, 1], [2, 2], [2, 0, 0]],
    ["Mover", "2", 3, [1, 2], [2, 1], [1, 1, 0]],
    ["Joker Two", "3", 2, [2, 2], [1, 1], [0, 2, 0]],
    ["Other", "4", 1, [2, 3], [1, 0], [0, 1, 1]],
  ]);
});

test("equal on the sum and every tie-break, players share a place", () => {
  const state = {
    teams: [{name: "1", city: "", number: 1}, {name: "2", city: "", number: 2}],
    entries: [[1, 2]], completed: [true], shootoutRounds: [],
    players: [{card: 2, name: "B"}, {card: 1, name: "A"}],
  };
  assert.deepEqual(kd.standings(state, [1], 2).map((r) => [r.player.name, r.place]), [["A", "1–2"], ["B", "1–2"]]);
  const fresh = {...state, completed: [false]};
  assert.deepEqual(kd.standings(fresh, [1], 2).map((r) => r.place), ["", ""]);
});

test("nextFreeCard and isPrime", () => {
  assert.equal(kd.nextFreeCard([{card: 1, name: ""}, {card: 2, name: ""}, {card: 4, name: ""}]), 3);
  assert.equal(kd.nextFreeCard([]), 1);
  assert.deepEqual([1, 2, 9, 11, 13, 15].map(kd.isPrime), [false, true, false, true, true, false]);
});
