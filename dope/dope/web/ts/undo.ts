// undo.ts — a host's undo on a sheet of bouts: Ctrl+Z (⌘Z) takes back the
// last thing this host did, and only what this host did.
//
// Each page load keeps its own stack of the cell edits it made itself; the
// edits other hosts make never enter it. A step is everything one gesture
// changed (a mark, a pasted block, a range of marks), since those are recorded
// within one task. Undoing a step restores each cell to what it held before,
// unless somebody else has changed that cell since: then their value stands
// and the cell is skipped. Undo never overwrites another host's work.

import type {PatchPath} from "./state-sync.js";

// The deepest a host can undo.
const UNDO_LIMIT = 200;

export interface UndoEdit {
  code: string;
  path: PatchPath;
  before: unknown;
  after: unknown;
}

export interface UndoOptions {
  // What a bout's document holds at a path now: the latest view with this
  // page's own un-acked edits on it.
  current: (code: string, path: PatchPath) => unknown;
  // Write a value back, without recording it.
  apply: (code: string, path: PatchPath, value: unknown) => void;
  limit?: number;
  // When a gesture ends; a test runs it by hand.
  schedule?: (close: () => void) => void;
}

export interface UndoResult {
  // The bouts a step changed back.
  codes: string[];
  // Cells another host had changed since, left as they are.
  skipped: number;
}

export interface Undo {
  // Remember one edit this page is about to make.
  record(code: string, path: PatchPath, before: unknown, after: unknown): void;
  // Take back the last step; null when there is none.
  undo(): UndoResult | null;
  size(): number;
}

export function createUndo(options: UndoOptions): Undo {
  const limit = options.limit ?? UNDO_LIMIT;
  const schedule = options.schedule ?? ((close) => queueMicrotask(close));
  const steps: UndoEdit[][] = [];
  let open: UndoEdit[] | null = null;

  function record(code: string, path: PatchPath, before: unknown, after: unknown): void {
    if (sameValue(before, after)) return;
    if (!open) {
      open = [];
      steps.push(open);
      if (steps.length > limit) steps.shift();
      schedule(() => { open = null; });
    }
    // A cell edited twice in one gesture goes back to what it held first.
    const earlier = open.find((edit) => edit.code === code && samePath(edit.path, path));
    if (earlier) {
      earlier.after = after;
      return;
    }
    open.push({code, path: [...path], before, after});
  }

  function undo(): UndoResult | null {
    open = null;
    const step = steps.pop();
    if (!step) return null;
    const codes = new Set<string>();
    let skipped = 0;
    for (const edit of [...step].reverse()) {
      if (!sameValue(options.current(edit.code, edit.path), edit.after)) {
        skipped++;
        continue;
      }
      options.apply(edit.code, edit.path, edit.before);
      codes.add(edit.code);
    }
    return {codes: [...codes], skipped};
  }

  return {record, undo, size: () => steps.length};
}

function samePath(a: PatchPath, b: PatchPath): boolean {
  return a.length === b.length && a.every((part, i) => String(part) === String(b[i]));
}

// sameValue compares two JSON values by what they hold; absent and empty
// marks are one value, since a pristine cell may be either.
function sameValue(a: unknown, b: unknown): boolean {
  const blank = (v: unknown) => v === undefined || v === null || v === "";
  if (blank(a) && blank(b)) return true;
  return JSON.stringify(a) === JSON.stringify(b);
}

// valueAt reads a path out of a JSON document.
export function valueAt(doc: unknown, path: PatchPath): unknown {
  let node: unknown = doc;
  for (const part of path) {
    if (node === null || typeof node !== "object") return undefined;
    node = (node as Record<string, unknown>)[String(part)];
  }
  return node;
}

// isUndoKey is Ctrl+Z or ⌘Z, without Shift, on any keyboard layout (the
// physical key, so a Cyrillic layout's Z key counts too).
export function isUndoKey(event: KeyboardEvent): boolean {
  if (!(event.ctrlKey || event.metaKey) || event.shiftKey || event.altKey) return false;
  return event.code === "KeyZ" || event.key.toLowerCase() === "z";
}
