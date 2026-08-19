import {test} from "node:test";
import assert from "node:assert/strict";
import * as brain from "./dist/brain-protocol.js";

test("parseState gives two sides of questions + tiebreaks rows with known marks", () => {
  const state = brain.parseState({tiebreaks: 1, teams: [{rows: [{player: "Аня", mark: "right"}, {mark: "maybe"}]}]}, 2);
  assert.equal(state.teams.length, 2);
  assert.deepEqual(state.teams[0].rows, [{player: "Аня", mark: "right"}, {player: "", mark: ""}, {player: "", mark: ""}]);
  assert.deepEqual(state.teams[1].rows, [{player: "", mark: ""}, {player: "", mark: ""}, {player: "", mark: ""}]);
  assert.equal(brain.parseState({tiebreaks: -2}, 1).tiebreaks, 0);
  assert.equal(brain.parseState("junk", 1).teams.length, 2);
});
