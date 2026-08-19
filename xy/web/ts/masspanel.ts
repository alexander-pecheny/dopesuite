// masspanel.ts — «Массовое действие»: ticking cards across the whole board, then
// doing one thing to all of them. The rules (what a select-all covers, board
// order, how a partly-failed run reads) live in massaction.ts; this is the mode,
// the bar, the checkboxes' state, the one dialog with its pickers, and the writes.

import { xyApp } from "./app.js";
import { xySync } from "./sync.js";
import { type MassAction, xyMass } from "./massaction.js";
import { boardOrder } from "./dragrank.js";
import { modal } from "./modal.js";
import type { MoveCtx } from "./carddetail.js";
import type { Transfer } from "./transfer.js";
import type { Board, BoardPanel } from "./panels.js";
import type { BoardCard } from "./unlock.js";

const { jput, el, byId } = xyApp;

export interface MassPanelDeps {
  kanban: HTMLElement;
  transfer: Pick<Transfer, "moveBoardOptions" | "loadMoveBoard" | "transferCard">;
  forgetCardLabels(cards: BoardCard[]): void;
  paintLabels(): void;
}

export interface MassPanel {
  readonly mode: boolean;
  readonly selected: ReadonlySet<number>;
  setMode(on: boolean): void;
  toggle(cardId: number): void;
  toggleAll(ids: number[]): void;
  // The render loop's two hooks: the bar (which is also what hides it) and a
  // repaint of the checkboxes without rebuilding the board.
  renderBar(): void;
  paintChecks(): void;
  // Drop ticks on cards that no longer exist.
  prune(): void;
  panel: BoardPanel;
}

