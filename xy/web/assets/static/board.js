// board.js — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { xyApp, xySizes } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";
import { xyDiff } from "./diff.js";
import { xySync } from "./sync.js";

const { fetchJSON, jpost, jpatch, jput, jdelete, el, deriveTitle } = xyApp;
const { keyBetween } = xyRank;

// Mutation wrappers — every board mutation flows through the sync engine, which
// sends it immediately when online or queues it (returning a negative temp id
// for creates) when offline, reconciling on reconnect. `create` mints an id;
// the rest return { id: null }. See sync.js.
const create = (kind, path, body) => xySync.mutate({ kind, method: "POST", path, body, board: boardId, mint: true });
const post = (kind, path, body) => xySync.mutate({ kind, method: "POST", path, body, board: boardId });
const patch = (kind, path, body) => xySync.mutate({ kind, method: "PATCH", path, body, board: boardId });
const put = (kind, path, body) => xySync.mutate({ kind, method: "PUT", path, body, board: boardId });
const del = (kind, path) => xySync.mutate({ kind, method: "DELETE", path, board: boardId });

const boardId = Number(location.pathname.split("/").pop());

const statusNode = document.getElementById("status");
const kanban = document.getElementById("kanban");
const titleNode = document.getElementById("boardTitle");

const state = { role: "editor", name: "", lists: [], groups: [], cards: [], labels: [], cardLabels: {}, members: [], memberNames: {}, me: null, unread: {}, sizes: null, defaultAuthor: "" };
let dk = null;
// One-shot guard per card-drag gesture: set true the moment a drop commits the
// move, so a stray duplicate drop is ignored and dragend can tell an aborted
// gesture (which must re-render to undo `dragover`'s DOM relocation) from a real one.
let cardDragCommitted = false;

// The header badge combines a transient per-action state (saving/error) with the
// persistent sync state (offline / queued edits), the latter taking precedence.
let lastOp = "saved"; // saved | saving | error
let syncState = { online: true, pending: 0, syncing: false };

function refreshBadge() {
  let state, title;
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
function setStatus(s) { lastOp = s; refreshBadge(); }

// ---- boot + unlock ----
async function boot() {
  if (!(await xyApp.requireLogin())) return;
  xySync.start();
  xySync.onStatus((st) => { syncState = st; refreshBadge(); });
  // When a board's queued edits fully reconcile with the server, reload so the
  // temp ids in view are replaced by the authoritative server ids.
  xySync.onBoardSynced((b) => { if (b === boardId) load(); });
  try {
    dk = await xyCrypto.loadCachedDK(boardId);
  } catch (_) {}
  if (!dk) {
    showUnlock();
    return;
  }
  await load();
}

function showUnlock() {
  document.getElementById("unlockOverlay").hidden = false;
  document.getElementById("unlockPass").focus();
}

document.getElementById("unlockForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const msg = document.getElementById("unlockMessage");
  msg.textContent = "";
  try {
    const keymeta = await fetchJSON(`/api/boards/${boardId}/keymeta`);
    dk = await xyCrypto.unlockBoard(document.getElementById("unlockPass").value, keymeta);
    await xyCrypto.cacheDK(boardId, dk);
    document.getElementById("unlockOverlay").hidden = true;
    await load();
  } catch (err) {
    msg.textContent = err.message;
  }
});

// Board-level actions live in the burger (☰) menu — sharing (rarely opened) and
// "forget password" (rarely needed) don't warrant header buttons.
// dopeMenu.setExtras renders them as actions.
window.dopeMenu?.setExtras([{
  label: "✏️ Переименовать доску",
  title: "Изменить название доски",
  onClick: () => renameBoard(),
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
  onClick: () => openMembers(),
}, {
  label: "🧹 Исправить оформление Trello",
  title: "Убрать артефакты Trello (двойные переносы, экранирование, смарт-ссылки) во всех карточках",
  onClick: () => fixTrelloFormattingBoard(),
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
  onClick: () => deleteBoard(),
}]);

// ---- board sizes (workspace width / list width / card height) ----
// A per-user display preference, edited on /profile (see profile.js) and
// delivered in the board snapshot; here it only drives the three CSS vars.
// Apply defaults immediately so the vars are defined; the snapshot then
// overrides state.sizes with the user's saved values (see the load path).
state.sizes = { ...xySizes.DEFAULT };
xySizes.apply(state.sizes);

// Board names are plaintext server-side metadata now (only the board's data stays
// encrypted). Backfill a legacy board's name once we've decrypted it on load — best-
// effort, online-only; the server ignores it if the board is already migrated.
function migrateBoardName(name) {
  if (!name || !xySync.isOnline()) return;
  jpost(`/api/boards/${boardId}/migrate-name`, { name }).catch(() => {});
}

// renameBoard / deleteBoard touch board-level metadata, which isn't part of the
// per-board sync outbox (lists/cards) — so both are online-only. The server soft-
// deletes the board (owner-only) and excludes it from the board list thereafter.
async function renameBoard() {
  const name = prompt("Новое название доски:", state.name || "");
  if (name == null) return;
  const t = name.trim();
  if (!t || t === state.name) return;
  if (!xySync.isOnline()) { alert("Переименование доски доступно только онлайн."); return; }
  setStatus("saving");
  try {
    await jpatch(`/api/boards/${boardId}`, { name: t });
    state.name = t;
    titleNode.textContent = t;
    document.title = t + " · xy";
    setStatus("saved");
  } catch (err) { setStatus("error"); alert("Не удалось переименовать: " + err.message); }
}

async function deleteBoard() {
  if (state.role !== "owner") { alert("Удалить доску может только её владелец."); return; }
  if (!confirm(`Удалить доску «${state.name || ""}» со всеми списками и карточками? Это действие необратимо.`)) return;
  if (!xySync.isOnline()) { alert("Удаление доски доступно только онлайн."); return; }
  try {
    await jdelete(`/api/boards/${boardId}`);
    try { await xyCrypto.forgetDK(boardId); } catch (_) {}
    location.href = "/";
  } catch (err) { alert("Не удалось удалить: " + err.message); }
}

// fixTrelloFormattingBoard re-applies chgksuite's Trello clean-up (the same fix
// the importer runs) to every already-imported card whose description still
// carries Trello artefacts. Each changed card is re-encrypted and patched with a
// desc_edit timeline entry, so the change is auditable and reversible.
async function fixTrelloFormattingBoard() {
  const changes = [];
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
    for (const ch of changes) {
      await patch("patchCard", `/api/cards/${ch.card.id}`, {
        description_enc: await xyCrypto.encField(dk, ch.desc),
        desc_event_enc: await xyCrypto.encField(dk, JSON.stringify({ before: ch.card.desc, after: ch.desc })),
      });
      ch.card.desc = ch.desc;
      done++;
    }
    setStatus("saved");
    render();
    alert(`Исправлено карточек: ${done}.`);
  } catch (err) {
    setStatus("error");
    alert("Ошибка при исправлении: " + err.message);
  }
}

// ---- members / sharing ----
// Membership is plaintext server-side metadata (not board-encrypted), so the
// sharing modal works without the data key. Owners can add/remove editors;
// everyone else sees a read-only roster. The roster also feeds author names into
// the card timeline (member user_id → username), so we cache it on board load.
async function fetchMembers() {
  const members = await fetchJSON(`/api/boards/${boardId}/members`);
  state.members = members;
  state.memberNames = {};
  for (const m of members) state.memberNames[m.user_id] = m.username || `#${m.user_id}`;
  return members;
}

async function loadMembers() {
  if (!xySync.isOnline()) return;
  try { await fetchMembers(); } catch (_) {}
  if (!state.me) {
    try { state.me = await fetchJSON(`/api/auth/me`); } catch (_) {}
  }
}

function openMembers() {
  document.getElementById("membersMessage").textContent = "";
  document.getElementById("membersOverlay").hidden = false;
  renderMembers();
}

function closeMembers() { document.getElementById("membersOverlay").hidden = true; }

async function renderMembers() {
  const listNode = document.getElementById("membersList");
  const addForm = document.getElementById("addMemberForm");
  const msg = document.getElementById("membersMessage");
  listNode.replaceChildren();
  let members;
  try {
    members = await fetchMembers();
  } catch (_) {
    msg.textContent = "Не удалось загрузить участников — нужно подключение к сети.";
    addForm.hidden = true;
    return;
  }
  const isOwner = state.role === "owner";
  addForm.hidden = !isOwner;
  for (const m of members) {
    const row = el("div", { class: "member-row" },
      el("span", { class: "member-name", text: m.username || `#${m.user_id}` }),
      el("span", { class: "member-role", text: m.role === "owner" ? "владелец" : "редактор" }),
    );
    if (isOwner && m.role !== "owner") {
      row.append(el("button", {
        class: "attach-del member-del", type: "button", title: "Убрать из доски", text: "×",
        onclick: () => removeMember(m),
      }));
    }
    listNode.append(row);
  }
}

async function removeMember(m) {
  if (!confirm(`Убрать ${m.username || "участника"} из доски?`)) return;
  try {
    await jdelete(`/api/boards/${boardId}/members/${m.user_id}`);
    await renderMembers();
  } catch (e) {
    document.getElementById("membersMessage").textContent = e.message;
  }
}

document.getElementById("addMemberForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = document.getElementById("addMemberName");
  const msg = document.getElementById("membersMessage");
  const name = input.value.trim();
  msg.textContent = "";
  if (!name) return;
  try {
    await jpost(`/api/boards/${boardId}/members`, { username: name });
    input.value = "";
    await renderMembers();
  } catch (e) {
    msg.textContent = e.message;
  }
});

document.getElementById("membersClose").addEventListener("click", closeMembers);
document.getElementById("membersOverlay").addEventListener("pointerdown", (e) => {
  if (e.target.id === "membersOverlay") closeMembers();
});

// ---- read markers (blue dots) + 🔔 activity bell ----
// Every user wants to read every OTHER user's changes; own edits never count.
// Read-tracking is online-only best-effort (like loadMembers above): it never
// goes through the sync outbox, so it's simply skipped offline.
const notifToggle = document.getElementById("notifToggle");
const notifBadge = document.getElementById("notifBadge");

// renderNotifBadge shows the 🔔 badge iff any card has an unread bucket.
function renderNotifBadge() {
  const any = Object.values(state.unread).some((u) => u.content || u.comments);
  notifBadge.hidden = !any;
}

// refreshCardUnreadDot updates a single kanban card's dot in place (cheaper
// than a full render() and doesn't disturb drag state).
function refreshCardUnreadDot(cardId) {
  const node = kanban.querySelector(`.kcard[data-card-id="${cardId}"]`);
  if (!node) return;
  const u = state.unread[cardId];
  const wantDot = !!(u && (u.content || u.comments));
  const existing = node.querySelector(".kcard-unread");
  if (wantDot && !existing) node.append(el("span", { class: "unread-dot unread-dot-corner kcard-unread", title: "Непрочитанные изменения" }));
  else if (!wantDot && existing) existing.remove();
}

// markCardRead advances the caller's read watermark(s) for a card to the
// highest event id currently loaded in its timeline (captured by loadTimeline
// into openCardEvents), then updates local state + the dots. Best-effort:
// failures are swallowed (a missed watermark just means the dot lingers).
async function markCardRead(cardId, { content = false, comments = false } = {}) {
  if (!xySync.isOnline()) return;
  const events = openCardEvents || [];
  const maxId = (pred) => events.filter(pred).reduce((m, e) => (e.id > m ? e.id : m), 0);
  const contentReadId = content ? maxId((e) => e.type !== "comment") : 0;
  const commentReadId = comments ? maxId((e) => e.type === "comment") : 0;
  if (!contentReadId && !commentReadId) return;
  try {
    await jpost(`/api/cards/${cardId}/read`, { content_read_id: contentReadId, comment_read_id: commentReadId });
  } catch (_) { return; }
  const u = { ...(state.unread[cardId] || {}) };
  if (content) u.content = false;
  if (comments) u.comments = false;
  if (u.content || u.comments) state.unread[cardId] = u;
  else delete state.unread[cardId];
  if (content) document.getElementById("contentUnreadDot").hidden = true;
  if (comments) document.getElementById("commentsUnreadDot").hidden = true;
  refreshCardUnreadDot(cardId);
  renderNotifBadge();
}

// ---- 🔔 bell panel: recent other-authored activity, newest first ----
let notifPanelEl = null;

function closeNotifPanel() {
  if (!notifPanelEl) return;
  notifPanelEl.remove();
  notifPanelEl = null;
  notifToggle.setAttribute("aria-expanded", "false");
  document.removeEventListener("pointerdown", onNotifOutside, true);
  document.removeEventListener("keydown", onNotifKey);
}
function onNotifOutside(e) { if (notifPanelEl && !notifPanelEl.contains(e.target) && e.target !== notifToggle) closeNotifPanel(); }
function onNotifKey(e) { if (e.key === "Escape") closeNotifPanel(); }

async function openNotifPanel() {
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
  notifToggle.parentElement.append(panel);
  notifPanelEl = panel;
  document.addEventListener("pointerdown", onNotifOutside, true);
  document.addEventListener("keydown", onNotifKey);

  let events = [];
  try { events = await fetchJSON(`/api/boards/${boardId}/activity`); } catch (_) {}
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
    const verbs = {
      comment: "комментарий", desc_edit: "правка описания",
      label_add: "добавлена метка", label_remove: "снята метка",
      attach_add: "вложение добавлено", attach_remove: "вложение удалено", attach_replace: "вложение заменено",
    };
    const verb = verbs[ev.type] || ev.type;
    const when = new Date(ev.created_at).toLocaleString("ru-RU");
    const bodyWrap = el("div", { class: "notif-row-body" },
      el("div", { class: "notif-row-meta", text: `${eventAuthor(ev)} ${verb} · ${cardTitle(card)} · ${when}` }));
    if (ev.type === "comment") {
      let preview = "";
      try { preview = await xyCrypto.decField(dk, ev.payload_enc); } catch (_) {}
      bodyWrap.append(el("div", { class: "notif-row-preview", text: deriveTitle(preview, 120) }));
    }
    row.append(bodyWrap);
    row.addEventListener("click", () => {
      closeNotifPanel();
      openCard(card).then(() => { if (ev.type === "comment") highlightComment(ev.id); });
    });
    body.append(row);
  }
}

notifToggle.addEventListener("click", () => { if (notifPanelEl) closeNotifPanel(); else openNotifPanel(); });

// ---- load + decrypt snapshot ----
// Source of truth: when online with an empty outbox, fetch the authoritative
// snapshot and refresh the mirror. With local edits queued (or offline), render
// the mirror, which the sync engine keeps current (server snapshot + applied
// pending ops). After the queue drains, onBoardSynced reloads with real ids.
let loading = false;
async function load() {
  if (loading) return; // dedupe overlapping refreshes (e.g. visibility + online)
  loading = true;
  setStatus("saving");
  try {
    let snap;
    const pending = await xySync.pendingCountForBoard(boardId);
    if (xySync.isOnline() && pending === 0) {
      try {
        snap = await fetchJSON(`/api/boards/${boardId}`);
        await xySync.saveSnapshot(boardId, snap);
      } catch (_) {
        snap = await xySync.loadSnapshot(boardId);
      }
    } else {
      snap = await xySync.loadSnapshot(boardId);
      if (!snap && xySync.isOnline()) {
        snap = await fetchJSON(`/api/boards/${boardId}`);
        await xySync.saveSnapshot(boardId, snap);
      }
    }
    if (!snap) {
      kanban.hidden = true;
      titleNode.textContent = "Доска недоступна офлайн";
      setStatus("error");
      statusNode.title = "Нет сохранённой копии — откройте доску при подключении";
      return;
    }
    state.role = snap.role || "editor";
    state.cardLabels = snap.card_labels || {};
    state.unread = snap.unread || {};
    // The caller's per-user display prefs (same on every board); absent (never
    // set) → defaults. Apply now so the board renders at the user's saved sizes.
    state.sizes = xySizes.sanitize(snap.sizes);
    xySizes.apply(state.sizes);
    state.defaultAuthor = snap.default_author || "";
    // Migrated boards (schema_version 2) carry a plaintext name; legacy boards still
    // need the DK to decrypt name_enc — and, since we now hold it, get backfilled.
    if (snap.schema_version >= 2) {
      state.name = snap.name;
    } else {
      state.name = await xyCrypto.decField(dk, snap.name_enc);
      migrateBoardName(state.name);
    }
    titleNode.textContent = state.name;
    document.title = state.name + " · xy";
    state.lists = await Promise.all(snap.lists.map(async (l) => ({
      id: l.id, type: l.type, rank: l.rank, groupId: l.group_id != null ? l.group_id : null,
      title: await xyCrypto.decField(dk, l.title_enc),
    })));
    state.groups = await Promise.all((snap.groups || []).map(async (g) => ({
      id: g.id, name: await xyCrypto.decField(dk, g.name_enc),
    })));
    state.cards = await Promise.all(snap.cards.map(async (c) => ({
      id: c.id, listId: c.list_id, kind: c.kind, rank: c.rank,
      desc: await xyCrypto.decField(dk, c.description_enc),
      handoutMeta: c.handout_meta_enc ? await xyCrypto.decField(dk, c.handout_meta_enc) : null,
    })));
    state.labels = await Promise.all(snap.labels.map(async (l) => ({
      id: l.id, kind: l.kind,
      name: await xyCrypto.decField(dk, l.name_enc),
      color: await xyCrypto.decField(dk, l.color_enc),
    })));
    render();
    renderNotifBadge();
    setStatus("saved");
    loadMembers(); // best-effort: populate the author-name map for timelines (online only)
    maybeOpenDeepLink(); // open a ?card=… / &comment=… deep link on first load
    pingVisit(); // stamp last-visit so the board list can order by it (online-only, once)
  } catch (e) {
    setStatus("error");
    console.error(e);
  } finally {
    loading = false;
  }
}

// There is no live push from the server, so a tab left in the background misses
// remote changes made meanwhile. Re-pull the authoritative snapshot when the tab
// returns to the foreground (only once unlocked). load() itself skips the network
// fetch when offline or when local edits are still queued, and its `loading`
// guard dedupes this against the sync engine's own onBoardSynced reloads.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && dk) load();
});

