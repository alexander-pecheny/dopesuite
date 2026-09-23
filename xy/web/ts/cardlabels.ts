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
import { type Tester, testerNames, testersFromList } from "./sessions.js";
import { type CardSeen, nameOf, parseCardSeen, type SeenPerson, seenPeople, sessionRef, withoutSeen, withSeen } from "./seen.js";
import { autocomplete } from "./kit/suggest.js";
import * as people from "./people.js";
import { icon } from "./icons_gen.js";
import S from "./i18nstrings.js";
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
  seenAddBtn: HTMLElement;
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
  // The people the card's tour names in its Tester List — the seen section shows the extras.
  tourPicked(list: BoardList): Set<string>;
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
  //
  // Each name can be taken off, and a person can be added by hand (#90). Both
  // are corrections to the Playings, stored on the card (seen.ts). A tester who
  // missed the question stays listed, struck out, so the absence can be undone.
  let seenAllFor: number | null = null;

  function ordered(people: SeenPerson[]): SeenPerson[] {
    const { players, teams } = testerNames(people.map((p) => p.tester));
    const byName = new Map(people.map((p) => [nameOf(p.tester), p]));
    return [...players, ...teams].map((n) => byName.get(n)).filter((p): p is SeenPerson => p != null);
  }

  function renderSeen(card: BoardCard): void {
    const node = ui.seen;
    const people = seenPeople(board.seenPlayings(card.id), parseCardSeen(card.seen));
    // People who saw it at a test come first, then the ones added by hand, then
    // the absent ones. A test is the stronger record, and it is what the tour's
    // Tester List is mostly made of.
    const everyone = [
      ...ordered(people.filter((p) => !p.absent && p.at.length)),
      ...ordered(people.filter((p) => !p.absent && !p.at.length)),
    ];
    const missed = ordered(people.filter((p) => p.absent));
    // Which test each person is listed for: where they saw it, or, for an
    // absent one, the test whose question they missed.
    const seen = parseCardSeen(card.seen);
    const testName = new Map(board.playingsOf(card.id).map((sid) => [sessionRef(sid, board.sessionMeta(sid)), board.sessionName(sid)]));
    const testsOf = (p: SeenPerson): string => {
      const refs = p.absent
        ? Object.keys(seen.absent).filter((ref) => testName.has(ref) && seen.absent[ref].includes(nameOf(p.tester)))
        : p.at;
      return refs.map((ref) => testName.get(ref)).filter(Boolean).join(", ");
    };
    const adding = addingFor === card.id;
    if (!everyone.length && !missed.length && !adding) { node.hidden = true; return; }

    const list = board.state.lists.find((l) => l.id === card.listId);
    const common = list ? deps.tourPicked(list) : new Set<string>();
    const isCommon = (p: SeenPerson): boolean => common.has(nameOf(p.tester));
    const hiding = everyone.some(isCommon) && seenAllFor !== card.id;
    const shown = hiding ? everyone.filter((p) => !isCommon(p)) : everyone;
    const names = shown.map((p) => nameOf(p.tester));
    const label = hiding ? S.card.seen.labelExceptCommon() : S.card.seen.label();

    // The label and the two controls are a head row of their own, and the names
    // a column under it. Both come from one complaint (#72): everything used to
    // sit on a single wrapping line, so ticking the checkbox grew the list of
    // names and slid the checkbox across the card, and four people in a row read
    // as one long string. The head row is the same width whatever is below it.
    const controls: HTMLElement[] = [];
    // The copy button goes FIRST so that the checkbox, which is the thing being
    // clicked, is the last item in a right-aligned group and therefore does not
    // move when the click makes a copy button appear beside it.
    if (names.length) {
      controls.push(el("button", {
        class: "input", type: "button",
        title: S.card.seen.copyTitle(),
        // The copy is a line to paste into a chat, so it stays one line.
        onclick: () => { void deps.copyPlain(label + names.join(", ")); },
      }, icon("clipboard")));
    }
    if (everyone.some(isCommon)) {
      const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
      cb.checked = !hiding;
      cb.addEventListener("change", () => {
        seenAllFor = cb.checked ? card.id : null;
        renderSeen(card);
      });
      controls.push(el("label", { class: "checkbox" }, cb, el("span", { text: S.card.seen.showAll() })));
    }
    const head = el("div", { class: "u-row u-gap-sm u-align-center u-justify-between u-wrap" },
      el("span", { class: "seen-label", text: label }),
      controls.length ? el("div", { class: "u-row u-gap-sm u-align-center" }, ...controls) : null);

    const row = (p: SeenPerson): HTMLElement => {
      const name = nameOf(p.tester);
      const cls = p.absent ? "seen-absent" : isCommon(p) ? "seen-common" : "";
      const title = p.absent ? S.card.seen.absentTitle() : isCommon(p) ? S.card.seen.commonTesterTitle() : "";
      const act = p.absent
        ? el("button", {
          class: "label-pick-x", type: "button", text: "↺",
          title: S.card.seen.restoreTitle(), "aria-label": S.card.seen.restoreAria(name),
          onclick: () => { void saveSeen(card, withSeen(parseCardSeen(card.seen), [p.tester], board.seenPlayings(card.id))); },
        })
        : el("button", {
          class: "label-pick-x", type: "button", text: "×",
          title: S.card.seen.removeTitle(), "aria-label": S.card.seen.removeAria(name),
          onclick: () => { void saveSeen(card, withoutSeen(parseCardSeen(card.seen), [name], board.seenPlayings(card.id))); },
        });
      return el("div", { class: "u-row u-gap-sm u-align-center" },
        el("span", { class: cls, title, text: name }),
        testsOf(p) ? el("span", { class: "seen-label", text: testsOf(p) }) : null,
        act);
    };
    const rows = [...shown, ...missed].map(row);
    node.hidden = false;
    node.replaceChildren(head,
      ...(rows.length ? [el("div", { class: "seen-names u-col u-gap-xs" }, ...rows)] : []),
      ...(adding ? [addField.box] : []));
  }

  async function saveSeen(card: BoardCard, next: CardSeen): Promise<void> {
    try {
      await board.writeSeen(card, next);
      renderSeen(card);
      board.render();
    } catch (err) { ui.message.textContent = errMsg(err); }
  }

  // The field that adds people by hand: a name from the Person Directory, a
  // name nobody has typed before, or a pasted list. It sits at the foot of the
  // Seen list rather than in a dropdown, so each name lands in plain view
  // above it, and it stays open, because the usual case is several people. One
  // node for the page, re-mounted by every render, so a save does not take the
  // focus away mid-list.
  let addingFor: number | null = null;
  const addField = (() => {
    const inp = el("input", {
      class: "input", type: "text",
      placeholder: S.card.seen.addPlaceholder(), autocomplete: "off",
      "aria-label": S.board.card.seenAdd(),
    }) as HTMLInputElement;
    const add = (testers: Tester[]): void => {
      const card = board.state.cards.find((c) => c.id === addingFor);
      if (!card || !testers.length) return;
      inp.value = "";
      void saveSeen(card, withSeen(parseCardSeen(card.seen), testers, board.seenPlayings(card.id))).then(() => inp.focus());
    };
    autocomplete(inp, (q) => q.trim()
      ? people.suggest(board.id, q).map((s) => ({ value: s.text, label: s.text, hint: s.board }))
      : [], (choice) => {
      const hit = people.suggest(board.id, choice.value).find((s) => s.text === choice.value);
      add([{ text: choice.value, type: hit ? hit.type : "player" }]);
    });
    inp.addEventListener("keydown", (e) => {
      const k = e as KeyboardEvent;
      if (k.defaultPrevented || k.isComposing) return;
      if (k.key === "Escape") {
        e.stopPropagation();
        closeSeenAdd();
        return;
      }
      if (k.key !== "Enter" || k.ctrlKey || k.metaKey) return;
      e.preventDefault();
      add(testersFromList(inp.value));
    });
    inp.addEventListener("paste", (e) => {
      const text = (e as ClipboardEvent).clipboardData?.getData("text/plain") || "";
      if (!text.includes("\n")) return;
      e.preventDefault();
      add(testersFromList(text));
    });
    const done = el("button", {
      class: "label-pick-x", type: "button", text: "×",
      title: S.card.seen.addClose(), "aria-label": S.card.seen.addClose(),
      onclick: () => closeSeenAdd(),
    });
    const box = el("div", { class: "u-col u-gap-xs" },
      el("div", { class: "u-row u-gap-sm u-align-center" }, inp, done),
      el("p", { class: "hint", text: S.card.seen.addHint() }));
    return { box, inp };
  })();

  function closeSeenAdd(): void {
    const card = board.state.cards.find((c) => c.id === addingFor);
    addingFor = null;
    if (card) renderSeen(card);
  }

  function openSeenAdd(): void {
    const card = board.state.cards.find((c) => c.id === deps.openCardId());
    if (!card) return;
    if (addingFor === card.id) { closeSeenAdd(); return; }
    addingFor = card.id;
    renderSeen(card);
    addField.inp.focus();
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

  // anchoredPopup is the dropdown shell every popup on this card shares:
  // mounted in its anchor, dismissed by Escape, an outside click, or a second
  // click on its trigger. It returns the close, or null when this click closed
  // an open one.
  function anchoredPopup(anchor: HTMLElement, kids: HTMLElement[]): (() => void) | null {
    const already = anchor.querySelector(".label-add-popup");
    closeLabelAddPopup();
    if (already) return null; // a second click on the trigger closes it
    const popup = el("div", { class: "menu-dropdown label-add-popup", role: "menu" }, ...kids);
    function close(): void {
      popup.remove();
      document.removeEventListener("pointerdown", onOutside, true);
      document.removeEventListener("keydown", onKey, true);
    }
    // A popup opened FROM this one (the colour palette, a name suggestion) is
    // body-mounted to escape our scroll clipping, so it is not inside `anchor` —
    // untreated, picking from it read as an outside click and took this popup
    // down with it.
    const above = (): Element | null => document.querySelector(".menu-fixed, .suggest-pop");
    function onOutside(e: PointerEvent): void {
      if (!(e.target instanceof Node) || anchor.contains(e.target)) return;
      if (e.target instanceof Element && e.target.closest(".menu-fixed, .suggest-pop")) return;
      close();
    }
    function onKey(e: KeyboardEvent): void {
      if (e.key !== "Escape" || above()) return;
      e.stopImmediatePropagation();
      close();
    }
    anchor.append(popup);
    document.addEventListener("pointerdown", onOutside, true);
    document.addEventListener("keydown", onKey, true);
    return close;
  }

  // filteredPopup is the label and test pickers: a filter field over a
  // scrollable list. A native <select> can host neither the filter
  // nor the swatches, hence the hand-rolled popup (it shares .menu-dropdown with
  // the list ⋯ menu).
  interface PopupItem { id: number; name: string; color?: string }

  function filteredPopup(opts: {
    anchor: HTMLElement;
    items: PopupItem[];
    placeholder: string;
    empty: string;
    extra?: HTMLElement;
    onPick(item: PopupItem): void;
  }): void {
    const filter = el("input", {
      class: "input label-add-filter", type: "text",
      placeholder: opts.placeholder, autocomplete: "off",
    }) as HTMLInputElement;
    const listBox = el("div", { class: "label-add-list" });
    const close = anchoredPopup(opts.anchor, opts.extra ? [filter, listBox, opts.extra] : [filter, listBox]);
    if (!close) return;

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
          onclick: () => { close!(); opts.onPick(item); },
        },
          item.color ? el("span", { class: "label-swatch", dataset: { c: item.color } }) : el("span"),
          el("span", { class: "label-add-name", text: item.name }),
        ));
      }
      deps.paintLabels();
    }

    filter.addEventListener("input", fill);
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
  ui.seenAddBtn.addEventListener("click", openSeenAdd);

  return { render: renderLabelPicker, closePopup: closeLabelAddPopup, ensurePlaying: addPlaying };
}
