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

Deno.test("a guest team ranks like any team and its Отказ is keyed on its own number", () => {
  const rules = multi.rulesOf(scheme);
  const state = multi.parseState({
    participants: [{number: 1, name: "А"}, {number: -1, name: "Гости"}, {number: -2, name: "Жюри"}],
    games: [{cells: [[1, 0, 0], [1, 1, 0], [1, 1, 1]]}, {cells: [[0, 0], [0, 0], [0, 0]]}],
    declined: {"n-2": true},
  }, rules, []);
  assertEquals([0, 1, 2].map((i) => multi.participantGuest(state, i)), [false, true, true]);
  assertEquals(multi.declinedKey(state, 1), "n-1");
  const rows = multi.rankedResultRows(state, rules, (i) => multi.participantName(state, i));
  assertEquals(rows.map((row) => row.name), ["Гости", "А"]);
});

Deno.test("a normalised мини-игра pays a share of the best in it, floored at zero", () => {
  const scheme = {
    minigames: [
      {name: "Эрудит", normalized: true, columns: [{values: [-10, 0, 10]}, {values: [-20, 0, 20]}]},
      {name: "Песни", normalized: true, columns: [{values: [0, 1]}, {values: [0, 1]}]},
    ],
  };
  const rules = multi.rulesOf(scheme);
  const state = multi.parseState({
    participants: [{number: 1, name: "А"}, {number: 2, name: "Б"}, {number: 3, name: "В"}, {number: 4, name: "Г"}],
    declined: {n4: true},
    games: [{cells: [[10, 20], [10, 0], [-10, -20], [10, 20]]}, {cells: [[1, 1], [1, 0], [0, 0], [1, 1]]}],
  }, rules, []);
  const sheet = multi.scoreSheet(state, rules);
  // А tops both: 30 of 30 and 2 of 2.
  assertEquals(sheet[0].games, [100, 100]);
  assertEquals(sheet[0].total, 200);
  // The raw numbers ride along — the sheet prints them under each block.
  assertEquals(sheet[0].raw, [30, 2]);
  // Б: 10 of 30, then 1 of 2.
  assertEquals(Math.round(sheet[1].games[0] * 100) / 100, 33.33);
  assertEquals(sheet[1].games[1], 50);
  // В finished on minus: nought, never below.
  assertEquals(sheet[2].games[0], 0);
  assertEquals(sheet[2].total, 0);
  // Г declined, so Г's 30 did not set the scale — А's did, and Г is unranked.
  assertEquals(multi.rankedResultRows(state, rules, (i) => "АБВГ"[i]).map((r) => r.name), ["А", "Б", "В"]);
});

Deno.test("a мини-игра nobody scored in pays nobody rather than dividing by zero", () => {
  const rules = multi.rulesOf({minigames: [{name: "Пусто", normalized: true, columns: [{values: [0, 1]}]}]});
  const state = multi.parseState({participants: [{number: 1, name: "А"}], games: [{cells: [[0]]}]}, rules, []);
  assertEquals(multi.scoreSheet(state, rules)[0].total, 0);
});

Deno.test("formatScore keeps a whole Итог whole and a normalised one to two places", () => {
  assertEquals(multi.formatScore(200), "200");
  assertEquals(multi.formatScore(186.7346), "186.73");
});

Deno.test("rankedResultRows ranks on the sum of places, as the server does", () => {
  // The Go test's fixture (TestComputeMultiResultsRanksOnTheSumOfPlaces).
  const values = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  const rules = multi.rulesOf({
    sorting: ["place_sum", "total"],
    minigames: [{name: "Фоторяд", columns: [{values}]}, {name: "Песни", columns: [{values}]}],
  });
  const state = multi.parseState({
    participants: ["А", "Б", "В", "Г"].map((name, i) => ({number: i + 1, name})),
    games: [{cells: [[10], [5], [0], [5]]}, {cells: [[0], [6], [6], [1]]}],
  }, rules, []);
  const ranked = multi.rankedResultRows(state, rules, (i) => ["А", "Б", "В", "Г"][i]);
  assertEquals(ranked.map((row) => [row.name, row.placeSum, row.placeText]),
    [["Б", 4, "1"], ["А", 5, "2"], ["В", 5.5, "3–4"], ["Г", 5.5, "3–4"]]);
  assertEquals(ranked[0].places, [2.5, 1.5]);
});
