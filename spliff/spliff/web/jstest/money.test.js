import {test} from "node:test";
import assert from "node:assert/strict";
import {exponentOf, formatMinor, parseAmount} from "./dist/money.js";

// The browser's exponent table has to be the same table the server's is, or a
// person types 1234 yen and the server reads 12.34 of them.

test("the exponent table knows the exceptions and nothing else", () => {
  for (const [code, want] of Object.entries({
    USD: 2, EUR: 2, GEL: 2, RUB: 2,
    JPY: 0, KRW: 0, ISK: 0, CLP: 0,
    KWD: 3, BHD: 3, JOD: 3, OMR: 3, TND: 3, LYD: 3, IQD: 3,
    usd: 2, ZZZ: 2,
  })) {
    assert.deepEqual(exponentOf(code), want, code);
  }
});

test("formatMinor writes the currency's own precision", () => {
  assert.deepEqual(formatMinor(1234, "USD"), "12.34");
  assert.deepEqual(formatMinor(5, "USD"), "0.05");
  assert.deepEqual(formatMinor(0, "USD"), "0.00");
  assert.deepEqual(formatMinor(-1234, "USD"), "-12.34");
  assert.deepEqual(formatMinor(-5, "USD"), "-0.05");
  assert.deepEqual(formatMinor(1234, "JPY"), "1234");
  assert.deepEqual(formatMinor(-1234, "JPY"), "-1234");
  assert.deepEqual(formatMinor(1234, "KWD"), "1.234");
  assert.deepEqual(formatMinor(4, "KWD"), "0.004");
});

test("parseAmount reads what a person types and refuses what it cannot", () => {
  assert.deepEqual(parseAmount("12.34", "USD"), 1234);
  assert.deepEqual(parseAmount("12,34", "USD"), 1234);
  assert.deepEqual(parseAmount("12", "USD"), 1200);
  assert.deepEqual(parseAmount(".5", "USD"), 50);
  assert.deepEqual(parseAmount("-0.05", "USD"), -5);
  assert.deepEqual(parseAmount("+7.10", "USD"), 710);
  assert.deepEqual(parseAmount("1 234.50", "USD"), 123450);
  assert.deepEqual(parseAmount("1234", "JPY"), 1234);
  assert.deepEqual(parseAmount("1.234", "KWD"), 1234);

  // One decimal too many is a refusal, not a rounding: there is no such thing
  // as a tenth of a yen.
  assert.deepEqual(parseAmount("12.345", "USD"), null);
  assert.deepEqual(parseAmount("1234.0", "JPY"), null);
  assert.deepEqual(parseAmount("1.2345", "KWD"), null);
  assert.deepEqual(parseAmount("", "USD"), null);
  assert.deepEqual(parseAmount("abc", "USD"), null);
  assert.deepEqual(parseAmount("1.2.3", "USD"), null);
});

test("what formatMinor writes, parseAmount reads back", () => {
  for (const code of ["USD", "JPY", "KWD"]) {
    for (const minor of [0, 1, 7, 999, -1, -1000, 123456789]) {
      assert.deepEqual(parseAmount(formatMinor(minor, code), code), minor, `${minor} ${code}`);
    }
  }
});
