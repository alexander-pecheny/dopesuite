// board.ts — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { overlayStack } from "./overlaystack.js";
import { modal } from "./modal.js";
import { type Board, boardMenu, createPanelShell, listMenu, listScope, registerPanel } from "./panels.js";
import { createRewrites } from "./rewrites.js";
import { createReplacePanel } from "./replace.js";
import { createMoveListPanel } from "./movelist.js";
import { createListsManage, unitsOf } from "./listsmanage.js";
import { createImportPanel } from "./importpack.js";
import { createExportPanel } from "./export.js";
import { createHandoutsPanel } from "./handouts.js";
import { createMassPanel } from "./masspanel.js";
import { createLabelsEditor, sortLabels } from "./labelsedit.js";
import { createTesterList } from "./testerlist.js";
import { createAuthorCountPanel } from "./authorcount.js";
import { xyApp, xySizes } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { type Tester, xyChgk } from "./chgk.js";
import { xySync } from "./sync.js";
import { createBoardMembers } from "./boardmembers.js";
import { create as createAttachments } from "./attachments.js";
import { createUnlock } from "./unlock.js";
import { boardOrder, byRank, dragAfterIn, dragAfterInX, rankAfterMove } from "./dragrank.js";
import { createTimeline, decodeCommentPayload, eventAuthor } from "./timeline.js";
import { createCardDetail, nowStamp } from "./carddetail.js";
import {
  type AnnounceCity, parseSession, partialSeen, type SeenQuestion, type SessionMeta,
  sessionLabel, type TitleMode, whoSaw,
} from "./sessions.js";
import * as people from "./people.js";
import { createSessionsPanel } from "./sessionspanel.js";
import { colorField, labelFill, labelInk, LABEL_COLORS } from "./colorpick.js";
import { anchorPopup } from "./popup.js";
import { plural, xyMass } from "./massaction.js";
import { xySearchIndex } from "./searchindex.js";
import type { DataKey } from "./crypto.js";
import type { OpBody } from "./store.js";
import type { ScreenValue } from "./chgk.js";
import type { BoardCard, BoardLabel, BoardList, BoardState, CardLabel } from "./unlock.js";
import type { MembersState } from "./boardmembers.js";
import type { MenuItem, Timeline } from "./timeline.js";
import type { PreviewCardLike } from "./carddetail.js";
import { icon, iconed } from "./icons_gen.js";

const { fetchJSON, jpost, jpatch, jput, jdelete, el, byId, errMsg, deriveTitle, onCmdEnter } = xyApp;
const { keyBetween } = xyRank;

function q(sel: string): HTMLElement {
  const node = document.querySelector<HTMLElement>(sel);
  if (!node) throw new Error(`page is missing ${sel}`);
  return node;
}

// Mutation wrappers — every board mutation flows through the sync engine, which
// sends it immediately when online or queues it (returning a negative temp id
// for creates) when offline, reconciling on reconnect. `create` mints an id;
// the rest return { id: null }. See sync.js.
const create = (kind: string, path: string, body: OpBody): Promise<{ id: number | null }> =>
  xySync.mutate({ kind, method: "POST", path, body, board: boardId, mint: true });
const post = (kind: string, path: string, body: OpBody): Promise<unknown> =>
  xySync.mutate({ kind, method: "POST", path, body, board: boardId });
const patch = (kind: string, path: string, body: OpBody): Promise<unknown> =>
  xySync.mutate({ kind, method: "PATCH", path, body, board: boardId });
const put = (kind: string, path: string, body: OpBody): Promise<unknown> =>
  xySync.mutate({ kind, method: "PUT", path, body, board: boardId });
const del = (kind: string, path: string): Promise<unknown> =>
  xySync.mutate({ kind, method: "DELETE", path, board: boardId });

const boardId = Number(location.pathname.split("/").pop());

const statusNode = byId("status");
const kanban = byId("kanban");
const titleNode = byId("boardTitle");

// The board's live state: the decrypted snapshot (unlock.js BoardState) plus the
// members roster boardmembers.js merges onto it.
type LiveState = BoardState & MembersState;

const state: LiveState = { role: "editor", name: "", lists: [], groups: [], cards: [], labels: [], sessions: [], cardLabels: [], cardSessions: [], tourTesters: [], members: [], memberNames: {}, me: null, unread: {}, sizes: { ...xySizes.DEFAULT }, defaultAuthor: "", cardTitle: "question", feedDefault: "all", timezone: "", announceCities: null, sessionTitleMode: "" };
let dk: DataKey | null = null;
function mustDK(): DataKey {
  if (!dk) throw new Error("нет ключа доски");
  return dk;
}
// One-shot guard per card-drag gesture: set true the moment a drop commits the
// move, so a stray duplicate drop is ignored and dragend can tell an aborted
// gesture (which must re-render to undo `dragover`'s DOM relocation) from a real one.
let cardDragCommitted = false;
// Board-level list drag: the dragged list's id + the same commit/abort guard.
// A grouped list drags its whole group as one block (reorder INSIDE a group
// lives in «Управление списками»).
let listDragId: number | null = null;
let listDragCommitted = false;

const badge = xyApp.syncBadge(statusNode);
const setStatus = badge.set;

// ---- boot + unlock ----
// The whole boot → unlock → snapshot-load flow lives in unlock.js; the board
// hands it the DOM nodes, the singletons and the callbacks it owns.
const unlock = createUnlock({
  boardId,
  ui: {
    overlay: byId("unlockOverlay"),
    form: byId<HTMLFormElement>("unlockForm"),
    pass: byId<HTMLInputElement>("unlockPass"),
    message: byId("unlockMessage"),
  },
  crypto: xyCrypto,
  sync: xySync,
  net: xyApp,
  status: badge,
  applySizes: xySizes.apply,
  onDK: (k) => { dk = k; },
  onState: (s) => {
    Object.assign(state, s);
    sessionMetaCache = new Map();
    document.title = state.name + " · xy";
    // Feed the person directory. The tester names are plaintext in hand at this
    // moment, so this costs a pass over a handful of sessions and no decryption.
    people.remember(boardId, state.name, state.sessions.flatMap((s) => parseSession(s.meta).testers));
    render();
    renderNotifBadge();
    void boardMembers.load(); // best-effort: populate the author-name map for timelines (online only)
    void rewrites.convertLegacyVersions();
    if (dk) void xySearchIndex.refreshComments(boardId, dk);
    cardDetail.maybeOpenDeepLink(); // open a ?card=… / &comment=… deep link on first load
  },
  onUnavailable: () => {
    kanban.hidden = true;
    titleNode.textContent = "Доска недоступна офлайн";
    statusNode.title = "Нет сохранённой копии — откройте доску при подключении";
  },
});

// There is no live push from the server, so a tab left in the background misses
// remote changes made meanwhile. Re-pull the authoritative snapshot when the tab
// returns to the foreground (only once unlocked). load() itself skips the network
// fetch when offline or when local edits are still queued, and its `loading`
// guard dedupes this against the sync engine's own onBoardSynced reloads.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && dk) void unlock.load();
});

// ---- board sizes (workspace width / list width / card height) ----
// A per-user display preference, edited on /profile (see profile.js) and
// delivered in the board snapshot; here it only drives the three CSS vars.
// Apply defaults immediately so the vars are defined; the snapshot then
// overrides state.sizes with the user's saved values (see the load path).
xySizes.apply(state.sizes);

// renameBoard / deleteBoard touch board-level metadata, which isn't part of the
// per-board sync outbox (lists/cards) — so both are online-only. The server
// tombstones the board (owner-only); the reaper destroys it after 14 days.
async function renameBoard(): Promise<void> {
  const name = prompt("Новое название доски:", state.name || "");
  if (name == null) return;
  const t = name.trim();
  if (!t || t === state.name) return;
  if (!xySync.requireOnline("Переименование доски доступно только онлайн.")) return;
  setStatus("saving");
  try {
    await jpatch(`/api/boards/${boardId}`, { name: t });
    state.name = t;
    titleNode.textContent = t;
    document.title = t + " · xy";
    setStatus("saved");
  } catch (err) { setStatus("error"); alert("Не удалось переименовать: " + errMsg(err)); }
}

async function deleteBoard(): Promise<void> {
  if (state.role !== "owner") { alert("Удалить доску может только её владелец."); return; }
  if (!xySync.requireOnline("Удаление доски доступно только онлайн.")) return;
  const warn = "Доска со всеми списками, карточками и вложениями будет скрыта сразу и безвозвратно удалена через 14 дней.";
  const name = (state.name || "").trim();
  if (name) {
    const typed = prompt(`${warn}\n\nЧтобы подтвердить, введите название доски:`);
    if (typed == null) return;
    if (typed.trim() !== name) { alert("Название не совпало — удаление отменено."); return; }
  } else if (!confirm(`${warn} Продолжить?`)) return;
  try {
    await jdelete(`/api/boards/${boardId}`);
    try { await xyCrypto.forgetDK(boardId); } catch (_) {}
    people.forget(boardId);
    await xySearchIndex.forget(boardId);
    location.href = "/";
  } catch (err) { alert("Не удалось удалить: " + errMsg(err)); }
}

// ---- members / sharing ----
// The members/sharing seam lives in boardmembers.js; it caches the roster onto
// `state` (memberNames feeds the timeline's author names) and owns its overlay.
const boardMembers = createBoardMembers(state, boardId);

// ---- read markers (blue dots) + 🔔 activity bell ----
// Every user wants to read every OTHER user's changes; own edits never count.
// Read-tracking is online-only best-effort (like the members roster load): it never
// goes through the sync outbox, so it's simply skipped offline.
const notifToggle = byId("notifToggle");
const notifBadge = byId("notifBadge");

