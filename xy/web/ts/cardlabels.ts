// cardlabels.ts — the open card's labels, playings and seen sections (ADR-0004):
// a label is the author's view of the question, a Playing is where it was
// tested, a label scoped to a Playing is what the testers thought there, and
// the seen section names who saw this question beyond the people the tour already
// names. Two pickers over one filtered popup; every write goes up as the card's
// whole set through the board's verbs.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { sortLabels } from "./labelsedit.js";
import { colorField, LABEL_COLORS } from "./colorpick.js";
import { testerNames } from "./sessions.js";
import type { SessionMeta, Tester } from "./sessions.js";
import { icon } from "./icons_gen.js";
import S from "./i18nstrings_ru_gen.js";
import type { Board } from "./panels.js";
import type { BoardCard, BoardLabel, BoardList } from "./unlock.js";
import type { DataKey } from "./crypto.js";

const { el, errMsg } = xyApp;

export interface CardLabelsUI {
  picker: HTMLElement;
  playings: HTMLElement;
  seen: HTMLElement;
  addRow: HTMLElement;
  addBtn: HTMLElement;
  playingAddRow: HTMLElement;
  playingAddBtn: HTMLElement;
  // The "create a new label" form is authored in board.dopeui but does not
  // belong in the card body; it is detached at boot and mounted at the foot of
  // the add-label popup, where creating a label belongs.
  newLabelForm: HTMLFormElement;
  newLabelName: HTMLInputElement;
  newLabelColor: HTMLElement;
  message: HTMLElement;
}

export interface CardLabelsDeps {
  mustDK(): DataKey;
  openCardId(): number | null;
  copyPlain(text: string): Promise<void>;
  // The Sessions the card's tour names in its Tester List — the seen section shows the extras.
  tourPicked(list: BoardList): Set<number>;
  createLabel(name: string, color: string): Promise<BoardLabel>;
  loadTimeline(cardId: number): Promise<void>;
  paintLabels(): void;
  // A person took the active test OFF this card — test mode must not put it
  // back (ADR-0012). Fires only for the ×, never for a session delete.
  onPlayingRemoved?(cardId: number, sessionId: number): void;
}

export interface CardLabels {
  render(card: BoardCard): void;
  closePopup(): void;
  // Test mode's write: mark the card with the session, silently, if it is not
  // already. The same whole-set write the picker's + makes.
  ensurePlaying(card: BoardCard, sessionId: number): Promise<void>;
}

