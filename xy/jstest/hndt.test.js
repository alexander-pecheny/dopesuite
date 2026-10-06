import { test } from "node:test";
import assert from "node:assert/strict";
import { xyHndt } from "../web/assets/static/dist/hndt.js";

// What a list's cards write is listexport's, checked against the Go side in
// listexport_parity.test.js; this file is reading a .hndt back.
const { parseHndtMetaByQuestion } = xyHndt;

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

test("only a line that is exactly --- splits blocks, as chgksuite and the renderer read it", () => {
  const blocks = xyHndt.parseHndtForm("for_question: 1\ncolumns: 3\n\nа\n--- \nб\n---\nfor_question: 2\ncolumns: 3\n\nв");
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0].text, "а\n---\nб");
});
