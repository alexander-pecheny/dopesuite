import {test} from "node:test";
import assert from "node:assert/strict";
import {createStageCache} from "./dist/stage-cache.js";

// makeCache wires a cache to a mutable stage list with no real DOM (no pane is
// built, so the DOM-touching paths are never reached). setStages swaps the
// structure the callbacks report, simulating a fest edit.
function makeCache(initialStages) {
  let stages = initialStages;
  const cache = createStageCache({
    container: {children: [], appendChild() {}},
    apiBase: () => "/api",
    schemeStages: () => stages,
    findStage: (code) => stages.find((s) => s.code === code) || null,
    stageType: (s) => s?.type || "matches",
    getMatches: (s) => s?.matches || [],
    buildPaneContent: () => {},
    onStageDataChanged: () => {},
    onMatchUpdated: () => {},
    onPaneShown: () => {},
    cleanupPane: () => {},
  });
  return {cache, setStages: (s) => (stages = s)};
}

test("adoptFest keeps cached match state across a same-structure revision bump", () => {
  const {cache} = makeCache([{code: "r16", matches: [{code: "A"}, {code: "B"}]}]);
  cache.adoptFest({revision: 1});
  cache.applyMatchUpdate({code: "A", seq: 3, total: 42});
  assert.equal(cache.matchState("A").total, 42);
  // Every score edit bumps the fest revision; the structure is unchanged.
  cache.adoptFest({revision: 2});
  assert.ok(cache.matchState("A"), "cache must survive a pure revision bump (no skeleton flash)");
  assert.equal(cache.matchState("A").total, 42);
});

test("adoptFest drops caches when the stage/match structure changes", () => {
  const {cache, setStages} = makeCache([{code: "r16", matches: [{code: "A"}, {code: "B"}]}]);
  cache.adoptFest({revision: 1});
  cache.applyMatchUpdate({code: "A", seq: 3, total: 42});
  assert.ok(cache.matchState("A"));
  setStages([{code: "r16", matches: [{code: "A"}, {code: "B"}, {code: "C"}]}]); // a match appears
  cache.adoptFest({revision: 2});
  assert.equal(cache.matchState("A"), null, "structural change clears the cache");
});

test("adoptFest treats a stage-type change as structural", () => {
  const {cache, setStages} = makeCache([{code: "r16", type: "matches", matches: [{code: "A"}]}]);
  cache.adoptFest({revision: 1});
  cache.applyMatchUpdate({code: "A", seq: 1, total: 5});
  setStages([{code: "r16", type: "reseed", matches: [{code: "A"}]}]);
  cache.adoptFest({revision: 2});
  assert.equal(cache.matchState("A"), null, "stage type change clears the cache");
});

test("applyMatchUpdate is seq-monotonic: older updates are dropped", () => {
  const {cache} = makeCache([{code: "r16", matches: [{code: "A"}]}]);
  cache.adoptFest({revision: 1});
  cache.applyMatchUpdate({code: "A", seq: 5, total: 50});
  const stale = cache.applyMatchUpdate({code: "A", seq: 3, total: 30});
  assert.equal(stale.stale, true, "older seq reported stale");
  assert.equal(cache.matchState("A").total, 50, "older-seq update did not overwrite newer state");
  cache.applyMatchUpdate({code: "A", seq: 6, total: 60});
  assert.equal(cache.matchState("A").total, 60, "newer seq applies");
});

test("applyMatchUpdate ignores an unknown match code", () => {
  const {cache} = makeCache([{code: "r16", matches: [{code: "A"}]}]);
  cache.adoptFest({revision: 1});
  assert.equal(cache.applyMatchUpdate({code: "ZZZ", seq: 1}).found, false);
});