export function createCardLabels(board: Board, ui: CardLabelsUI, deps: CardLabelsDeps): CardLabels {
  const labelById = (id: number): BoardLabel | undefined => board.state.labels.find((l) => l.id === id);
  const newLabelForm = ui.newLabelForm;
  const newLabelColor = colorField(ui.newLabelColor, LABEL_COLORS[0]);
  newLabelForm.remove();

  // The card's labels and playings are two separate pickers (ADR-0004): a label is
  // the author's view of the question, a Playing is where it was tested, and a
  // label scoped to a Playing is what the testers thought there. Mixing them into
  // one list was what made the "took it" label multiply by the number of tests.


  function labelChip(lbl: BoardLabel, onRemove: () => void, title: string): HTMLElement {
    return el("span", { class: "label-pick is-on", dataset: { c: lbl.color }, title: lbl.name },
      el("span", { class: "label-pick-name", text: lbl.name }),
      el("button", {
        class: "label-pick-x", type: "button", text: "×",
        title, "aria-label": S.card.chip.removeAria(title, lbl.name),
        onclick: onRemove,
      }));
  }

  function renderLabelPicker(card: BoardCard): void {
    const picker = ui.picker;
    picker.replaceChildren();
    const own = board.assignmentsOf(card.id, null);
    for (const a of own) {
      const lbl = labelById(a.labelId);
      if (lbl) picker.append(labelChip(lbl, () => { void setLabel(card, lbl, null, false); }, S.card.labels.removeTitle()));
    }
    if (!own.length) picker.append(el("span", { class: "label-empty", text: S.card.labels.empty() }));

    renderPlayings(card);
    renderSeen(card);
    closeLabelAddPopup();
    deps.paintLabels();
  }

  function renderPlayings(card: BoardCard): void {
    const box = ui.playings;
    box.replaceChildren();
    const ids = board.playingsOf(card.id);
    if (!ids.length) {
      box.append(el("span", { class: "label-empty", text: S.card.playings.empty() }));
      return;
    }
    for (const sid of ids) {
      const head = el("div", { class: "playing-head" },
        el("span", { class: "playing-name", text: board.sessionName(sid) }),
        el("button", {
          class: "label-pick-x", type: "button", text: "×",
          title: S.card.playings.removeTitle(), "aria-label": S.card.playings.removeAria(board.sessionName(sid)),
          onclick: () => { void removePlaying(card, sid); },
        }));
      const chips = el("div", { class: "playing-labels" });
      for (const a of board.assignmentsOf(card.id, sid)) {
        const lbl = labelById(a.labelId);
        if (lbl) chips.append(labelChip(lbl, () => { void setLabel(card, lbl, sid, false); }, S.card.playings.labelRemoveTitle()));
      }
      chips.append(el("button", {
        class: "input playing-add", type: "button", text: "＋",
        title: S.card.playings.labelAddTitle(),
        onclick: (e: Event) => { openLabelAddPopup(sid, (e.currentTarget as HTMLElement).parentElement as HTMLElement); },
      }));
      box.append(el("div", { class: "playing" }, head, chips));
    }
  }

  // renderSeen writes who saw THIS question beyond the people the tour already
  // names. A tour's preamble lists whoever tested most of it, and those people
  // know not to play; the ones who matter here are the extras — a question moved
  // in from another tournament, seen by three people nobody has warned. Showing
  // the full list again would bury them — but on a question every common tester
  // saw, the subtraction leaves nothing and the card reads as untested. So
  // The show-all-testers checkbox dims them back in instead. A peek, not a preference:
  // keyed to the open card, so a label write mid-peek does not collapse the list
  // and the next card starts folded again.
  let seenAllFor: number | null = null;

  function seenNames(testers: ReadonlyArray<Tester>): string[] {
    const { players, teams } = testerNames(testers);
    return [...players, ...teams];
  }

  function renderSeen(card: BoardCard): void {
    const node = ui.seen;
    const mine = board.playingsOf(card.id).map((sid) => board.sessionMeta(sid)).filter((m): m is SessionMeta => m != null);
    const everyone = seenNames(mine.flatMap((m) => m.testers || []));
    if (!everyone.length) { node.hidden = true; return; }

    const list = board.state.lists.find((l) => l.id === card.listId);
    const common = new Set<string>();
    for (const sid of list ? deps.tourPicked(list) : []) {
      const m = board.sessionMeta(sid);
      for (const t of (m && m.testers) || []) common.add((t.text || "").trim());
    }
    const hiding = common.size > 0 && seenAllFor !== card.id;
    const names = hiding ? everyone.filter((n) => !common.has(n)) : everyone;
    const label = hiding ? S.card.seen.labelExceptCommon() : S.card.seen.label();

    const parts: Array<HTMLElement | string> = [];
    if (names.length) {
      const spans: Array<HTMLElement | string> = [];
      for (const n of names) {
        if (spans.length) spans.push(", ");
        spans.push(common.has(n) ? el("span", { class: "seen-common", title: S.card.seen.commonTesterTitle(), text: n }) : n);
      }
      parts.push(el("span", { class: "seen-label", text: label }), el("span", { class: "seen-names" }, ...spans));
    }
    if (common.size) {
      const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
      cb.checked = !hiding;
      cb.addEventListener("change", () => {
        seenAllFor = cb.checked ? card.id : null;
        renderSeen(card);
      });
      parts.push(el("label", { class: "checkbox seen-all" }, cb, el("span", { text: S.card.seen.showAll() })));
    }
    if (names.length) {
      parts.push(el("button", {
        class: "input seen-copy", type: "button",
        title: S.card.seen.copyTitle(),
        onclick: () => { void deps.copyPlain(label + names.join(", ")); },
      }, icon("clipboard")));
    }
    node.hidden = false;
    node.replaceChildren(...parts);
  }

  function closeLabelAddPopup(): void {
    for (const popup of document.querySelectorAll(".label-add-popup")) popup.remove();
  }

  // setLabel adds or removes ONE assignment. The card's whole set goes up together
  // because the endpoint replaces it — cheap, and it keeps the offline mirror's
  // view of a card in a single op.
  async function setLabel(card: BoardCard, lbl: BoardLabel, sessionId: number | null, adding: boolean): Promise<void> {
    const rest = board.state.cardLabels.filter((a) =>
      a.cardId !== card.id || a.labelId !== lbl.id || a.sessionId !== sessionId);
    const next = adding ? [...rest, { cardId: card.id, labelId: lbl.id, sessionId }] : rest;
    try {
      const events = [{
        type: adding ? "label_add" : "label_remove",
        payload_enc: await xyCrypto.encField(deps.mustDK(), JSON.stringify({ label: lbl.name, label_id: lbl.id })),
      }];
      await board.verbs.put("setCardLabels", `/api/cards/${card.id}/labels`, {
        labels: next.filter((a) => a.cardId === card.id).map((a) => ({ label_id: a.labelId, session_id: a.sessionId })),
        events,
      });
      board.state.cardLabels = next;
      renderLabelPicker(card);
      board.render();
      await deps.loadTimeline(card.id);
    } catch (err) { ui.message.textContent = errMsg(err); }
  }

  async function addPlaying(card: BoardCard, sessionId: number): Promise<void> {
    if (board.playingsOf(card.id).includes(sessionId)) return;
    await writePlayings(card, [...board.playingsOf(card.id), sessionId]);
  }

  // removePlaying takes the labels scoped to it — a label scoped to a playing that
  // no longer exists cannot be read (ADR-0004) — so the confirmation names how many.
  async function removePlaying(card: BoardCard, sessionId: number): Promise<void> {
    const scoped = board.assignmentsOf(card.id, sessionId).length;
    const what = scoped
      ? S.card.playings.removeConfirmScoped(board.sessionName(sessionId), scoped)
      : S.card.playings.removeConfirm(board.sessionName(sessionId));
    if (!confirm(what)) return;
    await writePlayings(card, board.playingsOf(card.id).filter((id) => id !== sessionId));
    deps.onPlayingRemoved?.(card.id, sessionId);
  }

  async function writePlayings(card: BoardCard, ids: number[]): Promise<void> {
    try {
      await board.verbs.put("setCardSessions", `/api/cards/${card.id}/sessions`, { session_ids: ids });
      board.state.cardSessions = board.state.cardSessions.filter((p) => p.cardId !== card.id)
        .concat(ids.map((sessionId) => ({ cardId: card.id, sessionId })));
      const keep = new Set(ids);
      board.state.cardLabels = board.state.cardLabels.filter((a) =>
        a.cardId !== card.id || a.sessionId == null || keep.has(a.sessionId));
      renderLabelPicker(card);
      board.render();
    } catch (err) { ui.message.textContent = errMsg(err); }
  }

  // filteredPopup is the one dropdown shape this card uses three ways: a filter
  // field over a scrollable list, dismissed by Escape, an outside click, or a
  // second click on its trigger. A native <select> can host neither the filter nor
  // the swatches, hence the hand-rolled popup (it shares .menu-dropdown with the
  // list ⋯ menu).
  interface PopupItem { id: number; name: string; color?: string }

  function filteredPopup(opts: {
    anchor: HTMLElement;
    items: PopupItem[];
    placeholder: string;
    empty: string;
    extra?: HTMLElement;
    onPick(item: PopupItem): void;
  }): void {
    const already = opts.anchor.querySelector(".label-add-popup");
    closeLabelAddPopup();
    if (already) return; // a second click on the trigger closes it

    const filter = el("input", {
      class: "input label-add-filter", type: "text",
      placeholder: opts.placeholder, autocomplete: "off",
    }) as HTMLInputElement;
    const listBox = el("div", { class: "label-add-list" });
    const kids = opts.extra ? [filter, listBox, opts.extra] : [filter, listBox];
    const popup = el("div", { class: "menu-dropdown label-add-popup", role: "menu" }, ...kids);

    function fill(): void {
      const q = filter.value.trim().toLowerCase();
      const shown = q ? opts.items.filter((i) => i.name.toLowerCase().includes(q)) : opts.items;
      listBox.replaceChildren();
      if (!shown.length) {
        listBox.append(el("span", { class: "label-empty", text: opts.items.length ? S.card.add.noMatch() : opts.empty }));
        return;
      }
      for (const item of shown) {
        listBox.append(el("button", {
          class: "menu-item label-add-item", type: "button", role: "menuitem",
          onclick: () => { close(); opts.onPick(item); },
        },
          item.color ? el("span", { class: "label-swatch", dataset: { c: item.color } }) : el("span"),
          el("span", { class: "label-add-name", text: item.name }),
        ));
      }
      deps.paintLabels();
    }
    function close(): void {
      popup.remove();
      document.removeEventListener("pointerdown", onOutside, true);
      document.removeEventListener("keydown", onKey, true);
    }
    // A popup opened FROM this one (the colour palette) is body-mounted to escape
    // our scroll clipping, so it is not inside `anchor` — untreated, picking a
    // colour read as an outside click and took this popup and its form down.
    const above = (): Element | null => document.querySelector(".menu-fixed");
    function onOutside(e: PointerEvent): void {
      if (!(e.target instanceof Node) || opts.anchor.contains(e.target)) return;
      if (e.target instanceof Element && e.target.closest(".menu-fixed")) return;
      close();
    }
    function onKey(e: KeyboardEvent): void {
      if (e.key !== "Escape" || above()) return;
      e.stopImmediatePropagation();
      close();
    }

    filter.addEventListener("input", fill);
    opts.anchor.append(popup);
    document.addEventListener("pointerdown", onOutside, true);
    document.addEventListener("keydown", onKey, true);
    fill();
    filter.focus();
  }

  // openLabelAddPopup offers the labels not yet assigned IN THIS SCOPE. sessionId
  // null means the author's own; set means one Playing's — so the same label can
  // be added to a card twice, once each way (ADR-0004).
  function openLabelAddPopup(sessionId: number | null, anchorEl?: HTMLElement): void {
    const card = board.state.cards.find((c) => c.id === deps.openCardId());
    if (!card) return;
    const taken = new Set(board.assignmentsOf(card.id, sessionId).map((a) => a.labelId));
    const pool = sortLabels(board.state.labels.filter((l) => !taken.has(l.id)), board.state.cardLabels);
    filteredPopup({
      anchor: anchorEl || ui.addRow,
      items: pool.map((l) => ({ id: l.id, name: l.name, color: l.color })),
      placeholder: S.card.add.labelsPlaceholder(),
      empty: board.state.labels.length ? S.card.add.labelsAllAdded() : S.card.add.labelsNone(),
      // Creating a label from inside a test would still make a plain board label,
      // so the form belongs only to the author's own section.
      extra: sessionId == null ? newLabelForm : undefined,
      onPick: (item) => {
        const lbl = labelById(item.id);
        if (lbl) void setLabel(card, lbl, sessionId, true);
      },
    });
  }

  // openPlayingAddPopup offers the board's tests this question is not yet marked
  // with — the second of the card's two pickers.
  function openPlayingAddPopup(): void {
    const card = board.state.cards.find((c) => c.id === deps.openCardId());
    if (!card) return;
    const on = new Set(board.playingsOf(card.id));
    const pool = board.state.sessions.filter((s) => !on.has(s.id))
      .map((s) => ({ id: s.id, name: board.sessionName(s.id), date: (board.sessionMeta(s.id) || { date: "" }).date }))
      .sort((a, b) => (b.date || "").localeCompare(a.date || "") || b.id - a.id);
    filteredPopup({
      anchor: ui.playingAddRow,
      items: pool.map((s) => ({ id: s.id, name: s.name })),
      placeholder: S.card.add.playingsPlaceholder(),
      empty: board.state.sessions.length ? S.card.add.playingsAllMarked() : S.card.add.playingsNone(),
      onPick: (item) => { void addPlaying(card, item.id); },
    });
  }

  // NB: `newLabelForm` (the retained node), not getElementById — the form is
  // detached from the document above and lives inside the popup while it is open.
  newLabelForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = ui.newLabelName.value.trim();
    if (!name) return;
    try {
      const lbl = await deps.createLabel(name, newLabelColor.value());
      ui.newLabelName.value = "";
      const card = board.state.cards.find((c) => c.id === deps.openCardId());
      // The form is reachable only from inside the add-label popup, so naming a
      // label there means you want it ON this card — assign it instead of making
      // the user reopen the popup to pick what they just typed.
      if (card) await setLabel(card, lbl, null, true);
    } catch (err) { ui.message.textContent = errMsg(err); }
  });

  ui.addBtn.addEventListener("click", () => openLabelAddPopup(null));
  ui.playingAddBtn.addEventListener("click", openPlayingAddPopup);

  return { render: renderLabelPicker, closePopup: closeLabelAddPopup, ensurePlaying: addPlaying };
}
