import {test} from "node:test";
import assert from "node:assert/strict";
import {DopeStatsSync} from "./dist/stats-sync.js";

// A fake clock: setTimeout queues a job; runAll() runs the jobs queued so far
// (jobs a callback schedules land in the next runAll, so a self-rescheduling
// throttle advances one tick per call).
function fakeClock() {
  let jobs = [];
  return {
    setTimeout: (fn) => { jobs.push(fn); return jobs.length; },
    runAll: async () => { const cur = jobs; jobs = []; for (const fn of cur) await fn(); },
    pending: () => jobs.length,
  };
}

function harness(over = {}) {
  const calls = {rerender: 0, applied: [], prefetch: 0};
  const clock = fakeClock();
  const stageCache = {
    _state: over.baseState || null,
    matchState: () => stageCache._state,
    applyMatchUpdate: (v) => { calls.applied.push(v); stageCache._state = v; },
    prefetchAllStages: () => { calls.prefetch++; return Promise.resolve(); },
  };
  const gameTable = {applyDeltaOps: (base, ops) => ({...base, ops})};
  const sync = DopeStatsSync.create({
    stageCache, gameTable,
    matchCodeFromScope: (s) => s,
    isActive: over.isActive || (() => true),
    rerender: () => { calls.rerender++; },
    setTimeout: clock.setTimeout,
    throttleMs: 400, resyncMs: 400,
  });
  return {sync, calls, clock, stageCache};
}

test("a chainable delta applies to the cache and rerenders", () => {
  const {sync, calls} = harness({baseState: {code: "m", seq: 1}});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 1, seq: 2});
  assert.equal(calls.applied.length, 1);
  assert.equal(calls.applied[0].seq, 2);
  assert.equal(calls.rerender, 1); // leading edge
});

test("a full snapshot applies and rerenders", () => {
  const {sync, calls} = harness({baseState: {code: "m", seq: 1}});
  sync.applyMatchEvent({scope: "m", data: {code: "m"}, seq: 7});
  assert.equal(calls.applied.length, 1);
  assert.equal(calls.applied[0].seq, 7);
  assert.equal(calls.rerender, 1);
});

test("an already-applied delta is ignored", () => {
  const {sync, calls} = harness({baseState: {code: "m", seq: 5}});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 4, seq: 3});
  assert.equal(calls.applied.length, 0);
  assert.equal(calls.rerender, 0);
});

test("a seq gap schedules exactly one debounced resync", async () => {
  const {sync, calls, clock} = harness({baseState: {code: "m", seq: 5}});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 2, seq: 6}); // gap: base seq 5 != prev 2
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 2, seq: 6}); // second gap, still debounced
  assert.equal(calls.applied.length, 0);
  assert.equal(clock.pending(), 1);
  await clock.runAll();
  assert.equal(calls.prefetch, 1);
});

test("a gap with no base state resyncs", () => {
  const {sync, calls, clock} = harness({baseState: null});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 0, seq: 1});
  assert.equal(calls.applied.length, 0);
  assert.equal(clock.pending(), 1);
});

test("a burst of deltas throttles to leading + trailing rerenders", async () => {
  const {sync, calls, clock} = harness({baseState: {code: "m", seq: 1}});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 1, seq: 2}); // leading: rerender 1, timer armed
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 2, seq: 3}); // coalesced: pending, no rerender
  assert.equal(calls.rerender, 1);
  await clock.runAll(); // trailing tick fires
  assert.equal(calls.rerender, 2);
  await clock.runAll(); // nothing pending → timer clears, no further rerender
  assert.equal(calls.rerender, 2);
});

test("inactive stats view suppresses rerender but still applies", () => {
  const {sync, calls} = harness({baseState: {code: "m", seq: 1}, isActive: () => false});
  sync.applyMatchEvent({scope: "m", ops: [{}], prevSeq: 1, seq: 2});
  assert.equal(calls.applied.length, 1); // cache stays current off-screen
  assert.equal(calls.rerender, 0);        // but no visible rerender
});
