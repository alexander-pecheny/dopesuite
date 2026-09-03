// labelfilter.ts — "Label filter": a way of LOOKING at the board, never a
// way of changing it. Pick labels, pick all/any/none, and every list draws
// only the cards that match. Nothing is written, and nothing downstream of the
// drawing is touched: numbering, the exports, "Move list" and transfer
// all still see every card, so a package can never quietly ship short.
//
// The decisions are pure (jstest covers them); the modal, the bar and the board
// wiring are below them.
import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";

const { el, byId } = xyApp;

// all — the card carries every picked label; any — at least one; none —
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
// list and then carried across, so a filtered tour reads "1, 4, 7" — the number
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

  // A label the filter names may be deleted from the board under it ("Labels").
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
    { mode: "all", word: S.board.filter.allWord(), title: S.board.filter.allTitle(), phrase: S.board.filter.allPhrase() },
    { mode: "any", word: S.board.filter.anyWord(), title: S.board.filter.anyTitle(), phrase: S.board.filter.anyPhrase() },
    { mode: "none", word: S.board.filter.noneWord(), title: S.board.filter.noneTitle(), phrase: S.board.filter.nonePhrase() },
  ];
  const copyFor = (m: FilterMode): ModeCopy => MODES.find((c) => c.mode === m)!;

  function renderBody(): void {
    const body = byId("filterBody");
    const labels = [...deps.board.state.labels].sort((a, b) => a.name.localeCompare(b.name));
    if (!labels.length) {
      body.replaceChildren(el("p", { class: "label-empty", text: S.board.filter.noLabels() }));
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
    const reset = el("button", { class: "btn btn-ghost btn-small", type: "button", text: S.board.filter.reset() });
    reset.addEventListener("click", clear);
    body.replaceChildren(
      el("div", { class: "u-row u-gap-sm u-align-center u-wrap" }, el("span", { class: "hint", text: S.board.filter.lead() }), seg),
      row,
      // A card's dots are its unscoped labels only, so a card can match here on
      // a verdict from a test sitting. Better said out loud than discovered.
      el("p", { class: "hint", text: S.board.filter.scopedNote() }),
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
    const edit = el("button", { class: "btn btn-ghost btn-small", type: "button", text: S.board.filter.edit() });
    edit.addEventListener("click", open);
    const off = el("button", { class: "btn btn-ghost btn-small", type: "button", text: S.board.filter.reset() });
    off.addEventListener("click", clear);
    bar.replaceChildren(
      el("span", { class: "filter-bar-what", text: S.board.filter.barWhat(phrase, names.join(", ")) }),
      el("span", { class: "hint", text: S.board.filter.barNodrag() }),
      edit, off,
    );
  }

  function open(): void {
    filterModal.open();
    renderBody();
  }

  const panel: BoardPanel = {
    id: "label-filter", menu: "board", icon: "funnel",
    label: () => (active() ? S.board.filter.menuActive(String(state().labels.length)) : S.board.filter.title()),
    title: S.board.filter.menuTitle(),
    open,
  };

  return { panel, active, keep, clear, renderBar };
}

export type LabelFilter = ReturnType<typeof createLabelFilter>;
