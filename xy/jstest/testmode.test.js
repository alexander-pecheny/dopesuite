import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createDwell, createTestMode, DWELL_MS, IDLE_LIMIT_MS, testModeStore, TOUCH_EVERY_MS,
} from "../web/assets/static/dist/testmode.js";

// A hand-rolled clock + store, so every rule runs on wall-clock arithmetic
// rather than real timers — the same trick timer.test.js uses.
function harness(t0 = 1_000_000) {
  let now = t0;
  let stored = null;
  const mode = createTestMode({
    now: () => now,
    read: () => stored,
    write: (s) => { stored = s; },
  });
  return { mode, tick: (ms) => { now += ms; }, stored: () => stored };
}

test("start makes the test active on its board and nowhere else", () => {
  const h = harness();
  h.mode.start(7, 42);
  assert.equal(h.mode.sessionFor(7), 42);
  assert.equal(h.mode.sessionFor(8), null);
});

test("starting another test replaces the active one silently", () => {
  const h = harness();
  h.mode.start(7, 42);
  h.mode.start(9, 43);
  assert.equal(h.mode.sessionFor(7), null);
  assert.equal(h.mode.sessionFor(9), 43);
  assert.deepEqual(h.stored().unmarked, []); // the do-not-remark list resets too
});

test("stop clears the mode", () => {
  const h = harness();
  h.mode.start(7, 42);
  h.mode.stop();
  assert.equal(h.mode.active(), null);
  assert.equal(h.stored(), null);
});

test("an hour of idle wall clock expires the mode, even between reads", () => {
  const h = harness();
  h.mode.start(7, 42);
  h.tick(IDLE_LIMIT_MS);
  assert.equal(h.mode.sessionFor(7), 42); // exactly at the limit: still on
  h.tick(1);
  assert.equal(h.mode.sessionFor(7), null); // past it: off, and the slot is wiped
  assert.equal(h.stored(), null);
});

test("touch refreshes the idle clock", () => {
  const h = harness();
  h.mode.start(7, 42);
  h.tick(IDLE_LIMIT_MS - 1);
  h.mode.touch();
  h.tick(IDLE_LIMIT_MS - 1);
  assert.equal(h.mode.sessionFor(7), 42);
});

test("touch writes at most once per throttle window", () => {
  const h = harness();
  h.mode.start(7, 42);
  const before = h.stored().lastActivity;
  h.tick(TOUCH_EVERY_MS - 1);
  h.mode.touch();
  assert.equal(h.stored().lastActivity, before); // inside the window: no write
  h.tick(1);
  h.mode.touch();
  assert.equal(h.stored().lastActivity, before + TOUCH_EVERY_MS);
});

test("touch on an expired mode does not resurrect it", () => {
  const h = harness();
  h.mode.start(7, 42);
  h.tick(IDLE_LIMIT_MS + 1);
  h.mode.touch();
  assert.equal(h.stored(), null);
});

test("a hand-unmarked card is never auto-markable again this mode", () => {
  const h = harness();
  h.mode.start(7, 42);
  assert.equal(h.mode.allowMark(7, 5), true);
  h.mode.noteUnmarked(7, 5);
  assert.equal(h.mode.allowMark(7, 5), false);
  assert.equal(h.mode.allowMark(7, 6), true);
  assert.equal(h.mode.allowMark(8, 5), false); // wrong board: never markable
});

test("the store survives a round-trip and shrugs off garbage", () => {
  const bag = new Map();
  const storage = {
    getItem: (k) => bag.has(k) ? bag.get(k) : null,
    setItem: (k, v) => bag.set(k, v),
    removeItem: (k) => bag.delete(k),
  };
  const store = testModeStore(storage);
  assert.equal(store.read(), null);
  store.write({ boardId: 1, sessionId: 2, lastActivity: 3, unmarked: [4] });
  assert.deepEqual(store.read(), { boardId: 1, sessionId: 2, lastActivity: 3, unmarked: [4] });
  bag.set("xy-testmode", "{not json");
  assert.equal(store.read(), null);
  bag.set("xy-testmode", JSON.stringify({ boardId: "x" }));
  assert.equal(store.read(), null);
  store.write(null);
  assert.equal(bag.has("xy-testmode"), false);
});

// ---- the dwell watcher ----

function dwellHarness() {
  let now = 5_000_000;
  const timers = [];
  const marked = [];
  const h = { modeLive: true };
  const dwell = createDwell({
    now: () => now,
    setTimer: (fn, ms) => { const id = { fn, due: now + ms }; timers.push(id); return id; },
    clearTimer: (id) => { const i = timers.indexOf(id); if (i >= 0) timers.splice(i, 1); },
    tryMark: (cardId) => { if (!h.modeLive) return false; marked.push(cardId); return true; },
  });
  // fire runs every due timer, the way a throttled browser eventually would.
  const fire = () => { for (const t of timers.splice(0)) { if (now >= t.due) t.fn(); } };
  return Object.assign(h, { dwell, marked, fire, tick: (ms) => { now += ms; }, timers });
}

test("a card open for the dwell time is marked once", () => {
  const h = dwellHarness();
  h.dwell.opened(11);
  h.tick(DWELL_MS + 1000);
  h.fire();
  assert.deepEqual(h.marked, [11]);
  h.dwell.check(); // a later visibility flip must not mark again
  assert.deepEqual(h.marked, [11]);
});

test("closing before the minute discards the stamp", () => {
  const h = dwellHarness();
  h.dwell.opened(11);
  h.tick(DWELL_MS - 1);
  h.dwell.closed();
  h.tick(60_000);
  h.fire();
  assert.deepEqual(h.marked, []);
});

test("switching cards restarts the clock from zero", () => {
  const h = dwellHarness();
  h.dwell.opened(11);
  h.tick(DWELL_MS - 1);
  h.dwell.opened(12); // ←/→ walk lands here as open-over-open
  h.tick(DWELL_MS - 1);
  h.dwell.check();
  assert.deepEqual(h.marked, []);
  h.tick(1);
  h.dwell.check();
  assert.deepEqual(h.marked, [12]);
});

test("a throttled timer that fires late still marks — the clock is wall time", () => {
  const h = dwellHarness();
  h.dwell.opened(11);
  h.tick(DWELL_MS * 5); // backgrounded tab: the timer slept through
  h.fire();
  assert.deepEqual(h.marked, [11]);
});

test("check before the minute is a no-op", () => {
  const h = dwellHarness();
  h.dwell.opened(11);
  h.tick(DWELL_MS - 1);
  h.dwell.check();
  assert.deepEqual(h.marked, []);
});

test("a mode started mid-read still marks the card open all along", () => {
  const h = dwellHarness();
  h.modeLive = false;
  h.dwell.opened(11);
  h.tick(DWELL_MS + 1000);
  h.fire(); // minute up, but no mode live — the dwell must not be spent
  assert.deepEqual(h.marked, []);
  h.modeLive = true;
  h.dwell.check();
  h.dwell.check();
  assert.deepEqual(h.marked, [11]); // and once spent, it stays spent
});
