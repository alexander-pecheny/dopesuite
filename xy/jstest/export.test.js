import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const ids = ["exportOverlay", "exportForm", "exportModeOne", "exportModeMany", "exportOneFormat", "exportFormats", "exportFmt4s", "exportFmtDocx", "exportFmtDocxScreen", "exportFmtDocxSpoilers", "exportFmtPdf", "exportFmtPdfMobile", "exportFmtPptx", "exportFmtOpenquiz", "exportFmtHandouts", "exportToggleAll", "exportRun", "exportCancel", "exportMessage",
  // the telegram dialog the panel hands over to (tgexport.test.js drives it)
  "tgExportOverlay", "tgExportForm", "tgExportToken", "tgExportChannel", "tgExportChat", "tgExportRun", "tgExportCancel", "tgExportMessage"];
const p = installDOM(ids);
p.node("exportOverlay").hidden = true;
p.node("exportFmt4s").checked = true;
p.node("exportModeOne").checked = true;
// The dropdown the "one format" mode reads, as board.dopeui declares it.
const one = p.node("exportOneFormat");
one.options = ["4s", "docx", "docx_screen", "docx_spoilers", "pdf", "pdf_mobile", "pptx", "openquiz", "handouts"].map((value) => ({ value, disabled: false }));
one.value = "docx";
// The panel binds these at import, so they are swapped before it loads.
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");
const downloads = [];
xyApp.downloadBlob = (blob, name) => { downloads.push([blob, name]); };
let online = true;
xySync.isOnline = () => online;
const { createExportPanel } = await import("../web/assets/static/dist/export.js");
const { xyListExport } = await import("../web/assets/static/dist/listexport.js");

const cards = [
  { id: 1, listId: 1, kind: "question", rank: "a", desc: "? Раз\n! А\n" },
  { id: 2, listId: 1, kind: "question", rank: "b", desc: "  " },
  { id: 3, listId: 1, kind: "question", rank: "c", desc: "? Два (img pic.png)\n! Б" },
];
const scope = { list: { id: 1, title: "Тур 1", rank: "a", groupId: null }, grouped: false, group: null, lists: [], cards, title: "Тур 1" };

test("offline, only the .4s is offered — the dropdown drops the rest and moves onto it", async () => {
  online = false;
  const panel = createExportPanel(fakeBoard(), { appendImages: async () => new Set() });
  panel.open(scope);
  assert.equal(p.node("exportFmtDocx").disabled, true);
  assert.equal(p.node("exportFmt4s").disabled, false);
  assert.match(p.node("exportMessage").textContent, /^Офлайн/);
  assert.equal(one.value, "4s", "the .docx it stood on is unreachable offline");
  assert.deepEqual(one.options.filter((o) => !o.disabled).map((o) => o.value), ["4s"]);
  p.node("exportForm").fire("submit");
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(downloads.length, 1);
  assert.equal(downloads[0][1], "Тур 1.4s");
  assert.equal(await downloads[0][0].text(), xyListExport.exportSource(cards));
  assert.equal(p.node("exportOverlay").hidden, true, "a finished export closes the dialog");
  online = true;
});

test("the tick boxes belong to the zip: hidden under «один формат», back under «несколько»", () => {
  const panel = createExportPanel(fakeBoard(), { appendImages: async () => new Set() });
  panel.open(scope);
  assert.equal(p.node("exportFormats").hidden, true);
  assert.equal(p.node("exportRun").disabled, false, "the dropdown's format is what runs");

  p.node("exportModeOne").checked = false;
  p.node("exportModeMany").checked = true;
  p.node("exportModeMany").fire("change");
  assert.equal(p.node("exportFormats").hidden, false);

  for (const id of ["exportFmt4s", "exportFmtDocx", "exportFmtDocxScreen", "exportFmtDocxSpoilers", "exportFmtPdf", "exportFmtPdfMobile", "exportFmtPptx", "exportFmtOpenquiz", "exportFmtHandouts"]) p.node(id).checked = false;
  p.node("exportFmt4s").fire("change");
  assert.equal(p.node("exportRun").disabled, true, "nothing ticked is nothing to do");

  p.node("exportModeMany").checked = false;
  p.node("exportModeOne").checked = true;
  p.node("exportModeOne").fire("change");
  assert.equal(p.node("exportRun").disabled, false);
});

test("the export label says «группы» for a grouped list, and an empty list is not offered it", () => {
  const panel = createExportPanel(fakeBoard(), { appendImages: async () => new Set() });
  assert.equal(panel.label(scope), "Экспорт");
  assert.equal(panel.label({ ...scope, grouped: true, group: { id: 5, name: "Пакет" } }), "Экспорт группы");
  assert.equal(panel.offered({ ...scope, cards: [] }), false, "nothing to export, so no row to press");
  assert.equal(panel.offered(scope), true);
});