// renderNotifBadge shows the 🔔 badge iff any card has an unread bucket — red
// when any of it mentions me.
function renderNotifBadge(): void {
  const flags = Object.values(state.unread);
  notifBadge.hidden = !flags.some((u) => u.content || u.comments);
  notifBadge.classList.toggle("unread-dot-mention", flags.some((u) => u.mentions));
}

// unreadDotFor builds a card's dot: red for a mention, blue otherwise.
function unreadDotFor(u: { mentions?: boolean }, extra: string): HTMLElement {
  const mention = u.mentions ? " unread-dot-mention" : "";
  const title = u.mentions ? "Вас упомянули" : "Непрочитанные изменения";
  return el("span", { class: "unread-dot " + extra + mention, title });
}

// refreshCardUnreadDot updates a single kanban card's dot in place (cheaper
// than a full render() and doesn't disturb drag state).
function refreshCardUnreadDot(cardId: number): void {
  const node = kanban.querySelector(`.kcard[data-card-id="${cardId}"]`);
  if (!node) return;
  const u = state.unread[cardId];
  const wantDot = !!(u && (u.content || u.comments));
  const existing = node.querySelector(".kcard-unread");
  if (existing) existing.remove();
  if (wantDot) node.append(unreadDotFor(u, "unread-dot-corner kcard-unread"));
}

// ---- 🔔 bell panel: recent other-authored activity, newest first ----
interface ActivityEvent {
  id: number;
  card_id: number;
  type: string;
  created_at: string;
  unread?: boolean;
  mention?: boolean;
  mention_reply?: boolean;
  reply_to_id?: number | null;
  payload_enc?: string;
  author_user_id?: number | null;
}

let notifPanelEl: HTMLElement | null = null;

function closeNotifPanel(): void {
  if (!notifPanelEl) return;
  notifPanelEl.remove();
  notifPanelEl = null;
  notifToggle.setAttribute("aria-expanded", "false");
  document.removeEventListener("pointerdown", onNotifOutside, true);
  document.removeEventListener("keydown", onNotifKey, true);
}
function onNotifOutside(e: PointerEvent): void {
  if (notifPanelEl && e.target instanceof Node && !notifPanelEl.contains(e.target) && e.target !== notifToggle) closeNotifPanel();
}
// Transient popups (this panel, the ⋯ menu, the label picker) claim Escape in
// the CAPTURE phase and stop it there. They are not on the overlay stack — no
// history entry, nothing to go back to — but they are the innermost dismissible
// thing on screen, so Escape must close them without also closing the card
// underneath. Capture is what puts them ahead of the stack's own listener.
function onNotifKey(e: KeyboardEvent): void {
  if (e.key !== "Escape") return;
  e.stopImmediatePropagation();
  closeNotifPanel();
}

async function openNotifPanel(): Promise<void> {
  if (notifPanelEl) { closeNotifPanel(); return; }
  const panel = el("div", { class: "popover notif-panel" });
  const head = el("div", { class: "notif-panel-head" },
    el("span", { text: "События" }),
    el("button", {
      class: "btn btn-small", type: "button", text: "Прочитать всё",
      onclick: async () => {
        try { await jpost(`/api/boards/${boardId}/read-all`, {}); } catch (_) { return; }
        state.unread = {};
        render();
        renderNotifBadge();
        closeNotifPanel();
      },
    }));
  panel.append(head);
  const body = el("div", { class: "notif-panel-body" }, el("div", { class: "notif-empty", text: "Загрузка…" }));
  panel.append(body);
  notifToggle.setAttribute("aria-expanded", "true");
  notifToggle.parentElement?.append(panel);
  notifPanelEl = panel;
  document.addEventListener("pointerdown", onNotifOutside, true);
  document.addEventListener("keydown", onNotifKey, true);

  let events: ActivityEvent[] = [];
  try { events = (await fetchJSON(`/api/boards/${boardId}/activity`)) as ActivityEvent[]; } catch (_) {}
  if (notifPanelEl !== panel) return; // closed while loading
  body.replaceChildren();
  if (!events.length) { body.append(el("div", { class: "notif-empty", text: "Нет новых событий" })); return; }
  for (const ev of events) {
    const card = state.cards.find((c) => c.id === ev.card_id);
    if (!card) continue; // card deleted/moved away since the event was recorded
    const row = el("button", { class: "notif-row", type: "button" });
    if (ev.unread) row.append(el("span", { class: "unread-dot" + (ev.mention ? " unread-dot-mention" : "") }));
    // Neutral noun-phrase wording (mirrors renderEvent's own verbs map, gender-
    // agnostic since we don't know the author's grammatical gender).
    const verbs: Record<string, string> = {
      comment: "комментарий", desc_edit: "правка описания",
      label_add: "добавлена метка", label_remove: "снята метка",
      attach_add: "вложение добавлено", attach_remove: "вложение удалено", attach_replace: "вложение заменено",
      reaction: "реакция",
    };
    const verb = ev.mention ? (ev.mention_reply ? "ответ вам" : "упомянул(а) вас") : (verbs[ev.type] || ev.type);
    const when = new Date(ev.created_at).toLocaleString("ru-RU");
    const bodyWrap = el("div", { class: "notif-row-body" },
      el("div", { class: "notif-row-meta", text: `${eventAuthor(ev, state.me, state.memberNames)} ${verb} · ${cardTitle(card)} · ${when}` }));
    if (ev.type === "comment" || ev.type === "reaction") {
      let preview = "";
      try { preview = await xyCrypto.decField(mustDK(), ev.payload_enc || ""); } catch (_) {}
      if (ev.type === "comment") preview = decodeCommentPayload(preview).text;
      bodyWrap.append(el("div", { class: "notif-row-preview", text: deriveTitle(preview, 120) }));
    }
    row.append(bodyWrap);
    row.addEventListener("click", () => {
      closeNotifPanel();
      void cardDetail.openCard(card).then(() => { if (ev.type === "comment") void cardDetail.highlightComment(ev.id); });
    });
    body.append(row);
  }
}

notifToggle.addEventListener("click", () => { if (notifPanelEl) closeNotifPanel(); else void openNotifPanel(); });

const cardsOf = (listId: number): BoardCard[] => state.cards.filter((c) => c.listId === listId).sort(byRank);
const labelById = (id: number) => state.labels.find((l) => l.id === id);
const sessionById = (id: number) => state.sessions.find((s) => s.id === id);

let sessionMetaCache = new Map<number, SessionMeta>();
function sessionMeta(id: number): SessionMeta | null {
  const hit = sessionMetaCache.get(id);
  if (hit) return hit;
  const s = sessionById(id);
  if (!s) return null;
  const m = parseSession(s.meta);
  sessionMetaCache.set(id, m);
  return m;
}

function titleMode(): TitleMode {
  const m = state.sessionTitleMode;
  return m === "title" || m === "date" ? m : "date-title";
}

function sessionName(id: number): string {
  const m = sessionMeta(id);
  return m ? sessionLabel(m, titleMode()) : "тест";
}

function playingsOf(cardId: number): number[] {
  const ids = state.cardSessions.filter((p) => p.cardId === cardId).map((p) => p.sessionId);
  ids.sort((a, b) => {
    const ma = sessionMeta(a), mb = sessionMeta(b);
    return ((mb && mb.date) || "").localeCompare((ma && ma.date) || "") || b - a;
  });
  return ids;
}

function assignmentsOf(cardId: number, sessionId: number | null | undefined): CardLabel[] {
  return state.cardLabels.filter((a) =>
    a.cardId === cardId && (sessionId === undefined || a.sessionId === sessionId));
}

function sessionsOfCard(cardId: number): SessionMeta[] {
  const out: SessionMeta[] = [];
  for (const sid of playingsOf(cardId)) {
    const m = sessionMeta(sid);
    if (m) out.push(m);
  }
  return out;
}

// ---- render ----
const groupById = (id: number) => state.groups.find((g) => g.id === id);

// listsInGroup returns a group's member lists in board (rank) order.
function listsInGroup(groupId: number): BoardList[] {
  return state.lists.filter((l) => l.groupId === groupId).sort(byRank);
}

// The seam every panel works through (panels.ts).
const board: Board = {
  id: boardId,
  state,
  dk: mustDK,
  cardsOf,
  listsInGroup,
  groupById,
  assignmentsOf,
  playingsOf,
  sessionMeta,
  sessionName,
  verbs: { create, post, patch, put, del },
  render,
  setStatus,
  reload: () => unlock.load(),
};

// groupNumbering computes question numbers continuously across a group's lists:
// the cards of every member list are concatenated in order, numbered as one run
// (so list 2 picks up where list 1 left off, № / №№ directives included), then
// sliced back per list. Returns Map(listId → numbers[]).
function groupNumbering(lists: BoardList[]): Map<number, Array<string | null>> {
  const arrays = lists.map((l) => cardsOf(l.id));
  const numbers = xyChgk.numberQuestionCards(arrays.flat());
  const map = new Map<number, Array<string | null>>();
  let off = 0;
  arrays.forEach((arr, i) => { map.set(lists[i].id, numbers.slice(off, off + arr.length)); off += arr.length; });
  return map;
}

// plural picks the Russian declension for n: 1 вопрос, 2 вопроса, 12 вопросов.
function questionCountLabel(n: number): string {
  return `${n} ${plural(n, "вопрос", "вопроса", "вопросов")}`;
}

