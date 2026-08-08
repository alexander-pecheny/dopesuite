import { test } from "node:test";
import assert from "node:assert/strict";
import { xySearchIndex } from "../web/assets/static/dist/searchindex.js";

const { search, cardTitle } = xySearchIndex;

const board = (name, cards, comments = []) => ({
  board: 1,
  index: { name, lists: [{ id: 7, title: "Тур 1" }], cards, comments },
});
const card = (id, desc, alias = "") => ({ id, list: 7, kind: "question", desc, alias });

test("a hit names its board and list and shows the match", () => {
  const idx = [board("Синхрон", [card(3, "? Кто написал «Онегина»?\n! Пушкин")])];
  const { questions, questionTotal } = search(idx, "Пушкин", 50);
  assert.equal(questionTotal, 1);
  assert.equal(questions.length, 1);
  assert.equal(questions[0].boardName, "Синхрон");
  assert.equal(questions[0].list, "Тур 1");
  assert.equal(questions[0].card, 3);
  const m = questions[0].snippet.marks[0];
  assert.equal(questions[0].snippet.text.slice(m.start, m.end), "Пушкин");
  assert.equal(questions[0].more, 0);
  assert.equal(questions[0].comment, undefined);
});

// A card is one result however often the needle lands in it: the matches the
// snippet shows are all marked, and only the ones past its window are counted.
test("one row per card; matches beyond the snippet are counted", () => {
  const near = [board("Б", [card(1, "? Пушкин про Пушкина\n! Пушкин")])];
  const shown = search(near, "Пушкин", 50);
  assert.equal(shown.questions.length, 1);
  assert.equal(shown.questionTotal, 1);
  assert.equal(shown.questions[0].snippet.marks.length, 3);
  assert.equal(shown.questions[0].more, 0);

  const far = [board("Б", [card(1, "? Пушкин " + "а".repeat(300) + " Пушкин")])];
  const split = search(far, "Пушкин", 50);
  assert.equal(split.questions[0].snippet.marks.length, 1);
  assert.equal(split.questions[0].more, 1);
});

// A question and the discussion about it answer «где это было?» differently, so
// they are counted and listed apart — and one comment is one row, since each is
// its own remark with its own link.
test("comments are their own results, one row each", () => {
  const idx = [board("Б", [card(1, "? Вопрос")], [
    { card: 1, id: 42, text: "тут нужен зачёт" },
    { card: 1, id: 43, text: "и здесь зачёт спорный" },
  ])];
  const { questions, comments, questionTotal, commentTotal } = search(idx, "зачёт", 50);
  assert.equal(questions.length, 0);
  assert.equal(questionTotal, 0);
  assert.equal(commentTotal, 2);
  assert.deepEqual(comments.map((h) => h.comment), [42, 43]);
  assert.ok(comments[0].snippet.text.includes("зачёт"));
  assert.equal(comments[0].snippet.marks.length, 1);
  // The card it hangs off is still what the tile is titled by.
  assert.equal(comments[0].card, 1);
});

test("a card matching in both places is listed in both", () => {
  const idx = [board("Б", [card(1, "? Что за зачёт?")], [{ card: 1, id: 7, text: "зачёт узкий" }])];
  const { questions, comments } = search(idx, "зачёт", 50);
  assert.equal(questions.length, 1);
  assert.equal(comments.length, 1);
});

test("the cap limits what is shown, never what is counted", () => {
  const cards = [card(1, "? раз"), card(2, "? раз"), card(3, "? раз")];
  const { questions, questionTotal } = search([board("Б", cards)], "раз", 2);
  assert.equal(questions.length, 2);
  assert.equal(questionTotal, 3);
});

test("a card is titled by its alias, else by what the board previews", () => {
  assert.equal(cardTitle(card(1, "? Кто?\n! Пушкин", "про Пушкина")), "про Пушкина");
  assert.equal(cardTitle(card(1, "? Кто написал «Онегина»?\n! Пушкин")), "Кто написал «Онегина»?");
});
