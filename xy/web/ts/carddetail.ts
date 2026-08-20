// carddetail.ts — the card-detail modal, lifted out of board.js into a typed
// create(deps) factory: card creation (addCard), the Просмотр/Поля/
// Текст views over a shared draft (carddraft.js), the field builders with their
// hand-drawn suggest dropdowns, the edit-tools row (ударение / типограф / →.4s),
// direct links + deep links, open/close/back, read tracking, the move/copy
// dialog over transfer.ts, copy-question-for-test, and the alias's own save
// path. The board injects what it owns (live state, DK,
// mutation verbs, render, the preview/attachments/read-marker seams and
// questionNumberFor, which lives with the board's group-numbering logic); the
// timeline module is injected as `timeline` — the orchestrator creates the
// timeline first and wires its `card` seam back to this factory's API.
import { overlayStack } from "./overlaystack.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyVersions } from "./versions.js";
import { xyTypo } from "./typo.js";
import { parseSession, serializeSession } from "./sessions.js";
import { normalizeAlias, xyCardDraft } from "./carddraft.js";
import { xyRank } from "./rank.js";
import { byRank, rankForSlot } from "./dragrank.js";
import type { BoardKeymeta, DataKey } from "./crypto.js";
import type { CardFields, CopyTarget, Handout } from "./chgk.js";
import type { BoardCard, BoardLabel, BoardList, BoardSession, CardLabel, Playing, Snapshot, UnreadFlags } from "./unlock.js";
import type { OpBody } from "./store.js";
import type { CardEvent } from "./timeline.js";
import type { Transfer } from "./transfer.js";
import { icon, iconed } from "./icons_gen.js";

const { fetchJSON, jpost, jput, jdelete, el, onCmdEnter } = xyApp;
const { keyBetween } = xyRank;

// ---- pure helpers (exported for tests and for the board) ----

// nowStamp is the local stand-in for cards.created_at on a card this session
// just made: it is not in a snapshot yet, and offline it may not reach the
// server for a while. The next snapshot replaces it with the server's value.
export const nowStamp = (): string => new Date().toISOString();

// The 4s skeleton a new question's Текст view opens on: question / answer /
// comment / source / author, the blocks a question is expected to carry. The
// "@" line pre-fills the user's default author (a /profile setting) — saving
// an otherwise untouched stub is allowed and creates a card with just that.
export function questionStub(defaultAuthor: string): string {
  return "? \n! \n/ \n^ \n@ " + (defaultAuthor || "");
}

// ---- injected seams ----

// The slice of the board's live state this module reads and mutates in place.
export interface CardDetailState {
  name: string;
  lists: BoardList[];
  cards: BoardCard[];
  labels: BoardLabel[];
  cardLabels: CardLabel[];
  cardSessions: Playing[];
  sessions: BoardSession[];
  unread: Record<string, UnreadFlags>;
  defaultAuthor: string;
}

// The board's mutation wrappers (board.ts) — every board mutation flows
// through the sync engine. `create` mints an id; the rest return { id: null }.
export interface MutationVerbs {
  create(kind: string, path: string, body: OpBody): Promise<{ id: number | null }>;
  patch(kind: string, path: string, body: OpBody): Promise<unknown>;
  put(kind: string, path: string, body: OpBody): Promise<unknown>;
  del(kind: string, path: string): Promise<unknown>;
}

// The card shape the preview seam consumes: renderCardPreview builds a
// transient one from the draft, so it is looser than a persisted BoardCard.
export interface PreviewCardLike {
  id: number;
  kind: string;
  desc: string;
  listId: number;
}

// The board's docx-style preview machinery (renderPreviewCard and friends stay
// with the list preview in board.ts; the card preview reuses them).
export interface PreviewSeam {
  renderPreviewCard(card: PreviewCardLike, number: string | null, imgMap: Map<string, string>, screen: boolean): HTMLElement;
  resolveImages(cards: PreviewCardLike[], wanted: Set<string>, onImage: (name: string, url: string) => void): Promise<Map<string, string>>;
  imageRefs(cards: PreviewCardLike[]): Set<string>;
  fillPreviewImages(root: ParentNode, imgMap: Map<string, string>): void;
  previewList(list: BoardList, wholeGroup?: boolean): Promise<void>;
}

// The board's attachments seam: load refreshes the open card's attachment list
// (and the timeline's excerpt attachments); imageNames are the open card's
// image attachment filenames (the handout picker's choices), cleared on create
// mode where there is no card to load them from.
export interface AttachmentsSeam {
  load(cardId: number): Promise<void>;
  imageNames(): string[];
  clearImageNames(): void;
  upload(file: File, lossless: boolean, name: string): Promise<number | null>;
  resolveImages(cards: ReadonlyArray<{ id: number }>, wanted: Set<string>): Promise<Map<string, string>>;
  imageBlob(cardId: number, name: string): Promise<Blob | null>;
}

// The board-owned halves of read tracking: the kanban card dot and the 🔔 badge.
export interface ReadMarkerSeam {
  refreshCardUnreadDot(cardId: number): void;
  renderNotifBadge(): void;
}

// The timeline module's card-detail-facing surface (see timeline.ts).
export interface TimelineSeam {
  load(cardId: number): Promise<void>;
  events(): CardEvent[];
  resetFilter(): void;
  readBuckets(): { content: boolean; comments: boolean };
  ensureVisible(type: string): Promise<void>;
  commentDraft(): string;
  postComment(): Promise<boolean>;
  clearCommentDraft(): void;
}

// The nodes the card detail works on, resolved once by the page (board.ts).
// Names follow the ids, without the card prefix.
export interface CardDetailUI {
  addVersion: HTMLElement;
  alias: HTMLInputElement;
  close: HTMLButtonElement;
  commentsUnreadDot: HTMLElement;
  contentUnreadDot: HTMLElement;
  copy: HTMLElement;
  copyBtn: HTMLElement;
  copyMsg: HTMLElement;
  del: HTMLElement;
  desc: HTMLTextAreaElement;
  descLabel: HTMLElement;
  editTools: HTMLElement;
  fields: HTMLElement;
  insStress: HTMLElement;
  kind: HTMLSelectElement;
  link: HTMLElement;
  message: HTMLElement;
  overlay: HTMLElement;
  previewBody: HTMLElement;
  previewScreen: HTMLInputElement;
  save: HTMLButtonElement;
  timeline: HTMLElement;
  title: HTMLElement;
  to4s: HTMLElement; // #cardTo4s
  tabs: { preview: HTMLButtonElement; fields: HTMLButtonElement; text: HTMLButtonElement };
  typo: HTMLElement;
  versions: HTMLElement;
  viewFields: HTMLElement;
  viewPreview: HTMLElement;
  viewTabs: HTMLElement;
  viewText: HTMLElement;
  dirty: {
    cancel: HTMLElement;
    discard: HTMLElement;
    message: HTMLElement;
    overlay: HTMLElement;
    save: HTMLElement;
  };
  move: {
    board: HTMLSelectElement;
    btn: HTMLElement;
    list: HTMLSelectElement;
    pos: HTMLSelectElement;
  };
  listPreview: {
    body: HTMLElement;
    overlay: HTMLElement;
  };
}

export interface CardDetailDeps {
  boardId: number;
  ui: CardDetailUI;
  getState(): CardDetailState;
  getDK(): DataKey | null;
  verbs: MutationVerbs;
  setStatus(op: "saving" | "saved" | "error"): void;
  render(): void;
  cardsOf(listId: number): BoardCard[];
  labelById(id: number): BoardLabel | undefined;
  renderLabelPicker(card: BoardCard): void;
  paintLabels(): void;
  questionNumberFor(card: PreviewCardLike): string | null;
  // The board's shared transient popup (board.ts popupMenu) — the copy
  // button opens one when a card has more than one thing worth copying.
  popupMenu(anchor: HTMLElement, items: Array<{ label: string; onClick: () => void }>): void;
  forgetCardLabels(cards: BoardCard[]): void;
  preview: PreviewSeam;
  attachments: AttachmentsSeam;
  readMarkers: ReadMarkerSeam;
  timeline: TimelineSeam;
  // Moving or copying the open card (transfer.ts); the board wires one for
  // every panel that transfers.
  transfer: Pick<Transfer, "moveBoardOptions" | "loadMoveBoard" | "transferCard">;
  // Fires whenever the open card changes — a card id on open, null on close.
  // The test-mode dwell watcher (testmode.ts) is the one listener.
  onOpenCard?(cardId: number | null): void;
}

// cardReturn's shape: where the open card was launched from (a list preview's
// ✏️ button), so ↩️ can restore that preview scrolled to the same question.
export interface CardReturn {
  listId: number;
  cardId: number;
  group?: boolean;
}

// moveCtx: the currently-selected destination board for move/copy — its DK,
// lists (with titles) and cards-per-list (for computing the insertion rank).
export interface MoveLabel { id: number; name: string; color: string }
// A target board's sessions, decrypted, so a transferred test label can find the
// sitting it belongs to instead of matching on a name that may render differently.
export interface MoveSession { id: number; meta: string }
export interface MoveCtx {
  boardId: number;
  dk: DataKey;
  lists: Array<{ id: number; title: string; rank: string }>;
  cardsByList: Map<number, Array<{ id: number; rank: string }>>;
  labels: MoveLabel[];
  sessions: MoveSession[];
  name: string;
}

