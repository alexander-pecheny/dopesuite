import {assertEquals} from "jsr:@std/assert";
import * as troika from "./dist/troika-protocol.js";
import {computeTroikaPlayerStats} from "./dist/troika-stats.js";

// A тема as the sheet holds it: [вопрос][кресло].
function theme(order, ...questions) {
  return {order, answers: questions};
}
const none = ["", "", ""];

Deno.test("every correct answer pays the тема's нарицательная on its own", () => {
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
  assertEquals(troika.themeScore(state, 0, 0), 4);
  assertEquals(troika.themeScore(state, 0, 1), 2);
  assertEquals(troika.sideTotal(state, 0), 6);
  assertEquals(troika.sideTotal(state, 1), 0);
  assertEquals(troika.places(state), [1, 2]);
});

Deno.test("a ничья shares the place", () => {
  const one = {themes: [theme([1, 2, 3], ["right", "", ""], none, none)]};
  const state = troika.parseState({values: [1], sides: [one, structuredClone(one)]});
  assertEquals(troika.places(state), [1.5, 1.5]);
});

Deno.test("parseState sizes the бой from its values and fills the grid", () => {
  const state = troika.parseState({values: [1, 1, 3]});
  assertEquals(state.values, [1, 1, 3]);
  assertEquals(state.sides.length, 2);
  assertEquals(state.sides[0].themes.length, 3);
  assertEquals(state.sides[0].themes[0].answers, [none, none, none]);
  assertEquals(state.sides[0].themes[0].order, [0, 0, 0]);
  assertEquals(troika.started(state), false);
});

Deno.test("swapFrom rewrites the seating from a тема on, leaving the played ones", () => {
  const state = troika.parseState({values: [1, 1, 1]});
  troika.swapFrom(state, 0, 0, [7, 8, 9]);
  troika.swapFrom(state, 0, 2, [9, 7, 8]);
  assertEquals(state.sides[0].themes[0].order, [7, 8, 9]);
  assertEquals(state.sides[0].themes[1].order, [7, 8, 9]);
  assertEquals(state.sides[0].themes[2].order, [9, 7, 8]);
  assertEquals(troika.started(state), true);
});

// The three answer in turn and hear each other, so a correct answer is either
// the first on that вопрос or a repeat of one already on the table.
Deno.test("stats tell a first answer from a repeat, and rate the repeats", () => {
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
  assertEquals(by["Аня"].first, 1);
  assertEquals(by["Аня"].repeat, 0);
  assertEquals(by["Боря"].first, 1);
  assertEquals(by["Боря"].repeat, 1);
  // Вера had a right answer to repeat twice and took it once.
  assertEquals(by["Вера"].first, 0);
  assertEquals(by["Вера"].repeat, 1);
  assertEquals(by["Вера"].repeatChances, 2);
  assertEquals(by["Вера"].repeatRate, 0.5);
  // Sorted on first answers, then on repeats.
  assertEquals(rows.map((row) => row.player), ["Боря", "Аня", "Вера"]);
  assertEquals(by["Аня"].bouts, 1);
});

Deno.test("turnedAt is where either side sits differently from the тема before", () => {
  const state = troika.parseState({
    values: [1, 1, 1],
    sides: [
      {themes: [theme([1, 2, 3], none, none, none), theme([1, 2, 3], none, none, none), theme([2, 1, 3], none, none, none)]},
      {themes: [theme([4, 5, 6], none, none, none), theme([4, 5, 6], none, none, none), theme([4, 5, 6], none, none, none)]},
    ],
  });
  assertEquals([0, 1, 2].map((t) => troika.turnedAt(state, t)), [false, false, true]);
  troika.swapFrom(state, 1, 1, [5, 4, 6]);
  assertEquals([0, 1, 2].map((t) => troika.turnedAt(state, t)), [false, true, true]);
});
