import { test } from "node:test";
import assert from "node:assert/strict";
import { xyTimer } from "../web/assets/static/dist/timer.js";

const { parseCustom, PRESETS: presets, cueTimes, createTimer } = xyTimer;

test("presets carry the expected segment layouts", () => {
  assert.deepEqual(presets.regular.segments, [60]);
  assert.deepEqual(presets.duplet.segments, [30, 30]);
  assert.deepEqual(presets.blitz.segments, [20, 20, 20]);
});

test("parseCustom reads plus-separated positive integers", () => {
  assert.deepEqual(parseCustom("30+30"), [30, 30]);
  assert.deepEqual(parseCustom("40 + 20 + 10"), [40, 20, 10]);
  assert.deepEqual(parseCustom("90"), [90]);
});

test("parseCustom drops junk and falls back to a single minute", () => {
  assert.deepEqual(parseCustom(""), [60]);
  assert.deepEqual(parseCustom("foo"), [60]);
  assert.deepEqual(parseCustom("0+-5"), [60]); // non-positive values rejected
  assert.deepEqual(parseCustom("30+x+15"), [30, 15]); // keep the valid ones
});

test("cueTimes: a last segment warns at 10, ticks the answer window, then rings long", () => {
  const cues = cueTimes("running", true, 60);
  assert.deepEqual(cues[0], ["warn", 50]);
  assert.deepEqual(cues.slice(1, 11), Array.from({ length: 10 }, (_, j) => ["tick", 60 + j]));
  assert.deepEqual(cues[11], ["long", 70]);
  assert.equal(cues.length, 12);
});

test("cueTimes: an earlier duplet segment only ends long", () => {
  assert.deepEqual(cueTimes("running", false, 30), [["long", 30]]);
});

test("cueTimes: resuming inside the last ten seconds skips the warning", () => {
  assert.equal(cueTimes("running", true, 7.5)[0][0], "tick");
  assert.equal(cueTimes("running", true, 10)[0][0], "tick");
  assert.equal(cueTimes("running", true, 10.5)[0][0], "warn");
});

test("cueTimes: a resumed answer window ticks on each remaining second boundary", () => {
  const cues = cueTimes("answer", true, 6.4);
  assert.deepEqual(cues.map((c) => c[0]), ["tick", "tick", "tick", "tick", "tick", "tick", "long"]);
  assert.deepEqual(cues.map((c) => Math.round(c[1] * 10) / 10), [5.4, 4.4, 3.4, 2.4, 1.4, 0.4, 6.4]);
  assert.deepEqual(cueTimes("answer", true, 6), [["tick", 5], ["tick", 4], ["tick", 3], ["tick", 2], ["tick", 1], ["long", 6]]);
});

// ---- the kernel on a fake clock and a recording bell -------------------------
function harness() {
  let now = 0;
  const intervals = new Map();
  let nextId = 1;
  const clock = {
    now: () => now,
    setInterval: (fn, ms) => { const id = nextId++; intervals.set(id, { fn, ms, due: now + ms }); return id; },
    clearInterval: (id) => { intervals.delete(id); },
  };
  // advance runs the interval callbacks the way a browser would, in order.
  const advance = (ms) => {
    const end = now + ms;
    for (;;) {
      let next = null;
      for (const [id, it] of intervals) if (it.due <= end && (!next || it.due < next.it.due)) next = { id, it };
      if (!next) break;
      now = next.it.due;
      next.it.due += next.it.ms;
      next.it.fn();
    }
    now = end;
  };
  const bells = [];
  const bell = {
    play: (kind, inSec) => bells.push([kind, +(now / 1000 + inSec).toFixed(3)]),
    cancel: () => bells.push(["cancel"]),
    warm: () => bells.push(["warm"]),
  };
  const frames = [];
  const timer = createTimer({ clock, bell, view: { render: (vm) => frames.push({ ...vm }) } });
  return { timer, advance, bells, frames, last: () => frames[frames.length - 1], intervals };
}

test("a regular question runs its minute, rolls into the answer window and ends done", () => {
  const h = harness();
  assert.equal(h.last().shown, 60);
  h.timer.start();
  assert.deepEqual(h.bells.slice(0, 3), [["warm"], ["cancel"], ["warn", 50]], "the warning is booked at t=50s on the audio clock");
  assert.equal(h.bells.filter((b) => b[0] === "tick").length, 10);
  assert.deepEqual(h.bells[h.bells.length - 1], ["long", 70]);
  h.advance(30_000);
  assert.equal(h.last().shown, 30);
  assert.equal(h.last().phase, "running");
  h.advance(25_000);
  assert.equal(h.last().urgent, true, "the last ten seconds are painted urgent");
  h.advance(5_000);
  assert.equal(h.last().phase, "answer");
  assert.equal(h.last().shown, 10);
  assert.equal(h.last().label, "Ответ");
  h.advance(10_000);
  assert.equal(h.last().phase, "done");
  assert.equal(h.last().label, "Готово");
  assert.equal(h.intervals.size, 0, "the loop stops when the timer is done");
});

test("pausing freezes the countdown, cancels the booked bells, and resuming re-books them from what is left", () => {
  const h = harness();
  h.timer.start();
  h.advance(20_000);
  h.timer.pause();
  assert.equal(h.last().phase, "paused");
  assert.equal(h.last().startWord, "Продолжить");
  assert.deepEqual(h.bells[h.bells.length - 1], ["cancel"]);
  h.advance(60_000); // the wall clock moves; the timer does not
  assert.equal(h.last().shown, 40);
  const before = h.bells.length;
  h.timer.start();
  const booked = h.bells.slice(before);
  assert.deepEqual(booked.slice(0, 3), [["warm"], ["cancel"], ["warn", 80 + 30]], "40 s left at t=80 s: the warning is at 30 s from now");
  assert.deepEqual(booked[booked.length - 1], ["long", 80 + 50]);
});

test("a duplet ends its first segment long, waits for Start, then plays the second as the last", () => {
  const h = harness();
  h.timer.selectPreset("duplet");
  assert.equal(h.last().label, "Вопрос 1 / 2");
  const before = h.bells.length;
  h.timer.start();
  assert.deepEqual(h.bells.slice(before), [["warm"], ["cancel"], ["long", 30]], "no warning, no ticks on a non-final segment");
  h.advance(30_000);
  assert.equal(h.last().phase, "ready");
  assert.equal(h.last().label, "Вопрос 2 / 2");
  assert.equal(h.last().shown, 30);
  h.timer.start();
  assert.ok(h.bells.some((b) => b[0] === "warn"), "the second segment is the last: it warns and ticks");
  h.timer.reset();
  assert.equal(h.last().label, "Вопрос 1 / 2");
  assert.equal(h.timer.segIdx, 0);
});

test("a custom preset reads its durations; a bad one falls back to a minute", () => {
  const h = harness();
  h.timer.selectPreset("custom", "40+20");
  assert.deepEqual([...h.timer.segments], [40, 20]);
  h.timer.selectPreset("custom", "");
  assert.deepEqual([...h.timer.segments], [60]);
});
