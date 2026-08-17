import { test } from "node:test";
import assert from "node:assert/strict";
import { xyTimer } from "../web/assets/static/dist/timer.js";

const { _parseCustom: parseCustom, _presets: presets, _cueTimes: cueTimes } = xyTimer;

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