// pingVisit stamps this board as most-recently-visited (for the board-list
// ordering). Online-only best-effort, fired once per page session.
let visitPinged = false;
function pingVisit() {
  if (visitPinged || !xySync.isOnline()) return;
  visitPinged = true;
  jpost(`/api/boards/${boardId}/visit`, {}).catch(() => {});
}

const byRank = (a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0);
const cardsOf = (listId) => state.cards.filter((c) => c.listId === listId).sort(byRank);
const labelById = (id) => state.labels.find((l) => l.id === id);

// ---- render ----
const groupById = (id) => state.groups.find((g) => g.id === id);

// listsInGroup returns a group's member lists in board (rank) order.
function listsInGroup(groupId) {
  return state.lists.filter((l) => l.groupId === groupId).sort(byRank);
}

// groupNumbering computes question numbers continuously across a group's lists:
// the cards of every member list are concatenated in order, numbered as one run
// (so list 2 picks up where list 1 left off, № / №№ directives included), then
// sliced back per list. Returns Map(listId → numbers[]).
function groupNumbering(lists) {
  const arrays = lists.map((l) => cardsOf(l.id));
  const numbers = xyChgk.numberQuestionCards(arrays.flat());
  const map = new Map();
  let off = 0;
  arrays.forEach((arr, i) => { map.set(lists[i].id, numbers.slice(off, off + arr.length)); off += arr.length; });
  return map;
}

// questionCountLabel declines "вопрос" for n: 1 вопрос, 2 вопроса, 12 вопросов.
function questionCountLabel(n) {
  const m10 = n % 10, m100 = n % 100;
  const word = m100 >= 11 && m100 <= 14 ? "вопросов"
    : m10 === 1 ? "вопрос"
    : m10 >= 2 && m10 <= 4 ? "вопроса" : "вопросов";
  return `${n} ${word}`;
}

function render() {
  kanban.hidden = false;
  // Preserve scroll positions across the full rebuild below — otherwise a drag
  // (or any mutation that re-renders) snaps the board back to the top-left, which
  // is jarring mid-edit. Capture the horizontal board scroll + each list's
  // vertical scroll, then restore them once the fresh DOM is in place.
  const scrollLeft = kanban.scrollLeft;
  const listScroll = new Map();
  for (const b of kanban.querySelectorAll(".kcards")) listScroll.set(b.dataset.listId, b.scrollTop);
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
      const run = [];
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
  for (const b of kanban.querySelectorAll(".kcards")) {
    if (listScroll.has(b.dataset.listId)) b.scrollTop = listScroll.get(b.dataset.listId);
  }
}

function renderList(list, precomputedNumbers) {
  const col = el("div", { class: "klist", draggable: "true", dataset: { listId: list.id } });
  const menuWrap = el("div", { class: "klist-menu-wrap" });
  const menuBtn = el("button", { class: "kadd", title: "Меню списка", text: "⋯", "aria-haspopup": "true" });
  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    const items = [{ label: "➕ Добавить карточку", onClick: () => addCard(list) }];
    if (list.type === "test") items.push({ label: "👥 Копировать список тестеров", onClick: () => copyTesterList(list) });
    if (list.groupId != null) {
      items.push(
        { label: "🔍 Предпросмотр списка", onClick: () => previewList(list) },
        { label: "🔍 Предпросмотр всей группы", onClick: () => previewList(list, true) },
      );
    } else {
      items.push({ label: "🔍 Предпросмотр", onClick: () => previewList(list) });
    }
    items.push(
      { label: "↔️ Переместить список…", onClick: () => openMoveList(list) },
      { label: "✏️ Переименовать список", onClick: () => renameList(list) },
    );
    // Export / handout generation are question-list features; skip them for
    // test lists (whose cards hold tester sessions, not 4s questions).
    if (list.type !== "test") {
      const grouped = list.groupId != null;
      const suffix = grouped ? " группы" : "";
      items.push(
        { label: `📄 Экспорт${suffix} в docx`, onClick: () => exportList(list, "docx") },
        { label: `📕 Экспорт${suffix} в PDF`, onClick: () => exportList(list, "pdf") },
        { label: grouped ? "🧩 Генерация раздаток (вся группа)" : "🧩 Генерация раздаток", onClick: () => openHandouts(list) },
      );
    }
    items.push({ label: "🗑️ Удалить список", onClick: () => deleteList(list) });
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
  col.append(el("div", { class: "klist-head" }, headMain, menuWrap));
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

  // list drag
  col.addEventListener("dragstart", (e) => {
    if (e.target !== col) return;
    e.dataTransfer.setData("text/xy-list", String(list.id));
    col.classList.add("dragging");
  });
  col.addEventListener("dragend", () => col.classList.remove("dragging"));

  // card drop target
  body.addEventListener("dragover", (e) => {
    if (!e.dataTransfer.types.includes("text/xy-card")) return;
    e.preventDefault();
    const after = dragAfter(body, e.clientY);
    const dragging = document.querySelector(".kcard.dragging");
    if (!dragging) return;
    if (after == null) body.append(dragging);
    else body.insertBefore(dragging, after);
  });
  body.addEventListener("drop", (e) => {
    if (!e.dataTransfer.types.includes("text/xy-card")) return;
    e.preventDefault();
    if (cardDragCommitted) return; // ignore a stray second drop from the same gesture
    cardDragCommitted = true;
    const cardId = Number(e.dataTransfer.getData("text/xy-card"));
    commitCardMove(cardId, list.id, body);
  });
  return col;
}

// renameList re-encrypts a new title under the board key and patches the list
// (offline-capable via the sync outbox).
async function renameList(list) {
  const name = prompt("Новое название списка:", list.title || "");
  if (name == null) return;
  const t = name.trim();
  if (!t || t === list.title) return;
  setStatus("saving");
  try {
    await patch("patchList", `/api/lists/${list.id}`, { title_enc: await xyCrypto.encField(dk, t) });
    list.title = t;
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); alert("Не удалось переименовать: " + err.message); }
}

// deleteList soft-deletes the list and its cards (server cascades the cards),
// offline-capable via the sync outbox.
async function deleteList(list) {
  const n = cardsOf(list.id).length;
  const tail = n ? ` и ${n} карточк(и) в нём` : "";
  if (!confirm(`Удалить список «${list.title || "без названия"}»${tail}? Это действие необратимо.`)) return;
  setStatus("saving");
  try {
    await del("deleteList", `/api/lists/${list.id}`);
    state.lists = state.lists.filter((l) => l.id !== list.id);
    state.cards = state.cards.filter((c) => c.listId !== list.id);
    if (openCardId != null && !state.cards.some((c) => c.id === openCardId)) closeCard();
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); alert("Не удалось удалить: " + err.message); }
}

// Cards carry the card's *whole* text (whitespace collapsed), not a truncated
// preview: how much of it is visible is a display choice, made in CSS by the
// --kcard-lines clamp (see the sizes modal). Truncating here instead would cap
// the card at 80 characters no matter how much room the reader gives it.
const cardBody = (card) => deriveTitle(xyChgk.previewText(card.kind, card.desc), Infinity);

// cardTitle is the plain-text form (move/copy dialogs, titles); renderCardTitle
// below is the DOM form.
function cardTitle(card, number) {
  if (card.kind === "test") return testTitle(card.desc);
  const body = cardBody(card);
  if (card.kind === "question" && number) return `${number}. ${body}`;
  return body;
}

// renderCardTitle builds the title node. For numbered question cards the auto/
// directive number is rendered in a muted span so it reads as scaffolding,
// visually distinct from the question content itself.
function renderCardTitle(card, number) {
  if (card.kind === "question" && number) {
    return el("div", { class: "kcard-title" },
      el("span", { class: "kcard-num", text: `${number}. ` }),
      cardBody(card));
  }
  return el("div", { class: "kcard-title", text: cardTitle(card, number) });
}

function renderCard(card, number) {
  const node = el("div", { class: "kcard kcard-" + (card.kind || "normal"), draggable: "true", dataset: { cardId: card.id }, onclick: () => openCard(card) });
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
    e.dataTransfer.setData("text/xy-card", String(card.id));
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
function paintLabels() {
  for (const chip of document.querySelectorAll(".label-chip[data-c]")) {
    chip.style.backgroundColor = chip.dataset.c;
  }
  for (const sw of document.querySelectorAll(".label-pick[data-c], .label-swatch[data-c]")) {
    sw.style.backgroundColor = sw.dataset.c;
  }
}

// dragAfterIn returns which of `els` the dragged node should be inserted
// before, given the pointer's y (null = append at the end).
function dragAfterIn(els, y) {
  let closest = null, closestOffset = -Infinity;
  for (const c of els) {
    const box = c.getBoundingClientRect();
    const offset = y - box.top - box.height / 2;
    if (offset < 0 && offset > closestOffset) { closestOffset = offset; closest = c; }
  }
  return closest;
}

function dragAfter(container, y) {
  return dragAfterIn([...container.querySelectorAll(".kcard:not(.dragging)")], y);
}

// ---- add list / card ----
function renderAddList() {
  const wrap = el("div", { class: "klist klist-add" });
  const form = el("form", { class: "kadd-form" });
  const input = el("input", { class: "input", type: "text", placeholder: "+ Новый список" });
  const typeRow = el("label", { class: "attach-lossless" },
    el("input", { type: "checkbox", id: "newListTest" }), " тест-список");
  form.append(input, typeRow);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const title = input.value.trim();
    if (!title) return;
    const type = typeRow.querySelector("input").checked ? "test" : "normal";
    const ranks = [...state.lists].sort(byRank);
    const rank = keyBetween(ranks.length ? ranks[ranks.length - 1].rank : null, null);
    try {
      const titleEnc = await xyCrypto.encField(dk, title);
      const res = await create("createList", `/api/boards/${boardId}/lists`, { title_enc: titleEnc, rank, type });
      state.lists.push({ id: res.id, type, rank, title });
      input.value = "";
      typeRow.querySelector("input").checked = false;
      render();
    } catch (err) { setStatus("error"); }
  });
  wrap.append(form);
  return wrap;
}

// ---- list menu (popup) ----

// popupMenu mounts a small dropdown (dope .menu-dropdown styling) inside a
// position:relative anchor, closing on outside click / Escape / item choice.
// Reused by the per-list "⋯" menu.
function popupMenu(anchor, items) {
  const existing = anchor.querySelector(".menu-dropdown");
  if (existing) { existing.remove(); return; } // toggle off
  const menu = el("div", { class: "menu-dropdown", role: "menu" });
  for (const it of items) {
    menu.append(el("button", {
      class: "menu-item", type: "button", role: "menuitem", text: it.label,
      onclick: () => { close(); it.onClick(); },
    }));
  }
  function close() {
    menu.remove();
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey);
  }
  function onOutside(e) { if (!anchor.contains(e.target)) close(); }
  function onKey(e) { if (e.key === "Escape") close(); }
  anchor.append(menu);
  document.addEventListener("pointerdown", onOutside, true);
  document.addEventListener("keydown", onKey);
}

// ---- move / copy a whole list (within board → re-rank/duplicate; other board →
// client-side re-encryption of the list title + every card + label reconcile,
// mirroring the per-card move/copy below). The destination board is chosen by its
// (decrypted) name and the insertion position among its lists is selectable. ----

let listMoveSrc = null;  // the list being moved/copied
let listMoveCtx = null;  // destination board ctx (from loadMoveBoard)

function openMoveList(list) {
  listMoveSrc = list;
  document.getElementById("moveListMessage").textContent = "";
  document.getElementById("moveListOverlay").hidden = false;
  populateMoveListBoards();
}
function closeMoveList() { document.getElementById("moveListOverlay").hidden = true; }

// populateMoveListBoards fills the board <select> with decrypted board names
// (current board first/default), then loads the chosen board's list positions.
async function populateMoveListBoards() {
  const sel = document.getElementById("moveListBoard");
  sel.replaceChildren();
  let boards = [];
  try { boards = await fetchJSON("/api/boards"); } catch (_) {}
  if (!boards.some((b) => b.id === boardId)) boards.unshift({ id: boardId, name_enc: null });
  for (const b of boards) {
    let label = "доска #" + b.id;
    if (b.id === boardId) label = (state.name || label) + " (эта доска)";
    else if (b.schema_version >= 2) label = b.name; // plaintext name, no key needed
    else {
      try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc); }
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
async function onMoveListBoardChange() {
  const posSel = document.getElementById("moveListPos");
  const bid = Number(document.getElementById("moveListBoard").value);
  posSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
  try { listMoveCtx = await loadMoveBoard(bid); }
  catch (err) {
    listMoveCtx = null;
    posSel.replaceChildren(el("option", { value: "", text: err.message }));
    return;
  }
  const lists = listMoveCtx.lists.filter((l) => !(listMoveCtx.boardId === boardId && l.id === listMoveSrc.id));
  posSel.replaceChildren(el("option", { value: "end", text: "в конец" }));
  for (let i = 1; i <= lists.length; i++) posSel.append(el("option", { value: String(i), text: `позиция ${i}` }));
  posSel.value = "end";
}

async function doMoveListCopy(remove) {
  if (!listMoveSrc || !listMoveCtx) return;
  const targetBid = listMoveCtx.boardId;
  const sameBoard = targetBid === boardId;
  const msg = document.getElementById("moveListMessage");
  const rank = rankForSlot(listMoveCtx.lists, document.getElementById("moveListPos").value, sameBoard ? listMoveSrc.id : null);
  const srcCards = cardsOf(listMoveSrc.id);
  const type = listMoveSrc.type || "normal";

  // A grouped list must stay consecutive with its group, so reordering it on the
  // same board goes through «Управление списками» (which moves the whole group as
  // a unit). Copying it, or moving it to another board, is still fine.
  if (sameBoard && remove && listMoveSrc.groupId != null) {
    msg.textContent = "Список входит в группу — измените порядок через «Управление списками».";
    return;
  }

  // Same-board move is just a re-rank (no re-encryption needed).
  if (sameBoard && remove) {
    listMoveSrc.rank = rank;
    setStatus("saving");
    try {
      await patch("patchList", `/api/lists/${listMoveSrc.id}`, { rank });
      setStatus("saved"); render(); closeMoveList();
    } catch (err) { setStatus("error"); msg.textContent = err.message; load(); }
    return;
  }

  // Copying a list (it carries every card's comments/attachments) and any
  // cross-board op are online-only; only the intra-board move above works offline.
  if (!xySync.isOnline()) { msg.textContent = "Копирование и перенос между досками доступны только онлайн."; return; }
  msg.textContent = sameBoard ? "Копирование…" : "Перешифровка…";
  try {
    if (sameBoard) {
      // Duplicate the list and its cards on this board.
      const lres = await jpost(`/api/boards/${boardId}/lists`, {
        title_enc: await xyCrypto.encField(dk, listMoveSrc.title), rank, type,
      });
      state.lists.push({ id: lres.id, type, rank, title: listMoveSrc.title });
      let cr = null;
      for (const c of srcCards) {
        cr = keyBetween(cr, null);
        const cres = await jpost(`/api/lists/${lres.id}/cards`, await cardCopyBody(c, cr, dk));
        state.cards.push({ id: cres.id, listId: lres.id, kind: c.kind, rank: cr, desc: c.desc, handoutMeta: c.handoutMeta || null });
        const ids = state.cardLabels[c.id] || [];
        if (ids.length) { await jput(`/api/cards/${cres.id}/labels`, { label_ids: ids }); state.cardLabels[cres.id] = ids.slice(); }
        await copyCardExtras(c.id, dk, cres.id);
      }
    } else {
      // Cross-board: re-encrypt under the target board's key, reconcile labels by
      // decrypted name+color (same as the per-card path).
      const tdk = listMoveCtx.dk;
      const tLabels = listMoveCtx.labels.slice();
      const lres = await jpost(`/api/boards/${targetBid}/lists`, {
        title_enc: await xyCrypto.encField(tdk, listMoveSrc.title), rank, type,
      });
      let cr = null;
      for (const c of srcCards) {
        cr = keyBetween(cr, null);
        const cres = await jpost(`/api/lists/${lres.id}/cards`, await cardCopyBody(c, cr, tdk));
        const srcIds = state.cardLabels[c.id] || [];
        if (srcIds.length) {
          const targetIds = [];
          for (const sid of srcIds) {
            const sl = labelById(sid);
            if (!sl) continue;
            let match = tLabels.find((t) => t.name === sl.name && t.color === sl.color);
            if (!match) {
              const labres = await jpost(`/api/boards/${targetBid}/labels`, {
                name_enc: await xyCrypto.encField(tdk, sl.name), color_enc: await xyCrypto.encField(tdk, sl.color), kind: sl.kind,
              });
              match = { id: labres.id, name: sl.name, color: sl.color };
              tLabels.push(match);
            }
            targetIds.push(match.id);
          }
          if (targetIds.length) await jput(`/api/cards/${cres.id}/labels`, { label_ids: targetIds });
        }
        await copyCardExtras(c.id, tdk, cres.id);
      }
      if (remove) {
        await jdelete(`/api/lists/${listMoveSrc.id}`);
        state.lists = state.lists.filter((l) => l.id !== listMoveSrc.id);
        state.cards = state.cards.filter((c) => c.listId !== listMoveSrc.id);
      }
    }
    render();
    msg.textContent = remove ? "Перемещено." : "Скопировано.";
    setTimeout(closeMoveList, 700);
  } catch (err) { msg.textContent = err.message; }
}

document.getElementById("moveListBoard").addEventListener("change", onMoveListBoardChange);
document.getElementById("moveListCopyBtn").addEventListener("click", () => doMoveListCopy(false));
document.getElementById("moveListMoveBtn").addEventListener("click", () => doMoveListCopy(true));
document.getElementById("moveListClose").addEventListener("click", closeMoveList);
document.getElementById("moveListOverlay").addEventListener("pointerdown", (e) => {
  if (e.target.id === "moveListOverlay") closeMoveList();
});

// ---- lists management (reorder + group into list_of_lists) ----
// The «Управление списками» modal shows one row per list (and a bordered block
// per group). Lists can be reordered by dragging a row or by entering a target
// position; checking several rows lets you move them together or — when the
// checked rows are consecutive, ungrouped lists — link them into a group.
// Orderable units are standalone lists and whole groups; a group always moves as
// one block, keeping its members consecutive (the invariant the board relies on).
const listsManageOverlay = document.getElementById("listsManageOverlay");
const listsManageRows = document.getElementById("listsManageRows");
let manageSelected = new Set();       // selected unit keys ("l"+listId / "g"+groupId)
let manageUnitByKey = new Map();      // key → unit (rebuilt each render)
let manageDragKey = null;
let manageDragCommitted = false;
// Dragging a member row *inside* its group (reorder within, never across):
// the group id whose members container owns the gesture.
let memberDragGid = null;
let memberDragCommitted = false;

// computeUnits walks the rank-sorted lists, folding each maximal run of lists
// sharing a group_id into one group unit; ungrouped lists are singleton units.
function computeUnits() {
  const sorted = [...state.lists].sort(byRank);
  const units = [];
  let i = 0;
  while (i < sorted.length) {
    const l = sorted[i];
    if (l.groupId != null) {
      const gid = l.groupId, run = [];
      while (i < sorted.length && sorted[i].groupId === gid) { run.push(sorted[i]); i++; }
      units.push({ kind: "group", id: gid, key: "g" + gid, lists: run });
    } else {
      units.push({ kind: "list", id: l.id, key: "l" + l.id, lists: [l] });
      i++;
    }
  }
  return units;
}

function openListsManage() {
  manageSelected = new Set();
  document.getElementById("listsManageMessage").textContent = "";
  document.getElementById("listsMovePos").value = "";
  listsManageOverlay.hidden = false;
  renderManage();
}
function closeListsManage() { listsManageOverlay.hidden = true; }

function renderManage() {
  const units = computeUnits();
  manageUnitByKey = new Map(units.map((u) => [u.key, u]));
  // Drop selections whose units no longer exist (e.g. after a group dissolved).
  for (const k of [...manageSelected]) if (!manageUnitByKey.has(k)) manageSelected.delete(k);
  listsManageRows.replaceChildren();
  units.forEach((u, idx) => listsManageRows.append(renderManageUnit(u, idx + 1)));
  updateManageToolbar(units);
}

function manageCheckbox(unit) {
  const cb = el("input", { type: "checkbox" });
  cb.checked = manageSelected.has(unit.key);
  cb.addEventListener("change", () => {
    if (cb.checked) manageSelected.add(unit.key); else manageSelected.delete(unit.key);
    updateManageToolbar(computeUnits());
  });
  return el("label", { class: "lm-check" }, cb);
}

function manageMoveControl(unit) {
  const inp = el("input", { class: "input lm-move-pos", type: "number", min: "1", placeholder: "№" });
  const btn = el("button", { class: "btn btn-small btn-ghost lm-move-btn", type: "button", text: "↕️", title: "Переместить на эту позицию" });
  const go = () => { const n = parseInt(inp.value, 10); if (n >= 1) moveUnitsTo(new Set([unit.key]), n); };
  btn.addEventListener("click", go);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go(); } });
  return el("div", { class: "lm-move" }, inp, btn);
}

