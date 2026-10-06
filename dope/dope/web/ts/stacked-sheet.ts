// The stacked bout sheet: a tab's bouts drawn one after another as one sheet
// the cursor walks (EK, Hamsa and Troika stack them down the page, Brain lays
// them side by side). A cell of it has one address: the bout's code plus the
// fields the page names (a seat, a theme, a question, a kind of cell). The
// page states that address once, as the rows and the columns each bout has,
// and this module derives everything else from it: the data-* attributes a
// cell carries, the cursor's geometry (coordOf, cellAt), the presence kind
// another host's cursor is found by, and the writing of a mark through
// page.patch, so a host can undo it.
//
// The geometry covers only the bouts drawn on the tab in front: a column or a
// row for a bout on another tab would lead the cursor off the screen.

import {cssEscape} from "./cells.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit, Mark, SheetCursor, SheetSpec} from "./sheet-cursor.js";
import type {CursorKind} from "./game-shell.js";
import type {PatchPath} from "./state-sync.js";

export type AddressValue = string | number;
// A row's or a column's part of an address: its fields and their values.
export type AddressPart = object;

// A cell's address: the bout's code under `match`, then the page's fields.
export type SheetAddress<R extends AddressPart, C extends AddressPart> = {match: string} & R & C;

export interface StackedSheetSpec<B, R extends AddressPart, C extends AddressPart> {
  // The class every cell of the sheet carries, as a selector (".ek-cell").
  selector: string;
  // The address's fields besides `match`: every key a row or a column holds.
  // They are the data-* attributes a cell carries and the keys presence
  // matches a cursor by.
  fields: ReadonlyArray<NoInfer<keyof R & string | keyof C & string>>;
  // Which way the bouts follow each other: down the page (a row is a seat of
  // a bout) or across it (Brain: a column is a side of a bout).
  stack?: "rows" | "columns";
  // The bouts drawn on the tab in front, in the order they are drawn.
  bouts: () => B[];
  codeOf: (bout: B) => string;
  rowsOf: (bout: B) => R[];
  columnsOf: (bout: B) => C[];
  // How a mark is written; a sheet without it writes through its own
  // applyValues (Troika's written counts).
  marks?: SheetMarks<SheetAddress<R, C>>;
}

export interface SheetMarks<A> {
  // The mark the cell holds now, or null when it takes no mark (a finished
  // bout, an empty seat).
  markOf: (address: A) => Mark | null;
  // Put the mark into the page's parsed state, before it is sent.
  setMark: (address: A, mark: Mark) => void;
  // Where the mark goes in the bout's document.
  pathOf: (address: A) => PatchPath;
  // The bout page's patch, which keeps the host's undo.
  patch: (code: string, path: PatchPath, value: unknown) => void;
  // After a gesture: repaint what the marks feed (totals, the score).
  onWritten?: (codes: string[]) => void;
}

// The cursor's options a page still gives itself.
export type StackedCursorOptions = Omit<SheetSpec, "root" | "rows" | "cols" | "coordOf" | "cellAt" | "cellSelector" | "applyValues"> & {
  applyValues?: SheetSpec["applyValues"];
};

export interface StackedSheet<B, R extends AddressPart, C extends AddressPart> {
  // The presence kind for the sheet's cells.
  readonly cursorKind: CursorKind;
  // The data-* a cell at this address carries, for td()'s dataset.
  dataset(address: SheetAddress<R, C>): Record<string, string>;
  // The selector that finds the one cell at this address.
  selectorOf(address: SheetAddress<R, C>): string;
  // The address a cell carries, read against the bouts drawn now; null for a
  // cell of a bout not on the sheet.
  addressOf(cell: {dataset: Record<string, string | undefined>}): SheetAddress<R, C> | null;
  // Every address on the sheet, row by row.
  addresses(): Array<SheetAddress<R, C>>;
  rows(col: number): number;
  cols(row: number): number;
  coordOf(address: SheetAddress<R, C>): CellCoord | null;
  addressAt(coord: CellCoord): SheetAddress<R, C> | null;
  // Write marks through the page's patch, paint the cells, then onWritten.
  applyMarks(edits: CellEdit[]): void;
  cursor(root: HTMLElement, options: StackedCursorOptions): SheetCursor;
  // The bout a code names, among those drawn.
  boutOf(code: string): B | undefined;
}

// A lane is one bout's run along the stacking axis: its seats (or sides).
interface Lane<B, R, C> {
  bout: B;
  code: string;
  rows: R[];
  columns: C[];
}

// A presence cursor carries its app, its kind and its game beside the
// address, under these names; a field by one of them would be overwritten.
export const PRESENCE_KEYS = ["app", "kind", "gameID"];

