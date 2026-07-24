// carddetail.ts — the card-detail modal, lifted out of board.js into a typed
// create(deps) factory: create mode (addCard/addTestCard), the Просмотр/Поля/
// Текст views over a shared draft (carddraft.js), the field builders with their
// hand-drawn suggest dropdowns, the edit-tools row (ударение / типограф / →.4s),
// direct links + deep links, open/close/back, read tracking, move/copy
// (including cardCopyBody/copyCardExtras/loadMoveBoard, which the board's list
// move/copy reuses through this factory's API), copy-question-for-test, and the
// alias's own save path. The board injects what it owns (live state, DK,
// mutation verbs, render, the preview/attachments/read-marker seams and
// questionNumberFor, which lives with the board's group-numbering logic); the
// timeline module is injected as `timeline` — the orchestrator creates the
// timeline first and wires its `card` seam back to this factory's API.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { xyChgk } from "./chgk.js";
import { xyCardDraft } from "./carddraft.js";
import { xyRank } from "./rank.js";
import { byRank, rankForSlot } from "./dragrank.js";
import type { BoardKeymeta, DataKey } from "./crypto.js";
import type { CardFields, Handout, Tester, TesterLike } from "./chgk.js";
import type { BoardCard, BoardLabel, BoardList, Snapshot, UnreadFlags } from "./unlock.js";
import type { OpBody } from "./store.js";
import type { CardEvent } from "./timeline.js";

const { fetchJSON, jpost, jput, jdelete, el } = xyApp;
const { keyBetween } = xyRank;

// ---- pure helpers (exported for tests and for the board) ----

// nowStamp is the local stand-in for cards.created_at on a card this session
// just made: it is not in a snapshot yet, and offline it may not reach the
// server for a while. The next snapshot replaces it with the server's value.
export const nowStamp = (): string => new Date().toISOString();

// testTitle renders a test card's derived title from its JSON description.
export function testTitle(desc: string): string {
  try {
    const m = xyChgk.parseTestCard(desc);
    const head = m.title ? `${m.title} · ${m.datetime}` : m.datetime;
    const players = m.testers.filter((t) => t.type === "player").length;
    const teams = m.testers.filter((t) => t.type === "team").length;
    const parts: string[] = [];
    if (players) parts.push(`${players} игр.`);
    if (teams) parts.push(`${teams} ком.`);
    return `🗓️ ${head}${parts.length ? " · " + parts.join(", ") : ""}`;
  } catch (_) { return "тест-сессия"; }
}

// The 4s skeleton a new question's Текст view opens on: question / answer /
// comment / source / author, the blocks a question is expected to carry. The
// "@" line pre-fills the user's default author (a /profile setting) — saving
// an otherwise untouched stub is allowed and creates a card with just that.
export function questionStub(defaultAuthor: string): string {
  return "? \n! \n/ \n^ \n@ " + (defaultAuthor || "");
}

// testerSummaryLine is the shareable "Вопросы тестировали: …" line — players
// sorted by surname, teams alphabetically, both deduped (chgk.js
// testerCopyText), terminated with a period. "" when there are no testers.
export function testerSummaryLine(testers: ReadonlyArray<TesterLike>): string {
  const t = xyChgk.testerCopyText(testers);
  return t ? t + "." : "";
}

// ---- injected seams ----

// The slice of the board's live state this module reads and mutates in place.
export interface CardDetailState {
  name: string;
  lists: BoardList[];
  cards: BoardCard[];
  labels: BoardLabel[];
  cardLabels: Record<string, number[]>;
  unread: Record<string, UnreadFlags>;
  defaultAuthor: string;
}

// The board's mutation wrappers (board.js:20-24) — every board mutation flows
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
}

