// reorderable.ts — putting the rows of a list field in another order: the
// sources of a question, its authors, a session's testers. A row moves by its
// grip, dragged with a mouse or a finger (pointer events, unlike the HTML5
// drag the list manager (listsmanage.ts) uses, which a phone does not do), or by Alt and
// an arrow key from inside it. The DOM is the order: a field reads its rows
// back in the order they stand, and the move ends with an input event, which
// is what tells the card it has changed.
import { xyApp } from "./app.js";
import { icon } from "./icons_gen.js";
import { dragAfterIn } from "./dragrank.js";

const { el } = xyApp;

export interface ReorderOptions {
  // Which children of the container are the rows.
  item: string;
  // The part of a row that starts a drag: its grip, or the whole row.
  handle: string;
  // Rows stacked top to bottom (y) or a line of tags (x).
  axis: "x" | "y";
  // Told once a row has moved, with where it was and where it is now, for a
  // field that keeps its own list beside the DOM.
  moved?(from: number, to: number): void;
}

// moveIndex is a list with the item at `from` taken out and put back at `to`.
export function moveIndex<T>(list: readonly T[], from: number, to: number): T[] {
  const out = [...list];
  const [it] = out.splice(from, 1);
  out.splice(to, 0, it);
  return out;
}

// readingAfter is which of a wrapping line of tags the dragged one goes in
// front of, in reading order: the first that starts on a later line, or on
// the pointer's line past the pointer (null: the end).
export function readingAfter(rects: readonly { left: number; top: number; width: number; height: number }[], x: number, y: number): number | null {
  const i = rects.findIndex((r) => r.top > y || (r.top + r.height >= y && r.left + r.width / 2 > x));
  return i < 0 ? null : i;
}

// grip is the handle a row is dragged by.
export function grip(title: string): HTMLElement {
  return el("span", { class: "reorder-grip", title, "aria-hidden": "true" }, icon("grip-vertical"));
}

const KEYS: Record<ReorderOptions["axis"], Record<string, number>> = {
  y: { ArrowUp: -1, ArrowDown: 1 },
  x: { ArrowLeft: -1, ArrowRight: 1 },
};

export function reorderable(box: HTMLElement, opts: ReorderOptions): void {
  const rows = (): HTMLElement[] => [...box.children].filter((c): c is HTMLElement => c instanceof HTMLElement && c.matches(opts.item));
  const after = (els: HTMLElement[], e: PointerEvent): Element | null => {
    if (opts.axis === "y") return dragAfterIn(els, e.clientY);
    const i = readingAfter(els.map((c) => c.getBoundingClientRect()), e.clientX, e.clientY);
    return i === null ? null : els[i];
  };

  function done(row: HTMLElement, from: number): void {
    const to = rows().indexOf(row);
    if (to === from) return;
    opts.moved?.(from, to);
    box.dispatchEvent(new Event("input", { bubbles: true }));
  }

  let drag: { row: HTMLElement; from: number } | null = null;

  box.addEventListener("pointerdown", (e) => {
    const t = e.target as HTMLElement;
    const handle = t.closest<HTMLElement>(opts.handle);
    const row = handle?.closest<HTMLElement>(opts.item);
    if (e.button !== 0 || !handle || !row || row.parentElement !== box) return;
    // A button inside a handle (a tag's ×) is still a button.
    if (t.closest("button") && !t.closest("button")!.matches(opts.handle)) return;
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    drag = { row, from: rows().indexOf(row) };
    row.classList.add("reorder-dragging");
  });

  box.addEventListener("pointermove", (e) => {
    if (!drag) return;
    const others = rows().filter((r) => r !== drag!.row);
    const before = after(others, e);
    const anchor = before ?? others[others.length - 1]?.nextSibling ?? null;
    if (anchor !== drag.row && drag.row.nextSibling !== anchor) box.insertBefore(drag.row, anchor);
  });

  const end = (): void => {
    if (!drag) return;
    const d = drag;
    drag = null;
    d.row.classList.remove("reorder-dragging");
    done(d.row, d.from);
  };
  box.addEventListener("pointerup", end);
  box.addEventListener("pointercancel", end);

  // Alt and an arrow moves the row the focus is in by one, and the focus
  // stays with it, or with the same place in it when the field redraws.
  box.addEventListener("keydown", (e) => {
    const by = KEYS[opts.axis][e.key];
    const row = (e.target as HTMLElement).closest<HTMLElement>(opts.item);
    if (!e.altKey || !by || !row || row.parentElement !== box) return;
    const list = rows();
    const from = list.indexOf(row);
    const to = from + by;
    if (to < 0 || to >= list.length) return;
    e.preventDefault();
    const focused = document.activeElement as HTMLElement | null;
    const at = focused ? [...row.querySelectorAll<HTMLElement>("*")].indexOf(focused) : -1;
    box.insertBefore(row, by < 0 ? list[to] : list[to].nextSibling);
    done(row, from);
    if (focused && !focused.isConnected) rows()[to]?.querySelectorAll<HTMLElement>("*")[at]?.focus();
    else focused?.focus();
  });
}
