// labelfilter.ts — «Фильтр по меткам»: a way of LOOKING at the board, never a
// way of changing it. Pick labels, pick все/любая/ни одной, and every list draws
// only the cards that match. Nothing is written, and nothing downstream of the
// drawing is touched: numbering, the exports, «Переместить список» and transfer
// all still see every card, so a package can never quietly ship short.
//
// The decisions are pure (jstest covers them); the modal, the bar and the board
// wiring are below them.
import { xyApp } from "./app.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";

const { el, byId } = xyApp;

// все — the card carries every picked label; любая — at least one; ни одной —
// none of them. With one label picked the first two are the same filter.
export type FilterMode = "all" | "any" | "none";

export interface FilterState {
  mode: FilterMode;
  labels: readonly number[];
}

export function filterActive(f: FilterState): boolean {
  return f.labels.length > 0;
}

// matches reads the card's WHOLE assignment set — a label scoped to a Playing
// counts, though the card's dots show only the unscoped ones. So a card can
// match on a label that is nowhere on its face; the modal says so.
export function matches(f: FilterState, cardLabels: ReadonlySet<number>): boolean {
  if (!filterActive(f)) return true;
  switch (f.mode) {
    case "all": return f.labels.every((id) => cardLabels.has(id));
    case "any": return f.labels.some((id) => cardLabels.has(id));
    case "none": return !f.labels.some((id) => cardLabels.has(id));
  }
}

// shownCards is what one list draws. The numbers are computed over the WHOLE
// list and then carried across, so a filtered тур reads «1, 4, 7» — the number
// belongs to the question, not to the view. `keep` null means no filter, and
// then the arrays are handed straight back.
export function shownCards<C>(
  cards: readonly C[],
  numbers: ReadonlyArray<string | null>,
  keep: ((card: C) => boolean) | null,
): { cards: readonly C[]; numbers: ReadonlyArray<string | null> } {
  if (!keep) return { cards, numbers };
  const out: C[] = [];
  const nums: Array<string | null> = [];
  cards.forEach((c, i) => {
    if (!keep(c)) return;
    out.push(c);
    nums.push(numbers[i] ?? null);
  });
  return { cards: out, numbers: nums };
}

export const xyLabelFilter = { filterActive, matches, shownCards };

export interface FilterDeps {
  board: Board;
  // The chips take their fill from the label's own colour, once in the DOM.
  paintLabels(): void;
  onChange(): void;
}

const EMPTY: ReadonlySet<number> = new Set();

export function createLabelFilter(deps: FilterDeps) {
  const filterModal = modal("filter");
  let mode: FilterMode = "all";
  let picked = new Set<number>();

  // A label the filter names may be deleted from the board under it («Метки»).
  // Only the ones that still exist count, so the last one going takes the filter
  // with it and the board comes back whole rather than emptying out.
  const state = (): FilterState => ({
    mode,
    labels: deps.board.state.labels.filter((l) => picked.has(l.id)).map((l) => l.id),
  });
  const active = (): boolean => filterActive(state());

  // keep is what a list filters by, or null when no filter is on — the shape
  // shownCards wants. The assignments are indexed once per render rather than
  // rescanned per card: every list calls this, and a board carries thousands.
  function keep(): ((card: { id: number }) => boolean) | null {
    if (!active()) return null;
    const f = state();
    const byCard = new Map<number, Set<number>>();
    for (const a of deps.board.state.cardLabels) {
      let set = byCard.get(a.cardId);
      if (!set) byCard.set(a.cardId, (set = new Set()));
      set.add(a.labelId);
    }
    return (card) => matches(f, byCard.get(card.id) || EMPTY);
  }

  function apply(): void {
    renderBar();
    deps.onChange();
  }

  function clear(): void {
    picked = new Set();
    apply();
    renderBody();
  }

  interface ModeCopy { mode: FilterMode; word: string; title: string; phrase: string }
  const MODES: ModeCopy[] = [
    { mode: "all", word: "все", title: "Карточка несёт все выбранные метки", phrase: "Со всеми метками" },
    { mode: "any", word: "любая", title: "Карточка несёт хотя бы одну из выбранных", phrase: "С любой из меток" },
    { mode: "none", word: "ни одной", title: "Карточка не несёт ни одной из выбранных", phrase: "Без меток" },
  ];
  const copyFor = (m: FilterMode): ModeCopy => MODES.find((c) => c.mode === m)!;

  function renderBody(): void {
    const body = byId("filterBody");
    const labels = [...deps.board.state.labels].sort((a, b) => a.name.localeCompare(b.name));
    if (!labels.length) {
      body.replaceChildren(el("p", { class: "label-empty", text: "На доске нет меток." }));
      return;
    }
    const seg = el("div", { class: "seg" });
    for (const c of MODES) {
      const btn = el("button", { class: "seg-btn" + (c.mode === mode ? " active" : ""), type: "button", text: c.word, title: c.title });
      btn.addEventListener("click", () => { mode = c.mode; renderBody(); apply(); });
      seg.append(btn);
    }
    const row = el("div", { class: "label-picker" });
    for (const l of labels) {
      const chip = el("button", { class: "label-pick" + (picked.has(l.id) ? " active" : ""), type: "button", dataset: { c: l.color }, text: l.name });
      chip.addEventListener("click", () => {
        if (!picked.delete(l.id)) picked.add(l.id);
        renderBody();
        apply();
      });
      row.append(chip);
    }
    const reset = el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Сбросить" });
    reset.addEventListener("click", clear);
    body.replaceChildren(
      el("div", { class: "filter-modes" }, el("span", { class: "filter-modes-label", text: "Показывать карточки, у которых" }), seg),
      row,
      // A card's dots are its unscoped labels only, so a card can match here on
      // a verdict from a test sitting. Better said out loud than discovered.
      el("p", { class: "hint", text: "Метка засчитывается и тогда, когда она проставлена на тесте — такие метки на карточке не видны." }),
      reset,
    );
    deps.paintLabels();
  }

  function renderBar(): void {
    const bar = byId("filterBar");
    bar.hidden = !active();
    if (!active()) { bar.replaceChildren(); return; }
    const names = state().labels.map((id) => deps.board.state.labels.find((l) => l.id === id)!.name);
    const phrase = copyFor(mode).phrase;
    const edit = el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Изменить" });
    edit.addEventListener("click", open);
    const off = el("button", { class: "btn btn-ghost btn-small", type: "button", text: "Сбросить" });
    off.addEventListener("click", clear);
    bar.replaceChildren(
      el("span", { class: "filter-bar-what", text: `${phrase}: ${names.join(", ")}` }),
      el("span", { class: "filter-bar-note", text: "перетаскивание внутри списка выключено" }),
      edit, off,
    );
  }

  function open(): void {
    filterModal.open();
    renderBody();
  }

  const panel: BoardPanel = {
    id: "label-filter", menu: "board", icon: "funnel",
    label: () => (active() ? `Фильтр по меткам · ${state().labels.length}` : "Фильтр по меткам"),
    title: "Показать только карточки с выбранными метками — вид, который ничего не меняет на доске",
    open,
  };

  return { panel, active, keep, clear, renderBar };
}

export type LabelFilter = ReturnType<typeof createLabelFilter>;