function manageTitle(list) {
  return (list.type === "test" ? "🧪 " : "") + (list.title || "(без названия)");
}

function renderManageUnit(unit, pos) {
  const node = el("div", { class: "lm-unit lm-" + unit.kind, draggable: "true", dataset: { unitKey: unit.key } });
  if (unit.kind === "group") {
    const g = groupById(unit.id);
    node.append(el("div", { class: "lm-row lm-grouphead" },
      manageCheckbox(unit),
      el("span", { class: "lm-pos", text: "#" + pos }),
      el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
      el("span", { class: "lm-title lm-group-title", text: "🔗 " + ((g && g.name) || "Связанные списки") }),
      el("button", { class: "lm-icon", type: "button", text: "✏️", title: "Переименовать группу", onclick: () => renameGroup(unit.id) }),
      el("button", { class: "lm-icon", type: "button", text: "✂️", title: "Разъединить группу", onclick: () => unlinkGroup(unit.id) }),
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
        e.dataTransfer.effectAllowed = "move";
        try { e.dataTransfer.setData("text/plain", "m" + l.id); } catch (_) {}
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
      const byId = new Map(unit.lists.map((l) => [String(l.id), l]));
      const order = [...members.querySelectorAll(".lm-member")].map((n) => byId.get(n.dataset.listId)).filter(Boolean);
      if (order.length === unit.lists.length) applyMemberOrder(unit.key, order);
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
    e.dataTransfer.effectAllowed = "move";
    try { e.dataTransfer.setData("text/plain", unit.key); } catch (_) {}
  });
  node.addEventListener("dragend", () => {
    node.classList.remove("dragging");
    manageDragKey = null;
    if (!manageDragCommitted) renderManage(); // aborted drag — resync DOM from state
  });
  return node;
}

function manageDragAfter(y) {
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
  const order = [...listsManageRows.querySelectorAll(".lm-unit")].map((n) => manageUnitByKey.get(n.dataset.unitKey)).filter(Boolean);
  applyUnitOrder(order);
});

function updateManageToolbar(units) {
  const linkBtn = document.getElementById("listsLinkBtn");
  const moveBtn = document.getElementById("listsMoveBtn");
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
async function applyUnitOrder(orderedUnits) {
  const msg = document.getElementById("listsManageMessage");
  const flat = orderedUnits.flatMap((u) => u.lists);
  let r = null;
  const patches = [];
  for (const l of flat) { r = keyBetween(r, null); if (l.rank !== r) patches.push([l, r]); }
  if (!patches.length) { renderManage(); return; }
  setStatus("saving");
  try {
    for (const [l, rank] of patches) { l.rank = rank; await patch("patchList", `/api/lists/${l.id}`, { rank }); }
    setStatus("saved");
    render();
    renderManage();
  } catch (err) { setStatus("error"); msg.textContent = err.message; load(); }
}

// applyMemberOrder reorders the lists INSIDE one group: the group keeps its
// place among the units, only its members' ranks are rewritten.
function applyMemberOrder(unitKey, order) {
  const units = computeUnits();
  const target = units.find((u) => u.key === unitKey);
  if (!target) return Promise.resolve();
  target.lists = order;
  return applyUnitOrder(units);
}

// moveUnitsTo relocates the selected units, preserving their relative order, so
// the first lands at 1-based position posN among all units.
function moveUnitsTo(keys, posN) {
  const units = computeUnits();
  const selected = units.filter((u) => keys.has(u.key));
  if (!selected.length) return Promise.resolve();
  const remaining = units.filter((u) => !keys.has(u.key));
  const idx = Math.max(0, Math.min(posN - 1, remaining.length));
  remaining.splice(idx, 0, ...selected);
  return applyUnitOrder(remaining);
}

async function linkSelected() {
  const units = computeUnits();
  const selected = units.filter((u) => manageSelected.has(u.key));
  if (selected.length < 2 || selected.some((u) => u.kind !== "list")) return;
  const msg = document.getElementById("listsManageMessage");
  if (!xySync.isOnline()) { msg.textContent = "Связывание списков доступно только онлайн."; return; }
  const name = (prompt("Название списка списков:", "") || "").trim();
  if (!name) return;
  // Preserve board order (units are rank-sorted).
  const listIds = selected.sort((a, b) => units.indexOf(a) - units.indexOf(b)).flatMap((u) => u.lists.map((l) => l.id));
  try {
    await jpost(`/api/boards/${boardId}/list-groups`, { name_enc: await xyCrypto.encField(dk, name), list_ids: listIds });
    manageSelected = new Set();
    await load();
    renderManage();
  } catch (err) { msg.textContent = err.message; }
}

async function renameGroup(gid) {
  const g = groupById(gid);
  const name = (prompt("Новое название группы:", g ? g.name : "") || "").trim();
  if (!name) return;
  const msg = document.getElementById("listsManageMessage");
  if (!xySync.isOnline()) { msg.textContent = "Переименование доступно только онлайн."; return; }
  try {
    await jpatch(`/api/list-groups/${gid}`, { name_enc: await xyCrypto.encField(dk, name) });
    await load();
    renderManage();
  } catch (err) { msg.textContent = err.message; }
}

async function unlinkGroup(gid) {
  if (!confirm("Разъединить группу? Списки останутся, но нумерация снова станет раздельной.")) return;
  const msg = document.getElementById("listsManageMessage");
  if (!xySync.isOnline()) { msg.textContent = "Разъединение доступно только онлайн."; return; }
  try {
    await jdelete(`/api/list-groups/${gid}`);
    await load();
    renderManage();
  } catch (err) { msg.textContent = err.message; }
}

document.getElementById("listsLinkBtn").addEventListener("click", linkSelected);
document.getElementById("listsMoveBtn").addEventListener("click", () => {
  const n = parseInt(document.getElementById("listsMovePos").value, 10);
  if (!(n >= 1)) { document.getElementById("listsManageMessage").textContent = "Укажите позицию."; return; }
  moveUnitsTo(new Set(manageSelected), n);
});
document.getElementById("listsManageClose").addEventListener("click", closeListsManage);
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

// importCtx holds the package awaiting confirmation on the verification screen.
let importCtx = null;

const importPickOverlay = document.getElementById("importPickOverlay");

function openImportPick() {
  document.getElementById("importPickForm").reset();
  importPickOverlay.hidden = false;
}
function closeImportPick() { importPickOverlay.hidden = true; }

document.getElementById("importPickForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const file = document.getElementById("importFile").files[0];
  if (!file) return;
  const splitTours = document.getElementById("importSplitTours").checked;
  closeImportPick();
  await importFile(file, splitTours);
});
document.getElementById("importPickCancel").addEventListener("click", closeImportPick);
importPickOverlay.addEventListener("pointerdown", (e) => { if (e.target === importPickOverlay) closeImportPick(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !importPickOverlay.hidden) closeImportPick(); });

async function importFile(file, splitTours) {
  if (!xySync.isOnline()) { alert("Импорт доступен только онлайн."); return; }
  setStatus("saving");
  try {
    const fd = new FormData();
    fd.append("file", file, file.name);
    const res = await fetch("/api/import/parse", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const pkg = await res.json();
    setStatus("saved");
    // A .docx parse is a guess; let the user check it before it becomes a list.
    if (/\.docx$/i.test(file.name)) openImportVerify(pkg, splitTours);
    else await commitImport(pkg.name, pkg.source, pkg.images, splitTours);
  } catch (err) {
    setStatus("error");
    alert("Не удалось разобрать файл: " + err.message);
  }
}

// ---- verification screen (docx) ----

const importOverlay = document.getElementById("importOverlay");

// importCards splits 4s source the way the export path joins it: one card per
// blank-line-separated block. Each card's kind comes from its leading marker.
function importCards(source) {
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
function importKind(desc) {
  const blocks = xyChgk.parseBlocks(desc);
  if (blocks.some((b) => b.type === "question" || b.type === "answer" || b.type === "pre")) return "question";
  if (blocks.some((b) => b.type === "heading" || b.type === "ljheading")) return "heading";
  return "meta";
}

// importImgMap turns the package's base64 images into object URLs so the preview
// can show handouts exactly as the list will once imported.
function importImgMap(images) {
  const map = new Map();
  for (const img of images || []) {
    const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
    map.set(img.name, URL.createObjectURL(new Blob([bytes], { type: img.mime })));
  }
  return map;
}

function openImportVerify(pkg, splitTours) {
  closeImportVerify();
  importCtx = { name: pkg.name, images: pkg.images || [], imgMap: importImgMap(pkg.images), splitTours };
  document.getElementById("importTitle").textContent = "Проверка импорта: " + pkg.name;
  const src = document.getElementById("importSource");
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
function renderImportPreview() {
  const body = document.getElementById("importPreview");
  const cards = importCards(document.getElementById("importSource").value);
  const numbers = xyChgk.numberQuestionCards(cards);
  body.replaceChildren();
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], importCtx.imgMap, false, false)));
  const qs = cards.filter((c) => c.kind === "question").length;
  document.getElementById("importCount").textContent = `${cards.length} блоков, ${qs} вопросов`;
}

function closeImportVerify() {
  importOverlay.hidden = true;
  if (importCtx) for (const url of importCtx.imgMap.values()) URL.revokeObjectURL(url);
  importCtx = null;
  document.getElementById("importPreview").replaceChildren();
}

document.getElementById("importSource").addEventListener("input", debounceImportPreview());
document.getElementById("importClose").addEventListener("click", closeImportVerify);
document.getElementById("importCommit").addEventListener("click", async () => {
  if (!importCtx) return;
  const { name, images, splitTours } = importCtx;
  const source = document.getElementById("importSource").value;
  closeImportVerify();
  await commitImport(name, source, images, splitTours);
});
importOverlay.addEventListener("pointerdown", (e) => { if (e.target === importOverlay) closeImportVerify(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !importOverlay.hidden) closeImportVerify(); });

// Re-rendering the whole preview on every keystroke is wasteful on a big package.
function debounceImportPreview() {
  let t = null;
  return () => {
    clearTimeout(t);
    t = setTimeout(() => { if (importCtx) renderImportPreview(); }, 200);
  };
}

// ---- commit: 4s source + images → a new encrypted list (or a group of them) ----

