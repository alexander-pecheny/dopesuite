// board.ts — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { xyApp, xySizes } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";
import { xySync } from "./sync.js";
import { xyHandoutSession } from "./handoutsession.js";
import { createBoardMembers } from "./boardmembers.js";
import { create as createAttachments } from "./attachments.js";
import type { NamedAttachment } from "./attachments.js";
import { createUnlock } from "./unlock.js";
import { byRank, dragAfterIn, dragAfterInX, rankAfterMove, rankForSlot } from "./dragrank.js";
import { createTimeline, eventAuthor } from "./timeline.js";
import { createCardDetail, nowStamp, testTitle } from "./carddetail.js";
import type { DataKey } from "./crypto.js";
import type { SyncStatus } from "./sync.js";
import type { OpBody } from "./store.js";
import type { ScreenValue } from "./chgk.js";
import type { BoardCard, BoardLabel, BoardList, BoardState } from "./unlock.js";
import type { MembersState } from "./boardmembers.js";
import type { MenuItem, Timeline } from "./timeline.js";
import type { MoveCtx, PreviewCardLike } from "./carddetail.js";

const { fetchJSON, jpost, jpatch, jput, jdelete, el, deriveTitle, plusIcon, checkIcon, swapPlusIcon } = xyApp;
const { keyBetween } = xyRank;

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

const state: LiveState = { role: "editor", name: "", lists: [], groups: [], cards: [], labels: [], cardLabels: {}, members: [], memberNames: {}, me: null, unread: {}, sizes: { ...xySizes.DEFAULT }, defaultAuthor: "", cardTitle: "question" };
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

// The header badge combines a transient per-action state (saving/error) with the
// persistent sync state (offline / queued edits), the latter taking precedence.
let lastOp: "saved" | "saving" | "error" = "saved";
let syncState: Pick<SyncStatus, "online" | "pending" | "syncing"> = { online: true, pending: 0, syncing: false };

function refreshBadge(): void {
  let state: string, title: string;
  if (!syncState.online) {
    state = "offline";
    title = syncState.pending ? `Офлайн · ${syncState.pending} изм. ждут отправки` : "Офлайн";
  } else if (syncState.syncing || syncState.pending > 0) {
    state = "pending";
    title = syncState.pending ? `Синхронизация · осталось ${syncState.pending}` : "Синхронизация…";
  } else if (lastOp === "error") {
    state = "error"; title = "Ошибка";
  } else if (lastOp === "saving") {
    state = "saving"; title = "Подождите";
  } else {
    state = "saved"; title = "Готово";
  }
  statusNode.dataset.state = state;
  statusNode.title = title;
}
function setStatus(s: "saved" | "saving" | "error"): void { lastOp = s; refreshBadge(); }

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
  status: { set: setStatus, onSync: (st) => { syncState = st; refreshBadge(); } },
  applySizes: xySizes.apply,
  onDK: (k) => { dk = k; },
  onState: (s) => {
    Object.assign(state, s);
    titleNode.textContent = state.name;
    document.title = state.name + " · xy";
    render();
    renderNotifBadge();
    void boardMembers.load(); // best-effort: populate the author-name map for timelines (online only)
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

// Board-level actions live in the burger (☰) menu — sharing (rarely opened) and
// "forget password" (rarely needed) don't warrant header buttons.
// dopeMenu.setExtras renders them as actions.
window.dopeMenu?.setExtras([{
  label: "✏️ Переименовать доску",
  title: "Изменить название доски",
  onClick: () => { void renameBoard(); },
}, {
  label: "📋 Управление списками",
  title: "Переупорядочить списки и связать их в группы (списки списков)",
  onClick: () => openListsManage(),
}, {
  label: "📥 Импорт",
  title: "Импортировать пакет вопросов (.4s, .zip или .docx)",
  onClick: () => openImportPick(),
}, {
  label: "👥 Участники доски",
  title: "Поделиться доской: добавить или убрать участников",
  onClick: () => boardMembers.open(),
}, {
  label: "🧹 Исправить оформление Trello",
  title: "Убрать артефакты Trello (двойные переносы, экранирование, смарт-ссылки) во всех карточках",
  onClick: () => { void fixTrelloFormattingBoard(); },
}, {
  label: "🔒 Забыть пароль доски",
  title: "Забыть пароль доски на этом устройстве",
  onClick: async () => {
    await xyCrypto.forgetDK(boardId);
    location.reload();
  },
}, {
  label: "🗑️ Удалить доску",
  title: "Удалить доску со всеми списками и карточками (только владелец)",
  onClick: () => { void deleteBoard(); },
}]);

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
    location.href = "/";
  } catch (err) { alert("Не удалось удалить: " + errMsg(err)); }
}

// fixTrelloFormattingBoard re-applies chgksuite's Trello clean-up (the same fix
// the importer runs) to every already-imported card whose description still
// carries Trello artefacts. Each changed card is re-encrypted and patched with a
// desc_edit timeline entry, so the change is auditable and reversible.
async function fixTrelloFormattingBoard(): Promise<void> {
  const changes: Array<{ card: BoardCard; desc: string }> = [];
  for (const c of state.cards) {
    if (c.kind === "test") continue; // test cards hold JSON, not 4s markup
    const fixed = xyChgk.fixTrelloFormatting(c.desc);
    if (fixed !== c.desc) changes.push({ card: c, desc: fixed });
  }
  if (!changes.length) { alert("Нечего исправлять — оформление уже в порядке."); return; }
  if (!confirm(`Исправить оформление Trello в ${changes.length} карточк(ах)? Описания будут изменены.`)) return;
  setStatus("saving");
  let done = 0;
  try {
    const key = mustDK();
    for (const ch of changes) {
      await patch("patchCard", `/api/cards/${ch.card.id}`, {
        description_enc: await xyCrypto.encField(key, ch.desc),
        desc_event_enc: await xyCrypto.encField(key, JSON.stringify({ before: ch.card.desc, after: ch.desc })),
      });
      ch.card.desc = ch.desc;
      done++;
    }
    setStatus("saved");
    render();
    alert(`Исправлено карточек: ${done}.`);
  } catch (err) {
    setStatus("error");
    alert("Ошибка при исправлении: " + errMsg(err));
  }
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

// renderNotifBadge shows the 🔔 badge iff any card has an unread bucket.
function renderNotifBadge(): void {
  const any = Object.values(state.unread).some((u) => u.content || u.comments);
  notifBadge.hidden = !any;
}

// refreshCardUnreadDot updates a single kanban card's dot in place (cheaper
// than a full render() and doesn't disturb drag state).
function refreshCardUnreadDot(cardId: number): void {
  const node = kanban.querySelector(`.kcard[data-card-id="${cardId}"]`);
  if (!node) return;
  const u = state.unread[cardId];
  const wantDot = !!(u && (u.content || u.comments));
  const existing = node.querySelector(".kcard-unread");
  if (wantDot && !existing) node.append(el("span", { class: "unread-dot unread-dot-corner kcard-unread", title: "Непрочитанные изменения" }));
  else if (!wantDot && existing) existing.remove();
}

// ---- 🔔 bell panel: recent other-authored activity, newest first ----
interface ActivityEvent {
  id: number;
  card_id: number;
  type: string;
  created_at: string;
  unread?: boolean;
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
  document.removeEventListener("keydown", onNotifKey);
}
function onNotifOutside(e: PointerEvent): void {
  if (notifPanelEl && e.target instanceof Node && !notifPanelEl.contains(e.target) && e.target !== notifToggle) closeNotifPanel();
}
function onNotifKey(e: KeyboardEvent): void { if (e.key === "Escape") closeNotifPanel(); }

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
  document.addEventListener("keydown", onNotifKey);

  let events: ActivityEvent[] = [];
  try { events = (await fetchJSON(`/api/boards/${boardId}/activity`)) as ActivityEvent[]; } catch (_) {}
  if (notifPanelEl !== panel) return; // closed while loading
  body.replaceChildren();
  if (!events.length) { body.append(el("div", { class: "notif-empty", text: "Нет новых событий" })); return; }
  for (const ev of events) {
    const card = state.cards.find((c) => c.id === ev.card_id);
    if (!card) continue; // card deleted/moved away since the event was recorded
    const row = el("button", { class: "notif-row", type: "button" });
    if (ev.unread) row.append(el("span", { class: "unread-dot" }));
    // Neutral noun-phrase wording (mirrors renderEvent's own verbs map, gender-
    // agnostic since we don't know the author's grammatical gender).
    const verbs: Record<string, string> = {
      comment: "комментарий", desc_edit: "правка описания",
      label_add: "добавлена метка", label_remove: "снята метка",
      attach_add: "вложение добавлено", attach_remove: "вложение удалено", attach_replace: "вложение заменено",
    };
    const verb = verbs[ev.type] || ev.type;
    const when = new Date(ev.created_at).toLocaleString("ru-RU");
    const bodyWrap = el("div", { class: "notif-row-body" },
      el("div", { class: "notif-row-meta", text: `${eventAuthor(ev, state.me, state.memberNames)} ${verb} · ${cardTitle(card)} · ${when}` }));
    if (ev.type === "comment") {
      let preview = "";
      try { preview = await xyCrypto.decField(mustDK(), ev.payload_enc || ""); } catch (_) {}
      bodyWrap.append(el("div", { class: "notif-row-preview", text: deriveTitle(preview, 120) }));
    }
    row.append(bodyWrap);
    row.addEventListener("click", () => {
      closeNotifPanel();
      void cardDetail.openCard(card).then(() => { if (ev.type === "comment") cardDetail.highlightComment(ev.id); });
    });
    body.append(row);
  }
}

notifToggle.addEventListener("click", () => { if (notifPanelEl) closeNotifPanel(); else void openNotifPanel(); });

const cardsOf = (listId: number): BoardCard[] => state.cards.filter((c) => c.listId === listId).sort(byRank);
const labelById = (id: number) => state.labels.find((l) => l.id === id);

// ---- render ----
const groupById = (id: number) => state.groups.find((g) => g.id === id);

// listsInGroup returns a group's member lists in board (rank) order.
function listsInGroup(groupId: number): BoardList[] {
  return state.lists.filter((l) => l.groupId === groupId).sort(byRank);
}

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
function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10, m100 = n % 100;
  return m100 >= 11 && m100 <= 14 ? many : m10 === 1 ? one : m10 >= 2 && m10 <= 4 ? few : many;
}

function questionCountLabel(n: number): string {
  return `${n} ${plural(n, "вопрос", "вопроса", "вопросов")}`;
}

