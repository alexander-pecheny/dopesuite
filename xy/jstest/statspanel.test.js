import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { withStatsLine, statsQuestions } = await import("../web/assets/static/dist/statspanel.js");

const LINE = "Взятия: 1/2 (50%) (Т1)";

test("the line becomes the comment a question did not have", () => {
  assert.equal(withStatsLine("? Раз\n! А", LINE), `? Раз\n! А\n/ ${LINE}`);
});

test("the line is another paragraph of a plain comment", () => {
  assert.equal(withStatsLine("? Раз\n! А\n/ Коммент.\n@ Автор", LINE), `? Раз\n! А\n/ Коммент.\n${LINE}\n@ Автор`);
});

// composeFields puts the first item on the marker's line, which chgksuite reads
// as the same list.
test("the line is another item of a comment that is a list", () => {
  assert.equal(withStatsLine("? Раз\n! А\n/\n- один\n- два", LINE), `? Раз\n! А\n/ - один\n- два\n- ${LINE}`);
});

test("a warm-up and a card with no number are not counted", () => {
  const cards = [{ id: 1 }, { id: 2 }, { id: 3 }, { id: 4 }];
  assert.deepEqual(statsQuestions({ cards, numbers: ["0", "1", null, "2"] }), [2, 4]);
});
