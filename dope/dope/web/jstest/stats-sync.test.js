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
  const calls = {rerender: 0, prefetch: 0};
  const clock = fakeClock();
  const stageCache = {
    prefetchAllStages: () => { calls.prefetch++; return Promise.resolve(); },
  };
  const sync = DopeStatsSync.create({
    stageCache,
    isActive: over.isActive || (() => true),
    rerender: () => { calls.rerender++; },
    setTimeout: clock.setTimeout,
    throttleMs: 400, resyncMs: 400,
  });
  return {sync, calls, clock, stageCache};
}

test("two gaps schedule exactly one debounced resync", async () => {
  const {sync, calls, clock} = harness();
  sync.scheduleResync();
  sync.scheduleResync();
  assert.equal(clock.pending(), 1);
  await clock.runAll();
  assert.equal(calls.prefetch, 1);
  assert.equal(calls.rerender, 1, "the refetched bracket is recomputed once");
});

test("a burst of deltas throttles to leading + trailing rerenders", async () => {
  const {sync, calls, clock} = harness();
  sync.scheduleRerender(); // leading: rerender 1, timer armed
  sync.scheduleRerender(); // coalesced: pending, no rerender
  assert.equal(calls.rerender, 1);
  await clock.runAll(); // trailing tick fires
  assert.equal(calls.rerender, 2);
  await clock.runAll(); // nothing pending → timer clears, no further rerender
  assert.equal(calls.rerender, 2);
});

test("an inactive stats view does not rerender", () => {
  const {sync, calls} = harness({isActive: () => false});
  sync.scheduleRerender();
  assert.equal(calls.rerender, 0);
});
