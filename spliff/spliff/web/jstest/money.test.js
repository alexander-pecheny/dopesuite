import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import {exponentOf, formatMinor, parseAmount} from "./dist/money.js";

// The browser's exponent table has to be the same table the server's is, or a
// person types 1234 yen and the server reads 12.34 of them.

Deno.test("the exponent table knows the exceptions and nothing else", () => {
  for (const [code, want] of Object.entries({
    USD: 2, EUR: 2, GEL: 2, RUB: 2,
    JPY: 0, KRW: 0, ISK: 0, CLP: 0,
    KWD: 3, BHD: 3, JOD: 3, OMR: 3, TND: 3, LYD: 3, IQD: 3,
    usd: 2, ZZZ: 2,
  })) {
    assertEquals(exponentOf(code), want, code);
  }
});

Deno.test("formatMinor writes the currency's own precision", () => {
  assertEquals(formatMinor(1234, "USD"), "12.34");
  assertEquals(formatMinor(5, "USD"), "0.05");
  assertEquals(formatMinor(0, "USD"), "0.00");
  assertEquals(formatMinor(-1234, "USD"), "-12.34");
  assertEquals(formatMinor(-5, "USD"), "-0.05");
  assertEquals(formatMinor(1234, "JPY"), "1234");
  assertEquals(formatMinor(-1234, "JPY"), "-1234");
  assertEquals(formatMinor(1234, "KWD"), "1.234");
  assertEquals(formatMinor(4, "KWD"), "0.004");
});

Deno.test("parseAmount reads what a person types and refuses what it cannot", () => {
  assertEquals(parseAmount("12.34", "USD"), 1234);
  assertEquals(parseAmount("12,34", "USD"), 1234);
  assertEquals(parseAmount("12", "USD"), 1200);
  assertEquals(parseAmount(".5", "USD"), 50);
  assertEquals(parseAmount("-0.05", "USD"), -5);
  assertEquals(parseAmount("+7.10", "USD"), 710);
  assertEquals(parseAmount("1 234.50", "USD"), 123450);
  assertEquals(parseAmount("1234", "JPY"), 1234);
  assertEquals(parseAmount("1.234", "KWD"), 1234);

  // One decimal too many is a refusal, not a rounding: there is no such thing
  // as a tenth of a yen.
  assertEquals(parseAmount("12.345", "USD"), null);
  assertEquals(parseAmount("1234.0", "JPY"), null);
  assertEquals(parseAmount("1.2345", "KWD"), null);
  assertEquals(parseAmount("", "USD"), null);
  assertEquals(parseAmount("abc", "USD"), null);
  assertEquals(parseAmount("1.2.3", "USD"), null);
});

Deno.test("what formatMinor writes, parseAmount reads back", () => {
  for (const code of ["USD", "JPY", "KWD"]) {
    for (const minor of [0, 1, 7, 999, -1, -1000, 123456789]) {
      assertEquals(parseAmount(formatMinor(minor, code), code), minor, `${minor} ${code}`);
    }
  }
});