function render(): void {
  kanban.hidden = false;
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
  const addCardBtn = el("button", { class: "kadd", title: "Добавить карточку", "aria-label": "Добавить карточку" }, plusIcon());
  addCardBtn.addEventListener("click", () => cardDetail.addCard(list));
  const menuBtn = el("button", { class: "kadd", title: "Меню списка", text: "⋯", "aria-haspopup": "true" });
  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    const items: MenuItem[] = [{ icon: plusIcon(), label: "Добавить карточку", onClick: () => cardDetail.addCard(list) }];
    if (list.type === "test") items.push({ label: "👥 Копировать список тестеров", onClick: () => { void cardDetail.copyTesterList(list); } });
    if (list.groupId != null) {
      items.push(
        { label: "🔍 Предпросмотр списка", onClick: () => { void previewList(list); } },
        { label: "🔍 Предпросмотр всей группы", onClick: () => { void previewList(list, true); } },
      );
    } else {
      items.push({ label: "🔍 Предпросмотр", onClick: () => { void previewList(list); } });
    }
    items.push(
      { label: "↔️ Переместить список…", onClick: () => openMoveList(list) },
      { label: "✏️ Переименовать список", onClick: () => { void renameList(list); } },
    );
    // Export / handout generation are question-list features; skip them for
    // test lists (whose cards hold tester sessions, not 4s questions).
    if (list.type !== "test") {
      const grouped = list.groupId != null;
      const suffix = grouped ? " группы" : "";
      items.push(
        { label: `📄 Экспорт${suffix} в docx`, onClick: () => { void exportList(list, "docx"); } },
        { label: `📕 Экспорт${suffix} в PDF`, onClick: () => { void exportList(list, "pdf"); } },
        { label: `📱 Экспорт${suffix} в PDF для телефона`, onClick: () => { void exportList(list, "pdf", true); } },
        { label: grouped ? "🧩 Генерация раздаток (вся группа)" : "🧩 Генерация раздаток", onClick: () => openHandouts(list) },
      );
    }
    items.push({ label: "🗑️ Удалить список", onClick: () => { void deleteList(list); } });
    popupMenu(menuWrap, items);
  });
  menuWrap.append(menuBtn);
  // Test lists get a 🧪 prefix so they stand out from ordinary question lists.
  const titleText = (list.type === "test" ? "🧪 " : "") + (list.title || "(без названия)");
  const cards = cardsOf(list.id);
  const headMain = el("div", { class: "klist-headmain" },
    el("span", { class: "klist-title", text: titleText }));
  const qCount = list.type === "test" ? 0 : cards.filter((c) => c.kind === "question").length;
  if (qCount) headMain.append(el("span", { class: "klist-count", text: questionCountLabel(qCount) }));
  col.append(el("div", { class: "klist-head" }, headMain, addCardBtn, menuWrap));
  if (list.groupId != null) {
    const g = groupById(list.groupId);
    col.append(el("div", { class: "klist-group-tag", title: "Список входит в группу — сквозная нумерация и общий экспорт", text: "🔗" + ((g && g.name) || "связанные списки") }));
  }
  const body = el("div", { class: "kcards", dataset: { listId: list.id } });
  // Grouped lists carry continuous numbering computed across the whole group;
  // standalone lists number from 1.
  const numbers = list.type === "test" ? [] : (precomputedNumbers || xyChgk.numberQuestionCards(cards));
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
    await cleanupTestLabels(removed);
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); alert("Не удалось удалить: " + errMsg(err)); }
}

// cleanupTestLabels runs after cards are deleted: the auto-created green/red
// "взяли / не взяли" labels of a deleted test session are deleted with it,
// unless some remaining card is still marked by them (issue #13 — a board
// otherwise accretes a label pair per deleted session forever). Only the
// test_taken/test_missed kinds are touched: a hand-made label assigned to a
// test card may well be in use elsewhere by design.
async function cleanupTestLabels(deletedCards: BoardCard[]): Promise<void> {
  const candidates = new Set<number>();
  for (const c of deletedCards) {
    const ids = state.cardLabels[c.id] || [];
    if (c.kind === "test") {
      for (const lid of ids) {
        const l = labelById(lid);
        if (l && (l.kind === "test_taken" || l.kind === "test_missed")) candidates.add(lid);
      }
    }
    delete state.cardLabels[c.id]; // drop the dead card's row before the usage scan
  }
  for (const lid of candidates) {
    if (state.cards.some((c) => (state.cardLabels[c.id] || []).includes(lid))) continue;
    try {
      await del("deleteLabel", `/api/labels/${lid}`);
      state.labels = state.labels.filter((l) => l.id !== lid);
    } catch (_) { /* leave the label; it can be removed by hand */ }
  }
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
  // The alias input is on every card's detail, so it must win on every kind —
  // a test card that ignored its alias would be a silent no-op for the user.
  if (card.kind === "test") return aliasOf(card) || testTitle(card.desc);
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

function renderCard(card: BoardCard, number?: string | null): HTMLElement {
  const node = el("div", { class: "kcard kcard-" + (card.kind || "normal"), draggable: "true", dataset: { cardId: card.id }, onclick: () => { void cardDetail.openCard(card); } });
  const labelRow = el("div", { class: "kcard-labels" });
  for (const lid of state.cardLabels[card.id] || []) {
    const lbl = labelById(lid);
    if (lbl) labelRow.append(el("span", { class: "label-chip", title: lbl.name, dataset: { c: lbl.color } }));
  }
  if (labelRow.children.length) node.append(labelRow);
  node.append(renderCardTitle(card, number));
  const u = state.unread[card.id];
  if (u && (u.content || u.comments)) node.append(el("span", { class: "unread-dot unread-dot-corner kcard-unread", title: "Непрочитанные изменения" }));
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
  // color the chips via inline style is disallowed by CSP? inline style attr is allowed (style-src governs <style>/<link>, not the style attribute under CSP3 'unsafe-inline' for attributes? Actually attribute styles need style-src 'unsafe-inline'). Use dataset + a post-pass with CSSOM:
  return node;
}

// Apply label colors through the CSSOM (avoids inline-style CSP issues).
function paintLabels(): void {
  for (const chip of document.querySelectorAll<HTMLElement>(".label-chip[data-c]")) {
    chip.style.backgroundColor = chip.dataset.c || "";
  }
  for (const sw of document.querySelectorAll<HTMLElement>(".label-pick[data-c], .label-swatch[data-c]")) {
    sw.style.backgroundColor = sw.dataset.c || "";
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
  const units: Unit[] = [];
  let i = 0;
  while (i < order.length) {
    const l = order[i];
    if (l.groupId != null) {
      const run: BoardList[] = [];
      while (i < order.length && order[i].groupId === l.groupId) { run.push(order[i]); i++; }
      units.push({ kind: "group", id: l.groupId, key: "g" + l.groupId, lists: run });
    } else {
      units.push({ kind: "list", id: l.id, key: "l" + l.id, lists: [l] });
      i++;
    }
  }
  void applyUnitOrder(units);
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
  }, checkIcon()) as HTMLButtonElement;
  input.addEventListener("input", () => { okBtn.hidden = !input.value.trim(); });
  const typeSel = el("select", { class: "input", "aria-label": "Тип списка" },
    el("option", { value: "normal", text: "вопросы" }),
    el("option", { value: "test", text: "тесты" })) as HTMLSelectElement;
  const typeRow = el("label", { class: "attach-lossless" }, "Тип:", typeSel);
  form.append(el("div", { class: "u-row u-gap-sm" }, input, okBtn), typeRow);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const title = input.value.trim();
    if (!title) return;
    const type = typeSel.value;
    const ranks = [...state.lists].sort(byRank);
    const rank = keyBetween(ranks.length ? ranks[ranks.length - 1].rank : null, null);
    try {
      const titleEnc = await xyCrypto.encField(mustDK(), title);
      const res = await create("createList", `/api/boards/${boardId}/lists`, { title_enc: titleEnc, rank, type });
      state.lists.push({ id: res.id as number, type, rank, groupId: null, title });
      input.value = "";
      okBtn.hidden = true;
      typeSel.value = "normal";
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
      const box = el("input", { type: "checkbox", role: "menuitemcheckbox" }) as HTMLInputElement;
      box.checked = !!it.checked;
      const row = el("label", { class: "menu-item menu-item-check" }, box, it.label);
      row.addEventListener("click", (e) => { e.preventDefault(); close(); it.onClick(); });
      menu.append(row);
      continue;
    }
    // Most items carry their icon inside the label string (emoji); an SVG icon
    // comes as it.icon and takes the emoji's slot before the text.
    menu.append(el("button", {
      class: "menu-item", type: "button", role: "menuitem",
      onclick: () => { close(); it.onClick(); },
    }, it.icon ? [it.icon, " "] : [], it.label));
  }
  function close(): void {
    menu.remove();
    openListMenu = null;
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey);
    window.removeEventListener("scroll", close, true);
    window.removeEventListener("resize", close);
  }
  function onOutside(e: PointerEvent): void {
    if (e.target instanceof Node && !menu.contains(e.target) && !anchor.contains(e.target)) close();
  }
  function onKey(e: KeyboardEvent): void { if (e.key === "Escape") close(); }
  document.body.append(menu);
  // Right-align to the trigger, then clamp inside the viewport; below the
  // trigger unless there is no room, then above.
  const r = anchor.getBoundingClientRect();
  const pad = 8;
  const left = Math.max(pad, Math.min(r.right - menu.offsetWidth, window.innerWidth - menu.offsetWidth - pad));
  let top = r.bottom + 4;
  if (top + menu.offsetHeight > window.innerHeight - pad) top = Math.max(pad, r.top - menu.offsetHeight - 4);
  menu.style.left = left + "px";
  menu.style.top = top + "px";
  openListMenu = { anchor, close };
  document.addEventListener("pointerdown", onOutside, true);
  document.addEventListener("keydown", onKey);
  window.addEventListener("scroll", close, true);
  window.addEventListener("resize", close);
}

// ---- move / copy a whole list (within board → re-rank/duplicate; other board →
// client-side re-encryption of the list title + every card + label reconcile,
// mirroring the per-card move/copy below). The destination board is chosen by its
// (decrypted) name and the insertion position among its lists is selectable. ----

interface MoveBoardItem { id: number; name?: string; name_enc?: string | null; schema_version?: number }

let listMoveSrc: BoardList | null = null;  // the list being moved/copied
let listMoveCtx: MoveCtx | null = null;  // destination board ctx (from loadMoveBoard)

function openMoveList(list: BoardList): void {
  listMoveSrc = list;
  byId("moveListMessage").textContent = "";
  byId("moveListOverlay").hidden = false;
  void populateMoveListBoards();
}
function closeMoveList(): void { byId("moveListOverlay").hidden = true; }

