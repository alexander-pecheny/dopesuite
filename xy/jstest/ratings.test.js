import { test } from "node:test";
import assert from "node:assert/strict";
import { candidates, coeffFor, lineupOf, meanDL, placeFor, sessionDL, sessionDLs, techRating } from "../web/assets/static/dist/ratings.js";

const rel = {
  id: 459,
  date: "2026-10-08",
  q: 0.5,
  players: [
    [1, "Петрова", "Анна", "Сергеевна", 9000],
    [2, "Иванов", "Иван", "Петрович", 7000],
    [3, "Иванов", "Иван", "Ильич", 8000],
    [4, "Семёнов", "Пётр", "", 6000],
  ],
  teams: [[100, 12000], [101, 9000], [102, 5000]],
};

test("coeffFor reads the band table, and nothing from place 5000 on", () => {
  assert.equal(coeffFor(1), 1.61);
  assert.equal(coeffFor(500), 1);
  assert.equal(coeffFor(501), 0.81);
  assert.equal(coeffFor(4999), 0.31);
  assert.equal(coeffFor(5000), null);
});

test("techRating weighs the best six 6/6 … 1/6 and scales by q", () => {
  assert.equal(techRating([6000, 6000, 6000, 6000, 6000, 6000], 1), 21000);
  assert.equal(techRating([1, 6000, 6000, 6000, 6000, 6000, 6000], 0.5), 10500);
  assert.equal(techRating([], 0.5), 0);
});

test("placeFor puts a rating one past every team rated higher", () => {
  assert.equal(placeFor(13000, rel), 1);
  assert.equal(placeFor(9000, rel), 2);
  assert.equal(placeFor(100, rel), 4);
});

test("sessionDL scales what a test took to a team from places 251-500", () => {
  assert.equal(sessionDL(18, 36, 400), 5);
  assert.equal(sessionDL(30, 36, 3000), 0);
  assert.equal(sessionDL(10, 36, 5000), null);
  assert.equal(sessionDL(0, 0, 400), null);
});

test("candidates match surname and given name in either order, ё or е", () => {
  assert.deepEqual(candidates(rel, "Анна Петрова").map((p) => p.id), [1]);
  assert.deepEqual(candidates(rel, "петрова анна сергеевна").map((p) => p.id), [1]);
  assert.deepEqual(candidates(rel, "Петр Семенов").map((p) => p.id), [4]);
  assert.deepEqual(candidates(rel, "Петрова").map((p) => p.id), []);
  assert.deepEqual(candidates(rel, "Анна Петрова-Водкина").map((p) => p.id), []);
  assert.deepEqual(candidates(rel, "Иван Иванов").map((p) => p.id), [3, 2], "namesakes, best first");
});

test("lineupOf takes ids first, a unique name next, and lists the rest", () => {
  const players = [
    { text: "Анна Петрова", type: "player" },
    { text: "Иван Иванов", type: "player", id: 2 },
    { text: "Иван Иванов", type: "player" },
    { text: "Никто Неизвестный", type: "player" },
  ];
  const l = lineupOf(players, rel);
  assert.equal(l.rating, techRating([9000, 7000], 0.5));
  assert.deepEqual(l.unknown.map((t) => t.text), ["Иван Иванов", "Никто Неизвестный"]);
  assert.equal(lineupOf([...players, { text: "Команда", type: "team", id: 101 }], rel).rating, 9000);
});

test("meanDL averages the tests that could be placed", () => {
  const dls = sessionDLs([
    { taken: 18, played: 36, testers: [{ text: "Команда", type: "team", id: 100 }] },
    { taken: 5, played: 36, testers: [{ text: "Никто Неизвестный", type: "player" }] },
  ], { ...rel, teams: Array.from({ length: 6000 }, (_, i) => [i + 1, 20000 - i]) });
  assert.equal(dls[1].dl, null, "an empty lineup lands below place 5000");
  assert.equal(meanDL(dls), dls[0].dl);
  assert.equal(meanDL([]), null);
});
