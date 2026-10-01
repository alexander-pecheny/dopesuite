import {test} from "node:test";
import assert from "node:assert/strict";
import {boutScope, computeGroupBlockRounds, evalScoringRule} from "./dist/group-stats.js";

// The client mirror of a per-бой scoring rule (ADR-0008): arithmetic over the
// бой's outcome. «seats + 1 - place» is what личная СИ pays очки by.
test("evalScoringRule computes a бой's очки from its outcome", () => {
  assert.equal(evalScoringRule("seats + 1 - place", {seats: 3, place: 1}), 3);
  assert.equal(evalScoringRule("seats + 1 - place", {seats: 3, place: 2.5}), 1.5);
  assert.equal(evalScoringRule("2 * (seats - place)", {seats: 4, place: 1}), 6);
  assert.equal(evalScoringRule("что-то не то", {seats: 3, place: 1}), 0);
});

// The source sheets' «Группы» tab: a player, his очки, and the split by круг.
// Only finished бои pay; the rows come back sorted by очки.
test("computeGroupBlockRounds folds a группа's бои into очки per круг", () => {
  const rows = computeGroupBlockRounds({
    matches: [
      {code: "g1-1", blockRound: 1, finished: true, participants: [
        {name: "Виктор Вега", place: 2}, {name: "Алексей Погорелов", place: 1}, {name: "Николай Зотов", place: 3},
      ]},
      {code: "g1-2", blockRound: 2, finished: true, participants: [
        {name: "Виктор Вега", place: 1}, {name: "Николай Зотов", place: 2},
      ]},
      {code: "g1-3", blockRound: 3, finished: false, participants: [
        {name: "Виктор Вега", place: 1},
      ]},
    ],
    pointsRule: "seats + 1 - place",
    blockRoundCount: 3,
  });
  // bouts is the бой each круг seats him at, played or not: the groups tab
  // links a круг to it.
  assert.deepEqual(rows, [
    {id: 0, name: "Виктор Вега", points: 4, blockRounds: [2, 2, 0], bouts: ["g1-1", "g1-2", "g1-3"]},
    {id: 0, name: "Алексей Погорелов", points: 3, blockRounds: [3, 0, 0], bouts: ["g1-1", "", ""]},
    {id: 0, name: "Николай Зотов", points: 2, blockRounds: [1, 1, 0], bouts: ["g1-1", "g1-2", ""]},
  ]);
});

// Octobearfest's личная СИ pays «(4 − место) + сумма/1000». The client mirror
// knew only seats and place, so the rule read an unknown name, evaluated to 0,
// and the группа tab showed every player at zero while the Сетка — the
// server's reading — had their очки. The mirror now sees what the server's
// scope does: the seat's metrics and the other seats'.
test("computeGroupBlockRounds reads the seat's metrics like the server", () => {
  const rows = computeGroupBlockRounds({
    matches: [
      {blockRound: 1, finished: true, questionValues: [10, 20, 30, 40, 50], participants: [
        {name: "Ольга Бурлакова", place: 1, total: 20, plus: 20, correctCounts: [0, 1, 0, 0, 0]},
        {name: "Артем Икунин", place: 2, total: 0, plus: 30, correctCounts: [0, 0, 1, 0, 0]},
        {name: "Клевер Яценко", place: 3, total: -40, plus: 0, correctCounts: [0, 0, 0, 0, 0]},
      ]},
    ],
    pointsRule: "seats + 1 - place + total / 1000",
    blockRoundCount: 4,
  });
  assert.deepEqual(rows.map((row) => [row.name, row.points]), [
    ["Ольга Бурлакова", 3.02],
    ["Артем Икунин", 2],
    ["Клевер Яценко", 0.96],
  ]);
});

test("boutScope mirrors the server's names", () => {
  const scope = boutScope({finished: true, questionValues: [10, 20], participants: [
    {name: "A", place: 1, total: 30, plus: 30, correctCounts: [1, 1]},
    {name: "B", place: 1, total: 30, plus: 40, correctCounts: [0, 2]},
    {name: "C", place: 3, total: -10, plus: 0, correctCounts: [0, 0]},
  ]}, 0);
  assert.equal(scope.seats, 3);
  assert.equal(scope.tied, 1);
  assert.equal(scope.taken20, 1);
  assert.equal(scope.opp_total, 20);
  assert.equal(scope.opp_max_plus, 40);
  assert.equal(scope.opp2_place, 3);
});

test("computeGroupBlockRounds keeps two players of one name apart by id", () => {
  const rows = computeGroupBlockRounds({
    blockRoundCount: 1,
    matches: [{blockRound: 1, finished: true, participants: [
      {id: 1, name: "Иван Петров", place: 1}, {id: 2, name: "Иван Петров", place: 2}, {id: 3, name: "Анна", place: 3},
    ]}],
  });
  assert.deepEqual(rows.map((row) => [row.id, row.points]), [[1, 3], [2, 2], [3, 1]]);
});
