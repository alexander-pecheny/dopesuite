// board.ts — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { overlayStack } from "./overlaystack.js";
import { modal } from "./modal.js";
import { type Board, boardMenu, createPanelShell, listMenu, listNumbers, listScope, type Panel, registerPanel } from "./panels.js";
import { createRewrites } from "./rewrites.js";
import { createReplacePanel } from "./replace.js";
import { createMoveListPanel } from "./movelist.js";
import { createListsManage, unitsOf } from "./listsmanage.js";
import { createImportPanel } from "./importpack.js";
import { createExportPanel } from "./export.js";
import { createHandoutsPanel } from "./handouts.js";
import { createMassPanel } from "./masspanel.js";
import { createLabelFilter, shownCards } from "./labelfilter.js";
import { createLabelsEditor, sortLabels } from "./labelsedit.js";
import { createTesterList } from "./testerlist.js";
import { createBundleExportPanel } from "./bundleexport.js";
import { createBundleImport } from "./bundleimportpanel.js";
import { createAuthorCountPanel } from "./authorcount.js";
import { xyApp, xySizes } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";
import { fillPreviewImages, renderPreviewCard } from "./preview.js";
import { createCardLabels } from "./cardlabels.js";
import { createBell } from "./bell.js";
import { xyVersions } from "./versions.js";
import { xyHndt } from "./hndt.js";
import { xySync } from "./sync.js";
import { createBoardMembers } from "./boardmembers.js";
import { create as createAttachments } from "./attachments.js";
import { createUnlock } from "./unlock.js";
import { boardOrder, byRank, dragAfterIn, dragAfterInX, rankAfterMove, rankForSlot } from "./dragrank.js";
import { createTimeline, decodeCommentPayload, eventAuthor } from "./timeline.js";
import { createCardDetail, nowStamp } from "./carddetail.js";
import { createDwell, liveTestMode } from "./testmode.js";
import { createTransfer } from "./transfer.js";
import { type AnnounceCity, parseSession, type SessionMeta, sessionLabel, type TitleMode, whoSaw } from "./sessions.js";
import * as people from "./people.js";
import { createSessionsPanel } from "./sessionspanel.js";
import { colorField, labelFill, labelInk, LABEL_COLORS } from "./colorpick.js";
import { anchorPopup } from "./popup.js";
import { plural, xyMass } from "./massaction.js";
import { xySearchIndex } from "./searchindex.js";
import type { DataKey } from "./crypto.js";
import { xyStore } from "./store.js";
import type { OpBody } from "./store.js";
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