export interface CardDetailDeps {
  boardId: number;
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
  cleanupTestLabels(cards: BoardCard[]): Promise<void>;
  preview: PreviewSeam;
  attachments: AttachmentsSeam;
  readMarkers: ReadMarkerSeam;
  timeline: TimelineSeam;
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
export interface MoveLabel { id: number; kind?: string; name: string; color: string }
export interface MoveCtx {
  boardId: number;
  dk: DataKey;
  lists: Array<{ id: number; title: string; rank: string }>;
  cardsByList: Map<number, Array<{ id: number; rank: string }>>;
  labels: MoveLabel[];
}

export interface CardDetail {
  addCard(list: BoardList): void;
  openCard(card: BoardCard, opts?: { returnTo?: CardReturn | null }): Promise<void>;
  closeCard(): void;
  openCardId(): number | null;
  maybeOpenDeepLink(): void;
  highlightComment(eventId: number): void;
  copyCommentLink(eventId: number): Promise<void>;
  testerSummary(list: BoardList): string;
  copyTesterList(list: BoardList): Promise<void>;
  // Reused by the board's list move/copy (board.js's «Переместить список…»).
  loadMoveBoard(bid: number): Promise<MoveCtx>;
  cardCopyBody(src: BoardCard, rank: string, key: DataKey): Promise<OpBody>;
  copyCardExtras(srcCardId: number, targetDk: DataKey, newCardId: number): Promise<void>;
  reconcileLabels(srcCardId: number, targetBid: number, targetDk: DataKey, targetLabels: MoveLabel[]): Promise<number[]>;
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
  authors: FieldReader<string[] | null>;
  hndt: FieldReader<string | null>;
}

interface TesterRowEl extends HTMLElement { _read?: () => Tester }

interface MoveBoardItem { id: number; name?: string; name_enc?: string | null; schema_version?: number }

interface AttachmentDTO {
  id: number;
  filename_enc: string;
  mime: string;
  lossless?: boolean;
  is_excerpt?: boolean;
}

export function createCardDetail(deps: CardDetailDeps): CardDetail {
  function byId<T extends HTMLElement = HTMLElement>(id: string): T {
    const node = document.getElementById(id);
    if (!node) throw new Error(`page is missing #${id}`);
    return node as T;
  }
  function q(sel: string): HTMLElement {
    const node = document.querySelector<HTMLElement>(sel);
    if (!node) throw new Error(`page is missing ${sel}`);
    return node;
  }
  const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

  const { boardId, verbs, setStatus } = deps;
  const state = deps.getState;
  function mustDK(): DataKey {
    const dk = deps.getDK();
    if (!dk) throw new Error("нет ключа доски");
    return dk;
  }

  const cardOverlay = byId("cardOverlay");
  const cardDescEl = byId<HTMLTextAreaElement>("cardDesc");
  const cardAliasEl = byId<HTMLInputElement>("cardAlias");
  const cardKindEl = byId<HTMLSelectElement>("cardKind");
  const cardMessageEl = byId("cardMessage");
  const cardFieldsEl = byId("cardFields");
  const cardSaveBtn = byId<HTMLButtonElement>("cardSave");
  const cardAliasSaveBtn = byId<HTMLButtonElement>("cardAliasSave");
  const moveBoardSel = byId<HTMLSelectElement>("moveBoard");
  const moveListSel = byId<HTMLSelectElement>("moveList");
  const movePosSel = byId<HTMLSelectElement>("movePos");
  const cardDetailBox = q(".card-detail");
  const previewOverlay = byId("previewOverlay");

  // ---- card detail state ----
  let openCardId: number | null = null;
  let pendingList: BoardList | null = null; // set while composing a brand-new (unsaved) card
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
  const tabBtn = (v: string): HTMLButtonElement => byId<HTMLButtonElement>("cardTab" + v[0].toUpperCase() + v.slice(1));

  // ---- add card (create mode) ----
  // addCard opens the card detail in "create mode" — only the description editor
  // is shown (the card isn't persisted until you save a description, so we never
  // create empty cards). Labels/attachments/move/timeline appear only when editing
  // an existing card.
  function addCard(list: BoardList): void {
    if (list.type === "test") { void addTestCard(list); return; }
    pendingList = list;
    openCardId = null;
    cardView = "";
    cardFieldReaders = null;
    draft.blank();
    deps.attachments.clearImageNames();
    cardDescEl.value = "";
    cardAliasEl.value = "";
    cardKindEl.hidden = false;
    cardKindEl.value = "question";
    cardMessageEl.textContent = "";
    cardDetailBox.classList.add("creating");
    byId("cardCopy").hidden = true; // no number/desc yet
    cardOverlay.hidden = false;
    // New card: no preview yet — open straight into the structured editor.
    lastEditView = "fields";
    setCardView("fields");
    cardDescEl.focus();
  }

  // addTestCard: a test card's "description" is JSON {datetime, title, testers}
  // (see chgk.js parseTestCard). Creating it also auto-creates two board labels
  // ("{dt} взяли" green / "не взяли" red) for the user to assign to questions
  // later; the tester list is edited in the card detail.
  async function addTestCard(list: BoardList): Promise<void> {
    const now = new Date();
    const pad = (n: number): string => String(n).padStart(2, "0");
    const def = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`;
    const dt = prompt("Дата и время тест-сессии (ГГГГ-ММ-ДД ЧЧ:ММ):", def);
    if (!dt) return;
    // Optional human label to tell sessions apart at a glance (e.g. "Алиев и др.").
    // Folded into the card preview and the auto-created green/red label names.
    const title = (prompt("Название тест-сессии (необязательно, напр. «Алиев и др.»):", "") || "").trim();
    const tag = title ? `${dt} ${title}` : dt;
    const existing = deps.cardsOf(list.id);
    const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
    try {
      const dk = mustDK();
      const desc = JSON.stringify({ datetime: dt, title, testers: [] });
      const res = await verbs.create("createCard", `/api/lists/${list.id}/cards`, {
        description_enc: await xyCrypto.encField(dk, desc), rank, kind: "test",
      });
      state().cards.push({ id: res.id as number, listId: list.id, kind: "test", rank, desc, handoutMeta: null, alias: null, createdAt: nowStamp() });
      // auto labels, then assign both to the new card
      const autoIds: number[] = [];
      const pairs: Array<[string, string, string]> = [["взяли", "#3aa657", "test_taken"], ["не взяли", "#dd3322", "test_missed"]];
      for (const [suffix, color, kind] of pairs) {
        const lr = await verbs.create("createLabel", `/api/boards/${boardId}/labels`, {
          name_enc: await xyCrypto.encField(dk, `${tag} ${suffix}`),
          color_enc: await xyCrypto.encField(dk, color),
          kind,
        });
        state().labels.push({ id: lr.id as number, kind, name: `${tag} ${suffix}`, color });
        autoIds.push(lr.id as number);
      }
      await verbs.put("setCardLabels", `/api/cards/${res.id}/labels`, { label_ids: autoIds });
      state().cardLabels[res.id as number] = autoIds.slice();
      deps.render();
    } catch (err) { setStatus("error"); }
  }

  // setTestDetailTitle shows the test session's "datetime · title" heading above
  // the Поля/Текст switcher (test cards have no kind selector to fill that slot).
  function setTestDetailTitle(card: BoardCard): void {
    const node = byId("cardDetailTitle");
    const m = xyChgk.parseTestCard(card.desc);
    node.textContent = m.title ? `${m.datetime} · ${m.title}` : m.datetime;
    node.hidden = false;
  }

  // listTesters gathers the testers from every test card in a list (flattened).
  function listTesters(list: BoardList): Tester[] {
    const all: Tester[] = [];
    for (const c of deps.cardsOf(list.id)) {
      if (c.kind !== "test") continue;
      all.push(...xyChgk.parseTestCard(c.desc).testers);
    }
    return all;
  }

  // testerSummary is the shareable "Вопросы тестировали: …" line for a test list.
  function testerSummary(list: BoardList): string {
    return testerSummaryLine(listTesters(list));
  }

  // copyTesterList copies the test list's tester summary to the clipboard silently.
  async function copyTesterList(list: BoardList): Promise<void> {
    const text = testerSummary(list);
    if (!text) return;
    try { await copyText(text); } catch (_) {}
  }

  // fitTextarea grows a textarea to fit its content so the user never scrolls
  // inside it (CSS min-height still sets the floor). scrollHeight is 0 while the
  // element is display:none, so callers fit on render / when a field is revealed.
  function fitTextarea(ta: HTMLTextAreaElement): void {
    ta.style.height = "auto";
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
    if (pendingList) return cardKindEl.value || "question";
    const c = openCardCard();
    return c ? c.kind : "question";
  }
  function fieldsAvailable(): boolean { return draftKind() === "question"; }
  function isTestCard(): boolean { return draftKind() === "test"; }

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
  function suggestWrap(input: HTMLInputElement, values: string[], onPick?: (v: string) => void): HTMLElement {
    const menu = el("div", { class: "suggest-menu", hidden: true });
    const wrap = el("div", { class: "suggest-wrap" }, input, menu);
    let items: string[] = [], active = -1;
    const close = (): void => { menu.hidden = true; menu.replaceChildren(); items = []; active = -1; };
    const pick = (v: string): void => { input.value = v; close(); if (onPick) onPick(v); };
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
    input.addEventListener("keydown", (e) => {
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
    // The alias is not a 4s field and belongs to no view: its input lives above
    // the tabs, so it is read on every capture, whichever view is active (and for
    // test cards too, which return early below).
    draft.alias = cardAliasEl.value.trim() || null;
    if (isTestCard()) {
      // Test cards keep their canonical JSON ({datetime,title,testers}) in
      // draft.desc; both views edit only the testers list (datetime/title are set
      // at creation), so re-read them from draft.desc and fold the rows back in.
      const cur = xyChgk.parseTestCard(draft.desc);
      let testers: Tester[] | null = null;
      if (cardView === "text") testers = xyChgk.testersFromText(cardDescEl.value);
      else if (cardView === "fields" && testerReaders) testers = readTesterRows();
      if (testers) draft.desc = xyChgk.serializeTestCard({ datetime: cur.datetime, title: cur.title, testers });
      return;
    }
    if (cardView === "text") draft.desc = cardDescEl.value;
    else if (cardView === "fields" && cardFieldReaders) {
      const r = readCardFields(cardFieldReaders);
      draft.desc = r.desc;
      draft.meta = r.meta;
    }
  }

  // refreshSaveState enables the save button only when the draft differs from what
  // was last persisted, so it's obvious whether the current edits are applied. A
  // new (unsaved) card has no baseline — save stays enabled while it has content.
  function refreshSaveState(): void {
    captureDraft();
    const btn = cardSaveBtn;
    // The alias is NOT part of this: it is a separate column with its own save
    // button (refreshAliasState). «Сохранить» is about the card's 4s content —
    // on a NEW card that content still carries the alias along (see cardSave), so
    // the alias only counts as "dirty" here while creating.
    const dirty = draft.contentDirty(!!pendingList);
    btn.disabled = !dirty;
    // Просмотр is read-only, so nothing can be dirty there; the button hides.
    btn.hidden = cardView === "preview" && !dirty;
    // A stale "Карточка сохранена." next to a re-enabled button reads as a lie.
    if (dirty && cardMessageEl.textContent === "Карточка сохранена.") cardMessageEl.textContent = "";
    refreshAliasState();
  }

  // refreshAliasState enables the alias's own save button only when the input
  // differs from what is persisted. On a card being created there is no alias
  // column yet (the button is data-edit-only, hidden), so this is a no-op then.
  function refreshAliasState(): void {
    if (pendingList) return;
    const cur = cardAliasEl.value.trim() || null;
    cardAliasSaveBtn.disabled = !draft.aliasDirty(cur);
  }

  function setCardView(view: string): void {
    captureDraft();
    const test = isTestCard();
    // Test cards offer Поля (tester rows) + Текст (plaintext) but no Просмотр;
    // other non-question cards have no Поля, so they fall back to Текст.
    if (test && view === "preview") view = lastEditView === "text" ? "text" : "fields";
    else if (view === "fields" && !fieldsAvailable() && !test) view = "text";
    cardView = view;
    if (view !== "preview") lastEditView = view;
    byId("cardViewPreview").hidden = view !== "preview";
    byId("cardViewFields").hidden = view !== "fields";
    byId("cardViewText").hidden = view !== "text";
    for (const t of CARD_TABS) tabBtn(t).classList.toggle("active", t === view);
    // The raw tab shows 4s for questions but a plaintext tester list for test
    // cards — «Формат 4s» would be a lie there, so it falls back to «Текст».
    tabBtn("text").textContent = test ? "Текст" : "Формат 4s";
    tabBtn("fields").hidden = !fieldsAvailable() && !test;
    tabBtn("preview").hidden = !!pendingList || test;
    byId("cardViewTabs").hidden = false;
    // (the save button's visibility is refreshSaveState's alone — see the end of
    // this function — because it depends on more than the view)
    // The tools edit text, so they follow the two edit views. Both rewriting tools
    // are question-only: a test card's draft is JSON (its Текст view is a tester
    // list), and →.4s additionally needs the raw 4s editor it types into.
    byId("cardEditTools").hidden = view === "preview";
    byId("cardTypo").hidden = test;
    byId("cardTo4s").hidden = view !== "text" || !fieldsAvailable();
    byId("cardDescLabel").textContent = test ? "Тестировали (- игрок, -T команда)" : "Описание";
    if (view === "text") {
      const ta = cardDescEl;
      ta.value = test ? xyChgk.testersToText(xyChgk.parseTestCard(draft.desc).testers) : draft.desc;
      // A brand-new question opens on an empty editor, which says nothing about what
      // the format wants. Seed the markers so the writer fills in blanks instead of
      // recalling 4s from memory; the caret lands after the "?". "Empty" includes
      // the author-only draft an untouched Поля view composes when a default
      // author is set — that's still a blank form.
      const bare = ta.value.trim();
      const authorOnly = state().defaultAuthor && bare === "@ " + state().defaultAuthor;
      if (!test && pendingList && (!bare || authorOnly)) {
        ta.value = questionStub(state().defaultAuthor);
        ta.focus();
        ta.setSelectionRange(2, 2);
      }
      fitTextarea(ta);
    } else if (view === "fields") { if (test) renderTesterFields(); else renderCardFields(); }
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
  function buildField(label: string, kind: "area" | "input", initial: string | null | undefined, opts: { muted?: boolean; open?: boolean } = {}): FieldReader<string | null> {
    const wrap = el("div", { class: "fld" + (opts.muted ? " fld-muted" : "") });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ " + label, title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label }), rmBtn);
    const input = (kind === "area"
      ? el("textarea", { class: "card-desc fld-input", spellcheck: "false", rows: "1" })
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
    const body = el("div", { class: "fld-body" }, toggle, ta, sel);
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
      sel.hidden = mode !== "image";
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
    const addRow = (val: string): HTMLInputElement => {
      const inp = el("input", { class: "input fld-row-input", type: "text", value: val || "" }) as HTMLInputElement;
      const rrm = el("button", { class: "fld-row-rm", type: "button", text: "×", title: "Удалить строку" });
      const row = el("div", { class: "fld-row" }, suggestWrap(inp, suggestions), rrm);
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
    return { node: wrap, read: () => (present ? [...rows.querySelectorAll<HTMLInputElement>(".fld-row-input")].map((i) => i.value) : null) };
  }

  // buildAuthorsField: a tag input (like labels) seeded with autocomplete from the
  // board's existing authors; free text adds a new author.
  function buildAuthorsField(initial: string[] | null, suggestions: string[]): FieldReader<string[] | null> {
    const wrap = el("div", { class: "fld" });
    const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Автор", title: "Добавить поле" });
    const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Автор" }), rmBtn);
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
      if (!present) return null;
      const v = inp.value.trim();
      return v ? [...tagSet, v] : tagSet.slice();
    } };
  }

  // renderCardFields rebuilds the Поля editor from the current draft (and handout
  // settings). The last field (handout-gen markup) binds to draft.meta, not the 4s.
  function renderCardFields(): void {
    const f = xyChgk.splitFields(draft.desc);
    // A brand-new card pre-fills the user's default author (a /profile setting)
    // and opens the two fields every question has, ready to type into.
    const fresh = !!pendingList && !draft.desc.trim();
    if (fresh && f.authors == null && state().defaultAuthor) f.authors = [state().defaultAuthor];
    cardFieldsPre = f.preMarkup;
    cardFieldsExtra = f.extra;
    const box = cardFieldsEl;
    box.replaceChildren();
    const R: FieldReaders = {
      handout: buildHandoutField(f.handout),
      question: buildField("Текст вопроса", "area", f.question, { open: fresh }),
      answer: buildField("Ответ", "area", f.answer, { open: fresh }),
      zachet: buildField("Зачёт", "input", f.zachet),
      nezachet: buildField("Незачёт", "input", f.nezachet),
      comment: buildField("Комментарий", "area", f.comment),
      sources: buildSourcesField(f.sources, boardSources()),
      authors: buildAuthorsField(f.authors, boardAuthors()),
      hndt: buildField("Доп. разметка для генерации раздаток", "area", draft.meta, { muted: true }),
    };
    for (const k of ["handout", "question", "answer", "zachet", "nezachet", "comment", "sources", "authors", "hndt"] as const) box.append(R[k].node);
    // Size pre-filled fields now they're in the live DOM (scrollHeight is 0 while
    // detached, so the fit during buildField is a no-op for visible content).
    for (const ta of box.querySelectorAll("textarea")) fitTextarea(ta);
    cardFieldReaders = R;
  }

  // readCardFields collapses the Поля editor back into a 4s description + handout
  // settings, preserving the pre-question and unmodelled blocks captured at render time.
  function readCardFields(R: FieldReaders): { desc: string; meta: string | null } {
    const rec: Partial<CardFields> = {
      preMarkup: cardFieldsPre,
      handout: R.handout.read(),
      question: R.question.read(),
      answer: R.answer.read(),
      zachet: R.zachet.read(),
      nezachet: R.nezachet.read(),
      comment: R.comment.read(),
      sources: R.sources.read(),
      authors: R.authors.read(),
      extra: cardFieldsExtra,
    };
    return { desc: xyChgk.composeFields(rec), meta: R.hndt.read() };
  }

  // ---- test card "Поля" editor: one row per tester, each a name input + a
  // игрок/команда toggle (wysiwyg-style; the Текст view is the plaintext mirror).
  let testerReaders: (() => Tester[]) | null = null; // () => [{text,type}] for the current tester rows

  function renderTesterFields(): void {
    const box = cardFieldsEl;
    box.replaceChildren();
    const m = xyChgk.parseTestCard(draft.desc);
    // fld-wide: .card-fields wraps pills side by side now, so a stacked block
    // must claim the full row explicitly.
    const wrap = el("div", { class: "fld fld-wide" });
    const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Тестировали" }));
    const rows = el("div", { class: "fld-rows" });
    const addRow = (t: TesterLike | null): HTMLInputElement => {
      const seg = el("div", { class: "seg tester-seg" });
      const bP = el("button", { class: "seg-btn", type: "button", text: "игрок" });
      const bT = el("button", { class: "seg-btn", type: "button", text: "команда" });
      let type: Tester["type"] = t && t.type === "team" ? "team" : "player";
      const syncSeg = (): void => { bP.classList.toggle("active", type === "player"); bT.classList.toggle("active", type === "team"); };
      bP.addEventListener("click", () => { type = "player"; syncSeg(); });
      bT.addEventListener("click", () => { type = "team"; syncSeg(); });
      seg.append(bP, bT); syncSeg();
      const inp = el("input", { class: "input fld-row-input", type: "text", value: (t && t.text) || "", placeholder: "имя…" }) as HTMLInputElement;
      const rrm = el("button", { class: "fld-row-rm", type: "button", text: "×", title: "Удалить строку" });
      const row = el("div", { class: "fld-row tester-row" }, seg, inp, rrm) as TesterRowEl;
      rrm.addEventListener("click", () => row.remove());
      row._read = () => ({ text: inp.value, type });
      rows.append(row);
      return inp;
    };
    (m.testers.length ? m.testers : [{ text: "", type: "player" as const }]).forEach((t) => addRow(t));
    const rowAdd = el("button", { class: "input fld-add-row", type: "button", text: "+ тестер" });
    rowAdd.addEventListener("click", () => addRow({ text: "", type: "player" }).focus());
    wrap.append(head, rows, rowAdd);
    box.append(wrap);
    testerReaders = () => [...rows.querySelectorAll<TesterRowEl>(".tester-row")].map((r) => (r._read as () => Tester)());
  }

  function readTesterRows(): Tester[] { return testerReaders ? testerReaders() : []; }

  // renderCardPreview renders the open card's draft the docx way (single-card
  // version of the list preview). Read-only; double-click jumps back to editing.
  async function renderCardPreview(): Promise<void> {
    const body = byId("cardPreviewBody");
    if (!draft.desc.trim()) { body.replaceChildren(el("p", { class: "pv-empty", text: "Пусто." })); return; }
    const c = openCardCard();
    const card: PreviewCardLike = { id: c ? c.id : 0, kind: draftKind(), desc: draft.desc, listId: c ? c.listId : (pendingList ? pendingList.id : 0) };
    const number = card.kind === "question" ? deps.questionNumberFor(card) : null;
    const reqId = openCardId;
    const screen = byId<HTMLInputElement>("cardPreviewScreen").checked;
    const imgMap = new Map<string, string>();
    body.replaceChildren(deps.preview.renderPreviewCard(card, number, imgMap, screen));
    await deps.preview.resolveImages([card], deps.preview.imageRefs([card]), (name, url) => {
      imgMap.set(name, url);
      if (cardView === "preview" && openCardId === reqId) deps.preview.fillPreviewImages(body, imgMap);
    });
  }

  // Tab clicks + the preview screen toggle + double-click-to-edit.
  for (const v of CARD_TABS) tabBtn(v).addEventListener("click", () => setCardView(v));
  byId("cardPreviewScreen").addEventListener("change", () => { if (cardView === "preview") void renderCardPreview(); });
  byId("cardPreviewBody").addEventListener("dblclick", () => setCardView(lastEditView));

  // ---- edit tools (the row under the tabs) ----
  // ударение types into the field the user was editing, which by the time the click
  // lands is no longer the focused one (a button takes focus on mousedown) — so
  // remember the last field the caret was in. The Поля view rebuilds its inputs on
  // every view switch, hence the isConnected check when using it.
  let lastEditField: HTMLTextAreaElement | HTMLInputElement | null = null;
  for (const panel of ["cardViewFields", "cardViewText"]) {
    byId(panel).addEventListener("focusin", (e) => {
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
  byId("cardInsStress").addEventListener("click", () => {
    const f = editField();
    if (f) insertAtCaret(f, "́");
  });

  // типограф runs the WHOLE card — not just the focused field — through chgksuite's
  // typography pass (/api/typo: quotes → «ёлочки», hyphen runs → em dashes,
  // non-breaking spaces and hyphens, percent-escapes decoded back into the words a
  // pasted wiki link stands for). The draft is 4s either way, so Поля and Текст
  // send the same text; only where the result lands differs. Online-only, like →.4s:
  // the pass is the Go port on the server (it never keeps the text).
  byId("cardTypo").addEventListener("click", async () => {
    captureDraft();
    if (!draft.desc.trim()) return;
    if (!xySync.requireOnline("Типографика доступна только онлайн.")) return;
    setStatus("saving");
    try {
      const res = await fetch("/api/typo", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: draft.desc }),
      });
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
      const { text } = (await res.json()) as { text: string };
      setStatus("saved");
      draft.desc = text;
      // In Текст the user is looking at the raw 4s, so type it back into the editor
      // (undo intact); in Поля the fields are a view of the draft, so rebuild them.
      if (cardView === "text") replaceField(cardDescEl, text);
      else renderCardFields();
    } catch (err) {
      setStatus("error");
      alert("Не удалось применить типографику: " + errMsg(err));
    }
  });

  // →.4s runs the raw editor's content through the server's chgk text parser — the
  // .docx import pipeline minus the .docx — so a question pasted as plain prose
  // ("Вопрос 1: … Ответ: … Автор: …") becomes marked-up 4s. The parse is a guess, so
  // it lands back in the editor for the user to check; nothing is saved until Save.
  // Online-only: the parser is the Go port on the server (it never keeps the text).
  byId("cardTo4s").addEventListener("click", async () => {
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

  // reflectCardInUrl keeps the address bar in sync with the open card (replaceState,
  // so it doesn't pollute history) — a refresh or copied address reopens the card.
  function reflectCardInUrl(cardId: number | null): void {
    history.replaceState(null, "", cardId ? cardUrl(cardId) : location.pathname);
  }

  // maybeOpenDeepLink runs once after the first successful board load: if the URL
  // names a card (and optionally a comment), open it.
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
    openCard(card).then(() => { if (commentId) highlightComment(commentId); }).catch(() => {});
  }

  // highlightComment scrolls a comment into view and flashes it. The timeline is
  // rendered newest-first inside the card detail; the event node carries id
  // "tlev-{eventId}".
  function highlightComment(eventId: number): void {
    const node = document.getElementById("tlev-" + eventId);
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

  async function openCard(card: BoardCard, opts: { returnTo?: CardReturn | null } = {}): Promise<void> {
    stopReadTracking(); // tear down any timer/observer left over from a previous card
    pendingList = null;
    cardReturn = opts.returnTo || null;
    openCardId = card.id;
    reflectCardInUrl(card.id);
    cardView = "";
    cardFieldReaders = null;
    const openMeta = card.handoutMeta != null ? card.handoutMeta : null;
    const openAlias = card.alias != null ? card.alias : null;
    draft.open(card.desc, openMeta, openAlias);
    cardAliasEl.value = openAlias || "";
    cardDetailBox.classList.remove("creating");
    cardDescEl.value = card.desc;
    cardMessageEl.textContent = "";
    // Kind selector: editable for ordinary cards, hidden for test cards (their
    // "kind" is fixed and their description is JSON, not 4s markup).
    const isTest = card.kind === "test";
    cardKindEl.hidden = isTest;
    if (!isTest) cardKindEl.value = card.kind || "question";
    // Test cards show their session heading in place of the (hidden) kind selector.
    if (isTest) setTestDetailTitle(card);
    else byId("cardDetailTitle").hidden = true;
    // The "copy for testing" action only makes sense for question cards (it shares
    // the numbered, screen-mode question text); hide it otherwise.
    byId("cardCopy").hidden = card.kind !== "question";
    byId("cardCopyMsg").hidden = true;
    cardOverlay.hidden = false;
    deps.renderLabelPicker(card);
    deps.paintLabels();
    lastEditView = (isTest || fieldsAvailable()) ? "fields" : "text";
    // Render the chosen view straight away so reopening a card never flashes the
    // previously-open card's content. The preview resolves its own images, so it
    // doesn't wait on the per-card loads below — which run in parallel, not
    // sequentially, to cut the total round-trip.
    setCardView(isTest ? "fields" : "preview");
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
  // so viewing the timeline clears whichever buckets are unread. Content also
  // clears after a 10s dwell on the card body itself (a secondary trigger, for the
  // reader who studies the question text without scrolling down to the лента).
  function armReadTracking(card: BoardCard): void {
    const u = state().unread[card.id] || {};
    byId("contentUnreadDot").hidden = !u.content;
    byId("commentsUnreadDot").hidden = !u.comments;

    if (u.content) {
      contentReadTimer = setTimeout(() => {
        contentReadTimer = null;
        if (openCardId === card.id) void markCardRead(card.id, { content: true });
      }, 10000);
    }

    if (u.content || u.comments) {
      const timeline = byId("timeline");
      let dwellTimer: ReturnType<typeof setTimeout> | null = null;
      commentsObserver = new IntersectionObserver((entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && entry.intersectionRatio > 0) {
            if (!dwellTimer) {
              dwellTimer = setTimeout(() => {
                if (openCardId === card.id) void markCardRead(card.id, { content: !!u.content, comments: !!u.comments });
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
    const maxId = (pred: (e: CardEvent) => boolean): number => events.filter(pred).reduce((m, e) => (e.id > m ? e.id : m), 0);
    const contentReadId = content ? maxId((e) => e.type !== "comment") : 0;
    const commentReadId = comments ? maxId((e) => e.type === "comment") : 0;
    if (!contentReadId && !commentReadId) return;
    try {
      await jpost(`/api/cards/${cardId}/read`, { content_read_id: contentReadId, comment_read_id: commentReadId });
    } catch (_) { return; }
    const st = state();
    const u: UnreadFlags = { ...(st.unread[cardId] || {}) };
    if (content) u.content = false;
    if (comments) u.comments = false;
    if (u.content || u.comments) st.unread[cardId] = u;
    else delete st.unread[cardId];
    if (content) byId("contentUnreadDot").hidden = true;
    if (comments) byId("commentsUnreadDot").hidden = true;
    deps.readMarkers.refreshCardUnreadDot(cardId);
    deps.readMarkers.renderNotifBadge();
  }

  // ---- move / copy a card (same board → relocate/duplicate; other board →
  // client-side re-encryption). Boards are chosen by (decrypted) name and
  // the destination list + position are selectable. ----

  let moveCtx: MoveCtx | null = null;

  // ensureDK returns a usable DK for a board, unlocking via passphrase if needed.
  async function ensureDK(bid: number): Promise<DataKey> {
    if (bid === boardId) return mustDK();
    let d = await xyCrypto.loadCachedDK(bid);
    if (d) return d;
    const pass = prompt("Пароль целевой доски:");
    if (pass == null) throw new Error("отменено");
    const keymeta = (await fetchJSON(`/api/boards/${bid}/keymeta`)) as BoardKeymeta;
    d = await xyCrypto.unlockBoard(pass, keymeta);
    await xyCrypto.cacheDK(bid, d);
    return d;
  }

  // populateMoveBoards fills the board <select> with decrypted board names (the
  // current board first/default), then loads its lists.
  async function populateMoveBoards(): Promise<void> {
    const sel = moveBoardSel;
    sel.replaceChildren();
    let boards: MoveBoardItem[] = [];
    try { boards = (await fetchJSON("/api/boards")) as MoveBoardItem[]; } catch (_) {}
    // Always offer the current board (so the move UI works — and never prompts for
    // another board's password — even when offline and the board list is unfetched).
    if (!boards.some((b) => b.id === boardId)) boards.unshift({ id: boardId, name_enc: null });
    for (const b of boards) {
      let label = "доска #" + b.id;
      if (b.id === boardId) label = (state().name || label) + " (эта доска)";
      else if ((b.schema_version ?? 0) >= 2) label = b.name || label; // plaintext name, no key needed
      else {
        try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc || ""); }
        catch (_) {}
      }
      sel.append(el("option", { value: b.id, text: label }));
    }
    sel.value = String(boardId);
    await onMoveBoardChange();
  }

  // loadMoveBoard returns a ctx {boardId, dk, lists, cardsByList, labels} for the
  // given board — from in-memory state for the current board, otherwise by
  // fetching + decrypting its snapshot.
  async function loadMoveBoard(bid: number): Promise<MoveCtx> {
    if (bid === boardId) {
      const lists = [...state().lists].sort(byRank).map((l) => ({ id: l.id, title: l.title, rank: l.rank }));
      const cardsByList = new Map<number, Array<{ id: number; rank: string }>>();
      for (const l of lists) cardsByList.set(l.id, deps.cardsOf(l.id).map((c) => ({ id: c.id, rank: c.rank })));
      return { boardId: bid, dk: mustDK(), lists, cardsByList, labels: state().labels };
    }
    const tdk = await ensureDK(bid);
    const snap = (await fetchJSON(`/api/boards/${bid}`)) as Snapshot;
    const lists = await Promise.all((snap.lists || []).map(async (l) => ({
      id: l.id, rank: l.rank, title: await xyCrypto.decField(tdk, l.title_enc),
    })));
    lists.sort(byRank);
    const cardsByList = new Map<number, Array<{ id: number; rank: string }>>();
    for (const l of lists) {
      cardsByList.set(l.id, (snap.cards || []).filter((c) => c.list_id === l.id).map((c) => ({ id: c.id, rank: c.rank })).sort(byRank));
    }
    const labels = await Promise.all((snap.labels || []).map(async (l) => ({
      id: l.id, kind: l.kind, name: await xyCrypto.decField(tdk, l.name_enc), color: await xyCrypto.decField(tdk, l.color_enc),
    })));
    return { boardId: bid, dk: tdk, lists, cardsByList, labels };
  }

  async function onMoveBoardChange(): Promise<void> {
    const listSel = moveListSel;
    const bid = Number(moveBoardSel.value);
    listSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
    try { moveCtx = await loadMoveBoard(bid); }
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

  // cardCopyBody builds the create-card payload for a copy: it re-encrypts the
  // description and — when set — the handout-generation settings (field #10,
  // handout_meta_enc) and the alias under `key` (the destination board's data key).
  // kind carries over verbatim. Keeping these here (not in copyCardExtras) means
  // they copy offline too, like the description.
  async function cardCopyBody(src: BoardCard, rank: string, key: DataKey): Promise<OpBody> {
    const body: OpBody = { description_enc: await xyCrypto.encField(key, src.desc), rank, kind: src.kind };
    if (src.handoutMeta) body.handout_meta_enc = await xyCrypto.encField(key, src.handoutMeta);
    if (src.alias) body.alias_enc = await xyCrypto.encField(key, src.alias);
    return body;
  }

  // copyCardExtras carries a source card's comments and attachments onto a freshly
  // created destination card (labels are reconciled separately by the callers). The
  // source card is always on the current board, so its content is read under `dk`
  // and re-encrypted under the destination key `targetDk`. Comments are imported
  // preserving their original author + timestamp (the bulk /timeline/import
  // endpoint); attachments are downloaded, decrypted, re-encrypted and re-uploaded
  // (preserving mime + lossless flag). Copy/move is an online-only operation, so
  // this runs straight against the API (no sync outbox / temp ids).
  async function copyCardExtras(srcCardId: number, targetDk: DataKey, newCardId: number): Promise<void> {
    if (!xySync.isOnline() || !newCardId) return;
    const dk = mustDK();
    // Comments, oldest→newest so the copy keeps the original order, re-encrypted
    // under the destination key but carrying the source author + created_at.
    let events: CardEvent[] = [];
    try { events = (await fetchJSON(`/api/cards/${srcCardId}/timeline`)) as CardEvent[]; } catch (_) { events = []; }
    const comments: Array<Record<string, unknown>> = [];
    for (const ev of events) {
      if (ev.type !== "comment") continue;
      let text: string;
      try { text = await xyCrypto.decField(dk, ev.payload_enc || ""); } catch (_) { continue; }
      comments.push({
        // src ids travel so the server can rebuild threading under fresh ids
        src_id: ev.id,
        reply_to_src_id: ev.reply_to_id != null ? ev.reply_to_id : null,
        author_user_id: ev.author_user_id != null ? ev.author_user_id : null,
        created_at: ev.created_at,
        is_excerpt: !!ev.is_excerpt,
        payload_enc: await xyCrypto.encField(targetDk, text),
      });
    }
    if (comments.length) {
      try { await jpost(`/api/cards/${newCardId}/timeline/import`, { events: comments }); } catch (_) {}
    }
    // Attachments: re-encrypt the ciphertext bytes under the destination key.
    let atts: AttachmentDTO[] = [];
    try { atts = (await fetchJSON(`/api/cards/${srcCardId}/attachments`)) as AttachmentDTO[]; } catch (_) { atts = []; }
    for (const att of atts) {
      let name = "файл";
      try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) {}
      let plain: Uint8Array<ArrayBuffer>;
      try {
        const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
        if (!res.ok) continue;
        plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
      } catch (_) { continue; }
      let recipher: Uint8Array<ArrayBuffer>;
      try { recipher = await xyCrypto.encBytes(targetDk, plain); } catch (_) { continue; }
      const fd = new FormData();
      fd.append("meta", JSON.stringify({
        filename_enc: await xyCrypto.encField(targetDk, name),
        mime: att.mime, lossless: !!att.lossless, is_excerpt: !!att.is_excerpt,
        event_payload_enc: await xyCrypto.encField(targetDk, JSON.stringify({ file: name })),
      }));
      fd.append("blob", new Blob([recipher], { type: "application/octet-stream" }), "blob");
      try { await fetch(`/api/cards/${newCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd }); } catch (_) {}
    }
  }

  // reconcileLabels maps a source card's labels onto the target board by
  // decrypted name+color, creating any missing label there. targetLabels is the
  // running target-board list — mutated so a batch of copies reuses labels it
  // just created instead of duplicating them.
  async function reconcileLabels(srcCardId: number, targetBid: number, targetDk: DataKey, targetLabels: MoveLabel[]): Promise<number[]> {
    const srcIds = state().cardLabels[srcCardId] || [];
    const targetIds: number[] = [];
    for (const sid of srcIds) {
      const sl = deps.labelById(sid);
      if (!sl) continue;
      let match = targetLabels.find((t) => t.name === sl.name && t.color === sl.color);
      if (!match) {
        const lr = (await jpost(`/api/boards/${targetBid}/labels`, {
          name_enc: await xyCrypto.encField(targetDk, sl.name), color_enc: await xyCrypto.encField(targetDk, sl.color), kind: sl.kind,
        })) as { id: number };
        match = { id: lr.id, name: sl.name, color: sl.color };
        targetLabels.push(match);
      }
      targetIds.push(match.id);
    }
    return targetIds;
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
      if (sameBoard) {
        if (remove) {
          await verbs.patch("patchCard", `/api/cards/${card.id}`, { list_id: targetListId, rank });
          card.listId = targetListId;
          card.rank = rank;
        } else {
          const dk = mustDK();
          const res = (await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, dk))) as { id: number };
          state().cards.push({ id: res.id, listId: targetListId, kind: card.kind, rank, desc: card.desc, handoutMeta: card.handoutMeta || null, alias: card.alias || null, createdAt: nowStamp() });
          const ids = state().cardLabels[card.id] || [];
          if (ids.length) { await jput(`/api/cards/${res.id}/labels`, { label_ids: ids }); state().cardLabels[res.id] = ids.slice(); }
          await copyCardExtras(card.id, dk, res.id);
        }
      } else {
        const tdk = moveCtx.dk;
        const res = (await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, tdk))) as { id: number };
        const targetIds = await reconcileLabels(card.id, targetBid, tdk, moveCtx.labels.slice());
        if (targetIds.length) await jput(`/api/cards/${res.id}/labels`, { label_ids: targetIds });
        await copyCardExtras(card.id, tdk, res.id);
        if (remove) {
          await jdelete(`/api/cards/${card.id}`);
          const st = state();
          st.cards = st.cards.filter((c) => c.id !== card.id);
          cardOverlay.hidden = true;
        }
      }
      deps.render();
      if (sameBoard && remove) { await populateMoveBoards(); } // refresh positions
      msg.textContent = remove ? "Перемещено." : "Скопировано.";
    } catch (err) { msg.textContent = errMsg(err); }
  }

  moveBoardSel.addEventListener("change", () => { void onMoveBoardChange(); });
  moveListSel.addEventListener("change", onMoveListChange);
  byId("copyBtn").addEventListener("click", () => { void doMoveCopy(false); });
  byId("moveBtn").addEventListener("click", () => { void doMoveCopy(true); });

  // Change card kind after creation (edit mode only; create mode uses the same
  // selector but the value is applied on first save). Test cards never reach here
  // (their selector is hidden in openCard).
  cardKindEl.addEventListener("change", async () => {
    if (pendingList) { setCardView(fieldsAvailable() ? "fields" : "text"); return; } // create mode: re-eval tabs
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
    const node = byId("cardCopyMsg");
    node.textContent = text;
    if (isErr) node.setAttribute("data-err", ""); else node.removeAttribute("data-err");
    node.hidden = false;
    if (copyMsgTimer) clearTimeout(copyMsgTimer);
    copyMsgTimer = setTimeout(() => { node.hidden = true; }, 2500);
  }

  byId("cardCopy").addEventListener("click", async () => {
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return;
    try {
      await copyText(xyChgk.shareText(card.desc, deps.questionNumberFor(card)));
      showCopyMsg("Скопировано для теста", false);
    } catch (err) {
      showCopyMsg("Не удалось скопировать: " + errMsg(err), true);
    }
  });

  function closeCard(): void {
    stopReadTracking();
    cardOverlay.hidden = true;
    reflectCardInUrl(null);
    openCardId = null;
    pendingList = null;
    cardReturn = null;
    cardView = "";
    cardFieldReaders = null;
  }

  // cardBack drives the ↩️ button: if the card was opened from a list preview,
  // close it and restore that preview scrolled to the same question; otherwise it
  // is a plain close back to the board.
  async function cardBack(): Promise<void> {
    const ret = cardReturn; // capture before closeCard clears it
    closeCard();
    if (!ret || ret.listId == null) return;
    const list = state().lists.find((l) => l.id === ret.listId);
    if (!list) return;
    await deps.preview.previewList(list, ret.group);
    if (previewOverlay.hidden) return; // guard against a close during the await
    const node = byId("previewBody").querySelector(`[data-card-id="${ret.cardId}"]`);
    if (node) node.scrollIntoView({ block: "center" });
  }
  byId("cardClose").addEventListener("click", () => { void cardBack(); });
  byId("cardLink").addEventListener("click", () => { void copyCardLink(); });
  cardOverlay.addEventListener("pointerdown", (e) => { if (e.target === cardOverlay) closeCard(); });

  // Escape behaves like the ↩️ back button when the card is open — but only when
  // no in-card widget owns Escape first (paste modal, label popup), so it dismisses
  // those without also closing the card.
  const pasteOverlay = byId("pasteOverlay");
  const excerptsOverlay = byId("excerptsOverlay");
  const threadOverlay = byId("threadOverlay");
  const feedOverlay = byId("feedOverlay");
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || cardOverlay.hidden) return;
    if (!pasteOverlay.hidden || !excerptsOverlay.hidden || !threadOverlay.hidden
        || !feedOverlay.hidden || document.querySelector(".label-add-popup")) return;
    void cardBack();
  });

  cardSaveBtn.addEventListener("click", async () => {
    captureDraft(); // fold the active view's edits into draft.desc / draft.meta
    const msg = cardMessageEl;
    // create mode: persist a new card with the composed description, then switch to
    // the full edit view.
    if (pendingList) {
      const text = draft.desc;
      const list = pendingList;
      const kind = cardKindEl.value || "question";
      const existing = deps.cardsOf(list.id);
      const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
      const meta = draft.normalizedMeta();
      const alias = draft.normalizedAlias();
      try {
        const dk = mustDK();
        const reqBody: OpBody = { description_enc: await xyCrypto.encField(dk, text), rank, kind };
        if (meta) reqBody.handout_meta_enc = await xyCrypto.encField(dk, meta);
        if (alias) reqBody.alias_enc = await xyCrypto.encField(dk, alias);
        const res = await verbs.create("createCard", `/api/lists/${list.id}/cards`, reqBody);
        const card: BoardCard = { id: res.id as number, listId: list.id, kind, rank, desc: text, handoutMeta: meta, alias, createdAt: nowStamp() };
        state().cards.push(card);
        deps.render();
        await openCard(card);
        msg.textContent = "Карточка сохранена.";
      } catch (err) { msg.textContent = errMsg(err); }
      return;
    }
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return;
    const newDesc = draft.desc;
    const newMeta = draft.normalizedMeta();
    // The alias is deliberately absent here — it saves on its own button
    // (saveAlias). «Сохранить» touches the card's 4s content only.
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
      cardDescEl.value = newDesc;
      // Test cards have no Просмотр — keep them in the current editor view (re-rendering
      // their own tester editor); every other card jumps to the rendered preview, which
      // is itself the confirmation that the edits landed.
      if (isTestCard()) {
        setTestDetailTitle(card);
        if (cardView === "fields") renderTesterFields();
        else if (cardView === "text") { const ta = cardDescEl; ta.value = xyChgk.testersToText(xyChgk.parseTestCard(newDesc).testers); fitTextarea(ta); }
        refreshSaveState();
      } else {
        setCardView("preview");
      }
      msg.textContent = "Карточка сохранена.";
    } catch (err) { msg.textContent = errMsg(err); }
  });

  // Cmd/Ctrl-Enter saves from either edit view (textarea or structured fields).
  function saveOnCmdEnter(e: KeyboardEvent): void {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      cardSaveBtn.click();
    }
  }
  cardDescEl.addEventListener("keydown", saveOnCmdEnter);
  cardFieldsEl.addEventListener("keydown", saveOnCmdEnter);

  // Re-evaluate the save button on every edit. Typing fires "input"; the Поля
  // view's +/× field pills and the tool buttons change the draft via clicks, which
  // bubble here after their own handlers have run.
  // cardAlias is in the list because it sits outside the view panels — its edits
  // reach the draft through captureDraft like any other, but no panel handler
  // covers it.
  for (const id of ["cardDesc", "cardFields", "cardAlias"]) {
    const node = byId(id);
    node.addEventListener("input", refreshSaveState);
    node.addEventListener("click", refreshSaveState);
  }
  // saveAlias persists the alias alone — its own column, its own PATCH, decoupled
  // from the card's content save. "" clears it (same convention as the server's
  // optBlob). Online-capable via the sync outbox like any other card mutation.
  async function saveAlias(): Promise<void> {
    if (pendingList || !openCardId) return; // a new card saves its alias on create
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card) return;
    const btn = cardAliasSaveBtn;
    const next = cardAliasEl.value.trim() || null;
    if (!draft.aliasDirty(next)) return;
    try {
      const body: OpBody = { alias_enc: next ? await xyCrypto.encField(mustDK(), next) : "" };
      await verbs.patch("patchCard", `/api/cards/${card.id}`, body);
      card.alias = next;
      draft.commitAlias(next);
      deps.render(); // the board card previews the alias
      btn.disabled = true;
      btn.textContent = "✓";
      setTimeout(() => { btn.textContent = "Сохранить"; }, 1200);
    } catch (err) { cardMessageEl.textContent = errMsg(err); }
  }
  cardAliasSaveBtn.addEventListener("click", () => { void saveAlias(); });
  cardAliasEl.addEventListener("keydown", (e) => {
    // Enter saves the alias (not the card); Cmd/Ctrl+Enter keeps the card-save
    // shortcut for muscle memory, but on a saved card that too means the alias.
    if (e.key === "Enter") { e.preventDefault(); void saveAlias(); }
  });

  byId("cardDelete").addEventListener("click", async () => {
    const card = state().cards.find((c) => c.id === openCardId);
    if (!card || !confirm("Удалить карточку?")) return;
    try {
      await verbs.del("deleteCard", `/api/cards/${card.id}`);
      const st = state();
      st.cards = st.cards.filter((c) => c.id !== card.id);
      await deps.cleanupTestLabels([card]);
      cardOverlay.hidden = true;
      deps.render();
    } catch (err) { cardMessageEl.textContent = errMsg(err); }
  });

  return {
    addCard,
    openCard,
    closeCard,
    openCardId: () => openCardId,
    maybeOpenDeepLink,
    highlightComment,
    copyCommentLink,
    testerSummary,
    copyTesterList,
    loadMoveBoard,
    cardCopyBody,
    copyCardExtras, reconcileLabels,
  };
}
