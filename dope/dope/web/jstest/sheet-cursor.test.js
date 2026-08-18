import {test} from "node:test";
import assert from "node:assert/strict";
import {sheetModel, keyAction, parseClipboardGrid, serializeGrid, parseMark, markKey} from "./dist/sheet-cursor.js";

// A ragged sheet: three rows, the middle one two columns short — ЭК's stage
// sheet stacks бои with different shootout counts.
const ragged = sheetModel({rows: () => 3, cols: (row) => (row === 1 ? 3 : 5)});
// And ragged the other way: брейн's бои side by side, the third one two rows taller.
const tall = sheetModel({rows: (col) => (col >= 4 ? 8 : 6), cols: () => 6});

test("moves clamp to the sheet's edges and to a shorter row's last column", () => {
  assert.deepEqual(ragged.move({row: 0, col: 4}, 1, 0), {row: 1, col: 2}, "down into the short row clamps the column");
  assert.deepEqual(ragged.move({row: 1, col: 2}, 1, 0), {row: 2, col: 2}, "and stays there going on");
  assert.deepEqual(ragged.move({row: 0, col: 0}, -1, -1), {row: 0, col: 0}, "the top-left corner is a wall");
  assert.deepEqual(ragged.move({row: 2, col: 4}, 1, 1), {row: 2, col: 4}, "so is the bottom-right");
  assert.deepEqual(ragged.move({row: 0, col: 0}, 99, 0), {row: 2, col: 0}, "Home/End are big moves");
  assert.deepEqual(tall.move({row: 7, col: 4}, 0, -1), {row: 5, col: 3}, "left into a shorter бой clamps the row");
  assert.deepEqual(tall.move({row: 5, col: 3}, 1, 0), {row: 5, col: 3}, "down at a short бой's bottom stays");
  assert.deepEqual(tall.move({row: 5, col: 3}, 1, 1), {row: 6, col: 4}, "diagonally into the tall one goes on");
});

test("an empty sheet has no coordinate", () => {
  const empty = sheetModel({rows: () => 0, cols: () => 5});
  assert.equal(empty.clamp({row: 0, col: 0}), null);
  const noCols = sheetModel({rows: () => 2, cols: () => 0});
  assert.equal(noCols.move({row: 0, col: 0}, 1, 0), null);
});

test("a rect is normalized whichever way it was dragged", () => {
  assert.deepEqual(ragged.rect({row: 2, col: 3}, {row: 0, col: 1}), {rowStart: 0, rowEnd: 2, colStart: 1, colEnd: 3});
});

test("mark keys under either layout, digits, and the numpad", () => {
  for (const key of ["q", "й", "Q", "+", "1"]) assert.equal(markKey({key}), "right", key);
  for (const key of ["w", "ц", "-", "2"]) assert.equal(markKey({key}), "wrong", key);
  assert.equal(markKey({key: "Unidentified", code: "NumpadAdd"}), "right");
  assert.equal(markKey({key: "Unidentified", code: "NumpadSubtract"}), "wrong");
  assert.equal(markKey({key: "e"}), null);
});

test("pasted marks accept every spelling the sheets ever used", () => {
  for (const t of ["+", "1", "п", "п.", "да", "✓", "Q", "right"]) assert.equal(parseMark(t), "right", t);
  for (const t of ["-", "−", "0", "м", "нет", "✗", "w", "wrong"]) assert.equal(parseMark(t), "wrong", t);
  for (const t of ["", " ", "?", "3", null, undefined]) assert.equal(parseMark(t), "", String(t));
});

test("keyAction reads a marks sheet's keys", () => {
  assert.deepEqual(keyAction({key: "ArrowDown", shiftKey: true}, "marks"), {kind: "move", dRow: 1, dCol: 0, extend: true});
  assert.deepEqual(keyAction({key: "q"}, "marks"), {kind: "mark", mark: "right"});
  assert.deepEqual(keyAction({key: " "}, "marks"), {kind: "clear"});
  assert.deepEqual(keyAction({key: "Home"}, "marks"), {kind: "home", extend: false});
  assert.equal(keyAction({key: "Tab"}, "marks"), null, "Tab stays the browser's on a marks sheet");
  assert.equal(keyAction({key: "Enter"}, "marks"), null);
  assert.equal(keyAction({key: "a"}, "marks"), null);
});

test("keyAction reads a text sheet's keys", () => {
  assert.deepEqual(keyAction({key: "Tab", shiftKey: true}, "text"), {kind: "tab", dCol: -1});
  assert.deepEqual(keyAction({key: "Enter"}, "text"), {kind: "edit", text: null});
  assert.deepEqual(keyAction({key: "F2"}, "text"), {kind: "edit", text: null});
  assert.deepEqual(keyAction({key: "7"}, "text"), {kind: "edit", text: "7"});
  assert.deepEqual(keyAction({key: "а"}, "text"), {kind: "edit", text: "а"});
  assert.equal(keyAction({key: "z", ctrlKey: true}, "text"), null, "a shortcut is not typing");
  assert.equal(keyAction({key: "Shift"}, "text"), null);
  assert.deepEqual(keyAction({key: "Delete"}, "text"), {kind: "clear"});
});

test("clipboard grids round-trip, and a trailing newline is not a row", () => {
  assert.deepEqual(parseClipboardGrid("1\t2\r\n3\t4\n"), [["1", "2"], ["3", "4"]]);
  assert.deepEqual(parseClipboardGrid("+"), [["+"]]);
  assert.deepEqual(parseClipboardGrid("\n"), [[""]], "one empty line is one empty cell");
  assert.equal(serializeGrid([["+", "-"], ["", "+"]]), "+\t-\n\t+");
});