// The ☰ is data, and two things change it: the role (which arrives with the
// snapshot, after the menu is first built) and the mass-mode toggle, whose row
// is its own way out.
function refreshBoardMenu(): void {
  window.dopeMenu?.setExtras(boardMenu());
}

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
    forgot: byId("unlockForgot"),
    exit: byId("unlockExit"),
    exitBtn: byId("unlockExitBtn"),
  },
  crypto: xyCrypto,
  sync: xySync,
  net: xyApp,
  status: badge,
  applySizes: xySizes.apply,
  onDK: (k) => { dk = k; },
  exit: { deleteBoard, leaveBoard },
  onState: (s) => {
    Object.assign(state, s);
    sessionMetaCache = new Map();
    document.title = state.name + " · xy";
    refreshBoardMenu(); // the role came with the snapshot; «Удалить доску» wants it
    // Feed the person directory. The tester names are plaintext in hand at this
    // moment, so this costs a pass over a handful of sessions and no decryption.
    people.remember(boardId, state.name, state.sessions.flatMap((s) => parseSession(s.meta).testers));
    render();
    renderNotifBadge();
    // best-effort, online only: the author-name map for timelines, and the
    // invite links whose pending count the ☰ row shows.
    void boardMembers.load().then(refreshBoardMenu);
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

// Everything this device holds for a board, dropped together — the key, the
// names it fed the person directory, its readable words (ADR-0008), and the
// ciphertext Mirror, which is dead weight once the key that read it is gone.
async function forgetLocal(): Promise<void> {
  try { await xyCrypto.forgetDK(boardId); } catch (_) {}
  people.forget(boardId);
  await xySearchIndex.forget(boardId);
  try { await xyStore.deleteSnapshot(boardId); } catch (_) {}
}

// deleteBoard and leaveBoard take the name rather than reading `state`: the
// passphrase overlay offers both to someone who never unlocked the board and so
// has no state at all. Neither act is owner-gated here — the menu offers each to
// the role it belongs to and the server says the rule itself.
async function deleteBoard(name: string): Promise<void> {
  if (!xySync.requireOnline("Удаление доски доступно только онлайн.")) return;
  const warn = "Доска со всеми списками, карточками и вложениями будет скрыта сразу и безвозвратно удалена через 14 дней.";
  const want = (name || "").trim();
  if (want) {
    const typed = prompt(`${warn}\n\nЧтобы подтвердить, введите название доски:`);
    if (typed == null) return;
    if (typed.trim() !== want) { alert("Название не совпало — удаление отменено."); return; }
  } else if (!confirm(`${warn} Продолжить?`)) return;
  try {
    await jdelete(`/api/boards/${boardId}`);
    await forgetLocal();
    location.href = "/";
  } catch (err) { alert("Не удалось удалить: " + errMsg(err)); }
}

async function leaveBoard(name: string): Promise<void> {
  if (!xySync.requireOnline("Выход из доски доступен только онлайн.")) return;
  const what = (name || "").trim() ? `доску «${name.trim()}»` : "эту доску";
  if (!confirm(`Покинуть ${what}? У остальных участников она останется, а чтобы вернуться, понадобится новое приглашение.`)) return;
  try {
    await jdelete(`/api/boards/${boardId}/membership`);
    await forgetLocal();
    location.href = "/";
  } catch (err) { alert("Не удалось покинуть доску: " + errMsg(err)); }
}

// ---- members / sharing ----
// The members/sharing seam lives in boardmembers.js; it caches the roster onto
// `state` (memberNames feeds the timeline's author names) and owns its overlay.
const boardMembers = createBoardMembers(state, boardId, () => refreshBoardMenu());

// ---- read markers (blue dots) + 🔔 activity bell ----
// Every user wants to read every OTHER user's changes; own edits never count.
// Read-tracking is online-only best-effort (like the members roster load): it never
// goes through the sync outbox, so it's simply skipped offline.
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

// ---- test mode (ADR-0012) ----
// The device-local автопилот of a test evening: while a session is live here,
// a minute on an open card — or a comment on it — marks the card with the
// test. The kernel and the rules live in testmode.ts; this block is the
// board's wiring: the dwell watcher on the open card, the topbar badge, and
// the hooks the card detail, the лента and the Тесты panel call into.
const testMode = liveTestMode();
const testDwell = createDwell({
  now: () => Date.now(),
  setTimer: (fn, ms) => window.setTimeout(fn, ms),
  clearTimer: (id) => window.clearTimeout(id as number),
  tryMark: (cardId) => {
    const sid = testMode.sessionFor(boardId);
    if (sid == null) return false; // no mode live: leave the dwell to retry
    if (testMode.allowMark(boardId, cardId)) void markTestOnCard(cardId, sid);
    return true;
  },
});

async function markTestOnCard(cardId: number, sessionId: number): Promise<void> {
  const card = state.cards.find((c) => c.id === cardId);
  if (card) await cardLabels.ensurePlaying(card, sessionId);
}

// The session a comment on this card is born tagged with (timeline dep). A
// card already carrying the test tags freely; a hand-unmarked one tags not at
// all — a tag the card's picker cannot reproduce helps nobody.
function testTagFor(cardId: number): number | null {
  const sid = testMode.sessionFor(boardId);
  if (sid == null) return null;
  if (playingsOf(cardId).includes(sid)) return sid;
  return testMode.allowMark(boardId, cardId) ? sid : null;
}

function setTestMode(sessionId: number | null): void {
  if (sessionId == null) testMode.stop();
  else testMode.start(boardId, sessionId);
  updateTestBadge();
}

const testBadge = el("button", {
  class: "action-icon testmode-badge", type: "button", hidden: true,
  onclick: () => popupMenu(testBadge, [{ label: "Завершить тест-режим", onClick: () => setTestMode(null) }]),
});
byId("notifToggle").before(testBadge);

function updateTestBadge(): void {
  const sid = testMode.sessionFor(boardId);
  testBadge.hidden = sid == null;
  if (sid == null) return;
  const name = sessionName(sid);
  testBadge.title = `Тест-режим: «${name}». Завершить — по клику`;
  testBadge.setAttribute("aria-label", testBadge.title);
  testBadge.replaceChildren(icon("flask-conical"), el("span", { class: "testmode-badge-name", text: name }));
}

// The timer a backgrounded tab throttles is only a wake-up call; coming back
// to the tab, and a once-a-minute tick, re-ask the wall clock — which is also
// how the badge learns the idle hour has run out.
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    testDwell.check();
    updateTestBadge();
  }
});
window.addEventListener("pagehide", () => testDwell.check());
window.setInterval(() => {
  testDwell.check();
  updateTestBadge();
}, 60_000);

