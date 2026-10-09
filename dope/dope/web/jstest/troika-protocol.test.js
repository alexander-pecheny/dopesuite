import {test} from "node:test";
import assert from "node:assert/strict";
import * as troika from "./dist/troika-protocol.js";
import {computeTroikaPlayerStats} from "./dist/troika-stats.js";

// A тема as the sheet holds it: [вопрос][кресло].
function theme(order, ...questions) {
  return {order, answers: questions};
}
const none = ["", "", ""];

test("every correct answer pays the тема's нарицательная on its own", () => {
  const state = troika.parseState({
    values: [1, 2],
    sides: [
      {themes: [
        theme([1, 2, 3], ["right", "right", "right"], ["right", "wrong", ""], none),
        theme([1, 2, 3], ["right", "", ""], none, none),
      ]},
      {themes: [
        theme([4, 5, 6], ["wrong", "wrong", "wrong"], none, none),
        theme([4, 5, 6], none, none, none),
      ]},
    ],
  });
  // Тема 1 за 1: три взятия плюс одно = 4. Тема 2 за 2: одно = 2.
  assert.deepEqual(troika.themeScore(state, 0, 0), 4);
  assert.deepEqual(troika.themeScore(state, 0, 1), 2);
  assert.deepEqual(troika.sideTotal(state, 0), 6);
  assert.deepEqual(troika.sideTotal(state, 1), 0);
});

test("a ничья is level", () => {
  const one = {themes: [theme([1, 2, 3], ["right", "", ""], none, none)]};
  const state = troika.parseState({values: [1], sides: [one, structuredClone(one)]});
  assert.deepEqual(troika.level(state), true);
});

test("parseState sizes the бой from its values and fills the grid", () => {
  const state = troika.parseState({values: [1, 1, 3]});
  assert.deepEqual(state.values, [1, 1, 3]);
  assert.deepEqual(state.sides.length, 2);
  assert.deepEqual(state.sides[0].themes.length, 3);
  assert.deepEqual(state.sides[0].themes[0].answers, [none, none, none]);
  assert.deepEqual(state.sides[0].themes[0].order, [0, 0, 0]);
  assert.deepEqual(troika.started(state), false);
});

test("swapFrom rewrites the seating from a тема on, leaving the played ones", () => {
  const state = troika.parseState({values: [1, 1, 1]});
  troika.swapFrom(state, 0, 0, [7, 8, 9]);
  troika.swapFrom(state, 0, 2, [9, 7, 8]);
  assert.deepEqual(state.sides[0].themes[0].order, [7, 8, 9]);
  assert.deepEqual(state.sides[0].themes[1].order, [7, 8, 9]);
  assert.deepEqual(state.sides[0].themes[2].order, [9, 7, 8]);
  assert.deepEqual(troika.started(state), true);
});

// The three answer in turn and hear each other, so a correct answer is either
// the first on that вопрос or a repeat of one already on the table.
test("stats tell a first answer from a repeat, rate both, and sit the кресла", () => {
  const state = troika.parseState({
    values: [1],
    sides: [{themes: [theme([1, 2, 3],
      // Аня взяла первой, Боря повторил, Вера не стала.
      ["right", "right", "wrong"],
      // Аня не взяла, Боря взял первым, Вера повторила.
      ["wrong", "right", "right"],
      // Никто ничего не взял: повторять было нечего.
      ["wrong", "wrong", "wrong"])]}, {themes: [theme([4, 5, 6], none, none, none)]}],
  });
  const rows = computeTroikaPlayerStats([{
    state,
    sides: [
      {team: "Тройка", players: new Map([[1, "Аня"], [2, "Боря"], [3, "Вера"]])},
      {team: "Другая", players: new Map([[4, "Г"], [5, "Д"], [6, "Е"]])},
    ],
  }]);
  const by = Object.fromEntries(rows.map((row) => [row.player, row]));
  assert.deepEqual(by["Аня"].first, 1);
  assert.deepEqual(by["Аня"].repeat, 0);
  assert.deepEqual(by["Боря"].first, 1);
  assert.deepEqual(by["Боря"].repeat, 1);
  // Вера had a right answer to repeat twice and took it once.
  assert.deepEqual(by["Вера"].first, 0);
  assert.deepEqual(by["Вера"].repeat, 1);
  assert.deepEqual(by["Вера"].repeatChances, 2);
  assert.deepEqual(by["Вера"].repeatRate, 0.5);
  // Every answer counts: Боря answered three, two of them right.
  assert.deepEqual(by["Боря"].questions, 3);
  assert.deepEqual(by["Боря"].correct, 2);
  assert.deepEqual(by["Боря"].correctRate, 2 / 3);
  // The кресла each sat in for the тема the side played; the other side
  // played nothing and has no rows.
  assert.deepEqual(by["Вера"].chairs, [0, 0, 1]);
  assert.deepEqual(by["Г"], undefined);
  // Sorted on points, then right answers, then first answers.
  assert.deepEqual(rows.map((row) => row.player), ["Боря", "Аня", "Вера"]);
  assert.deepEqual(by["Аня"].bouts, 1);
});

