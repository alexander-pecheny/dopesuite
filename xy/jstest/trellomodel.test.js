import { test } from "node:test";
import assert from "node:assert/strict";
import { cardKind, mapCard, descEdits } from "../web/assets/static/trellomodel.js";

test("cardKind: question by any of its fields, wherever they sit", () => {
  assert.equal(cardKind("? Что это?\n! Ответ"), "question");
  assert.equal(cardKind("№ 3\n? Что это?"), "question");
  assert.equal(cardKind("! Ответ\n^ Источник"), "question");
});

test("cardKind: headings and meta", () => {
  assert.equal(cardKind("### Тур 1"), "heading");
  assert.equal(cardKind("# Редакторы благодарят"), "meta");
  assert.equal(cardKind("#DATE 2026-01-01"), "meta");
});

test("cardKind: an unmarked card is «Другое», not a question", () => {
  assert.equal(cardKind("надо позвонить Пете"), "other");
  assert.equal(cardKind(""), "other");
  assert.equal(cardKind("https://example.com/пакет"), "other");
});

test("mapCard: title becomes the alias, body the text", () => {
  const c = mapCard("  Пушкин, дуэль  ", "? Вопрос\n! Ответ");
  assert.deepEqual(c, { desc: "? Вопрос\n! Ответ", alias: "Пушкин, дуэль", kind: "question" });
});

test("mapCard: an empty body promotes the title to text, with no alias", () => {
  const c = mapCard("просто заметка", "   ");
  assert.deepEqual(c, { desc: "просто заметка", alias: null, kind: "other" });
});

test("mapCard: the body is de-Trello'd (escaped markers, smart links)", () => {
  const c = mapCard("x", "\\# Заголовок\n[https://a.b](https://a.b)");
  assert.equal(c.desc, "# Заголовок\nhttps://a.b");
  assert.equal(c.kind, "meta");
});

test("descEdits: chains Trello's replaced texts back from the current one", () => {
  // Trello order: newest first, each carrying the text it replaced.
  const edits = [
    { before: "v2", date: "2024-03-01T00:00:00Z", author: "Аня" },
    { before: "v1", date: "2024-02-01T00:00:00Z", author: "Боря" },
  ];
  assert.deepEqual(descEdits(edits, "v3"), [
    { before: "v1", after: "v2", date: "2024-02-01T00:00:00Z", author: "Боря" },
    { before: "v2", after: "v3", date: "2024-03-01T00:00:00Z", author: "Аня" },
  ]);
});

test("descEdits: nothing to import without history, no-op edits dropped", () => {
  assert.deepEqual(descEdits(undefined, "v1"), []);
  // A Trello edit that only touched formatting we strip leaves no diff.
  assert.deepEqual(descEdits([{ before: "a\n\nb", date: "d", author: "" }], "a\nb"), []);
});
