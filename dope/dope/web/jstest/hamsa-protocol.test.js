import {test} from "node:test";
import assert from "node:assert/strict";
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

test("a вопрос pays its раунд's номинал, and the ставка is whole", () => {
  const state = hamsa.parseState(doc({
    7: {themes: themes([1, "W---R"], [6, "--R--"]), bet: {amount: 250, answer: "right"}},
    8: {themes: themes([1, "R----"]), bet: {amount: 50, answer: "wrong"}},
  }), [7, 8]);
  // Тема 1 за 100..500: −100 + 500. Тема 6 — второй раунд, ×2: +600.
  assert.deepEqual(hamsa.themeScore(state, 7, 0), 400);
  assert.deepEqual(hamsa.themeScore(state, 7, 5), 600);
  assert.deepEqual(hamsa.total(state, 7), 400 + 600 + 250);
  // Σ+ считает только взятое и ставку не трогает.
  assert.deepEqual(hamsa.plus(state, 7), 500 + 600);
  assert.deepEqual(hamsa.betScore(state, 7), 250);
  assert.deepEqual(hamsa.total(state, 8), 100 - 50);
  assert.deepEqual(hamsa.betScore(state, 8), -50);
});

test("равные очки делят места, и оба первых места считаются", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes([1, "R----"])},
    3: {themes: themes()},
    4: {themes: themes([1, "W----"])},
  }), [1, 2, 3, 4]);
  assert.deepEqual(hamsa.placesFor(state, [1, 2, 3, 4]), [1.5, 1.5, 3, 4]);
  const rows = hamsa.rows(state, [1, 2, 3, 4]);
  assert.deepEqual(rows.map((row) => row.total), [100, 100, 0, -100]);
});

test("перестрелка разводит равные суммы и в Σ не входит", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes([1, "R----"]), shootout: [theme("R----")]},
  }), [1, 2]);
  assert.deepEqual(hamsa.total(state, 1), hamsa.total(state, 2));
  // Перестрелка играется на номиналах последнего раунда.
  assert.deepEqual(hamsa.shootoutTotal(state, 2), 400);
  assert.deepEqual(hamsa.placesFor(state, [1, 2]), [2, 1]);
});

test("место, назначенное ведущим, встаёт вместо посчитанного", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes(), pin: 1},
  }), [1, 2]);
  assert.deepEqual(hamsa.placesFor(state, [1, 2]), [1, 1]);
});

test("parseState даёт строку каждому посаженному, даже пустому", () => {
  const state = hamsa.parseState(doc({1: {themes: themes([1, "R----"])}}), [1, 2, 3]);
  assert.deepEqual(Object.keys(state.participants).sort(), ["1", "2", "3"]);
  assert.deepEqual(state.participants["2"].themes.length, 16);
  assert.deepEqual(hamsa.placesFor(state, [1, 2, 3]), [1, 2.5, 2.5]);
  assert.deepEqual(hamsa.started(state), true);
  assert.deepEqual(hamsa.started(hamsa.parseState(doc({}), [1])), false);
});

test("темы разложены по раундам, и каждый платит свой номинал", () => {
  const state = hamsa.parseState(doc({}), []);
  assert.deepEqual(hamsa.themeCount(state), 16);
  assert.deepEqual(hamsa.roundOfTheme(state, 0), 0);
  assert.deepEqual(hamsa.roundOfTheme(state, 5), 1);
  assert.deepEqual(hamsa.roundOfTheme(state, 15), 3);
  assert.deepEqual(hamsa.themeValues(state, 15), [400, 800, 1200, 1600, 2000]);
  assert.deepEqual(hamsa.baseValues(state), [100, 200, 300, 400, 500]);
  assert.deepEqual(hamsa.shootoutValues(state), [400, 800, 1200, 1600, 2000]);
});

test("статистика считает игрока по темам, которые он сыграл", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R---W", 11], [6, "--R--", 12])},
  }), [1]);
  const rows = computeHamsaPlayerStats([{
    state,
    seats: [{id: 1, team: "Сарепта", players: new Map([[11, "Анна Белова"], [12, "Борис Черных"]])}],
  }]);
  assert.deepEqual(rows.length, 2);
  // Анна: +100 за первый вопрос, −500 за пятый.
  assert.deepEqual(rows.map((row) => [row.player, row.sum, row.plus, row.battles]), [
    ["Борис Черных", 600, 600, 1],
    ["Анна Белова", -400, 100, 1],
  ]);
  // Счётчики названы по базовому номиналу, а не по тому, что заплатил раунд.
  assert.deepEqual(rows[0].right, [0, 0, 1, 0, 0]);
  assert.deepEqual(rows[1].wrong, [0, 0, 0, 0, 1]);
});

test("счётчики по вопросам считают взятое по позиции, без ставки и перестрелки", () => {
  const state = hamsa.parseState(doc({
    3: {
      themes: themes([1, "R---R"], [6, "R-R--"], [16, "----R"]),
      bet: {amount: 1000, answer: "right"},
      shootout: [theme("RRRRR")],
    },
  }), [3]);
  // Позиция 1: темы 1 и 6. Позиция 3: тема 6. Позиция 5: темы 1 и 16.
  assert.deepEqual(hamsa.correctCounts(state, 3), [2, 0, 1, 0, 2]);
  assert.deepEqual(hamsa.rows(state, [3])[0].correct, [2, 0, 1, 0, 2]);
});

test("жребий среди равных читается из документа и места не меняет", () => {
  const state = hamsa.parseState(doc({
    1: {themes: themes([1, "R----"])},
    2: {themes: themes(), lot: 2},
    3: {themes: themes(), lot: 1},
    4: {themes: themes([1, "W----"]), lot: 0},
  }), [1, 2, 3, 4]);
  const rows = hamsa.rows(state, [1, 2, 3, 4]);
  assert.deepEqual(rows.map((row) => row.place), [1, 2.5, 2.5, 4]);
  assert.deepEqual(rows.map((row) => row.tie), [1, 2, 2, 1]);
  assert.deepEqual(rows.map((row) => row.lot), [null, 2, 1, null]);
  assert.deepEqual(hamsa.started(hamsa.parseState(doc({1: {lot: 1}}), [1])), true);
});

test("у несыгранного боя мест ещё нет", () => {
  const state = hamsa.parseState(doc({}), [1, 2, 3, 4]);
  assert.deepEqual(hamsa.rows(state, [1, 2, 3, 4]).map((row) => row.place), [0, 0, 0, 0]);
});
