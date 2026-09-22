import {test} from "node:test";
import assert from "node:assert/strict";
import {computeGroupBlockRounds, evalScoringRule} from "./dist/group-stats.js";

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
      {blockRound: 1, finished: true, participants: [
        {name: "Виктор Вега", place: 2}, {name: "Алексей Погорелов", place: 1}, {name: "Николай Зотов", place: 3},
      ]},
      {blockRound: 2, finished: true, participants: [
        {name: "Виктор Вега", place: 1}, {name: "Николай Зотов", place: 2},
      ]},
      {blockRound: 3, finished: false, participants: [
        {name: "Виктор Вега", place: 1},
      ]},
    ],
    pointsRule: "seats + 1 - place",
    blockRoundCount: 3,
  });
  assert.deepEqual(rows, [
    {name: "Виктор Вега", points: 4, blockRounds: [2, 2, 0]},
    {name: "Алексей Погорелов", points: 3, blockRounds: [3, 0, 0]},
    {name: "Николай Зотов", points: 2, blockRounds: [1, 1, 0]},
  ]);
});
