import {assertEquals} from "jsr:@std/assert";
import * as ek from "./dist/ek-protocol.js";

Deno.test("a bout's document is read per seat, padded to its themes", () => {
  const state = ek.parseState({participants: {
    "11": {themes: [{players: [101], answers: ["right", "wrong", "", "", ""]}], pin: 2},
    // A row written before Эрудит-секстет holds one `player`.
    "12": {themes: [{player: 201, answers: ["", "", "", "", "right"]}], shootoutThemes: [{answers: ["right"]}]},
  }}, [11, 12, 0], 3);
  assertEquals(state.sections.size, 2);
  const a = state.sections.get(11);
  assertEquals(a.themes.length, 3);
  assertEquals(a.themes[2], {players: [], answers: ["", "", "", "", ""]});
  assertEquals(a.pin, 2);
  assertEquals(state.sections.get(12).themes[0].players, [201]);
  const values = [10, 20, 30, 40, 50];
  assertEquals(ek.scoreSection(a, values), {total: -10, plus: 10, shootout: 0, correct: [1, 0, 0, 0, 0], wrong: [0, 1, 0, 0, 0]});
  assertEquals(ek.scoreSection(state.sections.get(12), values).shootout, 10);
});

Deno.test("an edit goes to the seat's section by its id", () => {
  assertEquals(ek.answerPath(11, "shootoutThemes", 0, 4), ["participants", "11", "shootoutThemes", 0, "answers", 4]);
  assertEquals(ek.playersPath(11, "themes", 2), ["participants", "11", "themes", 2, "players"]);
  assertEquals(ek.pinPath(12), ["participants", "12", "pin"]);
});
