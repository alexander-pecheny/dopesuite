// massaction.ts — the decision half of «Массовое действие»: which cards are
// selected, what a select-all does, and how a partly-failed run reads.
//
// Board-wide rather than per-list, because the chore that wants it — «собрать
// все забракованные вопросы из трёх туров в Запас» — spans lists. The DOM, the
// pickers and the writes are masspanel.ts; everything here is pure so jstest
// can exercise the rules without a board.

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

// plural picks the Russian declension for n (1 карточка, 2 карточки, 12 карточек).
export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10, m100 = n % 100;
  return m100 >= 11 && m100 <= 14 ? many : m10 === 1 ? one : m10 >= 2 && m10 <= 4 ? few : many;
}

export function cardCount(n: number): string {
  return `${n} ${plural(n, "карточка", "карточки", "карточек")}`;
}

// runSummary reports a finished run. A bulk write is not a transaction — one
// card failing must not undo the rest — so the count that failed is said out
// loud rather than folded into a single «готово».
export function runSummary(ok: number, failed: number): string {
  if (!failed) return `Готово: ${cardCount(ok)}.`;
  return `Готово: ${ok}. Не удалось: ${failed} — они остались отмеченными.`;
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
  // The dialog's primary button — «Удалить 7 карточек», not «ОК».
  verb: string;
}

export const MASS_ACTIONS: MassAction[] = [
  { key: "move", label: "Переместить", title: "Перенести отмеченные карточки в другой список или на другую доску", needs: "target", verb: "Переместить" },
  { key: "copy", label: "Копировать", title: "Скопировать отмеченные карточки в другой список или на другую доску", needs: "target", verb: "Копировать" },
  { key: "label-add", label: "Добавить метку", title: "Поставить одну метку на все отмеченные карточки", needs: "label", verb: "Добавить" },
  { key: "label-del", label: "Снять метку", title: "Убрать одну метку со всех отмеченных карточек", needs: "label", verb: "Снять" },
  { key: "session-add", label: "Отметить тестом", title: "Отметить все выбранные карточки как сыгранные на тесте", needs: "session", verb: "Отметить" },
  { key: "session-del", label: "Снять тест", title: "Убрать отметку о тесте со всех отмеченных карточек", needs: "session", verb: "Снять" },
  { key: "delete", label: "Удалить", title: "Удалить отмеченные карточки", needs: "none", danger: true, verb: "Удалить" },
];

export const xyMass = {
  allSelected, toggleAll, toggleOne, prune, ordered, plural, cardCount, runSummary, MASS_ACTIONS,
};
