import {test} from "node:test";
import assert from "node:assert/strict";
import {createStateSync, createLiveEvents, applyDeltaOps, createPendingOps, createClientRecorder, createEpochTracker, gameEventsURL} from "./dist/state-sync.js";

// The engine reads window/document lazily. Sub-second waits (the resync jitter,
// the save debounce) fire straight away so no test ever waits; the wake-recovery
// backoff is queued instead, for runTimers() to release deliberately.
const timers = new Map();
let nextTimer = 0;
const lifecycle = new Map();

function bindLifecycle(type, fn) {
  if (!lifecycle.has(type)) lifecycle.set(type, []);
  lifecycle.get(type).push(fn);
}

globalThis.window = {
  setTimeout: (fn, ms) => {
    if (!ms || ms < 1000) {
      fn();
      return 0;
    }
    nextTimer += 1;
    timers.set(nextTimer, fn);
    return nextTimer;
  },
  clearTimeout: (id) => timers.delete(id),
  addEventListener: bindLifecycle,
};
globalThis.document = {addEventListener: bindLifecycle, visibilityState: "visible"};

// runTimers releases the queued timers, awaiting the async recovery they start.
async function runTimers() {
  const due = [...timers.values()];
  timers.clear();
  for (const fn of due) fn();
  await new Promise((r) => setTimeout(r, 0));
}

async function fire(type) {
  for (const fn of lifecycle.get(type) || []) fn();
  await new Promise((r) => setTimeout(r, 0));
}

function resetEnv() {
  timers.clear();
  lifecycle.clear();
  globalThis.document.visibilityState = "visible";
}

// fakeStream is the substitutable EventSource adapter: tests emit server events
// by calling the captured listeners. readyState is settable so a test can stage
// a live stream (1), one still connecting (0) or a dead one (2).
function fakeStream(readyState = 1) {
  const listeners = new Map();
  return {
    readyState,
    onerror: null,
    addEventListener(type, fn) {
      listeners.set(type, fn);
    },
    close() {
      this.readyState = 2;
    },
    emit(type, data) {
      listeners.get(type)?.({data: JSON.stringify(data)});
    },
  };
}

// Answers each call from `responses` in order (an exhausted list keeps answering
// with an empty 200). A null entry rejects, standing in for an offline attempt.
function fakeFetch(responses) {
  const calls = [];
  globalThis.fetch = (url, init) => {
    calls.push({url, init});
    const r = responses.length ? responses.shift() : {};
    if (r === null) return Promise.reject(new Error("offline"));
    return Promise.resolve({
      ok: true,
      headers: {get: (name) => r.headers?.[name] ?? null},
      json: () => Promise.resolve(r.body ?? {}),
      text: () => Promise.resolve(""),
    });
  };
  return calls;
}

function connectSync(overrides = {}) {
  resetEnv();
  const streams = [];
  const states = [];
  const statuses = [];
  const sync = createStateSync({
    scope: "game-state:1",
    stateURL: "/state",
    eventsURL: "/events",
    readonly: true,
    getState: () => (states.length ? states[states.length - 1] : {v: 0}),
    getInitialSeq: () => 5,
    getInitialEpoch: () => "e1",
    onRemoteState: (state) => states.push(state),
    setStatus: (state) => statuses.push(state),
    newEventSource: () => {
      const s = fakeStream();
      streams.push(s);
      return s;
    },
    ...overrides,
  });
  sync.connect();
  return {stream: streams[0], streams, states, statuses, sync};
}

test("delta chaining onto the seeded seq applies ops", () => {
  fakeFetch([]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 7}], seq: 6, prevSeq: 5, epoch: "e1"});
  assert.deepEqual(states, [{v: 7}]);
});

test("stale delta (seq <= applied) is ignored", () => {
  fakeFetch([]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 9}], seq: 5, prevSeq: 4, epoch: "e1"});
  assert.deepEqual(states, []);
});

test("seq gap triggers a full resync and realigns from headers", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "9", "X-State-Epoch": "e1"}, body: {v: 42}}]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 1}], seq: 8, prevSeq: 7, epoch: "e1"});
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/state");
  assert.deepEqual(states, [{v: 42}]);
  // The next delta chains onto the resynced seq.
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 2}], seq: 10, prevSeq: 9, epoch: "e1"});
  assert.deepEqual(states[1], {v: 2});
});

test("changed epoch resyncs instead of dropping post-restart deltas", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "1", "X-State-Epoch": "e2"}, body: {v: 100}}]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 1}], seq: 1, prevSeq: 0, epoch: "e2"});
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(calls.length, 1);
  assert.deepEqual(states, [{v: 100}]);
});

test("snapshot re-baselines unconditionally, even across an epoch change", () => {
  fakeFetch([]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:1", data: {v: 3}, seq: 2, epoch: "e2"});
  assert.deepEqual(states, [{v: 3}]);
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 4}], seq: 3, prevSeq: 2, epoch: "e2"});
  assert.deepEqual(states[1], {v: 4});
});