// populateMoveListBoards fills the board <select> with decrypted board names
// (current board first/default), then loads the chosen board's list positions.
async function populateMoveListBoards(): Promise<void> {
  const sel = byId<HTMLSelectElement>("moveListBoard");
  sel.replaceChildren();
  let boards: MoveBoardItem[] = [];
  try { boards = (await fetchJSON("/api/boards")) as MoveBoardItem[]; } catch (_) {}
  if (!boards.some((b) => b.id === boardId)) boards.unshift({ id: boardId, name_enc: null });
  for (const b of boards) {
    let label = "доска #" + b.id;
    if (b.id === boardId) label = (state.name || label) + " (эта доска)";
    else if ((b.schema_version ?? 0) >= 2) label = b.name || label; // plaintext name, no key needed
    else {
      try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc || ""); }
      catch (_) {}
    }
    sel.append(el("option", { value: b.id, text: label }));
  }
  sel.value = String(boardId);
  await onMoveListBoardChange();
}

// onMoveListBoardChange loads the destination board (prompting for its password
// when it isn't unlocked — see loadMoveBoard→ensureDK) and rebuilds the position
// <select> with one slot per existing list ("в конец" appends).
async function onMoveListBoardChange(): Promise<void> {
  const posSel = byId<HTMLSelectElement>("moveListPos");
  const bid = Number(byId<HTMLSelectElement>("moveListBoard").value);
  posSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
  try { listMoveCtx = await cardDetail.loadMoveBoard(bid); }
  catch (err) {
    listMoveCtx = null;
    posSel.replaceChildren(el("option", { value: "", text: errMsg(err) }));
    return;
  }
  const ctx = listMoveCtx, src = listMoveSrc;
  const lists = ctx.lists.filter((l) => !(ctx.boardId === boardId && src && l.id === src.id));
  posSel.replaceChildren(el("option", { value: "end", text: "в конец" }));
  for (let i = 1; i <= lists.length; i++) posSel.append(el("option", { value: String(i), text: `позиция ${i}` }));
  posSel.value = "end";
}

async function doMoveListCopy(remove: boolean): Promise<void> {
  const src = listMoveSrc, ctx = listMoveCtx;
  if (!src || !ctx) return;
  // A cross-board copy re-encrypts every card, comment and attachment — seconds
  // during which the modal stays open; a second click used to start a second
  // copy and leave a duplicated list on the target board.
  const copyBtn = byId<HTMLButtonElement>("moveListCopyBtn");
  const moveBtn = byId<HTMLButtonElement>("moveListMoveBtn");
  if (copyBtn.disabled) return;
  copyBtn.disabled = moveBtn.disabled = true;
  try {
    await moveListCopyLocked(remove, src, ctx);
  } finally {
    copyBtn.disabled = moveBtn.disabled = false;
  }
}

async function moveListCopyLocked(remove: boolean, src: BoardList, ctx: MoveCtx): Promise<void> {
  const targetBid = ctx.boardId;
  const sameBoard = targetBid === boardId;
  const msg = byId("moveListMessage");
  const rank = rankForSlot(ctx.lists, byId<HTMLSelectElement>("moveListPos").value, sameBoard ? src.id : undefined);
  const srcCards = cardsOf(src.id);
  const type = src.type || "normal";

  // A grouped list must stay consecutive with its group, so reordering it on the
  // same board goes through «Управление списками» (which moves the whole group as
  // a unit). Copying it, or moving it to another board, is still fine.
  if (sameBoard && remove && src.groupId != null) {
    msg.textContent = "Список входит в группу — измените порядок через «Управление списками».";
    return;
  }

  // Same-board move is just a re-rank (no re-encryption needed).
  if (sameBoard && remove) {
    src.rank = rank;
    setStatus("saving");
    try {
      await patch("patchList", `/api/lists/${src.id}`, { rank });
      setStatus("saved"); render(); closeMoveList();
    } catch (err) { setStatus("error"); msg.textContent = errMsg(err); void unlock.load(); }
    return;
  }

  // Copying a list (it carries every card's comments/attachments) and any
  // cross-board op are online-only; only the intra-board move above works offline.
  if (!xySync.requireOnline("Копирование и перенос между досками доступны только онлайн.", msg)) return;
  msg.textContent = sameBoard ? "Копирование…" : "Перешифровка…";
  try {
    if (sameBoard) {
      // Duplicate the list and its cards on this board.
      const key = mustDK();
      const lres = (await jpost(`/api/boards/${boardId}/lists`, {
        title_enc: await xyCrypto.encField(key, src.title), rank, type,
      })) as { id: number };
      state.lists.push({ id: lres.id, type, rank, groupId: null, title: src.title });
      let cr: string | null = null;
      for (const c of srcCards) {
        cr = keyBetween(cr, null);
        const cres = (await jpost(`/api/lists/${lres.id}/cards`, await cardDetail.cardCopyBody(c, cr, key))) as { id: number };
        state.cards.push({ id: cres.id, listId: lres.id, kind: c.kind, rank: cr, desc: c.desc, handoutMeta: c.handoutMeta || null, alias: c.alias || null, createdAt: nowStamp() });
        const ids = state.cardLabels[c.id] || [];
        if (ids.length) { await jput(`/api/cards/${cres.id}/labels`, { label_ids: ids }); state.cardLabels[cres.id] = ids.slice(); }
        await cardDetail.copyCardExtras(c.id, key, cres.id);
      }
    } else {
      // Cross-board: re-encrypt under the target board's key, reconcile labels by
      // decrypted name+color (same as the per-card path).
      const tdk = ctx.dk;
      const tLabels = ctx.labels.slice();
      const lres = (await jpost(`/api/boards/${targetBid}/lists`, {
        title_enc: await xyCrypto.encField(tdk, src.title), rank, type,
      })) as { id: number };
      let cr: string | null = null;
      for (const c of srcCards) {
        cr = keyBetween(cr, null);
        const cres = (await jpost(`/api/lists/${lres.id}/cards`, await cardDetail.cardCopyBody(c, cr, tdk))) as { id: number };
        const targetIds = await cardDetail.reconcileLabels(c.id, targetBid, tdk, tLabels);
        if (targetIds.length) await jput(`/api/cards/${cres.id}/labels`, { label_ids: targetIds });
        await cardDetail.copyCardExtras(c.id, tdk, cres.id);
      }
      if (remove) {
        await jdelete(`/api/lists/${src.id}`);
        state.lists = state.lists.filter((l) => l.id !== src.id);
        state.cards = state.cards.filter((c) => c.listId !== src.id);
      }
    }
    render();
    msg.textContent = remove ? "Перемещено." : "Скопировано.";
    setTimeout(closeMoveList, 700);
  } catch (err) { msg.textContent = errMsg(err); }
}

byId("moveListBoard").addEventListener("change", () => { void onMoveListBoardChange(); });
byId("moveListCopyBtn").addEventListener("click", () => { void doMoveListCopy(false); });
byId("moveListMoveBtn").addEventListener("click", () => { void doMoveListCopy(true); });
byId("moveListClose").addEventListener("click", closeMoveList);
byId("moveListOverlay").addEventListener("pointerdown", (e) => {
  if (e.target instanceof Element && e.target.id === "moveListOverlay") closeMoveList();
});

// ---- lists management (reorder + group into list_of_lists) ----
// The «Управление списками» modal shows one row per list (and a bordered block
// per group). Lists can be reordered by dragging a row or by entering a target
// position; checking several rows lets you move them together or — when the
// checked rows are consecutive, ungrouped lists — link them into a group.
// Orderable units are standalone lists and whole groups; a group always moves as
// one block, keeping its members consecutive (the invariant the board relies on).
interface Unit { kind: "group" | "list"; id: number; key: string; lists: BoardList[] }

const listsManageOverlay = byId("listsManageOverlay");
const listsManageRows = byId("listsManageRows");
let manageSelected = new Set<string>();       // selected unit keys ("l"+listId / "g"+groupId)
let manageUnitByKey = new Map<string, Unit>();      // key → unit (rebuilt each render)
let manageDragKey: string | null = null;
let manageDragCommitted = false;
// Dragging a member row *inside* its group (reorder within, never across):
// the group id whose members container owns the gesture.
let memberDragGid: number | null = null;
let memberDragCommitted = false;

// computeUnits walks the rank-sorted lists, folding each maximal run of lists
// sharing a group_id into one group unit; ungrouped lists are singleton units.
function computeUnits(): Unit[] {
  const sorted = [...state.lists].sort(byRank);
  const units: Unit[] = [];
  let i = 0;
  while (i < sorted.length) {
    const l = sorted[i];
    if (l.groupId != null) {
      const gid = l.groupId, run: BoardList[] = [];
      while (i < sorted.length && sorted[i].groupId === gid) { run.push(sorted[i]); i++; }
      units.push({ kind: "group", id: gid, key: "g" + gid, lists: run });
    } else {
      units.push({ kind: "list", id: l.id, key: "l" + l.id, lists: [l] });
      i++;
    }
  }
  return units;
}

function openListsManage(): void {
  manageSelected = new Set();
  byId("listsManageMessage").textContent = "";
  byId<HTMLInputElement>("listsMovePos").value = "";
  listsManageOverlay.hidden = false;
  renderManage();
}
function closeListsManage(): void { listsManageOverlay.hidden = true; }

function renderManage(): void {
  const units = computeUnits();
  manageUnitByKey = new Map(units.map((u) => [u.key, u]));
  // Drop selections whose units no longer exist (e.g. after a group dissolved).
  for (const k of [...manageSelected]) if (!manageUnitByKey.has(k)) manageSelected.delete(k);
  listsManageRows.replaceChildren();
  units.forEach((u, idx) => listsManageRows.append(renderManageUnit(u, idx + 1)));
  updateManageToolbar(units);
}

function manageCheckbox(unit: Unit): HTMLElement {
  const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
  cb.checked = manageSelected.has(unit.key);
  cb.addEventListener("change", () => {
    if (cb.checked) manageSelected.add(unit.key); else manageSelected.delete(unit.key);
    updateManageToolbar(computeUnits());
  });
  return el("label", { class: "lm-check" }, cb);
}

function manageMoveControl(unit: Unit): HTMLElement {
  const inp = el("input", { class: "input lm-move-pos", type: "number", min: "1", placeholder: "№" }) as HTMLInputElement;
  const btn = el("button", { class: "btn btn-small btn-ghost lm-move-btn", type: "button", text: "↕️", title: "Переместить на эту позицию" });
  const go = (): void => { const n = parseInt(inp.value, 10); if (n >= 1) void moveUnitsTo(new Set([unit.key]), n); };
  btn.addEventListener("click", go);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go(); } });
  return el("div", { class: "lm-move" }, inp, btn);
}

