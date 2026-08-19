// replace.ts — «Найти и заменить»: one replacement over the whole board, one
// list or one group. The matching is literal (find.ts) and the structure is out
// of its reach, so what is left to judge is context: the same needle in two
// places can want two different answers, which is why the preview ticks
// Occurrences and not cards.

import { xyApp } from "./app.js";
import { xyFind } from "./find.js";
import { xyMass } from "./massaction.js";
import { xySearchIndex } from "./searchindex.js";
import { boardOrder, byRank } from "./dragrank.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";
import type { Rewrites, DescChange } from "./rewrites.js";
import type { BoardCard } from "./unlock.js";
import type { Span as FindSpan } from "./find.js";

const { el, byId, errMsg } = xyApp;

export function createReplacePanel(board: Board, rewrites: Pick<Rewrites, "apply">): BoardPanel {
  const replaceModal = modal("replace");
  const replaceScope = byId<HTMLSelectElement>("replaceScope");
  const replaceFrom = byId<HTMLInputElement>("replaceFrom");
  const replaceTo = byId<HTMLInputElement>("replaceTo");
  const replaceCase = byId<HTMLInputElement>("replaceCase");

  interface Occurrence { i: number; card: BoardCard; span: FindSpan }

  // Rendering thousands of rows with a checkbox each freezes the tab; a page holds
  // a hundred. Ticks live over ALL occurrences, so what is off-screen is still
  // replaced and still counted.
  const REPLACE_PAGE = 100;
  let occurrences: Occurrence[] = [];
  let perCard = new Map<number, number[]>();
  // The text each card had when the plan was drawn, BY VALUE. A card object is
  // mutated in place by the editor and swapped wholesale by a snapshot reload, so
  // neither the reference nor its current .desc can say whether the spans still
  // point where the preview said they did.
  let plannedDesc = new Map<number, string>();
  let replacePicked = new Set<number>();
  let replacePageNo = 0;

  // The preview reads down the board rather than in whatever order the state is in.
  const cardsInBoardOrder = (cards: readonly BoardCard[]): BoardCard[] => boardOrder(board.state.lists, cards);

  function scopeCards(): BoardCard[] {
    const v = replaceScope.value;
    if (v.startsWith("list:")) {
      const id = Number(v.slice(5));
      return cardsInBoardOrder(board.state.cards.filter((c) => c.listId === id));
    }
    if (v.startsWith("group:")) {
      const id = Number(v.slice(6));
      const ids = new Set(board.state.lists.filter((l) => l.groupId === id).map((l) => l.id));
      return cardsInBoardOrder(board.state.cards.filter((c) => ids.has(c.listId)));
    }
    return cardsInBoardOrder(board.state.cards);
  }

  function planReplace(): void {
    const from = replaceFrom.value;
    occurrences = [];
    perCard = new Map();
    plannedDesc = new Map();
    if (from) {
      for (const card of scopeCards()) {
        for (const span of xyFind.replaceSpans(card.desc, from, replaceCase.checked)) {
          const o = { i: occurrences.length, card, span };
          occurrences.push(o);
          const ids = perCard.get(card.id);
          if (ids) ids.push(o.i); else perCard.set(card.id, [o.i]);
          plannedDesc.set(card.id, card.desc);
        }
      }
    }
    // A fresh plan is a fresh set of occurrences, so everything starts ticked —
    // which is why only the three inputs that CHANGE what was found re-plan; the
    // «Заменить на» field merely redraws (see the wiring below), or an editor's
    // unticking would be undone by fixing a typo in it.
    replacePicked = new Set(occurrences.map((o) => o.i));
    replacePageNo = 0;
    renderReplace();
  }

  function renderReplace(): void {
    const box = byId("replaceHits");
    const pages = Math.max(1, Math.ceil(occurrences.length / REPLACE_PAGE));
    replacePageNo = Math.min(replacePageNo, pages - 1);
    const slice = occurrences.slice(replacePageNo * REPLACE_PAGE, (replacePageNo + 1) * REPLACE_PAGE);
    const to = replaceTo.value;
    const rows: HTMLElement[] = [];
    // null, not -1: a card created offline HAS the id -1 (the first temp id), and
    // would then never get its heading row.
    let shownCard: number | null = null;
    for (const o of slice) {
      if (o.card.id !== shownCard) {
        shownCard = o.card.id;
        const ids = perCard.get(o.card.id) || [];
        const head = el("input", { type: "checkbox" }) as HTMLInputElement;
        head.checked = xyMass.allSelected(replacePicked, ids);
        head.addEventListener("change", () => {
          replacePicked = xyMass.toggleAll(replacePicked, ids);
          renderReplace();
        });
        rows.push(el("label", { class: "replace-card" }, head,
          el("span", { class: "replace-card-name", text: xySearchIndex.cardTitle(o.card, board.state.cardTitle, "(пустая карточка)") }),
          el("span", { class: "replace-card-count", text: `${ids.length}` })));
      }
      const snip = xyFind.snippet(o.card.desc, [o.span], 60);
      const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
      cb.checked = replacePicked.has(o.i);
      cb.addEventListener("change", () => {
        replacePicked = xyMass.toggleOne(replacePicked, o.i);
        renderReplace();
      });
      // One occurrence, one window: the mark is this occurrence's own span.
      const at = snip.marks[0];
      rows.push(el("label", { class: "replace-hit" }, cb,
        el("span", { class: "replace-hit-text" },
          snip.text.slice(0, at.start),
          el("del", { text: snip.text.slice(at.start, at.end) }),
          ...(to ? [el("ins", { text: to })] : []),
          snip.text.slice(at.end))));
    }
    box.replaceChildren(...rows);
    byId("replacePage").textContent = occurrences.length ? `Страница ${replacePageNo + 1} из ${pages}` : "";
    byId<HTMLButtonElement>("replacePrev").disabled = replacePageNo === 0;
    byId<HTMLButtonElement>("replaceNext").disabled = replacePageNo >= pages - 1;
    const picked = occurrences.filter((o) => replacePicked.has(o.i));
    const cards = new Set(picked.map((o) => o.card.id)).size;
    const run = byId<HTMLButtonElement>("replaceRun");
    run.disabled = picked.length === 0;
    // An empty «на» deletes, and the button says so rather than promising a
    // replacement with nothing.
    const verb = replaceTo.value ? "Заменить" : "Удалить";
    run.textContent = picked.length ? `${verb} ${picked.length} в ${xyMass.cardCount(cards)}` : verb;
    replaceModal.message(replaceFrom.value && !occurrences.length ? "Ничего не найдено." : "");
  }

  async function runReplace(): Promise<void> {
    const to = replaceTo.value;
    const byCard = new Map<number, { card: BoardCard; spans: FindSpan[] }>();
    for (const o of occurrences) {
      if (!replacePicked.has(o.i)) continue;
      const entry = byCard.get(o.card.id) || { card: o.card, spans: [] };
      entry.spans.push(o.span);
      byCard.set(o.card.id, entry);
    }
    // Spans are offsets into the text as it was when the preview was drawn. If that
    // text has moved since — a co-author's edit arriving with a snapshot reload, or
    // this editor's own save — applying them would corrupt the card and record a
    // desc_edit whose «before» never existed. Such a card is left out and said out
    // loud rather than rewritten blind.
    const changes: DescChange[] = [];
    let stale = 0;
    for (const x of byCard.values()) {
      const live = board.state.cards.find((c) => c.id === x.card.id);
      if (!live || live.desc !== plannedDesc.get(x.card.id)) { stale++; continue; }
      changes.push({ card: live, desc: xyFind.applySpans(live.desc, x.spans, to) });
    }
    if (!changes.length && !stale) return;
    board.setStatus("saving");
    try {
      await rewrites.apply(changes);
      board.setStatus("saved");
      // Re-plan first: what is left to replace has changed, and renderReplace owns
      // the message line, so the report has to be written after it.
      planReplace();
      replaceModal.message(`Готово: ${xyMass.cardCount(changes.length)}.` +
        (stale ? ` ${xyMass.cardCount(stale)} изменились, пока шёл просмотр — они пропущены, найдите заново.` : ""));
    } catch (err) {
      board.setStatus("error");
      replaceModal.message("Ошибка при замене: " + errMsg(err));
    }
  }

  function openReplace(): void {
    // The scope list is built on open, so a list renamed or grouped since last time
    // is named correctly.
    const groups = new Map(board.state.groups.map((g) => [g.id, g.name]));
    const seen = new Set<number>();
    const opts = [el("option", { value: "board", text: "Вся доска" })];
    for (const l of [...board.state.lists].sort(byRank)) {
      if (l.groupId != null && !seen.has(l.groupId)) {
        seen.add(l.groupId);
        opts.push(el("option", { value: `group:${l.groupId}`, text: `Группа: ${groups.get(l.groupId) || ""}` }));
      }
      opts.push(el("option", { value: `list:${l.id}`, text: l.title }));
    }
    replaceScope.replaceChildren(...opts);
    replaceModal.open();
    planReplace();
    replaceFrom.focus();
  }

  // Typing re-scans every card in scope, so it waits for a pause; the case toggle
  // and the scope select change the answer at once and re-plan immediately.
  let planTimer = 0;
  replaceFrom.addEventListener("input", () => {
    clearTimeout(planTimer);
    planTimer = setTimeout(planReplace, 200);
  });
  for (const node of [replaceCase, replaceScope]) node.addEventListener("input", () => planReplace());
  // What is replaced has not changed — only what the preview shows it becoming.
  replaceTo.addEventListener("input", () => renderReplace());
  byId("replacePrev").addEventListener("click", () => { replacePageNo--; renderReplace(); });
  byId("replaceNext").addEventListener("click", () => { replacePageNo++; renderReplace(); });
  byId("replaceRun").addEventListener("click", () => { void runReplace(); });


  return {
    id: "replace", menu: "board", icon: "replace",
    label: "Найти и заменить",
    title: "Заменить один и тот же текст во всех карточках доски, списка или группы",
    open: openReplace,
  };
}
