import { test } from "node:test";
import assert from "node:assert/strict";
import { xyTimer } from "../web/assets/static/dist/timer.js";

const { _parseCustom: parseCustom, _presets: presets } = xyTimer;

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
