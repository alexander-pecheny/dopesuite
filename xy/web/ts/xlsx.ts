// xlsx.ts — one sheet of plain cells as an .xlsx workbook, the least of the
// format that Excel, LibreOffice and openpyxl all read. Strings are inline, so
// there is no shared-strings table and no styles part.

import { zipWrite } from "./zip.js";

export type XlsxCell = string | number;

const CONTENT_TYPES = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`;
const ROOT_RELS = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`;
const WORKBOOK = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`;
const WORKBOOK_RELS = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`;

const LETTERS = 26;
const A = 65;

// column is a 0-based column index as its letters: 0 → A, 26 → AA.
function column(i: number): string {
  let s = "";
  for (let n = i + 1; n > 0; n = Math.floor((n - 1) / LETTERS)) s = String.fromCharCode(A + ((n - 1) % LETTERS)) + s;
  return s;
}

// XML 1.0 cannot carry most control characters at all, even escaped.
const escape = (s: string): string => s
  .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F]/g, "")
  .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

function cell(v: XlsxCell, ref: string): string {
  if (typeof v === "number") return `<c r="${ref}"><v>${v}</v></c>`;
  return `<c r="${ref}" t="inlineStr"><is><t xml:space="preserve">${escape(v)}</t></is></c>`;
}

function sheet(rows: ReadonlyArray<ReadonlyArray<XlsxCell>>): string {
  const body = rows.map((row, r) => `<row r="${r + 1}">${row.map((v, c) => cell(v, `${column(c)}${r + 1}`)).join("")}</row>`).join("");
  return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>${body}</sheetData></worksheet>`;
}

export async function xlsxSheet(rows: ReadonlyArray<ReadonlyArray<XlsxCell>>): Promise<Uint8Array<ArrayBuffer>> {
  const enc = new TextEncoder();
  const part = (name: string, xml: string) => ({ name, data: enc.encode(xml) as Uint8Array<ArrayBuffer> });
  return zipWrite([
    part("[Content_Types].xml", CONTENT_TYPES),
    part("_rels/.rels", ROOT_RELS),
    part("xl/workbook.xml", WORKBOOK),
    part("xl/_rels/workbook.xml.rels", WORKBOOK_RELS),
    part("xl/worksheets/sheet1.xml", sheet(rows)),
  ], () => true);
}

export const xyXlsx = { xlsxSheet };
