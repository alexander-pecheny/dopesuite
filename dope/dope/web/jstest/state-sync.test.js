import {test} from "node:test";
import assert from "node:assert/strict";
import {createLiveEvents, createScopedWriter, createSyncIndicator, applyDeltaOps, createPendingOps, createClientRecorder, createEpochTracker, gameEventsURL} from "./dist/state-sync.js";

// The engine reads window/document lazily. By default sub-second waits (the
// resync jitter, the write debounce) fire straight away so no test ever waits,
// and the wake-recovery backoff is queued for runTimers() to release
// deliberately. holdShort() queues the short ones too, for the tests that
// assert on coalescing under the debounce window.
const timers = new Map();
let nextTimer = 0;
let holdShortTimers = false;
const lifecycle = new Map();

function bindLifecycle(type, fn) {
  if (!lifecycle.has(type)) lifecycle.set(type, []);
  lifecycle.get(type).push(fn);
}

globalThis.window = {
  setTimeout: (fn, ms) => {
    if (!holdShortTimers && (!ms || ms < 1000)) {
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

// runTimers releases the queued timers, awaiting the async work they start.
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
  holdShortTimers = false;
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
// with an empty 200). A null entry rejects, standing in for an offline attempt;
// {status: 400} answers a rejection.
function fakeFetch(responses) {
  const calls = [];
  globalThis.fetch = (url, init) => {
    calls.push({url, init, body: init?.body ? JSON.parse(init.body) : null});
    const r = responses.length ? responses.shift() : {};
    if (r === null) return Promise.reject(new Error("offline"));
    const status = r.status ?? 200;
    return Promise.resolve({
      ok: status < 300,
      status,
      headers: {get: (name) => r.headers?.[name] ?? null},
      json: () => Promise.resolve(r.body ?? {}),
      text: () => Promise.resolve(r.text ?? ""),
    });
  };
  return calls;
}

// A game-state page: one scope, one blob, resyncable through /state.
function connectGame(overrides = {}) {
  resetEnv();
  const streams = [];
  const states = [];
  const statuses = [];
  let seq = 5;
  const live = createLiveEvents({
    eventsURL: () => "/events",
    gameID: 1,
    epoch: "e1",
    scopes: [{
      prefix: "game-state:1",
      base: () => ({data: states.length ? states[states.length - 1] : {v: 0}, seq}),
      adopt: (_scope, view) => { seq = view.seq; states.push(view.data); },
      stateURL: () => "/state",
    }],
    indicator: createSyncIndicator((state) => statuses.push(state)),
    newEventSource: () => {
      const s = fakeStream();
      streams.push(s);
      return s;
    },
    ...overrides,
  });
  live.connect();
  return {stream: streams[0], streams, states, statuses, live, seqOf: () => seq};
}

test("delta chaining onto the seeded seq applies ops", () => {
  fakeFetch([]);
  const {stream, states, seqOf} = connectGame();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 7}], seq: 6, prevSeq: 5, epoch: "e1"});
  assert.deepEqual(states, [{v: 7}]);
  assert.equal(seqOf(), 6);
});

test("stale delta (seq <= applied) is ignored", () => {
  fakeFetch([]);
  const {stream, states} = connectGame();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 9}], seq: 5, prevSeq: 4, epoch: "e1"});
  assert.deepEqual(states, []);
});

test("seq gap triggers a full resync and realigns from headers", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "9", "X-State-Epoch": "e1"}, body: {v: 42}}]);
  const {stream, states} = connectGame();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 1}], seq: 8, prevSeq: 7, epoch: "e1"});
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/state");
  assert.deepEqual(states, [{v: 42}]);
  // The next delta chains onto the resynced seq.
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 2}], seq: 10, prevSeq: 9, epoch: "e1"});
  assert.deepEqual(states[1], {v: 2});
});

test("changed epoch resyncs a game-state page instead of dropping post-restart deltas", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "1", "X-State-Epoch": "e2"}, body: {v: 100}}]);
  const {stream, states} = connectGame();
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 1}], seq: 1, prevSeq: 0, epoch: "e2"});
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(calls.length, 1);
  assert.deepEqual(states, [{v: 100}]);
  // Post-resync deltas chain in the new epoch without another resync.
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 2}], seq: 2, prevSeq: 1, epoch: "e2"});
  assert.deepEqual(states[1], {v: 2});
  assert.equal(calls.length, 1);
});