function manageTitle(list: BoardList): string {
  return (list.type === "test" ? "🧪 " : "") + (list.title || "(без названия)");
}

function renderManageUnit(unit: Unit, pos: number): HTMLElement {
  const node = el("div", { class: "lm-unit lm-" + unit.kind, draggable: "true", dataset: { unitKey: unit.key } });
  if (unit.kind === "group") {
    const g = groupById(unit.id);
    node.append(el("div", { class: "lm-row lm-grouphead" },
      manageCheckbox(unit),
      el("span", { class: "lm-pos", text: "#" + pos }),
      el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
      el("span", { class: "lm-title lm-group-title", text: "🔗 " + ((g && g.name) || "Связанные списки") }),
      el("button", { class: "lm-icon", type: "button", text: "✏️", title: "Переименовать группу", onclick: () => { void renameGroup(unit.id); } }),
      el("button", { class: "lm-icon", type: "button", text: "✂️", title: "Разъединить группу", onclick: () => { void unlinkGroup(unit.id); } }),
      manageMoveControl(unit),
    ));
    // Members are draggable within their own group (the whole group is still
    // the unit that moves among lists — a member can't be dragged out of it,
    // that would break the group's consecutiveness).
    const members = el("div", { class: "lm-members" });
    for (const l of unit.lists) {
      const row = el("div", { class: "lm-member", draggable: "true", dataset: { listId: l.id } },
        el("span", { class: "lm-handle", text: "≡", title: "Перетащить внутри группы" }),
        el("span", { class: "lm-title", text: manageTitle(l) }));
      row.addEventListener("dragstart", (e) => {
        e.stopPropagation(); // the unit node is draggable too — don't start both
        memberDragGid = unit.id;
        memberDragCommitted = false;
        row.classList.add("dragging");
        if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
        try { e.dataTransfer?.setData("text/plain", "m" + l.id); } catch (_) {}
      });
      row.addEventListener("dragend", () => {
        row.classList.remove("dragging");
        memberDragGid = null;
        if (!memberDragCommitted) renderManage(); // aborted drag — resync DOM from state
      });
      members.append(row);
    }
    members.addEventListener("dragover", (e) => {
      if (memberDragGid !== unit.id) return;
      e.preventDefault();
      e.stopPropagation();
      const dragging = members.querySelector(".lm-member.dragging");
      if (!dragging) return;
      const after = dragAfterIn([...members.querySelectorAll(".lm-member:not(.dragging)")], e.clientY);
      if (after == null) members.append(dragging);
      else members.insertBefore(dragging, after);
    });
    members.addEventListener("drop", (e) => {
      if (memberDragGid !== unit.id) return;
      e.preventDefault();
      e.stopPropagation();
      memberDragCommitted = true;
      const byId = new Map(unit.lists.map((l): [string, BoardList] => [String(l.id), l]));
      const order = [...members.querySelectorAll<HTMLElement>(".lm-member")]
        .map((n) => byId.get(n.dataset.listId || ""))
        .filter((l): l is BoardList => !!l);
      if (order.length === unit.lists.length) void applyMemberOrder(unit.key, order);
    });
    node.append(members);
  } else {
    node.append(el("div", { class: "lm-row" },
      manageCheckbox(unit),
      el("span", { class: "lm-pos", text: "#" + pos }),
      el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
      el("span", { class: "lm-title", text: manageTitle(unit.lists[0]) }),
      manageMoveControl(unit),
    ));
  }
  node.addEventListener("dragstart", (e) => {
    manageDragKey = unit.key;
    manageDragCommitted = false;
    node.classList.add("dragging");
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
    try { e.dataTransfer?.setData("text/plain", unit.key); } catch (_) {}
  });
  node.addEventListener("dragend", () => {
    node.classList.remove("dragging");
    manageDragKey = null;
    if (!manageDragCommitted) renderManage(); // aborted drag — resync DOM from state
  });
  return node;
}

function manageDragAfter(y: number): Element | null {
  return dragAfterIn([...listsManageRows.querySelectorAll(".lm-unit:not(.dragging)")], y);
}

listsManageRows.addEventListener("dragover", (e) => {
  if (manageDragKey == null) return;
  e.preventDefault();
  const dragging = listsManageRows.querySelector(".lm-unit.dragging");
  if (!dragging) return;
  const after = manageDragAfter(e.clientY);
  if (after == null) listsManageRows.append(dragging);
  else listsManageRows.insertBefore(dragging, after);
});
listsManageRows.addEventListener("drop", (e) => {
  if (manageDragKey == null) return;
  e.preventDefault();
  manageDragCommitted = true;
  const order = [...listsManageRows.querySelectorAll<HTMLElement>(".lm-unit")]
    .map((n) => manageUnitByKey.get(n.dataset.unitKey || ""))
    .filter((u): u is Unit => !!u);
  void applyUnitOrder(order);
});

function updateManageToolbar(units: Unit[]): void {
  const linkBtn = byId<HTMLButtonElement>("listsLinkBtn");
  const moveBtn = byId<HTMLButtonElement>("listsMoveBtn");
  const selected = units.filter((u) => manageSelected.has(u.key));
  moveBtn.disabled = selected.length === 0;
  // Linking needs ≥2 selected, all ungrouped single lists, consecutive in order.
  let canLink = selected.length >= 2 && selected.every((u) => u.kind === "list");
  if (canLink) {
    const idxs = selected.map((u) => units.indexOf(u)).sort((a, b) => a - b);
    canLink = idxs.every((v, i) => i === 0 || v === idxs[i - 1] + 1);
  }
  linkBtn.disabled = !canLink;
}

// applyUnitOrder rewrites list ranks to match the given unit order (groups stay
// contiguous because their member lists are emitted together). Only changed
// ranks are patched. Offline-capable (rank patches flow through the sync engine).
async function applyUnitOrder(orderedUnits: Unit[]): Promise<void> {
  const msg = byId("listsManageMessage");
  const flat = orderedUnits.flatMap((u) => u.lists);
  let r: string | null = null;
  const patches: Array<[BoardList, string]> = [];
  for (const l of flat) { r = keyBetween(r, null); if (l.rank !== r) patches.push([l, r]); }
  if (!patches.length) { renderManage(); return; }
  setStatus("saving");
  try {
    for (const [l, rank] of patches) { l.rank = rank; await patch("patchList", `/api/lists/${l.id}`, { rank }); }
    setStatus("saved");
    render();
    renderManage();
  } catch (err) { setStatus("error"); msg.textContent = errMsg(err); void unlock.load(); }
}

// applyMemberOrder reorders the lists INSIDE one group: the group keeps its
// place among the units, only its members' ranks are rewritten.
function applyMemberOrder(unitKey: string, order: BoardList[]): Promise<void> {
  const units = computeUnits();
  const target = units.find((u) => u.key === unitKey);
  if (!target) return Promise.resolve();
  target.lists = order;
  return applyUnitOrder(units);
}

// moveUnitsTo relocates the selected units, preserving their relative order, so
// the first lands at 1-based position posN among all units.
function moveUnitsTo(keys: Set<string>, posN: number): Promise<void> {
  const units = computeUnits();
  const selected = units.filter((u) => keys.has(u.key));
  if (!selected.length) return Promise.resolve();
  const remaining = units.filter((u) => !keys.has(u.key));
  const idx = Math.max(0, Math.min(posN - 1, remaining.length));
  remaining.splice(idx, 0, ...selected);
  return applyUnitOrder(remaining);
}

async function linkSelected(): Promise<void> {
  const units = computeUnits();
  const selected = units.filter((u) => manageSelected.has(u.key));
  if (selected.length < 2 || selected.some((u) => u.kind !== "list")) return;
  const msg = byId("listsManageMessage");
  if (!xySync.requireOnline("Связывание списков доступно только онлайн.", msg)) return;
  const name = (prompt("Название списка списков:", "") || "").trim();
  if (!name) return;
  // Preserve board order (units are rank-sorted).
  const listIds = selected.sort((a, b) => units.indexOf(a) - units.indexOf(b)).flatMap((u) => u.lists.map((l) => l.id));
  try {
    await jpost(`/api/boards/${boardId}/list-groups`, { name_enc: await xyCrypto.encField(mustDK(), name), list_ids: listIds });
    manageSelected = new Set();
    await unlock.load();
    renderManage();
  } catch (err) { msg.textContent = errMsg(err); }
}

async function renameGroup(gid: number): Promise<void> {
  const g = groupById(gid);
  const name = (prompt("Новое название группы:", g ? g.name : "") || "").trim();
  if (!name) return;
  const msg = byId("listsManageMessage");
  if (!xySync.requireOnline("Переименование доступно только онлайн.", msg)) return;
  try {
    await jpatch(`/api/list-groups/${gid}`, { name_enc: await xyCrypto.encField(mustDK(), name) });
    await unlock.load();
    renderManage();
  } catch (err) { msg.textContent = errMsg(err); }
}

async function unlinkGroup(gid: number): Promise<void> {
  if (!confirm("Разъединить группу? Списки останутся, но нумерация снова станет раздельной.")) return;
  const msg = byId("listsManageMessage");
  if (!xySync.requireOnline("Разъединение доступно только онлайн.", msg)) return;
  try {
    await jdelete(`/api/list-groups/${gid}`);
    await unlock.load();
    renderManage();
  } catch (err) { msg.textContent = errMsg(err); }
}

byId("listsLinkBtn").addEventListener("click", () => { void linkSelected(); });
byId("listsMoveBtn").addEventListener("click", () => {
  const n = parseInt(byId<HTMLInputElement>("listsMovePos").value, 10);
  if (!(n >= 1)) { byId("listsManageMessage").textContent = "Укажите позицию."; return; }
  void moveUnitsTo(new Set(manageSelected), n);
});
byId("listsManageClose").addEventListener("click", closeListsManage);
listsManageOverlay.addEventListener("pointerdown", (e) => { if (e.target === listsManageOverlay) closeListsManage(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !listsManageOverlay.hidden) closeListsManage(); });

// ---- import a package (.4s / .zip / .docx) into a new list ----
// The server parses the upload with the Go port of chgksuite's parser
// (internal/chgk/chgkimport) and hands back 4s source plus the images it
// references. Everything below happens client-side under the board key: the list,
// its cards and the image attachments are all encrypted before they go back up.
//
// A .4s (or a .zip of one plus its images) is already in our own format, so it
// imports straight away. A .docx has been through a lossy heuristic parse, so it
// goes to the verification screen first.

