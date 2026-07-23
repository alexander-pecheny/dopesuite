import { test } from "node:test";
import assert from "node:assert/strict";
import { xyRank } from "../web/assets/static/dist/rank.js";

const { keyBetween, nKeysBetween } = xyRank;

test("first key and append stay ordered", () => {
  const a = keyBetween(null, null);
  const b = keyBetween(a, null);
  const c = keyBetween(b, null);
  assert.ok(a < b && b < c, `${a} < ${b} < ${c}`);
});

test("insert between two keys lands strictly between", () => {
  const a = keyBetween(null, null);
  const b = keyBetween(a, null);
  const mid = keyBetween(a, b);
  assert.ok(a < mid && mid < b, `${a} < ${mid} < ${b}`);
});

test("prepend before the first key", () => {
  const a = keyBetween(null, null);
  const before = keyBetween(null, a);
  assert.ok(before < a, `${before} < ${a}`);
});

test("repeated midpoint insertion keeps order over many steps", () => {
  let lo = keyBetween(null, null);
  let hi = keyBetween(lo, null);
  const seen = [lo, hi];
  for (let i = 0; i < 50; i++) {
    const m = keyBetween(lo, hi);
    assert.ok(lo < m && m < hi, `step ${i}: ${lo} < ${m} < ${hi}`);
    seen.push(m);
    hi = m; // keep squeezing into the left gap
  }
  // a freshly sorted copy equals insertion order invariants (no dup keys)
  assert.equal(new Set(seen).size, seen.length, "no duplicate ranks");
});

test("nKeysBetween yields n ordered keys", () => {
  const keys = nKeysBetween(null, null, 5);
  assert.equal(keys.length, 5);
  for (let i = 1; i < keys.length; i++) assert.ok(keys[i - 1] < keys[i]);
});

test("a >= b throws", () => {
  const a = keyBetween(null, null);
  assert.throws(() => keyBetween(a, a));
});
