// xyimagearrange.ts — arranging an Edited Image's boxes the way Figma does
// (ADR-0024): aligning a selection's edges or centres, spacing it out evenly,
// and the grid a box snaps to while it is dragged. All pure, over rectangles
// in millimetres; the editor applies the results.

export interface Box { x: number; y: number; w: number; h: number }

export type AlignMode = "left" | "hcenter" | "right" | "top" | "vmiddle" | "bottom";
export type Axis = "x" | "y";

const HALF = 2;
// Spacing out needs a box between the two that stay.
export const MIN_DISTRIBUTE = 3;

// bounds is the smallest rectangle that holds every box.
export function bounds(rs: readonly Box[]): Box {
  const x = Math.min(...rs.map((r) => r.x));
  const y = Math.min(...rs.map((r) => r.y));
  const right = Math.max(...rs.map((r) => r.x + r.w));
  const bottom = Math.max(...rs.map((r) => r.y + r.h));
  return { x, y, w: right - x, h: bottom - y };
}

// align moves each box so that the named edge or centre lines up with the
// same edge or centre of `to`: the selection's bounds when several boxes are
// selected, the frame when there is one.
export function align(rs: readonly Box[], mode: AlignMode, to: Box): void {
  for (const r of rs) ALIGN[mode](r, to);
}

const ALIGN: Record<AlignMode, (r: Box, to: Box) => void> = {
  left: (r, to) => { r.x = to.x; },
  hcenter: (r, to) => { r.x = to.x + (to.w - r.w) / HALF; },
  right: (r, to) => { r.x = to.x + to.w - r.w; },
  top: (r, to) => { r.y = to.y; },
  vmiddle: (r, to) => { r.y = to.y + (to.h - r.h) / HALF; },
  bottom: (r, to) => { r.y = to.y + to.h - r.h; },
};

// distribute makes the gaps between neighbouring boxes equal along one axis.
// The two outermost boxes stay where they are.
export function distribute(rs: readonly Box[], axis: Axis): void {
  const size = axis === "x" ? "w" : "h";
  const order = [...rs].sort((a, b) => a[axis] - b[axis]);
  if (order.length < MIN_DISTRIBUTE) return;
  const first = order[0];
  const last = order[order.length - 1];
  const span = last[axis] + last[size] - first[axis];
  const gap = (span - order.reduce((a, r) => a + r[size], 0)) / (order.length - 1);
  let at = first[axis] + first[size] + gap;
  for (const r of order.slice(1, -1)) {
    r[axis] = at;
    at += r[size] + gap;
  }
}

// The grid is the editor's, not the picture's: one setting for every image
// this person edits, remembered on this device.
export interface Grid { step: number; show: boolean; snap: boolean }

const GRID_KEY = "xy.xyimage.grid";
const DEFAULT_STEP = 5; // mm
export const GRID_STEP_RANGE = { min: 0.5, max: 100 };

export function loadGrid(): Grid {
  const out: Grid = { step: DEFAULT_STEP, show: false, snap: false };
  try {
    const saved = JSON.parse(localStorage.getItem(GRID_KEY) ?? "null") as Partial<Grid> | null;
    if (saved && typeof saved.step === "number" && saved.step >= GRID_STEP_RANGE.min) out.step = Math.min(GRID_STEP_RANGE.max, saved.step);
    if (saved?.show === true) out.show = true;
    if (saved?.snap === true) out.snap = true;
  } catch { /* storage refused: the defaults */ }
  return out;
}

export function saveGrid(g: Grid): void {
  try { localStorage.setItem(GRID_KEY, JSON.stringify(g)); } catch { /* storage refused: not remembered */ }
}

// snapPoint puts a coordinate on the nearest grid line, the lines running
// from `origin` (the frame's corner) every `step`.
export function snapPoint(v: number, origin: number, step: number): number {
  return origin + Math.round((v - origin) / step) * step;
}

// snapShift is how far to move a box of this start and size along one axis
// so that whichever of its two edges is nearer a grid line lands on it.
export function snapShift(start: number, size: number, origin: number, step: number): number {
  const lo = snapPoint(start, origin, step) - start;
  const hi = snapPoint(start + size, origin, step) - (start + size);
  return Math.abs(lo) <= Math.abs(hi) ? lo : hi;
}
