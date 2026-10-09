import {test} from "node:test";
import assert from "node:assert/strict";
import {evenShares} from "./dist/txform.js";
import cases from "../../domain/split/testdata/even_cases.json" with {type: "json"};

// The even split as the editor computes it, against the cases Go's
// domain/split is checked with (split_test.go reads the same file). If the two
// ever disagree, two phones showing one bill show different numbers.

for (const c of cases.even) {
  test(`even split parity: ${c.name}`, () => {
    const state = {
      totalMinor: c.total,
      members: c.joined,
      payments: c.payments.map(([member, minor]) => ({member, minor})),
      shares: c.split.map((member) => ({member, minor: null})),
    };
    assert.deepEqual(evenShares(state), c.want);
  });
}
