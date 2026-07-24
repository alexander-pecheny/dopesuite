import {test} from "node:test";
import assert from "node:assert/strict";
import {createStateSync, createLiveEvents} from "./dist/state-sync.js";

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