interface ImportImage { name: string; data: string; mime: string }
interface ImportCard { id: number; kind: string; desc: string }
interface ImportPkg { name: string; source: string; images?: ImportImage[] }

// importCtx holds the package awaiting confirmation on the verification screen.
let importCtx: { name: string; images: ImportImage[]; imgMap: Map<string, string>; splitTours: boolean } | null = null;

const importPickOverlay = byId("importPickOverlay");

function openImportPick(): void {
  byId<HTMLFormElement>("importPickForm").reset();
  importPickOverlay.hidden = false;
}
function closeImportPick(): void { importPickOverlay.hidden = true; }

byId("importPickForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const files = byId<HTMLInputElement>("importFile").files;
  const file = files && files[0];
  if (!file) return;
  const splitTours = byId<HTMLInputElement>("importSplitTours").checked;
  closeImportPick();
  await importFile(file, splitTours);
});
byId("importPickCancel").addEventListener("click", closeImportPick);
importPickOverlay.addEventListener("pointerdown", (e) => { if (e.target === importPickOverlay) closeImportPick(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !importPickOverlay.hidden) closeImportPick(); });

async function importFile(file: File, splitTours: boolean): Promise<void> {
  if (!xySync.requireOnline("Импорт доступен только онлайн.")) return;
  setStatus("saving");
  try {
    const fd = new FormData();
    fd.append("file", file, file.name);
    const res = await fetch("/api/import/parse", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const pkg = (await res.json()) as ImportPkg;
    setStatus("saved");
    // A .docx parse is a guess; let the user check it before it becomes a list.
    if (/\.docx$/i.test(file.name)) openImportVerify(pkg, splitTours);
    else await commitImport(pkg.name, pkg.source, pkg.images, splitTours);
  } catch (err) {
    setStatus("error");
    alert("Не удалось разобрать файл: " + errMsg(err));
  }
}

// ---- verification screen (docx) ----

const importOverlay = byId("importOverlay");

// importCards splits 4s source the way the export path joins it: one card per
// blank-line-separated block. Each card's kind comes from its leading marker.
function importCards(source: string): ImportCard[] {
  return source
    .split(/\n[ \t]*\n/)
    .map((b) => b.trim())
    .filter(Boolean)
    .map((desc, i) => ({ id: -(i + 1), kind: importKind(desc), desc }));
}

// importKind maps a 4s block to an xy card kind. A question is recognised by its
// fields, not by its first line: compose_4s puts the "№ N" directive ahead of the
// "? …" marker, and an unmarked block ("pre") is question text whose author
// didn't prefix it.
function importKind(desc: string): string {
  const blocks = xyChgk.parseBlocks(desc);
  if (blocks.some((b) => b.type === "question" || b.type === "answer" || b.type === "pre")) return "question";
  if (blocks.some((b) => b.type === "heading" || b.type === "ljheading")) return "heading";
  return "meta";
}

// importImgMap turns the package's base64 images into object URLs so the preview
// can show handouts exactly as the list will once imported.
function importImgMap(images: ImportImage[] | undefined): Map<string, string> {
  const map = new Map<string, string>();
  for (const img of images || []) {
    const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
    map.set(img.name, URL.createObjectURL(new Blob([bytes], { type: img.mime })));
  }
  return map;
}

function openImportVerify(pkg: ImportPkg, splitTours: boolean): void {
  closeImportVerify();
  importCtx = { name: pkg.name, images: pkg.images || [], imgMap: importImgMap(pkg.images), splitTours };
  byId("importTitle").textContent = "Проверка импорта: " + pkg.name;
  const src = byId<HTMLTextAreaElement>("importSource");
  src.value = pkg.source;
  importOverlay.hidden = false;
  renderImportPreview();
  src.focus();
  // Focusing puts the caret at the end; the user wants to read from the top.
  src.setSelectionRange(0, 0);
  src.scrollTop = 0;
}

// renderImportPreview re-renders the right pane from whatever is in the editor,
// using the same renderer the list preview uses — so what you check is what you get.
function renderImportPreview(): void {
  const ctx = importCtx;
  if (!ctx) return;
  const body = byId("importPreview");
  const cards = importCards(byId<HTMLTextAreaElement>("importSource").value);
  const numbers = xyChgk.numberQuestionCards(cards);
  body.replaceChildren();
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], ctx.imgMap, false, false)));
  const qs = cards.filter((c) => c.kind === "question").length;
  byId("importCount").textContent = `${cards.length} блоков, ${qs} вопросов`;
}

function closeImportVerify(): void {
  importOverlay.hidden = true;
  if (importCtx) for (const url of importCtx.imgMap.values()) URL.revokeObjectURL(url);
  importCtx = null;
  byId("importPreview").replaceChildren();
}

byId("importSource").addEventListener("input", debounceImportPreview());
byId("importClose").addEventListener("click", closeImportVerify);
byId("importCommit").addEventListener("click", async () => {
  if (!importCtx) return;
  const { name, images, splitTours } = importCtx;
  const source = byId<HTMLTextAreaElement>("importSource").value;
  closeImportVerify();
  await commitImport(name, source, images, splitTours);
});
importOverlay.addEventListener("pointerdown", (e) => { if (e.target === importOverlay) closeImportVerify(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !importOverlay.hidden) closeImportVerify(); });

// Re-rendering the whole preview on every keystroke is wasteful on a big package.
function debounceImportPreview(): () => void {
  let t: ReturnType<typeof setTimeout> | null = null;
  return () => {
    if (t) clearTimeout(t);
    t = setTimeout(() => { if (importCtx) renderImportPreview(); }, 200);
  };
}

// ---- commit: 4s source + images → a new encrypted list (or a group of them) ----

// splitCardsByTours groups the blocks into tours: a "## …" section block starts
// a new tour and names its list (the section card itself is kept, so the 4s
// source survives export intact). Blocks before the first section — usually the
// editors/date preamble — become their own leading list.
function splitCardsByTours(cards: ImportCard[]): Array<{ title: string; cards: ImportCard[] }> {
  const tours: Array<{ title: string; cards: ImportCard[] }> = [];
  let cur: { title: string; cards: ImportCard[] } | null = null;
  for (const c of cards) {
    const sec = xyChgk.parseBlocks(c.desc).find((b) => b.type === "section");
    if (sec) {
      cur = { title: sec.text.split("\n")[0].trim() || `Тур ${tours.length + 1}`, cards: [] };
      tours.push(cur);
    } else if (!cur) {
      cur = { title: "Преамбула", cards: [] };
      tours.push(cur);
    }
    cur.cards.push(c);
  }
  return tours;
}

// commitImport creates the list(s), one card per 4s block, and attaches each
// image to the card whose text references it via an `(img …)` directive. With
// splitTours on, each tour becomes its own list and the lists are linked into a
// list group — continuous numbering and combined export across tours.
//
// The lists and cards are posted directly (jpost), not through the sync
// outbox: an import is online-only anyway, and mutate() hands back a negative
// temp id whenever the queue is non-empty — which the attachment upload, a plain
// POST to /api/cards/{id}/attachments, cannot use. Going direct keeps every id real.
async function commitImport(name: string, source: string, images: ImportImage[] | undefined, splitTours: boolean): Promise<void> {
  const cards = importCards(source);
  if (!cards.length) { alert("В файле не найдено вопросов."); return; }
  if (!xySync.requireOnline("Импорт доступен только онлайн.")) return;
  const tours = splitTours ? splitCardsByTours(cards) : [];
  // The server refuses a group of one, and a group of one is pointless anyway.
  const grouped = tours.length >= 2;
  const title = (prompt(grouped ? "Название группы списков:" : "Название нового списка:", name || "Импорт") || "").trim();
  if (!title) return;
  const parts = grouped ? tours : [{ title, cards }];

  setStatus("saving");
  const byName = new Map((images || []).map((i): [string, ImportImage] => [i.name, i]));
  let done = 0, attached = 0;
  const failed: string[] = []; // images the server refused — the card would keep a dead (img …)
  try {
    const key = mustDK();
    const ranks = [...state.lists].sort(byRank);
    let rank: string | null = ranks.length ? ranks[ranks.length - 1].rank : null;
    const listIds: number[] = [];
    for (const part of parts) {
      rank = keyBetween(rank, null);
      const lres = (await jpost(`/api/boards/${boardId}/lists`, {
        title_enc: await xyCrypto.encField(key, part.title), rank, type: "normal",
      })) as { id: number };
      listIds.push(lres.id);
      state.lists.push({ id: lres.id, type: "normal", rank, groupId: null, title: part.title });

      let cardRank: string | null = null;
      for (const c of part.cards) {
        cardRank = keyBetween(cardRank, null);
        const res = (await jpost(`/api/lists/${lres.id}/cards`, {
          description_enc: await xyCrypto.encField(key, c.desc), rank: cardRank, kind: c.kind,
        })) as { id: number };
        state.cards.push({ id: res.id, listId: lres.id, kind: c.kind, rank: cardRank, desc: c.desc, handoutMeta: null, alias: null, createdAt: nowStamp() });
        done++;
        // Attach only the images this card actually references, so a handout lands
        // on the question that uses it (which is where the preview/export look).
        const refs = new Set<string>();
        for (const m of c.desc.matchAll(/\(img\b([^)]*)\)/g)) refs.add(imgName(m[1]));
        for (const ref of refs) {
          const img = byName.get(ref);
          if (!img) continue;
          if (await attachImported(res.id, img)) attached++;
          else failed.push(ref);
        }
      }
    }
    if (grouped) {
      await jpost(`/api/boards/${boardId}/list-groups`, { name_enc: await xyCrypto.encField(key, title), list_ids: listIds });
      // Reload rather than mirror group_id/groups[] locally — import is online-only.
      await unlock.load();
    } else render();
    setStatus("saved");
    let msg = grouped
      ? `Импортировано: ${parts.length} списков (по турам), ${done} карточек, ${attached} изображений.`
      : `Импортировано: ${done} карточек, ${attached} изображений.`;
    if (splitTours && !grouped) msg += "\nТуры («## …») в файле не найдены — создан один список.";
    // A dropped image is invisible otherwise: the card keeps its (img …) directive
    // but the picture is gone, and the parse response is not kept to retry from.
    if (failed.length) msg += `\n\nНе удалось загрузить изображения (${failed.length}): ${failed.join(", ")}`;
    alert(msg);
  } catch (err) {
    // The lists and the cards created so far are already on the server — show them
    // rather than leaving the board looking as if nothing happened.
    render();
    setStatus("error");
    alert(`Импорт прерван после ${done} карточек: ${errMsg(err)}\n\nЧастично импортированный список остался на доске — удалите его перед повторным импортом.`);
  }
}