// renderBoardTitle writes the crumb: the board's name, then how many questions
// are on it. It rides on render() rather than on the snapshot, so adding or
// deleting a card moves the number with it. Hidden at zero — a board with
// nothing on it does not need telling.
function renderBoardTitle(): void {
  const n = state.cards.filter((c) => c.kind === "question").length;
  titleNode.replaceChildren(state.name);
  if (n) titleNode.append(el("span", { class: "board-qcount", text: questionCountLabel(n) }));
}

// The Search Index is written by whoever holds the key (ADR-0008), and on this
// page that is every render: the state it reads is already plaintext, so this
// costs a walk over the cards and one IndexedDB put, no decryption. Debounced,
// because a drag renders on every frame.
let reindexTimer = 0;
function scheduleReindex(): void {
  clearTimeout(reindexTimer);
  reindexTimer = setTimeout(() => {
    const lists = [...state.lists].sort(byRank);
    const cards = boardOrder(state.lists, state.cards);
    void xySearchIndex.putCards(
      boardId,
      state.name,
      lists.map((l) => ({ id: l.id, title: l.title })),
      cards.map((c) => ({ id: c.id, list: c.listId, kind: c.kind, desc: c.desc, alias: c.alias || "" })),
    );
  }, 800);
}

function render(): void {
  kanban.hidden = false;
  scheduleReindex();
  renderBoardTitle();
  // The list "⋯" menu floats on <body>: a rebuild would strand it next to a
  // stale anchor, so close it with the DOM it was opened for.
  if (openListMenu) openListMenu.close();
  // Preserve scroll positions across the full rebuild below — otherwise a drag
  // (or any mutation that re-renders) snaps the board back to the top-left, which
  // is jarring mid-edit. Capture the horizontal board scroll + each list's
  // vertical scroll, then restore them once the fresh DOM is in place.
  const scrollLeft = kanban.scrollLeft;
  const listScroll = new Map<string | undefined, number>();
  for (const b of kanban.querySelectorAll<HTMLElement>(".kcards")) listScroll.set(b.dataset.listId, b.scrollTop);
  kanban.replaceChildren();
  const sorted = [...state.lists].sort(byRank);
  // Walk the lists in board order; a maximal run of consecutive lists sharing a
  // group_id gets continuous numbering. On the board the members render as
  // ordinary lists, each with a small 🔗group tag underneath (a bordered
  // wrapper box around the run used to trap the board's scroll).
  let i = 0;
  while (i < sorted.length) {
    const l = sorted[i];
    if (l.groupId != null) {
      const run: BoardList[] = [];
      while (i < sorted.length && sorted[i].groupId === l.groupId) { run.push(sorted[i]); i++; }
      const numbering = groupNumbering(run);
      for (const list of run) kanban.append(renderList(list, numbering.get(list.id)));
    } else {
      kanban.append(renderList(l));
      i++;
    }
  }
  kanban.append(renderAddList());
  paintLabels();
  // Unconditional: renderBar is also what HIDES the bar, so guarding it on
  // mass.mode left «Готово» with nothing to close.
  mass.prune();
  mass.renderBar();
  kanban.scrollLeft = scrollLeft;
  for (const b of kanban.querySelectorAll<HTMLElement>(".kcards")) {
    const top = listScroll.get(b.dataset.listId);
    if (top != null) b.scrollTop = top;
  }
}

function renderList(list: BoardList, precomputedNumbers?: Array<string | null>): HTMLElement {
  const col = el("div", { class: "klist", draggable: "true", dataset: { listId: list.id } });
  const menuWrap = el("div", { class: "klist-menu-wrap" });
  // Adding a card is the most-used list action (issue #4): a dedicated "+"
  // beside the ⋯ menu saves the menu round-trip. The menu item stays too.
  const addCardBtn = el("button", { class: "kadd", title: "Добавить карточку", "aria-label": "Добавить карточку" }, icon("plus"));
  addCardBtn.addEventListener("click", () => { void cardDetail.addCard(list); });
  const menuBtn = el("button", { class: "kadd", title: "Меню списка", "aria-haspopup": "true" }, icon("ellipsis"));
  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    const items: MenuItem[] = listMenu(listScope(board, list)).map((it) => ({ icon: icon(it.icon), label: it.label, onClick: it.onClick }));
    popupMenu(menuWrap, items);
  });
  menuWrap.append(menuBtn);
  const cards = cardsOf(list.id);
  const headMain = el("div", { class: "klist-headmain" },
    el("span", { class: "klist-title", text: list.title || "(без названия)" }));
  const qCount = cards.filter((c) => c.kind === "question").length;
  if (qCount) headMain.append(el("span", { class: "klist-count", text: questionCountLabel(qCount) }));
  const headKids: HTMLElement[] = [];
  if (mass.mode) {
    const ids = cards.map((c) => c.id);
    const all = el("input", { type: "checkbox", "aria-label": "Отметить весь список" }) as HTMLInputElement;
    all.dataset.listId = String(list.id);
    all.checked = xyMass.allSelected(mass.selected, ids);
    all.addEventListener("change", () => mass.toggleAll(ids));
    headKids.push(el("label", { class: "klist-check" }, all));
  }
  col.append(el("div", { class: "klist-head" }, ...headKids, headMain, addCardBtn, menuWrap));
  if (list.groupId != null) {
    const g = groupById(list.groupId);
    col.append(el("div", { class: "klist-group-tag", title: "Список входит в группу — сквозная нумерация и общий экспорт" }, ...iconed("link", (g && g.name) || "связанные списки")));
  }
  const body = el("div", { class: "kcards", dataset: { listId: list.id } });
  // Grouped lists carry continuous numbering computed across the whole group;
  // standalone lists number from 1.
  const numbers = precomputedNumbers || xyChgk.numberQuestionCards(cards);
  cards.forEach((card, i) => body.append(renderCard(card, numbers[i])));
  col.append(body);

  // list drag — a grouped list picks up its whole group (all member columns
  // move as one block); a standalone list moves alone.
  col.addEventListener("dragstart", (e) => {
    if (e.target !== col) return;
    e.dataTransfer?.setData("text/xy-list", String(list.id));
    listDragId = list.id;
    listDragCommitted = false;
    for (const n of listDragBlock()) n.classList.add("dragging");
  });
  col.addEventListener("dragend", () => {
    for (const n of kanban.querySelectorAll(".klist.dragging")) n.classList.remove("dragging");
    listDragId = null;
    // Aborted gesture: `dragover` may have relocated the block without a commit
    // to back it — re-render from state so the DOM matches the source of truth.
    if (!listDragCommitted) render();
  });

  // card drop target
  body.addEventListener("dragover", (e) => {
    if (!e.dataTransfer?.types.includes("text/xy-card")) return;
    e.preventDefault();
    const after = dragAfter(body, e.clientY);
    const dragging = document.querySelector(".kcard.dragging");
    if (!dragging) return;
    if (after == null) body.append(dragging);
    else body.insertBefore(dragging, after);
  });
  body.addEventListener("drop", (e) => {
    if (!e.dataTransfer?.types.includes("text/xy-card")) return;
    e.preventDefault();
    if (cardDragCommitted) return; // ignore a stray second drop from the same gesture
    cardDragCommitted = true;
    const cardId = Number(e.dataTransfer.getData("text/xy-card"));
    void commitCardMove(cardId, list.id, body);
  });
  return col;
}

// renameList re-encrypts a new title under the board key and patches the list
// (offline-capable via the sync outbox).
async function renameList(list: BoardList): Promise<void> {
  const name = prompt("Новое название списка:", list.title || "");
  if (name == null) return;
  const t = name.trim();
  if (!t || t === list.title) return;
  setStatus("saving");
  try {
    await patch("patchList", `/api/lists/${list.id}`, { title_enc: await xyCrypto.encField(mustDK(), t) });
    list.title = t;
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); alert("Не удалось переименовать: " + errMsg(err)); }
}

// deleteList soft-deletes the list and its cards (server cascades the cards),
// offline-capable via the sync outbox.
async function deleteList(list: BoardList): Promise<void> {
  const n = cardsOf(list.id).length;
  const tail = n ? ` и ${n} карточк(и) в нём` : "";
  if (!confirm(`Удалить список «${list.title || "без названия"}»${tail}? Это действие необратимо.`)) return;
  setStatus("saving");
  try {
    const removed = cardsOf(list.id);
    await del("deleteList", `/api/lists/${list.id}`);
    state.lists = state.lists.filter((l) => l.id !== list.id);
    state.cards = state.cards.filter((c) => c.listId !== list.id);
    const oc = cardDetail.openCardId();
    if (oc != null && !state.cards.some((c) => c.id === oc)) cardDetail.closeCard();
    forgetCardLabels(removed);
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); alert("Не удалось удалить: " + errMsg(err)); }
}

// forgetCardLabels drops dead cards' assignments and playings. cardLabels is a
// flat list, so this MUST filter — `delete list[id]` punches a hole at that
// index, dropping someone else's assignment and keeping the dead card's own.
function forgetCardLabels(deletedCards: BoardCard[]): void {
  const dead = new Set(deletedCards.map((c) => c.id));
  state.cardLabels = state.cardLabels.filter((a) => !dead.has(a.cardId));
  state.cardSessions = state.cardSessions.filter((p) => !dead.has(p.cardId));
}

// Cards carry the card's *whole* text (whitespace collapsed), not a truncated
// preview: how much of it is visible is a display choice, made in CSS by the
// --kcard-lines clamp (see the sizes modal). Truncating here instead would cap
// the card at 80 characters no matter how much room the reader gives it.
// An alias (a card's own 1–3 keywords) wins over both: it was written precisely
// to identify this card at a glance, so it beats any derivation from the text.
// state.cardTitle is the reader's fallback preference — question text or answer.
const cardBody = (card: BoardCard): string =>
  aliasOf(card) || deriveTitle(xyChgk.previewText(card.kind, card.desc, state.cardTitle), Infinity);

