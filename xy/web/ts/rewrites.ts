// rewrites.ts — the board-wide description rewrites: the Trello clean-up, the
// typography pass (with its stress-mark review) and the legacy Version
// conversion. All three are one walk — collect what a transform changes, then
// patch each changed card with a desc_edit timeline entry so the rewrite is
// auditable and reversible — so they share it, and find-and-replace borrows it.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyVersions } from "./versions.js";
import { xyTypo } from "./typo.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";
import type { BoardCard } from "./unlock.js";

const { el, byId, errMsg } = xyApp;

export interface DescChange { card: BoardCard; desc: string }

export interface Rewrites {
  collect(next: (c: BoardCard) => string | null): DescChange[];
  apply(changes: ReadonlyArray<DescChange>): Promise<void>;
  // The cards written under the old scheme (ADR-0005) become whole bodies the
  // first time their board is opened after that release. Idempotent.
  convertLegacyVersions(): Promise<void>;
  // The two ☰ entries, in the order the menu lists them.
  typograph: BoardPanel;
}

export function createRewrites(board: Board): Rewrites {
  // Only question cards. A test card's description is JSON (a legacy session),
  // and a pass meant for 4s turns its quotes into «ёлочки» and its metadata into
  // something parseSession cannot read back.
  function collect(next: (c: BoardCard) => string | null): DescChange[] {
    const out: DescChange[] = [];
    for (const c of board.state.cards) {
      if (c.kind !== "question") continue;
      const desc = next(c);
      if (desc !== null && desc !== c.desc) out.push({ card: c, desc });
    }
    return out;
  }

  async function apply(changes: ReadonlyArray<DescChange>): Promise<void> {
    const key = board.dk();
    for (const ch of changes) {
      await board.verbs.patch("patchCard", `/api/cards/${ch.card.id}`, {
        description_enc: await xyCrypto.encField(key, ch.desc),
        desc_event_enc: await xyCrypto.encField(key, JSON.stringify({ before: ch.card.desc, after: ch.desc })),
      });
      ch.card.desc = ch.desc;
    }
    board.render();
  }

  // typograph runs the typography pass over every card on the board, every
  // version of it. It runs in the browser, so a whole package's question text is
  // never posted anywhere and this works offline like any other board edit.
  // Stress marks are the one part of the pass that guesses. chgk writes stress by
  // capitalising the vowel («брАзер»), and a camel-cased compound («ГазпромИнвест»)
  // is exactly the same shape, so a board-wide press asks first — one tick per
  // distinct word, however many cards it appears in. Everything else the pass does
  // (quotes, dashes, spaces, percent-escapes) is not a guess and is not asked about.
  async function typograph(): Promise<void> {
    const picks = xyTypo.accentPicks(board.state.cards.map((c) => c.desc));
    if (!picks.length) { await runTypograph(null); return; }
    openAccentReview(picks, (allow) => { void runTypograph(allow); });
  }

  async function runTypograph(allow: Set<string> | null): Promise<void> {
    const opts = allow ? { allow } : {};
    const changes = collect((c) => xyTypo.passVersions(c.desc, opts));
    const total = board.state.cards.length;
    if (!changes.length) { alert("Нечего типографить — вся доска уже в порядке."); return; }
    // «N из M», because the rest were already right: the pass only rewrites a card
    // whose text it actually changes, and a bare count reads like it skipped some.
    if (!allow && !confirm(`Типографить ${changes.length} из ${total}? В остальных карточках менять нечего.`)) return;
    board.setStatus("saving");
    try {
      await apply(changes);
      board.setStatus("saved");
      alert(`Оттипографлено карточек: ${changes.length} из ${total}.`);
    } catch (err) {
      board.setStatus("error");
      alert("Ошибка при типографике: " + errMsg(err));
    }
  }

  // ---- the stress-mark review ----
  const accentModal = modal("accent");
  let accentApply: ((allow: Set<string>) => void) | null = null;

  function openAccentReview(picks: ReadonlyArray<{ from: string; to: string }>, applyPicks: (allow: Set<string>) => void): void {
    const box = byId("accentPicks");
    box.replaceChildren(...picks.map((p) => {
      const cb = el("input", { type: "checkbox", checked: "checked" }) as HTMLInputElement;
      cb.dataset.word = p.from;
      return el("label", { class: "accent-pick" }, cb,
        el("span", { class: "accent-from", text: p.from }),
        el("span", { class: "accent-arrow", text: "→" }),
        el("span", { class: "accent-to", text: p.to }));
    }));
    accentApply = applyPicks;
    accentModal.open({ onClose: () => { accentApply = null; } });
  }

  byId("accentRun").addEventListener("click", () => {
    const allow = new Set<string>();
    for (const cb of byId("accentPicks").querySelectorAll<HTMLInputElement>("input:checked")) {
      if (cb.dataset.word) allow.add(cb.dataset.word);
    }
    const applyPicks = accentApply;
    accentModal.close();
    applyPicks?.(allow);
  });

  async function convertLegacyVersions(): Promise<void> {
    if (!xySync.isOnline()) return;
    const changes = collect((c) => (c.kind === "question" ? xyVersions.convertLegacyVersions(c.desc) : null));
    if (!changes.length) return;
    try {
      await apply(changes);
    } catch (err) {
      // Nothing is lost by failing: the cards keep their old spelling and the next
      // load tries again.
      console.error("не удалось перевести версии карточек", err);
    }
  }

  return {
    collect,
    apply,
    convertLegacyVersions,
    typograph: {
      // wand-sparkles twice over: the vendored lucide set has no «type» glyph, and
      // both items are the same kind of act — rewrite the text of every card at once.
      id: "typograph", menu: "board", icon: "wand-sparkles",
      label: "Типографить всю доску",
      title: "Кавычки-ёлочки, тире, неразрывные пробелы и раскодированные ссылки — во всех карточках и всех версиях",
      open: () => { void typograph(); },
    },
  };
}