export interface CardDetail {
  addCard(list: BoardList): Promise<void>;
  openCard(card: BoardCard, opts?: { returnTo?: CardReturn | null }): Promise<void>;
  closeCard(): void;
  openCardId(): number | null;
  maybeOpenDeepLink(): void;
  highlightComment(eventId: number): Promise<void>;
  copyCommentLink(eventId: number): Promise<void>;
  // The clipboard write, with its insecure-context fallback — the Тесты panel
  // copies invite and tester lines through the same path.
  copyPlain(text: string): Promise<void>;
}

interface FieldReader<T> { node: HTMLElement; read(): T }
interface FieldReaders {
  handout: FieldReader<Handout | null>;
  question: FieldReader<string | null>;
  answer: FieldReader<string | null>;
  zachet: FieldReader<string | null>;
  nezachet: FieldReader<string | null>;
  comment: FieldReader<string | null>;
  sources: FieldReader<string[] | null>;
  authors: FieldReader<AuthorsValue>;
  hndt: FieldReader<string | null>;
}

// The Автор field reads back as two things: the names and the caption chosen for
// them. `names: null` is the field being absent altogether.
interface AuthorsValue { names: string[] | null; label: string | null }

export function createCardDetail(deps: CardDetailDeps): CardDetail {
  const ui = deps.ui;
  const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

  const { boardId, verbs, setStatus, transfer } = deps;
  const state = deps.getState;
  function mustDK(): DataKey {
    const dk = deps.getDK();
    if (!dk) throw new Error("нет ключа доски");
    return dk;
  }

  const cardOverlay = ui.overlay;
  const cardDescEl = ui.desc;
  const cardAliasEl = ui.alias;
  const cardKindEl = ui.kind;
  const cardMessageEl = ui.message;
  const cardFieldsEl = ui.fields;
  const cardSaveBtn = ui.save;
  const cardCloseBtn = ui.close;
  const moveBoardSel = ui.move.board;
  const moveListSel = ui.move.list;
  const movePosSel = ui.move.pos;
  const previewOverlay = ui.listPreview.overlay;

  // ---- card detail state ----
  let openCardId: number | null = null;
  // Set while the open card is one just created and still untouched. The card is
  // persisted from the start; this only drives the blank-form affordances — the
  // 4s stub, the two fields opened ready to type into, and hiding Просмотр,
  // which has nothing to show yet.
  let freshCard = false;
  // Which version the card editor is scoped to. A version is a whole 4s body, so
  // all three views — Просмотр, Поля and Текст — show this one and no other.
  // Purely a view cursor: versions live in the card's own description (see
  // chgk.js), and nothing about the cursor is persisted.
  let versionIdx = 0;
  // cardReturn remembers where the open card was launched from so its ↩️ back
  // button lands there: null → plain close (board view); {listId, cardId} → reopen
  // that list's preview scrolled to this question (set only when opened from a
  // preview's ✏️ button).
  let cardReturn: CardReturn | null = null;

  // contentReadTimer: 10s content-dwell timer; commentsObserver: IntersectionObserver
  // that starts a short dwell once #timeline scrolls into view. Both are armed in
  // openCard and torn down in closeCard (and re-armed on every openCard, so
  // switching cards never leaks a timer/observer onto the wrong card).
  let contentReadTimer: ReturnType<typeof setTimeout> | null = null;
  let commentsObserver: IntersectionObserver | null = null;

  // ---- card detail views: Просмотр (preview) / Поля (fields) / Текст (raw 4s) ----
  // The open card carries a working draft of its 4s description (and handout
  // settings) that flows between the three views without persisting; Save commits
  // the draft. cardView is the active view; lastEditView is the edit tab restored
  // when the user clicks ✎ / double-clicks the preview.
  let cardView = "";
  let lastEditView = "fields";
  // The card's working draft (4s desc + handout meta + alias) and its persisted
  // baseline live in carddraft.js so the dirty check is unit-tested; this file
  // keeps the DOM and drives `draft`.
  const draft = xyCardDraft.create();
  let cardFieldReaders: FieldReaders | null = null; // per-field read() closures for the Поля view
  // Blocks the Поля editor doesn't render but must not eat: the pre-question
  // markup (№/№№ and friends) and anything else unmodelled. Both are captured at
  // render time and re-emitted verbatim on recompose; the Текст view edits them.
  let cardFieldsPre: string | null = null;
  let cardFieldsExtra: string | null = null;

  const CARD_TABS = ["preview", "fields", "text"] as const;
  const tabBtn = (v: string): HTMLButtonElement => ui.tabs[v as (typeof CARD_TABS)[number]];

  // ---- add card ----
  // addCard persists a blank card and opens it. It used to stay unsaved until you
  // typed a description, which meant everything a card needs a row for — labels,
  // тесты, вложения, the лента, move/copy — was hidden on the one screen where
  // you most want to attach a picture (issue #26). A blank card is a real card;
  // an accidental one is deleted like any other.
  async function addCard(list: BoardList): Promise<void> {
    const existing = deps.cardsOf(list.id);
    const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
    const kind = "question";
    try {
      const dk = mustDK();
      const res = await verbs.create("createCard", `/api/lists/${list.id}/cards`, {
        description_enc: await xyCrypto.encField(dk, ""), rank, kind,
      });
      const card: BoardCard = {
        id: res.id as number, listId: list.id, kind, rank, desc: "",
        handoutMeta: null, alias: null, createdAt: nowStamp(),
      };
      state().cards.push(card);
      deps.render();
      await openCard(card, { fresh: true });
    } catch (err) {
      // The overlay is not open yet, so its message line would be invisible —
      // this failure has to surface on the board itself.
      deps.setStatus("error");
      alert("Не удалось создать карточку: " + errMsg(err));
    }  }

  // fitTextarea grows a textarea to fit its content so the user never scrolls
  // inside it (CSS min-height still sets the floor). scrollHeight is 0 while the
  // element is display:none, so callers fit on render / when a field is revealed.
  function fitTextarea(ta: HTMLTextAreaElement): void {
    // "" — not "auto" — so the floor is whatever `rows` and CSS say; a field
    // asking for three lines keeps them while its content is one line long.
    ta.style.height = "";
    if (ta.scrollHeight <= ta.clientHeight) return;
    // box-sizing is border-box, so the height must include the borders that
    // scrollHeight (content + padding only) omits, else the last line is clipped.
    const border = ta.offsetHeight - ta.clientHeight;
    ta.style.height = ta.scrollHeight + border + "px";
  }
  // autoGrow makes a textarea self-sizing: no inner scrollbar or resize handle,
  // and it regrows on every input.
  function autoGrow(ta: HTMLTextAreaElement): void {
    ta.style.overflowY = "hidden";
    ta.style.resize = "none";
    ta.addEventListener("input", () => fitTextarea(ta));
  }
  autoGrow(cardDescEl);

  function openCardCard(): BoardCard | undefined { return state().cards.find((c) => c.id === openCardId); }

  function draftKind(): string {
    const c = openCardCard();
    return c ? c.kind : cardKindEl.value || "question";
  }
  function fieldsAvailable(): boolean { return draftKind() === "question"; }

  // boardAuthors / boardSources collect the author names and source lines already
  // used across the board's question cards (deduped, sorted) — the autocomplete
  // suggestions for the Автор and Источник fields. A pack's questions tend to
  // share both (the same authors, the same handful of references), so offering
  // what the board already says beats retyping it.
  function boardFieldValues(pick: (f: CardFields) => string[] | null): string[] {
    const set = new Set<string>();
    for (const c of state().cards) {
      if (c.kind !== "question") continue;
      for (const v of pick(xyChgk.splitFields(c.desc)) || []) {
        const s = (v || "").trim();
        if (s) set.add(s);
      }
    }
    return [...set].sort((a, b) => a.localeCompare(b, "ru"));
  }
  function boardAuthors(): string[] { return boardFieldValues((f) => f.authors); }
  function boardSources(): string[] { return boardFieldValues((f) => f.sources); }

  // suggestWrap wraps an input in a hand-drawn autocomplete dropdown (substring
  // filter, tap or ↑/↓+Enter to pick). A <datalist> would be less code, but iOS
  // Safari simply never shows its options, so the suggestions are drawn by hand.
  // `values` is captured at build time — the board's authors/sources don't change
  // while the editor is open. onPick (optional) runs after a suggestion is taken.
  function suggestWrap(input: HTMLInputElement | HTMLTextAreaElement, values: string[], onPick?: (v: string) => void): HTMLElement {
    const menu = el("div", { class: "suggest-menu", hidden: true });
    const wrap = el("div", { class: "suggest-wrap" }, input, menu);
    let items: string[] = [], active = -1;
    const close = (): void => { menu.hidden = true; menu.replaceChildren(); items = []; active = -1; };
    // The "input" event is what the editor listens to — a picked suggestion has
    // to grow the field and arm Сохранить exactly as typing it would.
    const pick = (v: string): void => { input.value = v; input.dispatchEvent(new Event("input", { bubbles: true })); close(); if (onPick) onPick(v); };
    const setActive = (i: number): void => {
      active = i;
      [...menu.children].forEach((n, j) => n.classList.toggle("active", j === i));
    };
    const open = (): void => {
      const q2 = input.value.trim().toLowerCase();
      items = values.filter((v) => v.toLowerCase().includes(q2) && v !== input.value.trim()).slice(0, 8);
      if (!items.length) { close(); return; }
      menu.replaceChildren(...items.map((v) => {
        const b = el("button", { class: "suggest-item", type: "button", text: v });
        // pointerdown + preventDefault, not click: picking must not blur the input
        // (blur closes the menu before a click would land).
        b.addEventListener("pointerdown", (e) => { e.preventDefault(); pick(v); });
        return b;
      }));
      menu.hidden = false;
      active = -1;
    };
    input.addEventListener("input", open);
    input.addEventListener("focus", open);
    input.addEventListener("blur", close);
    // Registered before any caller keydown handler (Enter-commits-tag in the
    // authors field), so a menu pick can stopImmediatePropagation past it.
    // Bound through HTMLElement: on the input|textarea union TS drops the typed
    // event map and hands the listener a bare Event.
    const node: HTMLElement = input;
    node.addEventListener("keydown", (e) => {
      if (menu.hidden) return;
      if (e.key === "ArrowDown") { e.preventDefault(); setActive((active + 1) % items.length); }
      else if (e.key === "ArrowUp") { e.preventDefault(); setActive((active - 1 + items.length) % items.length); }
      else if (e.key === "Enter" && active >= 0) { e.preventDefault(); e.stopImmediatePropagation(); pick(items[active]); }
      else if (e.key === "Escape") { e.stopPropagation(); close(); }
    });
    return wrap;
  }

  // captureDraft folds the currently-visible view's edits back into the draft so
  // switching views never loses unsaved input.
  function captureDraft(): void {
    if (cardView === "text") writeVersionDesc(cardDescEl.value);
    else if (cardView === "fields" && cardFieldReaders) {
      const r = readCardFields(cardFieldReaders);
      writeVersionDesc(r.desc);
      draft.meta = r.meta;
    }
  }

  // refreshSaveState enables the save button only when the draft differs from what
  // was last persisted, so it's obvious whether the current edits are applied. A
  // new (unsaved) card has no baseline — save stays enabled while it has content.
  function refreshSaveState(): void {
    captureDraft();
    const btn = cardSaveBtn;
    const dirty = draft.contentDirty(false);
    btn.disabled = !dirty;
    // Просмотр is read-only, so nothing can be dirty there; the button hides.
    btn.hidden = cardView === "preview" && !dirty;
    // A stale "Карточка сохранена." next to a re-enabled button reads as a lie.
    if (dirty && cardMessageEl.textContent === "Карточка сохранена.") cardMessageEl.textContent = "";
  }

  function setCardView(view: string): void {
    captureDraft();
    // A non-question card has no Поля, so it falls back to Текст.
    if (view === "fields" && !fieldsAvailable()) view = "text";
    cardView = view;
    if (view !== "preview") lastEditView = view;
    ui.viewPreview.hidden = view !== "preview";
    ui.viewFields.hidden = view !== "fields";
    ui.viewText.hidden = view !== "text";
    for (const t of CARD_TABS) tabBtn(t).classList.toggle("active", t === view);
    tabBtn("text").textContent = "Формат 4s";
    tabBtn("fields").hidden = !fieldsAvailable();
    // Nothing to preview until the card has content; the tab appears as soon as
    // it does, so the flag needs no clearing.
    tabBtn("preview").hidden = freshCard && !draft.desc.trim();
    ui.viewTabs.hidden = false;
    // (the save button's visibility is refreshSaveState's alone — see the end of
    // this function — because it depends on more than the view)
    // The tools edit text, so they follow the two edit views. →.4s additionally
    // needs the raw 4s editor it types into.
    ui.editTools.hidden = view === "preview";
    ui.addVersion.hidden = !fieldsAvailable();
    renderVersionTabs();
    ui.typo.hidden = false;
    ui.to4s.hidden = view !== "text" || !fieldsAvailable();
    ui.descLabel.textContent = "Описание";
    if (view === "text") {
      const ta = cardDescEl;
      ta.value = versionDesc();
      // A brand-new question opens on an empty editor, which says nothing about what
      // the format wants. Seed the markers so the writer fills in blanks instead of
      // recalling 4s from memory; the caret lands after the "?". "Empty" includes
      // the author-only draft an untouched Поля view composes when a default
      // author is set — that's still a blank form.
      const bare = ta.value.trim();
      const authorOnly = state().defaultAuthor && bare === "@ " + state().defaultAuthor;
      if (freshCard && (!bare || authorOnly)) {
        ta.value = questionStub(state().defaultAuthor);
        ta.focus();
        ta.setSelectionRange(2, 2);
      }
      fitTextarea(ta);
    } else if (view === "fields") renderCardFields();
    else if (view === "preview") void renderCardPreview();
    refreshSaveState();
  }

  // ensureOption adds a <select> option for `name` if it isn't already present (so
  // an image referenced by the handout but not currently attached still shows).
  function ensureOption(sel: HTMLSelectElement, name: string): void {
    if (name && ![...sel.options].some((o) => o.value === name)) sel.append(el("option", { value: name, text: name }));
  }

  // buildField is the generic absent/present field control: a "+ label" pill when
  // absent, a labelled input with a "×" (back to absent) when present.
  function buildField(label: string, kind: "area" | "input", initial: string | null | undefined, opts: { muted?: boolean; open?: boolean; rows?: number } = {}): FieldReader<string | null> {
    const wrap = el("div", { class: "fld" + (opts.muted ? " fld-muted" : "") });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ " + label, title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label }), rmBtn);
    const input = (kind === "area"
      ? el("textarea", { class: "card-desc fld-input", spellcheck: "false", rows: String(opts.rows || 1) })
      : el("input", { class: "input fld-input", type: "text" })) as HTMLTextAreaElement | HTMLInputElement;
    const body = el("div", { class: "fld-body" }, input);
    if (kind === "area") autoGrow(input as HTMLTextAreaElement);
    let present = initial !== null && initial !== undefined;
    if (present) input.value = initial as string;
    // opts.open pre-expands an absent field (new cards open Текст вопроса/Ответ
    // ready to type). Left untouched it still reads as absent, so an unedited
    // stub composes to the same (empty) draft as before.
    const autoOpened = !present && !!opts.open;
    if (autoOpened) present = true;
    const sync = (): void => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; wrap.classList.toggle("fld-present", present); if (present && kind === "area") fitTextarea(input as HTMLTextAreaElement); };
    addBtn.addEventListener("click", () => { present = true; sync(); input.focus(); });
    rmBtn.addEventListener("click", () => { present = false; sync(); });
    wrap.append(addBtn, head, body);
    sync();
    return { node: wrap, read: () => (present ? (autoOpened && input.value === "" ? null : input.value) : null) };
  }

  // buildHandoutField: the "Раздаточный материал" field with a текст/картинка
  // toggle. Image mode picks among the card's attached images.
  function buildHandoutField(initial: Handout | null): FieldReader<Handout | null> {
    const wrap = el("div", { class: "fld" });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Раздаточный материал", title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Раздаточный материал" }), rmBtn);
    const modeText = el("button", { class: "seg-btn", type: "button", text: "текст" });
    const modeImg = el("button", { class: "seg-btn", type: "button", text: "картинка" });
    const toggle = el("div", { class: "seg" }, modeText, modeImg);
    const ta = el("textarea", { class: "card-desc fld-input", spellcheck: "false", rows: "1" }) as HTMLTextAreaElement;
    autoGrow(ta);
    const sel = el("select", { class: "input fld-input" }) as HTMLSelectElement;
    const cardImageNames = deps.attachments.imageNames();
    for (const n of cardImageNames) sel.append(el("option", { value: n, text: n }));
    // Picking a handout picture used to mean attaching the file further down the
    // card first, then coming back up here to choose it — and on a card that had
    // no attachments yet the dropdown was simply empty, with no way out of it
    // (issue #26). This attaches and selects in one gesture.
    const filePick = el("input", { type: "file", accept: "image/*", hidden: true }) as HTMLInputElement;
    const attachBtn = el("button", { class: "input fld-add-row", type: "button", title: "Загрузить картинку и подставить её сюда" }, ...iconed("paperclip", "Прикрепить…"));
    attachBtn.addEventListener("click", () => filePick.click());
    filePick.addEventListener("change", async () => {
      const file = filePick.files && filePick.files[0];
      filePick.value = ""; // so re-picking the same file fires change again
      if (!file) return;
      try {
        await deps.attachments.upload(file, true, file.name);
        ensureOption(sel, file.name);
        sel.value = file.name;
      } catch (err) { cardMessageEl.textContent = errMsg(err); }
    });
    const imgRow = el("div", { class: "fld-row" }, sel, attachBtn, filePick);
    const body = el("div", { class: "fld-body" }, toggle, ta, imgRow);
    let mode: "text" | "image" = initial && initial.kind === "image" ? "image" : "text";
    if (initial) {
      if (initial.kind === "image") { ensureOption(sel, initial.name); sel.value = initial.name || ""; }
      else ta.value = initial.text || "";
    }
    if (!cardImageNames.length) ensureOption(sel, "");
    const syncMode = (): void => {
      modeText.classList.toggle("active", mode === "text");
      modeImg.classList.toggle("active", mode === "image");
      ta.hidden = mode !== "text";
      imgRow.hidden = mode !== "image";
      if (mode === "text" && present) fitTextarea(ta);
    };
    modeText.addEventListener("click", () => { mode = "text"; syncMode(); });
    modeImg.addEventListener("click", () => { mode = "image"; syncMode(); });
    let present = !!initial;
    const sync = (): void => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; wrap.classList.toggle("fld-present", present); if (present && mode === "text") fitTextarea(ta); };
    addBtn.addEventListener("click", () => { present = true; sync(); });
    rmBtn.addEventListener("click", () => { present = false; sync(); });
    wrap.append(addBtn, head, body);
    sync(); syncMode();
    return {
      node: wrap,
      read: (): Handout | null => (present ? (mode === "image" ? { kind: "image", name: sel.value } : { kind: "text", text: ta.value }) : null),
    };
  }

  // buildSourcesField: the multi-line "Источник" field (one input per source line,
  // add/remove rows), each row autocompleting from the board's existing sources.
  function buildSourcesField(initial: string[] | null, suggestions: string[]): FieldReader<string[] | null> {
    const wrap = el("div", { class: "fld" });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Источник", title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Источник" }), rmBtn);
    const rows = el("div", { class: "fld-rows" });
    // A source is one line of the 4s but often a URL longer than the card is
    // wide, so it is a textarea: it wraps and grows instead of scrolling out of
    // sight. Enter still belongs to the row list, not to the text.
    const addRow = (val: string): HTMLTextAreaElement => {
      const inp = el("textarea", { class: "card-desc fld-row-input fld-src", spellcheck: "false", rows: "1" }) as HTMLTextAreaElement;
      inp.value = val || "";
      autoGrow(inp);
      const rrm = el("button", { class: "fld-row-rm", type: "button", text: "×", title: "Удалить строку" });
      const row = el("div", { class: "fld-row" }, suggestWrap(inp, suggestions), rrm);
      // After suggestWrap, so an Enter that takes a suggestion is stopped before
      // it reaches this and opens a row nobody asked for.
      inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); addRow("").focus(); } });
      rrm.addEventListener("click", () => row.remove());
      rows.append(row);
      return inp;
    };
    const rowAdd = el("button", { class: "input fld-add-row", type: "button", text: "+ строка" });
    rowAdd.addEventListener("click", () => addRow("").focus());
    const body = el("div", { class: "fld-body" }, rows, rowAdd);
    let present = initial !== null && initial !== undefined;
    (present ? ((initial as string[]).length ? (initial as string[]) : [""]) : []).forEach((s) => addRow(s));
    const sync = (): void => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; wrap.classList.toggle("fld-present", present); };
    addBtn.addEventListener("click", () => { present = true; if (!rows.children.length) addRow(""); sync(); });
    rmBtn.addEventListener("click", () => { present = false; sync(); });
    wrap.append(addBtn, head, body);
    sync();
    return { node: wrap, read: () => (present ? [...rows.querySelectorAll<HTMLTextAreaElement>(".fld-row-input")].map((i) => i.value) : null) };
  }

  // buildAuthorsField: a tag input (like labels) seeded with autocomplete from the
  // board's existing authors; free text adds a new author. The field's own label
  // is a dropdown, because the caption is a choice the 4s carries as a
  // "!!override" — Автор / Авторка / Авторы / Авторки (issue #44).
  function buildAuthorsField(initial: string[] | null, suggestions: string[], label: string | null): FieldReader<AuthorsValue> {
    const wrap = el("div", { class: "fld" });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Автор", title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const labelSel = el("select", { class: "fld-label-select", title: "Подпись поля в экспорте" }) as HTMLSelectElement;
    for (const l of xyChgk.AUTHOR_LABELS) labelSel.append(el("option", { value: l, text: l }));
    // A card may already carry a caption of its own («!!Составитель»). It gets its
    // own entry rather than being silently folded into Автор — nothing an editor
    // wrote is thrown away by opening the card.
    if (label && !(xyChgk.AUTHOR_LABELS as readonly string[]).includes(label)) {
      labelSel.append(el("option", { value: label, text: label }));
    }
    labelSel.value = label || xyChgk.AUTHOR_LABELS[0];
    const head = el("div", { class: "fld-head" }, labelSel, rmBtn);
    const tags = el("div", { class: "fld-tags" });
    const tagSet: string[] = [];
    const inp = el("input", { class: "input fld-tag-input", type: "text", placeholder: "имя автора…" }) as HTMLInputElement;
    const renderTags = (): void => {
      tags.replaceChildren(...tagSet.map((t, i) => {
        const rm = el("button", { class: "fld-tag-rm", type: "button", text: "×" });
        rm.addEventListener("click", () => { tagSet.splice(i, 1); renderTags(); });
        return el("span", { class: "fld-tag" }, document.createTextNode(t), rm);
      }));
    };
    const commit = (): void => { const v = inp.value.trim(); if (v) { tagSet.push(v); inp.value = ""; renderTags(); } };
    // suggestWrap first, so its keydown handler outranks the Enter-commit below.
    const inpWrap = suggestWrap(inp, suggestions, commit);
    inp.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === ",") { e.preventDefault(); commit(); } });
    inp.addEventListener("blur", commit);
    const body = el("div", { class: "fld-body" }, tags, inpWrap);
    let present = initial !== null && initial !== undefined;
    if (present) (initial as string[]).forEach((t) => tagSet.push(t));
    renderTags();
    const sync = (): void => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; wrap.classList.toggle("fld-present", present); };
    addBtn.addEventListener("click", () => { present = true; sync(); inp.focus(); });
    rmBtn.addEventListener("click", () => { present = false; sync(); });
    wrap.append(addBtn, head, body);
    sync();
    // read() runs on EVERY input event (refreshSaveState → captureDraft), so it
    // must not commit() — that turned each typed letter into its own author tag.
    // Include the in-progress text without touching the input; actual commits
    // happen on Enter/comma/blur/suggestion-pick.
    return { node: wrap, read: () => {
      if (!present) return { names: null, label: null };
      const v = inp.value.trim();
      return { names: v ? [...tagSet, v] : tagSet.slice(), label: labelSel.value || null };
    } };
  }

  // renderCardFields rebuilds the Поля editor from the current draft (and handout
  // settings). The last field (handout-gen markup) binds to draft.meta, not the 4s.
  function renderCardFields(): void {
    const f = xyChgk.splitFields(versionDesc());
    // A brand-new card pre-fills the user's default author (a /profile setting)
    // and opens the two fields every question has, ready to type into.
    const fresh = freshCard && !draft.desc.trim();
    if (fresh && f.authors == null && state().defaultAuthor) f.authors = [state().defaultAuthor];
    cardFieldsPre = f.preMarkup;
    cardFieldsExtra = f.extra;
    const box = cardFieldsEl;
    box.replaceChildren();
    const R: FieldReaders = {
      handout: buildHandoutField(f.handout),
      question: buildField("Текст вопроса", "area", f.question, { open: fresh }),
      answer: buildField("Ответ", "area", f.answer, { open: fresh }),
      // Three lines, and growing: a зачёт is a list of accepted wordings, and
      // one line of it was a slot you wrote a paragraph through.
      zachet: buildField("Зачёт", "area", f.zachet, { rows: 3 }),
      nezachet: buildField("Незачёт", "area", f.nezachet, { rows: 3 }),
      comment: buildField("Комментарий", "area", f.comment),
      sources: buildSourcesField(f.sources, boardSources()),
      authors: buildAuthorsField(f.authors, boardAuthors(), f.authorLabel),
      hndt: buildField("Доп. разметка для генерации раздаток", "area", draft.meta, { muted: true }),
    };
    for (const k of ["handout", "question", "answer", "zachet", "nezachet", "comment", "sources", "authors", "hndt"] as const) box.append(R[k].node);
    // Size pre-filled fields now they're in the live DOM (scrollHeight is 0 while
    // detached, so the fit during buildField is a no-op for visible content).
    for (const ta of box.querySelectorAll("textarea")) fitTextarea(ta);
    cardFieldReaders = R;
  }

  // ---- versions ----
  // draft.desc holds the card whole — every version of it. The editor works on
  // one: versionDesc is the window all three views read, writeVersionDesc the one
  // they write back through. Both go through the draft rather than the editor,
  // which is what keeps the versions you cannot see from being dropped on save.
  function versionDesc(): string {
    return xyVersions.versionBody(draft.desc, versionIdx);
  }

  function writeVersionDesc(body: string): void {
    draft.desc = xyVersions.setVersionBody(draft.desc, versionIdx, body);
  }

  function versionCount(): number {
    return xyVersions.versionCount(draft.desc);
  }

  // applyVersions reshapes the card's versions and re-renders. Every version edit
  // — add, delete, promote, rename — goes through here, so there is one place that
  // keeps draft, cursor and views in step. The transform is passed in rather than
  // its result: the draft has to be captured BEFORE the description is read, or an
  // in-flight edit is reshuffled away.
  function applyVersions(fn: (desc: string) => { desc: string; index: number }): void {
    captureDraft();
    const next = fn(draft.desc);
    draft.desc = next.desc;
    selectVersion(next.index);
  }

  // selectVersion moves the cursor and redraws. It captures nothing itself: both
  // callers fold their edits in first, and a capture taken after the cursor moved
  // files the outgoing version's words under the incoming one — which is how
  // editing a freshly added version used to change both, and how promoting one
  // could overwrite the version it swapped with.
  //
  // Every editor still holds the OUTGOING version at this point, and setCardView
  // opens with a capture of its own, so both have to be re-pointed BEFORE it runs:
  // the field readers are dropped (renderCardFields rebuilds them immediately) and
  // the raw editor is retyped, which makes that capture a write of the incoming
  // body onto itself.
  function selectVersion(i: number): void {
    versionIdx = i;
    cardFieldReaders = null;
    cardDescEl.value = versionDesc();
    setCardView(cardView || "fields");
    refreshSaveState();
  }

  // renderVersionTabs draws the second strip. It only appears once there is more
  // than one version — a plain question must not grow a row of chrome that says
  // «Версия 1» and nothing else, and a name is there to tell siblings apart. The
  // three actions ride on the selected tab rather than on every one: that many
  // controls per version is a lot on a phone, and all of them act on what you are
  // looking at.
  function renderVersionTabs(): void {
    const box = ui.versions;
    const n = versionCount();
    if (versionIdx >= n) versionIdx = n - 1;
    // All three views are scoped to one version now, Текст included, so the strip
    // belongs above every one of them.
    const show = n > 1 && fieldsAvailable();
    box.hidden = !show;
    if (!show) { box.replaceChildren(); return; }
    const nodes: HTMLElement[] = [];
    for (let i = 0; i < n; i++) {
      const name = xyVersions.versionName(draft.desc, i);
      const btn = el("button", { class: "seg-btn" + (i === versionIdx ? " active" : ""), type: "button", role: "tab", text: name || `Версия ${i + 1}` });
      btn.addEventListener("click", () => { captureDraft(); selectVersion(i); });
      nodes.push(btn);
      if (i !== versionIdx) continue;
      if (i > 0) {
        const up = el("button", { class: "vtab-act", type: "button", title: "Сделать первой — первая версия и есть та, которую видно на доске", "aria-label": "Сделать первой" }, icon("arrow-up"));
        up.addEventListener("click", () => applyVersions((d) => xyVersions.promoteVersion(d, i)));
        nodes.push(up);
      }
      const ren = el("button", { class: "vtab-act", type: "button", title: "Назвать версию — название видно только здесь, ни в один экспорт оно не попадёт", "aria-label": "Назвать версию" }, icon("pencil"));
      ren.addEventListener("click", () => {
        const typed = prompt("Название версии:", name || "");
        if (typed === null) return;
        applyVersions((d) => ({ desc: xyVersions.setVersionName(d, i, typed), index: i }));
      });
      nodes.push(ren);
      const rm = el("button", { class: "vtab-act", type: "button", title: "Удалить эту версию целиком", "aria-label": "Удалить версию" }, icon("trash-2"));
      rm.addEventListener("click", () => applyVersions((d) => xyVersions.removeVersion(d, i)));
      nodes.push(rm);
    }
    box.replaceChildren(...nodes);
  }

  ui.addVersion.addEventListener("click", () => {
    applyVersions((d) => xyVersions.addVersion(d, versionIdx));
  });

  // readCardFields collapses the Поля editor back into a 4s description + handout
  // settings, preserving the pre-question and unmodelled blocks captured at render time.
  function readCardFields(R: FieldReaders): { desc: string; meta: string | null } {
    const authors = R.authors.read();
    const rec: Partial<CardFields> = {
      preMarkup: cardFieldsPre,
      handout: R.handout.read(),
      question: R.question.read(),
      answer: R.answer.read(),
      zachet: R.zachet.read(),
      nezachet: R.nezachet.read(),
      comment: R.comment.read(),
      sources: R.sources.read(),
      authors: authors.names,
      authorLabel: authors.label,
      extra: cardFieldsExtra,
    };
    return { desc: xyChgk.composeFields(rec), meta: R.hndt.read() };
  }

  // renderCardPreview renders the open card's draft the docx way (single-card
  // version of the list preview). Read-only; double-click jumps back to editing.
  async function renderCardPreview(): Promise<void> {
    const body = ui.previewBody;
    if (!draft.desc.trim()) { body.replaceChildren(el("p", { class: "pv-empty", text: "Пусто." })); return; }
    const c = openCardCard();
    const card: PreviewCardLike = { id: c ? c.id : 0, kind: draftKind(), desc: versionDesc(), listId: c ? c.listId : 0 };
    const number = card.kind === "question" ? deps.questionNumberFor(card) : null;
    const reqId = openCardId;
    const screen = ui.previewScreen.checked;
    const imgMap = new Map<string, string>();
    body.replaceChildren(deps.preview.renderPreviewCard(card, number, imgMap, screen));
    await deps.preview.resolveImages([card], deps.preview.imageRefs([card]), (name, url) => {
      imgMap.set(name, url);
      if (cardView === "preview" && openCardId === reqId) deps.preview.fillPreviewImages(body, imgMap);
    });
  }

  // Tab clicks + the preview screen toggle + double-click-to-edit.
  for (const v of CARD_TABS) tabBtn(v).addEventListener("click", () => setCardView(v));
  ui.previewScreen.addEventListener("change", () => { if (cardView === "preview") void renderCardPreview(); });
  ui.previewBody.addEventListener("dblclick", () => setCardView(lastEditView));

  // ---- edit tools (the row under the tabs) ----
  // A button takes focus on mousedown, and the Автор tag input commits what is
  // typed on blur — so «Ва» + ударение became a chip and a lone accent (issue
  // #63). The tools keep the caret where it is; the phone keyboard stays up too.
  for (const node of [ui.insStress, ui.typo, ui.to4s]) {
    node.addEventListener("mousedown", (e) => e.preventDefault());
  }
  // Still remember the last field the caret was in: the card may have just
  // opened with nothing focused. The Поля view rebuilds its inputs on every view
  // switch, hence the isConnected check when using it.
  let lastEditField: HTMLTextAreaElement | HTMLInputElement | null = null;
  for (const panel of [ui.viewFields, ui.viewText]) {
    panel.addEventListener("focusin", (e) => {
      const t = e.target;
      if (t instanceof HTMLElement && t.matches("textarea, input[type=text]")) lastEditField = t as HTMLTextAreaElement | HTMLInputElement;
    });
  }

  // editField is the field ударение writes into: the last one edited, or — when the
  // card was just opened and nothing has been focused yet — the raw editor.
  function editField(): HTMLTextAreaElement | HTMLInputElement | null {
    if (lastEditField && lastEditField.isConnected && lastEditField.offsetParent) return lastEditField;
    return cardView === "text" ? cardDescEl : null;
  }

  // insertAtCaret types text at the field's caret (replacing its selection). It goes
  // through execCommand because that is the only way to edit a field without
  // throwing away the browser's undo stack — a hand-spliced .value makes Ctrl-Z drop
  // everything typed before it. It also fires `input`, which is what regrows an
  // autoGrow textarea; the fallback has to do that itself.
  function insertAtCaret(field: HTMLTextAreaElement | HTMLInputElement, text: string): void {
    field.focus();
    if (document.execCommand("insertText", false, text)) return;
    const s = field.selectionStart ?? 0, e = field.selectionEnd ?? 0;
    field.setRangeText(text, s, e, "end");
    field.dispatchEvent(new Event("input", { bubbles: true }));
  }

  // replaceField swaps a field's whole content through the same undo-preserving path.
  function replaceField(field: HTMLTextAreaElement | HTMLInputElement, text: string): void {
    field.focus();
    field.setSelectionRange(0, field.value.length);
    if (!document.execCommand("insertText", false, text)) {
      field.value = text;
      field.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }

  // The combining acute (U+0301) attaches to the character left of the caret, which
  // is the chgk convention for marking stress ("зАмок" → "зам́ок" as typed).
  ui.insStress.addEventListener("click", () => {
    const f = editField();
    if (f) insertAtCaret(f, "́");
  });

  // типограф runs the WHOLE card — not just the focused field — through
  // chgksuite's typography pass (quotes → «ёлочки», hyphen runs → em dashes,
  // non-breaking spaces and hyphens, percent-escapes decoded back into the words a
  // pasted wiki link stands for). It runs in the browser (typo.ts), so it works
  // offline and no question text is posted anywhere; the draft is 4s either way,
  // so Поля and Текст feed it the same thing and only the landing differs.
  ui.typo.addEventListener("click", () => {
    captureDraft();
    if (!draft.desc.trim()) return;
    // EVERY version, not just the one on screen: the button says «типограф», and
    // a wording you are not looking at has the same кавычки the one you are does.
    // The pass runs per version and the card is reassembled, so the separators are
    // never handed to it.
    draft.desc = xyTypo.passVersions(draft.desc);
    // In Текст the user is looking at the raw 4s, so type it back into the editor
    // (undo intact); in Поля the fields are a view of the draft, so rebuild them.
    if (cardView === "text") replaceField(cardDescEl, versionDesc());
    else renderCardFields();
    // The tools change the draft by clicking, not by typing, so nothing has fired
    // the `input` that normally re-tests it against what is saved.
    refreshSaveState();
  });

  // →.4s runs the raw editor's content through the server's chgk text parser — the
  // .docx import pipeline minus the .docx — so a question pasted as plain prose
  // ("Вопрос 1: … Ответ: … Автор: …") becomes marked-up 4s. The parse is a guess, so
  // it lands back in the editor for the user to check; nothing is saved until Save.
  // Online-only: the parser is the Go port on the server (it never keeps the text).
  ui.to4s.addEventListener("click", async () => {
    const ta = cardDescEl;
    const text = ta.value.trim();
    if (!text) return;
    if (!xySync.requireOnline("Разбор текста доступен только онлайн.")) return;
    setStatus("saving");
    try {
      const res = await fetch("/api/import/text", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text }),
      });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      const { source } = (await res.json()) as { source: string };
      setStatus("saved");
      replaceField(ta, source);
    } catch (err) {
      setStatus("error");
      alert("Не удалось разобрать текст: " + errMsg(err));
    }
  });

  // ---- direct links (shareable URLs for a card and a comment) ----
  // A card link is /board/{id}?card={cardId}; a comment link adds &comment={eventId}
  // (the timeline event id). Opening such a URL deep-links straight to the card and,
  // for a comment link, scrolls to and flashes that comment.
  function cardUrl(cardId: number): string { return `${location.origin}${location.pathname}?card=${cardId}`; }
  function commentUrl(cardId: number, eventId: number): string { return `${cardUrl(cardId)}&comment=${eventId}`; }

  // maybeOpenDeepLink runs once after the first successful board load: if the URL
  // names a card (and optionally a comment), open it. The bare board URL is put
  // back first, so the entry the card pushes has the board underneath it and
  // going back from a shared link lands there instead of leaving the app.
  let deepLinkDone = false;
  function maybeOpenDeepLink(): void {
    if (deepLinkDone) return;
    deepLinkDone = true;
    const params = new URLSearchParams(location.search);
    const cardId = Number(params.get("card"));
    if (!cardId) return;
    const card = state().cards.find((c) => c.id === cardId);
    if (!card) return;
    const commentId = Number(params.get("comment")) || null;
    history.replaceState(null, "", location.pathname);
    openCard(card).then(() => { if (commentId) void highlightComment(commentId); }).catch(() => {});
  }

  // highlightComment scrolls a comment into view and flashes it. The event node
  // carries id "tlev-{eventId}" — and only exists if the лента's filter shows
  // comments at all, which is what ensureVisible settles first.
  async function highlightComment(eventId: number): Promise<void> {
    await deps.timeline.ensureVisible("comment");
    const node = ui.timeline.querySelector<HTMLElement>(`#tlev-${eventId}`);
    if (!node) return;
    node.scrollIntoView({ block: "center" });
    node.classList.add("tl-highlight");
    setTimeout(() => node.classList.remove("tl-highlight"), 2500);
  }

  async function copyCardLink(): Promise<void> {
    if (openCardId == null) return;
    try { await copyText(cardUrl(openCardId)); showCopyMsg("Ссылка на карточку скопирована", false); }
    catch (err) { showCopyMsg("Не удалось скопировать: " + errMsg(err), true); }
  }

  async function copyCommentLink(eventId: number): Promise<void> {
    if (openCardId == null) return;
    try { await copyText(commentUrl(openCardId, eventId)); showCopyMsg("Ссылка на комментарий скопирована", false); }
    catch (err) { showCopyMsg("Не удалось скопировать: " + errMsg(err), true); }
  }

  async function openCard(card: BoardCard, opts: { returnTo?: CardReturn | null; fresh?: boolean } = {}): Promise<void> {
    stopReadTracking(); // tear down any timer/observer left over from a previous card
    freshCard = !!opts.fresh;
    versionIdx = 0;
    cardReturn = opts.returnTo || null;
    openCardId = card.id;
    deps.onOpenCard?.(card.id);
    cardView = "";
    cardFieldReaders = null;
    const openMeta = card.handoutMeta != null ? card.handoutMeta : null;
    const openAlias = card.alias != null ? card.alias : null;
    draft.open(card.desc, openMeta, openAlias);
    cancelAliasSave(); // the previous card's pending write is its own to make
    cardAliasEl.value = openAlias || "";
    cardDescEl.value = xyVersions.versionBody(card.desc, 0);
    cardMessageEl.textContent = "";
    cardKindEl.hidden = false;
    cardKindEl.value = card.kind || "question";
    ui.title.hidden = true;
    // The "copy for testing" action only makes sense for question cards (it shares
    // the numbered, screen-mode question text); hide it otherwise.
    ui.copy.hidden = card.kind !== "question";
    ui.copyMsg.hidden = true;
    // The exit says what it does: opened from a list preview, closing lands back
    // in that preview, so it is ← Назад; opened from the board, it is × Закрыть.
    const back = !!cardReturn;
    cardCloseBtn.replaceChildren(icon(back ? "arrow-left" : "x"));
    cardCloseBtn.title = back ? "Назад" : "Закрыть";
    cardCloseBtn.setAttribute("aria-label", cardCloseBtn.title);
    cardOverlay.hidden = false;
    // Opened from a list preview, the card takes that preview's place rather
    // than stacking on top of it — one step forward, so one back gets out.
    const entry = { el: cardOverlay, close: hideCard, confirm: confirmLeaveCard };
    if (cardReturn) overlayStack.replace(previewOverlay, entry, cardUrl(card.id));
    else if (!overlayStack.isTop(cardOverlay)) overlayStack.open(entry, cardUrl(card.id));
    else overlayStack.replace(cardOverlay, entry, cardUrl(card.id));
    deps.renderLabelPicker(card);
    deps.paintLabels();
    lastEditView = fieldsAvailable() ? "fields" : "text";
    // Render the chosen view straight away so reopening a card never flashes the
    // previously-open card's content. The preview resolves its own images, so it
    // doesn't wait on the per-card loads below — which run in parallel, not
    // sequentially, to cut the total round-trip.
    // A card just created has nothing to preview, so it opens on the editor.
    setCardView(freshCard ? lastEditView : "preview");
    // Before the load: a card opens on the reader's own лента default, whatever
    // the previously open card was narrowed to.
    deps.timeline.resetFilter();
    await Promise.all([deps.attachments.load(card.id), deps.timeline.load(card.id), populateMoveBoards()]);
    armReadTracking(card);
  }

  // stopReadTracking clears the content-dwell timer and disconnects the
  // comments IntersectionObserver — called before re-arming (openCard) and on
  // closeCard, so neither ever fires against a card that's no longer open.
  function stopReadTracking(): void {
    if (contentReadTimer) { clearTimeout(contentReadTimer); contentReadTimer = null; }
    if (commentsObserver) { commentsObserver.disconnect(); commentsObserver = null; }
  }

  // armReadTracking shows/clears the in-card unread dots and arms the read
  // triggers. Both content edits (desc_edit) and comments are recorded as entries
  // in the timeline (лента) — that's where a reader actually sees *what* changed —
  // so viewing the timeline clears whichever buckets are unread — but only those
  // the лента's filter actually put on screen. Content also clears after a 10s
  // dwell on the card body itself (a secondary trigger, for the reader who studies
  // the question text without scrolling down to the лента) — deliberately NOT
  // filter-aware: that dwell is on the new text, which is the edit's result.
  function armReadTracking(card: BoardCard): void {
    const u = state().unread[card.id] || {};
    ui.contentUnreadDot.hidden = !u.content;
    ui.commentsUnreadDot.hidden = !u.comments;
    ui.commentsUnreadDot.classList.toggle("unread-dot-mention", !!u.mentions);

    if (u.content) {
      contentReadTimer = setTimeout(() => {
        contentReadTimer = null;
        if (openCardId === card.id) void markCardRead(card.id, { content: true });
      }, 10000);
    }

    if (u.content || u.comments) {
      const timeline = ui.timeline;
      let dwellTimer: ReturnType<typeof setTimeout> | null = null;
      commentsObserver = new IntersectionObserver((entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && entry.intersectionRatio > 0) {
            if (!dwellTimer) {
              dwellTimer = setTimeout(() => {
                const seen = deps.timeline.readBuckets();
                if (openCardId === card.id) void markCardRead(card.id, { content: !!u.content && seen.content, comments: !!u.comments && seen.comments });
              }, 2000);
            }
          } else if (dwellTimer) {
            clearTimeout(dwellTimer);
            dwellTimer = null;
          }
        }
      });
      commentsObserver.observe(timeline);
    }
  }

  // markCardRead advances the caller's read watermark(s) for a card to the
  // highest event id currently loaded in its timeline (captured by loadTimeline
  // into openCardEvents), then updates local state + the dots. Best-effort:
  // failures are swallowed (a missed watermark just means the dot lingers).
  async function markCardRead(cardId: number, { content = false, comments = false }: { content?: boolean; comments?: boolean } = {}): Promise<void> {
    if (!xySync.isOnline()) return;
    const events = deps.timeline.events() || [];
    // A reaction rides the bucket of its target: a comment's reaction clears
    // with the comments, a card-level one with the content (mirrors the
    // server's unread split).
    const inCommentBucket = (e: CardEvent): boolean =>
      e.type === "comment" || (e.type === "reaction" && e.reply_to_id != null);
    const maxId = (pred: (e: CardEvent) => boolean): number => events.filter(pred).reduce((m, e) => (e.id > m ? e.id : m), 0);
    const contentReadId = content ? maxId((e) => !inCommentBucket(e)) : 0;
    const commentReadId = comments ? maxId(inCommentBucket) : 0;
    if (!contentReadId && !commentReadId) return;
    try {
      await jpost(`/api/cards/${cardId}/read`, { content_read_id: contentReadId, comment_read_id: commentReadId });
    } catch (_) { return; }
    const st = state();
    const u: UnreadFlags = { ...(st.unread[cardId] || {}) };
    if (content) u.content = false;
    if (comments) { u.comments = false; u.mentions = false; }
    if (u.content || u.comments) st.unread[cardId] = u;
    else delete st.unread[cardId];
    if (content) ui.contentUnreadDot.hidden = true;
    if (comments) ui.commentsUnreadDot.hidden = true;
    deps.readMarkers.refreshCardUnreadDot(cardId);
    deps.readMarkers.renderNotifBadge();
  }

  // ---- move / copy a card (same board → relocate/duplicate; other board →
  // client-side re-encryption). Boards are chosen by (decrypted) name and
  // the destination list + position are selectable. ----

  let moveCtx: MoveCtx | null = null;



  async function populateMoveBoards(): Promise<void> {
    const sel = moveBoardSel;
    sel.replaceChildren();
    for (const b of await transfer.moveBoardOptions()) sel.append(el("option", { value: b.id, text: b.label }));
    sel.value = String(boardId);
    await onMoveBoardChange();
  }


  async function onMoveBoardChange(): Promise<void> {
    const listSel = moveListSel;
    const bid = Number(moveBoardSel.value);
    listSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
    try { moveCtx = await transfer.loadMoveBoard(bid); }
    catch (err) {
      moveCtx = null;
      listSel.replaceChildren(el("option", { value: "", text: errMsg(err) }));
      movePosSel.replaceChildren();
      return;
    }
    listSel.replaceChildren();
    for (const l of moveCtx.lists) listSel.append(el("option", { value: l.id, text: l.title || "(без названия)" }));
    if (!moveCtx.lists.length) listSel.append(el("option", { value: "", text: "нет списков" }));
    onMoveListChange();
  }

  // onMoveListChange fills the position <select> with "в конец" + one slot per
  // existing card (the card being moved is excluded when staying on its board).
  function onMoveListChange(): void {
    const posSel = movePosSel;
    posSel.replaceChildren();
    if (!moveCtx) return;
    const listId = Number(moveListSel.value);
    const cards = (moveCtx.cardsByList.get(listId) || []).filter((c) => !(moveCtx && moveCtx.boardId === boardId && c.id === openCardId));
    posSel.append(el("option", { value: "end", text: "в конец" }));
    for (let i = 1; i <= cards.length; i++) posSel.append(el("option", { value: String(i), text: `позиция ${i}` }));
    posSel.value = "end";
  }







  async function doMoveCopy(remove: boolean): Promise<void> {
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card || !moveCtx) return;
    const targetBid = moveCtx.boardId;
    const targetListId = Number(moveListSel.value);
    if (!targetListId) return;
    const msg = cardMessageEl;
    const listCards = moveCtx.cardsByList.get(targetListId) || [];
    const sameBoard = targetBid === boardId;
    const rank = rankForSlot(listCards, movePosSel.value, sameBoard && remove ? card.id : undefined);
    // The only offline-capable case is an intra-board move (just a re-parent/re-rank).
    // Copying — and anything touching another board — carries comments/attachments
    // and re-encrypts, so it's online-only.
    const intraBoardMove = sameBoard && remove;
    if (!intraBoardMove && !xySync.requireOnline("Копирование и перенос между досками доступны только онлайн.", msg)) return;
    msg.textContent = sameBoard ? "Сохранение…" : "Перешифровка…";
    try {
      await transfer.transferCard(card, targetListId, moveCtx, remove, rank);
      if (!sameBoard && remove) dismissCard();
      deps.render();
      if (intraBoardMove) { await populateMoveBoards(); } // refresh positions
      msg.textContent = remove ? "Перемещено." : "Скопировано.";
    } catch (err) { msg.textContent = errMsg(err); }
  }

  moveBoardSel.addEventListener("change", () => { void onMoveBoardChange(); });
  moveListSel.addEventListener("change", onMoveListChange);
  ui.copyBtn.addEventListener("click", () => { void doMoveCopy(false); });
  ui.move.btn.addEventListener("click", () => { void doMoveCopy(true); });

  // Change card kind after creation (edit mode only; create mode uses the same
  // selector but the value is applied on first save). Test cards never reach here
  // (their selector is hidden in openCard).
  cardKindEl.addEventListener("change", async () => {
    if (openCardId == null) return;
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return;
    const kind = cardKindEl.value;
    const msg = cardMessageEl;
    try {
      await verbs.patch("patchCard", `/api/cards/${card.id}`, { kind });
      card.kind = kind;
      deps.render();
      setCardView(cardView || "text"); // re-eval tab availability (Поля is question-only)
      msg.textContent = "Тип изменён.";
    } catch (err) { msg.textContent = errMsg(err); }
  });

  // ---- copy a question to the clipboard for a test session ----
  // (questionNumberFor stays with the board's group-numbering logic; injected.)

  // copyText writes to the clipboard, falling back to a hidden textarea +
  // execCommand on insecure contexts / older browsers without the async API.
  async function copyText(text: string): Promise<void> {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return;
    }
    const ta = el("textarea") as HTMLTextAreaElement;
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.append(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    ta.remove();
    if (!ok) throw new Error("буфер обмена недоступен");
  }

  // showCopyMsg flashes the copy result right under the button (auto-hiding) so the
  // feedback is next to the action, not buried at the bottom of the panel.
  let copyMsgTimer: ReturnType<typeof setTimeout> | null = null;
  function showCopyMsg(text: string, isErr: boolean): void {
    const node = ui.copyMsg;
    node.textContent = text;
    if (isErr) node.setAttribute("data-err", ""); else node.removeAttribute("data-err");
    node.hidden = false;
    if (copyMsgTimer) clearTimeout(copyMsgTimer);
    copyMsgTimer = setTimeout(() => { node.hidden = true; }, 2500);
  }

  // imagePng decrypts a handout picture and re-encodes it as PNG — the one image
  // type both Chrome and Firefox accept on the clipboard. The attachment is very
  // likely a WebP (that is what the upload offers), which neither will take.
  async function imagePng(name: string): Promise<Blob> {
    const card = openCardCard();
    if (!card) throw new Error("карточка не открыта");
    const blob = await deps.attachments.imageBlob(card.id, name);
    if (!blob) throw new Error("картинка не найдена среди вложений");
    if (blob.type === "image/png") return blob;
    const bmp = await createImageBitmap(blob);
    const canvas = document.createElement("canvas");
    canvas.width = bmp.width;
    canvas.height = bmp.height;
    canvas.getContext("2d")!.drawImage(bmp, 0, 0);
    return await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("не удалось перекодировать"))), "image/png");
    });
  }

  // runCopy performs one target. The image is written twice over because the
  // browsers disagree: Safari only honours a write issued in the click's own turn,
  // so the ClipboardItem gets a promise; Firefox refuses a promise outright, so the
  // retry hands it the decoded picture (its user activation is a time window, not
  // one task). Both failing reports the FIRST error — the real one.
  async function runCopy(t: CopyTarget): Promise<void> {
    if (!t.image) { await copyText(t.text || ""); return; }
    if (typeof ClipboardItem === "undefined" || !navigator.clipboard?.write) {
      throw new Error("браузер не умеет копировать картинки");
    }
    const png = imagePng(t.image);
    try {
      await navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
    } catch (err) {
      const blob = await png; // an unusable picture raises its own error, which is the real one
      await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]).catch(() => { throw err; });
    }
  }

  function copyAndReport(t: CopyTarget): void {
    void runCopy(t)
      .then(() => showCopyMsg(`Скопировано: ${t.label.toLowerCase()}`, false))
      .catch((err) => showCopyMsg("Не удалось скопировать: " + errMsg(err), true));
  }

  // One button, because a card usually has exactly one thing worth copying. When
  // it has more — a раздатка, or the legs of a блиц — they are separate pastes
  // (issue #45), so the button opens the list instead of guessing.
  ui.copy.addEventListener("click", () => {
    const card = openCardCard();
    if (!card) return;
    captureDraft(); // copy what is on screen, not what was last saved
    const targets = xyChgk.copyTargets(versionDesc(), deps.questionNumberFor(card));
    if (targets.length === 1) { copyAndReport(targets[0]); return; }
    deps.popupMenu(ui.copy, targets.map((t) => ({ label: t.label, onClick: () => copyAndReport(t) })));
  });

  // hideCard is the card's teardown, run by the overlay stack when the card is
  // dismissed — by ↩️, Escape, Android's back button or the backdrop, all of
  // which are the same gesture now. If the card was opened from a list preview,
  // that preview is restored (pushing its own stack entry) scrolled to the same
  // question, so going back once more leaves it for the board.
  async function hideCard(): Promise<void> {
    const ret = cardReturn; // capture before the reset below clears it
    stopReadTracking();
    cardOverlay.hidden = true;
    openCardId = null;
    deps.onOpenCard?.(null);
    freshCard = false;
    cardReturn = null;
    cardView = "";
    cardFieldReaders = null;
    if (!ret || ret.listId == null) return;
    const list = state().lists.find((l) => l.id === ret.listId);
    if (!list) return;
    await deps.preview.previewList(list, ret.group);
    if (previewOverlay.hidden) return; // guard against a close during the await
    const node = ui.listPreview.body.querySelector(`[data-card-id="${ret.cardId}"]`);
    if (node) node.scrollIntoView({ block: "center" });
  }

  // closeCard asks to dismiss; the stack runs hideCard once the unsaved-changes
  // gate (confirmLeaveCard) is satisfied.
  function closeCard(): void { overlayStack.pop(); }

  // dismissCard closes a card that no longer exists — deleted, or moved to
  // another board. There is nothing left to save, so the dirty gate is skipped.
  function dismissCard(): void {
    draft.open("", null, null); // clean baseline: nothing to prompt about
    openCardId = null; // and no alias flush onto a card that is gone
    deps.onOpenCard?.(null);
    overlayStack.pop();
  }

  // ---- leaving a dirty card ----
  // Every exit used to discard unsaved edits without a word. Now each one — ↩️,
  // Escape, Android back, the backdrop, and the ← / → walk — asks first. The
  // prompt is deliberately NOT on the overlay stack: it runs *during* the
  // stack's dismissal, and pushing it there would recurse.
  const dirtyOverlay = ui.dirty.overlay;
  let dirtyAnswer: ((leave: boolean) => void) | null = null;

  function settleDirty(leave: boolean): void {
    const answer = dirtyAnswer;
    dirtyAnswer = null;
    dirtyOverlay.hidden = true;
    if (answer) answer(leave);
  }

  // confirmLeaveCard resolves true when the card may be left. A clean card never
  // prompts; a failed save keeps you on the card with the error showing.
  function confirmLeaveCard(): Promise<boolean> {
    captureDraft(); // the active view's edits count, even if the caret left it
    // A still-pending alias write goes out now, unawaited: its timer would fire
    // after the card closed, with nothing left to save against. Only when
    // pending — clicking ✕ blurs the input first, and that save is still in
    // flight, so its baseline has not moved and this would send it twice.
    if (aliasTimer) void saveAlias();
    // A typed-but-unsent comment is unsaved work too: leaving raises the same
    // prompt, where Сохранить posts it.
    if (!draft.contentDirty(false) && !deps.timeline.commentDraft()) return Promise.resolve(true);
    ui.dirty.message.textContent = "";
    dirtyOverlay.hidden = false;
    return new Promise<boolean>((resolve) => { dirtyAnswer = resolve; });
  }

  ui.dirty.save.addEventListener("click", async () => {
    if (draft.contentDirty(false)) {
      const saved = await saveCard();
      if (!saved) { ui.dirty.message.textContent = "Не удалось сохранить — карточка осталась открытой."; return; }
    }
    if (deps.timeline.commentDraft() && !(await deps.timeline.postComment())) {
      ui.dirty.message.textContent = "Не удалось отправить комментарий — карточка осталась открытой.";
      return;
    }
    settleDirty(true);
  });
  ui.dirty.discard.addEventListener("click", () => { deps.timeline.clearCommentDraft(); settleDirty(true); });
  ui.dirty.cancel.addEventListener("click", () => { settleDirty(false); });
  dirtyOverlay.addEventListener("pointerdown", (e) => { if (e.target === dirtyOverlay) settleDirty(false); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && dirtyAnswer) { e.stopPropagation(); settleDirty(false); }
  });

  // ---- walking the list with ← / → ----
  // Reviewing a package means opening each card in turn; the arrows do that
  // without going back to the board. They only fire when the caret is not in a
  // field, so typing is never hijacked — and an existing card opens in the
  // read-only Просмотр view, where that is always true.
  function typingTarget(el: EventTarget | null): boolean {
    if (!(el instanceof HTMLElement)) return false;
    return el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName);
  }

  // walkScope is the run of cards the arrows move through: the open card's list,
  // or — when that list belongs to a group — the whole group in board order, the
  // same scope its numbering, preview and export already use.
  function walkScope(card: BoardCard): BoardCard[] {
    const list = state().lists.find((l) => l.id === card.listId);
    if (!list) return deps.cardsOf(card.listId);
    const lists = list.groupId != null
      ? state().lists.filter((l) => l.groupId === list.groupId && l.type !== "test").sort(byRank)
      : [list];
    return lists.flatMap((l) => deps.cardsOf(l.id));
  }

  async function walkCard(step: number): Promise<void> {
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return;
    const scope = walkScope(card);
    const next = scope[scope.findIndex((c) => c.id === card.id) + step];
    if (!next) return; // the ends of the walk are ends, not wrap-arounds
    if (!(await confirmLeaveCard())) return;
    await openCard(next, { returnTo: cardReturn });
  }

  document.addEventListener("keydown", (e) => {
    if (cardOverlay.hidden || dirtyAnswer) return;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    if (e.metaKey || e.ctrlKey || e.altKey || typingTarget(e.target)) return;
    e.preventDefault();
    void walkCard(e.key === "ArrowRight" ? 1 : -1);
  });

  cardCloseBtn.addEventListener("click", closeCard);
  ui.link.addEventListener("click", () => { void copyCardLink(); });
  cardOverlay.addEventListener("pointerdown", (e) => { if (e.target === cardOverlay) closeCard(); });

  cardSaveBtn.addEventListener("click", () => { void saveCard(); });

  // saveCard persists the open card's 4s content, reporting whether the write
  // landed so the unsaved-changes prompt knows not to leave on a failure.
  async function saveCard(): Promise<boolean> {
    captureDraft(); // fold the active view's edits into draft.desc / draft.meta
    const msg = cardMessageEl;
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return false;
    const newDesc = draft.desc;
    const newMeta = draft.normalizedMeta();
    // The alias is deliberately absent here — it autosaves (saveAlias).
    msg.textContent = "";
    try {
      const dk = mustDK();
      const body: OpBody = { description_enc: await xyCrypto.encField(dk, newDesc) };
      if (newDesc !== card.desc) {
        body.desc_event_enc = await xyCrypto.encField(dk, JSON.stringify({ before: card.desc, after: newDesc }));
      }
      // Persist handout-gen settings (field #10) when they changed: "" clears them.
      if (newMeta !== (card.handoutMeta || null)) {
        body.handout_meta_enc = newMeta ? await xyCrypto.encField(dk, newMeta) : "";
      }
      await verbs.patch("patchCard", `/api/cards/${card.id}`, body);
      card.desc = newDesc;
      card.handoutMeta = newMeta;
      draft.commitContent(newDesc, newMeta);
      deps.render();
      await deps.timeline.load(card.id);
      cardDescEl.value = versionDesc();
      // The rendered preview is itself the confirmation that the edits landed.
      setCardView("preview");
      msg.textContent = "Карточка сохранена.";
    } catch (err) { msg.textContent = errMsg(err); return false; }
    return true;
  }

  // Cmd/Ctrl-Enter saves from either edit view (textarea or structured fields).
  onCmdEnter(cardDescEl, () => cardSaveBtn.click());
  onCmdEnter(cardFieldsEl, () => cardSaveBtn.click());

  // Re-evaluate the save button on every edit. Typing fires "input"; the Поля
  // view's +/× field pills and the tool buttons change the draft via clicks, which
  // bubble here after their own handlers have run.
  for (const node of [ui.desc, ui.fields]) {
    node.addEventListener("input", refreshSaveState);
    node.addEventListener("click", refreshSaveState);
  }

  // ---- the alias, which saves itself ----
  // Its own column, its own PATCH: coupling it to «Сохранить» meant no alias
  // could be saved from the read-only Просмотр. "" clears it (optBlob).
  const ALIAS_SAVE_DELAY = 1000;
  let aliasTimer: ReturnType<typeof setTimeout> | null = null;
  function cancelAliasSave(): void {
    if (aliasTimer) clearTimeout(aliasTimer);
    aliasTimer = null;
  }

  async function saveAlias(): Promise<void> {
    cancelAliasSave();
    const card = openCardCard();
    if (!card) return;
    const next = normalizeAlias(cardAliasEl.value);
    if (!draft.aliasDirty(next)) return;
    setStatus("saving");
    try {
      const body: OpBody = { alias_enc: next ? await xyCrypto.encField(mustDK(), next) : "" };
      await verbs.patch("patchCard", `/api/cards/${card.id}`, body);
      card.alias = next;
      // Only while this card is still the open one: an exit flush resolves after
      // the ←/→ walk has opened the next card, and the draft is that card's now.
      if (openCardId === card.id) draft.commitAlias(next);
      deps.render(); // the board card previews the alias
      setStatus("saved");
    } catch (err) {
      setStatus("error");
      cardMessageEl.textContent = errMsg(err);
    }
  }

  cardAliasEl.addEventListener("input", () => {
    cancelAliasSave();
    aliasTimer = setTimeout(() => { void saveAlias(); }, ALIAS_SAVE_DELAY);
  });
  cardAliasEl.addEventListener("blur", () => { void saveAlias(); });
  cardAliasEl.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    cardAliasEl.blur(); // saves via blur, and drops the on-screen keyboard
  });

  ui.del.addEventListener("click", async () => {
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card || !confirm("Удалить карточку?")) return;
    try {
      await verbs.del("deleteCard", `/api/cards/${card.id}`);
      const st = state();
      st.cards = st.cards.filter((c) => c.id !== card.id);
      deps.forgetCardLabels([card]);
      dismissCard();
      deps.render();
    } catch (err) { cardMessageEl.textContent = errMsg(err); }
  });

  return {
    addCard,
    openCard,
    closeCard,
    openCardId: () => openCardId,
    copyPlain: copyText,
    maybeOpenDeepLink,
    highlightComment,
    copyCommentLink,
  };
}
