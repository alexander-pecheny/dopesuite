import assert from "node:assert/strict";
import {loadStaticModule, fakeIndex} from "./browser-module.js";

const T = loadStaticModule("match-table.js").DopeTable;

Deno.test("patchScoreTable writes shared value cells through the index", () => {
  const idx = fakeIndex();
  const total0 = idx.register("total", {team: 0});
  const plus1 = idx.register("plus", {team: 1});
  const tiebreak0 = idx.register("tiebreak", {team: 0});
  const ccLow = idx.register("correctCount", {team: 0, valueIndex: 0});
  const ccHigh = idx.register("correctCount", {team: 0, valueIndex: 4});
  const themeScore = idx.register("themeScore", {team: 0, shootout: "0", theme: 1});
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 1, answer: 2});
  const state = {
    teams: [
      {total: 170, plus: 0, shootoutTotal: 7, correctCounts: [3, 0, 0, 0, 9],
        themes: [{score: 0, answers: []}, {score: 60, answers: ["", "", "right"]}], shootoutThemes: []},
      {total: 0, plus: 110, correctCounts: [0, 0, 0, 0, 0],
        themes: [{score: 0, answers: []}, {score: 0, answers: []}], shootoutThemes: []},
    ],
  };
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.equal(total0.textContent, "170");
  assert.equal(plus1.textContent, "110");
  assert.equal(tiebreak0.textContent, "7", "tiebreak prefers shootoutTotal over tiebreak");
  // correctCount columns render reversed: cell valueIndex i shows correctCounts[4 - i].
  assert.equal(ccLow.textContent, "9", "valueIndex 0 -> correctCounts[4]");
  assert.equal(ccHigh.textContent, "3", "valueIndex 4 -> correctCounts[0]");
  assert.equal(themeScore.textContent, "60");
  assert.ok(answer.classList.contains("right"));
});

Deno.test("patchScoreTable clears a stale mark before applying the new one", () => {
  const idx = fakeIndex();
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 0, answer: 0});
  answer.classList.add("wrong");
  const state = {teams: [{total: 0, plus: 0, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: ["right"]}]}]};
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.ok(answer.classList.contains("right"));
  assert.ok(!answer.classList.contains("wrong"), "previous mark removed");
});

Deno.test("patchScoreTable invokes the host editable hooks once per team/theme", () => {
  const idx = fakeIndex();
  const state = {teams: [{total: 0, plus: 0, place: 1, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: []}, {score: 0, answers: []}]}]};
  let teamHooks = 0;
  let themeHooks = 0;
  T.patchScoreTable(idx, state, {
    formatNumber: String,
    patchTeam: () => teamHooks++,
    patchTheme: () => themeHooks++,
  });
  assert.equal(teamHooks, 1, "one patchTeam call per team");
  assert.equal(themeHooks, 2, "one patchTheme call per theme row");
});

Deno.test("patchScoreTable tolerates cells missing from the index", () => {
  const idx = fakeIndex(); // nothing registered
  assert.doesNotThrow(() =>
    T.patchScoreTable(idx, {teams: [{total: 1, plus: 1, correctCounts: [], themes: [], shootoutThemes: []}]}, {formatNumber: String}));
});

Deno.test("canPatchScoreShape: identical shape is patchable", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    teams: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  assert.equal(T.canPatchScoreShape(base, structuredClone(base)), true);
});

Deno.test("canPatchScoreShape: shape changes force a rebuild", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    teams: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  const cases = {
    "team count": (s) => s.teams.push({name: "Z", themes: [], shootoutThemes: []}),
    "team name": (s) => (s.teams[0].name = "X2"),
    "theme count": (s) => s.teams[0].themes.push({}),
    "shootout count": (s) => s.teams[0].shootoutThemes.push({}),
    "finished flag": (s) => (s.finished = true),
    "question values": (s) => (s.questionValues = [10, 20, 30]),
    "code": (s) => (s.code = "D"),
  };
  for (const [label, mutate] of Object.entries(cases)) {
    const next = structuredClone(base);
    mutate(next);
    assert.equal(T.canPatchScoreShape(base, next), false, `${label} change must rebuild`);
  }
  assert.equal(T.canPatchScoreShape(null, base), false);
  assert.equal(T.canPatchScoreShape(base, null), false);
});

Deno.test("applyDeltaOps applies set-ops to a clone without mutating the base", () => {
  const base = {seq: 1, revision: 5, teams: [{total: 10}]};
  const next = T.applyDeltaOps(base, [
    {op: "set", path: ["teams", 0, "total"], value: 20},
    {op: "set", path: ["revision"], value: 6},
  ]);
  assert.equal(next.teams[0].total, 20);
  assert.equal(next.revision, 6);
  assert.equal(base.teams[0].total, 10, "base.teams not mutated");
  assert.equal(base.revision, 5, "base.revision not mutated");
});

Deno.test("applyDeltaOps skips non-set ops", () => {
  const next = T.applyDeltaOps({a: 1}, [{op: "delete", path: ["a"]}, {op: "set", path: ["b"], value: 2}]);
  assert.equal(next.a, 1);
  assert.equal(next.b, 2);
});

Deno.test("computeEKPlayerStats aggregates per player across battles, regular themes only", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", teams: [
        {name: "Alpha", themes: [
          {player: "Ann", answers: ["right", "wrong", "", "", "right"]},
          {player: "Bob", answers: ["", "", "right", "", ""]},
          {player: "", answers: ["right", "right", "right", "right", "right"]},
        ], shootoutThemes: [
          {player: "Ann", answers: ["right", "right", "right", "right", "right"]},
        ]},
      ]},
    ]},
    {code: "r8", matches: [
      {code: "M", teams: [
        {name: "Alpha", themes: [
          {player: "Ann", answers: ["right", "", "", "", ""]},
        ]},
      ]},
    ]},
  ];
  const rows = T.computeEKPlayerStats(stages);
  assert.equal(rows.length, 2, "empty-player theme is skipped");
  const ann = rows[0];
  const bob = rows[1];
  assert.equal(ann.player, "Ann", "ordered by Σ desc");
  assert.equal(ann.sum, 50, "10+50-20 + 10, shootout excluded");
  assert.equal(ann.plus, 70, "10+50+10, no negatives");
  assert.equal(ann.battles, 2);
  assert.deepEqual(ann.right, [2, 0, 0, 0, 1]);
  assert.deepEqual(ann.wrong, [0, 1, 0, 0, 0]);
  assert.equal(ann.rightTotal, 3);
  assert.equal(bob.sum, 30);
  assert.equal(bob.battles, 1);
  // Team-share: Alpha's attributed correct answers = Ann 3 + Bob 1 = 4.
  assert.equal(Math.round(ann.share * 100), 75);
  assert.equal(Math.round(bob.share * 100), 25);
});
