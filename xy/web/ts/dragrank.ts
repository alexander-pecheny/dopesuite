// dragrank.ts — the drag/rank kernel lifted out of board.js: drag-position
// geometry (which sibling the dragged node lands before) and fractional-rank
// recompute for drops. The decisions are pure functions over rects and id
// lists; dragAfterIn/dragAfterInX are the only DOM-aware adapters.

import { xyRank } from "./rank.js";

const { keyBetween } = xyRank;

export interface Ranked { rank: string }
export interface RankedCard extends Ranked { id: number }

// byRank orders board entities by their fractional-index rank string.
export const byRank = (a: Ranked, b: Ranked): number =>
  a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0;

export interface VExtent { top: number; height: number }
export interface HExtent { left: number; width: number }

// dragAfterIndex returns the index of the rect the dragged node should be
// inserted before, given the pointer's y (null = append at the end): the
// nearest rect whose midline is still below the pointer.
export function dragAfterIndex(rects: readonly VExtent[], y: number): number | null {
  let closest: number | null = null, closestOffset = -Infinity;
  rects.forEach((r, i) => {
    const offset = y - r.top - r.height / 2;
    if (offset < 0 && offset > closestOffset) { closestOffset = offset; closest = i; }
  });
  return closest;
}

// dragAfterIndexX is dragAfterIndex for a horizontal row of columns.
export function dragAfterIndexX(rects: readonly HExtent[], x: number): number | null {
  return dragAfterIndex(rects.map((r) => ({ top: r.left, height: r.width })), x);
}

// dragAfterIn returns which of `els` the dragged node should be inserted
// before, given the pointer's y (null = append at the end).
export function dragAfterIn(els: readonly Element[], y: number): Element | null {
  const i = dragAfterIndex(els.map((c) => c.getBoundingClientRect()), y);
  return i === null ? null : els[i];
}

// dragAfterInX is dragAfterIn for a horizontal row of columns.
export function dragAfterInX(els: readonly Element[], x: number): Element | null {
  const i = dragAfterIndexX(els.map((c) => c.getBoundingClientRect()), x);
  return i === null ? null : els[i];
}

// rankAfterMove recomputes a dropped card's rank from the target list's new
// order: `order` is the list's card ids as they appear after the drop (moved
// id included), rankOf resolves a neighbour id to its current rank (null =
// unknown → open end). Stale out-of-order neighbour ranks drop the next
// bound; an unrepresentable gap falls back to appending after prev.
export function rankAfterMove(
  order: readonly number[],
  movedId: number,
  rankOf: (id: number) => string | null,
): string {
  const idx = order.indexOf(movedId);
  const prevId = order[idx - 1], nextId = order[idx + 1];
  const prev = prevId ? rankOf(prevId) : null;
  let next = nextId ? rankOf(nextId) : null;
  if (prev !== null && next !== null && prev >= next) next = null; // guard
  try { return keyBetween(prev, next); } catch { return keyBetween(prev, null); }
}

// rankForSlot computes a fractional rank for inserting into `cards` at a 1-based
// slot ("end"/"" appends). excludeId drops the moving card from the neighbour set.
export function rankForSlot(cards: readonly RankedCard[], posValue: string, excludeId?: number): string {
  const arr = cards.filter((c) => c.id !== excludeId).sort(byRank);
  let prev: RankedCard | undefined, next: RankedCard | undefined;
  if (posValue === "end" || posValue === "") {
    prev = arr.length ? arr[arr.length - 1] : undefined;
  } else {
    const k = parseInt(posValue, 10);
    prev = k >= 2 ? arr[k - 2] : undefined;
    next = k - 1 < arr.length ? arr[k - 1] : undefined;
  }
  try { return keyBetween(prev ? prev.rank : null, next ? next.rank : null); }
  catch { return keyBetween(prev ? prev.rank : null, null); }
}

export const xyDragRank = {
  byRank, dragAfterIndex, dragAfterIndexX, dragAfterIn, dragAfterInX, rankAfterMove, rankForSlot,
};
