import {assertEquals} from "jsr:@std/assert";
import * as hamsa from "./dist/hamsa-protocol.js";
import {computeHamsaPlayerStats} from "./dist/hamsa-stats.js";

// The rounds the регламент plays: five тем at ×1, ×2 and ×3, one at ×4, over
// base номиналы of 100..500.
const ROUNDS = [
  {themes: 5, values: [100, 200, 300, 400, 500]},
  {themes: 5, values: [200, 400, 600, 800, 1000]},
  {themes: 5, values: [300, 600, 900, 1200, 1500]},
  {themes: 1, values: [400, 800, 1200, 1600, 2000]},
];

// A тема as the sheet writes it: R взял, W потерял, - не играл.
function theme(marks, player = 0) {
  return {player, answers: [...marks].map((ch) => (ch === "R" ? "right" : ch === "W" ? "wrong" : ""))};
}

function themes(...pairs) {
  const grid = [];
  for (let t = 0; t < 16; t++) grid.push(theme("-----"));
  for (const [index, marks, player] of pairs) grid[index - 1] = theme(marks, player);
  return grid;
}

function doc(participants) {
  return {rounds: ROUNDS, participants};
}

Deno.test("a вопрос pays its раунд's номинал, and the ставка is whole", () => {
  const state = hamsa.parseState(doc({
    7: {themes: themes([1, "W---R"], [6, "--R--"]), bet: {amount: 250, answer: "right"}},
    8: {themes: themes([1, "R----"]), bet: {amount: 50, answer: "wrong"}},
  }), [7, 8]);
  // Тема 1 за 100..500: −100 + 500. Тема 6 — второй раунд, ×2: +600.
  assertEquals(hamsa.themeScore(state, 7, 0), 400);
  assertEquals(hamsa.themeScore(state, 7, 5), 600);
  assertEquals(hamsa.total(state, 7), 400 + 600 + 250);
  // Σ+ считает только взятое и ставку не трогает.
  assertEquals(hamsa.plus(state, 7), 500 + 600);
  assertEquals(hamsa.betScore(state, 7), 250);
  assertEquals(hamsa.total(state, 8), 100 - 50);
  assertEquals(hamsa.betScore(state, 8), -50);
});

Deno.test("равные очки делят места, и оба первых места считаются", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes([1, "R----"])},
    3: {themes: themes()},
    4: {themes: themes([1, "W----"])},
  }), [1, 2, 3, 4]);
  assertEquals(hamsa.placesFor(state, [1, 2, 3, 4]), [1.5, 1.5, 3, 4]);
  const rows = hamsa.rows(state, [1, 2, 3, 4]);
  assertEquals(rows.map((row) => row.total), [100, 100, 0, -100]);
});

Deno.test("перестрелка разводит равные суммы и в Σ не входит", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes([1, "R----"]), shootout: [theme("R----")]},
  }), [1, 2]);
  assertEquals(hamsa.total(state, 1), hamsa.total(state, 2));
  // Перестрелка играется на номиналах последнего раунда.
  assertEquals(hamsa.shootoutTotal(state, 2), 400);
  assertEquals(hamsa.placesFor(state, [1, 2]), [2, 1]);
});

Deno.test("место, назначенное ведущим, встаёт вместо посчитанного", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes(), pin: 1},
  }), [1, 2]);
  assertEquals(hamsa.placesFor(state, [1, 2]), [1, 1]);
});

Deno.test("parseState даёт строку каждому посаженному, даже пустому", () => {
  const state = hamsa.parseState(doc({1: {themes: themes([1, "R----"])}}), [1, 2, 3]);
  assertEquals(Object.keys(state.participants).sort(), ["1", "2", "3"]);
  assertEquals(state.participants["2"].themes.length, 16);
  assertEquals(hamsa.placesFor(state, [1, 2, 3]), [1, 2.5, 2.5]);
  assertEquals(hamsa.started(state), true);
  assertEquals(hamsa.started(hamsa.parseState(doc({}), [1])), false);
});

Deno.test("темы разложены по раундам, и каждый платит свой номинал", () => {
  const state = hamsa.parseState(doc({}), []);
  assertEquals(hamsa.themeCount(state), 16);
  assertEquals(hamsa.roundOfTheme(state, 0), 0);
  assertEquals(hamsa.roundOfTheme(state, 5), 1);
  assertEquals(hamsa.roundOfTheme(state, 15), 3);
  assertEquals(hamsa.themeValues(state, 15), [400, 800, 1200, 1600, 2000]);
  assertEquals(hamsa.baseValues(state), [100, 200, 300, 400, 500]);
  assertEquals(hamsa.shootoutValues(state), [400, 800, 1200, 1600, 2000]);
});

Deno.test("статистика считает игрока по темам, которые он сыграл", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R---W", 11], [6, "--R--", 12])},
  }), [1]);
  const rows = computeHamsaPlayerStats([{
    state,
    seats: [{id: 1, team: "Сарепта", players: new Map([[11, "Анна Белова"], [12, "Борис Черных"]])}],
  }]);
  assertEquals(rows.length, 2);
  // Анна: +100 за первый вопрос, −500 за пятый.
  assertEquals(rows.map((row) => [row.player, row.sum, row.plus, row.battles]), [
    ["Борис Черных", 600, 600, 1],
    ["Анна Белова", -400, 100, 1],
  ]);
  // Счётчики названы по базовому номиналу, а не по тому, что заплатил раунд.
  assertEquals(rows[0].right, [0, 0, 1, 0, 0]);
  assertEquals(rows[1].wrong, [0, 0, 0, 0, 1]);
});