export function createMassPanel(board: Board, deps: MassPanelDeps): MassPanel {
  let massMode = false;
  let massSelected: Set<number> = new Set();
  let massAction: MassAction | null = null;

  function massCards(): BoardCard[] {
    return xyMass.ordered(massSelected, boardCardsInOrder());
  }

  // A bulk move must land its cards in board order, not in whatever order they
  // were ticked.
  const boardCardsInOrder = (): BoardCard[] => boardOrder(board.state.lists, board.state.cards);

  function setMassMode(on: boolean): void {
    massMode = on;
    if (!on) massSelected = new Set();
    document.body.classList.toggle("mass-mode", on);
    board.render();
  }

  function massToggle(id: number): void {
    massSelected = xyMass.toggleOne(massSelected, id);
    renderMassBar();
    paintMassChecks();
  }

  // paintMassChecks syncs every checkbox to the selection without rebuilding the
  // board — a full render on each tick would lose the scroll position and make
  // ticking a run of cards feel like it was fighting back.
  function paintMassChecks(): void {
    for (const box of deps.kanban.querySelectorAll<HTMLInputElement>(".kcard-check input")) {
      box.checked = massSelected.has(Number(box.dataset.cardId));
    }
    for (const box of deps.kanban.querySelectorAll<HTMLInputElement>(".klist-check input")) {
      const ids = board.cardsOf(Number(box.dataset.listId)).map((c) => c.id);
      box.checked = xyMass.allSelected(massSelected, ids);
    }
  }

  function renderMassBar(): void {
    const bar = byId("massBar");
    bar.hidden = !massMode;
    if (!massMode) return;
    const n = massSelected.size;
    const actions = n
      ? xyMass.MASS_ACTIONS.map((a) => {
          const b = el("button", { class: "input mass-act" + (a.danger ? " mass-act-danger" : ""), type: "button", title: a.title, text: a.label });
          b.addEventListener("click", () => { void openMass(a); });
          return b;
        })
      : [el("span", { class: "mass-hint", text: "Отметьте карточки" })];
    const done = el("button", { class: "input", type: "button", text: "Готово" });
    done.addEventListener("click", () => setMassMode(false));
    bar.replaceChildren(
      el("span", { class: "mass-count", text: n ? `Выбрано: ${xyMass.cardCount(n)}` : "Массовое действие" }),
      el("div", { class: "mass-acts" }, ...actions),
      done,
    );
  }

  // ---- the one dialog ----
  const massModal = modal("mass");
  let massTarget: { listId: number; ctx: MoveCtx } | null = null;
  let massPick: number | null = null;

  function hideMass(): void { massAction = null; massTarget = null; massPick = null; }

  async function openMass(action: MassAction): Promise<void> {
    massAction = action;
    massPick = null;
    massTarget = null;
    const n = massSelected.size;
    massModal.el.querySelector<HTMLElement>(".appearance-modal-title")!.textContent = `${action.label}: ${xyMass.cardCount(n)}`;
    const run = byId<HTMLButtonElement>("massRun");
    run.textContent = `${action.verb} (${n})`;
    run.disabled = action.needs !== "none";
    run.classList.toggle("btn-danger", !!action.danger);
    const body = byId("massBody");
    body.replaceChildren();
    if (action.needs === "label") buildMassLabelPick(body, run);
    else if (action.needs === "session") buildMassSessionPick(body, run);
    else if (action.needs === "target") await buildMassTargetPick(body, run);
    else body.append(el("p", { class: "label-empty", text: "Карточки будут удалены. Их можно восстановить в течение 14 дней." }));
    massModal.open({ onClose: hideMass });
  }

  // The label picker is the board's own label list, same chips as the card's —
  // reusing the vocabulary rather than inventing a bulk-only one.
  function buildMassLabelPick(body: HTMLElement, run: HTMLButtonElement): void {
    if (!board.state.labels.length) { body.append(el("p", { class: "label-empty", text: "На доске нет меток." })); return; }
    const row = el("div", { class: "label-picker" });
    for (const l of [...board.state.labels].sort((a, b) => a.name.localeCompare(b.name))) {
      const chip = el("button", { class: "label-pick", type: "button", dataset: { c: l.color }, text: l.name });
      chip.addEventListener("click", () => {
        massPick = l.id;
        for (const other of row.querySelectorAll(".label-pick")) other.classList.remove("active");
        chip.classList.add("active");
        run.disabled = false;
      });
      row.append(chip);
    }
    body.append(row);
    deps.paintLabels();
  }

  function buildMassSessionPick(body: HTMLElement, run: HTMLButtonElement): void {
    if (!board.state.sessions.length) { body.append(el("p", { class: "label-empty", text: "На доске нет тестов." })); return; }
    const sel = el("select", { class: "input" }) as HTMLSelectElement;
    sel.append(el("option", { value: "", text: "— выберите тест —" }));
    for (const s of board.state.sessions) sel.append(el("option", { value: String(s.id), text: board.sessionName(s.id) }));
    sel.addEventListener("change", () => { massPick = Number(sel.value) || null; run.disabled = !massPick; });
    body.append(sel);
  }

  // Move/copy reuses the card's own destination machinery (loadMoveBoard →
  // MoveCtx), so a bulk move offers exactly the boards, lists and positions a
  // single card's does.
  async function buildMassTargetPick(body: HTMLElement, run: HTMLButtonElement): Promise<void> {
    const boardSel = el("select", { class: "input" }) as HTMLSelectElement;
    const listSel = el("select", { class: "input" }) as HTMLSelectElement;
    body.append(el("label", { class: "section-label", text: "Доска" }), boardSel,
                el("label", { class: "section-label", text: "Список" }), listSel);
    const boards = await deps.transfer.moveBoardOptions();
    for (const b of boards) boardSel.append(el("option", { value: String(b.id), text: b.label }));
    boardSel.value = String(board.id);
    const fillLists = async (): Promise<void> => {
      listSel.replaceChildren();
      run.disabled = true;
      massTarget = null;
      const ctx = await deps.transfer.loadMoveBoard(Number(boardSel.value));
      if (!ctx) { listSel.append(el("option", { value: "", text: "— пароль доски неизвестен —" })); return; }
      for (const l of ctx.lists) listSel.append(el("option", { value: String(l.id), text: l.title || "(без названия)" }));
      const pick = (): void => {
        const listId = Number(listSel.value);
        massTarget = listId ? { listId, ctx } : null;
        run.disabled = !massTarget;
      };
      listSel.addEventListener("change", pick);
      pick();
    };
    boardSel.addEventListener("change", () => { void fillLists(); });
    await fillLists();
  }

  // runMass performs the action card by card, reporting as it goes. It is not a
  // transaction on purpose: one card failing (a lost connection, a card someone
  // else deleted) must not undo the ones that worked. Failures stay selected, so
  // «try again» means clicking the same button.
  async function runMass(): Promise<void> {
    const action = massAction;
    if (!action) return;
    const cards = massCards();
    const msg = byId("massMessage");
    const run = byId<HTMLButtonElement>("massRun");
    // Copying, and anything touching another board, re-encrypts and carries
    // attachments — online-only, like the single-card path. Saying so once beats
    // letting every card fail with the same message.
    const online = action.key === "copy" || (action.key === "move" && massTarget && massTarget.ctx.boardId !== board.id);
    if (online && !xySync.requireOnline("Копирование и перенос между досками доступны только онлайн.", msg)) return;
    run.disabled = true;
    const failed = new Set<number>();
    let ok = 0;
    for (const [i, card] of cards.entries()) {
      msg.textContent = `${i + 1} из ${cards.length}…`;
      try {
        await applyMass(action, card);
        ok++;
      } catch (_) {
        failed.add(card.id);
      }
    }
    massSelected = failed;
    board.render();
    msg.textContent = xyMass.runSummary(ok, failed.size);
    run.disabled = false;
    if (!failed.size) setTimeout(massModal.close, 900);
  }

  async function applyMass(action: MassAction, card: BoardCard): Promise<void> {
    switch (action.key) {
      case "delete":
        await board.verbs.del("deleteCard", `/api/cards/${card.id}`);
        board.state.cards = board.state.cards.filter((c) => c.id !== card.id);
        deps.forgetCardLabels([card]);
        return;
      case "label-add":
      case "label-del": {
        if (massPick == null) throw new Error("не выбрана метка");
        const own = board.state.cardLabels.filter((a) => a.cardId === card.id);
        const keep = action.key === "label-del"
          ? own.filter((a) => !(a.labelId === massPick && a.sessionId == null))
          : own.some((a) => a.labelId === massPick && a.sessionId == null) ? own : [...own, { cardId: card.id, labelId: massPick, sessionId: null }];
        await jput(`/api/cards/${card.id}/labels`, { labels: keep.map((a) => ({ label_id: a.labelId, session_id: a.sessionId })) });
        board.state.cardLabels = board.state.cardLabels.filter((a) => a.cardId !== card.id).concat(keep);
        return;
      }
      case "session-add":
      case "session-del": {
        if (massPick == null) throw new Error("не выбран тест");
        const plays = board.playingsOf(card.id);
        const next = action.key === "session-del"
          ? plays.filter((id) => id !== massPick)
          : plays.includes(massPick) ? plays : [...plays, massPick];
        await jput(`/api/cards/${card.id}/sessions`, { session_ids: next });
        board.state.cardSessions = board.state.cardSessions.filter((p) => p.cardId !== card.id)
          .concat(next.map((sessionId) => ({ cardId: card.id, sessionId })));
        // A playing that is gone takes its scoped labels with it (ADR-0004).
        if (action.key === "session-del") {
          board.state.cardLabels = board.state.cardLabels.filter((a) => !(a.cardId === card.id && a.sessionId === massPick));
        }
        return;
      }
      case "move":
      case "copy": {
        if (!massTarget) throw new Error("не выбран список");
        await deps.transfer.transferCard(card, massTarget.listId, massTarget.ctx, action.key === "move");
        return;
      }
    }
  }

  byId("massRun").addEventListener("click", () => { void runMass(); });


  return {
    get mode() { return massMode; },
    get selected() { return massSelected; },
    setMode: setMassMode,
    toggle: massToggle,
    toggleAll: (ids) => { massSelected = xyMass.toggleAll(massSelected, ids); renderMassBar(); paintMassChecks(); },
    renderBar: renderMassBar,
    paintChecks: paintMassChecks,
    prune: () => { if (massMode) massSelected = xyMass.prune(massSelected, board.state.cards); },
    panel: {
      id: "mass", menu: "board", icon: "list-checks",
      label: "Массовое действие",
      title: "Отметить карточки на всей доске и сделать с ними одно действие",
      open: () => setMassMode(!massMode),
    },
  };
}
