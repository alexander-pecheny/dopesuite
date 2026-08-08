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
  const { hits, total } = search(idx, "Пушкин", 50);
  assert.equal(total, 1);
  assert.equal(hits.length, 1);
  assert.equal(hits[0].boardName, "Синхрон");
  assert.equal(hits[0].list, "Тур 1");
  assert.equal(hits[0].card, 3);
  assert.equal(hits[0].snippet.text.slice(hits[0].snippet.start, hits[0].snippet.end), "Пушкин");
  assert.equal(hits[0].more, 0);
  assert.equal(hits[0].comment, undefined);
});

test("further matches on the same card are counted, not listed", () => {
  const idx = [board("Б", [card(1, "? Пушкин про Пушкина\n! Пушкин")])];
  const { hits, total } = search(idx, "Пушкин", 50);
  assert.equal(hits.length, 1);
  assert.equal(total, 1);
  assert.equal(hits[0].more, 2);
});

test("a card matched only by a comment links to that comment", () => {
  const idx = [board("Б", [card(1, "? Вопрос")], [{ card: 1, id: 42, text: "тут нужен зачёт" }])];
  const { hits } = search(idx, "зачёт", 50);
  assert.equal(hits.length, 1);
  assert.equal(hits[0].comment, 42);
  assert.equal(hits[0].snippet.text.includes("зачёт"), true);
});

test("the cap limits what is shown, never what is counted", () => {
  const cards = [card(1, "? раз"), card(2, "? раз"), card(3, "? раз")];
  const { hits, total } = search([board("Б", cards)], "раз", 2);
  assert.equal(hits.length, 2);
  assert.equal(total, 3);
});

test("a card is titled by its alias, else by what the board previews", () => {
  assert.equal(cardTitle(card(1, "? Кто?\n! Пушкин", "про Пушкина")), "про Пушкина");
  assert.equal(cardTitle(card(1, "? Кто написал «Онегина»?\n! Пушкин")), "Кто написал «Онегина»?");
});
