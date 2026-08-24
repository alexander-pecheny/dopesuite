import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const ids = ["exportOverlay", "exportForm", "exportFmt4s", "exportFmtDocx", "exportFmtPdf", "exportFmtPdfMobile", "exportFmtHandouts", "exportToggleAll", "exportRun", "exportCancel", "exportMessage"];
const p = installDOM(ids);
p.node("exportOverlay").hidden = true;
p.node("exportFmt4s").checked = true;
// The panel binds these at import, so they are swapped before it loads.
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");
const downloads = [];
xyApp.downloadBlob = (blob, name) => { downloads.push([blob, name]); };
let online = true;
xySync.isOnline = () => online;
const { createExportPanel, exportSource } = await import("../web/assets/static/dist/export.js");

const cards = [
  { id: 1, listId: 1, kind: "question", rank: "a", desc: "? Раз\n! А\n" },
  { id: 2, listId: 1, kind: "question", rank: "b", desc: "  " },
  { id: 3, listId: 1, kind: "question", rank: "c", desc: "? Два (img pic.png)\n! Б" },
];
const scope = { list: { id: 1, title: "Тур 1", rank: "a", groupId: null }, grouped: false, group: null, lists: [], cards, title: "Тур 1" };

test("exportSource is the cards' 4s in order, blank-line separated, empty cards dropped", () => {
  assert.equal(exportSource(cards), "? Раз\n! А\n\n? Два (img pic.png)\n! Б\n");
});

// 4s ends an element at a blank line and drops what follows; xy's editor lets one
// stand inside a question. See fsource.TestBlankLineEndsElement for the parser side.
test("a blank line inside a field becomes (LINEBREAK), so the rest of it survives 4s", () => {
  const c = [{ id: 9, listId: 1, kind: "question", rank: "a", desc: "? Первый абзац\n\nВторой абзац\n! А" }];
  assert.equal(exportSource(c), "? Первый абзац(LINEBREAK)\nВторой абзац\n! А\n");
});

test("a run of blank lines keeps its height: one (LINEBREAK) each", () => {
  const c = [{ id: 9, listId: 1, kind: "question", rank: "a", desc: "? А\n\n\nБ\n! О" }];
  assert.equal(exportSource(c), "? А(LINEBREAK)(LINEBREAK)\nБ\n! О\n");
});

test("a list item is not a marker: the blank line before it still folds", () => {
  const c = [{ id: 9, listId: 1, kind: "question", rank: "a", desc: "? Вопрос:\n\n- раз\n- два\n! О" }];
  assert.equal(exportSource(c), "? Вопрос:(LINEBREAK)\n- раз\n- два\n! О\n");
});

test("a blank line before a marker just goes — the field after it stays in the question", () => {
  const c = [{ id: 9, listId: 1, kind: "question", rank: "a", desc: "? Вопрос\n! Ответ\n\n^ Источник" }];
  assert.equal(exportSource(c), "? Вопрос\n! Ответ\n^ Источник\n");
});

test("offline, only the .4s is offered and it downloads without the network", async () => {
  online = false;
  const panel = createExportPanel(fakeBoard(), { appendImages: async () => new Set() });
  panel.open(scope);
  assert.equal(p.node("exportFmtDocx").disabled, true);
  assert.equal(p.node("exportFmt4s").disabled, false);
  assert.match(p.node("exportMessage").textContent, /^Офлайн/);
  assert.equal(p.node("exportToggleAll").text, "Снять выделение", "the one available format is ticked");
  p.node("exportForm").fire("submit");
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(downloads.length, 1);
  assert.equal(downloads[0][1], "Тур 1.4s");
  assert.equal(await downloads[0][0].text(), exportSource(cards));
  assert.equal(p.node("exportOverlay").hidden, true, "a finished export closes the dialog");
  online = true;
});

test("the export label says «группы» for a grouped list, and an empty list is not offered it", () => {
  const panel = createExportPanel(fakeBoard(), { appendImages: async () => new Set() });
  assert.equal(panel.label(scope), "Экспорт");
  assert.equal(panel.label({ ...scope, grouped: true, group: { id: 5, name: "Пакет" } }), "Экспорт группы");
  assert.equal(panel.offered({ ...scope, cards: [] }), false, "nothing to export, so no row to press");
  assert.equal(panel.offered(scope), true);
});