// aliasOf normalizes a card's alias to a non-empty string or "" (absent cards,
// null, and whitespace-only all collapse to "no alias").
const aliasOf = (card: BoardCard | null | undefined): string => ((card && card.alias) || "").trim();

// cardTitle is the plain-text form (move/copy dialogs, titles); renderCardTitle
// below is the DOM form.
function cardTitle(card: BoardCard, number?: string | null): string {
  const body = cardBody(card);
  if (card.kind === "question" && number) return `${number}. ${body}`;
  return body;
}

// renderCardTitle builds the title node. For numbered question cards the auto/
// directive number is rendered in a muted span so it reads as scaffolding,
// visually distinct from the question content itself.
function renderCardTitle(card: BoardCard, number?: string | null): HTMLElement {
  // An aliased card gets a modifier class: the alias is a label, not an excerpt,
  // so it should not be line-clamped down to nothing by --kcard-lines.
  const cls = "kcard-title" + (aliasOf(card) ? " kcard-title-alias" : "");
  if (card.kind === "question" && number) {
    return el("div", { class: cls },
      el("span", { class: "kcard-num", text: `${number}. ` }),
      cardBody(card));
  }
  return el("div", { class: cls, text: cardTitle(card, number) });
}

// leadIcon marks a glyph as having words after it — see .ico-lead: CSS cannot
// tell "glyph alone" from "glyph then text" apart, because a label is a bare
// text node and :only-child counts elements.
function leadIcon(node: Node): Node {
  if (node instanceof SVGElement) node.setAttribute("class", "ico ico-lead");
  return node;
}

// labelDot is a label: a disc of the label's own colour and nothing else. A
// glyph outlined in one colour and filled with another needs room the row does
// not have — at the size a card badge actually renders, a rim is half a device
// pixel and only mutes the colour it encircles. A disc is all fill.
function labelDot(color: string, title: string): HTMLElement {
  const dot = el("span", { class: "kcard-label", title });
  dot.style.background = labelFill(color);
  return dot;
}

// ---- the test flask ----
// A playing is a SOLID flask, in one colour: the first verdict recorded at that
// test, or --muted when the testers recorded nothing — «tested, nobody said
// anything». It used to be an outline with the verdicts poured in as liquid,
// which needed three inks inside 14 pixels and read as a grey smudge.
// Verdicts past the first hang beside it as a braille-like grid of discs (see
// .kcard-verdicts), so the flask keeps one colour however many were recorded.
const VERDICT_DOTS = 6;

function flaskIcon(color: string): SVGSVGElement {
  const svg = icon("flask-conical");
  svg.querySelector("path")?.setAttribute("fill", "currentColor");
  svg.style.color = labelFill(color);
  return svg;
}

function renderCard(card: BoardCard, number?: string | null): HTMLElement {
  const node = el("div", { class: "kcard kcard-" + (card.kind || "normal"), draggable: "true", dataset: { cardId: card.id }, onclick: () => { void cardDetail.openCard(card); } });
  // In массовое действие a card is something you pick, not something you open:
  // the tickbox swallows the click so ticking a run never opens one by accident.
  if (mass.mode) {
    const box = el("input", { type: "checkbox", "aria-label": "Отметить карточку" }) as HTMLInputElement;
    box.dataset.cardId = String(card.id);
    box.checked = mass.selected.has(card.id);
    const wrap = el("label", { class: "kcard-check" }, box);
    wrap.addEventListener("click", (e) => { e.stopPropagation(); });
    box.addEventListener("change", () => mass.toggle(card.id));
    node.append(wrap);
  }
  const labelRow = el("div", { class: "kcard-labels" });
  // Derived from the text, so it leads the row: nobody put it there and nobody
  // can take it off, unlike everything after it.
  if (card.kind === "question" && xyChgk.handoutForCard(card.desc)) {
    labelRow.append(el("span", { class: "kcard-handout", title: "Раздаточный материал" }, icon("file-text")));
  }
  // Which questions the group has not settled on yet — the card itself shows
  // version 1, and this is the only sign the others exist.
  const versions = xyChgk.versionCount(card.desc);
  if (card.kind === "question" && versions > 1) {
    labelRow.append(el("span", { class: "kcard-versions", title: `Версий: ${versions}` }, ...iconed("copy", String(versions))));
  }
  // The board card shows the author's own labels; a test's verdict belongs to the
  // card detail, where it can say WHICH test it came from.
  for (const a of assignmentsOf(card.id, null)) {
    const lbl = labelById(a.labelId);
    if (lbl) labelRow.append(labelDot(lbl.color, lbl.name));
  }
  // One flask per test the question was played at, coloured by the verdicts
  // recorded there. The colours are the only signal now, so the tooltip names
  // them — it used to live on the individual dots.
  for (const sid of playingsOf(card.id)) {
    const verdicts = assignmentsOf(card.id, sid)
      .map((a) => labelById(a.labelId))
      .filter((l): l is BoardLabel => !!l);
    const title = "Тест: " + sessionName(sid) +
      (verdicts.length ? " — " + verdicts.map((l) => l.name).join(", ") : "");
    const test = el("span", { class: "kcard-test", title },
      el("span", { class: "kcard-test-icon" }, flaskIcon(verdicts[0]?.color || "")));
    // The rest of the verdicts, as many as the grid holds; the tooltip above
    // still names every one of them.
    if (verdicts.length > 1) {
      const grid = el("span", { class: "kcard-verdicts" });
      for (const v of verdicts.slice(1, 1 + VERDICT_DOTS)) {
        const dot = el("span", { class: "kcard-verdict" });
        dot.style.background = labelFill(v.color);
        grid.append(dot);
      }
      test.append(grid);
    }
    labelRow.append(test);
  }
  if (labelRow.children.length) node.append(labelRow);
  node.append(renderCardTitle(card, number));
  const u = state.unread[card.id];
  if (u && (u.content || u.comments)) node.append(unreadDotFor(u, "unread-dot-corner kcard-unread"));
  node.addEventListener("dragstart", (e) => {
    e.stopPropagation();
    e.dataTransfer?.setData("text/xy-card", String(card.id));
    node.classList.add("dragging");
    cardDragCommitted = false;
  });
  // On dragend, if no drop committed the move, the gesture was aborted (common on
  // mobile, where native DnD is flaky / unsupported): `dragover` may have already
  // relocated this node into another list's DOM without a patch to back it. Re-render
  // from state so the DOM matches the source of truth — otherwise the orphaned,
  // uncommitted node reads as a duplicate. See the duplication bug investigation.
  node.addEventListener("dragend", () => {
    node.classList.remove("dragging");
    if (!cardDragCommitted) render();
  });
  return node;
}

// Apply label colors through the CSSOM (avoids inline-style CSP issues).
// The value written is `var(--label-green)`, not a hex, so the browser re-reads
// it when the theme flips — nothing here has to re-render on a theme change.
function paintLabels(): void {
  for (const chip of document.querySelectorAll<HTMLElement>(".label-swatch[data-c]")) {
    chip.style.background = labelFill(chip.dataset.c || "");
  }
  // A pick is the one that carries its name, so its ink follows its colour.
  for (const pick of document.querySelectorAll<HTMLElement>(".label-pick[data-c]")) {
    pick.style.background = labelFill(pick.dataset.c || "");
    const ink = labelInk(pick.dataset.c || "");
    if (ink) pick.style.color = ink;
  }
}

function dragAfter(container: HTMLElement, y: number): Element | null {
  return dragAfterIn([...container.querySelectorAll(".kcard:not(.dragging)")], y);
}

// ---- board-level list reorder (drag a column) ----
// Orderable units are standalone lists and whole groups, same as the
// «Управление списками» modal: the dragged block is every column of the
// dragged list's unit, and an insertion point is only ever BETWEEN units —
// snapToUnitStart keeps a drop from splitting somebody else's group.

const listByIdNum = (id: number): BoardList | undefined => state.lists.find((l) => l.id === id);

// listDragBlock returns the dragged unit's column nodes in DOM order.
function listDragBlock(): HTMLElement[] {
  const dragged = listDragId != null ? listByIdNum(listDragId) : undefined;
  if (!dragged) return [];
  const ids = new Set(
    dragged.groupId == null
      ? [String(dragged.id)]
      : state.lists.filter((l) => l.groupId === dragged.groupId).map((l) => String(l.id)),
  );
  return [...kanban.querySelectorAll<HTMLElement>(".klist[data-list-id]")].filter((n) => ids.has(n.dataset.listId || ""));
}

// snapToUnitStart walks a grouped target column back to the first column of its
// group's run, so the block is inserted before the whole group, never inside it.
function snapToUnitStart(col: Element | null): Element | null {
  if (!col) return null;
  const gidOf = (n: Element | null): number | null => {
    if (!(n instanceof HTMLElement) || !n.dataset.listId || n.classList.contains("dragging")) return null;
    const l = listByIdNum(Number(n.dataset.listId));
    return l ? l.groupId : null;
  };
  const gid = gidOf(col);
  if (gid == null) return col;
  let first = col;
  while (gidOf(first.previousElementSibling) === gid) first = first.previousElementSibling as Element;
  return first;
}

