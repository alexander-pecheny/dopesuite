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

test("parseHndtMetaByQuestion keeps question_label, so the style survives the modal", () => {
  const hndt = "for_question: 1\ncolumns: 3\nquestion_label: inside\n\nтекст";
  assert.equal(parseHndtMetaByQuestion(hndt)["1"], "columns: 3\nquestion_label: inside");
});

test("generateHndt reads a handout that has no bracket, as a parsed .docx writes it", () => {
  const cards = [
    { id: 1, kind: "question", desc: "? Раздаточный материал.\n(img pic.png)\nЧто изображено?\n! а" },
    { id: 2, kind: "question", desc: "? Раздаточный материал\nThere is ******* of ******.\nВосстановите слова.\n! б" },
    { id: 3, kind: "question", desc: "? Без картинки.\n! в" },
  ];
  const out = generateHndt(cards, ["1", "2", "3"], {});
  assert.equal(out, "for_question: 1\ncolumns: 3\n\nimage: pic.png\n---\nfor_question: 2\ncolumns: 3\n\nThere is ******* of ******.\nВосстановите слова.");
});

test("the Поля model gives back a generated document unchanged", () => {
  const src = "for_question: 1\ncolumns: 3\n\nimage: pic.png\n---\nfor_question: 3\ncolumns: 2\nfont_size: 12\n\nтекст\n\nвторая строка";
  assert.equal(xyHndt.composeHndtForm(xyHndt.parseHndtForm(src)), src);
});

test("the Поля model reads the settings, the handout kind, and the lines it has no control for", () => {
  const [a, b] = xyHndt.parseHndtForm("for_question: 5\nquestion_label: inside\ncolumns: 2\nrows: 4\nno_center: 1\n\nimage: x.png\n---\nfor_question: 6\nколонки: 3\n\nТекст");
  assert.deepEqual([a.kind, a.image, xyHndt.hndtGet(a, "question_label"), xyHndt.hndtGet(a, "no_center")], ["image", "x.png", "inside", "1"]);
  // A line whose key is not reserved is handout text, as the generator reads it.
  assert.deepEqual([b.kind, b.text, xyHndt.hndtGet(b, "rows")], ["text", "колонки: 3\n\nТекст", null]);
});

test("editing through the model keeps the order of the settings and adds new ones after them", () => {
  const [b] = xyHndt.parseHndtForm("for_question: 2\ncolumns: 3\nfont_size: 14\n\nтекст");
  xyHndt.hndtSet(b, "columns", "4");
  xyHndt.hndtSet(b, "no_center", "1");
  xyHndt.hndtSet(b, "font_size", null);
  b.kind = "image"; b.image = "p.jpg";
  assert.equal(xyHndt.composeHndtForm([b]), "for_question: 2\ncolumns: 4\nno_center: 1\n\nimage: p.jpg");
});

test("an empty block, a page break to chgksuite, survives the form", () => {
  const src = "for_question: 1\ncolumns: 3\n\nа\n---\n\n---\nfor_question: 2\ncolumns: 3\n\nб";
  const blocks = xyHndt.parseHndtForm(src);
  assert.deepEqual(blocks.map((b) => b.blank), [false, true, false]);
  assert.equal(xyHndt.composeHndtForm(blocks), "for_question: 1\ncolumns: 3\n\nа\n---\n\n---\nfor_question: 2\ncolumns: 3\n\nб");
});