test("foreign-scope events are ignored", () => {
  fakeFetch([]);
  const {stream, states} = connectSync();
  stream.emit("state", {scope: "game-state:2", ops: [{op: "set", path: ["v"], value: 8}], seq: 6, prevSeq: 5, epoch: "e1"});
  assert.deepEqual(states, []);
});

test("a dead stream is re-opened and re-seeded when the tab returns", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "9"}, body: {v: 42}}]);
  const {stream, streams, states, statuses} = connectSync();
  stream.close();
  await fire("visibilitychange");
  assert.equal(streams.length, 2);
  assert.deepEqual(states, [{v: 42}]);
  assert.equal(calls.length, 1);
  assert.equal(statuses.at(-1), "saved");
});

test("a live stream is left alone when the tab returns", async () => {
  const calls = fakeFetch([]);
  const {streams} = connectSync();
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

test("recovery keeps retrying until the state is re-seeded", async () => {
  // The iOS case: the tab wakes with the radio still down, so the first attempts
  // fail and no further visibility event is coming to trigger another.
  const calls = fakeFetch([null, null, {headers: {"X-State-Seq": "9"}, body: {v: 42}}]);
  const {stream, states, statuses} = connectSync();
  stream.close();
  await fire("visibilitychange");
  assert.equal(calls.length, 1);
  assert.deepEqual(states, []);
  await runTimers();
  assert.equal(calls.length, 2);
  await runTimers();
  assert.equal(calls.length, 3);
  assert.deepEqual(states, [{v: 42}]);
  assert.equal(statuses.at(-1), "saved");
  await runTimers();
  assert.equal(calls.length, 3, "a re-seeded page ends the retry chain");
});

test("a stream that reconnects on its own is still re-seeded", async () => {
  const calls = fakeFetch([null, {headers: {"X-State-Seq": "9"}, body: {v: 42}}]);
  const {stream, streams, states} = connectSync();
  stream.close();
  await fire("visibilitychange");
  // Native EventSource retry brings the socket back mid-chain. It looks live,
  // but the page is still on the state it never re-fetched, so recovery goes on.
  streams.at(-1).readyState = 1;
  await runTimers();
  assert.equal(calls.length, 2);
  assert.deepEqual(states, [{v: 42}]);
});

test("recovery stays off while the tab is hidden", async () => {
  const calls = fakeFetch([]);
  const {stream, streams} = connectSync();
  stream.close();
  globalThis.document.visibilityState = "hidden";
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

test("recovery stays off after the server locks down", async () => {
  const calls = fakeFetch([]);
  const {stream, streams} = connectSync({onLockdown: () => {}});
  stream.emit("lockdown", {});
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

test("createLiveEvents dispatches after the epoch guard and latches on reset", async () => {
  resetEnv();
  const stream = fakeStream();
  const seen = [];
  let reloads = 0;
  globalThis.window.location = {reload: () => reloads++};
  const live = createLiveEvents({
    eventsURL: () => "/events",
    onMessage: (message) => seen.push(message.scope),
    reload: () => Promise.resolve(),
    newEventSource: () => stream,
  });
  live.connect();
  stream.emit("state", {scope: "match:1:a", data: {}, epoch: "e1"});
  stream.emit("state", {scope: "match:1:b", data: {}, epoch: "e1"});
  assert.deepEqual(seen, ["match:1:a", "match:1:b"]);
  // Epoch flip: a jittered reload is scheduled, and nothing more is dispatched.
  stream.emit("state", {scope: "match:1:c", data: {}, epoch: "e2"});
  assert.deepEqual(seen, ["match:1:a", "match:1:b"]);
  await runTimers();
  assert.equal(reloads, 1);
});

test("createLiveEvents retries a reload that failed on wake", async () => {
  resetEnv();
  const streams = [];
  let fail = true;
  let ups = 0;
  let errors = 0;
  const live = createLiveEvents({
    eventsURL: () => "/events",
    onMessage: () => {},
    onUp: () => ups++,
    onRecoverError: () => errors++,
    reload: () => (fail ? Promise.reject(new Error("offline")) : Promise.resolve()),
    // The tab wakes on a stream that is still connecting; the re-opened one is live.
    newEventSource: () => {
      const s = fakeStream(streams.length ? 1 : 0);
      streams.push(s);
      return s;
    },
  });
  live.connect();
  await fire("visibilitychange");
  assert.equal(errors, 1);
  assert.equal(streams.length, 1, "a failed reload opens no stream");
  fail = false;
  await runTimers();
  assert.equal(ups, 1);
  assert.equal(streams.length, 2);
  await runTimers();
  assert.equal(streams.length, 2, "a re-seeded page ends the retry chain");
});

test("createLiveEvents lockdown closes the stream and notifies", () => {
  resetEnv();
  const stream = fakeStream();
  let locked = 0;
  const live = createLiveEvents({
    eventsURL: () => "/events",
    onMessage: () => {},
    onLockdown: () => locked++,
    reload: () => Promise.resolve(),
    newEventSource: () => stream,
  });
  live.connect();
  stream.emit("lockdown", {});
  assert.equal(locked, 1);
  assert.equal(stream.readyState, 2);
});

// fakeLocalStorage is an in-memory Storage stand-in for testing persistence;
// assign it to window.localStorage before exercising code that reads it.
function fakeLocalStorage() {
  const store = new Map();
  return {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    get length() {
      return store.size;
    },
  };
}

test("applyDeltaOps applies set-ops to a clone without mutating the base", () => {
  const base = {seq: 1, revision: 5, teams: [{total: 10}]};
  const next = applyDeltaOps(base, [
    {op: "set", path: ["teams", 0, "total"], value: 20},
    {op: "set", path: ["revision"], value: 6},
  ]);
  assert.equal(next.teams[0].total, 20);
  assert.equal(next.revision, 6);
  assert.equal(base.teams[0].total, 10, "base.teams not mutated");
  assert.equal(base.revision, 5, "base.revision not mutated");
});

test("applyDeltaOps skips non-set ops", () => {
  const next = applyDeltaOps({a: 1}, [{op: "delete", path: ["a"]}, {op: "set", path: ["b"], value: 2}]);
  assert.equal(next.a, 1);
  assert.equal(next.b, 2);
});

test("createPendingOps overlays un-acked edits and coalesces by path", () => {
  const p = createPendingOps();
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

test("createPendingOps: ack drops confirmed ops, requeue keeps them, newer queued wins", () => {
  const p = createPendingOps();
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

test("createPendingOps.has reports un-acked paths (queued then in flight, cleared on ack)", () => {
  const p = createPendingOps();
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

test("createPendingOps.has marks cells under a coarse ancestor op (OD whole-array patch)", () => {
  const p = createPendingOps();
  p.add(["entries"], [[1, 2]]); // OD patches the whole entries array in some flows
  assert.equal(p.has(["entries", 3, 0]), true, "a cell under the patched subtree is pending");
  assert.equal(p.has(["entries"]), true, "the patched path itself is pending");
  assert.equal(p.has(["shootoutRounds", 0]), false, "an unrelated subtree is not pending");
});

test("createPendingOps persists un-acked edits and rehydrates them on a fresh instance", () => {
  window.localStorage = fakeLocalStorage();
  const ops = createPendingOps;
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

test("createPendingOps drops persisted edits past the TTL (no resurrecting ancient sessions)", () => {
  window.localStorage = fakeLocalStorage();
  const key = "dope.pending:game-state:9";
  // Pre-seed an ancient op (ts near epoch) directly in storage.
  window.localStorage.setItem(key, JSON.stringify([{op: "set", path: ["a"], value: 1, ts: 1}]));
  const p = createPendingOps({storageKey: key, ttlMs: 1000});
  assert.equal(p.queued(), 0, "stale op past TTL is not recovered");
  assert.equal(window.localStorage.getItem(key), null, "and the stale entry is purged");
});

test("createClientRecorder is a safe no-op when localStorage is unavailable", () => {
  // With no localStorage the recorder must degrade to disabled and never throw,
  // so it can never break a page where storage is blocked. (The window is shared
  // across tests now, so drop the storage an earlier test installed.)
  window.localStorage = undefined;
  const rec = createClientRecorder({scope: "game-state:2"});
  assert.equal(rec.enabled, false);
  assert.doesNotThrow(() => {
    rec.event("delta", {seq: 5});
    rec.snapshot("tick", {finished: false, themes: []});
  });
  const dump = rec.dump();
  assert.equal(dump.scope, "game-state:2");
  assert.ok(Array.isArray(dump.events) && Array.isArray(dump.snapshots));
});

test("gameEventsURL includes game_id only when present, encoded", () => {
  assert.equal(gameEventsURL("f1"), "/events?fest_id=f1");
  assert.equal(gameEventsURL("f 1", "g/2"), "/events?fest_id=f%201&game_id=g%2F2");
});

test("createEpochTracker baselines the first epoch and flags real changes", () => {
  const tracker = createEpochTracker();
  assert.equal(tracker.changed({epoch: ""}), false, "empty epoch ignored");
  assert.equal(tracker.changed({epoch: "a"}), false, "first epoch becomes baseline");
  assert.equal(tracker.epoch, "a");
  assert.equal(tracker.changed({epoch: "a"}), false, "same epoch is not a change");
  assert.equal(tracker.changed({}), false, "missing epoch ignored");
  assert.equal(tracker.changed({epoch: "b"}), true, "new epoch is a change");
});