kanban.addEventListener("dragover", (e) => {
  if (listDragId == null || !e.dataTransfer?.types.includes("text/xy-list")) return;
  e.preventDefault();
  const block = listDragBlock();
  if (!block.length) return;
  const others = [...kanban.querySelectorAll(".klist[data-list-id]:not(.dragging)")];
  const after = snapToUnitStart(dragAfterInX(others, e.clientX));
  const anchor = after || kanban.querySelector(".klist-add");
  for (const n of block) kanban.insertBefore(n, anchor);
});

kanban.addEventListener("drop", (e) => {
  if (listDragId == null || !e.dataTransfer?.types.includes("text/xy-list")) return;
  e.preventDefault();
  listDragCommitted = true;
  // Fold the DOM column order into units and persist it via the same rank
  // writer the lists-management modal uses.
  const order = [...kanban.querySelectorAll<HTMLElement>(".klist[data-list-id]")]
    .map((n) => listByIdNum(Number(n.dataset.listId)))
    .filter((l): l is BoardList => !!l);
  void listsManage.applyUnitOrder(unitsOf(order));
});

// ---- add list / card ----
function renderAddList(): HTMLElement {
  const wrap = el("div", { class: "klist klist-add" });
  const form = el("form", { class: "kadd-form" });
  const input = el("input", { class: "input u-grow", type: "text", placeholder: "+ Новый список" }) as HTMLInputElement;
  // Android's soft keyboard has no Enter on this field, so a visible ✓ submit
  // appears as soon as there is a name to create.
  const okBtn = el("button", {
    class: "kadd kadd-ok", type: "submit", title: "Создать список", "aria-label": "Создать список", hidden: true,
  }, icon("check")) as HTMLButtonElement;
  input.addEventListener("input", () => { okBtn.hidden = !input.value.trim(); });
  // Every list is a question list now: a test session is board-level, not a
  // column, so the old «вопросы / тесты» picker has nothing left to pick.
  form.append(el("div", { class: "u-row u-gap-sm" }, input, okBtn));
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const title = input.value.trim();
    if (!title) return;
    const type = "normal";
    const ranks = [...state.lists].sort(byRank);
    const rank = keyBetween(ranks.length ? ranks[ranks.length - 1].rank : null, null);
    try {
      const titleEnc = await xyCrypto.encField(mustDK(), title);
      const res = await create("createList", `/api/boards/${boardId}/lists`, { title_enc: titleEnc, rank, type });
      state.lists.push({ id: res.id as number, type, rank, groupId: null, title });
      input.value = "";
      okBtn.hidden = true;
      render();
    } catch (err) { setStatus("error"); }
  });
  wrap.append(form);
  return wrap;
}

// ---- list menu (popup) ----

// popupMenu mounts a small dropdown (dope .menu-dropdown styling) on <body>,
// position:fixed next to the anchor and clamped to the viewport — an
// absolutely-positioned menu inside the kanban scroll container got CLIPPED at
// the work area's edge whenever it was wider than the space beside its list.
// Closes on outside click / Escape / scroll / item choice.
// Reused by the per-list "⋯" menu.
let openListMenu: { anchor: HTMLElement; close: () => void } | null = null; // { anchor, close } of the one open menu
function popupMenu(anchor: HTMLElement, items: MenuItem[]): void {
  if (openListMenu) {
    const sameAnchor = openListMenu.anchor === anchor;
    openListMenu.close();
    if (sameAnchor) return; // toggle off
  }
  const menu = el("div", { class: "menu-dropdown menu-fixed", role: "menu" });
  for (const it of items) {
    // An item with `checked` is a toggle: a real checkbox, styled by the design
    // system's input[type=checkbox] rules, rather than a ☐/☑ glyph. It is a
    // <label> so the whole row remains the hit target. preventDefault keeps the
    // box from flipping optimistically — the caller re-renders from what the
    // server actually stored.
    if (it.checked !== undefined) {
      // `radio` picks one of a set instead of flipping one flag — a comment
      // belongs to at most one test, unlike a card, which is played at several.
      const kind = it.radio ? "radio" : "checkbox";
      const box = el("input", { type: kind, name: "menu-" + kind, role: "menuitem" + kind }) as HTMLInputElement;
      box.checked = !!it.checked;
      const row = el("label", { class: "menu-item menu-item-check" }, box, it.label);
      row.addEventListener("click", (e) => { e.preventDefault(); close(); it.onClick(); });
      menu.append(row);
      continue;
    }
    // The glyph is a separate node, never part of the label: a menu row is read
    // by its words, and the icon is only an anchor for the eye.
    menu.append(el("button", {
      class: "menu-item", type: "button", role: "menuitem",
      onclick: () => { close(); it.onClick(); },
    }, it.icon ? [leadIcon(it.icon)] : [], it.label));
  }
  const { close } = anchorPopup(menu, anchor, { anchor, onClose: () => { openListMenu = null; } });
  openListMenu = { anchor, close };
}

// ---- list preview (docx-style HTML render, entirely client-side) ----
// Renders a whole list the way chgksuite's docx export would — questions with
// numbered labels and Ответ/Зачёт/Комментарий/etc. fields, plus meta, headings
// and handouts — but in the browser, so it's instant. Inline 4s markup
// (bold/italic/links/(img …)/(screen …)) is parsed via xyChgk; referenced image
// handouts are resolved from the cards' attachments (decrypted + object-URL'd).

// The card shape the preview renders: a persisted board card, the card detail's
// transient draft card, or an import-verify block (which has no list yet).
interface PvCard { id: number; kind: string; desc: string; listId?: number }

const PV_LABELS = xyChgk.QUESTION_LABELS;
const previewOverlay = byId("previewOverlay");

// (attachment caches live in attachments.ts)

// fillPreviewImages swaps the "[изображение: …]" placeholders inside an already
// rendered preview for the images that have since resolved.
function fillPreviewImages(root: ParentNode, imgMap: Map<string, string>): void {
  for (const ph of root.querySelectorAll<HTMLElement>(".pv-img-missing[data-img]")) {
    const url = imgMap.get(ph.dataset.img || "");
    if (url) ph.replaceWith(el("img", { class: "pv-img", src: url, alt: ph.dataset.img }));
  }
}

// fieldOpts returns the render options for a field given the screen-mode toggle.
// Meta/headings are never screen-transformed. `nbsp` (non-breaking spaces/
// hyphens) applies everywhere except sources and handouts, like docx.
interface RichOpts { accents?: boolean; brackets?: boolean; nbsp?: boolean }
function fieldOpts(field: string, screen: boolean): RichOpts {
  const nbsp = field !== "source" && field !== "handout";
  if (!screen) return { accents: false, brackets: false, nbsp };
  return { accents: true, brackets: !xyChgk.fieldKeepsBrackets(field), nbsp };
}

// renderRich turns a 4s text element into DOM, mirroring the docx render: inline
// bold/italic/underline/strike/small-caps, links, (screen …), explicit
// (LINEBREAK)/(PAGEBREAK), and (img …) handouts (shown inline). opts.{accents,
// brackets} select print vs. screen mode; opts.nbsp glues non-breaking
// spaces/hyphens into plain text. Styling is applied via the CSSOM (.style.*) to
// stay within the strict CSP.
function renderRich(text: string, imgMap: Map<string, string>, opts: RichOpts = {}): DocumentFragment {
  const screenSide = !!(opts.accents || opts.brackets);
  const nb = (t: string): string => (opts.nbsp ? xyChgk.replaceNoBreak(t) : t);
  const frag = document.createDocumentFragment();
  // An image renders as a block, so it already ends its line; under pre-wrap the
  // source's own newline right after "(img …)" would add a second, empty one.
  let afterImg = false;
  for (let [type, val] of xyChgk.renderRuns(text, opts)) {
    if (afterImg) {
      afterImg = false;
      if (!type && typeof val === "string" && val.startsWith("\n")) val = val.slice(1);
    }
    if (type === "linebreak") { frag.append(el("br")); continue; }
    if (type === "pagebreak") { frag.append(el("hr", { class: "pv-pagebreak" })); continue; }
    if (type === "img") {
      const name = xyChgk.imgName(val);
      const url = imgMap.get(name);
      if (url) frag.append(el("img", { class: "pv-img", src: url, alt: name }));
      else frag.append(el("span", { class: "pv-img-missing", dataset: { img: name }, text: `[изображение: ${name}]` }));
      afterImg = true;
      continue;
    }
    if (type === "screen") {
      const sv = val as ScreenValue;
      frag.append(document.createTextNode(nb((screenSide ? sv.for_screen : sv.for_print) || "")));
      continue;
    }
    if (type === "hyperlink") {
      frag.append(el("a", { class: "pv-link", href: val, target: "_blank", rel: "noopener noreferrer", text: val }));
      continue;
    }
    if (!type) { frag.append(document.createTextNode(nb(val as string))); continue; }
    const span = el("span", { text: nb(val as string) });
    if (type.includes("italic")) span.style.fontStyle = "italic";
    if (type.includes("bold")) span.style.fontWeight = "bold";
    if (type.includes("underline")) span.style.textDecoration = "underline";
    if (type === "strike") span.style.textDecoration = "line-through";
    if (type === "sc") span.classList.add("pv-sc");
    frag.append(span);
  }
  return frag;
}

