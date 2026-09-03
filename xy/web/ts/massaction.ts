// massaction.ts — the decision half of "Mass action": which cards are
// selected, what a select-all does, and how a partly-failed run reads.
//
// Board-wide rather than per-list, because the chore that wants it — "collect
// all rejected questions from three tours into Reserve" — spans lists. The DOM, the
// pickers and the writes are masspanel.ts; everything here is pure so jstest
// can exercise the rules without a board.

import S from "./i18nstrings.js";

// One selectable thing, as this module needs to see it.
export interface SelectableCard { id: number }

// allSelected reports whether every card in `ids` is already picked — the state
// a list's header checkbox shows, and what decides which way it toggles. An
// empty list is not "all selected": there is nothing to deselect.
export function allSelected(selected: ReadonlySet<number>, ids: readonly number[]): boolean {
  return ids.length > 0 && ids.every((id) => selected.has(id));
}

// toggleAll is the header checkbox: select every card of the list, or — when
// they already all are — drop exactly those, leaving other lists' picks alone.
export function toggleAll(selected: ReadonlySet<number>, ids: readonly number[]): Set<number> {
  const next = new Set(selected);
  if (allSelected(selected, ids)) ids.forEach((id) => next.delete(id));
  else ids.forEach((id) => next.add(id));
  return next;
}

export function toggleOne(selected: ReadonlySet<number>, id: number): Set<number> {
  const next = new Set(selected);
  if (!next.delete(id)) next.add(id);
  return next;
}

// prune drops picks whose card is gone — deleted by this run, or by someone else
// while the mode was open. Without it a stale id would be "selected" forever and
// every later action would report a failure nobody can see.
export function prune(selected: ReadonlySet<number>, cards: readonly SelectableCard[]): Set<number> {
  const live = new Set(cards.map((c) => c.id));
  return new Set([...selected].filter((id) => live.has(id)));
}

// ordered returns the selected cards in board order, not click order: a bulk
// move must land them in the order the reader sees them, or the target list
// comes out shuffled by how the picking happened to go.
export function ordered<T extends SelectableCard>(selected: ReadonlySet<number>, cards: readonly T[]): T[] {
  return cards.filter((c) => selected.has(c.id));
}

export function cardCount(n: number): string {
  return S.board.count.cards(n);
}

// runSummary reports a finished run. A bulk write is not a transaction — one
// card failing must not undo the rest — so the count that failed is said out
// loud rather than folded into a single "done".
export function runSummary(ok: number, failed: number): string {
  if (!failed) return S.board.mass.done(cardCount(ok));
  return S.board.mass.doneFailed(String(ok), String(failed));
}

// The actions the bar offers. `needs` says what the dialog must ask for before
// it can run: nothing (delete), a destination (move/copy), a label, or a test.
export type MassNeed = "none" | "target" | "label" | "session";
export interface MassAction {
  key: string;
  label: string;
  title: string;
  needs: MassNeed;
  danger?: boolean;
  // The dialog's primary button — "Delete 7 cards", not "OK".
  verb: string;
}

export const MASS_ACTIONS: MassAction[] = [
  { key: "move", label: S.board.mass.moveLabel(), title: S.board.mass.moveTitle(), needs: "target", verb: S.board.actions.move() },
  { key: "copy", label: S.board.mass.copyLabel(), title: S.board.mass.copyTitle(), needs: "target", verb: S.board.actions.copy() },
  { key: "label-add", label: S.board.mass.labelAddLabel(), title: S.board.mass.labelAddTitle(), needs: "label", verb: S.board.mass.labelAddVerb() },
  { key: "label-del", label: S.board.mass.labelDelLabel(), title: S.board.mass.labelDelTitle(), needs: "label", verb: S.board.mass.labelDelVerb() },
  { key: "session-add", label: S.board.mass.sessionAddLabel(), title: S.board.mass.sessionAddTitle(), needs: "session", verb: S.board.mass.sessionAddVerb() },
  { key: "session-del", label: S.board.mass.sessionDelLabel(), title: S.board.mass.sessionDelTitle(), needs: "session", verb: S.board.mass.sessionDelVerb() },
  { key: "delete", label: S.board.mass.deleteLabel(), title: S.board.mass.deleteTitle(), needs: "none", danger: true, verb: S.board.mass.deleteLabel() },
];

export const xyMass = {
  allSelected, toggleAll, toggleOne, prune, ordered, cardCount, runSummary, MASS_ACTIONS,
};
