import assert from "node:assert/strict";
import {loadStaticModule, fakeIndex, fakeLocalStorage} from "./browser-module.js";

const T = loadStaticModule("match-table.js").DopeTable;

const SCORE_OPTS = {entity: "team", shootout: true};

Deno.test("patchScoreTable writes shared value cells through the index", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const total0 = idx.register("total", {team: 0});
  const plus1 = idx.register("plus", {team: 1});
  const tiebreak0 = idx.register("tiebreak", {team: 0});
  const ccLow = idx.register("correctCount", {team: 0, valueIndex: 0});
  const ccHigh = idx.register("correctCount", {team: 0, valueIndex: 4});
  const themeScore = idx.register("themeScore", {team: 0, shootout: "0", theme: 1});
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 1, answer: 2});
  const state = {
    teams: [
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

Deno.test("patchScoreTable clears a stale mark before applying the new one", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const answer = idx.register("answer", {team: 0, shootout: "0", theme: 0, answer: 0});
  answer.classList.add("wrong");
  const state = {teams: [{total: 0, plus: 0, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: ["right"]}]}]};
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.ok(answer.classList.contains("right"));
  assert.ok(!answer.classList.contains("wrong"), "previous mark removed");
});

Deno.test("patchScoreTable syncs the per-round player name in place", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS));
  const player0 = idx.register("playerText", {team: 0, shootout: "0", theme: 0});
  const player1 = idx.register("playerText", {team: 0, shootout: "0", theme: 1});
  const state = {teams: [{total: 0, plus: 0, correctCounts: [], shootoutThemes: [],
    themes: [{score: 0, answers: [], player: "Alice"}, {score: 0, answers: [], player: "Bob"}]}]};
  T.patchScoreTable(idx, state, {formatNumber: String});
  assert.equal(player0.textContent, "Alice", "player text patched from MatchView, not just marks");
  assert.equal(player1.textContent, "Bob");
});

// Guardrail for the class of bug this refactor fixes: any live cell must be in
// the single spec list with a sync, so it can never be rendered-but-not-synced.
Deno.test("scoreCellSpecs declares a sync for every live cell, incl. the player", () => {
  const synced = T.scoreCellSpecs(SCORE_OPTS).filter((s) => s.sync).map((s) => s.name);
  for (const name of ["answer", "themeScore", "total", "plus", "tiebreak", "correctCount",
    "playerText", "playerSelect"]) {
    assert.ok(synced.includes(name), `${name} must sync in place`);
  }
});

Deno.test("patchScoreTable tolerates cells missing from the index", () => {
  const idx = fakeIndex(T.scoreCellSpecs(SCORE_OPTS)); // specs present, nothing registered
  assert.doesNotThrow(() =>
    T.patchScoreTable(idx, {teams: [{total: 1, plus: 1, correctCounts: [], themes: [], shootoutThemes: []}]}, {formatNumber: String}));
});

Deno.test("canPatchScoreShape: identical shape is patchable", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    teams: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  assert.equal(T.canPatchScoreShape(base, structuredClone(base)), true);
});

