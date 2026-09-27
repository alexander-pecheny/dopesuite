import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/ek-seating.js";
import * as Stats from "./dist/ek-stats.js";

// Выход на тему: a seating prints its players' surnames in full, one line,
// in the order the state lists them. Roster names read «Имя Фамилия», so the
// surname is the last word; a one-word name is all there is.
test("seatingLabel prints the seated players' whole surnames", () => {
  assert.equal(T.seatingLabel(["Анна Климович", "Анна Лис"]), "Климович, Лис");
  assert.equal(T.seatingLabel(["Иван Иванов", "Мария Иванова", "Дуремар"]), "Иванов, Иванова, Дуремар");
  assert.equal(T.seatingLabel([]), "");
});

test("seatedNames drops the blanks a cleared seat leaves", () => {
  assert.deepEqual(T.seatedNames(["Иван Иванов", "", null, " Игорь Ким "]), ["Иван Иванов", "Игорь Ким"]);
  assert.deepEqual(T.seatedNames(null), []);
});

// A theme's points are the team's: they divide equally among whoever sat it,
// while a question taken or missed counts whole for each of them. Worked by
// hand: theme 1 (Ann + Bob) is +10 −20 = −10, so each takes −5; theme 2 (Ann
// alone) is +50, whole. Ann: −5 + 50 = 45, Σ+ = 5 + 50 = 55. Bob: −5, Σ+ = 5.
test("a theme's Σ divides among the players seated on it, its counts do not", () => {
  const rows = Stats.computeEKPlayerStats([
    {code: "r1", matches: [
      {code: "A", participants: [
        {name: "Альфа", themes: [
          {players: ["Ann", "Bob"], answers: ["right", "wrong", "", "", ""]},
          {players: ["Ann"], answers: ["", "", "", "", "right"]},
        ]},
      ]},
    ]},
  ]);
  const byName = Object.fromEntries(rows.map((row) => [row.player, row]));
  assert.equal(byName["Ann"].sum, 45);
  assert.equal(byName["Ann"].plus, 55);
  assert.equal(byName["Bob"].sum, -5);
  assert.equal(byName["Bob"].plus, 5);
  // Both were credited the whole +10 and the whole −20, and both played the
  // one бой once however many themes they sat.
  assert.deepEqual(byName["Bob"].right, [1, 0, 0, 0, 0]);
  assert.deepEqual(byName["Bob"].wrong, [0, 1, 0, 0, 0]);
  assert.equal(byName["Ann"].battles, 1);
  assert.equal(byName["Bob"].battles, 1);
  // The share stays each player's slice of the team's positive Σ: 45 of 45.
  assert.equal(Math.round(byName["Ann"].share * 100), 100);
  assert.equal(byName["Bob"].share, 0);
});

test("a divided Σ prints to one decimal, and a whole one prints whole", () => {
  assert.equal(Stats.statNumber(45), "45");
  assert.equal(Stats.statNumber(-5), "-5");
  assert.equal(Stats.statNumber(10 / 3), "3.3");
  assert.equal(Stats.statNumber(-10 / 3), "-3.3");
  assert.equal(Stats.statNumber(6.95), "7");
});
