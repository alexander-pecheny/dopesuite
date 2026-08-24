import {assertEquals} from "jsr:@std/assert";
import * as multi from "./dist/multi-protocol.js";

const scheme = {
  minigames: [
    {name: "Фоторяд", columns: [{values: [0, 1]}, {values: [0, 1]}, {values: [0, 1]}]},
    {name: "Штраф", columns: [{values: [-1, 0, 1]}, {values: [-1, 0, 1]}]},
  ],
};

Deno.test("rulesOf reads the мини-игры and notices a minus", () => {
  const rules = multi.rulesOf(scheme);
  assertEquals(rules.minigames.length, 2);
  assertEquals(rules.minigames[0].columns.length, 3);
  assertEquals(rules.signed, true);
  assertEquals(rules.sorting, ["total"]);
  assertEquals(multi.rulesOf({minigames: [scheme.minigames[0]]}).signed, false);
});

Deno.test("parseState pads every grid to the scheme's width", () => {
  const rules = multi.rulesOf(scheme);
  const state = multi.parseState({participants: [{number: 1, name: "А"}], games: [{cells: [[1]]}]}, rules, []);
  assertEquals(state.games.length, 2);
  assertEquals(state.games[0].cells[0], [1, 0, 0]);
  assertEquals(state.games[1].cells[0], [0, 0]);
});

Deno.test("scoreSheet sums per мини-игра, and Σ+ counts only the positives", () => {
  const rules = multi.rulesOf(scheme);
  const state = multi.parseState({
    participants: [{number: 1, name: "А"}],
    games: [{cells: [[1, 1, 1]]}, {cells: [[-1, 1]]}],
  }, rules, []);
  const [row] = multi.scoreSheet(state, rules);
  assertEquals(row.games, [3, 0]);
  assertEquals(row.total, 3);
  assertEquals(row.plus, 4);
});

Deno.test("rankedResultRows shares a place and obeys the scheme's comparators", () => {
  const state = (rules) => multi.parseState({
    participants: [{number: 1, name: "А"}, {number: 2, name: "Б"}],
    games: [{cells: [[1, 1, 0], [0, 0, 0]]}, {cells: [[0, 0], [1, 1]]}],
  }, rules, []);

  const plain = multi.rulesOf(scheme);
  const shared = multi.rankedResultRows(state(plain), plain, () => "");
  assertEquals(shared.map((row) => row.placeText), ["1–2", "1–2"]);

  const tiebroken = multi.rulesOf({...scheme, sorting: ["total", "game1"]});
  const ranked = multi.rankedResultRows(state(tiebroken), tiebroken, (i) => ["А", "Б"][i]);
  assertEquals(ranked.map((row) => [row.name, row.placeText]), [["А", "1"], ["Б", "2"]]);
});

Deno.test("a declined team keeps its row and leaves the ranking", () => {
  const rules = multi.rulesOf(scheme);
  const state = multi.parseState({
    participants: [{number: 1, name: "А"}, {number: 2, name: "Б"}],
    games: [{cells: [[1, 1, 1], [1, 1, 1]]}, {cells: [[0, 0], [0, 0]]}],
    declined: {n2: true},
  }, rules, []);
  const rows = multi.rankedResultRows(state, rules, (i) => ["А", "Б"][i]);
  assertEquals(rows.map((row) => row.name), ["А"]);
  assertEquals(rows[0].placeText, "1");
});