export function stackedSheet<B, R extends AddressPart, C extends AddressPart>(spec: StackedSheetSpec<B, R, C>): StackedSheet<B, R, C> {
  type A = SheetAddress<R, C>;
  const byRows = (spec.stack || "rows") === "rows";
  const fields = [...spec.fields] as string[];
  const clash = fields.find((field) => PRESENCE_KEYS.includes(field) || field === "match");
  if (clash) throw new Error(`stacked sheet: "${clash}" is not a field name a cell can carry`);
  const keys = ["match", ...fields];
  const cellClass = spec.selector;

  function lanes(): Array<Lane<B, R, C>> {
    return spec.bouts().map((bout) => ({bout, code: spec.codeOf(bout), rows: spec.rowsOf(bout), columns: spec.columnsOf(bout)}));
  }

  // The stacked axis runs through every bout's seats in turn; the other axis
  // is the bout's own (its columns, or Brain's questions).
  function stackedAt(index: number): {lane: Lane<B, R, C>; at: number} | null {
    let rest = index;
    for (const lane of lanes()) {
      const span = byRows ? lane.rows.length : lane.columns.length;
      if (rest < span) return rest < 0 ? null : {lane, at: rest};
      rest -= span;
    }
    return null;
  }

  function stackedCount(): number {
    return lanes().reduce((sum, lane) => sum + (byRows ? lane.rows.length : lane.columns.length), 0);
  }

  const rowCount = (col: number) => (byRows ? stackedCount() : stackedAt(col)?.lane.rows.length || 0);
  const colCount = (row: number) => (byRows ? stackedAt(row)?.lane.columns.length || 0 : stackedCount());

  function addressAt(coord: CellCoord): A | null {
    const hit = stackedAt(byRows ? coord.row : coord.col);
    if (!hit) return null;
    const row = byRows ? hit.lane.rows[hit.at] : hit.lane.rows[coord.row];
    const column = byRows ? hit.lane.columns[coord.col] : hit.lane.columns[hit.at];
    if (!row || !column) return null;
    return {match: hit.lane.code, ...row, ...column} as A;
  }

  const same = (part: AddressPart, read: (key: string) => string | undefined) =>
    Object.entries(part).every(([key, value]) => read(key) === String(value));

  // locate finds a cell by what it carries: its bout among those drawn, the
  // row and the column whose fields it holds.
  function locate(read: (key: string) => string | undefined): {coord: CellCoord; address: A} | null {
    let offset = 0;
    for (const lane of lanes()) {
      const span = byRows ? lane.rows.length : lane.columns.length;
      if (lane.code === read("match")) {
        const r = lane.rows.findIndex((row) => same(row, read));
        const c = lane.columns.findIndex((column) => same(column, read));
        if (r < 0 || c < 0) return null;
        const coord = byRows ? {row: offset + r, col: c} : {row: r, col: offset + c};
        return {coord, address: {match: lane.code, ...lane.rows[r], ...lane.columns[c]} as A};
      }
      offset += span;
    }
    return null;
  }

  function dataset(address: A): Record<string, string> {
    const out: Record<string, string> = {};
    for (const key of keys) out[key] = String((address as Record<string, AddressValue>)[key] ?? "");
    return out;
  }

  function selectorOf(address: A): string {
    const data = dataset(address);
    return cellClass + keys.map((key) => `[${dataAttr(key)}="${cssEscape(data[key])}"]`).join("");
  }

  function addressOf(cell: {dataset: Record<string, string | undefined>}): A | null {
    return locate((key) => cell.dataset[key])?.address || null;
  }

  function coordOf(address: A): CellCoord | null {
    const data = dataset(address);
    return locate((key) => data[key])?.coord || null;
  }

  function addresses(): A[] {
    const out: A[] = [];
    for (const lane of lanes()) {
      for (const row of lane.rows) for (const column of lane.columns) out.push({match: lane.code, ...row, ...column} as A);
    }
    return out;
  }

  function applyMarks(edits: CellEdit[]): void {
    const marks = spec.marks;
    if (!marks) return;
    const touched = new Set<string>();
    for (const edit of edits) {
      const cell = edit.cell as HTMLElement;
      const address = addressOf(cell);
      if (!address) continue;
      const before = marks.markOf(address);
      if (before === null) continue;
      const mark = parseMark(edit.value);
      if (before === mark) continue;
      marks.setMark(address, mark);
      marks.patch(address.match, marks.pathOf(address), mark);
      paintMark(cell, mark);
      touched.add(address.match);
    }
    if (touched.size) marks.onWritten?.([...touched]);
  }

  function cursor(root: HTMLElement, options: StackedCursorOptions): SheetCursor {
    return createSheetCursor({
      ...options,
      root,
      cellSelector: cellClass,
      rows: rowCount,
      cols: colCount,
      coordOf: (cell) => locate((key) => (cell as HTMLElement).dataset[key])?.coord || null,
      cellAt: (coord) => {
        const address = addressAt(coord);
        return address ? root.querySelector<HTMLElement>(selectorOf(address)) : null;
      },
      applyValues: options.applyValues || applyMarks,
    });
  }

  return {
    cursorKind: {selector: cellClass, keys},
    dataset,
    selectorOf,
    addressOf,
    addresses,
    rows: rowCount,
    cols: colCount,
    coordOf,
    addressAt,
    applyMarks,
    cursor,
    boutOf: (code) => spec.bouts().find((bout) => spec.codeOf(bout) === code),
  };
}

// paintMark shows a mark: green for right, red for wrong, paper for none. The
// cursor reads it back off the class.
export function paintMark(cell: Element, mark: Mark): void {
  cell.classList.toggle("right", mark === "right");
  cell.classList.toggle("wrong", mark === "wrong");
}

function dataAttr(key: string): string {
  return "data-" + key.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
}