// attachImported encrypts one imported image and posts it as an attachment of
// `cardId`, under the same filename the (img …) directive refers to. Lossless:
// re-encoding would change nothing but could degrade a handout. Returns false (and
// lets the caller report it) if the server rejects it — e.g. an oversized scan.
async function attachImported(cardId: number, img: ImportImage): Promise<boolean> {
  try {
    const key = mustDK();
    const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
    const cipher = await xyCrypto.encBytes(key, bytes);
    const fd = new FormData();
    fd.append("meta", JSON.stringify({
      filename_enc: await xyCrypto.encField(key, img.name),
      mime: img.mime, lossless: true,
      event_payload_enc: await xyCrypto.encField(key, JSON.stringify({ file: img.name })),
    }));
    fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
    const res = await fetch(`/api/cards/${cardId}/attachments`, {
      method: "POST", credentials: "same-origin", body: fd,
    });
    return res.ok;
  } catch (_) { return false; }
}

// ---- export a list to .docx / .pdf ----
// Concatenate the list's card descriptions (in board order) into a chgksuite
// "4s" document, gather any images referenced by `(img ...)` directives from the
// cards' attachments, and hand both to the server, which composes the file in
// memory and streams it back. Both formats take the same request and render the
// same document: the PDF is typeset by typst to look like the docx (same layout,
// same non-breaking spaces/hyphens, same keep-together questions).
// See internal/server/export.go.
// exportScope resolves which lists a per-list action (export / handouts) covers:
// a standalone list is just itself; a grouped list pulls in every (non-test) list
// of its group, in board order, so the whole list_of_lists exports as one file.
// Returns { cards (concatenated, in order), title }.
function exportScope(list: BoardList): { cards: BoardCard[]; title: string } {
  let lists = [list], title = list.title || "export";
  if (list.groupId != null) {
    lists = listsInGroup(list.groupId).filter((l) => l.type !== "test");
    const g = groupById(list.groupId);
    if (g && g.name) title = g.name;
  }
  return { cards: lists.flatMap((l) => cardsOf(l.id)), title };
}

