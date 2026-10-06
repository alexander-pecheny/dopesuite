import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/score-table.js";

test("computePlaces ranks by total with shared-rank ranges", () => {
  // 30, 20, 20, 10 -> "1", "2–3", "2–3", "4"
  assert.deepEqual(T.computePlaces([10, 20, 30, 20]), ["4", "2–3", "1", "2–3"]);
});

test("computePlaces breaks ties with the supplied comparator", () => {
  // Equal totals (20,20) split by tiebreak: lower tiebreak ranks higher.
  // compareTiebreak(a,b) > 0 means a ranks below b.
  const places = T.computePlaces([20, 20, 10], {
    tiebreaks: [2, 1, 0],
    compareTiebreak: (a, b) => b - a, // bigger tiebreak wins
  });
  assert.deepEqual(places, ["1", "2", "3"], "tiebreak separates the equal totals");
  // When tiebreaks also match, teams stay tied.
  const tied = T.computePlaces([20, 20], {tiebreaks: [5, 5], compareTiebreak: (a, b) => b - a});
  assert.deepEqual(tied, ["1–2", "1–2"]);
});
