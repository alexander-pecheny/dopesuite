import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
import {formatMinor, parseAmount} from "./dist/money.js";
import cases from "../../domain/money/testdata/amount_cases.json" with {type: "json"};

// What a person types and what an amount looks like, against the cases Go's
// domain/money is checked with (amount_cases_test.go reads the same file).

Deno.test("parseAmount reads every case the way Go's Parse does", () => {
  for (const c of cases.parse) {
    assertEquals(parseAmount(c.text, c.currency), c.minor, JSON.stringify(c.text));
  }
});

Deno.test("formatMinor writes every case the way Go's Format does", () => {
  for (const c of cases.format) {
    assertEquals(formatMinor(c.minor, c.currency), c.text, `${c.minor} ${c.currency}`);
  }
});
