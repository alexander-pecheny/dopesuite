import {test} from "node:test";
import assert from "node:assert/strict";
import {createStateSync, createLiveEvents} from "./dist/state-sync.js";

// The engine reads window/document lazily; give it fakes with immediate timers
// so debounce/jitter never make a test wait.
globalThis.window = {
  setTimeout: (fn) => {
    fn();
    return 0;
  },
  clearTimeout: () => {},
  addEventListener: () => {},
};
globalThis.document = {addEventListener: () => {}, visibilityState: "visible"};

// fakeStream is the substitutable EventSource adapter: tests emit server events
// by calling the captured listeners.
function fakeStream() {
  const listeners = new Map();
  return {
    readyState: 1,
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

function fakeFetch(responses) {
  const calls = [];
  globalThis.fetch = (url, init) => {
    calls.push({url, init});
    const r = responses.shift() || {};
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
  const stream = fakeStream();
  const states = [];
  const sync = createStateSync({
    scope: "game-state:1",
    stateURL: "/state",
    eventsURL: "/events",
    readonly: true,
    getState: () => (states.length ? states[states.length - 1] : {v: 0}),
    getInitialSeq: () => 5,
    getInitialEpoch: () => "e1",
    onRemoteState: (state) => states.push(state),
    newEventSource: () => stream,
    ...overrides,
  });
  sync.connect();
  return {stream, states, sync};
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

test("createLiveEvents dispatches after the epoch guard and latches on reset", () => {
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
  // Epoch flip: reload scheduled (immediately, with the fake timer), no dispatch.
  stream.emit("state", {scope: "match:1:c", data: {}, epoch: "e2"});
  assert.deepEqual(seen, ["match:1:a", "match:1:b"]);
  assert.equal(reloads, 1);
});

test("createLiveEvents lockdown closes the stream and notifies", () => {
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