async function exportList(list: BoardList, format = "docx", mobile = false): Promise<void> {
  const ext = format === "pdf" ? "pdf" : "docx";
  const scope = exportScope(list);
  if (mobile) scope.title += "_mobile";
  const cards = scope.cards;
  if (!cards.length) { alert("В списке нет карточек."); return; }
  if (!xySync.requireOnline(`Экспорт в ${ext} доступен только онлайн.`)) return;
  setStatus("saving");
  try {
    const source = cards.map((c) => c.desc.trim()).filter(Boolean).join("\n\n") + "\n";
    // collect (img …) references — the filename is the LAST token (the rest are
    // w=/h=/big/inline options), so use imgName, not the first token.
    const wanted = new Set<string>();
    for (const m of source.matchAll(/\(img\b([^)]*)\)/g)) { const n = imgName(m[1]); if (n) wanted.add(n); }

    const fd = new FormData();
    fd.append("source", source);
    fd.append("filename", scope.title);
    if (mobile) fd.append("device", "mobile");

    // resolve referenced images from the cards' attachments (decrypt + attach)
    const found = await appendImages(fd, cards, wanted);
    const missing = [...wanted].filter((n) => !found.has(n));
    if (missing.length && !confirm(`Не найдены изображения: ${missing.join(", ")}. Продолжить?`)) {
      setStatus("saved");
      return;
    }
    const res = await fetch("/api/export/" + ext, { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = el("a", { href: url, download: `${scope.title}.${ext}` });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
    setStatus("saved");
  } catch (err) {
    setStatus("error");
    alert("Экспорт не удался: " + errMsg(err));
  }
}

// ---- handouts generation (chgksuite .hndt → PDF) ----
// "Генерация раздаток": port of `chgksuite handouts 4s2hndt` (in chgk.js) builds
// an editable .hndt source from the list's questions, merging each question's
// saved layout settings (handout_meta) with its live handout text. "Сгенерировать
// PDF" posts the source + referenced images to the server, which runs
// `chgksuite handouts hndt2pdf` (tectonic) and streams an ephemeral PDF. On close
// the per-question settings (everything but the handout text) are persisted back.
const handoutsOverlay = byId("handoutsOverlay");
let handoutsCtx: { list: BoardList; cards: BoardCard[]; numbers: Array<string | null>; title: string } | null = null;   // { list, cards, numbers }
let handoutsPdfUrl: string | null = null;

function openHandouts(list: BoardList): void {
  // Grouped lists generate one set of handouts for the whole list_of_lists, with
  // question numbers continuous across the group (numberQuestionCards over the
  // concatenated cards), matching the board + docx export.
  const scope = exportScope(list);
  const cards = scope.cards;
  const numbers = xyChgk.numberQuestionCards(cards);
  const metas: Record<number, string> = {};
  for (const c of cards) if (c.handoutMeta) metas[c.id] = c.handoutMeta;
  const source = xyChgk.generateHndt(cards, numbers, metas);
  handoutsCtx = { list, cards, numbers, title: scope.title };
  byId<HTMLTextAreaElement>("handoutsSource").value = source;
  byId("handoutsMessage").textContent = source.trim() ? "" : "В списке нет вопросов с раздаточным материалом.";
  clearHandoutsPdf();
  handoutsOverlay.hidden = false;
  // Pre-stage the referenced images now (in the background) so the first PDF /
  // split_fit generation doesn't pay the gather+upload, and start heartbeating.
  handoutSession.ensure(source).catch(() => {});
  handoutSession.startHeartbeat();
}

// WebKit won't render a PDF inside an <iframe> in a standalone web app (macOS
// Dock app / iOS home-screen PWA — the preview pane comes up blank), and on
// iOS even the in-browser iframe shows at most a flat first page. No Safari
// setting changes this; the working path there is a top-level navigation, so
// those contexts get an «Открыть PDF» button instead of the inline preview.
function pdfInlinePreviewBroken(): boolean {
  const ua = navigator.userAgent;
  const ios = /iPad|iPhone|iPod/.test(ua) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const webkitOnly = /AppleWebKit/.test(ua) && !/Chrome|CriOS|EdgiOS|FxiOS|Android/.test(ua);
  const standalone = (navigator as { standalone?: boolean }).standalone === true || (typeof matchMedia === "function" && matchMedia("(display-mode: standalone)").matches);
  return ios || (webkitOnly && standalone);
}

function pdfPreviewNode(url: string): HTMLElement {
  if (!pdfInlinePreviewBroken()) return el("iframe", { class: "handouts-pdf-frame", src: url, title: "PDF" });
  return el("div", { class: "handouts-pdf-fallback" },
    el("div", { class: "handouts-pdf-note", text: "Safari не показывает PDF внутри приложения." }),
    el("a", { class: "btn", href: url, target: "_blank", rel: "noopener", text: "Открыть PDF" }));
}

function clearHandoutsPdf(): void {
  const pane = byId("handoutsPdf");
  pane.replaceChildren();
  const dl = byId<HTMLAnchorElement>("handoutsDownload");
  dl.hidden = true;
  if (handoutsPdfUrl) { URL.revokeObjectURL(handoutsPdfUrl); handoutsPdfUrl = null; }
}

// persistHandoutMeta writes the edited per-question settings back onto the cards
// (everything in each .hndt block except the live handout text/image), so the
// layout is restored next time the modal opens.
async function persistHandoutMeta(): Promise<void> {
  if (!handoutsCtx) return;
  const source = byId<HTMLTextAreaElement>("handoutsSource").value;
  const byNumber = xyChgk.parseHndtMetaByQuestion(source);
  const { cards, numbers } = handoutsCtx;
  for (let i = 0; i < cards.length; i++) {
    const c = cards[i];
    if (c.kind !== "question") continue;
    const num = numbers[i];
    if (num == null || !(String(num) in byNumber)) continue;
    const meta = byNumber[String(num)] || null;
    const norm = meta && meta.trim() ? meta : null;
    if (norm === (c.handoutMeta || null)) continue;
    try {
      const body: OpBody = { handout_meta_enc: norm ? await xyCrypto.encField(mustDK(), norm) : "" };
      await patch("patchCard", `/api/cards/${c.id}`, body);
      c.handoutMeta = norm;
    } catch (_) { /* best-effort: keep editing even if a write fails */ }
  }
}

async function closeHandouts(): Promise<void> {
  handoutsOverlay.hidden = true;
  void handoutSession.close(); // stop heartbeat + delete the staged images server-side
  await persistHandoutMeta();
  clearHandoutsPdf();
  handoutsCtx = null;
}

async function generateHandoutsPdf(): Promise<void> {
  if (!handoutsCtx) return;
  if (!xySync.requireOnline("Генерация PDF доступна только онлайн.", byId("handoutsMessage"))) return;
  const source = byId<HTMLTextAreaElement>("handoutsSource").value;
  const msg = byId("handoutsMessage");
  if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
  const btn = byId<HTMLButtonElement>("handoutsGenerate");
  btn.disabled = true;
  msg.textContent = "Генерация…";
  clearHandoutsPdf();
  try {
    const fd = await handoutsBody(source);
    const res = await fetch("/api/handouts/pdf", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const blob = await res.blob();
    handoutsPdfUrl = URL.createObjectURL(blob);
    byId("handoutsPdf").replaceChildren(pdfPreviewNode(handoutsPdfUrl));
    const dl = byId<HTMLAnchorElement>("handoutsDownload");
    dl.href = handoutsPdfUrl;
    dl.setAttribute("download", (handoutsCtx.title || handoutsCtx.list.title || "handouts") + ".pdf");
    dl.hidden = false;
    msg.textContent = "Готово.";
  } catch (err) {
    msg.textContent = "Не удалось сгенерировать: " + errMsg(err);
  } finally {
    btn.disabled = false;
  }
}

// ---- handout image staging (server-side cache) ----
// Opening the modal uploads the referenced images to the server once; every PDF
// / split_fit generation then just references the session id, so the images
// aren't re-decrypted + re-uploaded each time (which dominated the latency). A 5s
// heartbeat keeps the session alive; the server reaps it after ~1 min of silence
// (tab closed / backgrounded), and we re-stage on demand if it lapsed.
function wantedImages(source: string): Set<string> {
  const wanted = new Set<string>();
  for (const m of source.matchAll(/^\s*image:\s*(.+?)\s*$/gm)) wanted.add(m[1]);
  for (const m of source.matchAll(/\(img\b([^)]*)\)/g)) { const n = imgName(m[1]); if (n) wanted.add(n); }
  return wanted;
}

// stageImages gathers + decrypts the referenced images and uploads them to a new
// server session, returning { session, names } (null when there are none / on
// error). The session lifecycle around it lives in handoutSession.
async function stageImages(source: string): Promise<{ session: string; names: Set<string> } | null> {
  if (!handoutsCtx) return null;
  const wanted = wantedImages(source);
  if (!wanted.size) return null;
  const fd = new FormData();
  const found = await appendImages(fd, handoutsCtx.cards, wanted);
  try {
    const res = await fetch("/api/handouts/stage", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const { session } = (await res.json()) as { session: string };
    return { session, names: found };
  } catch (_) { return null; }
}

async function heartbeatPing(sessionId: string): Promise<boolean> {
  try {
    const fd = new FormData();
    fd.append("session", sessionId);
    const res = await fetch("/api/handouts/heartbeat", { method: "POST", credentials: "same-origin", body: fd });
    return res.ok;
  } catch (_) { return false; }
}

async function unstageSession(sessionId: string): Promise<void> {
  try { await fetch(`/api/handouts/stage?session=${encodeURIComponent(sessionId)}`, { method: "DELETE", credentials: "same-origin" }); } catch (_) {}
}

// handoutSession owns the stage-once/heartbeat/reap/cleanup lifecycle (see
// handoutsession.js); the callbacks above are the board-specific network ops.
const handoutSession = xyHandoutSession.create({
  wantedNames: wantedImages,
  stage: stageImages,
  heartbeat: heartbeatPing,
  unstage: unstageSession,
});

// handoutsBody builds the generate request body: the source + (when there are
// images) the staged session id, so images aren't re-sent each generate.
async function handoutsBody(source: string): Promise<FormData> {
  const fd = new FormData();
  fd.append("source", source);
  fd.append("filename", (handoutsCtx && (handoutsCtx.title || handoutsCtx.list.title)) || "handouts");
  const sid = await handoutSession.ensure(source);
  if (sid) fd.append("session", sid);
  return fd;
}

// Revive the staged session when the user returns to a backgrounded tab (its
// heartbeats may have lapsed and the server reaped it).
document.addEventListener("visibilitychange", async () => {
  if (document.visibilityState !== "visible" || handoutsOverlay.hidden || !handoutsCtx) return;
  if (!(await handoutSession.beat())) handoutSession.ensure(byId<HTMLTextAreaElement>("handoutsSource").value).catch(() => {});
});

// appendImages resolves each wanted image to its decrypted bytes and appends it
// to fd as an "img" part. The cards' attachment lists are fetched in parallel
// (the old per-card sequential scan dominated handout/export latency), and the
// matched image bodies are fetched in parallel too. Returns the set of resolved
// names so the caller can prompt about any still missing.
async function appendImages(fd: FormData, cards: ReadonlyArray<{ id: number }>, wanted: Set<string>): Promise<Set<string>> {
  const found = new Set<string>();
  if (!wanted.size) return found;
  const lists = await Promise.all(cards.map((c) => attachments.cardAttachments(c.id)));
  const targets = new Map<string, NamedAttachment>(); // name → attachment (first match wins)
  for (const atts of lists) {
    for (const att of atts) {
      if (att.name && wanted.has(att.name) && !targets.has(att.name)) targets.set(att.name, att);
    }
  }
  await Promise.all([...targets].map(async ([name, att]) => {
    try {
      const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
      if (!res.ok) return;
      const plain = await xyCrypto.decBytes(mustDK(), new Uint8Array(await res.arrayBuffer()));
      fd.append("img", new Blob([plain], { type: att.mime }), name);
      found.add(name);
    } catch (_) {}
  }));
  return found;
}

// generateSplitFitZip runs chgksuite's split_fit on the current .hndt (pages each
// handout to fit, one fitted PDF per question + an all-questions PDF) and hands
// the user a zip of all the PDFs. Online-only (shells out server-side).
async function generateSplitFitZip(): Promise<void> {
  if (!handoutsCtx) return;
  const msg = byId("handoutsMessage");
  if (!xySync.requireOnline("Split-fit доступен только онлайн.", msg)) return;
  const source = byId<HTMLTextAreaElement>("handoutsSource").value;
  if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
  const btn = byId<HTMLButtonElement>("handoutsSplitFit");
  btn.disabled = true;
  msg.textContent = "Split-fit… (подбор раскладки может занять время)";
  try {
    const fd = await handoutsBody(source);
    const res = await fetch("/api/handouts/split_fit", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = el("a", { href: url, download: (handoutsCtx.title || handoutsCtx.list.title || "handouts") + ".zip" });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
    msg.textContent = "Готово — zip со всеми PDF скачан.";
  } catch (err) {
    msg.textContent = "Split-fit не удался: " + errMsg(err);
  } finally {
    btn.disabled = false;
  }
}

byId("handoutsGenerate").addEventListener("click", () => { void generateHandoutsPdf(); });
byId("handoutsSplitFit").addEventListener("click", () => { void generateSplitFitZip(); });
byId("handoutsClose").addEventListener("click", () => { void closeHandouts(); });
handoutsOverlay.addEventListener("pointerdown", (e) => { if (e.target === handoutsOverlay) void closeHandouts(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !handoutsOverlay.hidden) void closeHandouts(); });

// ---- list preview (docx-style HTML render, entirely client-side) ----
// Renders a whole list the way chgksuite's docx export would — questions with
// numbered labels and Ответ/Зачёт/Комментарий/etc. fields, plus meta, headings
// and handouts — but in the browser, so it's instant. Inline 4s markup
// (bold/italic/links/(img …)/(screen …)) is parsed via xyChgk; referenced image
// handouts are resolved from the cards' attachments (decrypted + object-URL'd).

// The card shape the preview renders: a persisted board card, the card detail's
// transient draft card, or an import-verify block (which has no list yet).
interface PvCard { id: number; kind: string; desc: string; listId?: number }

// Field labels mirror chgksuite/resources/labels_ru.toml (question_labels).
const PV_LABELS: Record<string, string> = {
  answer: "Ответ", zachet: "Зачёт", nezachet: "Незачёт",
  comment: "Комментарий", source: "Источник", author: "Автор",
  handout: "Раздаточный материал", editor: "Редактор", date: "Дата",
};
const previewOverlay = byId("previewOverlay");

// imgName extracts the referenced filename from an (img …) run value: like
// chgksuite's parseimg, the filename is the last whitespace token (the rest are
// w=/h=/big/inline options).
function imgName(val: unknown): string {
  const toks = String(val).trim().split(/\s+/).filter(Boolean);
  return toks.length ? toks[toks.length - 1] : "";
}

// imageRefs collects every (img …) filename referenced across the list's cards.
function imageRefs(cards: ReadonlyArray<{ desc: string }>): Set<string> {
  const wanted = new Set<string>();
  for (const c of cards) {
    for (const m of (c.desc || "").matchAll(/\(img\b([^)]*)\)/g)) {
      const name = imgName(m[1]);
      if (name) wanted.add(name);
    }
  }
  return wanted;
}

// (attachment caches live in attachments.ts)

// fillPreviewImages swaps the "[изображение: …]" placeholders inside an already
// rendered preview for the images that have since resolved.
function fillPreviewImages(root: ParentNode, imgMap: Map<string, string>): void {
  for (const ph of root.querySelectorAll<HTMLElement>(".pv-img-missing[data-img]")) {
    const url = imgMap.get(ph.dataset.img || "");
    if (url) ph.replaceWith(el("img", { class: "pv-img", src: url, alt: ph.dataset.img }));
  }
}

// Fields that accept a "!!Label " label override (chgksuite OVERRIDE_PREFIX).
const PV_OVERRIDABLE = new Set(["question", "answer", "zachet", "nezachet", "comment", "source", "author"]);

// fieldOpts returns the render options for a field given the screen-mode toggle.
// Screen mode strips stress accents everywhere and host-only [ … ] notes — except
// answers and zachet, which keep their brackets (matching chgksuite docx screen
// mode). Meta/headings are never screen-transformed. `nbsp` (non-breaking
// spaces/hyphens) applies everywhere except sources and handouts, like docx.
interface RichOpts { accents?: boolean; brackets?: boolean; nbsp?: boolean }
function fieldOpts(field: string, screen: boolean): RichOpts {
  const nbsp = field !== "source" && field !== "handout";
  if (!screen) return { accents: false, brackets: false, nbsp };
  const keepBrackets = field === "answer" || field === "zachet";
  return { accents: true, brackets: !keepBrackets, nbsp };
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
      const name = imgName(val);
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

// pvField renders a "Label: value" line: peels a "!!Label" override, numbers any
// "- …" list, and (for sources that became a list) uses the plural label.
function pvField(field: string, defaultLabel: string, text: string, imgMap: Map<string, string>, screen: boolean, cls: string): HTMLElement {
  const ov: { label: string | null; text: string } = PV_OVERRIDABLE.has(field) ? xyChgk.applyOverride(text) : { label: null, text };
  const lst = xyChgk.splitList(ov.text);
  let label = ov.label || defaultLabel;
  if (!ov.label && field === "source" && lst.items) label = "Источники";
  const node = el("div", { class: "pv-field" + (cls ? " " + cls : "") },
    el("strong", { class: "pv-label", text: label + ": " }));
  node.append(renderFieldBody(ov.text, imgMap, fieldOpts(field, screen)));
  return node;
}

// pvEditBtn builds the small inline ✏️ button rendered just before each preview
// block's leading label (e.g. "✏️Вопрос 1."): it closes the preview and drops
// straight into the card editor, remembering the preview + card so the card's
// ↩️ back button can restore this exact preview scrolled to the same question.
function pvEditBtn(card: BoardCard): HTMLElement {
  const list = previewListRef;
  return el("button", {
    class: "pv-edit", title: "Редактировать карточку", "aria-label": "Редактировать карточку",
    text: "✏️",
    onclick: (e: Event) => {
      e.stopPropagation();
      const group = previewGroupMode;
      closePreview();
      void cardDetail.openCard(card, { returnTo: list ? { listId: list.id, cardId: card.id, group } : null });
    },
  });
}

// renderPreviewCard renders one card the way the docx export would: a question
// card becomes a numbered question with its answer/zachet/etc.; meta/heading/
// section/editor/date cards become their corresponding paragraphs/headings.
// `edit` adds the ✏️ jump-to-editor button — only the list preview passes it; the
// card-detail preview (already inside the editor) leaves it off.
function renderPreviewCard(card: PvCard, number: string | null, imgMap: Map<string, string>, screen: boolean, edit = false): HTMLElement {
  if (card.kind === "test") {
    return el("p", { class: "pv-meta pv-test", text: testTitle(card.desc), dataset: { cardId: card.id } });
  }
  const blocks = xyChgk.parseBlocks(card.desc);
  const find = (t: string) => blocks.find((b) => b.type === t);

  if (card.kind === "question" || find("question")) {
    const wrap = el("article", { class: "pv-q", dataset: { cardId: card.id } });
    const handout = find("handout");
    if (handout) wrap.append(pvField("handout", PV_LABELS.handout, handout.text, imgMap, screen, "pv-handout"));
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
      if (b) wrap.append(pvField(f, PV_LABELS[f], b.text, imgMap, screen, pvSmallCls(f)));
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
      wrap.append(pvField(b.type, PV_LABELS[b.type], b.text, imgMap, false, pvSmallCls(b.type)));
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

function closePreview(): void {
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
  const scopeLists = group ? listsInGroup(list.groupId as number).filter((l) => l.type !== "test") : [list];
  const cards = scopeLists.flatMap((l) => cardsOf(l.id));
  byId("previewTitle").textContent = group
    ? "🔗" + (group.name || "связанные списки")
    : (list.type === "test" ? "🧪 " : "") + (list.title || "Предпросмотр");
  const body = byId("previewBody");
  body.replaceChildren();
  previewCtx = null;
  previewListRef = list;
  previewGroupMode = !!group;
  // Screen-mode toggle + tester-copy button are mutually exclusive per list kind.
  const isTest = !group && list.type === "test";
  q(".preview-screen-toggle").hidden = isTest;
  byId("previewCopyTesters").hidden = !isTest;
  previewOverlay.hidden = false;
  if (isTest) {
    const text = cardDetail.testerSummary(list);
    body.replaceChildren(text
      ? el("p", { class: "pv-testers", text })
      : el("p", { class: "pv-empty", text: "В этом списке пока нет тестеров." }));
    return;
  }
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
  await attachments.resolveImages(cards, imageRefs(cards), (name, url) => {
    imgMap.set(name, url);
    // Ignore a close (or another list's preview) that happened during the await.
    if (previewCtx === ctx && !previewOverlay.hidden) fillPreviewImages(body, imgMap);
  });
}

// Copy the previewed test list's tester summary; brief inline confirmation.
byId("previewCopyTesters").addEventListener("click", async (e) => {
  if (!previewListRef) return;
  const btn = e.currentTarget as HTMLButtonElement; // capture before await (currentTarget clears after dispatch)
  if (!btn.dataset.label) btn.dataset.label = btn.textContent || "";
  await cardDetail.copyTesterList(previewListRef);
  btn.textContent = "Скопировано ✓";
  setTimeout(() => { btn.textContent = btn.dataset.label || ""; }, 1500);
});

byId("previewScreen").addEventListener("change", (e) => renderPreviewBody((e.target as HTMLInputElement).checked));
byId("previewClose").addEventListener("click", closePreview);
previewOverlay.addEventListener("pointerdown", (e) => { if (e.target === previewOverlay) closePreview(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !previewOverlay.hidden) closePreview(); });

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
  cleanupTestLabels,
  preview: { renderPreviewCard, resolveImages: attachments.resolveImages, imageRefs, fillPreviewImages, previewList },
  attachments,
  readMarkers: { refreshCardUnreadDot, renderNotifBadge },
  timeline: {
    load: (cardId) => timeline.load(cardId),
    events: () => timeline.events(),
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
  attachments: { url: attachments.attachmentUrl, download: attachments.download },
});

// ---- labels ----
// renderLabelPicker shows only the labels actually assigned to this card (each
// removable on click). Boards can have dozens of labels (e.g. one green/red pair
// per test session), so the rest live behind an "add label" dropdown rather than
// being dumped on screen.
// labelLastUsage maps label id → the highest card id currently carrying it.
// Card ids grow monotonically, so the max id is a recency proxy for "last used"
// without scanning per-card timelines. Labels absent from the map were never
// used (or imported with no assignments).
function labelLastUsage(): Map<number, number> {
  const usage = new Map<number, number>();
  for (const [cardId, ids] of Object.entries(state.cardLabels)) {
    const cid = Number(cardId);
    for (const id of ids || []) {
      const prev = usage.get(id);
      if (prev === undefined || cid > prev) usage.set(id, cid);
    }
  }
  return usage;
}

// sortLabels orders by last usage descending; labels with no usage data fall to
// the bottom, ordered alphabetically descending.
function sortLabels(labels: BoardLabel[]): BoardLabel[] {
  const usage = labelLastUsage();
  return labels.slice().sort((a, b) => {
    const ua = usage.get(a.id), ub = usage.get(b.id);
    const ha = ua !== undefined, hb = ub !== undefined;
    if (ha && hb) return (ub as number) - (ua as number);
    if (ha !== hb) return ha ? -1 : 1;
    return b.name.localeCompare(a.name, "ru");
  });
}

function renderLabelPicker(card: BoardCard): void {
  const picker = byId("labelPicker");
  picker.replaceChildren();
  const assigned = state.cardLabels[card.id] || [];
  for (const id of assigned) {
    const lbl = labelById(id);
    if (!lbl) continue;
    picker.append(el("button", {
      class: "label-pick is-on", type: "button", dataset: { c: lbl.color },
      title: "Снять метку", text: lbl.name + " ×",
      onclick: () => { void toggleLabel(card, lbl); },
    }));
  }
  if (!assigned.length) picker.append(el("span", { class: "label-empty", text: "меток нет" }));
  closeLabelAddPopup();
  paintLabels();
}

function closeLabelAddPopup(): void {
  const popup = document.querySelector("#labelAddRow .label-add-popup");
  if (popup) popup.remove();
}

// The "create a new label" form is authored in board.dopeui but does NOT belong
// in the card body: it used to sit there permanently as a third stacked row under
// «Метки», duplicating the popup's job and pushing the section to three lines for
// a control almost nobody needs on any given card. Detach it once at boot and
// keep the node; openLabelAddPopup mounts it at the foot of the popup, where
// "create a label" actually belongs. Handlers bound to the element survive the
// move, so its submit listener below keeps working.
const newLabelForm = byId<HTMLFormElement>("newLabelForm");
newLabelForm.remove();

// The compiled pages spell "+" as the ➕ emoji; swap it for the SVG plus.
swapPlusIcon(byId("labelAddBtn"));
swapPlusIcon(newLabelForm.querySelector<HTMLButtonElement>('button[type="submit"]')!);

// openLabelAddPopup mounts a custom dropdown under the "➕ Добавить метку" button:
// a filter field above a scrollable list of the unassigned labels, sorted by last
// usage (sortLabels), with the create-new-label form at the foot. A native
// <select> can't host a search box, hence the hand-rolled popup (shares the
// .menu-dropdown styling of the list "⋯" menu).
function openLabelAddPopup(): void {
  const found = state.cards.find((c) => c.id === cardDetail.openCardId());
  if (!found) return;
  const card = found;
  const anchor = byId("labelAddRow");
  if (anchor.querySelector(".label-add-popup")) { closeLabelAddPopup(); return; } // toggle off

  const assignedSet = new Set(state.cardLabels[card.id] || []);
  const pool = sortLabels(state.labels.filter((l) => !assignedSet.has(l.id)));

  const filter = el("input", {
    class: "input label-add-filter", type: "text",
    placeholder: "Фильтр меток…", autocomplete: "off",
  }) as HTMLInputElement;
  const listBox = el("div", { class: "label-add-list" });
  const popup = el("div", { class: "menu-dropdown label-add-popup", role: "menu" }, filter, listBox, newLabelForm);

  function fill(): void {
    const q = filter.value.trim().toLowerCase();
    const items = q ? pool.filter((l) => l.name.toLowerCase().includes(q)) : pool;
    listBox.replaceChildren();
    if (!items.length) { listBox.append(el("span", { class: "label-empty", text: "ничего не найдено" })); return; }
    for (const lbl of items) {
      listBox.append(el("button", {
        class: "menu-item label-add-item", type: "button", role: "menuitem",
        onclick: () => { close(); void toggleLabel(card, lbl); },
      },
        el("span", { class: "label-swatch", dataset: { c: lbl.color } }),
        el("span", { class: "label-add-name", text: lbl.name }),
      ));
    }
    paintLabels();
  }
  function close(): void {
    popup.remove();
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey);
  }
  function onOutside(e: PointerEvent): void { if (e.target instanceof Node && !anchor.contains(e.target)) close(); }
  function onKey(e: KeyboardEvent): void { if (e.key === "Escape") { close(); byId("labelAddBtn").focus(); } }

  filter.addEventListener("input", fill);
  anchor.append(popup);
  document.addEventListener("pointerdown", onOutside, true);
  document.addEventListener("keydown", onKey);
  fill();
  filter.focus();
}

byId("labelAddBtn").addEventListener("click", openLabelAddPopup);

async function toggleLabel(card: BoardCard, lbl: BoardLabel): Promise<void> {
  const cur = new Set(state.cardLabels[card.id] || []);
  const adding = !cur.has(lbl.id);
  if (adding) cur.add(lbl.id); else cur.delete(lbl.id);
  const ids = [...cur];
  try {
    const events = [{
      type: adding ? "label_add" : "label_remove",
      payload_enc: await xyCrypto.encField(mustDK(), JSON.stringify({ label: lbl.name })),
    }];
    await put("setCardLabels", `/api/cards/${card.id}/labels`, { label_ids: ids, events });
    state.cardLabels[card.id] = ids;
    renderLabelPicker(card);
    render();
    await timeline.load(card.id);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
}

// NB: `newLabelForm` (the retained node), not getElementById — the form is
// detached from the document above and lives inside the popup while it is open.
newLabelForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = byId<HTMLInputElement>("newLabelName").value.trim();
  const color = byId<HTMLInputElement>("newLabelColor").value;
  if (!name) return;
  try {
    const res = await create("createLabel", `/api/boards/${boardId}/labels`, {
      name_enc: await xyCrypto.encField(mustDK(), name),
      color_enc: await xyCrypto.encField(mustDK(), color),
    });
    const lbl: BoardLabel = { id: res.id as number, kind: "normal", name, color };
    state.labels.push(lbl);
    byId<HTMLInputElement>("newLabelName").value = "";
    const card = state.cards.find((c) => c.id === cardDetail.openCardId());
    // The form is now reachable only from inside the add-label popup, so naming a
    // label there means you want it ON this card — assign it instead of merely
    // creating it and making the user reopen the popup to pick what they just
    // typed. toggleLabel does the API call, re-renders and closes the popup.
    if (card) await toggleLabel(card, lbl);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
});

void unlock.boot();