// splitCardsByTours groups the blocks into tours: a "## …" section block starts
// a new tour and names its list (the section card itself is kept, so the 4s
// source survives export intact). Blocks before the first section — usually the
// editors/date preamble — become their own leading list.
function splitCardsByTours(cards) {
  const tours = [];
  let cur = null;
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
async function commitImport(name, source, images, splitTours) {
  const cards = importCards(source);
  if (!cards.length) { alert("В файле не найдено вопросов."); return; }
  if (!xySync.isOnline()) { alert("Импорт доступен только онлайн."); return; }
  const tours = splitTours ? splitCardsByTours(cards) : [];
  // The server refuses a group of one, and a group of one is pointless anyway.
  const grouped = tours.length >= 2;
  const title = (prompt(grouped ? "Название группы списков:" : "Название нового списка:", name || "Импорт") || "").trim();
  if (!title) return;
  const parts = grouped ? tours : [{ title, cards }];

  setStatus("saving");
  const byName = new Map((images || []).map((i) => [i.name, i]));
  let done = 0, attached = 0;
  const failed = []; // images the server refused — the card would keep a dead (img …)
  try {
    const ranks = [...state.lists].sort(byRank);
    let rank = ranks.length ? ranks[ranks.length - 1].rank : null;
    const listIds = [];
    for (const part of parts) {
      rank = keyBetween(rank, null);
      const lres = await jpost(`/api/boards/${boardId}/lists`, {
        title_enc: await xyCrypto.encField(dk, part.title), rank, type: "normal",
      });
      listIds.push(lres.id);
      state.lists.push({ id: lres.id, type: "normal", rank, title: part.title });

      let cardRank = null;
      for (const c of part.cards) {
        cardRank = keyBetween(cardRank, null);
        const res = await jpost(`/api/lists/${lres.id}/cards`, {
          description_enc: await xyCrypto.encField(dk, c.desc), rank: cardRank, kind: c.kind,
        });
        state.cards.push({ id: res.id, listId: lres.id, kind: c.kind, rank: cardRank, desc: c.desc });
        done++;
        // Attach only the images this card actually references, so a handout lands
        // on the question that uses it (which is where the preview/export look).
        const refs = new Set();
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
      await jpost(`/api/boards/${boardId}/list-groups`, { name_enc: await xyCrypto.encField(dk, title), list_ids: listIds });
      // Reload rather than mirror group_id/groups[] locally — import is online-only.
      await load();
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
    alert(`Импорт прерван после ${done} карточек: ${err.message}\n\nЧастично импортированный список остался на доске — удалите его перед повторным импортом.`);
  }
}

// attachImported encrypts one imported image and posts it as an attachment of
// `cardId`, under the same filename the (img …) directive refers to. Lossless:
// re-encoding would change nothing but could degrade a handout. Returns false (and
// lets the caller report it) if the server rejects it — e.g. an oversized scan.
async function attachImported(cardId, img) {
  try {
    const bytes = Uint8Array.from(atob(img.data), (c) => c.charCodeAt(0));
    const cipher = await xyCrypto.encBytes(dk, bytes);
    const fd = new FormData();
    fd.append("meta", JSON.stringify({
      filename_enc: await xyCrypto.encField(dk, img.name),
      mime: img.mime, lossless: true,
      event_payload_enc: await xyCrypto.encField(dk, JSON.stringify({ file: img.name })),
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
function exportScope(list) {
  let lists = [list], title = list.title || "export";
  if (list.groupId != null) {
    lists = listsInGroup(list.groupId).filter((l) => l.type !== "test");
    const g = groupById(list.groupId);
    if (g && g.name) title = g.name;
  }
  return { cards: lists.flatMap((l) => cardsOf(l.id)), title };
}

async function exportList(list, format = "docx") {
  const ext = format === "pdf" ? "pdf" : "docx";
  const scope = exportScope(list);
  const cards = scope.cards;
  if (!cards.length) { alert("В списке нет карточек."); return; }
  if (!xySync.isOnline()) { alert(`Экспорт в ${ext} доступен только онлайн.`); return; }
  setStatus("saving");
  try {
    const source = cards.map((c) => c.desc.trim()).filter(Boolean).join("\n\n") + "\n";
    // collect (img …) references — the filename is the LAST token (the rest are
    // w=/h=/big/inline options), so use imgName, not the first token.
    const wanted = new Set();
    for (const m of source.matchAll(/\(img\b([^)]*)\)/g)) { const n = imgName(m[1]); if (n) wanted.add(n); }

    const fd = new FormData();
    fd.append("source", source);
    fd.append("filename", scope.title);

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
    alert("Экспорт не удался: " + err.message);
  }
}

// ---- handouts generation (chgksuite .hndt → PDF) ----
// "Генерация раздаток": port of `chgksuite handouts 4s2hndt` (in chgk.js) builds
// an editable .hndt source from the list's questions, merging each question's
// saved layout settings (handout_meta) with its live handout text. "Сгенерировать
// PDF" posts the source + referenced images to the server, which runs
// `chgksuite handouts hndt2pdf` (tectonic) and streams an ephemeral PDF. On close
// the per-question settings (everything but the handout text) are persisted back.
const handoutsOverlay = document.getElementById("handoutsOverlay");
let handoutsCtx = null;   // { list, cards, numbers }
let handoutsPdfUrl = null;

function openHandouts(list) {
  // Grouped lists generate one set of handouts for the whole list_of_lists, with
  // question numbers continuous across the group (numberQuestionCards over the
  // concatenated cards), matching the board + docx export.
  const scope = exportScope(list);
  const cards = scope.cards;
  const numbers = xyChgk.numberQuestionCards(cards);
  const metas = {};
  for (const c of cards) if (c.handoutMeta) metas[c.id] = c.handoutMeta;
  const source = xyChgk.generateHndt(cards, numbers, metas);
  handoutsCtx = { list, cards, numbers, title: scope.title };
  document.getElementById("handoutsSource").value = source;
  document.getElementById("handoutsMessage").textContent = source.trim() ? "" : "В списке нет вопросов с раздаточным материалом.";
  clearHandoutsPdf();
  handoutsOverlay.hidden = false;
  // Pre-stage the referenced images now (in the background) so the first PDF /
  // split_fit generation doesn't pay the gather+upload, and start heartbeating.
  ensureStaged(source).catch(() => {});
  startHandoutHeartbeat();
}

function clearHandoutsPdf() {
  const pane = document.getElementById("handoutsPdf");
  pane.replaceChildren();
  const dl = document.getElementById("handoutsDownload");
  dl.hidden = true;
  if (handoutsPdfUrl) { URL.revokeObjectURL(handoutsPdfUrl); handoutsPdfUrl = null; }
}

// persistHandoutMeta writes the edited per-question settings back onto the cards
// (everything in each .hndt block except the live handout text/image), so the
// layout is restored next time the modal opens.
async function persistHandoutMeta() {
  if (!handoutsCtx) return;
  const source = document.getElementById("handoutsSource").value;
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
      const body = { handout_meta_enc: norm ? await xyCrypto.encField(dk, norm) : "" };
      await patch("patchCard", `/api/cards/${c.id}`, body);
      c.handoutMeta = norm;
    } catch (_) { /* best-effort: keep editing even if a write fails */ }
  }
}

async function closeHandouts() {
  handoutsOverlay.hidden = true;
  unstageHandouts(); // stop heartbeat + delete the staged images server-side
  await persistHandoutMeta();
  clearHandoutsPdf();
  handoutsCtx = null;
}

async function generateHandoutsPdf() {
  if (!handoutsCtx) return;
  if (!xySync.isOnline()) { document.getElementById("handoutsMessage").textContent = "Генерация PDF доступна только онлайн."; return; }
  const source = document.getElementById("handoutsSource").value;
  const msg = document.getElementById("handoutsMessage");
  if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
  const btn = document.getElementById("handoutsGenerate");
  btn.disabled = true;
  msg.textContent = "Генерация…";
  clearHandoutsPdf();
  try {
    const fd = await handoutsBody(source);
    const res = await fetch("/api/handouts/pdf", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const blob = await res.blob();
    handoutsPdfUrl = URL.createObjectURL(blob);
    const embed = el("iframe", { class: "handouts-pdf-frame", src: handoutsPdfUrl, title: "PDF" });
    document.getElementById("handoutsPdf").replaceChildren(embed);
    const dl = document.getElementById("handoutsDownload");
    dl.href = handoutsPdfUrl;
    dl.setAttribute("download", (handoutsCtx.title || handoutsCtx.list.title || "handouts") + ".pdf");
    dl.hidden = false;
    msg.textContent = "Готово.";
  } catch (err) {
    msg.textContent = "Не удалось сгенерировать: " + err.message;
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
const handoutsStage = { sessionId: null, names: null, inflight: null, heartbeat: null };

function wantedImages(source) {
  const wanted = new Set();
  for (const m of source.matchAll(/^\s*image:\s*(.+?)\s*$/gm)) wanted.add(m[1]);
  for (const m of source.matchAll(/\(img\b([^)]*)\)/g)) { const n = imgName(m[1]); if (n) wanted.add(n); }
  return wanted;
}

// stageImages gathers + decrypts the referenced images and uploads them to a new
// server session, returning its id (null when there are none / on error).
async function stageImages(source) {
  const wanted = wantedImages(source);
  if (!wanted.size) { handoutsStage.sessionId = null; handoutsStage.names = new Set(); return null; }
  const fd = new FormData();
  const found = await appendImages(fd, handoutsCtx.cards, wanted);
  try {
    const res = await fetch("/api/handouts/stage", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const { session } = await res.json();
    handoutsStage.sessionId = session;
    handoutsStage.names = found;
    return session;
  } catch (_) { handoutsStage.sessionId = null; return null; }
}

// ensureStaged returns a session id whose staged images cover the source's
// references, staging once if needed (deduped). null when the source has none.
async function ensureStaged(source) {
  const wanted = wantedImages(source);
  if (!wanted.size) return null;
  if (handoutsStage.sessionId && handoutsStage.names && [...wanted].every((n) => handoutsStage.names.has(n))) {
    return handoutsStage.sessionId;
  }
  if (handoutsStage.inflight) return handoutsStage.inflight;
  handoutsStage.inflight = stageImages(source).finally(() => { handoutsStage.inflight = null; });
  return handoutsStage.inflight;
}

// sendHeartbeat pings the session; returns false (and clears it) when the server
// reaped it, so the next ensureStaged re-stages.
async function sendHeartbeat() {
  if (!handoutsStage.sessionId) return false;
  try {
    const fd = new FormData();
    fd.append("session", handoutsStage.sessionId);
    const res = await fetch("/api/handouts/heartbeat", { method: "POST", credentials: "same-origin", body: fd });
    if (res.ok) return true;
    handoutsStage.sessionId = null;
    return false;
  } catch (_) { return false; }
}

function startHandoutHeartbeat() {
  stopHandoutHeartbeat();
  handoutsStage.heartbeat = setInterval(sendHeartbeat, 5000);
}
function stopHandoutHeartbeat() {
  if (handoutsStage.heartbeat) { clearInterval(handoutsStage.heartbeat); handoutsStage.heartbeat = null; }
}
async function unstageHandouts() {
  stopHandoutHeartbeat();
  const sid = handoutsStage.sessionId;
  handoutsStage.sessionId = null;
  handoutsStage.names = null;
  if (sid) { try { await fetch(`/api/handouts/stage?session=${encodeURIComponent(sid)}`, { method: "DELETE", credentials: "same-origin" }); } catch (_) {} }
}

// handoutsBody builds the generate request body: the source + (when there are
// images) the staged session id, so images aren't re-sent each generate.
async function handoutsBody(source) {
  const fd = new FormData();
  fd.append("source", source);
  fd.append("filename", handoutsCtx.title || handoutsCtx.list.title || "handouts");
  const sid = await ensureStaged(source);
  if (sid) fd.append("session", sid);
  return fd;
}

// Revive the staged session when the user returns to a backgrounded tab (its
// heartbeats may have lapsed and the server reaped it).
document.addEventListener("visibilitychange", async () => {
  if (document.visibilityState !== "visible" || handoutsOverlay.hidden || !handoutsCtx) return;
  if (!(await sendHeartbeat())) ensureStaged(document.getElementById("handoutsSource").value).catch(() => {});
});

// appendImages resolves each wanted image to its decrypted bytes and appends it
// to fd as an "img" part. The cards' attachment lists are fetched in parallel
// (the old per-card sequential scan dominated handout/export latency), and the
// matched image bodies are fetched in parallel too. Returns the set of resolved
// names so the caller can prompt about any still missing.
async function appendImages(fd, cards, wanted) {
  const found = new Set();
  if (!wanted.size) return found;
  const lists = await Promise.all(cards.map((c) => cardAttachments(c.id)));
  const targets = new Map(); // name → attachment (first match wins)
  for (const atts of lists) {
    for (const att of atts) {
      if (att.name && wanted.has(att.name) && !targets.has(att.name)) targets.set(att.name, att);
    }
  }
  await Promise.all([...targets].map(async ([name, att]) => {
    try {
      const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
      if (!res.ok) return;
      const plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
      fd.append("img", new Blob([plain], { type: att.mime }), name);
      found.add(name);
    } catch (_) {}
  }));
  return found;
}

// generateSplitFitZip runs chgksuite's split_fit on the current .hndt (pages each
// handout to fit, one fitted PDF per question + an all-questions PDF) and hands
// the user a zip of all the PDFs. Online-only (shells out server-side).
async function generateSplitFitZip() {
  if (!handoutsCtx) return;
  const msg = document.getElementById("handoutsMessage");
  if (!xySync.isOnline()) { msg.textContent = "Split-fit доступен только онлайн."; return; }
  const source = document.getElementById("handoutsSource").value;
  if (!source.trim()) { msg.textContent = "Пустой источник."; return; }
  const btn = document.getElementById("handoutsSplitFit");
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
    msg.textContent = "Split-fit не удался: " + err.message;
  } finally {
    btn.disabled = false;
  }
}

document.getElementById("handoutsGenerate").addEventListener("click", generateHandoutsPdf);
document.getElementById("handoutsSplitFit").addEventListener("click", generateSplitFitZip);
document.getElementById("handoutsClose").addEventListener("click", closeHandouts);
handoutsOverlay.addEventListener("pointerdown", (e) => { if (e.target === handoutsOverlay) closeHandouts(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !handoutsOverlay.hidden) closeHandouts(); });

// ---- list preview (docx-style HTML render, entirely client-side) ----
// Renders a whole list the way chgksuite's docx export would — questions with
// numbered labels and Ответ/Зачёт/Комментарий/etc. fields, plus meta, headings
// and handouts — but in the browser, so it's instant. Inline 4s markup
// (bold/italic/links/(img …)/(screen …)) is parsed via xyChgk; referenced image
// handouts are resolved from the cards' attachments (decrypted + object-URL'd).

// Field labels mirror chgksuite/resources/labels_ru.toml (question_labels).
const PV_LABELS = {
  answer: "Ответ", zachet: "Зачёт", nezachet: "Незачёт",
  comment: "Комментарий", source: "Источник", author: "Автор",
  handout: "Раздаточный материал", editor: "Редактор", date: "Дата",
};
const previewOverlay = document.getElementById("previewOverlay");

// imgName extracts the referenced filename from an (img …) run value: like
// chgksuite's parseimg, the filename is the last whitespace token (the rest are
// w=/h=/big/inline options).
function imgName(val) {
  const toks = String(val).trim().split(/\s+/).filter(Boolean);
  return toks.length ? toks[toks.length - 1] : "";
}

// imageRefs collects every (img …) filename referenced across the list's cards.
function imageRefs(cards) {
  const wanted = new Set();
  for (const c of cards) {
    for (const m of (c.desc || "").matchAll(/\(img\b([^)]*)\)/g)) {
      const name = imgName(m[1]);
      if (name) wanted.add(name);
    }
  }
  return wanted;
}

// ---- attachment caches (preview image resolution) ----
// A card's attachment list changes on upload/delete, so it's cached per card and
// invalidated by loadAttachments (which every mutation path already calls).
// Attachment *bytes* are immutable for a given id, so a decrypted object URL is
// memoized for the page's lifetime — reopening a preview then costs no network,
// no decrypt. The URL cache is LRU-capped; evicted URLs are revoked.
const attListCache = new Map(); // cardId → [{ ...att, name }]
const attUrlCache = new Map();  // attId  → decrypted object URL
const ATT_URL_CACHE_MAX = 64;

// cardAttachments lists one card's attachments with their filenames decrypted.
async function cardAttachments(cardId, refresh = false) {
  if (refresh) attListCache.delete(cardId);
  const hit = attListCache.get(cardId);
  if (hit) return hit;
  let atts;
  try { atts = await fetchJSON(`/api/cards/${cardId}/attachments`); } catch (_) { return []; }
  const out = await Promise.all(atts.map(async (att) => {
    let name = "";
    try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) {}
    return { ...att, name };
  }));
  attListCache.set(cardId, out);
  return out;
}

// attachmentUrl decrypts one attachment into an object URL, reading its
// ciphertext through the offline IndexedDB mirror (so a reload doesn't
// re-download) and memoizing the result.
async function attachmentUrl(att) {
  const hit = attUrlCache.get(att.id);
  if (hit) { attUrlCache.delete(att.id); attUrlCache.set(att.id, hit); return hit; } // LRU touch
  let cipher;
  const cached = await xySync.getAttachment(att.id).catch(() => null);
  if (cached) {
    cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
  } else {
    const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error("не удалось скачать вложение");
    cipher = new Uint8Array(await res.arrayBuffer());
    try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher }); } catch (_) {}
  }
  const plain = await xyCrypto.decBytes(dk, cipher);
  const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
  attUrlCache.set(att.id, url);
  for (const stale of [...attUrlCache.keys()].slice(0, attUrlCache.size - ATT_URL_CACHE_MAX)) {
    URL.revokeObjectURL(attUrlCache.get(stale));
    attUrlCache.delete(stale);
  }
  return url;
}

// resolveImages maps each wanted image name → a decrypted object URL, scanning
// the cards' attachments (online only — mirrors the docx export's image
// gathering). Attachment lists and image bodies are fetched in parallel, and
// `onImage` fires per image as it lands so callers can fill placeholders in
// progressively instead of waiting for the slowest one. Missing names simply
// stay a placeholder (see renderRich).
async function resolveImages(cards, wanted, onImage) {
  const map = new Map();
  if (!wanted.size || !xySync.isOnline()) return map;
  const lists = await Promise.all(cards.map((c) => cardAttachments(c.id)));
  const targets = new Map(); // name → attachment (first match wins, in card order)
  for (const atts of lists) {
    for (const att of atts) {
      if (att.name && wanted.has(att.name) && !targets.has(att.name)) targets.set(att.name, att);
    }
  }
  await Promise.all([...targets].map(async ([name, att]) => {
    try {
      const url = await attachmentUrl(att);
      map.set(name, url);
      if (onImage) onImage(name, url);
    } catch (_) {}
  }));
  return map;
}

// fillPreviewImages swaps the "[изображение: …]" placeholders inside an already
// rendered preview for the images that have since resolved.
function fillPreviewImages(root, imgMap) {
  for (const ph of root.querySelectorAll(".pv-img-missing[data-img]")) {
    const url = imgMap.get(ph.dataset.img);
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
function fieldOpts(field, screen) {
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
function renderRich(text, imgMap, opts = {}) {
  const screenSide = !!(opts.accents || opts.brackets);
  const nb = (t) => (opts.nbsp ? xyChgk.replaceNoBreak(t) : t);
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
    if (type === "screen") { frag.append(document.createTextNode(nb((screenSide ? val.for_screen : val.for_print) || ""))); continue; }
    if (type === "hyperlink") {
      frag.append(el("a", { class: "pv-link", href: val, target: "_blank", rel: "noopener noreferrer", text: val }));
      continue;
    }
    if (!type) { frag.append(document.createTextNode(nb(val))); continue; }
    const span = el("span", { text: nb(val) });
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
function renderFieldBody(text, imgMap, opts) {
  const frag = document.createDocumentFragment();
  const lst = xyChgk.splitList(text);
  if (lst.items) {
    if (lst.preamble.trim()) frag.append(renderRich(lst.preamble, imgMap, opts));
    const box = el("div", { class: "pv-list" });
    lst.items.forEach((it, i) => {
      const li = el("div", { class: "pv-list-item" }, el("span", { class: "pv-list-num", text: `${i + 1}. ` }));
      li.append(renderRich(it, imgMap, opts));
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
function pvSmallCls(field) {
  return field === "source" || field === "author" ? "pv-small" : "";
}

// pvField renders a "Label: value" line: peels a "!!Label" override, numbers any
// "- …" list, and (for sources that became a list) uses the plural label.
function pvField(field, defaultLabel, text, imgMap, screen, cls) {
  const ov = PV_OVERRIDABLE.has(field) ? xyChgk.applyOverride(text) : { label: null, text };
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
function pvEditBtn(card) {
  const list = previewListRef;
  return el("button", {
    class: "pv-edit", title: "Редактировать карточку", "aria-label": "Редактировать карточку",
    text: "✏️",
    onclick: (e) => {
      e.stopPropagation();
      const group = previewGroupMode;
      closePreview();
      openCard(card, { returnTo: list ? { listId: list.id, cardId: card.id, group } : null });
    },
  });
}

// renderPreviewCard renders one card the way the docx export would: a question
// card becomes a numbered question with its answer/zachet/etc.; meta/heading/
// section/editor/date cards become their corresponding paragraphs/headings.
// `edit` adds the ✏️ jump-to-editor button — only the list preview passes it; the
// card-detail preview (already inside the editor) leaves it off.
function renderPreviewCard(card, number, imgMap, screen, edit) {
  if (card.kind === "test") {
    return el("p", { class: "pv-meta pv-test", text: testTitle(card.desc), dataset: { cardId: card.id } });
  }
  const blocks = xyChgk.parseBlocks(card.desc);
  const find = (t) => blocks.find((b) => b.type === t);

  if (card.kind === "question" || find("question")) {
    const wrap = el("article", { class: "pv-q", dataset: { cardId: card.id } });
    const handout = find("handout");
    if (handout) wrap.append(pvField("handout", PV_LABELS.handout, handout.text, imgMap, screen, "pv-handout"));
    // Question line: small inline ✏️ (edit lists only) + bold "Вопрос N." label
    // (overridable) + question text (which may itself be a blitz/duplet list).
    const qov = xyChgk.applyOverride(xyChgk.questionText(card.desc));
    const qLabel = qov.label || "Вопрос";
    const qline = el("div", { class: "pv-q-text" });
    if (edit) qline.append(pvEditBtn(card));
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
  if (edit) (wrap.firstElementChild || wrap).prepend(pvEditBtn(card));
  return wrap;
}

// previewCtx holds the resolved cards/numbers/images for the open preview so the
// screen-mode toggle can re-render without refetching attachments.
let previewCtx = null;
let previewListRef = null; // the list currently shown in the preview overlay
let previewGroupMode = false; // true when the overlay shows the whole group

function renderPreviewBody(screen) {
  const body = document.getElementById("previewBody");
  body.replaceChildren();
  if (!previewCtx) return;
  const { cards, numbers, imgMap } = previewCtx;
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], imgMap, screen, true)));
}

function closePreview() {
  previewOverlay.hidden = true;
  previewCtx = null;
  previewListRef = null;
  previewGroupMode = false;
  document.getElementById("previewBody").replaceChildren();
}

// previewList opens the preview modal and renders the whole list. Test lists show
// their tester summary (the same line the copy action produces); question lists
// render docx-style — text instantly, image handouts resolved + filled in after.
// wholeGroup previews the list's entire group (non-test members, board order,
// continuous numbering) — the same scope its export/handouts cover.
async function previewList(list, wholeGroup = false) {
  const group = wholeGroup && list.groupId != null ? groupById(list.groupId) : null;
  const scopeLists = group ? listsInGroup(list.groupId).filter((l) => l.type !== "test") : [list];
  const cards = scopeLists.flatMap((l) => cardsOf(l.id));
  document.getElementById("previewTitle").textContent = group
    ? "🔗" + (group.name || "связанные списки")
    : (list.type === "test" ? "🧪 " : "") + (list.title || "Предпросмотр");
  const body = document.getElementById("previewBody");
  body.replaceChildren();
  previewCtx = null;
  previewListRef = list;
  previewGroupMode = !!group;
  // Screen-mode toggle + tester-copy button are mutually exclusive per list kind.
  const isTest = !group && list.type === "test";
  document.querySelector(".preview-screen-toggle").hidden = isTest;
  document.getElementById("previewCopyTesters").hidden = !isTest;
  previewOverlay.hidden = false;
  if (isTest) {
    const text = testerSummary(list);
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
  const imgMap = new Map();
  const ctx = { cards, numbers, imgMap };
  previewCtx = ctx;
  renderPreviewBody(document.getElementById("previewScreen").checked);
  await resolveImages(cards, imageRefs(cards), (name, url) => {
    imgMap.set(name, url);
    // Ignore a close (or another list's preview) that happened during the await.
    if (previewCtx === ctx && !previewOverlay.hidden) fillPreviewImages(body, imgMap);
  });
}

// Copy the previewed test list's tester summary; brief inline confirmation.
document.getElementById("previewCopyTesters").addEventListener("click", async (e) => {
  if (!previewListRef) return;
  const btn = e.currentTarget; // capture before await (currentTarget clears after dispatch)
  if (!btn.dataset.label) btn.dataset.label = btn.textContent;
  await copyTesterList(previewListRef);
  btn.textContent = "Скопировано ✓";
  setTimeout(() => { btn.textContent = btn.dataset.label; }, 1500);
});

document.getElementById("previewScreen").addEventListener("change", (e) => renderPreviewBody(e.target.checked));
document.getElementById("previewClose").addEventListener("click", closePreview);
previewOverlay.addEventListener("pointerdown", (e) => { if (e.target === previewOverlay) closePreview(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !previewOverlay.hidden) closePreview(); });

// addCard opens the card detail in "create mode" — only the description editor
// is shown (the card isn't persisted until you save a description, so we never
// create empty cards). Labels/attachments/move/timeline appear only when editing
// an existing card.
function addCard(list) {
  if (list.type === "test") return addTestCard(list);
  pendingList = list;
  openCardId = null;
  cardView = "";
  cardFieldReaders = null;
  cardDraft = "";
  cardDraftMeta = null;
  cardImageNames = [];
  document.getElementById("cardDesc").value = "";
  document.getElementById("cardKind").hidden = false;
  document.getElementById("cardKind").value = "question";
  document.getElementById("cardMessage").textContent = "";
  document.querySelector(".card-detail").classList.add("creating");
  document.getElementById("cardCopy").hidden = true; // no number/desc yet
  cardOverlay.hidden = false;
  // New card: no preview yet — open straight into the structured editor.
  lastEditView = "fields";
  setCardView("fields");
  document.getElementById("cardDesc").focus();
}

// addTestCard: a test card's "description" is JSON {datetime, title, testers}
// (see chgk.js parseTestCard). Creating it also auto-creates two board labels
// ("{dt} взяли" green / "не взяли" red) for the user to assign to questions
// later; the tester list is edited in the card detail.
async function addTestCard(list) {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, "0");
  const def = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`;
  const dt = prompt("Дата и время тест-сессии (ГГГГ-ММ-ДД ЧЧ:ММ):", def);
  if (!dt) return;
  // Optional human label to tell sessions apart at a glance (e.g. "Алиев и др.").
  // Folded into the card preview and the auto-created green/red label names.
  const title = (prompt("Название тест-сессии (необязательно, напр. «Алиев и др.»):", "") || "").trim();
  const tag = title ? `${dt} ${title}` : dt;
  const existing = cardsOf(list.id);
  const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
  try {
    const desc = JSON.stringify({ datetime: dt, title, testers: [] });
    const res = await create("createCard", `/api/lists/${list.id}/cards`, {
      description_enc: await xyCrypto.encField(dk, desc), rank, kind: "test",
    });
    state.cards.push({ id: res.id, listId: list.id, kind: "test", rank, desc });
    // auto labels, then assign both to the new card
    const autoIds = [];
    for (const [suffix, color, kind] of [["взяли", "#3aa657", "test_taken"], ["не взяли", "#dd3322", "test_missed"]]) {
      const lr = await create("createLabel", `/api/boards/${boardId}/labels`, {
        name_enc: await xyCrypto.encField(dk, `${tag} ${suffix}`),
        color_enc: await xyCrypto.encField(dk, color),
        kind,
      });
      state.labels.push({ id: lr.id, kind, name: `${tag} ${suffix}`, color });
      autoIds.push(lr.id);
    }
    await put("setCardLabels", `/api/cards/${res.id}/labels`, { label_ids: autoIds });
    state.cardLabels[res.id] = autoIds.slice();
    render();
  } catch (err) { setStatus("error"); }
}

// testTitle renders a test card's derived title from its JSON description.
function testTitle(desc) {
  try {
    const m = xyChgk.parseTestCard(desc);
    const head = m.title ? `${m.title} · ${m.datetime}` : m.datetime;
    const players = m.testers.filter((t) => t.type === "player").length;
    const teams = m.testers.filter((t) => t.type === "team").length;
    const parts = [];
    if (players) parts.push(`${players} игр.`);
    if (teams) parts.push(`${teams} ком.`);
    return `🗓️ ${head}${parts.length ? " · " + parts.join(", ") : ""}`;
  } catch (_) { return "тест-сессия"; }
}

// setTestDetailTitle shows the test session's "datetime · title" heading above
// the Поля/Текст switcher (test cards have no kind selector to fill that slot).
function setTestDetailTitle(card) {
  const node = document.getElementById("cardDetailTitle");
  const m = xyChgk.parseTestCard(card.desc);
  node.textContent = m.title ? `${m.datetime} · ${m.title}` : m.datetime;
  node.hidden = false;
}

// listTesters gathers the testers from every test card in a list (flattened).
function listTesters(list) {
  const all = [];
  for (const c of cardsOf(list.id)) {
    if (c.kind !== "test") continue;
    all.push(...xyChgk.parseTestCard(c.desc).testers);
  }
  return all;
}

// testerSummary is the shareable "Вопросы тестировали: …" line for a test list —
// players sorted by surname, teams alphabetically, both deduped (chgk.js
// testerCopyText), terminated with a period. "" when the list has no testers.
function testerSummary(list) {
  const t = xyChgk.testerCopyText(listTesters(list));
  return t ? t + "." : "";
}

// copyTesterList copies the test list's tester summary to the clipboard silently.
async function copyTesterList(list) {
  const text = testerSummary(list);
  if (!text) return;
  try { await copyText(text); } catch (_) {}
}

// ---- commit card move (rank recompute from DOM order) ----
async function commitCardMove(cardId, targetListId, body) {
  const card = state.cards.find((c) => c.id === cardId);
  if (!card) return;
  const order = [...body.querySelectorAll(".kcard")].map((n) => Number(n.dataset.cardId));
  const idx = order.indexOf(cardId);
  const prevId = order[idx - 1], nextId = order[idx + 1];
  const rankOf = (id) => { const c = state.cards.find((x) => x.id === id); return c ? c.rank : null; };
  let prev = prevId ? rankOf(prevId) : null;
  let next = nextId ? rankOf(nextId) : null;
  if (prev !== null && next !== null && prev >= next) next = null; // guard
  let rank;
  try { rank = keyBetween(prev, next); } catch (_) { rank = keyBetween(prev, null); }
  card.listId = targetListId;
  card.rank = rank;
  setStatus("saving");
  try {
    await patch("patchCard", `/api/cards/${cardId}`, { list_id: targetListId, rank });
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); load(); }
}

// ---- card detail ----
let openCardId = null;
let pendingList = null; // set while composing a brand-new (unsaved) card
// cardReturn remembers where the open card was launched from so its ↩️ back
// button lands there: null → plain close (board view); {listId, cardId} → reopen
// that list's preview scrolled to this question (set only when opened from a
// preview's ✏️ button).
let cardReturn = null;
const cardOverlay = document.getElementById("cardOverlay");

// openCardEvents mirrors the open card's timeline (set by loadTimeline) so
// markCardRead can compute the per-bucket max event id without a re-fetch.
let openCardEvents = [];
// contentReadTimer: 10s content-dwell timer; commentsObserver: IntersectionObserver
// that starts a short dwell once #timeline scrolls into view. Both are armed in
// openCard and torn down in closeCard (and re-armed on every openCard, so
// switching cards never leaks a timer/observer onto the wrong card).
let contentReadTimer = null;
let commentsObserver = null;

// ---- card detail views: Просмотр (preview) / Поля (fields) / Текст (raw 4s) ----
// The open card carries a working draft of its 4s description (and handout
// settings) that flows between the three views without persisting; Save commits
// the draft. cardView is the active view; lastEditView is the edit tab restored
// when the user clicks ✎ / double-clicks the preview.
let cardView = "";
let lastEditView = "fields";
let cardDraft = "";          // unsaved working 4s description
let cardDraftMeta = null;    // unsaved handout-generation settings (string|null)
let cardFieldReaders = null; // per-field read() closures for the Поля view
// Blocks the Поля editor doesn't render but must not eat: the pre-question
// markup (№/№№ and friends) and anything else unmodelled. Both are captured at
// render time and re-emitted verbatim on recompose; the Текст view edits them.
let cardFieldsPre = null;
let cardFieldsExtra = null;

const CARD_TABS = ["preview", "fields", "text"];
const tabBtn = (v) => document.getElementById("cardTab" + v[0].toUpperCase() + v.slice(1));

// The 4s skeleton a new question's Текст view opens on: question / answer /
// comment / source / author, the blocks a question is expected to carry. The
// "@" line pre-fills the user's default author (a /profile setting) — saving
// an otherwise untouched stub is allowed and creates a card with just that.
function questionStub() {
  return "? \n! \n/ \n^ \n@ " + (state.defaultAuthor || "");
}

// fitTextarea grows a textarea to fit its content so the user never scrolls
// inside it (CSS min-height still sets the floor). scrollHeight is 0 while the
// element is display:none, so callers fit on render / when a field is revealed.
function fitTextarea(ta) {
  ta.style.height = "auto";
  // box-sizing is border-box, so the height must include the borders that
  // scrollHeight (content + padding only) omits, else the last line is clipped.
  const border = ta.offsetHeight - ta.clientHeight;
  ta.style.height = ta.scrollHeight + border + "px";
}
// autoGrow makes a textarea self-sizing: no inner scrollbar or resize handle,
// and it regrows on every input.
function autoGrow(ta) {
  ta.style.overflowY = "hidden";
  ta.style.resize = "none";
  ta.addEventListener("input", () => fitTextarea(ta));
}
autoGrow(document.getElementById("cardDesc"));

function openCardCard() { return state.cards.find((c) => c.id === openCardId); }

function draftKind() {
  if (pendingList) return document.getElementById("cardKind").value || "question";
  const c = openCardCard();
  return c ? c.kind : "question";
}
function fieldsAvailable() { return draftKind() === "question"; }
function isTestCard() { return draftKind() === "test"; }

// boardAuthors / boardSources collect the author names and source lines already
// used across the board's question cards (deduped, sorted) — the autocomplete
// suggestions for the Автор and Источник fields. A pack's questions tend to
// share both (the same authors, the same handful of references), so offering
// what the board already says beats retyping it.
function boardFieldValues(pick) {
  const set = new Set();
  for (const c of state.cards) {
    if (c.kind !== "question") continue;
    for (const v of pick(xyChgk.splitFields(c.desc)) || []) {
      const s = (v || "").trim();
      if (s) set.add(s);
    }
  }
  return [...set].sort((a, b) => a.localeCompare(b, "ru"));
}
function boardAuthors() { return boardFieldValues((f) => f.authors); }
function boardSources() { return boardFieldValues((f) => f.sources); }

// suggestWrap wraps an input in a hand-drawn autocomplete dropdown (substring
// filter, tap or ↑/↓+Enter to pick). A <datalist> would be less code, but iOS
// Safari simply never shows its options, so the suggestions are drawn by hand.
// `values` is captured at build time — the board's authors/sources don't change
// while the editor is open. onPick (optional) runs after a suggestion is taken.
function suggestWrap(input, values, onPick) {
  const menu = el("div", { class: "suggest-menu", hidden: true });
  const wrap = el("div", { class: "suggest-wrap" }, input, menu);
  let items = [], active = -1;
  const close = () => { menu.hidden = true; menu.replaceChildren(); items = []; active = -1; };
  const pick = (v) => { input.value = v; close(); if (onPick) onPick(v); };
  const setActive = (i) => {
    active = i;
    [...menu.children].forEach((n, j) => n.classList.toggle("active", j === i));
  };
  const open = () => {
    const q = input.value.trim().toLowerCase();
    items = values.filter((v) => v.toLowerCase().includes(q) && v !== input.value.trim()).slice(0, 8);
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
function captureDraft() {
  if (isTestCard()) {
    // Test cards keep their canonical JSON ({datetime,title,testers}) in
    // cardDraft; both views edit only the testers list (datetime/title are set
    // at creation), so re-read them from cardDraft and fold the rows back in.
    const cur = xyChgk.parseTestCard(cardDraft);
    let testers = null;
    if (cardView === "text") testers = xyChgk.testersFromText(document.getElementById("cardDesc").value);
    else if (cardView === "fields" && testerReaders) testers = readTesterRows();
    if (testers) cardDraft = xyChgk.serializeTestCard({ datetime: cur.datetime, title: cur.title, testers });
    return;
  }
  if (cardView === "text") cardDraft = document.getElementById("cardDesc").value;
  else if (cardView === "fields" && cardFieldReaders) {
    const r = readCardFields();
    cardDraft = r.desc;
    cardDraftMeta = r.meta;
  }
}

function setCardView(view) {
  captureDraft();
  const test = isTestCard();
  // Test cards offer Поля (tester rows) + Текст (plaintext) but no Просмотр;
  // other non-question cards have no Поля, so they fall back to Текст.
  if (test && view === "preview") view = lastEditView === "text" ? "text" : "fields";
  else if (view === "fields" && !fieldsAvailable() && !test) view = "text";
  cardView = view;
  if (view !== "preview") lastEditView = view;
  document.getElementById("cardViewPreview").hidden = view !== "preview";
  document.getElementById("cardViewFields").hidden = view !== "fields";
  document.getElementById("cardViewText").hidden = view !== "text";
  for (const t of CARD_TABS) tabBtn(t).classList.toggle("active", t === view);
  tabBtn("fields").hidden = !fieldsAvailable() && !test;
  tabBtn("preview").hidden = !!pendingList || test;
  document.getElementById("cardViewTabs").hidden = false;
  document.getElementById("cardSave").hidden = view === "preview";
  // The tools edit text, so they follow the two edit views. Both rewriting tools
  // are question-only: a test card's draft is JSON (its Текст view is a tester
  // list), and →.4s additionally needs the raw 4s editor it types into.
  document.getElementById("cardEditTools").hidden = view === "preview";
  document.getElementById("cardTypo").hidden = test;
  document.getElementById("cardTo4s").hidden = view !== "text" || !fieldsAvailable();
  document.getElementById("cardDescLabel").textContent = test ? "Тестировали (- игрок, -T команда)" : "Описание";
  if (view === "text") {
    const ta = document.getElementById("cardDesc");
    ta.value = test ? xyChgk.testersToText(xyChgk.parseTestCard(cardDraft).testers) : cardDraft;
    // A brand-new question opens on an empty editor, which says nothing about what
    // the format wants. Seed the markers so the writer fills in blanks instead of
    // recalling 4s from memory; the caret lands after the "?". "Empty" includes
    // the author-only draft an untouched Поля view composes when a default
    // author is set — that's still a blank form.
    const bare = ta.value.trim();
    const authorOnly = state.defaultAuthor && bare === "@ " + state.defaultAuthor;
    if (!test && pendingList && (!bare || authorOnly)) {
      ta.value = questionStub();
      ta.focus();
      ta.setSelectionRange(2, 2);
    }
    fitTextarea(ta);
  } else if (view === "fields") { if (test) renderTesterFields(); else renderCardFields(); }
  else if (view === "preview") renderCardPreview();
}

// ensureOption adds a <select> option for `name` if it isn't already present (so
// an image referenced by the handout but not currently attached still shows).
function ensureOption(sel, name) {
  if (name && ![...sel.options].some((o) => o.value === name)) sel.append(el("option", { value: name, text: name }));
}

// buildField is the generic absent/present field control: a "+ label" pill when
// absent, a labelled input with a "×" (back to absent) when present.
function buildField(label, kind, initial, opts = {}) {
  const wrap = el("div", { class: "fld" + (opts.muted ? " fld-muted" : "") });
  const addBtn = el("button", { class: "fld-add", type: "button", text: "+ " + label, title: "Добавить поле" });
  const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
  const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label }), rmBtn);
  const input = kind === "area"
    ? el("textarea", { class: "card-desc fld-input", spellcheck: "false" })
    : el("input", { class: "input fld-input", type: "text" });
  const body = el("div", { class: "fld-body" }, input);
  if (kind === "area") autoGrow(input);
  let present = initial !== null && initial !== undefined;
  if (present) input.value = initial;
  const sync = () => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; if (present && kind === "area") fitTextarea(input); };
  addBtn.addEventListener("click", () => { present = true; sync(); input.focus(); });
  rmBtn.addEventListener("click", () => { present = false; sync(); });
  wrap.append(addBtn, head, body);
  sync();
  return { node: wrap, read: () => (present ? input.value : null) };
}

// buildHandoutField: the "Раздаточный материал" field with a текст/картинка
// toggle. Image mode picks among the card's attached images.
function buildHandoutField(initial) {
  const wrap = el("div", { class: "fld" });
  const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Раздаточный материал", title: "Добавить поле" });
  const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
  const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Раздаточный материал" }), rmBtn);
  const modeText = el("button", { class: "seg-btn", type: "button", text: "текст" });
  const modeImg = el("button", { class: "seg-btn", type: "button", text: "картинка" });
  const toggle = el("div", { class: "seg" }, modeText, modeImg);
  const ta = el("textarea", { class: "card-desc fld-input", spellcheck: "false" });
  autoGrow(ta);
  const sel = el("select", { class: "input fld-input" });
  for (const n of cardImageNames) sel.append(el("option", { value: n, text: n }));
  const body = el("div", { class: "fld-body" }, toggle, ta, sel);
  let mode = initial && initial.kind === "image" ? "image" : "text";
  if (initial) {
    if (initial.kind === "image") { ensureOption(sel, initial.name); sel.value = initial.name || ""; }
    else ta.value = initial.text || "";
  }
  if (!cardImageNames.length) ensureOption(sel, "");
  const syncMode = () => {
    modeText.classList.toggle("active", mode === "text");
    modeImg.classList.toggle("active", mode === "image");
    ta.hidden = mode !== "text";
    sel.hidden = mode !== "image";
    if (mode === "text" && present) fitTextarea(ta);
  };
  modeText.addEventListener("click", () => { mode = "text"; syncMode(); });
  modeImg.addEventListener("click", () => { mode = "image"; syncMode(); });
  let present = !!initial;
  const sync = () => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; if (present && mode === "text") fitTextarea(ta); };
  addBtn.addEventListener("click", () => { present = true; sync(); });
  rmBtn.addEventListener("click", () => { present = false; sync(); });
  wrap.append(addBtn, head, body);
  sync(); syncMode();
  return {
    node: wrap,
    read: () => (present ? (mode === "image" ? { kind: "image", name: sel.value } : { kind: "text", text: ta.value }) : null),
  };
}

// buildSourcesField: the multi-line "Источник" field (one input per source line,
// add/remove rows), each row autocompleting from the board's existing sources.
function buildSourcesField(initial, suggestions) {
  const wrap = el("div", { class: "fld" });
  const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Источник", title: "Добавить поле" });
  const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
  const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Источник" }), rmBtn);
  const rows = el("div", { class: "fld-rows" });
  const addRow = (val) => {
    const inp = el("input", { class: "input fld-row-input", type: "text", value: val || "" });
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
  (present ? (initial.length ? initial : [""]) : []).forEach((s) => addRow(s));
  const sync = () => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; };
  addBtn.addEventListener("click", () => { present = true; if (!rows.children.length) addRow(""); sync(); });
  rmBtn.addEventListener("click", () => { present = false; sync(); });
  wrap.append(addBtn, head, body);
  sync();
  return { node: wrap, read: () => (present ? [...rows.querySelectorAll(".fld-row-input")].map((i) => i.value) : null) };
}

// buildAuthorsField: a tag input (like labels) seeded with autocomplete from the
// board's existing authors; free text adds a new author.
function buildAuthorsField(initial, suggestions) {
  const wrap = el("div", { class: "fld" });
  const addBtn = el("button", { class: "fld-add", type: "button", text: "+ Автор", title: "Добавить поле" });
  const rmBtn = el("button", { class: "fld-rm", type: "button", text: "×", title: "Убрать поле" });
  const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Автор" }), rmBtn);
  const tags = el("div", { class: "fld-tags" });
  const tagSet = [];
  const inp = el("input", { class: "input fld-tag-input", type: "text", placeholder: "имя автора…" });
  const renderTags = () => {
    tags.replaceChildren(...tagSet.map((t, i) => {
      const rm = el("button", { class: "fld-tag-rm", type: "button", text: "×" });
      rm.addEventListener("click", () => { tagSet.splice(i, 1); renderTags(); });
      return el("span", { class: "fld-tag" }, document.createTextNode(t), rm);
    }));
  };
  const commit = () => { const v = inp.value.trim(); if (v) { tagSet.push(v); inp.value = ""; renderTags(); } };
  // suggestWrap first, so its keydown handler outranks the Enter-commit below.
  const inpWrap = suggestWrap(inp, suggestions, commit);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === ",") { e.preventDefault(); commit(); } });
  inp.addEventListener("blur", commit);
  const body = el("div", { class: "fld-body" }, tags, inpWrap);
  let present = initial !== null && initial !== undefined;
  if (present) initial.forEach((t) => tagSet.push(t));
  renderTags();
  const sync = () => { addBtn.hidden = present; head.hidden = !present; body.hidden = !present; };
  addBtn.addEventListener("click", () => { present = true; sync(); inp.focus(); });
  rmBtn.addEventListener("click", () => { present = false; sync(); });
  wrap.append(addBtn, head, body);
  sync();
  return { node: wrap, read: () => { commit(); return present ? tagSet.slice() : null; } };
}

// renderCardFields rebuilds the Поля editor from the current draft (and handout
// settings). The last field (handout-gen markup) binds to cardDraftMeta, not the 4s.
function renderCardFields() {
  const f = xyChgk.splitFields(cardDraft);
  // A brand-new card pre-fills the user's default author (a /profile setting).
  if (pendingList && !cardDraft.trim() && f.authors == null && state.defaultAuthor) f.authors = [state.defaultAuthor];
  cardFieldsPre = f.preMarkup;
  cardFieldsExtra = f.extra;
  const box = document.getElementById("cardFields");
  box.replaceChildren();
  const R = {};
  R.handout = buildHandoutField(f.handout);
  R.question = buildField("Текст вопроса", "area", f.question);
  R.answer = buildField("Ответ", "area", f.answer);
  R.zachet = buildField("Зачёт", "input", f.zachet);
  R.nezachet = buildField("Незачёт", "input", f.nezachet);
  R.comment = buildField("Комментарий", "area", f.comment);
  R.sources = buildSourcesField(f.sources, boardSources());
  R.authors = buildAuthorsField(f.authors, boardAuthors());
  R.hndt = buildField("Доп. разметка для генерации раздаток", "area", cardDraftMeta, { muted: true });
  for (const k of ["handout", "question", "answer", "zachet", "nezachet", "comment", "sources", "authors", "hndt"]) box.append(R[k].node);
  // Size pre-filled fields now they're in the live DOM (scrollHeight is 0 while
  // detached, so the fit during buildField is a no-op for visible content).
  for (const ta of box.querySelectorAll("textarea")) fitTextarea(ta);
  cardFieldReaders = R;
}

// readCardFields collapses the Поля editor back into a 4s description + handout
// settings, preserving the pre-question and unmodelled blocks captured at render time.
function readCardFields() {
  const R = cardFieldReaders;
  const rec = {
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
let testerReaders = null; // () => [{text,type}] for the current tester rows

function renderTesterFields() {
  const box = document.getElementById("cardFields");
  box.replaceChildren();
  const m = xyChgk.parseTestCard(cardDraft);
  const wrap = el("div", { class: "fld" });
  const head = el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: "Тестировали" }));
  const rows = el("div", { class: "fld-rows" });
  const addRow = (t) => {
    const seg = el("div", { class: "seg tester-seg" });
    const bP = el("button", { class: "seg-btn", type: "button", text: "игрок" });
    const bT = el("button", { class: "seg-btn", type: "button", text: "команда" });
    let type = t && t.type === "team" ? "team" : "player";
    const syncSeg = () => { bP.classList.toggle("active", type === "player"); bT.classList.toggle("active", type === "team"); };
    bP.addEventListener("click", () => { type = "player"; syncSeg(); });
    bT.addEventListener("click", () => { type = "team"; syncSeg(); });
    seg.append(bP, bT); syncSeg();
    const inp = el("input", { class: "input fld-row-input", type: "text", value: (t && t.text) || "", placeholder: "имя…" });
    const rrm = el("button", { class: "fld-row-rm", type: "button", text: "×", title: "Удалить строку" });
    const row = el("div", { class: "fld-row tester-row" }, seg, inp, rrm);
    rrm.addEventListener("click", () => row.remove());
    row._read = () => ({ text: inp.value, type });
    rows.append(row);
    return inp;
  };
  (m.testers.length ? m.testers : [{ text: "", type: "player" }]).forEach((t) => addRow(t));
  const rowAdd = el("button", { class: "input fld-add-row", type: "button", text: "+ тестер" });
  rowAdd.addEventListener("click", () => addRow({ text: "", type: "player" }).focus());
  wrap.append(head, rows, rowAdd);
  box.append(wrap);
  testerReaders = () => [...rows.querySelectorAll(".tester-row")].map((r) => r._read());
}

function readTesterRows() { return testerReaders ? testerReaders() : []; }

// renderCardPreview renders the open card's draft the docx way (single-card
// version of the list preview). Read-only; double-click jumps back to editing.
async function renderCardPreview() {
  const body = document.getElementById("cardPreviewBody");
  if (!cardDraft.trim()) { body.replaceChildren(el("p", { class: "pv-empty", text: "Пусто." })); return; }
  const c = openCardCard();
  const card = { id: c ? c.id : 0, kind: draftKind(), desc: cardDraft, listId: c ? c.listId : (pendingList ? pendingList.id : 0) };
  const number = card.kind === "question" ? questionNumberFor(card) : null;
  const reqId = openCardId;
  const screen = document.getElementById("cardPreviewScreen").checked;
  const imgMap = new Map();
  body.replaceChildren(renderPreviewCard(card, number, imgMap, screen));
  await resolveImages([card], imageRefs([card]), (name, url) => {
    imgMap.set(name, url);
    if (cardView === "preview" && openCardId === reqId) fillPreviewImages(body, imgMap);
  });
}

// Tab clicks + the preview screen toggle + double-click-to-edit.
for (const v of CARD_TABS) tabBtn(v).addEventListener("click", () => setCardView(v));
document.getElementById("cardPreviewScreen").addEventListener("change", () => { if (cardView === "preview") renderCardPreview(); });
document.getElementById("cardPreviewBody").addEventListener("dblclick", () => setCardView(lastEditView));

// ---- edit tools (the row under the tabs) ----
// ударение types into the field the user was editing, which by the time the click
// lands is no longer the focused one (a button takes focus on mousedown) — so
// remember the last field the caret was in. The Поля view rebuilds its inputs on
// every view switch, hence the isConnected check when using it.
let lastEditField = null;
for (const panel of ["cardViewFields", "cardViewText"]) {
  document.getElementById(panel).addEventListener("focusin", (e) => {
    if (e.target.matches("textarea, input[type=text]")) lastEditField = e.target;
  });
}

// editField is the field ударение writes into: the last one edited, or — when the
// card was just opened and nothing has been focused yet — the raw editor.
function editField() {
  if (lastEditField && lastEditField.isConnected && lastEditField.offsetParent) return lastEditField;
  return cardView === "text" ? document.getElementById("cardDesc") : null;
}

// insertAtCaret types text at the field's caret (replacing its selection). It goes
// through execCommand because that is the only way to edit a field without
// throwing away the browser's undo stack — a hand-spliced .value makes Ctrl-Z drop
// everything typed before it. It also fires `input`, which is what regrows an
// autoGrow textarea; the fallback has to do that itself.
function insertAtCaret(field, text) {
  field.focus();
  if (document.execCommand("insertText", false, text)) return;
  const s = field.selectionStart, e = field.selectionEnd;
  field.setRangeText(text, s, e, "end");
  field.dispatchEvent(new Event("input", { bubbles: true }));
}

// replaceField swaps a field's whole content through the same undo-preserving path.
function replaceField(field, text) {
  field.focus();
  field.setSelectionRange(0, field.value.length);
  if (!document.execCommand("insertText", false, text)) {
    field.value = text;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  }
}

// The combining acute (U+0301) attaches to the character left of the caret, which
// is the chgk convention for marking stress ("зАмок" → "зам́ок" as typed).
document.getElementById("cardInsStress").addEventListener("click", () => {
  const f = editField();
  if (f) insertAtCaret(f, "́");
});

// типограф runs the WHOLE card — not just the focused field — through chgksuite's
// typography pass (/api/typo: quotes → «ёлочки», hyphen runs → em dashes,
// non-breaking spaces and hyphens, percent-escapes decoded back into the words a
// pasted wiki link stands for). The draft is 4s either way, so Поля and Текст
// send the same text; only where the result lands differs. Online-only, like →.4s:
// the pass is the Go port on the server (it never keeps the text).
document.getElementById("cardTypo").addEventListener("click", async () => {
  captureDraft();
  if (!cardDraft.trim()) return;
  if (!xySync.isOnline()) { alert("Типографика доступна только онлайн."); return; }
  setStatus("saving");
  try {
    const res = await fetch("/api/typo", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: cardDraft }),
    });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const { text } = await res.json();
    setStatus("saved");
    cardDraft = text;
    // In Текст the user is looking at the raw 4s, so type it back into the editor
    // (undo intact); in Поля the fields are a view of the draft, so rebuild them.
    if (cardView === "text") replaceField(document.getElementById("cardDesc"), text);
    else renderCardFields();
  } catch (err) {
    setStatus("error");
    alert("Не удалось применить типографику: " + err.message);
  }
});

// →.4s runs the raw editor's content through the server's chgk text parser — the
// .docx import pipeline minus the .docx — so a question pasted as plain prose
// ("Вопрос 1: … Ответ: … Автор: …") becomes marked-up 4s. The parse is a guess, so
// it lands back in the editor for the user to check; nothing is saved until Save.
// Online-only: the parser is the Go port on the server (it never keeps the text).
document.getElementById("cardTo4s").addEventListener("click", async () => {
  const ta = document.getElementById("cardDesc");
  const text = ta.value.trim();
  if (!text) return;
  if (!xySync.isOnline()) { alert("Разбор текста доступен только онлайн."); return; }
  setStatus("saving");
  try {
    const res = await fetch("/api/import/text", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const { source } = await res.json();
    setStatus("saved");
    replaceField(ta, source);
  } catch (err) {
    setStatus("error");
    alert("Не удалось разобрать текст: " + err.message);
  }
});

// ---- direct links (shareable URLs for a card and a comment) ----
// A card link is /board/{id}?card={cardId}; a comment link adds &comment={eventId}
// (the timeline event id). Opening such a URL deep-links straight to the card and,
// for a comment link, scrolls to and flashes that comment.
function cardUrl(cardId) { return `${location.origin}${location.pathname}?card=${cardId}`; }
function commentUrl(cardId, eventId) { return `${cardUrl(cardId)}&comment=${eventId}`; }

// reflectCardInUrl keeps the address bar in sync with the open card (replaceState,
// so it doesn't pollute history) — a refresh or copied address reopens the card.
function reflectCardInUrl(cardId) {
  history.replaceState(null, "", cardId ? cardUrl(cardId) : location.pathname);
}

// maybeOpenDeepLink runs once after the first successful board load: if the URL
// names a card (and optionally a comment), open it.
let deepLinkDone = false;
function maybeOpenDeepLink() {
  if (deepLinkDone) return;
  deepLinkDone = true;
  const params = new URLSearchParams(location.search);
  const cardId = Number(params.get("card"));
  if (!cardId) return;
  const card = state.cards.find((c) => c.id === cardId);
  if (!card) return;
  const commentId = Number(params.get("comment")) || null;
  openCard(card).then(() => { if (commentId) highlightComment(commentId); }).catch(() => {});
}

// highlightComment scrolls a comment into view and flashes it. The timeline is
// rendered newest-first inside the card detail; the event node carries id
// "tlev-{eventId}".
function highlightComment(eventId) {
  const node = document.getElementById("tlev-" + eventId);
  if (!node) return;
  node.scrollIntoView({ block: "center" });
  node.classList.add("tl-highlight");
  setTimeout(() => node.classList.remove("tl-highlight"), 2500);
}

async function copyCardLink() {
  if (openCardId == null) return;
  try { await copyText(cardUrl(openCardId)); showCopyMsg("Ссылка на карточку скопирована", false); }
  catch (err) { showCopyMsg("Не удалось скопировать: " + err.message, true); }
}

async function copyCommentLink(eventId) {
  if (openCardId == null) return;
  try { await copyText(commentUrl(openCardId, eventId)); showCopyMsg("Ссылка на комментарий скопирована", false); }
  catch (err) { showCopyMsg("Не удалось скопировать: " + err.message, true); }
}

async function openCard(card, opts = {}) {
  stopReadTracking(); // tear down any timer/observer left over from a previous card
  pendingList = null;
  cardReturn = opts.returnTo || null;
  openCardId = card.id;
  reflectCardInUrl(card.id);
  cardView = "";
  cardFieldReaders = null;
  cardDraft = card.desc;
  cardDraftMeta = card.handoutMeta != null ? card.handoutMeta : null;
  document.querySelector(".card-detail").classList.remove("creating");
  document.getElementById("cardDesc").value = card.desc;
  document.getElementById("cardMessage").textContent = "";
  // Kind selector: editable for ordinary cards, hidden for test cards (their
  // "kind" is fixed and their description is JSON, not 4s markup).
  const isTest = card.kind === "test";
  const kindSel = document.getElementById("cardKind");
  kindSel.hidden = isTest;
  if (!isTest) kindSel.value = card.kind || "question";
  // Test cards show their session heading in place of the (hidden) kind selector.
  if (isTest) setTestDetailTitle(card);
  else document.getElementById("cardDetailTitle").hidden = true;
  // The "copy for testing" action only makes sense for question cards (it shares
  // the numbered, screen-mode question text); hide it otherwise.
  document.getElementById("cardCopy").hidden = card.kind !== "question";
  document.getElementById("cardCopyMsg").hidden = true;
  cardOverlay.hidden = false;
  renderLabelPicker(card);
  paintLabels();
  lastEditView = (isTest || fieldsAvailable()) ? "fields" : "text";
  // Render the chosen view straight away so reopening a card never flashes the
  // previously-open card's content. The preview resolves its own images, so it
  // doesn't wait on the per-card loads below — which run in parallel, not
  // sequentially, to cut the total round-trip.
  setCardView(isTest ? "fields" : "preview");
  await Promise.all([loadAttachments(card.id), loadTimeline(card.id), populateMoveBoards()]);
  armReadTracking(card);
}

// stopReadTracking clears the content-dwell timer and disconnects the
// comments IntersectionObserver — called before re-arming (openCard) and on
// closeCard, so neither ever fires against a card that's no longer open.
function stopReadTracking() {
  if (contentReadTimer) { clearTimeout(contentReadTimer); contentReadTimer = null; }
  if (commentsObserver) { commentsObserver.disconnect(); commentsObserver = null; }
}

// armReadTracking shows/clears the in-card unread dots and arms the read
// triggers. Both content edits (desc_edit) and comments are recorded as entries
// in the timeline (лента) — that's where a reader actually sees *what* changed —
// so viewing the timeline clears whichever buckets are unread. Content also
// clears after a 10s dwell on the card body itself (a secondary trigger, for the
// reader who studies the question text without scrolling down to the лента).
function armReadTracking(card) {
  const u = state.unread[card.id] || {};
  const contentDot = document.getElementById("contentUnreadDot");
  const commentsDot = document.getElementById("commentsUnreadDot");
  contentDot.hidden = !u.content;
  commentsDot.hidden = !u.comments;

  if (u.content) {
    contentReadTimer = setTimeout(() => {
      contentReadTimer = null;
      if (openCardId === card.id) markCardRead(card.id, { content: true });
    }, 10000);
  }

  if (u.content || u.comments) {
    const timeline = document.getElementById("timeline");
    let dwellTimer = null;
    commentsObserver = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting && entry.intersectionRatio > 0) {
          if (!dwellTimer) {
            dwellTimer = setTimeout(() => {
              if (openCardId === card.id) markCardRead(card.id, { content: !!u.content, comments: !!u.comments });
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

// ---- move / copy a card (same board → relocate/duplicate; other board →
// client-side re-encryption). Boards are chosen by (decrypted) name and
// the destination list + position are selectable. ----

// moveCtx caches the currently-selected destination board: its DK, lists (with
// titles) and cards-per-list (for computing the insertion rank).
let moveCtx = null;

// ensureDK returns a usable DK for a board, unlocking via passphrase if needed.
async function ensureDK(bid) {
  if (bid === boardId) return dk;
  let d = await xyCrypto.loadCachedDK(bid);
  if (d) return d;
  const pass = prompt("Пароль целевой доски:");
  if (pass == null) throw new Error("отменено");
  const keymeta = await fetchJSON(`/api/boards/${bid}/keymeta`);
  d = await xyCrypto.unlockBoard(pass, keymeta);
  await xyCrypto.cacheDK(bid, d);
  return d;
}

// populateMoveBoards fills the board <select> with decrypted board names (the
// current board first/default), then loads its lists.
async function populateMoveBoards() {
  const sel = document.getElementById("moveBoard");
  sel.replaceChildren();
  let boards = [];
  try { boards = await fetchJSON("/api/boards"); } catch (_) {}
  // Always offer the current board (so the move UI works — and never prompts for
  // another board's password — even when offline and the board list is unfetched).
  if (!boards.some((b) => b.id === boardId)) boards.unshift({ id: boardId, name_enc: null });
  for (const b of boards) {
    let label = "доска #" + b.id;
    if (b.id === boardId) label = (state.name || label) + " (эта доска)";
    else if (b.schema_version >= 2) label = b.name; // plaintext name, no key needed
    else {
      try { const cdk = await xyCrypto.loadCachedDK(b.id); if (cdk) label = await xyCrypto.decField(cdk, b.name_enc); }
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
async function loadMoveBoard(bid) {
  if (bid === boardId) {
    const lists = [...state.lists].sort(byRank).map((l) => ({ id: l.id, title: l.title, rank: l.rank }));
    const cardsByList = new Map();
    for (const l of lists) cardsByList.set(l.id, cardsOf(l.id).map((c) => ({ id: c.id, rank: c.rank })));
    return { boardId: bid, dk, lists, cardsByList, labels: state.labels };
  }
  const tdk = await ensureDK(bid);
  const snap = await fetchJSON(`/api/boards/${bid}`);
  const lists = await Promise.all((snap.lists || []).map(async (l) => ({
    id: l.id, rank: l.rank, title: await xyCrypto.decField(tdk, l.title_enc),
  })));
  lists.sort(byRank);
  const cardsByList = new Map();
  for (const l of lists) {
    cardsByList.set(l.id, (snap.cards || []).filter((c) => c.list_id === l.id).map((c) => ({ id: c.id, rank: c.rank })).sort(byRank));
  }
  const labels = await Promise.all((snap.labels || []).map(async (l) => ({
    id: l.id, kind: l.kind, name: await xyCrypto.decField(tdk, l.name_enc), color: await xyCrypto.decField(tdk, l.color_enc),
  })));
  return { boardId: bid, dk: tdk, lists, cardsByList, labels };
}

async function onMoveBoardChange() {
  const listSel = document.getElementById("moveList");
  const bid = Number(document.getElementById("moveBoard").value);
  listSel.replaceChildren(el("option", { value: "", text: "загрузка…" }));
  try { moveCtx = await loadMoveBoard(bid); }
  catch (err) {
    moveCtx = null;
    listSel.replaceChildren(el("option", { value: "", text: err.message }));
    document.getElementById("movePos").replaceChildren();
    return;
  }
  listSel.replaceChildren();
  for (const l of moveCtx.lists) listSel.append(el("option", { value: l.id, text: l.title || "(без названия)" }));
  if (!moveCtx.lists.length) listSel.append(el("option", { value: "", text: "нет списков" }));
  onMoveListChange();
}

// onMoveListChange fills the position <select> with "в конец" + one slot per
// existing card (the card being moved is excluded when staying on its board).
function onMoveListChange() {
  const posSel = document.getElementById("movePos");
  posSel.replaceChildren();
  if (!moveCtx) return;
  const listId = Number(document.getElementById("moveList").value);
  const cards = (moveCtx.cardsByList.get(listId) || []).filter((c) => !(moveCtx.boardId === boardId && c.id === openCardId));
  posSel.append(el("option", { value: "end", text: "в конец" }));
  for (let i = 1; i <= cards.length; i++) posSel.append(el("option", { value: String(i), text: `позиция ${i}` }));
  posSel.value = "end";
}

// rankForSlot computes a fractional rank for inserting into `cards` at a 1-based
// slot ("end" appends). excludeId drops the moving card from the neighbour set.
function rankForSlot(cards, posValue, excludeId) {
  const arr = cards.filter((c) => c.id !== excludeId).sort(byRank);
  let prev = null, next = null;
  if (posValue === "end" || posValue === "") {
    prev = arr.length ? arr[arr.length - 1] : null;
  } else {
    const k = parseInt(posValue, 10);
    prev = k >= 2 ? arr[k - 2] : null;
    next = k - 1 < arr.length ? arr[k - 1] : null;
  }
  try { return keyBetween(prev ? prev.rank : null, next ? next.rank : null); }
  catch (_) { return keyBetween(prev ? prev.rank : null, null); }
}

// cardCopyBody builds the create-card payload for a copy: it re-encrypts the
// description and — when set — the handout-generation settings (field #10,
// handout_meta_enc) under `key` (the destination board's data key). kind carries
// over verbatim. Keeping handout meta here (not in copyCardExtras) means it copies
// offline too, like the description.
async function cardCopyBody(src, rank, key) {
  const body = { description_enc: await xyCrypto.encField(key, src.desc), rank, kind: src.kind };
  if (src.handoutMeta) body.handout_meta_enc = await xyCrypto.encField(key, src.handoutMeta);
  return body;
}

// copyCardExtras carries a source card's comments and attachments onto a freshly
// created destination card (labels are reconciled separately by the callers). The
// source card is always on the current board, so its content is read under `dk`
// and re-encrypted under the destination key `targetDk`. Comments are imported
// preserving their original author + timestamp (the bulk /comments/import
// endpoint); attachments are downloaded, decrypted, re-encrypted and re-uploaded
// (preserving mime + lossless flag). Copy/move is an online-only operation, so
// this runs straight against the API (no sync outbox / temp ids).
async function copyCardExtras(srcCardId, targetDk, newCardId) {
  if (!xySync.isOnline() || !newCardId) return;
  // Comments, oldest→newest so the copy keeps the original order, re-encrypted
  // under the destination key but carrying the source author + created_at.
  let events = [];
  try { events = await fetchJSON(`/api/cards/${srcCardId}/timeline`); } catch (_) { events = []; }
  const comments = [];
  for (const ev of events) {
    if (ev.type !== "comment") continue;
    let text;
    try { text = await xyCrypto.decField(dk, ev.payload_enc); } catch (_) { continue; }
    comments.push({
      author_user_id: ev.author_user_id != null ? ev.author_user_id : null,
      created_at: ev.created_at,
      payload_enc: await xyCrypto.encField(targetDk, text),
    });
  }
  if (comments.length) {
    try { await jpost(`/api/cards/${newCardId}/comments/import`, { comments }); } catch (_) {}
  }
  // Attachments: re-encrypt the ciphertext bytes under the destination key.
  let atts = [];
  try { atts = await fetchJSON(`/api/cards/${srcCardId}/attachments`); } catch (_) { atts = []; }
  for (const att of atts) {
    let name = "файл";
    try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) {}
    let plain;
    try {
      const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
      if (!res.ok) continue;
      plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
    } catch (_) { continue; }
    let recipher;
    try { recipher = await xyCrypto.encBytes(targetDk, plain); } catch (_) { continue; }
    const fd = new FormData();
    fd.append("meta", JSON.stringify({
      filename_enc: await xyCrypto.encField(targetDk, name),
      mime: att.mime, lossless: !!att.lossless,
      event_payload_enc: await xyCrypto.encField(targetDk, JSON.stringify({ file: name })),
    }));
    fd.append("blob", new Blob([recipher], { type: "application/octet-stream" }), "blob");
    try { await fetch(`/api/cards/${newCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd }); } catch (_) {}
  }
}

async function doMoveCopy(remove) {
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card || !moveCtx) return;
  const targetBid = moveCtx.boardId;
  const targetListId = Number(document.getElementById("moveList").value);
  if (!targetListId) return;
  const msg = document.getElementById("cardMessage");
  const listCards = moveCtx.cardsByList.get(targetListId) || [];
  const sameBoard = targetBid === boardId;
  const rank = rankForSlot(listCards, document.getElementById("movePos").value, sameBoard && remove ? card.id : null);
  // The only offline-capable case is an intra-board move (just a re-parent/re-rank).
  // Copying — and anything touching another board — carries comments/attachments
  // and re-encrypts, so it's online-only.
  const intraBoardMove = sameBoard && remove;
  if (!intraBoardMove && !xySync.isOnline()) { msg.textContent = "Копирование и перенос между досками доступны только онлайн."; return; }
  msg.textContent = sameBoard ? "Сохранение…" : "Перешифровка…";
  try {
    if (sameBoard) {
      if (remove) {
        await patch("patchCard", `/api/cards/${card.id}`, { list_id: targetListId, rank });
        card.listId = targetListId;
        card.rank = rank;
      } else {
        const res = await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, dk));
        state.cards.push({ id: res.id, listId: targetListId, kind: card.kind, rank, desc: card.desc, handoutMeta: card.handoutMeta || null });
        const ids = state.cardLabels[card.id] || [];
        if (ids.length) { await jput(`/api/cards/${res.id}/labels`, { label_ids: ids }); state.cardLabels[res.id] = ids.slice(); }
        await copyCardExtras(card.id, dk, res.id);
      }
    } else {
      const tdk = moveCtx.dk;
      const res = await jpost(`/api/lists/${targetListId}/cards`, await cardCopyBody(card, rank, tdk));
      // reconcile labels by decrypted name+color
      const srcIds = state.cardLabels[card.id] || [];
      if (srcIds.length) {
        const tLabels = moveCtx.labels.slice();
        const targetIds = [];
        for (const sid of srcIds) {
          const sl = labelById(sid);
          if (!sl) continue;
          let match = tLabels.find((t) => t.name === sl.name && t.color === sl.color);
          if (!match) {
            const lr = await jpost(`/api/boards/${targetBid}/labels`, {
              name_enc: await xyCrypto.encField(tdk, sl.name), color_enc: await xyCrypto.encField(tdk, sl.color), kind: sl.kind,
            });
            match = { id: lr.id, name: sl.name, color: sl.color };
            tLabels.push(match);
          }
          targetIds.push(match.id);
        }
        if (targetIds.length) await jput(`/api/cards/${res.id}/labels`, { label_ids: targetIds });
      }
      await copyCardExtras(card.id, tdk, res.id);
      if (remove) {
        await jdelete(`/api/cards/${card.id}`);
        state.cards = state.cards.filter((c) => c.id !== card.id);
        cardOverlay.hidden = true;
      }
    }
    render();
    if (sameBoard && remove) { await populateMoveBoards(); } // refresh positions
    msg.textContent = remove ? "Перемещено." : "Скопировано.";
  } catch (err) { msg.textContent = err.message; }
}

document.getElementById("moveBoard").addEventListener("change", onMoveBoardChange);
document.getElementById("moveList").addEventListener("change", onMoveListChange);
document.getElementById("copyBtn").addEventListener("click", () => doMoveCopy(false));
document.getElementById("moveBtn").addEventListener("click", () => doMoveCopy(true));

// Change card kind after creation (edit mode only; create mode uses the same
// selector but the value is applied on first save). Test cards never reach here
// (their selector is hidden in openCard).
document.getElementById("cardKind").addEventListener("change", async (e) => {
  if (pendingList) { setCardView(fieldsAvailable() ? "fields" : "text"); return; } // create mode: re-eval tabs
  if (openCardId == null) return;
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  const kind = e.target.value;
  const msg = document.getElementById("cardMessage");
  try {
    await patch("patchCard", `/api/cards/${card.id}`, { kind });
    card.kind = kind;
    render();
    setCardView(cardView || "text"); // re-eval tab availability (Поля is question-only)
    msg.textContent = "Тип изменён.";
  } catch (err) { msg.textContent = err.message; }
});

// ---- copy a question to the clipboard for a test session ----
// questionNumberFor returns the display number this question card would show on
// the board (auto-assigned or directive-driven), matching the kanban preview.
function questionNumberFor(card) {
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

// copyText writes to the clipboard, falling back to a hidden textarea +
// execCommand on insecure contexts / older browsers without the async API.
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = el("textarea");
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
let copyMsgTimer = null;
function showCopyMsg(text, isErr) {
  const node = document.getElementById("cardCopyMsg");
  node.textContent = text;
  if (isErr) node.setAttribute("data-err", ""); else node.removeAttribute("data-err");
  node.hidden = false;
  clearTimeout(copyMsgTimer);
  copyMsgTimer = setTimeout(() => { node.hidden = true; }, 2500);
}

document.getElementById("cardCopy").addEventListener("click", async () => {
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  try {
    await copyText(xyChgk.shareText(card.desc, questionNumberFor(card)));
    showCopyMsg("Скопировано для теста", false);
  } catch (err) {
    showCopyMsg("Не удалось скопировать: " + err.message, true);
  }
});

function closeCard() {
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
async function cardBack() {
  const ret = cardReturn; // capture before closeCard clears it
  closeCard();
  if (!ret || ret.listId == null) return;
  const list = state.lists.find((l) => l.id === ret.listId);
  if (!list) return;
  await previewList(list, ret.group);
  if (previewOverlay.hidden) return; // guard against a close during the await
  const node = document.getElementById("previewBody").querySelector(`[data-card-id="${ret.cardId}"]`);
  if (node) node.scrollIntoView({ block: "center" });
}
document.getElementById("cardClose").addEventListener("click", cardBack);
document.getElementById("cardLink").addEventListener("click", copyCardLink);
cardOverlay.addEventListener("pointerdown", (e) => { if (e.target === cardOverlay) closeCard(); });
// Escape behaves like the ↩️ back button when the card is open — but only when
// no in-card widget owns Escape first (paste modal, label popup), so it dismisses
// those without also closing the card.
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || cardOverlay.hidden) return;
  if (!pasteOverlay.hidden || document.querySelector(".label-add-popup")) return;
  cardBack();
});

document.getElementById("cardSave").addEventListener("click", async () => {
  captureDraft(); // fold the active view's edits into cardDraft / cardDraftMeta
  const msg = document.getElementById("cardMessage");
  // create mode: persist a new card with the composed description, then switch to
  // the full edit view.
  if (pendingList) {
    const text = cardDraft;
    const list = pendingList;
    const kind = document.getElementById("cardKind").value || "question";
    const existing = cardsOf(list.id);
    const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
    const meta = cardDraftMeta && cardDraftMeta.trim() ? cardDraftMeta : null;
    try {
      const reqBody = { description_enc: await xyCrypto.encField(dk, text), rank, kind };
      if (meta) reqBody.handout_meta_enc = await xyCrypto.encField(dk, meta);
      const res = await create("createCard", `/api/lists/${list.id}/cards`, reqBody);
      const card = { id: res.id, listId: list.id, kind, rank, desc: text, handoutMeta: meta };
      state.cards.push(card);
      render();
      await openCard(card);
    } catch (err) { msg.textContent = err.message; }
    return;
  }
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  const newDesc = cardDraft;
  const newMeta = cardDraftMeta && cardDraftMeta.trim() ? cardDraftMeta : null;
  msg.textContent = "";
  try {
    const body = { description_enc: await xyCrypto.encField(dk, newDesc) };
    if (newDesc !== card.desc) {
      body.desc_event_enc = await xyCrypto.encField(dk, JSON.stringify({ before: card.desc, after: newDesc }));
    }
    // Persist handout-gen settings (field #10) when they changed: "" clears them.
    if (newMeta !== (card.handoutMeta || null)) {
      body.handout_meta_enc = newMeta ? await xyCrypto.encField(dk, newMeta) : "";
    }
    await patch("patchCard", `/api/cards/${card.id}`, body);
    card.desc = newDesc;
    card.handoutMeta = newMeta;
    render();
    await loadTimeline(card.id);
    // Reflect the saved/normalized desc back into the editor views (test cards
    // re-render their own tester editor, not the question fields).
    if (isTestCard()) {
      setTestDetailTitle(card);
      if (cardView === "fields") renderTesterFields();
      else if (cardView === "text") { const ta = document.getElementById("cardDesc"); ta.value = xyChgk.testersToText(xyChgk.parseTestCard(newDesc).testers); fitTextarea(ta); }
    } else {
      document.getElementById("cardDesc").value = newDesc;
      if (cardView === "text") fitTextarea(document.getElementById("cardDesc"));
      if (cardView === "fields") renderCardFields();
      else if (cardView === "preview") renderCardPreview();
    }
    msg.textContent = "Сохранено.";
  } catch (err) { msg.textContent = err.message; }
});

// Cmd/Ctrl-Enter saves from either edit view (textarea or structured fields).
function saveOnCmdEnter(e) {
  if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
    e.preventDefault();
    document.getElementById("cardSave").click();
  }
}
document.getElementById("cardDesc").addEventListener("keydown", saveOnCmdEnter);
document.getElementById("cardFields").addEventListener("keydown", saveOnCmdEnter);

