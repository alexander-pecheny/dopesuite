import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/ek-stats.js";

test("computeEKPlayerStats aggregates per player across battles, regular themes only", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", participants: [
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
      {code: "M", participants: [
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
  // Team-share is each player's slice of the team's net Σ: Alpha total = 50 + 30 = 80.
  assert.equal(Math.round(ann.share * 100), 63); // 50/80
  assert.equal(Math.round(bob.share * 100), 38); // 30/80
});

test("computeEKPlayerStats keys by (team, player) — concatenation collisions stay separate", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", participants: [
        {name: "Альфа", themes: [{player: "Бета", answers: ["right", "", "", "", ""]}]},
        {name: "АльфаБ", themes: [{player: "ета", answers: ["", "right", "", "", ""]}]},
      ]},
      // battle-id collision: (stage "r16", match "8") vs (stage "r168", match "")
      {code: "8", participants: [{name: "Альфа", themes: [{player: "Бета", answers: ["right", "", "", "", ""]}]}]},
    ]},
    {code: "r168", matches: [
      {code: "", participants: [{name: "Альфа", themes: [{player: "Бета", answers: ["", "", "right", "", ""]}]}]},
    ]},
  ];
  const rows = T.computeEKPlayerStats(stages);
  assert.equal(rows.length, 2, "colliding (team,player) concatenations must not merge");
  const beta = rows.find((r) => r.team === "Альфа");
  assert.equal(beta.battles, 3, "colliding battle ids must count separately");
});

test("computeEKPlayerStats team-share zeroes out non-helpers", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", participants: [
        // Share is over POSITIVE contributors only; negatives are 0.
        {name: "Plus", themes: [
          {player: "Up", answers: ["right", "right", "", "", ""]},   // +30
          {player: "Down", answers: ["wrong", "", "", "", ""]},      // -10
        ]},
        // Net-negative team: the positive player still gets a share (its slice
        // of the team's positive points), the negative player is 0.
        {name: "Minus", themes: [
          {player: "Good", answers: ["right", "", "", "", ""]},      // +10
          {player: "Bad", answers: ["", "", "", "", "wrong"]},       // -50
        ]},
      ]},
    ]},
  ];
  const byName = Object.fromEntries(computeEKShareStats(stages));
  // Plus team positive-total = 30 (only Up). Up = 30/30 = 100%; Down is 0.
  assert.equal(Math.round(byName["Up"] * 100), 100);
  assert.equal(byName["Down"], 0);
  // Minus team positive-total = 10 (only Good). Good = 10/10 = 100% even though
  // the team net is negative; Bad (negative) is 0.
  assert.equal(Math.round(byName["Good"] * 100), 100);
  assert.equal(byName["Bad"], 0);

  function computeEKShareStats(s) {
    return T.computeEKPlayerStats(s).map((r) => [r.player, r.share]);
  }
});

// Личная СИ's Статистика: the participant is the player, so the aggregate is
// per seat — Σ, Σ+ (positive points), бои and the taken counts per value —
// regular themes only, sorted by Σ.
test("computeIndividualPlayerStats aggregates per participant", () => {
  const stages = [{
    code: "s1",
    matches: [{
      code: "m1",
      finished: true,
      participants: [
        {name: "Виктор Вега", themes: [{answers: ["right", "", "", "", ""]}, {answers: ["", "wrong", "", "", ""]}]},
        {name: "Николай Зотов", themes: [{answers: ["", "", "", "", "right"]}]},
      ],
    }, {
      code: "m2",
      finished: true,
      participants: [
        {name: "Виктор Вега", themes: [{answers: ["", "", "right", "", ""]}]},
      ],
    }, {
      // Seeded but unplayed: not a бой played, nothing counted.
      code: "m3",
      participants: [
        {name: "Виктор Вега", themes: [{answers: ["right", "", "", "", ""]}]},
      ],
    }],
  }];
  const rows = T.computeIndividualPlayerStats(stages);
  assert.deepEqual(rows, [
    {player: "Николай Зотов", sum: 50, plus: 50, battles: 1, right: [0, 0, 0, 0, 1]},
    {player: "Виктор Вега", sum: 20, plus: 40, battles: 2, right: [1, 0, 1, 0, 0]},
  ]);
});
