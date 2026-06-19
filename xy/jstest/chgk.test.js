import { test } from "node:test";
import assert from "node:assert/strict";
import { xyChgk } from "../web/assets/static/chgk.js";

const { questionText, blockText, numberQuestionCards, parseBlocks, numberDirective } = xyChgk;

test("question text strips the leading '? ' marker", () => {
  const desc = "? В каком году?\n! 1799\n^ источник";
  assert.equal(questionText(desc), "В каком году?");
});

test("question without a marker falls back to the whole text", () => {
  assert.equal(questionText("Просто текст вопроса"), "Просто текст вопроса");
});

test("multi-line question keeps continuation lines", () => {
  const desc = "? Первая строка\nвторая строка\n! ответ";
  assert.equal(questionText(desc), "Первая строка\nвторая строка");
});

test("meta and heading blocks are extracted", () => {
  assert.equal(blockText("# Редактор пакета", "meta"), "Редактор пакета");
  assert.equal(blockText("### Тур 1", "heading"), "Тур 1");
});

test("number directives: № explicit and №№ base", () => {
  assert.deepEqual(numberDirective(parseBlocks("№ 5\n? q")), { value: "5", base: false });
  assert.deepEqual(numberDirective(parseBlocks("№№ 10\n? q")), { value: "10", base: true });
});

test("auto-numbers questions 1,2,3 in order", () => {
  const cards = [
    { kind: "question", desc: "? a" },
    { kind: "question", desc: "? b" },
    { kind: "question", desc: "? c" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["1", "2", "3"]);
});

test("headings and meta do not consume numbers", () => {
  const cards = [
    { kind: "heading", desc: "### Тур 1" },
    { kind: "question", desc: "? a" },
    { kind: "meta", desc: "# инфо" },
    { kind: "question", desc: "? b" },
  ];
  assert.deepEqual(numberQuestionCards(cards), [null, "1", null, "2"]);
});

test("№№ resets the running base and subsequent questions continue", () => {
  const cards = [
    { kind: "question", desc: "№№ 4\n? a" },
    { kind: "question", desc: "? b" },
    { kind: "question", desc: "? c" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["4", "5", "6"]);
});

test("explicit № overrides but a zero number does not advance the counter", () => {
  const cards = [
    { kind: "question", desc: "№ 0\n? warmup" },
    { kind: "question", desc: "? first real" },
    { kind: "question", desc: "№ 7\n? seven" },
    { kind: "question", desc: "? eight" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["0", "1", "7", "8"]);
});