document.getElementById("cardDelete").addEventListener("click", async () => {
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card || !confirm("Удалить карточку?")) return;
  try {
    await del("deleteCard", `/api/cards/${card.id}`);
    state.cards = state.cards.filter((c) => c.id !== card.id);
    cardOverlay.hidden = true;
    render();
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
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
function labelLastUsage() {
  const usage = new Map();
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
function sortLabels(labels) {
  const usage = labelLastUsage();
  return labels.slice().sort((a, b) => {
    const ua = usage.get(a.id), ub = usage.get(b.id);
    const ha = ua !== undefined, hb = ub !== undefined;
    if (ha && hb) return ub - ua;
    if (ha !== hb) return ha ? -1 : 1;
    return b.name.localeCompare(a.name, "ru");
  });
}

function renderLabelPicker(card) {
  const picker = document.getElementById("labelPicker");
  picker.replaceChildren();
  const assigned = state.cardLabels[card.id] || [];
  for (const id of assigned) {
    const lbl = labelById(id);
    if (!lbl) continue;
    picker.append(el("button", {
      class: "label-pick is-on", type: "button", dataset: { c: lbl.color },
      title: "Снять метку", text: lbl.name + " ×",
      onclick: () => toggleLabel(card, lbl),
    }));
  }
  if (!assigned.length) picker.append(el("span", { class: "label-empty", text: "меток нет" }));
  closeLabelAddPopup();
  paintLabels();
}

function closeLabelAddPopup() {
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
const newLabelForm = document.getElementById("newLabelForm");
newLabelForm.remove();

// openLabelAddPopup mounts a custom dropdown under the "➕ Добавить метку" button:
// a filter field above a scrollable list of the unassigned labels, sorted by last
// usage (sortLabels), with the create-new-label form at the foot. A native
// <select> can't host a search box, hence the hand-rolled popup (shares the
// .menu-dropdown styling of the list "⋯" menu).
function openLabelAddPopup() {
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  const anchor = document.getElementById("labelAddRow");
  if (anchor.querySelector(".label-add-popup")) { closeLabelAddPopup(); return; } // toggle off

  const assignedSet = new Set(state.cardLabels[card.id] || []);
  const pool = sortLabels(state.labels.filter((l) => !assignedSet.has(l.id)));

  const filter = el("input", {
    class: "input label-add-filter", type: "text",
    placeholder: "Фильтр меток…", autocomplete: "off",
  });
  const listBox = el("div", { class: "label-add-list" });
  const popup = el("div", { class: "menu-dropdown label-add-popup", role: "menu" }, filter, listBox, newLabelForm);

  function fill() {
    const q = filter.value.trim().toLowerCase();
    const items = q ? pool.filter((l) => l.name.toLowerCase().includes(q)) : pool;
    listBox.replaceChildren();
    if (!items.length) { listBox.append(el("span", { class: "label-empty", text: "ничего не найдено" })); return; }
    for (const lbl of items) {
      listBox.append(el("button", {
        class: "menu-item label-add-item", type: "button", role: "menuitem",
        onclick: () => { close(); toggleLabel(card, lbl); },
      },
        el("span", { class: "label-swatch", dataset: { c: lbl.color } }),
        el("span", { class: "label-add-name", text: lbl.name }),
      ));
    }
    paintLabels();
  }
  function close() {
    popup.remove();
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey);
  }
  function onOutside(e) { if (!anchor.contains(e.target)) close(); }
  function onKey(e) { if (e.key === "Escape") { close(); document.getElementById("labelAddBtn").focus(); } }

  filter.addEventListener("input", fill);
  anchor.append(popup);
  document.addEventListener("pointerdown", onOutside, true);
  document.addEventListener("keydown", onKey);
  fill();
  filter.focus();
}

document.getElementById("labelAddBtn").addEventListener("click", openLabelAddPopup);

async function toggleLabel(card, lbl) {
  const cur = new Set(state.cardLabels[card.id] || []);
  const adding = !cur.has(lbl.id);
  if (adding) cur.add(lbl.id); else cur.delete(lbl.id);
  const ids = [...cur];
  try {
    const events = [{
      type: adding ? "label_add" : "label_remove",
      payload_enc: await xyCrypto.encField(dk, JSON.stringify({ label: lbl.name })),
    }];
    await put("setCardLabels", `/api/cards/${card.id}/labels`, { label_ids: ids, events });
    state.cardLabels[card.id] = ids;
    renderLabelPicker(card);
    render();
    await loadTimeline(card.id);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

// NB: `newLabelForm` (the retained node), not getElementById — the form is
// detached from the document above and lives inside the popup while it is open.
newLabelForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = document.getElementById("newLabelName").value.trim();
  const color = document.getElementById("newLabelColor").value;
  if (!name) return;
  try {
    const res = await create("createLabel", `/api/boards/${boardId}/labels`, {
      name_enc: await xyCrypto.encField(dk, name),
      color_enc: await xyCrypto.encField(dk, color),
    });
    const lbl = { id: res.id, kind: "normal", name, color };
    state.labels.push(lbl);
    document.getElementById("newLabelName").value = "";
    const card = state.cards.find((c) => c.id === openCardId);
    // The form is now reachable only from inside the add-label popup, so naming a
    // label there means you want it ON this card — assign it instead of merely
    // creating it and making the user reopen the popup to pick what they just
    // typed. toggleLabel does the API call, re-renders and closes the popup.
    if (card) await toggleLabel(card, lbl);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
});

// ---- timeline ----
async function loadTimeline(cardId) {
  const tl = document.getElementById("timeline");
  tl.replaceChildren();
  // Refresh the cached server timeline when online, then merge any pending
  // (un-synced) events synthesized from the outbox so offline edits/comments show.
  if (xySync.isOnline()) {
    try { const ev = await fetchJSON(`/api/cards/${cardId}/timeline`); await xySync.cacheTimeline(cardId, ev); } catch (_) {}
  }
  let events = [];
  try { events = await xySync.timelineFor(cardId); } catch (_) {}
  if (cardId === openCardId) openCardEvents = events;
  // Newest first: events are oldest→newest (by id); show them reversed.
  for (const ev of [...events].reverse()) {
    let payload = "";
    try { payload = await xyCrypto.decField(dk, ev.payload_enc); } catch (_) {}
    tl.append(renderEvent(ev, payload));
  }
}

// eventAuthor resolves a timeline event's author to a display name. Pending
// (offline, un-synced) events carry no author_user_id yet — they're authored by
// the current user, so fall back to "me".
function eventAuthor(ev) {
  let uid = ev.author_user_id;
  if (uid == null && state.me) uid = state.me.user_id;
  if (uid == null) return "";
  if (state.memberNames[uid]) return state.memberNames[uid];
  if (state.me && state.me.user_id === uid && state.me.username) return state.me.username;
  return `#${uid}`;
}

function renderEvent(ev, payload) {
  const when = new Date(ev.created_at).toLocaleString("ru-RU");
  const author = eventAuthor(ev);
  const meta = (rest) => (author ? `${author} · ${rest}` : rest);
  const wrap = el("div", { class: "tl-event tl-" + ev.type });
  if (ev.type === "comment") {
    const metaRow = el("div", { class: "tl-meta" }, meta(when));
    // Synced comments have a stable event id → offer a copyable direct link and
    // make the node an anchor target. Pending (offline) comments have no id yet.
    if (ev.id) {
      wrap.id = "tlev-" + ev.id;
      metaRow.append(el("button", {
        class: "tl-link", type: "button", title: "Копировать ссылку на комментарий",
        text: "🔗", onclick: () => copyCommentLink(ev.id),
      }));
    }
    wrap.append(metaRow, el("div", { class: "tl-comment", text: payload }));
  } else if (ev.type === "desc_edit") {
    let diff = {};
    try { diff = JSON.parse(payload); } catch (_) {}
    // Two-pane before/after, with the word-level changes highlighted within each
    // pane: removed tokens struck through in the "before" pane, added tokens
    // highlighted in the "after" pane; unchanged text plain in both.
    const before = el("div", { class: "tl-before" });
    const after = el("div", { class: "tl-after" });
    for (const op of xyDiff.diffTokens(diff.before || "", diff.after || "")) {
      if (op.type === "eq") {
        before.append(document.createTextNode(op.text));
        after.append(document.createTextNode(op.text));
      } else if (op.type === "del") {
        before.append(el("del", { class: "tl-chg", text: op.text }));
      } else {
        after.append(el("ins", { class: "tl-chg", text: op.text }));
      }
    }
    wrap.append(el("div", { class: "tl-meta", text: meta("правка описания · " + when) }),
      el("div", { class: "tl-diff" }, before, after));
  } else {
    let info = {};
    try { info = JSON.parse(payload); } catch (_) {}
    const verbs = {
      label_add: "добавлена метка", label_remove: "снята метка",
      attach_add: "вложение добавлено", attach_remove: "вложение удалено", attach_replace: "вложение заменено",
    };
    const verb = verbs[ev.type] || ev.type;
    const detail = info.label || info.file || "";
    wrap.append(el("div", { class: "tl-meta", text: meta(`${verb}${detail ? ": " + detail : ""} · ${when}`) }));
  }
  return wrap;
}

document.getElementById("commentForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = document.getElementById("commentInput");
  const text = input.value.trim();
  if (!text || !openCardId) return;
  try {
    await post("comment", `/api/cards/${openCardId}/comments`, { payload_enc: await xyCrypto.encField(dk, text) });
    input.value = "";
    await loadTimeline(openCardId);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
});

// ---- attachments ----
// cardImageNames holds the decrypted filenames of the open card's image
// attachments — the choices offered by the handout image picker (Поля view).
let cardImageNames = [];

async function loadAttachments(cardId) {
  const box = document.getElementById("attachments");
  box.replaceChildren();
  cardImageNames = [];
  // Always refetch: this runs on card open and after every upload/delete, so it
  // doubles as the invalidation point for the preview's attachment-list cache.
  const list = await cardAttachments(cardId, true);
  for (const att of list) {
    const name = att.name || "файл";
    const isImage = (att.mime || "").startsWith("image/");
    if (isImage) cardImageNames.push(name);
    // Images open in a new tab (save via right-click there); other files download.
    const row = el("div", { class: "attach-row" },
      el("button", { class: "attach-name", type: "button", text: `📎 ${name}`, onclick: () => (isImage ? viewAttachment(att) : download(att, name)) }),
      el("span", { class: "attach-size", text: humanSize(att.size) }),
      el("button", { class: "attach-del", type: "button", title: "Удалить", text: "×", onclick: () => removeAttachment(att, name) }),
    );
    box.append(row);
  }
}

function humanSize(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

// recompressToWebp re-encodes an image File to WebP q70. Opt-in (see
// uploadAttachment): the default is to store what the user uploaded.
async function recompressToWebp(file) {
  if (!file.type.startsWith("image/")) return { bytes: new Uint8Array(await file.arrayBuffer()), mime: file.type || "application/octet-stream" };
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d").drawImage(bitmap, 0, 0);
  const blob = await new Promise((res) => canvas.toBlob(res, "image/webp", 0.7));
  if (!blob) return { bytes: new Uint8Array(await file.arrayBuffer()), mime: file.type };
  return { bytes: new Uint8Array(await blob.arrayBuffer()), mime: "image/webp" };
}

// uploadAttachment encrypts `file` under the saved name and POSTs it to the open
// card. Attachments are stored AS UPLOADED by default; re-encoding to WebP q70
// (lossless=false) is opt-in, because the exports no longer need it: docx and PDF
// both re-encode each picture for the size it is drawn at (imgconv.ForExport), so
// throwing away the original on the way in bought nothing but a worse original.
// Online-only — callers must gate on xySync.isOnline(). Refreshes list+timeline.
async function uploadAttachment(file, lossless, name) {
  if (!file || !openCardId) return;
  const msg = document.getElementById("cardMessage");
  msg.textContent = "Шифрование…";
  let bytes, mime;
  if (lossless) { bytes = new Uint8Array(await file.arrayBuffer()); mime = file.type || "application/octet-stream"; }
  else ({ bytes, mime } = await recompressToWebp(file));
  const cipher = await xyCrypto.encBytes(dk, bytes);
  const fd = new FormData();
  fd.append("meta", JSON.stringify({
    filename_enc: await xyCrypto.encField(dk, name),
    mime, lossless,
    event_payload_enc: await xyCrypto.encField(dk, JSON.stringify({ file: name })),
  }));
  fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
  const res = await fetch(`/api/cards/${openCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
  if (!res.ok) throw new Error((await res.text()) || "ошибка загрузки");
  msg.textContent = "";
  await loadAttachments(openCardId);
  await loadTimeline(openCardId);
}

document.getElementById("attachUpload").addEventListener("click", async () => {
  const input = document.getElementById("attachFile");
  const file = input.files[0];
  if (!file || !openCardId) return;
  if (!xySync.isOnline()) { document.getElementById("cardMessage").textContent = "Загрузка вложений доступна только онлайн."; return; }
  const compress = document.getElementById("attachCompress").checked;
  try {
    await uploadAttachment(file, !compress, file.name);
    input.value = "";
    document.getElementById("attachCompress").checked = false;
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
});

// ---- paste-to-attach ----
// Pasting an image while a saved card is open captures it, then asks for a
// filename + whether to WebP-compress (off by default, like the file picker)
// before encrypting and uploading it as an attachment.
let pastedFile = null;
const pasteOverlay = document.getElementById("pasteOverlay");

function extFromMime(m) {
  const map = { "image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/bmp": "bmp", "image/svg+xml": "svg" };
  if (map[m]) return map[m];
  const sub = (m || "").split("/")[1];
  return sub ? sub.replace(/[^a-z0-9]+/gi, "") : "png";
}

// withExt drops any extension the user typed and forces the one that matches the
// stored format (webp when compressing, else the source image's type), so the
// filename never claims a type the bytes aren't.
function withExt(name, ext) {
  const base = name.replace(/\.[^./\\]+$/, "").trim();
  return `${base || "вставка"}.${ext}`;
}

function closePasteModal() { pasteOverlay.hidden = true; pastedFile = null; }

document.addEventListener("paste", (e) => {
  // Only intercept image pastes while a persisted card is open (attachments need
  // a real card id); leave plain-text paste into the editor/comment box alone.
  if (openCardId == null || cardOverlay.hidden) return;
  const items = e.clipboardData && e.clipboardData.items;
  if (!items) return;
  let file = null;
  for (const it of items) {
    if (it.kind === "file" && it.type.startsWith("image/")) { file = it.getAsFile(); break; }
  }
  if (!file) return;
  e.preventDefault();
  pastedFile = file;
  const nameInput = document.getElementById("pasteName");
  // Clipboard images usually arrive as the generic "image.png"; offer a friendlier
  // default the user can overwrite.
  nameInput.value = (file.name && file.name !== "image.png") ? file.name : `вставка.${extFromMime(file.type)}`;
  document.getElementById("pasteCompress").checked = false;
  pasteOverlay.hidden = false;
  nameInput.focus();
  nameInput.select();
});

document.getElementById("pasteForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!pastedFile) return;
  const msg = document.getElementById("cardMessage");
  const file = pastedFile;
  const compress = document.getElementById("pasteCompress").checked;
  const name = withExt(document.getElementById("pasteName").value, compress ? "webp" : extFromMime(file.type));
  closePasteModal();
  if (!xySync.isOnline()) { msg.textContent = "Загрузка вложений доступна только онлайн."; return; }
  try {
    await uploadAttachment(file, !compress, name);
  } catch (err) { msg.textContent = err.message; }
});

document.getElementById("pasteCancel").addEventListener("click", closePasteModal);
pasteOverlay.addEventListener("pointerdown", (e) => { if (e.target === pasteOverlay) closePasteModal(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !pasteOverlay.hidden) closePasteModal(); });

// viewAttachment shows an image attachment in a new tab. attachmentUrl already
// handles the offline mirror + memoizes the object URL, which stays alive for
// the page's lifetime — so the tab can be reloaded / the image saved from there.
async function viewAttachment(att) {
  try {
    const url = await attachmentUrl(att);
    window.open(url, "_blank", "noopener");
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

async function download(att, name) {
  try {
    // Prefer the network; fall back to a previously-cached copy when offline.
    let cipher;
    try {
      const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
      if (!res.ok) throw new Error("не удалось скачать");
      cipher = new Uint8Array(await res.arrayBuffer());
      try { await xySync.putAttachment(att.id, { mime: att.mime, bytes: cipher }); } catch (_) {}
    } catch (netErr) {
      const cached = await xySync.getAttachment(att.id);
      if (!cached) throw new Error("вложение недоступно офлайн");
      cipher = cached.bytes instanceof Uint8Array ? cached.bytes : new Uint8Array(cached.bytes);
    }
    const plain = await xyCrypto.decBytes(dk, cipher);
    const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
    const a = el("a", { href: url, download: name });
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

async function removeAttachment(att, name) {
  if (!confirm(`Удалить вложение «${name}»?`)) return;
  if (!xySync.isOnline()) { document.getElementById("cardMessage").textContent = "Удаление вложений доступно только онлайн."; return; }
  try {
    const ev = await xyCrypto.encField(dk, JSON.stringify({ file: name }));
    await jdelete(`/api/attachments/${att.id}?event_payload_enc=${encodeURIComponent(ev)}`);
    await loadAttachments(openCardId);
    await loadTimeline(openCardId);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

boot();