// ---- 🔔 bell: the badge and the panel of recent other-authored activity ----
const bell = createBell(board, { toggle: byId("notifToggle"), badge: byId("notifBadge") }, {
  mustDK,
  cardTitle,
  openCard: (card) => cardDetail.openCard(card),
  highlightComment: (id) => cardDetail.highlightComment(id),
});
const renderNotifBadge = (): void => bell.renderBadge();

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
  // render always runs on a fresh snapshot, which is where a session deleted
  // on ANOTHER device shows up — a mode left pointing at it must switch off.
  const liveSid = testMode.sessionFor(boardId);
  if (liveSid != null && !state.sessions.some((s) => s.id === liveSid)) testMode.stop();
  updateTestBadge();
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
      for (const list of run) kanban.append(renderList(list, listNumbers(board, list)));
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
  // Also unconditional, and for the same reason: a label the filter names can be
  // deleted under it, and the bar has to stop naming it.
  labelFilter.renderBar();
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
    const items: MenuItem[] = listMenu(listScope(board, list)).map((it) => ({ icon: icon(it.icon), label: it.label, onClick: it.onClick, divider: it.divider }));
    popupMenu(menuWrap, items);
  });
  menuWrap.append(menuBtn);
  const cards = cardsOf(list.id);
  const headMain = el("div", { class: "klist-headmain" },
    el("span", { class: "klist-title", text: list.title || "(без названия)" }));
  // The numbers belong to the questions, not to the view: they are computed over
  // the WHOLE list and carried across, so a filtered тур reads «1, 4, 7».
  const allNumbers = precomputedNumbers || xyChgk.numberQuestionCards(cards);
  const view = shownCards(cards, allNumbers, labelFilter.keep());
  const shown = view.cards;
  const qCount = cards.filter((c) => c.kind === "question").length;
  const qShown = shown.filter((c) => c.kind === "question").length;
  if (qCount) {
    headMain.append(el("span", {
      class: "klist-count",
      // «1 из 4», not «1 из 4 вопроса»: the word cannot agree with both numbers.
      // The «из» form appears whenever a filter is on, even where it happens to
      // hide nothing, so the head reads the same way across the board.
      text: labelFilter.active() ? `${qShown} из ${qCount}` : questionCountLabel(qCount),
    }));
  }
  const headKids: HTMLElement[] = [];
  if (mass.mode) {
    const ids = shown.map((c) => c.id);
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
  shown.forEach((card, i) => body.append(renderCard(card, view.numbers[i])));
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
    // Under a filter the cards between two visible ones are hidden, so an
    // insertion point among them means nothing — the column only says «drop
    // here» and the drop appends to the list's true end.
    if (labelFilter.active()) { body.classList.toggle("kcards-drop", draggedCardListId() !== list.id); return; }
    const after = dragAfter(body, e.clientY);
    const dragging = document.querySelector(".kcard.dragging");
    if (!dragging) return;
    if (after == null) body.append(dragging);
    else body.insertBefore(dragging, after);
  });
  // dragleave also fires moving between the column's own children, and stripping
  // the outline there makes it flicker for the length of the drag.
  body.addEventListener("dragleave", (e) => {
    if (!body.contains(e.relatedTarget as Node | null)) body.classList.remove("kcards-drop");
  });
  body.addEventListener("drop", (e) => {
    if (!e.dataTransfer?.types.includes("text/xy-card")) return;
    e.preventDefault();
    body.classList.remove("kcards-drop");
    if (cardDragCommitted) return; // ignore a stray second drop from the same gesture
    const cardId = Number(e.dataTransfer.getData("text/xy-card"));
    if (labelFilter.active()) {
      // Reorder is off; only a move to ANOTHER list means anything.
      const card = state.cards.find((c) => c.id === cardId);
      if (!card || card.listId === list.id) return;
      cardDragCommitted = true;
      void commitCardAppend(cardId, list.id);
      return;
    }
    cardDragCommitted = true;
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
  if (card.kind === "question" && xyHndt.handoutForCard(card.desc)) {
    labelRow.append(el("span", { class: "kcard-handout", title: "Раздаточный материал" }, icon("file-text")));
  }
  // Which questions the group has not settled on yet — the card itself shows
  // version 1, and this is the only sign the others exist.
  const versions = xyVersions.versionCount(card.desc);
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

// draggedCardListId is the list the card in flight came from — dragover cannot
// read the dataTransfer (only the drop can), so the dragging node is the source.
function draggedCardListId(): number | null {
  const node = kanban.querySelector<HTMLElement>(".kcard.dragging");
  const card = node ? state.cards.find((c) => c.id === Number(node.dataset.cardId)) : undefined;
  return card ? card.listId : null;
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
    // A rule that would open the menu or double another separates nothing.
    if (it.divider && menu.lastElementChild && !menu.lastElementChild.classList.contains("menu-sep")) {
      menu.append(el("div", { class: "menu-sep", role: "separator" }));
    }
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
const previewOverlay = byId("previewOverlay");

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
      // The preview renders the export-folded card (every wording page-broken
      // under one number); open the real one so the editor sees its versions.
      const real = state.cards.find((c) => c.id === card.id) || card;
      void cardDetail.openCard(real, { returnTo: list ? { listId: list.id, cardId: card.id, group } : null });
    },
  }, icon("pencil"));
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
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], imgMap, screen, pvEditBtn)));
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
    .map((c) => (xyVersions.versionCount(c.desc) > 1 ? { ...c, desc: xyVersions.composeVersions(c.desc) } : c));
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
  const numbers = !group && list.groupId != null ? listNumbers(board, list) : xyChgk.numberQuestionCards(cards);
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
  const order = [...body.querySelectorAll<HTMLElement>(".kcard")].map((n) => Number(n.dataset.cardId));
  const rankOf = (id: number): string | null => { const c = state.cards.find((x) => x.id === id); return c ? c.rank : null; };
  await commitCardTo(cardId, targetListId, rankAfterMove(order, cardId, rankOf));
}

