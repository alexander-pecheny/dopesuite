// A sheet's pinned columns, declared once. A sheet names the columns that stay
// at its left edge while the rest scrolls under them, in order, each with its
// width as CSS (a length or the var() its width rule reads). This module works
// out where each one starts, as a calc() of the widths before it, so a phone
// that gives a column another width through a media query moves the columns
// after it too. A cell of a pinned column is then marked: the one `pinned`
// class, its `left` written on the cell, and `pin-edge` on the last column,
// which draws the cue that the sheet has scrolled under the pinned block.
//
// The offset is written on the cell rather than chosen by a selector. Before
// this, every format restated the chain in its own rule (a phone block, the
// spectator sheet, Hamsa's wider total, Multi's wider total, the fight page's
// row marker, OD's unfrozen place), and rules that outranked one another moved
// columns over their neighbours. A column a sheet does not declare is not
// pinned at all, so OD's detailed sheet leaves its place out of the
// declaration instead of undoing the pin with a later selector.
//
// The cue is one class on the scrolling frame, `pins-scrolled`, which
// `bindPinnedScroll` keeps in step with the frame's scroll. The game shell
// binds it on every page's sheet frame.
//
// The head rows are pinned the same way, downwards. A sheet's head can be more
// than one row: Hamsa's rounds over its themes, Multi's minigames over their
// questions, OD's question numbers over their locks. Every row sticks, and a row after the
// first sticks where the rows above it end, so the sheet scrolls under all of
// them and no row covers another. `sheetHead` builds every sheet's thead: each
// row but the last declares its height as CSS, and the module writes that
// height on the row's cells and each row's `top` as a calc() of the heights
// above it. Before this, three sheets restated the offsets in tr:nth-child
// rules and Hamsa had none, so its themes row stuck over its rounds.

import {bindScrollEdges} from "./widgets.js";
import type {ScrollEdgeBinding} from "./widgets.js";

export interface PinnedColumn {
  // The column's name in the sheet's own words: "name", "total", "place".
  key: string;
  // Its width as CSS: "var(--team-col)", "12px", a calc().
  width: string;
}

export interface Pins {
  // The keys of the pinned columns, in order.
  readonly keys: readonly string[];
  // Where each pinned column starts, as CSS, in declaration order.
  readonly offsets: readonly string[];
  // Where the pinned block ends: a head that rides the scroll past it starts here.
  readonly end: string;
  // Whether the sheet pins the column with this key.
  pinned(key: string): boolean;
  // Marks a cell of the column with this key as pinned. A key the sheet did
  // not declare leaves the cell as it is, so the cell scrolls.
  mark<T extends Element>(cell: T, key: string): T;
  // Marks a cell that spans the whole pinned block, such as a head over all
  // of it: it starts where the block starts and carries the block's edge.
  markSpan<T extends Element>(cell: T): T;
  // Makes an element ride the scroll just past the pinned block, such as the
  // name of a Multi minigame over a block of columns dozens wide. It gets no class:
  // it is not part of the block and draws no edge.
  markTrailing<T extends Element>(element: T): T;
}

export const PINNED_CLASS = "pinned";
export const PIN_EDGE_CLASS = "pin-edge";
export const PINS_SCROLLED_CLASS = "pins-scrolled";

// pinOffsets is the pure arithmetic: the start of each column and the end of
// the block, each a CSS length. With no start (or "0px") the first column
// starts at 0px and the terms leave the zero out.
export function pinOffsets(widths: readonly string[], start = "0px"): {offsets: string[]; end: string} {
  const terms = start && start !== "0px" && start !== "0" ? [start] : [];
  const sum = (): string => (terms.length === 0 ? "0px" : terms.length === 1 ? terms[0] : `calc(${terms.join(" + ")})`);
  const offsets: string[] = [];
  for (const width of widths) {
    offsets.push(sum());
    terms.push(width);
  }
  return {offsets, end: sum()};
}

interface PinTarget {
  classList: {add(...names: string[]): void};
  style: {setProperty(name: string, value: string): void};
}

