import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/score-table.js";

// The module reads window/document lazily (never at import time); give it one
// shared fake window here.
globalThis.window = {};
globalThis.document = {activeElement: null, createElement: () => fakeCell()};


// fakeCell is a minimal stand-in for a DOM node: textContent + classList + value.
function fakeCell() {
  const classes = new Set();
  return {
    textContent: "",
    dataset: {},
    value: "",
    classList: {
      add: (...xs) => xs.forEach((x) => classes.add(x)),
      remove: (...xs) => xs.forEach((x) => classes.delete(x)),
      contains: (x) => classes.has(x),
    },
    // Minimal stubs for syncs that walk the DOM (e.g. the player popover lookup);
    // tests that need real traversal assert on the node directly instead.
    closest: () => null,
  };
}

// fakeIndex mimics what createScoreTableIndex returns, without a DOM: it carries
// the real `specs` (pass T.scoreCellSpecs(...) so patchScoreTable runs the real
// per-cell sync logic) and lets a test register cells under a spec name with
// their data-* coordinates. register() returns the cell so the test can assert
// what the sync wrote; eachNode/get drive patchScoreTable and lookups.
function fakeIndex(specs = []) {
  const byName = new Map(); // name -> [cell, ...]
  return {
    specs,
    register: (name, dataset = {}) => {
      const cell = fakeCell();
      for (const [k, v] of Object.entries(dataset)) cell.dataset[k] = String(v);
      if (!byName.has(name)) byName.set(name, []);
      byName.get(name).push(cell);
      return cell;
    },
    eachNode: (name, cb) => (byName.get(name) || []).forEach(cb),
    get: (name) => (byName.get(name) || [])[0] || null,
  };
}

const SCORE_OPTS = {entity: "team", shootout: true};

test("patchScoreTable writes shared value cells through the index", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const total0 = idx.register("total", {team: 0});
  const plus1 = idx.register("plus", {team: 1});
  const tiebreak0 = idx.register("tiebreak", {team: 0});
  const ccLow = idx.register("correctCount", {team: 0, valueIndex: 0});
  const ccHigh = idx.register("correctCount", {team: 0, valueIndex: 4});
  const themeScore = idx.register("themeScore", {team: 0, shootout: "0", theme: 1});
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 1, answer: 2});
  const state = {
    participants: [
      {total: 170, plus: 0, shootoutTotal: 7, correctCounts: [3, 0, 0, 0, 9],
        themes: [{score: 0, answers: []}, {score: 60, answers: ["", "", "right"]}], shootoutThemes: []},
      {total: 0, plus: 110, correctCounts: [0, 0, 0, 0, 0],
        themes: [{score: 0, answers: []}, {score: 0, answers: []}], shootoutThemes: []},
    ],
  };
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.equal(total0.textContent, "170");
  assert.equal(plus1.textContent, "110");
  assert.equal(tiebreak0.textContent, "7", "tiebreak prefers shootoutTotal over tiebreak");
  // correctCount columns render reversed: cell valueIndex i shows correctCounts[4 - i].
  assert.equal(ccLow.textContent, "9", "valueIndex 0 -> correctCounts[4]");
  assert.equal(ccHigh.textContent, "3", "valueIndex 4 -> correctCounts[0]");
  assert.equal(themeScore.textContent, "60");
  assert.ok(answer.classList.contains("right"));
});

test("patchScoreTable clears a stale mark before applying the new one", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 0, answer: 0});
  answer.classList.add("wrong");
  const state = {participants: [{total: 0, plus: 0, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: ["right"]}]}]};
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.ok(answer.classList.contains("right"));
  assert.ok(!answer.classList.contains("wrong"), "previous mark removed");
});

test("patchScoreTable syncs the per-round player name in place", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const player0 = idx.register("playerText", {team: 0, shootout: "0", theme: 0});
  const player1 = idx.register("playerText", {team: 0, shootout: "0", theme: 1});
  const state = {participants: [{total: 0, plus: 0, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: [], player: "Alice"}, {score: 0, answers: [], player: "Bob"}]}]};
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.equal(player0.textContent, "Alice", "player text patched from MatchView, not just marks");
  assert.equal(player1.textContent, "Bob");
});

// Guardrail for the class of bug this refactor fixes: any live cell must be in
// the single spec list with a sync, so it can never be rendered-but-not-synced.
test("scoreCellSpecs declares a sync for every live cell, incl. the player", () => {
  const synced = T.scoreCellSpecs(SCORE_OPTS).filter((s) => s.sync).map((s) => s.name);
  for (const name of ["answer", "themeScore", "total", "plus", "tiebreak", "correctCount",
    "playerText", "playerSelect"]) {
    assert.ok(synced.includes(name), `${name} must sync in place`);
  }
});

test("patchScoreTable tolerates cells missing from the index", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS)); // specs present, nothing registered
  assert.doesNotThrow(() =>
    T.patchScoreTable(idx, {participants: [{total: 1, plus: 1, correctCounts: [], themes: [], shootoutThemes: []}]}, {formatNumber: String}));
});

test("canPatchScoreShape: identical shape is patchable", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    participants: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  assert.equal(T.canPatchScoreShape(base, structuredClone(base)), true);
});

test("canPatchScoreShape: shape changes force a rebuild", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    participants: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  const cases = {
    "team count": (s) => s.participants.push({name: "Z", themes: [], shootoutThemes: []}),
    "team name": (s) => (s.participants[0].name = "X2"),
    "theme count": (s) => s.participants[0].themes.push({}),
    "shootout count": (s) => s.participants[0].shootoutThemes.push({}),
    "finished flag": (s) => (s.finished = true),
    "question values": (s) => (s.questionValues = [10, 20, 30]),
    "code": (s) => (s.code = "D"),
  };
  for (const [label, mutate] of Object.entries(cases)) {
    const next = structuredClone(base);
    mutate(next);
    assert.equal(T.canPatchScoreShape(base, next), false, `${label} change must rebuild`);
  }
  assert.equal(T.canPatchScoreShape(null, base), false);
  assert.equal(T.canPatchScoreShape(base, null), false);
});

test("computePlaces ranks by total with shared-rank ranges", () => {
  // 30, 20, 20, 10 -> "1", "2–3", "2–3", "4"
  assert.deepEqual(T.computePlaces([10, 20, 30, 20]), ["4", "2–3", "1", "2–3"]);
});

test("computePlaces breaks ties with the supplied comparator", () => {
  // Equal totals (20,20) split by tiebreak: lower tiebreak ranks higher.
  // compareTiebreak(a,b) > 0 means a ranks below b.
  const places = T.computePlaces([20, 20, 10], {
    tiebreaks: [2, 1, 0],
    compareTiebreak: (a, b) => b - a, // bigger tiebreak wins
  });
  assert.deepEqual(places, ["1", "2", "3"], "tiebreak separates the equal totals");
  // When tiebreaks also match, teams stay tied.
  const tied = T.computePlaces([20, 20], {tiebreaks: [5, 5], compareTiebreak: (a, b) => b - a});
  assert.deepEqual(tied, ["1–2", "1–2"]);
});