// commitCardAppend is what a drop does while a filter is on: the DOM holds only
// the cards that matched, so a rank read off the visible neighbours would land
// the card at an arbitrary point among the hidden ones. The end of the true list
// is the one position that means the same thing filtered or not.
async function commitCardAppend(cardId: number, targetListId: number): Promise<void> {
  await commitCardTo(cardId, targetListId, rankForSlot(cardsOf(targetListId), "end", cardId));
}

async function commitCardTo(cardId: number, targetListId: number, rank: string): Promise<void> {
  const card = state.cards.find((c) => c.id === cardId);
  if (!card) return;
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
  if (!list) return null;
  const idx = cardsOf(list.id).findIndex((c) => c.id === card.id);
  return idx >= 0 ? listNumbers(board, list)[idx] : null;
}

// ---- card detail + timeline ----
// Both live in their own modules (carddetail.ts / timeline.ts), wired to what
// the board owns. The card module is created first so its document-level
// listeners (the Escape handler) register before the timeline's; the timeline
// seam it needs binds lazily through arrow closures.
let timeline: Timeline;
const attachments = createAttachments({
  ui: {
    message: byId("cardMessage"), list: byId("attachments"), upload: byId("attachUpload"),
    file: byId<HTMLInputElement>("attachFile"), compress: byId<HTMLInputElement>("attachCompress"),
    cardOverlay: byId("cardOverlay"), pasteForm: byId("pasteForm"),
    pasteName: byId<HTMLInputElement>("pasteName"), pasteCompress: byId<HTMLInputElement>("pasteCompress"),
  },
  mustDK,
  openCardId: () => cardDetail.openCardId(),
  popupMenu,
  timeline: {
    load: (cardId) => timeline.load(cardId),
    setAttachments: (list) => timeline.setAttachments(list),
  },
  onCommentImage: (attId) => timeline.addDraftImage(attId),
});