Deno.test("canPatchScoreShape: shape changes force a rebuild", () => {
  const base = {code: "C", finished: false, questionValues: [10, 20],
    teams: [{name: "X", themes: [{}, {}], shootoutThemes: []}, {name: "Y", themes: [{}, {}], shootoutThemes: []}]};
  const cases = {
    "team count": (s) => s.teams.push({name: "Z", themes: [], shootoutThemes: []}),
    "team name": (s) => (s.teams[0].name = "X2"),
    "theme count": (s) => s.teams[0].themes.push({}),
    "shootout count": (s) => s.teams[0].shootoutThemes.push({}),
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

Deno.test("applyDeltaOps applies set-ops to a clone without mutating the base", () => {
  const base = {seq: 1, revision: 5, teams: [{total: 10}]};
  const next = T.applyDeltaOps(base, [
    {op: "set", path: ["teams", 0, "total"], value: 20},
    {op: "set", path: ["revision"], value: 6},
  ]);
  assert.equal(next.teams[0].total, 20);
  assert.equal(next.revision, 6);
  assert.equal(base.teams[0].total, 10, "base.teams not mutated");
  assert.equal(base.revision, 5, "base.revision not mutated");
});

Deno.test("applyDeltaOps skips non-set ops", () => {
  const next = T.applyDeltaOps({a: 1}, [{op: "delete", path: ["a"]}, {op: "set", path: ["b"], value: 2}]);
  assert.equal(next.a, 1);
  assert.equal(next.b, 2);
});

Deno.test("createPendingOps overlays un-acked edits and coalesces by path", () => {
  const p = T.createPendingOps();
  p.add(["teams", 0, "themes", 1, "answers", 2], "right");
  p.add(["teams", 0, "themes", 1, "answers", 2], "wrong"); // same path: last write wins
  p.add(["teams", 1, "player"], "Bob");
  const base = {teams: [{themes: [{}, {answers: ["", "", ""]}]}, {player: ""}]};
  const overlaid = p.overlay(base);
  assert.equal(overlaid.teams[0].themes[1].answers[2], "wrong");
  assert.equal(overlaid.teams[1].player, "Bob");
  assert.equal(base.teams[0].themes[1].answers[2], "", "base not mutated");
  assert.equal(p.queued(), 2, "two distinct paths queued");
});

Deno.test("createPendingOps: ack drops confirmed ops, requeue keeps them, newer queued wins", () => {
  const p = T.createPendingOps();
  p.add(["a"], 1);
  const sent = p.take(); // a:1 now in-flight
  assert.equal(p.queued(), 0);
  assert.equal(p.inFlightCount(), 1);
  // A newer edit to the same path lands while the first is in flight.
  p.add(["a"], 2);
  p.ack(sent); // server confirmed a:1; drop only it, keep the queued a:2
  assert.equal(p.inFlightCount(), 0);
  assert.equal(p.overlay({}).a, 2, "newer queued value survives ack of the in-flight one");
  // Requeue of a stale op must not clobber the newer queued op for the same path.
  const sent2 = p.take();
  p.add(["a"], 3);
  p.ack(sent2);
  p.requeue(sent2); // sent2 is a:2; queue already has a:3 → keep a:3
  assert.equal(p.overlay({}).a, 3);
});

Deno.test("createPendingOps.has reports un-acked paths (queued then in flight, cleared on ack)", () => {
  const p = T.createPendingOps();
  const path = ["themes", 0, "answers", 1, 2];
  assert.equal(p.has(path), false);
  p.add(path, "right");
  assert.equal(p.has(path), true, "queued edit is pending");
  const sent = p.take(); // moved to in-flight
  assert.equal(p.has(path), true, "in-flight edit is still pending");
  assert.equal(p.has(["themes", 0, "answers", 1, 3]), false, "a different cell is not pending");
  p.ack(sent);
  assert.equal(p.has(path), false, "cleared once the server confirms it");
});

Deno.test("createPendingOps.has marks cells under a coarse ancestor op (OD whole-array patch)", () => {
  const p = T.createPendingOps();
  p.add(["entries"], [[1, 2]]); // OD patches the whole entries array in some flows
  assert.equal(p.has(["entries", 3, 0]), true, "a cell under the patched subtree is pending");
  assert.equal(p.has(["entries"]), true, "the patched path itself is pending");
  assert.equal(p.has(["shootoutRounds", 0]), false, "an unrelated subtree is not pending");
});

Deno.test("createPendingOps persists un-acked edits and rehydrates them on a fresh instance", () => {
  const mod = loadStaticModule("match-table.js");
  mod.localStorage = fakeLocalStorage();
  const ops = mod.DopeTable.createPendingOps;
  const key = "dope.pending:game-state:2";

  const p1 = ops({storageKey: key});
  p1.add(["themes", 0, "answers", 1, 2], "right");
  p1.add(["themes", 0, "answers", 1, 3], "wrong");

  // A fresh instance (simulating a page reload) recovers the un-acked edits.
  const p2 = ops({storageKey: key});
  assert.equal(p2.queued(), 2, "recovered both un-acked edits");
  assert.equal(p2.has(["themes", 0, "answers", 1, 2]), true);
  const overlaid = p2.overlay({});
  assert.equal(overlaid.themes[0].answers[1][2], "right");
  assert.equal(overlaid.themes[0].answers[1][3], "wrong");

  // Once confirmed (take + ack), persistence is cleared and a later load is empty.
  p2.ack(p2.take());
  assert.equal(ops({storageKey: key}).queued(), 0, "nothing recovered after ack");
});

Deno.test("createPendingOps drops persisted edits past the TTL (no resurrecting ancient sessions)", () => {
  const mod = loadStaticModule("match-table.js");
  mod.localStorage = fakeLocalStorage();
  const key = "dope.pending:game-state:9";
  // Pre-seed an ancient op (ts near epoch) directly in storage.
  mod.localStorage.setItem(key, JSON.stringify([{op: "set", path: ["a"], value: 1, ts: 1}]));
  const p = mod.DopeTable.createPendingOps({storageKey: key, ttlMs: 1000});
  assert.equal(p.queued(), 0, "stale op past TTL is not recovered");
  assert.equal(mod.localStorage.getItem(key), null, "and the stale entry is purged");
});

Deno.test("createClientRecorder is a safe no-op when localStorage is unavailable", () => {
  // The test window has no localStorage; the recorder must degrade to disabled
  // and never throw, so it can never break a page where storage is blocked.
  const rec = T.createClientRecorder({scope: "game-state:2"});
  assert.equal(rec.enabled, false);
  assert.doesNotThrow(() => {
    rec.event("delta", {seq: 5});
    rec.snapshot("tick", {finished: false, themes: []});
  });
  const dump = rec.dump();
  assert.equal(dump.scope, "game-state:2");
  assert.ok(Array.isArray(dump.events) && Array.isArray(dump.snapshots));
});

Deno.test("computeEKPlayerStats aggregates per player across battles, regular themes only", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", teams: [
        {name: "Alpha", themes: [
          {player: "Ann", answers: ["right", "wrong", "", "", "right"]},
          {player: "Bob", answers: ["", "", "right", "", ""]},
          {player: "", answers: ["right", "right", "right", "right", "right"]},
        ], shootoutThemes: [
          {player: "Ann", answers: ["right", "right", "right", "right", "right"]},
        ]},
      ]},
    ]},
    {code: "r8", matches: [
      {code: "M", teams: [
        {name: "Alpha", themes: [
          {player: "Ann", answers: ["right", "", "", "", ""]},
        ]},
      ]},
    ]},
  ];
  const rows = T.computeEKPlayerStats(stages);
  assert.equal(rows.length, 2, "empty-player theme is skipped");
  const ann = rows[0];
  const bob = rows[1];
  assert.equal(ann.player, "Ann", "ordered by Σ desc");
  assert.equal(ann.sum, 50, "10+50-20 + 10, shootout excluded");
  assert.equal(ann.plus, 70, "10+50+10, no negatives");
  assert.equal(ann.battles, 2);
  assert.deepEqual(ann.right, [2, 0, 0, 0, 1]);
  assert.deepEqual(ann.wrong, [0, 1, 0, 0, 0]);
  assert.equal(ann.rightTotal, 3);
  assert.equal(bob.sum, 30);
  assert.equal(bob.battles, 1);
  // Team-share is each player's slice of the team's net Σ: Alpha total = 50 + 30 = 80.
  assert.equal(Math.round(ann.share * 100), 63); // 50/80
  assert.equal(Math.round(bob.share * 100), 38); // 30/80
});