test("turnedAt is where either side sits differently from the тема before", () => {
  const state = troika.parseState({
    values: [1, 1, 1],
    sides: [
      {themes: [theme([1, 2, 3], none, none, none), theme([1, 2, 3], none, none, none), theme([2, 1, 3], none, none, none)]},
      {themes: [theme([4, 5, 6], none, none, none), theme([4, 5, 6], none, none, none), theme([4, 5, 6], none, none, none)]},
    ],
  });
  assert.deepEqual([0, 1, 2].map((t) => troika.turnedAt(state, t)), [false, false, true]);
  troika.swapFrom(state, 1, 1, [5, 4, 6]);
  assert.deepEqual([0, 1, 2].map((t) => troika.turnedAt(state, t)), [false, true, true]);
});

test("a бой of three is level when two of its sides are", () => {
  const one = {themes: [theme([1, 2, 3], ["right", "", ""], none, none)]};
  const two = {themes: [theme([4, 5, 6], ["right", "right", ""], none, none)]};
  const state = troika.parseState({values: [1], sides: [one, two, structuredClone(one)]}, 3);
  assert.deepEqual(state.sides.length, 3);
  assert.deepEqual(troika.level(state), true);
});

test("parseState pads to the seats the бой has", () => {
  const state = troika.parseState({values: [1]}, 3);
  assert.deepEqual(state.sides.length, 3);
  assert.deepEqual(state.sides[2].themes[0].answers, [none, none, none]);
});

test("a перестрелка тема counts and is told apart from the бой's own", () => {
  const side = (mark) => ({themes: [
    theme([1, 2, 3], ["right", "", ""], none, none),
    theme([1, 2, 3], [mark, "", ""], none, none),
  ]});
  const state = troika.parseState({values: [1, 1], shootout: 1, sides: [side("right"), side("")]});
  assert.deepEqual(troika.isShootoutTheme(state, 0), false);
  assert.deepEqual(troika.isShootoutTheme(state, 1), true);
  assert.deepEqual(troika.level(state), false);
});

test("a pinned place is read off the document", () => {
  const one = {themes: [theme([1, 2, 3], ["right", "", ""], none, none)]};
  const state = troika.parseState({values: [1], sides: [one, structuredClone(one)], pin: [2, 1]});
  assert.deepEqual(state.pin, [2, 1]);
});

test("a written бой counts right answers per вопрос at the тема's value", () => {
  const state = troika.parseState({
    values: [1, 3], written: true,
    sides: [{counts: [[3, 2, 0], [0, 0, 1]]}, {counts: [[1, 1, 1], [9, -1, "x"]]}],
  }, 2);
  assert.deepEqual(state.written, true);
  assert.deepEqual(state.sides[0].themes, []);
  // A count is 0..3: nine is three, the rest nothing.
  assert.deepEqual(state.sides[1].counts[1], [3, 0, 0]);
  assert.deepEqual(troika.sideTotal(state, 0), 5 + 3);
  assert.deepEqual(troika.sideTotal(state, 1), 3 + 9);
  assert.deepEqual(troika.started(state), true);
});

test("a turning бой seats its second half with the пристяжные changed over", () => {
  const state = troika.parseState({values: [1, 1, 1, 1, 1, 1], swap: 3});
  assert.deepEqual(state.swap, 3);
  troika.swapFrom(state, 0, 0, [7, 8, 9]);
  assert.deepEqual(state.sides[0].themes.map((t) => t.order), [
    [7, 8, 9], [7, 8, 9], [7, 8, 9], [8, 7, 9], [8, 7, 9], [8, 7, 9],
  ]);
  // A замена before the turn is turned with the rest of the half.
  troika.swapFrom(state, 0, 1, [7, 5, 9]);
  assert.deepEqual(state.sides[0].themes[2].order, [7, 5, 9]);
  assert.deepEqual(state.sides[0].themes[3].order, [5, 7, 9]);
  // One set at or after the turn is already the second half's.
  troika.swapFrom(state, 0, 4, [1, 2, 3]);
  assert.deepEqual(state.sides[0].themes[3].order, [5, 7, 9]);
  assert.deepEqual(state.sides[0].themes[5].order, [1, 2, 3]);
  assert.deepEqual(troika.turnedAt(state, 3), true);
});

test("parseState keeps no turn outside the бой", () => {
  assert.deepEqual(troika.parseState({values: [1, 1], swap: 2}).swap, 0);
  assert.deepEqual(troika.parseState({values: [1, 1], swap: 1, written: true}).swap, 0);
  assert.deepEqual(troika.parseState({values: [1, 1]}).swap, 0);
});

test("finishing marks wrong the blanks of a played тема, and nothing else", () => {
  const state = troika.parseState({values: [1, 1], sides: [
    // Side 0 played тема 1 (one right answer), left тема 2 untouched.
    {themes: [theme([1, 2, 3], ["right", "", ""], none, none), theme([1, 2, 3], none, none, none)]},
    // Side 1 played тема 1, its seating not recorded.
    {themes: [theme([0, 0, 0], none, ["", "wrong", ""], none), theme([0, 0, 0], none, none, none)]},
  ]});
  const cells = troika.unmarkedInPlayedThemes(state);
  const key = (c) => `${c.side}:${c.theme}:${c.question}:${c.chair}`;
  // Each side's тема 1: 9 cells, one marked. Neither side's тема 2.
  assert.deepEqual(cells.length, 8 + 8);
  assert.deepEqual(cells.some((c) => c.theme === 1), false);
  assert.deepEqual(cells.map(key).includes("0:0:0:0"), false, "a marked answer stays as it is");
  assert.deepEqual(troika.unmarkedInPlayedThemes(troika.parseState({values: [1], written: true})), []);
});