// renderFieldBody renders a field value, turning a chgksuite "- …" list into a
// numbered 1./2./… list (with an optional preamble) — this is also how blitz /
// duplet questions and multi-part answers render. Otherwise a plain rich run.
// Works for every field (question, answer, source, comment, …), not just sources.
function renderFieldBody(text: string, imgMap: Map<string, string>, opts: RichOpts): DocumentFragment {
  const frag = document.createDocumentFragment();
  const lst = xyChgk.splitList(text);
  if (lst.items) {
    if (lst.preamble.trim()) frag.append(renderRich(lst.preamble, imgMap, opts));
    const box = el("div", { class: "pv-list" });
    lst.items.forEach((it, i) => {
      const li = el("div", { class: "pv-list-item" }, el("span", { class: "pv-list-num", text: `${i + 1}.` }));
      const body = el("div", { class: "pv-list-body" });
      body.append(renderRich(it, imgMap, opts));
      li.append(body);
      box.append(li);
    });
    frag.append(box);
  } else {
    frag.append(renderRich(lst.preamble, imgMap, opts));
  }
  return frag;
}

// pvSmallCls: sources and authors are set smaller, like the docx/PDF exports
// (12pt body → 10pt).
function pvSmallCls(field: string): string {
  return field === "source" || field === "author" ? "pv-small" : "";
}

// pvField renders a "Label: value" line, numbering any "- …" list. The caption
// rules (a "!!Label" override, the plural source label) are xyChgk's, shared with
// the copy targets.
function pvField(field: string, text: string, imgMap: Map<string, string>, screen: boolean, cls: string): HTMLElement {
  const cap = xyChgk.fieldCaption(field, text);
  const node = el("div", { class: "pv-field" + (cls ? " " + cls : "") },
    el("strong", { class: "pv-label", text: cap.label + ": " }));
  node.append(renderFieldBody(cap.text, imgMap, fieldOpts(field, screen)));
  return node;
}

// pvEditBtn builds the small inline ✏️ button rendered just before each preview
// block's leading label (e.g. "✏️Вопрос 1."): it hides the preview and drops
// straight into the card editor, remembering the preview + card so the card's
// ↩️ back button can restore this exact preview scrolled to the same question.
// The preview is hidden rather than closed: the card takes over its place on the
// overlay stack (openCard replaces it), so this is one step forward, not two.
function pvEditBtn(card: BoardCard): HTMLElement {
  const list = previewListRef;
  return el("button", {
    class: "pv-edit", title: "Редактировать карточку", "aria-label": "Редактировать карточку",
    onclick: (e: Event) => {
      e.stopPropagation();
      const group = previewGroupMode;
      hidePreview();
      void cardDetail.openCard(card, { returnTo: list ? { listId: list.id, cardId: card.id, group } : null });
    },
  }, icon("pencil"));
}

// renderPreviewCard renders one card the way the docx export would: a question
// card becomes a numbered question with its answer/zachet/etc.; meta/heading/
// section/editor/date cards become their corresponding paragraphs/headings.
// `edit` adds the ✏️ jump-to-editor button — only the list preview passes it; the
// card-detail preview (already inside the editor) leaves it off.
function renderPreviewCard(card: PvCard, number: string | null, imgMap: Map<string, string>, screen: boolean, edit = false): HTMLElement {
  const blocks = xyChgk.parseBlocks(card.desc);
  const find = (t: string) => blocks.find((b) => b.type === t);

  if (card.kind === "question" || find("question")) {
    const wrap = el("article", { class: "pv-q", dataset: { cardId: card.id } });
    const handout = find("handout");
    if (handout) wrap.append(pvField("handout", handout.text, imgMap, screen, "pv-handout"));
    // Question line: small inline ✏️ (edit lists only) + bold "Вопрос N." label
    // (overridable) + question text (which may itself be a blitz/duplet list).
    const qov = xyChgk.applyOverride(xyChgk.questionText(card.desc));
    const qLabel = qov.label || "Вопрос";
    const qline = el("div", { class: "pv-q-text" });
    if (edit) qline.append(pvEditBtn(card as BoardCard));
    qline.append(el("strong", { class: "pv-label", text: `${qLabel}${number ? " " + number : ""}. ` }));
    qline.append(renderFieldBody(qov.text, imgMap, fieldOpts("question", screen)));
    wrap.append(qline);
    for (const f of ["answer", "zachet", "nezachet", "comment", "source", "author"]) {
      const b = find(f);
      if (b) wrap.append(pvField(f, b.text, imgMap, screen, pvSmallCls(f)));
    }
    return wrap;
  }

  // Non-question card: render each block by type (never screen-transformed).
  const wrap = el("div", { class: "pv-block", dataset: { cardId: card.id } });
  for (const b of blocks) {
    if (b.type === "num" || b.type === "numnum") continue; // numbering directive only
    if (b.type === "heading" || b.type === "ljheading") {
      const h = el("h2", { class: "pv-heading" });
      h.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(h);
    } else if (b.type === "section") {
      const h = el("h3", { class: "pv-section" });
      h.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(h);
    } else if (PV_LABELS[b.type]) {
      wrap.append(pvField(b.type, b.text, imgMap, false, pvSmallCls(b.type)));
    } else {
      const p = el("p", { class: "pv-meta" });
      p.append(renderRich(b.text, imgMap, { nbsp: true }));
      wrap.append(p);
    }
  }
  // Inline ✏️ tucked in front of the block's first line (edit lists only).
  if (edit) (wrap.firstElementChild || wrap).prepend(pvEditBtn(card as BoardCard));
  return wrap;
}

// previewCtx holds the resolved cards/numbers/images for the open preview so the
// screen-mode toggle can re-render without refetching attachments.
let previewCtx: { cards: BoardCard[]; numbers: Array<string | null>; imgMap: Map<string, string> } | null = null;
let previewListRef: BoardList | null = null; // the list currently shown in the preview overlay
let previewGroupMode = false; // true when the overlay shows the whole group

function renderPreviewBody(screen: boolean): void {
  const body = byId("previewBody");
  body.replaceChildren();
  if (!previewCtx) return;
  const { cards, numbers, imgMap } = previewCtx;
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], imgMap, screen, true)));
}

function closePreview(): void { overlayStack.pop(); }

function hidePreview(): void {
  previewOverlay.hidden = true;
  previewCtx = null;
  previewListRef = null;
  previewGroupMode = false;
  byId("previewBody").replaceChildren();
}

// previewList opens the preview modal and renders the whole list. Test lists show
// their tester summary (the same line the copy action produces); question lists
// render docx-style — text instantly, image handouts resolved + filled in after.
// wholeGroup previews the list's entire group (non-test members, board order,
// continuous numbering) — the same scope its export/handouts cover.
async function previewList(list: BoardList, wholeGroup = false): Promise<void> {
  const group = wholeGroup && list.groupId != null ? groupById(list.groupId) : null;
  const scopeLists = group ? listsInGroup(list.groupId as number) : [list];
  // The preview is what the pack will look like, so a versioned card is folded
  // the way exportSource folds it — every wording page-broken under one number.
  // (The card editor's Просмотр is the other thing: there you are reading ONE
  // version, so it renders the body it is handed.)
  const cards = scopeLists.flatMap((l) => cardsOf(l.id))
    .map((c) => (xyChgk.versionCount(c.desc) > 1 ? { ...c, desc: xyChgk.composeVersions(c.desc) } : c));
  const title = byId("previewTitle");
  if (group) title.replaceChildren(...iconed("link", group.name || "связанные списки"));
  else title.textContent = list.title || "Предпросмотр";
  const body = byId("previewBody");
  body.replaceChildren();
  previewCtx = null;
  previewListRef = list;
  previewGroupMode = !!group;
  q(".preview-screen-toggle").hidden = false;
  previewOverlay.hidden = false;
  overlayStack.open({ el: previewOverlay, close: hidePreview });
  if (!cards.length) {
    body.append(el("p", { class: "pv-empty", text: "В списке нет карточек." }));
    return;
  }
  // Text renders straight away (cards are decrypted at board load); image
  // handouts resolve in the background and replace their placeholders as they
  // arrive, so a long list is readable immediately.
  // Match the board: a grouped list numbers continuously across its group. In
  // whole-group mode the cards ARE the concatenated group, so number them flat.
  const numbers = !group && list.groupId != null
    ? (groupNumbering(listsInGroup(list.groupId)).get(list.id) || [])
    : xyChgk.numberQuestionCards(cards);
  const imgMap = new Map<string, string>();
  const ctx = { cards, numbers, imgMap };
  previewCtx = ctx;
  renderPreviewBody(byId<HTMLInputElement>("previewScreen").checked);
  await attachments.resolveImages(cards, xyChgk.imageRefs(cards), (name, url) => {
    imgMap.set(name, url);
    // Ignore a close (or another list's preview) that happened during the await.
    if (previewCtx === ctx && !previewOverlay.hidden) fillPreviewImages(body, imgMap);
  });
}

byId("previewScreen").addEventListener("change", (e) => renderPreviewBody((e.target as HTMLInputElement).checked));
byId("previewClose").addEventListener("click", closePreview);
previewOverlay.addEventListener("pointerdown", (e) => { if (e.target === previewOverlay) closePreview(); });

// ---- commit card move (rank recompute from DOM order) ----
async function commitCardMove(cardId: number, targetListId: number, body: HTMLElement): Promise<void> {
  const card = state.cards.find((c) => c.id === cardId);
  if (!card) return;
  const order = [...body.querySelectorAll<HTMLElement>(".kcard")].map((n) => Number(n.dataset.cardId));
  const rankOf = (id: number): string | null => { const c = state.cards.find((x) => x.id === id); return c ? c.rank : null; };
  const rank = rankAfterMove(order, cardId, rankOf);
  card.listId = targetListId;
  card.rank = rank;
  setStatus("saving");
  try {
    await patch("patchCard", `/api/cards/${cardId}`, { list_id: targetListId, rank });
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); void unlock.load(); }
}