Deno.test("computeEKPlayerStats team-share zeroes out non-helpers", () => {
  const stages = [
    {code: "r16", matches: [
      {code: "A", teams: [
        // Share is over POSITIVE contributors only; negatives are 0.
        {name: "Plus", themes: [
          {player: "Up", answers: ["right", "right", "", "", ""]},   // +30
          {player: "Down", answers: ["wrong", "", "", "", ""]},      // -10
        ]},
        // Net-negative team: the positive player still gets a share (its slice
        // of the team's positive points), the negative player is 0.
        {name: "Minus", themes: [
          {player: "Good", answers: ["right", "", "", "", ""]},      // +10
          {player: "Bad", answers: ["", "", "", "", "wrong"]},       // -50
        ]},
      ]},
    ]},
  ];
  const byName = Object.fromEntries(computeEKShareStats(stages));
  // Plus team positive-total = 30 (only Up). Up = 30/30 = 100%; Down is 0.
  assert.equal(Math.round(byName["Up"] * 100), 100);
  assert.equal(byName["Down"], 0);
  // Minus team positive-total = 10 (only Good). Good = 10/10 = 100% even though
  // the team net is negative; Bad (negative) is 0.
  assert.equal(Math.round(byName["Good"] * 100), 100);
  assert.equal(byName["Bad"], 0);

  function computeEKShareStats(s) {
    return T.computeEKPlayerStats(s).map((r) => [r.player, r.share]);
  }
});

Deno.test("createLocalCache round-trips JSON and degrades safely", () => {
  const win = loadStaticModule("match-table.js");
  const T = win.DopeTable;
  win.localStorage = fakeLocalStorage();
  const cache = T.createLocalCache("slot");
  assert.equal(cache.read(), null, "empty slot reads as null");
  cache.write({a: 1});
  assert.deepEqual(cache.read(), {a: 1});
  cache.write(null); // null is a no-op, must not clobber the stored value
  assert.deepEqual(cache.read(), {a: 1});
  win.localStorage.setItem("slot", "{not json");
  assert.equal(cache.read(), null, "corrupt JSON reads as null, not a throw");
});

Deno.test("gameEventsURL includes game_id only when present, encoded", () => {
  const T = loadStaticModule("match-table.js").DopeTable;
  assert.equal(T.gameEventsURL("f1"), "/events?fest_id=f1");
  assert.equal(T.gameEventsURL("f 1", "g/2"), "/events?fest_id=f%201&game_id=g%2F2");
});