test("snapshot re-baselines unconditionally, even across an epoch change", () => {
  const calls = fakeFetch([]);
  const {stream, states} = connectGame();
  stream.emit("state", {scope: "game-state:1", data: {v: 3}, seq: 7, epoch: "e2"});
  assert.deepEqual(states, [{v: 3}]);
  stream.emit("state", {scope: "game-state:1", ops: [{op: "set", path: ["v"], value: 4}], seq: 8, prevSeq: 7, epoch: "e2"});
  assert.deepEqual(states[1], {v: 4});
  assert.equal(calls.length, 0, "no resync: the snapshot carried the new epoch");
});

test("sibling-game and foreign-scope events are ignored", () => {
  fakeFetch([]);
  const unhandled = [];
  const {stream, states} = connectGame({onUnhandled: (m) => unhandled.push(m.scope)});
  stream.emit("state", {scope: "game-state:2", ops: [{op: "set", path: ["v"], value: 8}], seq: 6, prevSeq: 5, epoch: "e1"});
  stream.emit("state", {scope: "fest:1", data: {}, seq: 1, epoch: "e1"});
  assert.deepEqual(states, []);
  assert.deepEqual(unhandled, ["fest:1"], "a sibling game's state is dropped silently; anything else is the page's call");
});

test("a dead stream is re-opened and re-seeded when the tab returns", async () => {
  const calls = fakeFetch([{headers: {"X-State-Seq": "9"}, body: {v: 42}}]);
  const {stream, streams, states, statuses} = connectGame();
  stream.close();
  await fire("visibilitychange");
  assert.equal(streams.length, 2);
  assert.deepEqual(states, [{v: 42}]);
  assert.equal(calls.length, 1);
  assert.equal(statuses.at(-1), "saved");
});

