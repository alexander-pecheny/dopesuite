import {test} from "node:test";
import assert from "node:assert/strict";
import * as ek from "./dist/ek-protocol.js";

test("a bout's document is read per seat, padded to its themes", () => {
  const state = ek.parseState({participants: {
    "11": {themes: [{players: [101], answers: ["right", "wrong", "", "", ""]}], pin: 2},
    // A row written before Эрудит-секстет holds one `player`.
    "12": {themes: [{player: 201, answers: ["", "", "", "", "right"]}], shootoutThemes: [{answers: ["right"]}]},
  }}, [11, 12, 0], 3);
  assert.deepEqual(state.sections.size, 2);
  const a = state.sections.get(11);
  assert.deepEqual(a.themes.length, 3);
  assert.deepEqual(a.themes[2], {players: [], answers: ["", "", "", "", ""]});
  assert.deepEqual(a.pin, 2);
  assert.deepEqual(state.sections.get(12).themes[0].players, [201]);
  const values = [10, 20, 30, 40, 50];
  assert.deepEqual(ek.scoreSection(a, values), {total: -10, plus: 10, shootout: 0, correct: [1, 0, 0, 0, 0], wrong: [0, 1, 0, 0, 0]});
  assert.deepEqual(ek.scoreSection(state.sections.get(12), values).shootout, 10);
});

test("an edit goes to the seat's section by its id", () => {
  assert.deepEqual(ek.answerPath(11, "shootoutThemes", 0, 4), ["participants", "11", "shootoutThemes", 0, "answers", 4]);
  assert.deepEqual(ek.playersPath(11, "themes", 2), ["participants", "11", "themes", 2, "players"]);
  assert.deepEqual(ek.pinPath(12), ["participants", "12", "pin"]);
});
