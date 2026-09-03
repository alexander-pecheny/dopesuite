import {test} from "node:test";
import assert from "node:assert/strict";
import {plural} from "../assets/dist/esm/i18nstrings_plural_gen.js";

test("the Russian rule picks one/few/many the way dopecore does", () => {
  const cases = {
    0: "many", 1: "one", 2: "few", 4: "few", 5: "many", 11: "many", 12: "many",
    14: "many", 15: "many", 20: "many", 21: "one", 22: "few", 24: "few",
    25: "many", 30: "many", 31: "one", 100: "many", 101: "one", 111: "many",
    "-1": "one", "-3": "few",
  };
  for (const [n, want] of Object.entries(cases)) {
    assert.equal(plural("ru", Number(n), "one", "few", "many"), want, `n = ${n}`);
  }
});
