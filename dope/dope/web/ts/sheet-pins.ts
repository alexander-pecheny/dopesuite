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