Deno.test("createGameDataLoader hydrates from __GAME_INIT__, caches it, and revalidates", async () => {
  const win = loadStaticModule("match-table.js");
  const T = win.DopeTable;
  win.localStorage = fakeLocalStorage();
  win.__GAME_INIT__ = {scheme: {s: 1}, state: {t: 2}, fest: {f: 3}, seq: 5};
  const adopted = [];
  let revalidated = 0;
  const loader = T.createGameDataLoader({
    route: {festID: "f", gameID: "g", apiBase: "/api"},
    cachePrefix: "od",
    adopt: (snap, source) => adopted.push({snap, source}),
    revalidate: () => revalidated++,
  });
  await loader.load();
  assert.equal(adopted.length, 1);
  assert.equal(adopted[0].source, "init");
  assert.equal(adopted[0].snap.init.seq, 5, "raw init payload forwarded on the init path");
  assert.equal(win.__GAME_INIT__, null, "init payload consumed");
  assert.deepEqual(loader.cache.read(), {scheme: {s: 1}, state: {t: 2}, fest: {f: 3}}, "snapshot cached without the init envelope");
  for (let i = 0; i < 3; i++) await Promise.resolve(); // flush the detached revalidation
  assert.equal(revalidated, 1);
});

Deno.test("createGameDataLoader falls back to the localStorage snapshot", async () => {
  const win = loadStaticModule("match-table.js");
  const T = win.DopeTable;
  win.localStorage = fakeLocalStorage();
  win.__GAME_INIT__ = null;
  const route = {festID: "f", gameID: "g", apiBase: "/api"};
  T.createGameDataLoader({route, cachePrefix: "si", adopt: () => {}, revalidate: () => {}})
    .cache.write({scheme: {s: 1}, state: {t: 2}, fest: null});
  const seen = [];
  let revalidated = 0;
  const loader = T.createGameDataLoader({
    route,
    cachePrefix: "si",
    adopt: (_snap, source) => seen.push(source),
    revalidate: () => revalidated++,
  });
  await loader.load();
  assert.deepEqual(seen, ["cache"]);
  for (let i = 0; i < 3; i++) await Promise.resolve();
  assert.equal(revalidated, 1, "cache hit still kicks a background revalidation");
});

Deno.test("markNameOverflow flags only cells whose name is clipped", () => {
  const T = loadStaticModule("match-table.js").DopeTable;
  function cell(scrollWidth, clientWidth) {
    const classes = new Set();
    return {
      classList: {toggle: (c, on) => (on ? classes.add(c) : classes.delete(c))},
      has: (c) => classes.has(c),
      querySelector: () => ({scrollWidth, clientWidth}),
    };
  }
  const clipped = cell(100, 50);
  const fits = cell(40, 50);
  const root = {querySelectorAll: () => [clipped, fits]};
  T.markNameOverflow(root, {cellSelector: ".c", nameSelector: ".n", truncatedClass: "trunc"});
  assert.ok(clipped.has("trunc"), "name wider than its cell is flagged");
  assert.ok(!fits.has("trunc"), "name within 1px is not flagged");
});

Deno.test("computePlaces ranks by total with shared-rank ranges", () => {
  const T = loadStaticModule("match-table.js").DopeTable;
  // 30, 20, 20, 10 -> "1", "2–3", "2–3", "4"
  assert.deepEqual(T.computePlaces([10, 20, 30, 20]), ["4", "2–3", "1", "2–3"]);
});

Deno.test("computePlaces breaks ties with the supplied comparator", () => {
  const T = loadStaticModule("match-table.js").DopeTable;
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

Deno.test("createEpochTracker baselines the first epoch and flags real changes", () => {
  const T = loadStaticModule("match-table.js").DopeTable;
  const tracker = T.createEpochTracker();
  assert.equal(tracker.changed({epoch: ""}), false, "empty epoch ignored");
  assert.equal(tracker.changed({epoch: "a"}), false, "first epoch becomes baseline");
  assert.equal(tracker.epoch, "a");
  assert.equal(tracker.changed({epoch: "a"}), false, "same epoch is not a change");
  assert.equal(tracker.changed({}), false, "missing epoch ignored");
  assert.equal(tracker.changed({epoch: "b"}), true, "new epoch is a change");
});

Deno.test("notifyEmbeddedResize stays a no-op outside an embed", () => {
  const win = loadStaticModule("match-table.js");
  const T = win.DopeTable;
  let posted = 0;
  win.requestAnimationFrame = (cb) => cb();
  win.parent = {postMessage: () => posted++}; // a distinct parent frame...
  T.notifyEmbeddedResize(false); // ...but the page isn't the embed view
  assert.equal(posted, 0, "not embedded -> no postMessage");
  win.parent = win; // embed flag set, but there is no outer frame to message
  T.notifyEmbeddedResize(true);
  assert.equal(posted, 0, "no parent frame -> no postMessage");
});