// ---- copy a question to the clipboard for a test session ----
// questionNumberFor returns the display number this question card would show on
// the board (auto-assigned or directive-driven), matching the kanban preview.
function questionNumberFor(card: PreviewCardLike): string | null {
  if (!card || card.kind !== "question") return null;
  const list = state.lists.find((l) => l.id === card.listId);
  // Match the board: a grouped list numbers continuously across its group.
  if (list && list.groupId != null) {
    const nums = groupNumbering(listsInGroup(list.groupId)).get(list.id) || [];
    const idx = cardsOf(card.listId).findIndex((c) => c.id === card.id);
    return idx >= 0 ? nums[idx] : null;
  }
  const cards = cardsOf(card.listId);
  const numbers = xyChgk.numberQuestionCards(cards);
  const idx = cards.findIndex((c) => c.id === card.id);
  return idx >= 0 ? numbers[idx] : null;
}

// ---- card detail + timeline ----
// Both live in their own modules (carddetail.js / timeline.js), wired to what
// the board owns. The card module is created first so its document-level
// listeners (the Escape handler) register in the same order board.js had; the
// timeline seam it needs binds lazily through arrow closures.
let timeline: Timeline;
const attachments = createAttachments({
  mustDK,
  openCardId: () => cardDetail.openCardId(),
  popupMenu,
  timeline: {
    load: (cardId) => timeline.load(cardId),
    setAttachments: (list) => timeline.setAttachments(list),
  },
  onCommentImage: (attId) => timeline.addDraftImage(attId),
});

const cardDetail = createCardDetail({
  boardId,
  getState: () => state,
  getDK: () => dk,
  verbs: { create, patch, put, del },
  setStatus,
  render,
  cardsOf,
  labelById,
  renderLabelPicker,
  paintLabels,
  questionNumberFor,
  forgetCardLabels,
  preview: { renderPreviewCard, resolveImages: attachments.resolveImages, imageRefs: xyChgk.imageRefs, fillPreviewImages, previewList },
  attachments,
  popupMenu,
  readMarkers: { refreshCardUnreadDot, renderNotifBadge },
  timeline: {
    load: (cardId) => timeline.load(cardId),
    events: () => timeline.events(),
    resetFilter: () => timeline.resetFilter(),
    readBuckets: () => timeline.readBuckets(),
    ensureVisible: (type) => timeline.ensureVisible(type),
    commentDraft: () => timeline.commentDraft(),
    postComment: () => timeline.postComment(),
    clearCommentDraft: () => timeline.clearCommentDraft(),
  },
});
timeline = createTimeline({
  getState: () => state,
  getDK: () => dk,
  post,
  popupMenu,
  plural,
  card: {
    openCardId: () => cardDetail.openCardId(),
    copyCommentLink: (id) => { void cardDetail.copyCommentLink(id); },
  },
  labelName: (id) => { const l = labelById(id); return l ? l.name : ""; },
  cardSessions: (cardId) => playingsOf(cardId).map((id) => ({ id, label: sessionName(id) })),
  sessionName,
  attachments: { url: attachments.attachmentUrl, download: attachments.download },
});

// ---- labels ----
// The card's «Метки» and «Тесты» are two separate pickers (ADR-0004): a label is
// the author's view of the question, a Playing is where it was tested, and a
// label scoped to a Playing is what the testers thought there. Mixing them into
// one list was what made «взяли» multiply by the number of tests.


function labelChip(lbl: BoardLabel, onRemove: () => void, title: string): HTMLElement {
  return el("span", { class: "label-pick is-on", dataset: { c: lbl.color }, title: lbl.name },
    el("span", { class: "label-pick-name", text: lbl.name }),
    el("button", {
      class: "label-pick-x", type: "button", text: "×",
      title, "aria-label": `${title}: ${lbl.name}`,
      onclick: onRemove,
    }));
}

function renderLabelPicker(card: BoardCard): void {
  const picker = byId("labelPicker");
  picker.replaceChildren();
  const own = assignmentsOf(card.id, null);
  for (const a of own) {
    const lbl = labelById(a.labelId);
    if (lbl) picker.append(labelChip(lbl, () => { void setLabel(card, lbl, null, false); }, "Снять метку"));
  }
  if (!own.length) picker.append(el("span", { class: "label-empty", text: "меток нет" }));

  renderPlayings(card);
  renderSeen(card);
  closeLabelAddPopup();
  paintLabels();
}

function renderPlayings(card: BoardCard): void {
  const box = byId("cardPlayings");
  box.replaceChildren();
  const ids = playingsOf(card.id);
  if (!ids.length) {
    box.append(el("span", { class: "label-empty", text: "тестов нет" }));
    return;
  }
  for (const sid of ids) {
    const head = el("div", { class: "playing-head" },
      el("span", { class: "playing-name", text: sessionName(sid) }),
      el("button", {
        class: "label-pick-x", type: "button", text: "×",
        title: "Убрать тест с вопроса", "aria-label": `Убрать тест ${sessionName(sid)}`,
        onclick: () => { void removePlaying(card, sid); },
      }));
    const chips = el("div", { class: "playing-labels" });
    for (const a of assignmentsOf(card.id, sid)) {
      const lbl = labelById(a.labelId);
      if (lbl) chips.append(labelChip(lbl, () => { void setLabel(card, lbl, sid, false); }, "Снять отметку теста"));
    }
    chips.append(el("button", {
      class: "input playing-add", type: "button", text: "＋",
      title: "Добавить метку этого теста",
      onclick: (e: Event) => { openLabelAddPopup(sid, (e.currentTarget as HTMLElement).parentElement as HTMLElement); },
    }));
    box.append(el("div", { class: "playing" }, head, chips));
  }
}

// renderSeen writes who saw THIS question beyond the people the tour already
// names. A tour's preamble lists whoever tested most of it, and those people
// know not to play; the ones who matter here are the extras — a question moved
// in from another tournament, seen by three people nobody has warned. Showing
// the full list again would bury them.
function renderSeen(card: BoardCard): void {
  const node = byId("cardSeen");
  const mine = sessionsOfCard(card.id);
  if (!mine.length) { node.hidden = true; return; }

  const list = state.lists.find((l) => l.id === card.listId);
  const named = list ? testerList.tourPicked(list) : new Set<number>();
  const common = new Set<string>();
  for (const sid of named) {
    const m = sessionMeta(sid);
    for (const t of (m && m.testers) || []) common.add((t.text || "").trim());
  }

  const extras = mine.map((m) => ({
    ...m,
    testers: (m.testers || []).filter((t) => !common.has((t.text || "").trim())),
  }));
  const line = whoSaw(common.size ? extras : mine);
  node.hidden = !line;
  if (!line) return;

  const label = common.size ? "Видели вопрос, кроме общих тестеров списка: " : "Видели: ";
  node.replaceChildren(
    el("span", { class: "seen-label", text: label }),
    el("span", { class: "seen-names", text: line }),
    el("button", {
      class: "input seen-copy", type: "button",
      title: "Скопировать",
      onclick: () => { void cardDetail.copyPlain(label + line); },
    }, icon("clipboard")),
  );
}

function closeLabelAddPopup(): void {
  for (const popup of document.querySelectorAll(".label-add-popup")) popup.remove();
}

// The "create a new label" form is authored in board.dopeui but does NOT belong
// in the card body: it used to sit there permanently as a third stacked row under
// «Метки», duplicating the popup's job. Detach it once at boot and keep the node;
// openLabelAddPopup mounts it at the foot of the popup, where "create a label"
// actually belongs. Handlers bound to the element survive the move.
const newLabelForm = byId<HTMLFormElement>("newLabelForm");
const newLabelColor = colorField(byId("newLabelColor"), LABEL_COLORS[0]);
newLabelForm.remove();

// The compiled pages spell "+" as the ➕ emoji; swap it for the SVG plus.

