import {test} from "node:test";
import assert from "node:assert/strict";
import {formatMinor, parseAmount} from "./dist/money.js";
import cases from "../../domain/money/testdata/amount_cases.json" with {type: "json"};

// What a person types and what an amount looks like, against the cases Go's
// domain/money is checked with (amount_cases_test.go reads the same file).

test("parseAmount reads every case the way Go's Parse does", () => {
  for (const c of cases.parse) {
    assert.deepEqual(parseAmount(c.text, c.currency), c.minor, JSON.stringify(c.text));
  }
});

test("formatMinor writes every case the way Go's Format does", () => {
  for (const c of cases.format) {
    assert.deepEqual(formatMinor(c.minor, c.currency), c.text, `${c.minor} ${c.currency}`);
  }
});