// transfer moves and copies cards within and across boards; the card editor,
// «Массовое действие» and «Переместить список…» share it.
const transfer = createTransfer({ boardId, getState: () => state, getDK: () => dk, verbs: { patch }, cardsOf, labelById });

const cardDetail = createCardDetail({
  boardId,
  transfer,
  ui: {
    addVersion: byId("cardAddVersion"),
    alias: byId<HTMLInputElement>("cardAlias"),
    close: byId<HTMLButtonElement>("cardClose"),
    commentsUnreadDot: byId("commentsUnreadDot"),
    contentUnreadDot: byId("contentUnreadDot"),
    copy: byId("cardCopy"),
    copyBtn: byId("copyBtn"),
    copyMsg: byId("cardCopyMsg"),
    del: byId("cardDelete"),
    desc: byId<HTMLTextAreaElement>("cardDesc"),
    descLabel: byId("cardDescLabel"),
    editTools: byId("cardEditTools"),
    fields: byId("cardFields"),
    insStress: byId("cardInsStress"),
    kind: byId<HTMLSelectElement>("cardKind"),
    link: byId("cardLink"),
    message: byId("cardMessage"),
    overlay: byId("cardOverlay"),
    previewBody: byId("cardPreviewBody"),
    previewScreen: byId<HTMLInputElement>("cardPreviewScreen"),
    save: byId<HTMLButtonElement>("cardSave"),
    timeline: byId("timeline"),
    title: byId("cardDetailTitle"),
    to4s: byId("cardTo4s"),
    tabs: { preview: byId<HTMLButtonElement>("cardTabPreview"), fields: byId<HTMLButtonElement>("cardTabFields"), text: byId<HTMLButtonElement>("cardTabText") },
    typo: byId("cardTypo"),
    versions: byId("cardVersions"),
    viewFields: byId("cardViewFields"),
    viewPreview: byId("cardViewPreview"),
    viewTabs: byId("cardViewTabs"),
    viewText: byId("cardViewText"),
    dirty: {
      cancel: byId("dirtyCancel"),
      discard: byId("dirtyDiscard"),
      message: byId("dirtyMessage"),
      overlay: byId("dirtyOverlay"),
      save: byId("dirtySave"),
    },
    move: {
      board: byId<HTMLSelectElement>("moveBoard"),
      btn: byId("moveBtn"),
      list: byId<HTMLSelectElement>("moveList"),
      pos: byId<HTMLSelectElement>("movePos"),
    },
    listPreview: {
      body: byId("previewBody"),
      overlay: byId("previewOverlay"),
    },
  },
  getState: () => state,
  getDK: () => dk,
  verbs: { create, patch, put, del },
  setStatus,
  render,
  cardsOf,
  labelById,
  renderLabelPicker: (card) => cardLabels.render(card),
  paintLabels,
  questionNumberFor,
  forgetCardLabels,
  preview: { renderPreviewCard, resolveImages: attachments.resolveImages, imageRefs: xyChgk.imageRefs, fillPreviewImages, previewList },
  attachments,
  onOpenCard: (id) => { if (id != null) testDwell.opened(id); else testDwell.closed(); },
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
  ui: {
    timeline: byId("timeline"),
    cardMessage: byId("cardMessage"),
    commentForm: byId<HTMLFormElement>("commentForm"),
    commentInput: byId<HTMLTextAreaElement>("commentInput"),
    threadForm: byId<HTMLFormElement>("threadForm"),
    threadInput: byId<HTMLTextAreaElement>("threadInput"),
    threadBody: byId("threadBody"),
    threadMessage: byId("threadMessage"),
    feedExpand: byId("feedExpand"),
    feedGrid: byId("feedGrid"),
    feedOrder: byId<HTMLSelectElement>("feedOrder"),
    feedFilter: byId<HTMLSelectElement>("feedFilter"),
    feedFilterFull: byId<HTMLSelectElement>("feedFilterFull"),
    feedDiffViewRow: byId("feedDiffViewRow"),
    feedDiffViewFullRow: byId("feedDiffViewFullRow"),
    feedDiffView: byId<HTMLSelectElement>("feedDiffView"),
    feedDiffViewFull: byId<HTMLSelectElement>("feedDiffViewFull"),
    excerptsView: byId<HTMLButtonElement>("excerptsView"),
    excerptsCount: byId("excerptsCount"),
    excerptsBody: byId("excerptsBody"),
  },
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
  testSession: testTagFor,
  onTestComment: (cardId, sid) => { void markTestOnCard(cardId, sid); },
  attachments: { url: attachments.attachmentUrl, download: attachments.download },
});

