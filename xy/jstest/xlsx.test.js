// xlsx.ts: the workbook is a zip of the five parts a reader needs, and the
// sheet holds the cells as written, numbers as numbers.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyXlsx } from "../web/assets/static/dist/xlsx.js";
import { xyZip } from "../web/assets/static/dist/zip.js";

test("xlsx: one sheet with inline strings, numbers and escaped text", async () => {
  const bytes = await xyXlsx.xlsxSheet([[" ID", "Комментарий"], [7, "a < b & c\nвторая строка\u0001"]]);
  const parts = await xyZip.zipRead(bytes);
  assert.deepEqual(parts.map((p) => p.name), ["[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"]);
  const xml = new TextDecoder().decode(parts[4].data);
  assert.ok(xml.includes('<c r="A2"><v>7</v></c>'));
  assert.ok(xml.includes('<c r="B2" t="inlineStr"><is><t xml:space="preserve">a &lt; b &amp; c\nвторая строка</t></is></c>'));
});