test("a live stream is left alone when the tab returns", async () => {
  const calls = fakeFetch([]);
  const {streams} = connectGame();
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

test("recovery keeps retrying until the state is re-seeded", async () => {
  // The iOS case: the tab wakes with the radio still down, so the first attempts
  // fail and no further visibility event is coming to trigger another.
  const calls = fakeFetch([null, null, {headers: {"X-State-Seq": "9"}, body: {v: 42}}]);
  const {stream, states, statuses} = connectGame();
  stream.close();
  await fire("visibilitychange");
  assert.equal(calls.length, 1);
  assert.deepEqual(states, []);
  assert.equal(statuses.at(-1), "reconnecting");
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
  const {stream, streams, states} = connectGame();
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
  const {stream, streams} = connectGame();
  stream.close();
  globalThis.document.visibilityState = "hidden";
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

test("recovery stays off after the server locks down", async () => {
  const calls = fakeFetch([]);
  let locked = 0;
  const {stream, streams} = connectGame({onLockdown: () => locked++});
  stream.emit("lockdown", {});
  assert.equal(locked, 1);
  assert.equal(stream.readyState, 2);
  await fire("visibilitychange");
  assert.equal(streams.length, 1);
  assert.equal(calls.length, 0);
});

// A multi-view page: one scope family per бой, views cached by code, no
// state endpoint — a gap evicts, an epoch reset reloads the page.
function connectMatches(overrides = {}) {
  resetEnv();
  const stream = fakeStream();
  const cache = new Map();
  const gaps = [];
  const live = createLiveEvents({
    eventsURL: () => "/events",
    gameID: 1,
    scopes: [{
      prefix: "match:1:",
      base: (scope) => { const v = cache.get(scope); return v ? {data: v, seq: v.seq} : null; },
      adopt: (scope, view) => cache.set(scope, {...view.data, seq: view.seq}),
      gap: (scope) => gaps.push(scope),
    }],
    reload: () => Promise.resolve(),
    newEventSource: () => stream,
    ...overrides,
  });
  live.connect();
  return {stream, cache, gaps, live};
}

test("a delta whose prevSeq does not chain reports a gap for that scope only", () => {
  const {stream, cache, gaps} = connectMatches();
  stream.emit("state", {scope: "match:1:a", data: {code: "a", x: 1}, seq: 3, epoch: "e1"});
  stream.emit("state", {scope: "match:1:b", data: {code: "b", x: 1}, seq: 3, epoch: "e1"});
  stream.emit("state", {scope: "match:1:a", ops: [{path: ["x"], value: 2}], seq: 4, prevSeq: 3, epoch: "e1"});
  stream.emit("state", {scope: "match:1:b", ops: [{path: ["x"], value: 9}], seq: 6, prevSeq: 5, epoch: "e1"});
  stream.emit("state", {scope: "match:1:c", ops: [{path: ["x"], value: 1}], seq: 1, prevSeq: 0, epoch: "e1"});
  assert.deepEqual(cache.get("match:1:a"), {code: "a", x: 2, seq: 4});
  assert.deepEqual(cache.get("match:1:b"), {code: "b", x: 1, seq: 3}, "the gapped бой is left for the page to refetch");
  assert.deepEqual(gaps, ["match:1:b", "match:1:c"], "no base is a gap too");
});

test("a multi-view page reloads on an epoch reset and dispatches nothing more", async () => {
  let reloads = 0;
  globalThis.window.location = {reload: () => reloads++};
  const {stream, cache} = connectMatches();
  stream.emit("state", {scope: "match:1:a", data: {code: "a"}, seq: 1, epoch: "e1"});
  stream.emit("state", {scope: "match:1:b", data: {code: "b"}, seq: 1, epoch: "e2"});
  assert.equal(cache.has("match:1:b"), false);
  await runTimers();
  assert.equal(reloads, 1);
});

test("createLiveEvents retries a reload that failed on wake", async () => {
  resetEnv();
  const streams = [];
  let fail = true;
  let errors = 0;
  const statuses = [];
  const live = createLiveEvents({
    eventsURL: () => "/events",
    scopes: [],
    onRecoverError: () => errors++,
    indicator: createSyncIndicator((s) => statuses.push(s)),
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
  assert.equal(statuses.at(-1), "reconnecting");
  fail = false;
  await runTimers();
  assert.equal(streams.length, 2);
  assert.equal(statuses.at(-1), "saved");
  await runTimers();
  assert.equal(streams.length, 2, "a re-seeded page ends the retry chain");
});

// ---- the writer -----------------------------------------------------------

function writer(overrides = {}) {
  resetEnv();
  holdShortTimers = true;
  const adopted = [];
  const statuses = [];
  const w = createScopedWriter({
    urlOf: (scope) => `/api/${scope}/state`,
    adopt: (scope, response) => adopted.push({scope, response}),
    indicator: createSyncIndicator((s) => statuses.push(s)),
    ...overrides,
  });
  return {w, adopted, statuses};
}

test("edits to one scope coalesce into one PATCH per debounce window, last write per path wins", async () => {
  const calls = fakeFetch([{body: {v: "b", w: 1}}]);
  const {w, adopted, statuses} = writer();
  w.patch("match:1:a", ["v"], "a");
  w.patch("match:1:a", ["w"], 1);
  w.patch("match:1:a", ["v"], "b");
  assert.equal(calls.length, 0, "nothing sent inside the window");
  assert.equal(statuses.at(-1), "saving");
  assert.equal(w.isPending("match:1:a", ["v"]), true);
  await runTimers();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, "/api/match:1:a/state");
  assert.equal(calls[0].init.method, "PATCH");
  assert.deepEqual(calls[0].body.ops, [{path: ["v"], value: "b"}, {path: ["w"], value: 1}]);
  assert.deepEqual(adopted, [{scope: "match:1:a", response: {v: "b", w: 1}}]);
  assert.equal(w.isPending("match:1:a", ["v"]), false);
  assert.equal(statuses.at(-1), "saved");
});

test("two scopes flush independently", async () => {
  const calls = fakeFetch([]);
  const {w} = writer();
  w.patch("match:1:a", ["v"], 1);
  w.patch("match:1:b", ["v"], 2);
  await runTimers();
  assert.deepEqual(calls.map((c) => c.url).sort(), ["/api/match:1:a/state", "/api/match:1:b/state"]);
});

test("un-acked edits overlay every view until confirmed; a 5xx retries them", async () => {
  const calls = fakeFetch([{status: 500}, {body: {v: 1}}]);
  const {w, statuses} = writer();
  w.patch("game-state:1", ["v"], 1);
  assert.deepEqual(w.overlay("game-state:1", {v: 0, other: true}), {v: 1, other: true});
  await runTimers(); // first flush: 500
  assert.equal(calls.length, 1);
  assert.equal(statuses.at(-1), "error");
  assert.deepEqual(w.overlay("game-state:1", {v: 0}), {v: 1}, "still overlaid while retrying");
  assert.equal(w.isPending("game-state:1", ["v"]), true);
  await runTimers(); // the 2s retry
  assert.equal(calls.length, 2);
  assert.equal(statuses.at(-1), "saved");
  assert.deepEqual(w.overlay("game-state:1", {v: 0}), {v: 0}, "acked: nothing to overlay");
});

test("a 4xx drops the ops loudly instead of retrying forever", async () => {
  const calls = fakeFetch([{status: 400, text: "nope"}]);
  const rejected = [];
  const {w, statuses} = writer({onRejected: (info) => rejected.push(info)});
  w.patch("game-state:1", ["v"], 1);
  await runTimers();
  await runTimers();
  assert.equal(calls.length, 1);
  assert.equal(rejected.length, 1);
  assert.equal(rejected[0].scope, "game-state:1");
  assert.equal(statuses.at(-1), "error");
  assert.equal(w.hasPending(), false);
});

test("encode translates queued ops to the wire and can hold them for a base", async () => {
  const calls = fakeFetch([{body: {}}]);
  let base = null;
  const {w} = writer({
    encode: (_scope, ops) => (base ? ops.map((op) => ({path: ["participants", base[op.path[1]], ...op.path.slice(2)], value: op.value})) : null),
  });
  w.patch("match:1:a", ["participants", 0, "place"], 2);
  await runTimers();
  assert.equal(calls.length, 0, "no base yet: held, not sent");
  assert.equal(w.isPending("match:1:a", ["participants", 0, "place"]), true);
  base = {0: "17"};
  w.recover("match:1:a");
  await runTimers();
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].body.ops, [{path: ["participants", "17", "place"], value: 2}]);
});

test("docPath overlays ops inside the adopted view (брейн edits view.state)", () => {
  const {w} = writer({docPath: ["state"]});
  w.patch("match:1:a", ["teams", 0, "rows", 1, "mark"], "+");
  assert.deepEqual(w.overlay("match:1:a", {code: "a", state: {teams: [{rows: [{}, {}]}]}}),
    {code: "a", state: {teams: [{rows: [{}, {mark: "+"}]}]}});
});

test("a structural send overlays its intent until its own write settles; a newer intent wins", async () => {
  const gate = [];
  globalThis.fetch = () => new Promise((resolve) => gate.push(resolve));
  const {w, adopted, statuses} = writer();
  const first = w.send("match:1:a", {url: "/api/a/finish", body: {finished: true}}, {path: ["finished"], value: true});
  assert.equal(statuses.at(-1), "saving");
  assert.deepEqual(w.overlay("match:1:a", {finished: false}), {finished: true});
  assert.equal(w.isPending("match:1:a", ["finished"]), true);
  const second = w.send("match:1:a", {url: "/api/a/finish", body: {finished: false}}, {path: ["finished"], value: false});
  assert.deepEqual(w.overlay("match:1:a", {finished: true}), {finished: false}, "the newer toggle's intent");
  // The first write settles late: it must not clear the newer intent.
  gate[0]({ok: true, status: 200, json: () => Promise.resolve({finished: true}), text: () => Promise.resolve("")});
  await first;
  assert.deepEqual(w.overlay("match:1:a", {finished: true}), {finished: false});
  gate[1]({ok: true, status: 200, json: () => Promise.resolve({finished: false}), text: () => Promise.resolve("")});
  await second;
  assert.deepEqual(w.overlay("match:1:a", {finished: true}), {finished: true}, "settled: nothing overlaid");
  assert.equal(adopted.length, 2);
  assert.equal(statuses.at(-1), "saved");
});

test("persisted edits recover on the next load and re-send", async () => {
  window.localStorage = fakeLocalStorage();
  const calls = fakeFetch([{body: {}}]);
  const {w} = writer();
  w.patch("game-state:3", ["v"], 1); // never flushed: the page reloads
  const {w: fresh, statuses} = writer();
  assert.equal(fresh.recover("game-state:3"), 1);
  assert.equal(statuses.at(-1), "saving");
  assert.deepEqual(fresh.overlay("game-state:3", {}), {v: 1});
  await runTimers();
  assert.equal(calls.length, 1);
  assert.equal(fresh.hasPending(), false);
  window.localStorage = undefined;
});

test("hiding the tab flushes what the debounce window holds", async () => {
  const calls = fakeFetch([{body: {}}]);
  const {w} = writer();
  w.patch("game-state:1", ["v"], 1);
  globalThis.document.visibilityState = "hidden";
  await fire("visibilitychange");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].init.keepalive, true);
});

test("a viewer's writer writes nothing", async () => {
  const calls = fakeFetch([]);
  const {w} = writer({readonly: true});
  w.patch("game-state:1", ["v"], 1);
  await w.send("game-state:1", {url: "/x"});
  await runTimers();
  assert.equal(calls.length, 0);
  assert.equal(w.hasPending(), false);
});

test("the indicator: reconnecting beats error beats saving beats saved", () => {
  const seen = [];
  const ind = createSyncIndicator((s) => seen.push(s));
  ind.busy("a");
  ind.fail();
  ind.idle("a", false);
  ind.offline();
  ind.online();
  ind.touch();
  assert.deepEqual(seen, ["saving", "error", "error", "reconnecting", "error", "saved"]);
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