// ---- the open card's labels, playings and «Видели» (cardlabels.ts) ----
const cardLabels = createCardLabels(board, {
  picker: byId("labelPicker"), playings: byId("cardPlayings"), seen: byId("cardSeen"),
  addRow: byId("labelAddRow"), addBtn: byId("labelAddBtn"),
  playingAddRow: byId("playingAddRow"), playingAddBtn: byId("playingAddBtn"),
  newLabelForm: byId<HTMLFormElement>("newLabelForm"), newLabelName: byId<HTMLInputElement>("newLabelName"), newLabelColor: byId("newLabelColor"),
  message: byId("cardMessage"),
}, {
  mustDK,
  openCardId: () => cardDetail.openCardId(),
  copyPlain: (text) => cardDetail.copyPlain(text),
  tourPicked: (list) => testerList.tourPicked(list),
  createLabel: (name, color) => labelsEditor.createLabel(name, color),
  loadTimeline: (cardId) => timeline.load(cardId),
  paintLabels,
  onPlayingRemoved: (cardId, sessionId) => {
    if (testMode.sessionFor(boardId) === sessionId) testMode.noteUnmarked(boardId, cardId);
  },
});
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
    if (testMode.sessionFor(boardId) === id) setTestMode(null);
  },
  copyText: (text: string) => cardDetail.copyPlain(text),
  activeTestSession: () => testMode.sessionFor(boardId),
  setTestMode,
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


// ---- the panels ----
// Every feature the ☰ and the list ⋯ menus offer lives in its own module and
// registers here, in menu order (panels.ts). The two menus render the registry.
const shell = createPanelShell(modal("panel"), { title: q("#panelOverlay .appearance-modal-title"), body: byId("panelBody") });
const rewrites = createRewrites(board);
const labelsEditor = createLabelsEditor(board);
const testerList = createTesterList(board, shell, cardDetail);
const listsManage = createListsManage(board);
const mass = createMassPanel(board, { kanban, transfer, forgetCardLabels, paintLabels, refreshMenu: refreshBoardMenu });
const labelFilter = createLabelFilter({ board, paintLabels, onChange: () => { refreshBoardMenu(); render(); } });