// declarePins takes a sheet's pinned columns and gives back what marks their
// cells. `start` is where the first column sits, for a sheet that leaves a
// gutter before it.
export function declarePins(columns: readonly PinnedColumn[], options: {start?: string} = {}): Pins {
  const keys = columns.map((column) => column.key);
  if (new Set(keys).size !== keys.length) throw new Error(`sheet-pins: a key is declared twice: ${keys.join(", ")}`);
  const {offsets, end} = pinOffsets(columns.map((column) => column.width), options.start);
  const last = keys.length - 1;
  const pin = <T extends Element>(cell: T, index: number, edge: boolean): T => {
    const target = cell as unknown as PinTarget;
    target.classList.add(PINNED_CLASS);
    if (edge) target.classList.add(PIN_EDGE_CLASS);
    target.style.setProperty("position", "sticky");
    target.style.setProperty("left", offsets[index]);
    return cell;
  };
  return {
    keys,
    offsets,
    end,
    pinned: (key) => keys.includes(key),
    mark(cell, key) {
      const index = keys.indexOf(key);
      return index < 0 ? cell : pin(cell, index, index === last);
    },
    markSpan(cell) {
      return keys.length === 0 ? cell : pin(cell, 0, true);
    },
    markTrailing(element) {
      const target = element as unknown as PinTarget;
      target.style.setProperty("position", "sticky");
      target.style.setProperty("left", end);
      return element;
    },
  };
}

// bindPinnedScroll marks the frame once its content has scrolled sideways, so
// the last pinned column of every sheet in it draws its edge.
export function bindPinnedScroll(frame: Element | null | undefined): ScrollEdgeBinding {
  return bindScrollEdges(frame, ({left}, el) => {
    el.classList.toggle(PINS_SCROLLED_CLASS, left);
  });
}

// === head rows ===

export interface HeadRow {
  row: HTMLTableRowElement;
  // The row's height as CSS ("28px", "var(--head-row)"). Every row but the
  // last declares one, because the rows below it stick where it ends; the
  // last row may leave it to the stylesheet.
  height?: string;
}

// headRowTops is the pure arithmetic: where each head row sticks, as CSS.
// It takes the heights of the rows in order; the last one's is not needed.
export function headRowTops(heights: readonly (string | undefined)[]): string[] {
  if (heights.length === 0) return [];
  const above = heights.slice(0, -1);
  const missing = above.findIndex((height) => !height);
  if (missing >= 0) throw new Error(`sheet-pins: head row ${missing + 1} of ${heights.length} declares no height, and the rows below it stick where it ends`);
  const {offsets, end} = pinOffsets(above as string[]);
  return [...offsets, end];
}

interface HeadCellTarget extends PinTarget {
  rowSpan?: number;
}

interface HeadRowTarget {
  children: ArrayLike<HeadCellTarget>;
}

// stackHeadRows pins each head row below the ones above it: every cell gets
// `position: sticky` and its row's `top`, and a cell of a row that declares
// its height, spanning that row alone, gets the height. A cell spanning
// several rows starts in the first of them and keeps the height the rows give
// it.
export function stackHeadRows(rows: readonly HeadRow[]): void {
  const tops = headRowTops(rows.map((row) => row.height));
  rows.forEach(({row, height}, index) => {
    for (const cell of Array.from((row as unknown as HeadRowTarget).children)) {
      cell.style.setProperty("position", "sticky");
      cell.style.setProperty("top", tops[index]);
      if (height && (cell.rowSpan ?? 1) === 1) cell.style.setProperty("height", height);
    }
  });
}

// sheetHead is a sheet's thead: its rows in order, each stuck below the ones
// above it. Every table a page builds makes its head here, so a sheet that
// grows a second head row cannot leave it sticking over the first.
export function sheetHead(rows: readonly HeadRow[]): HTMLTableSectionElement {
  const thead = document.createElement("thead");
  for (const {row} of rows) thead.appendChild(row);
  stackHeadRows(rows);
  return thead;
}
