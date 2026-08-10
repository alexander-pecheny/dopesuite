import {test} from "node:test";
import assert from "node:assert/strict";
import {computeGroupRounds, evalScoringRule} from "./dist/group-stats.js";

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
test("computeGroupRounds folds a группа's бои into очки per круг", () => {
  const rows = computeGroupRounds({
    matches: [
      {round: 1, finished: true, participants: [
        {name: "Виктор Вега", place: 2}, {name: "Алексей Погорелов", place: 1}, {name: "Николай Зотов", place: 3},
      ]},
      {round: 2, finished: true, participants: [
        {name: "Виктор Вега", place: 1}, {name: "Николай Зотов", place: 2},
      ]},
      {round: 3, finished: false, participants: [
        {name: "Виктор Вега", place: 1},
      ]},
    ],
    pointsRule: "seats + 1 - place",
    roundCount: 3,
  });
  assert.deepEqual(rows, [
    {name: "Виктор Вега", points: 4, rounds: [2, 2, 0]},
    {name: "Алексей Погорелов", points: 3, rounds: [3, 0, 0]},
    {name: "Николай Зотов", points: 2, rounds: [1, 1, 0]},
  ]);
});