// starts marks the head of a cluster: the menu draws a rule above it. Order and
// clustering are this file's business — a panel module has no idea what it will
// sit next to.
const starts = <P extends Panel>(p: P): P => ({ ...p, divider: true });

registerPanel(
  // Что на доске
  listsManage.panel,
  labelsEditor.panel,
  labelFilter.panel,
  { id: "sessions", menu: "board", icon: "flask-conical", label: "Тесты", title: "Тест-сессии доски: кто когда играл, приглашение со временем начала", open: () => sessionsPanel.open() },

  // Правки по всей доске
  starts(mass.panel),
  createReplacePanel(board, rewrites),
  rewrites.typograph,

  // Файлы: одно и то же содержимое туда и обратно
  starts(createImportPanel(board, renderPreviewCard, createBundleImport(board, shell))),
  createBundleExportPanel(board, shell),

  // Сама доска
  starts({ id: "rename-board", menu: "board", icon: "pencil", label: "Переименовать доску", title: "Изменить название доски", open: () => { void renameBoard(); } }),
  // The waiting count rides the row: a join request is board-level plaintext, so
  // it has no place in the 🔔 (which reads encrypted card events), and the owner
  // would otherwise never learn someone is queued (ADR-0017).
  {
    id: "members", menu: "board", icon: "users", title: "Поделиться доской: добавить или убрать участников",
    label: () => { const n = boardMembers.pendingCount(); return n ? `Участники доски · ${n}` : "Участники доски"; },
    open: () => boardMembers.open(),
  },
  {
    id: "forget-password", menu: "board", icon: "lock", label: "Забыть пароль доски", title: "Забыть пароль доски на этом устройстве",
    // Once the DK is gone the board is ciphertext with no key on this device, so
    // everything derived from it goes too — the Search Index for a sharper reason
    // (ADR-0008): plaintext outliving its key would keep the board readable with none.
    open: async () => {
      await forgetLocal();
      location.reload();
    },
  },
  { id: "delete-board", menu: "board", icon: "trash-2", label: "Удалить доску", title: "Удалить доску со всеми списками и карточками", offered: () => state.role === "owner", open: () => { void deleteBoard(state.name); } },
  { id: "leave-board", menu: "board", icon: "unlink", label: "Покинуть доску", title: "Выйти из доски — у остальных участников она останется", offered: () => state.role !== "" && state.role !== "owner", open: () => { void leaveBoard(state.name); } },

  // Карточки списка
  { id: "add-card", menu: "list", icon: "plus", label: "Добавить карточку", open: (s) => { void cardDetail.addCard(s.list); } },
  { id: "preview", menu: "list", icon: "eye", label: (s) => s.grouped ? "Предпросмотр списка" : "Предпросмотр", open: (s) => { void previewList(s.list); } },
  { id: "preview-group", menu: "list", icon: "eye", label: "Предпросмотр всей группы", offered: (s) => s.grouped, open: (s) => { void previewList(s.list, true); } },

  // Что в нём насчитывается
  starts(testerList.panel),
  createAuthorCountPanel(shell, cardDetail),

  // Что из него выходит
  starts(createExportPanel(board, attachments)),
  createHandoutsPanel(board, attachments),

  // Сам список
  starts({ id: "rename-list", menu: "list", icon: "pencil", label: "Переименовать список", open: (s) => { void renameList(s.list); } }),
  createMoveListPanel(board, transfer),
  { id: "delete-list", menu: "list", icon: "trash-2", label: "Удалить список", open: (s) => { void deleteList(s.list); } },
);
// Board-level actions live in the burger (☰) menu — sharing (rarely opened) and
// "forget password" (rarely needed) don't warrant header buttons.
refreshBoardMenu();

void unlock.boot();

