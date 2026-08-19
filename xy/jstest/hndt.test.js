import { test } from "node:test";
import assert from "node:assert/strict";
import { xyHndt } from "../web/assets/static/dist/hndt.js";

const { generateHndt, parseHndtMetaByQuestion } = xyHndt;

test("generateHndt emits a block per question with a handout", () => {
  const cards = [
    { id: 1, kind: "question", desc: "> Текст раздатки\n? Вопрос 1\n! ответ" },
    { id: 2, kind: "question", desc: "? Без раздатки\n! ответ" },
    { id: 3, kind: "question", desc: "> (img foto.png)\n? Что тут?\n! х" },
  ];
  const numbers = ["1", "2", "3"];
  const out = generateHndt(cards, numbers, {});
  const blocks = out.split("\n---\n");
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0], "for_question: 1\ncolumns: 3\n\nТекст раздатки");
  assert.equal(blocks[1], "for_question: 3\ncolumns: 3\n\nimage: foto.png");
});

test("generateHndt uses saved per-question settings", () => {
  const cards = [{ id: 7, kind: "question", desc: "> Раздатка\n? Q\n! a" }];
  const out = generateHndt(cards, ["4"], { 7: "columns: 2\nrows: 5" });
  assert.equal(out, "for_question: 4\ncolumns: 2\nrows: 5\n\nРаздатка");
});

test("generateHndt reads a legacy inline handout bracket", () => {
  const cards = [{ id: 1, kind: "question", desc: "? Текст [Раздаточный материал: листок] вопроса\n! a" }];
  const out = generateHndt(cards, ["1"], {});
  assert.equal(out, "for_question: 1\ncolumns: 3\n\nлисток");
});

test("parseHndtMetaByQuestion strips content, keeps settings by question", () => {
  const hndt = "for_question: 1\ncolumns: 2\nrows: 3\n\nтекст\n---\nfor_question: 4\ncolumns: 3\n\nimage: a.png";
  const m = parseHndtMetaByQuestion(hndt);
  assert.equal(m["1"], "columns: 2\nrows: 3");
  assert.equal(m["4"], "columns: 3");
});
