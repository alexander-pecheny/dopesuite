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

test("with the reverse shootout the shootout themes after the first run from 50 down", () => {
  const cols = (reverse) => ek.sheetColumns(1, 3, reverse).map(({cellKind, theme, q}) => `${cellKind === "themes" ? "T" : "П"}${theme + 1}:${q}`).join(" ");
  assert.equal(cols(false), "T1:0 T1:1 T1:2 T1:3 T1:4 П1:0 П1:1 П1:2 П1:3 П1:4 П2:0 П2:1 П2:2 П2:3 П2:4 П3:0 П3:1 П3:2 П3:3 П3:4");
  assert.equal(cols(true), "T1:0 T1:1 T1:2 T1:3 T1:4 П1:0 П1:1 П1:2 П1:3 П1:4 П2:4 П2:3 П2:2 П2:1 П2:0 П3:4 П3:3 П3:2 П3:1 П3:0");
  assert.equal(ek.readsBackwards("themes", 1, true), false);
  assert.equal(ek.readsBackwards("shootoutThemes", 0, true), false);
  assert.equal(ek.readsBackwards("shootoutThemes", 1, true), true);
  assert.deepEqual(ek.questionOrder(true), [4, 3, 2, 1, 0]);
});