// setLabel adds or removes ONE assignment. The card's whole set goes up together
// because the endpoint replaces it — cheap, and it keeps the offline mirror's
// view of a card in a single op.
async function setLabel(card: BoardCard, lbl: BoardLabel, sessionId: number | null, adding: boolean): Promise<void> {
  const rest = state.cardLabels.filter((a) =>
    a.cardId !== card.id || a.labelId !== lbl.id || a.sessionId !== sessionId);
  const next = adding ? [...rest, { cardId: card.id, labelId: lbl.id, sessionId }] : rest;
  try {
    const events = [{
      type: adding ? "label_add" : "label_remove",
      payload_enc: await xyCrypto.encField(mustDK(), JSON.stringify({ label: lbl.name, label_id: lbl.id })),
    }];
    await put("setCardLabels", `/api/cards/${card.id}/labels`, {
      labels: next.filter((a) => a.cardId === card.id).map((a) => ({ label_id: a.labelId, session_id: a.sessionId })),
      events,
    });
    state.cardLabels = next;
    renderLabelPicker(card);
    render();
    await timeline.load(card.id);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
}

async function addPlaying(card: BoardCard, sessionId: number): Promise<void> {
  await writePlayings(card, [...new Set([...playingsOf(card.id), sessionId])]);
}

// removePlaying takes the labels scoped to it — a label scoped to a playing that
// no longer exists cannot be read (ADR-0004) — so the confirmation names how many.
async function removePlaying(card: BoardCard, sessionId: number): Promise<void> {
  const scoped = assignmentsOf(card.id, sessionId).length;
  const what = scoped
    ? `Снять тест «${sessionName(sessionId)}» и ${scoped} ${plural(scoped, "метку", "метки", "меток")} на нём?`
    : `Снять тест «${sessionName(sessionId)}» с вопроса?`;
  if (!confirm(what)) return;
  await writePlayings(card, playingsOf(card.id).filter((id) => id !== sessionId));
}

async function writePlayings(card: BoardCard, ids: number[]): Promise<void> {
  try {
    await put("setCardSessions", `/api/cards/${card.id}/sessions`, { session_ids: ids });
    state.cardSessions = state.cardSessions.filter((p) => p.cardId !== card.id)
      .concat(ids.map((sessionId) => ({ cardId: card.id, sessionId })));
    const keep = new Set(ids);
    state.cardLabels = state.cardLabels.filter((a) =>
      a.cardId !== card.id || a.sessionId == null || keep.has(a.sessionId));
    renderLabelPicker(card);
    render();
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
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
      listBox.append(el("span", { class: "label-empty", text: opts.items.length ? "ничего не найдено" : opts.empty }));
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
    paintLabels();
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
  const card = state.cards.find((c) => c.id === cardDetail.openCardId());
  if (!card) return;
  const taken = new Set(assignmentsOf(card.id, sessionId).map((a) => a.labelId));
  const pool = sortLabels(state.labels.filter((l) => !taken.has(l.id)), state.cardLabels);
  filteredPopup({
    anchor: anchorEl || byId("labelAddRow"),
    items: pool.map((l) => ({ id: l.id, name: l.name, color: l.color })),
    placeholder: "Фильтр меток…",
    empty: state.labels.length ? "все метки доски уже добавлены" : "меток на доске нет",
    // Creating a label from inside a test would still make a plain board label,
    // so the form belongs only to the author's own section.
    extra: sessionId == null ? newLabelForm : undefined,
    onPick: (item) => {
      const lbl = labelById(item.id);
      if (lbl) void setLabel(card, lbl, sessionId, true);
    },
  });
}

byId("labelAddBtn").addEventListener("click", () => openLabelAddPopup(null));

// openPlayingAddPopup offers the board's tests this question is not yet marked
// with — the second of the card's two pickers.
function openPlayingAddPopup(): void {
  const card = state.cards.find((c) => c.id === cardDetail.openCardId());
  if (!card) return;
  const on = new Set(playingsOf(card.id));
  const pool = state.sessions.filter((s) => !on.has(s.id))
    .map((s) => ({ id: s.id, name: sessionName(s.id), date: (sessionMeta(s.id) || { date: "" }).date }))
    .sort((a, b) => (b.date || "").localeCompare(a.date || "") || b.id - a.id);
  filteredPopup({
    anchor: byId("playingAddRow"),
    items: pool.map((s) => ({ id: s.id, name: s.name })),
    placeholder: "Фильтр тестов…",
    empty: state.sessions.length ? "все тесты доски уже отмечены" : "тестов на доске нет",
    onPick: (item) => { void addPlaying(card, item.id); },
  });
}

byId("playingAddBtn").addEventListener("click", openPlayingAddPopup);

// ---- the Тесты panel + the label editor ----

const sessionsPanel = createSessionsPanel({
  boardId,
  el,
  byId,
  sessions: () => state.sessions,
  boardName: () => state.name,
  defaultTimezone: () => state.timezone,
  defaultCities: () => {
    const c = state.announceCities;
    return Array.isArray(c) ? (c as AnnounceCity[]) : [];
  },
  // How many questions this test was played at — the number the Тесты panel
  // shows and the tester-list modal counts coverage from.
  playedCount: (sessionId) =>
    new Set(state.cardSessions.filter((p) => p.sessionId === sessionId).map((p) => p.cardId)).size,
  createSession: async (meta) => {
    const res = await create("createSession", `/api/boards/${boardId}/sessions`, {
      meta_enc: await xyCrypto.encField(mustDK(), meta),
    });
    const id = res.id as number;
    state.sessions.push({ id, meta, createdAt: nowStamp() });
    sessionMetaCache.delete(id);
    return id;
  },
  patchSession: async (id, meta) => {
    await patch("patchSession", `/api/sessions/${id}`, { meta_enc: await xyCrypto.encField(mustDK(), meta) });
    const s = state.sessions.find((x) => x.id === id);
    if (s) s.meta = meta;
    sessionMetaCache.delete(id);
  },
  deleteSession: async (id) => {
    await del("deleteSession", `/api/sessions/${id}`);
    state.sessions = state.sessions.filter((s) => s.id !== id);
    // The label survives — it is an ordinary board label. What goes is the
    // playings and the assignments scoped to them (ADR-0004).
    state.cardSessions = state.cardSessions.filter((p) => p.sessionId !== id);
    state.cardLabels = state.cardLabels.filter((a) => a.sessionId !== id);
    sessionMetaCache.delete(id);
  },
  copyText: (text: string) => cardDetail.copyPlain(text),
  loadNotes: async (sessionId) => {
    const raw = (await fetchJSON(`/api/sessions/${sessionId}/timeline`)) as Array<{
      payload_enc: string; card_id?: number; created_at: string; author_user_id?: number | null;
    }>;
    const out: Array<{ text: string; card: number | null; when: string; author: string }> = [];
    for (const e of raw) {
      let text = "";
      try { text = await xyCrypto.decField(mustDK(), e.payload_enc || ""); } catch (_) { continue; }
      // Same author resolution the card's лента uses, so the two read alike.
      out.push({ text, card: e.card_id ?? null, when: e.created_at, author: eventAuthor(e, state.me, state.memberNames) });
    }
    return out;
  },
  addNote: async (sessionId, text) => {
    await post("addSessionNote", `/api/sessions/${sessionId}/comments`, {
      payload_enc: await xyCrypto.encField(mustDK(), text),
    });
  },
  modal,
  render,
});


// NB: `newLabelForm` (the retained node), not getElementById — the form is
// detached from the document above and lives inside the popup while it is open.
newLabelForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = byId<HTMLInputElement>("newLabelName").value.trim();
  if (!name) return;
  try {
    const lbl = await labelsEditor.createLabel(name, newLabelColor.value());
    byId<HTMLInputElement>("newLabelName").value = "";
    const card = state.cards.find((c) => c.id === cardDetail.openCardId());
    // The form is reachable only from inside the add-label popup, so naming a
    // label there means you want it ON this card — assign it instead of making
    // the user reopen the popup to pick what they just typed.
    if (card) await setLabel(card, lbl, null, true);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
});

// ---- the panels ----
// Every feature the ☰ and the list ⋯ menus offer lives in its own module and
// registers here, in menu order (panels.ts). The two menus render the registry.
const shell = createPanelShell(modal("panel"), { title: q("#panelOverlay .appearance-modal-title"), body: byId("panelBody") });
const rewrites = createRewrites(board);
const labelsEditor = createLabelsEditor(board);
const testerList = createTesterList(board, shell, cardDetail);
const listsManage = createListsManage(board);
const mass = createMassPanel(board, { kanban, cardDetail, forgetCardLabels, paintLabels });

registerPanel(
  { id: "rename-board", menu: "board", icon: "pencil", label: "Переименовать доску", title: "Изменить название доски", open: () => { void renameBoard(); } },
  listsManage.panel,
  mass.panel,
  createImportPanel(board, renderPreviewCard),
  { id: "sessions", menu: "board", icon: "flask-conical", label: "Тесты", title: "Тест-сессии доски: кто когда играл, приглашение со временем начала", open: () => sessionsPanel.open() },
  labelsEditor.panel,
  { id: "members", menu: "board", icon: "users", label: "Участники доски", title: "Поделиться доской: добавить или убрать участников", open: () => boardMembers.open() },
  rewrites.panels[0],
  createReplacePanel(board, rewrites),
  rewrites.panels[1],
  {
    id: "forget-password", menu: "board", icon: "lock", label: "Забыть пароль доски", title: "Забыть пароль доски на этом устройстве",
    open: async () => {
      await xyCrypto.forgetDK(boardId);
      // The names this board contributed to the person directory outlive nothing:
      // once the DK is gone its content is ciphertext with no key on this device.
      // Its Search Index goes the same way, and for a sharper reason (ADR-0008):
      // plaintext that outlived its key would keep the board readable with none.
      people.forget(boardId);
      await xySearchIndex.forget(boardId);
      location.reload();
    },
  },
  { id: "delete-board", menu: "board", icon: "trash-2", label: "Удалить доску", title: "Удалить доску со всеми списками и карточками (только владелец)", open: () => { void deleteBoard(); } },

  { id: "add-card", menu: "list", icon: "plus", label: "Добавить карточку", open: (s) => { void cardDetail.addCard(s.list); } },
  { id: "preview", menu: "list", icon: "eye", label: (s) => s.group ? "Предпросмотр списка" : "Предпросмотр", open: (s) => { void previewList(s.list); } },
  { id: "preview-group", menu: "list", icon: "eye", label: "Предпросмотр всей группы", offered: (s) => !!s.group, open: (s) => { void previewList(s.list, true); } },
  testerList.panel,
  createAuthorCountPanel(shell, cardDetail),
  createMoveListPanel(board, cardDetail),
  { id: "rename-list", menu: "list", icon: "pencil", label: "Переименовать список", open: (s) => { void renameList(s.list); } },
  createExportPanel(board, attachments),
  createHandoutsPanel(board, attachments),
  { id: "delete-list", menu: "list", icon: "trash-2", label: "Удалить список", open: (s) => { void deleteList(s.list); } },
);
// Board-level actions live in the burger (☰) menu — sharing (rarely opened) and
// "forget password" (rarely needed) don't warrant header buttons.
window.dopeMenu?.setExtras(boardMenu().map((it) => ({ icon: it.icon, label: it.label, title: it.title, onClick: it.onClick })));

void unlock.boot();

