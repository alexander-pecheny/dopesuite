import {test} from "node:test";
import assert from "node:assert/strict";
import {rankGroup} from "./dist/brain-rank.js";

// The reference-sheet group (КИНСБФ §4.2 default order): Рыб (0) and Перед (2)
// tie on 4 очка; Перед won their бой, so Перед ranks first despite the worse
// diff. Пост (1) and Дело (3) tie on 2; Дело won their бой.
const referenceTeams = [
  {points: 4, taken: 9, conceded: 4},
  {points: 2, taken: 3, conceded: 8},
  {points: 4, taken: 6, conceded: 6},
  {points: 2, taken: 5, conceded: 5},
];
const referenceDuels = [
  {a: 0, b: 1, pa: 2, pb: 0},
  {a: 2, b: 3, pa: 2, pb: 0},
  {a: 0, b: 3, pa: 2, pb: 0},
  {a: 1, b: 2, pa: 2, pb: 0},
  {a: 0, b: 2, pa: 0, pb: 2},
  {a: 1, b: 3, pa: 0, pb: 2},
];

test("личная встреча ranks the reference group as the regulations do", () => {
  assert.deepEqual(rankGroup(referenceTeams, referenceDuels), [2, 4, 1, 3]);
});

test("an explicit football-style order ignores личная встреча", () => {
  assert.deepEqual(rankGroup(referenceTeams, referenceDuels, ["points", "diff", "taken"]), [1, 4, 2, 3]);
});

test("a three-way mini-table stays tied and falls through to taken, then diff", () => {
  // A (0) beats B (1) beats C (2) beats A, D (3) swept; mini-table all 2 очка.
  // Taken splits A (10) from B and C (9 each); diff orders B (+4) over C (+3).
  const teams = [
    {points: 4, taken: 10, conceded: 9},
    {points: 4, taken: 9, conceded: 5},
    {points: 4, taken: 9, conceded: 6},
    {points: 0, taken: 5, conceded: 13},
  ];
  const duels = [
    {a: 0, b: 1, pa: 2, pb: 0},
    {a: 1, b: 2, pa: 2, pb: 0},
    {a: 2, b: 0, pa: 2, pb: 0},
    {a: 0, b: 3, pa: 2, pb: 0},
    {a: 1, b: 3, pa: 2, pb: 0},
    {a: 2, b: 3, pa: 2, pb: 0},
  ];
  assert.deepEqual(rankGroup(teams, duels), [1, 2, 3, 4]);
});

test("a full tie shares the rank", () => {
  const teams = [
    {points: 1, taken: 3, conceded: 3},
    {points: 1, taken: 3, conceded: 3},
  ];
  assert.deepEqual(rankGroup(teams, [{a: 0, b: 1, pa: 1, pb: 1}]), [1, 1]);
});
