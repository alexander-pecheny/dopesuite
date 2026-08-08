// board.ts — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { overlayStack } from "./overlaystack.js";
import { xyApp, xySizes } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { type Tester, xyChgk } from "./chgk.js";
import { xyTypo } from "./typo.js";
import { xySync } from "./sync.js";
import { xyHandoutSession } from "./handoutsession.js";
import { createBoardMembers } from "./boardmembers.js";
import { create as createAttachments } from "./attachments.js";
import { gatherTargets } from "./attachments.js";
import { createUnlock } from "./unlock.js";
import { byRank, dragAfterIn, dragAfterInX, rankAfterMove, rankForSlot } from "./dragrank.js";
import { createTimeline, eventAuthor } from "./timeline.js";
import { createCardDetail, nowStamp } from "./carddetail.js";
import {
  type AnnounceCity, parseSession, partialSeen, type SeenQuestion, type SessionMeta,
  sessionLabel, type TitleMode, whoSaw,
} from "./sessions.js";
import * as people from "./people.js";
import { createSessionsPanel } from "./sessionspanel.js";
import { type ColorField, colorField, labelFill, labelInk, LABEL_COLORS } from "./colorpick.js";
import { anchorPopup } from "./popup.js";
import { type MassAction, plural, xyMass } from "./massaction.js";
import { namedUrl, revokeNamedUrl } from "./namedurl.js";
import { xySearchIndex } from "./searchindex.js";
import { xyFind } from "./find.js";
import type { Span as FindSpan } from "./find.js";
import type { DataKey } from "./crypto.js";
import type { SyncStatus } from "./sync.js";
import type { OpBody } from "./store.js";
import type { ScreenValue } from "./chgk.js";
import type { BoardCard, BoardLabel, BoardList, BoardState, CardLabel } from "./unlock.js";
import type { MembersState } from "./boardmembers.js";
import type { MenuItem, Timeline } from "./timeline.js";
import type { MoveCtx, PreviewCardLike } from "./carddetail.js";
import { icon, iconed } from "./icons_gen.js";

const { fetchJSON, jpost, jpatch, jput, jdelete, el, deriveTitle, onCmdEnter } = xyApp;
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
    sessionMetaCache = new Map();
    document.title = state.name + " · xy";
    // Feed the person directory. The tester names are plaintext in hand at this
    // moment, so this costs a pass over a handful of sessions and no decryption.
    people.remember(boardId, state.name, state.sessions.flatMap((s) => parseSession(s.meta).testers));
    render();
    renderNotifBadge();
    void boardMembers.load(); // best-effort: populate the author-name map for timelines (online only)
    void convertLegacyVersionsBoard();
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

// Board-level actions live in the burger (☰) menu — sharing (rarely opened) and
// "forget password" (rarely needed) don't warrant header buttons.
// dopeMenu.setExtras renders them as actions.
window.dopeMenu?.setExtras([{
  icon: "pencil",
  label: "Переименовать доску",
  title: "Изменить название доски",
  onClick: () => { void renameBoard(); },
}, {
  icon: "columns-3",
  label: "Управление списками",
  title: "Переупорядочить списки и связать их в группы (списки списков)",
  onClick: () => openListsManage(),
}, {
  icon: "list-checks",
  label: "Массовое действие",
  title: "Отметить карточки на всей доске и сделать с ними одно действие",
  onClick: () => setMassMode(!massMode),
}, {
  icon: "file-up",
  label: "Импорт",
  title: "Импортировать пакет вопросов (.4s, .zip или .docx)",
  onClick: () => openImportPick(),
}, {
  icon: "flask-conical",
  label: "Тесты",
  title: "Тест-сессии доски: кто когда играл, приглашение со временем начала",
  onClick: () => sessionsPanel.open(),
}, {
  icon: "tags",
  label: "Метки",
  title: "Переименовать, перекрасить или удалить метки доски",
  onClick: () => openLabelsEditor(),
}, {
  icon: "users",
  label: "Участники доски",
  title: "Поделиться доской: добавить или убрать участников",
  onClick: () => boardMembers.open(),
}, {
  icon: "wand-sparkles",
  label: "Исправить оформление Trello",
  title: "Убрать артефакты Trello (двойные переносы, экранирование, смарт-ссылки) во всех карточках",
  onClick: () => { void fixTrelloFormattingBoard(); },
}, {
  icon: "replace",
  label: "Найти и заменить",
  title: "Заменить один и тот же текст во всех карточках доски, списка или группы",
  onClick: () => openReplace(),
}, {
  // wand-sparkles twice over: the vendored lucide set has no «type» glyph, and
  // both items are the same kind of act — rewrite the text of every card at once.
  icon: "wand-sparkles",
  label: "Типографить всю доску",
  title: "Кавычки-ёлочки, тире, неразрывные пробелы и раскодированные ссылки — во всех карточках и всех версиях",
  onClick: () => { void typographBoard(); },
}, {
  icon: "lock",
  label: "Забыть пароль доски",
  title: "Забыть пароль доски на этом устройстве",
  onClick: async () => {
    await xyCrypto.forgetDK(boardId);
    // The names this board contributed to the person directory outlive nothing:
    // once the DK is gone its content is ciphertext with no key on this device.
    // Its Search Index goes the same way, and for a sharper reason (ADR-0008):
    // plaintext that outlived its key would keep the board readable with none.
    people.forget(boardId);
    await xySearchIndex.forget(boardId);
    location.reload();
  },
}, {
  icon: "trash-2",
  label: "Удалить доску",
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
    people.forget(boardId);
    await xySearchIndex.forget(boardId);
    location.href = "/";
  } catch (err) { alert("Не удалось удалить: " + errMsg(err)); }
}

// ---- board-wide description rewrites ----
// Two things rewrite every card's 4s at once: the Trello clean-up and the version
// conversion. Both are the same walk — collect what a transform changes, then
// patch each changed card with a desc_edit timeline entry so the rewrite is
// auditable and reversible — so they share it. A transform returns null for
// «nothing to do here».

interface DescChange { card: BoardCard; desc: string }

function collectDescChanges(next: (c: BoardCard) => string | null): DescChange[] {
  const out: DescChange[] = [];
  for (const c of state.cards) {
    const desc = next(c);
    if (desc !== null && desc !== c.desc) out.push({ card: c, desc });
  }
  return out;
}

async function applyDescChanges(changes: ReadonlyArray<DescChange>): Promise<void> {
  const key = mustDK();
  for (const ch of changes) {
    await patch("patchCard", `/api/cards/${ch.card.id}`, {
      description_enc: await xyCrypto.encField(key, ch.desc),
      desc_event_enc: await xyCrypto.encField(key, JSON.stringify({ before: ch.card.desc, after: ch.desc })),
    });
    ch.card.desc = ch.desc;
  }
  render();
}

// fixTrelloFormattingBoard re-applies chgksuite's Trello clean-up (the same fix
// the importer runs) to every already-imported card whose description still
// carries Trello artefacts.
async function fixTrelloFormattingBoard(): Promise<void> {
  const changes = collectDescChanges((c) => xyChgk.fixTrelloFormatting(c.desc));
  if (!changes.length) { alert("Нечего исправлять — оформление уже в порядке."); return; }
  if (!confirm(`Исправить оформление Trello в ${changes.length} карточк(ах)? Описания будут изменены.`)) return;
  setStatus("saving");
  try {
    await applyDescChanges(changes);
    setStatus("saved");
    alert(`Исправлено карточек: ${changes.length}.`);
  } catch (err) {
    setStatus("error");
    alert("Ошибка при исправлении: " + errMsg(err));
  }
}

// typographBoard runs the typography pass over every card on the board, every
// version of it. It runs in the browser, so a whole package's question text is
// never posted anywhere and this works offline like any other board edit.
// Stress marks are the one part of the pass that guesses. chgk writes stress by
// capitalising the vowel («брАзер»), and a camel-cased compound («ГазпромИнвест»)
// is exactly the same shape, so a board-wide press asks first — one tick per
// distinct word, however many cards it appears in. Everything else the pass does
// (quotes, dashes, spaces, percent-escapes) is not a guess and is not asked about.
async function typographBoard(): Promise<void> {
  const picks = xyTypo.accentPicks(state.cards.map((c) => c.desc));
  if (!picks.length) { await runTypographBoard(null); return; }
  openAccentReview(picks, (allow) => { void runTypographBoard(allow); });
}

async function runTypographBoard(allow: Set<string> | null): Promise<void> {
  const opts = allow ? { allow } : {};
  const changes = collectDescChanges((c) => xyTypo.passVersions(c.desc, opts));
  const total = state.cards.length;
  if (!changes.length) { alert("Нечего типографить — вся доска уже в порядке."); return; }
  // «N из M», because the rest were already right: the pass only rewrites a card
  // whose text it actually changes, and a bare count reads like it skipped some.
  if (!allow && !confirm(`Типографить ${changes.length} из ${total}? В остальных карточках менять нечего.`)) return;
  setStatus("saving");
  try {
    await applyDescChanges(changes);
    setStatus("saved");
    alert(`Оттипографлено карточек: ${changes.length} из ${total}.`);
  } catch (err) {
    setStatus("error");
    alert("Ошибка при типографике: " + errMsg(err));
  }
}

// ---- найти и заменить ----
// One replacement over the whole board, one list or one group. The matching is
// literal (find.ts) and the structure is out of its reach, so what is left to
// judge is context: the same needle in two places can want two different
// answers, which is why the preview ticks Occurrences and not cards.
const replaceOverlay = byId("replaceOverlay");
const replaceScope = byId<HTMLSelectElement>("replaceScope");
const replaceFrom = byId<HTMLInputElement>("replaceFrom");
const replaceTo = byId<HTMLInputElement>("replaceTo");
const replaceCase = byId<HTMLInputElement>("replaceCase");
const replaceMessage = byId("replaceMessage");

interface Occurrence { i: number; card: BoardCard; span: FindSpan }

// Rendering thousands of rows with a checkbox each freezes the tab; a page holds
// a hundred. Ticks live over ALL occurrences, so what is off-screen is still
// replaced and still counted.
const REPLACE_PAGE = 100;
let occurrences: Occurrence[] = [];
let perCard = new Map<number, number[]>();
// The text each card had when the plan was drawn, BY VALUE. A card object is
// mutated in place by the editor and swapped wholesale by a snapshot reload, so
// neither the reference nor its current .desc can say whether the spans still
// point where the preview said they did.
let plannedDesc = new Map<number, string>();
let replacePicked = new Set<number>();
let replacePageNo = 0;

// cardsInBoardOrder walks lists in board order and cards by rank within them, so
// the preview reads down the board rather than in whatever order the state is in.
function cardsInBoardOrder(cards: readonly BoardCard[]): BoardCard[] {
  const order = new Map([...state.lists].sort(byRank).map((l, i) => [l.id, i]));
  return [...cards].sort((a, b) => (order.get(a.listId) ?? 0) - (order.get(b.listId) ?? 0) || byRank(a, b));
}

function scopeCards(): BoardCard[] {
  const v = replaceScope.value;
  if (v.startsWith("list:")) {
    const id = Number(v.slice(5));
    return cardsInBoardOrder(state.cards.filter((c) => c.listId === id));
  }
  if (v.startsWith("group:")) {
    const id = Number(v.slice(6));
    const ids = new Set(state.lists.filter((l) => l.groupId === id).map((l) => l.id));
    return cardsInBoardOrder(state.cards.filter((c) => ids.has(c.listId)));
  }
  return cardsInBoardOrder(state.cards);
}

function planReplace(): void {
  const from = replaceFrom.value;
  occurrences = [];
  perCard = new Map();
  plannedDesc = new Map();
  if (from) {
    for (const card of scopeCards()) {
      for (const span of xyFind.replaceSpans(card.desc, from, replaceCase.checked)) {
        const o = { i: occurrences.length, card, span };
        occurrences.push(o);
        const ids = perCard.get(card.id);
        if (ids) ids.push(o.i); else perCard.set(card.id, [o.i]);
        plannedDesc.set(card.id, card.desc);
      }
    }
  }
  // A fresh plan is a fresh set of occurrences, so everything starts ticked —
  // which is why only the three inputs that CHANGE what was found re-plan; the
  // «Заменить на» field merely redraws (see the wiring below), or an editor's
  // unticking would be undone by fixing a typo in it.
  replacePicked = new Set(occurrences.map((o) => o.i));
  replacePageNo = 0;
  renderReplace();
}

function renderReplace(): void {
  const box = byId("replaceHits");
  const pages = Math.max(1, Math.ceil(occurrences.length / REPLACE_PAGE));
  replacePageNo = Math.min(replacePageNo, pages - 1);
  const slice = occurrences.slice(replacePageNo * REPLACE_PAGE, (replacePageNo + 1) * REPLACE_PAGE);
  const to = replaceTo.value;
  const rows: HTMLElement[] = [];
  // null, not -1: a card created offline HAS the id -1 (the first temp id), and
  // would then never get its heading row.
  let shownCard: number | null = null;
  for (const o of slice) {
    if (o.card.id !== shownCard) {
      shownCard = o.card.id;
      const ids = perCard.get(o.card.id) || [];
      const head = el("input", { type: "checkbox" }) as HTMLInputElement;
      head.checked = xyMass.allSelected(replacePicked, ids);
      head.addEventListener("change", () => {
        replacePicked = xyMass.toggleAll(replacePicked, ids);
        renderReplace();
      });
      rows.push(el("label", { class: "replace-card" }, head,
        el("span", { class: "replace-card-name", text: xySearchIndex.cardTitle(o.card, state.cardTitle, "(пустая карточка)") }),
        el("span", { class: "replace-card-count", text: `${ids.length}` })));
    }
    const snip = xyFind.snippet(o.card.desc, o.span, 60);
    const cb = el("input", { type: "checkbox" }) as HTMLInputElement;
    cb.checked = replacePicked.has(o.i);
    cb.addEventListener("change", () => {
      replacePicked = xyMass.toggleOne(replacePicked, o.i);
      renderReplace();
    });
    rows.push(el("label", { class: "replace-hit" }, cb,
      el("span", { class: "replace-hit-text" },
        snip.text.slice(0, snip.start),
        el("del", { text: snip.text.slice(snip.start, snip.end) }),
        ...(to ? [el("ins", { text: to })] : []),
        snip.text.slice(snip.end))));
  }
  box.replaceChildren(...rows);
  byId("replacePage").textContent = occurrences.length ? `Страница ${replacePageNo + 1} из ${pages}` : "";
  byId<HTMLButtonElement>("replacePrev").disabled = replacePageNo === 0;
  byId<HTMLButtonElement>("replaceNext").disabled = replacePageNo >= pages - 1;
  const picked = occurrences.filter((o) => replacePicked.has(o.i));
  const cards = new Set(picked.map((o) => o.card.id)).size;
  const run = byId<HTMLButtonElement>("replaceRun");
  run.disabled = picked.length === 0;
  // An empty «на» deletes, and the button says so rather than promising a
  // replacement with nothing.
  const verb = replaceTo.value ? "Заменить" : "Удалить";
  run.textContent = picked.length ? `${verb} ${picked.length} в ${xyMass.cardCount(cards)}` : verb;
  replaceMessage.textContent = replaceFrom.value && !occurrences.length ? "Ничего не найдено." : "";
}

async function runReplace(): Promise<void> {
  const to = replaceTo.value;
  const byCard = new Map<number, { card: BoardCard; spans: FindSpan[] }>();
  for (const o of occurrences) {
    if (!replacePicked.has(o.i)) continue;
    const entry = byCard.get(o.card.id) || { card: o.card, spans: [] };
    entry.spans.push(o.span);
    byCard.set(o.card.id, entry);
  }
  // Spans are offsets into the text as it was when the preview was drawn. If that
  // text has moved since — a co-author's edit arriving with a snapshot reload, or
  // this editor's own save — applying them would corrupt the card and record a
  // desc_edit whose «before» never existed. Such a card is left out and said out
  // loud rather than rewritten blind.
  const changes: DescChange[] = [];
  let stale = 0;
  for (const x of byCard.values()) {
    const live = state.cards.find((c) => c.id === x.card.id);
    if (!live || live.desc !== plannedDesc.get(x.card.id)) { stale++; continue; }
    changes.push({ card: live, desc: xyFind.applySpans(live.desc, x.spans, to) });
  }
  if (!changes.length && !stale) return;
  setStatus("saving");
  try {
    await applyDescChanges(changes);
    setStatus("saved");
    // Re-plan first: what is left to replace has changed, and renderReplace owns
    // the message line, so the report has to be written after it.
    planReplace();
    replaceMessage.textContent = `Готово: ${xyMass.cardCount(changes.length)}.` +
      (stale ? ` ${xyMass.cardCount(stale)} изменились, пока шёл просмотр — они пропущены, найдите заново.` : "");
  } catch (err) {
    setStatus("error");
    replaceMessage.textContent = "Ошибка при замене: " + errMsg(err);
  }
}

function openReplace(): void {
  // The scope list is built on open, so a list renamed or grouped since last time
  // is named correctly.
  const groups = new Map(state.groups.map((g) => [g.id, g.name]));
  const seen = new Set<number>();
  const opts = [el("option", { value: "board", text: "Вся доска" })];
  for (const l of [...state.lists].sort(byRank)) {
    if (l.groupId != null && !seen.has(l.groupId)) {
      seen.add(l.groupId);
      opts.push(el("option", { value: `group:${l.groupId}`, text: `Группа: ${groups.get(l.groupId) || ""}` }));
    }
    opts.push(el("option", { value: `list:${l.id}`, text: l.title }));
  }
  replaceScope.replaceChildren(...opts);
  replaceMessage.textContent = "";
  planReplace();
  replaceOverlay.hidden = false;
  overlayStack.open({ el: replaceOverlay, close: () => { replaceOverlay.hidden = true; } });
  replaceFrom.focus();
}

// Typing re-scans every card in scope, so it waits for a pause; the case toggle
// and the scope select change the answer at once and re-plan immediately.
let planTimer = 0;
replaceFrom.addEventListener("input", () => {
  clearTimeout(planTimer);
  planTimer = setTimeout(planReplace, 200);
});
for (const node of [replaceCase, replaceScope]) node.addEventListener("input", () => planReplace());
// What is replaced has not changed — only what the preview shows it becoming.
replaceTo.addEventListener("input", () => renderReplace());
byId("replacePrev").addEventListener("click", () => { replacePageNo--; renderReplace(); });
byId("replaceNext").addEventListener("click", () => { replacePageNo++; renderReplace(); });
byId("replaceRun").addEventListener("click", () => { void runReplace(); });
byId("replaceCancel").addEventListener("click", () => overlayStack.pop());

// ---- the stress-mark review ----
const accentOverlay = byId("accentOverlay");
let accentApply: ((allow: Set<string>) => void) | null = null;

function hideAccentReview(): void {
  accentOverlay.hidden = true;
  accentApply = null;
}

function closeAccentReview(): void { overlayStack.pop(); }

function openAccentReview(picks: ReadonlyArray<{ from: string; to: string }>, apply: (allow: Set<string>) => void): void {
  const box = byId("accentPicks");
  box.replaceChildren(...picks.map((p) => {
    const cb = el("input", { type: "checkbox", checked: "checked" }) as HTMLInputElement;
    cb.dataset.word = p.from;
    return el("label", { class: "accent-pick" }, cb,
      el("span", { class: "accent-from", text: p.from }),
      el("span", { class: "accent-arrow", text: "→" }),
      el("span", { class: "accent-to", text: p.to }));
  }));
  accentApply = apply;
  accentOverlay.hidden = false;
  overlayStack.open({ el: accentOverlay, close: hideAccentReview });
}

byId("accentCancel").addEventListener("click", closeAccentReview);
byId("accentRun").addEventListener("click", () => {
  const allow = new Set<string>();
  for (const cb of byId("accentPicks").querySelectorAll<HTMLInputElement>("input:checked")) {
    if (cb.dataset.word) allow.add(cb.dataset.word);
  }
  const apply = accentApply;
  closeAccentReview();
  apply?.(allow);
});

// convertLegacyVersionsBoard rewrites the cards written under the old scheme,
// where a Version was a run of question text between (PAGEBREAK) directives and
// every other field was shared. They become whole bodies the first time their
// board is opened after this release. Idempotent — a converted card offers
// nothing left to find — so the cost of running it on every load is one pass over
// the descriptions. Each rewrite carries a desc_edit entry, like any other edit.
async function convertLegacyVersionsBoard(): Promise<void> {
  if (!xySync.isOnline()) return;
  const changes = collectDescChanges((c) => (c.kind === "question" ? xyChgk.convertLegacyVersions(c.desc) : null));
  if (!changes.length) return;
  try {
    await applyDescChanges(changes);
  } catch (err) {
    // Nothing is lost by failing: the cards keep their old spelling and the next
    // load tries again.
    console.error("не удалось перевести версии карточек", err);
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
    const cards = cardsInBoardOrder(state.cards);
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
  // Unconditional: renderMassBar is also what HIDES the bar, so guarding it on
  // massMode left «Готово» with nothing to close.
  if (massMode) massSelected = xyMass.prune(massSelected, state.cards);
  renderMassBar();
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
    const items: MenuItem[] = [{ icon: icon("plus"), label: "Добавить карточку", onClick: () => { void cardDetail.addCard(list); } }];
    if (list.groupId != null) {
      items.push(
        { icon: icon("eye"), label: "Предпросмотр списка", onClick: () => { void previewList(list); } },
        { icon: icon("eye"), label: "Предпросмотр всей группы", onClick: () => { void previewList(list, true); } },
      );
    } else {
      items.push({ icon: icon("eye"), label: "Предпросмотр", onClick: () => { void previewList(list); } });
    }
    items.push(
      { icon: icon("users"), label: "Список тестеров", onClick: () => openTesterList(list) },
      { icon: icon("arrow-left-right"), label: "Переместить список…", onClick: () => openMoveList(list) },
      { icon: icon("pencil"), label: "Переименовать список", onClick: () => { void renameList(list); } },
    );
    const grouped = list.groupId != null;
    const suffix = grouped ? " группы" : "";
    items.push(
      { icon: icon("file-down"), label: `Экспорт${suffix}`, onClick: () => openExport(list) },
      { icon: icon("file-text"), label: grouped ? "Генерация раздаток (вся группа)" : "Генерация раздаток", onClick: () => openHandouts(list) },
    );
    items.push({ icon: icon("trash-2"), label: "Удалить список", onClick: () => { void deleteList(list); } });
    popupMenu(menuWrap, items);
  });
  menuWrap.append(menuBtn);
  const cards = cardsOf(list.id);
  const headMain = el("div", { class: "klist-headmain" },
    el("span", { class: "klist-title", text: list.title || "(без названия)" }));
  const qCount = cards.filter((c) => c.kind === "question").length;
  if (qCount) headMain.append(el("span", { class: "klist-count", text: questionCountLabel(qCount) }));
  const headKids: HTMLElement[] = [];
  if (massMode) {
    const ids = cards.map((c) => c.id);
    const all = el("input", { type: "checkbox", "aria-label": "Отметить весь список" }) as HTMLInputElement;
    all.dataset.listId = String(list.id);
    all.checked = xyMass.allSelected(massSelected, ids);
    all.addEventListener("change", () => {
      massSelected = xyMass.toggleAll(massSelected, ids);
      renderMassBar();
      paintMassChecks();
    });
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
  if (massMode) {
    const box = el("input", { type: "checkbox", "aria-label": "Отметить карточку" }) as HTMLInputElement;
    box.dataset.cardId = String(card.id);
    box.checked = massSelected.has(card.id);
    const wrap = el("label", { class: "kcard-check" }, box);
    wrap.addEventListener("click", (e) => { e.stopPropagation(); });
    box.addEventListener("change", () => massToggle(card.id));
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
  overlayStack.open({ el: byId("moveListOverlay"), close: hideMoveList });
  void populateMoveListBoards();
}
function hideMoveList(): void { byId("moveListOverlay").hidden = true; }
function closeMoveList(): void { overlayStack.pop(); }

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
        const own = assignmentsOf(c.id, null);
        if (own.length) {
          await jput(`/api/cards/${cres.id}/labels`, { labels: own.map((a) => ({ label_id: a.labelId, session_id: null })) });
          state.cardLabels.push(...own.map((a) => ({ cardId: cres.id, labelId: a.labelId, sessionId: null })));
        }
        await cardDetail.copyCardExtras(c.id, key, cres.id);
      }
    } else {
      // Cross-board: re-encrypt under the target board's key, reconcile labels by
      // decrypted name+color (same as the per-card path).
      const tdk = ctx.dk;
      const lres = (await jpost(`/api/boards/${targetBid}/lists`, {
        title_enc: await xyCrypto.encField(tdk, src.title), rank, type,
      })) as { id: number };
      let cr: string | null = null;
      for (const c of srcCards) {
        cr = keyBetween(cr, null);
        const cres = (await jpost(`/api/lists/${lres.id}/cards`, await cardDetail.cardCopyBody(c, cr, tdk))) as { id: number };
        const plays = await cardDetail.reconcilePlayings(c.id, targetBid, tdk, ctx);
        if (plays.length) await jput(`/api/cards/${cres.id}/sessions`, { session_ids: plays });
        const assignments = await cardDetail.reconcileLabels(c.id, targetBid, tdk, ctx);
        if (assignments.length) await jput(`/api/cards/${cres.id}/labels`, { labels: assignments });
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
  overlayStack.open({ el: listsManageOverlay, close: hideListsManage });
  renderManage();
}
function hideListsManage(): void { listsManageOverlay.hidden = true; }
function closeListsManage(): void { overlayStack.pop(); }

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
  const btn = el("button", { class: "btn btn-small btn-ghost lm-move-btn", type: "button", title: "Переместить на эту позицию" }, icon("arrow-up-down"));
  const go = (): void => { const n = parseInt(inp.value, 10); if (n >= 1) void moveUnitsTo(new Set([unit.key]), n); };
  btn.addEventListener("click", go);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); go(); } });
  return el("div", { class: "lm-move" }, inp, btn);
}

function manageTitle(list: BoardList): string {
  return list.title || "(без названия)";
}

function renderManageUnit(unit: Unit, pos: number): HTMLElement {
  const node = el("div", { class: "lm-unit lm-" + unit.kind, draggable: "true", dataset: { unitKey: unit.key } });
  if (unit.kind === "group") {
    const g = groupById(unit.id);
    node.append(el("div", { class: "lm-row lm-grouphead" },
      manageCheckbox(unit),
      el("span", { class: "lm-pos", text: "#" + pos }),
      el("span", { class: "lm-handle", text: "≡", title: "Перетащить" }),
      el("span", { class: "lm-title lm-group-title" }, ...iconed("link", (g && g.name) || "Связанные списки")),
      el("button", { class: "lm-icon", type: "button", title: "Переименовать группу", onclick: () => { void renameGroup(unit.id); } }, icon("pencil")),
      el("button", { class: "lm-icon", type: "button", title: "Разъединить группу", onclick: () => { void unlinkGroup(unit.id); } }, icon("unlink")),
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
  overlayStack.open({ el: importPickOverlay, close: hideImportPick });
}
function hideImportPick(): void { importPickOverlay.hidden = true; }
function closeImportPick(): void { overlayStack.pop(); }

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
  overlayStack.open({ el: importOverlay, close: hideImportVerify });
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

function hideImportVerify(): void {
  importOverlay.hidden = true;
  if (importCtx) for (const url of importCtx.imgMap.values()) URL.revokeObjectURL(url);
  importCtx = null;
  byId("importPreview").replaceChildren();
}
function closeImportVerify(): void { overlayStack.pop(); }

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

// ---- export a list ----
// Concatenate the list's card descriptions (in board order) into a chgksuite
// "4s" document, gather any images referenced by `(img ...)` directives from the
// cards' attachments, and hand both to the server, which composes the requested
// formats in memory and streams back one file — or a zip of all of them.
// The .docx and the .pdf render the same document: the PDF is typeset by typst
// to look like the docx (same layout, same non-breaking spaces/hyphens, same
// keep-together questions). See internal/server/exportpack.go.
// exportScope resolves which lists a per-list action (export / handouts) covers:
// a standalone list is just itself; a grouped list pulls in every list of its
// group, in board order, so the whole list_of_lists exports as one file.
// Returns { cards (concatenated, in order), title }.
function exportScope(list: BoardList): { cards: BoardCard[]; title: string } {
  let lists = [list], title = list.title || "export";
  if (list.groupId != null) {
    lists = listsInGroup(list.groupId);
    const g = groupById(list.groupId);
    if (g && g.name) title = g.name;
  }
  return { cards: lists.flatMap((l) => cardsOf(l.id)), title };
}

// exportSource is the 4s document a list exports as: its cards' descriptions in
// board order, blank-line separated. Every format is rendered from this one
// string, which is why the versions are folded back into one question block here
// and nowhere else — a versioned card is still one numbered question.
function exportSource(cards: ReadonlyArray<BoardCard>): string {
  return cards.map((c) => xyChgk.composeVersions(c.desc).trim()).filter(Boolean).join("\n\n") + "\n";
}

// The export modal's five formats, in the order they are offered. `server` marks
// the ones that need the server to render, so offline can disable exactly those.
const EXPORT_FORMATS = [
  { key: "4s", box: "exportFmt4s", server: false },
  { key: "docx", box: "exportFmtDocx", server: true },
  { key: "pdf", box: "exportFmtPdf", server: true },
  { key: "pdf_mobile", box: "exportFmtPdfMobile", server: true },
  { key: "handouts", box: "exportFmtHandouts", server: true },
] as const;

const exportOverlay = byId("exportOverlay");
let exportCtx: { cards: BoardCard[]; title: string; hndt: string } | null = null;

function exportBox(box: string): HTMLInputElement { return byId<HTMLInputElement>(box); }
function exportChosen(): string[] {
  return EXPORT_FORMATS.filter((f) => exportBox(f.box).checked && !exportBox(f.box).disabled).map((f) => f.key);
}

// syncExportForm keeps the button row honest: nothing ticked is nothing to do,
// and the toggle-all label says which way it will go.
function syncExportForm(): void {
  const chosen = exportChosen();
  byId<HTMLButtonElement>("exportRun").disabled = chosen.length === 0;
  const available = EXPORT_FORMATS.filter((f) => !exportBox(f.box).disabled);
  const allOn = available.length > 0 && chosen.length === available.length;
  byId("exportToggleAll").textContent = allOn ? "Снять выделение" : "Выбрать все";
}

function openExport(list: BoardList): void {
  const scope = exportScope(list);
  if (!scope.cards.length) { alert("В списке нет карточек."); return; }
  const numbers = xyChgk.numberQuestionCards(scope.cards);
  const metas: Record<number, string> = {};
  for (const c of scope.cards) if (c.handoutMeta) metas[c.id] = c.handoutMeta;
  const hndt = xyChgk.generateHndt(scope.cards, numbers, metas);
  exportCtx = { cards: scope.cards, title: scope.title, hndt };

  // Offline everything but the .4s is unreachable: the other formats render
  // server-side, and even the .4s ships without its images (they are fetched).
  const offline = !xySync.isOnline();
  for (const f of EXPORT_FORMATS) {
    const box = exportBox(f.box);
    box.disabled = (offline && f.server) || (f.key === "handouts" && !hndt.trim());
    if (box.disabled) box.checked = false;
  }
  const notes: string[] = [];
  if (offline) notes.push("Офлайн: доступен только .4s, без изображений.");
  if (!hndt.trim()) notes.push("В списке нет вопросов с раздаточным материалом.");
  byId("exportMessage").textContent = notes.join(" ");
  syncExportForm();
  exportOverlay.hidden = false;
  overlayStack.open({ el: exportOverlay, close: hideExport });
}

function closeExport(): void { overlayStack.pop(); }

function hideExport(): void {
  exportOverlay.hidden = true;
  exportCtx = null;
}

// runExport renders the ticked formats. A bare .4s with no images never touches
// the network — it is the one export that works offline.
async function runExport(): Promise<void> {
  if (!exportCtx) return;
  const { cards, title, hndt } = exportCtx;
  const formats = exportChosen();
  if (!formats.length) return;
  const source = exportSource(cards);
  // Images are fetched (and decrypted) from the server, so offline there are
  // none to be had — the .4s then goes out as bare text rather than not at all.
  const wanted = xySync.isOnline() ? imageRefs(cards) : new Set<string>();
  const wantsImages = formats.includes("4s") && wanted.size > 0;

  if (formats.length === 1 && formats[0] === "4s" && !wantsImages) {
    downloadBlob(new Blob([source], { type: "text/plain;charset=utf-8" }), `${title}.4s`);
    closeExport();
    return;
  }

  const msg = byId("exportMessage");
  if (!xySync.requireOnline("Эти форматы доступны только онлайн.", msg)) return;
  const btn = byId<HTMLButtonElement>("exportRun");
  btn.disabled = true;
  msg.textContent = formats.includes("handouts") ? "Экспорт… (вёрстка раздаток может занять время)" : "Экспорт…";
  setStatus("saving");
  try {
    const fd = new FormData();
    fd.append("source", source);
    fd.append("filename", title);
    fd.append("formats", formats.join(","));
    if (formats.includes("handouts")) fd.append("hndt", hndt);

    // Images are only shipped for the .4s (which references them by name);
    // docx and pdf embed their own copies, so nothing else needs the upload.
    const needed = new Set<string>();
    if (formats.some((f) => f !== "4s") || wantsImages) for (const n of wanted) needed.add(n);
    const found = await appendImages(fd, cards, needed);
    const missing = [...needed].filter((n) => !found.has(n));
    if (missing.length && !confirm(`Не найдены изображения: ${missing.join(", ")}. Продолжить?`)) {
      setStatus("saved");
      msg.textContent = "";
      return;
    }
    const res = await fetch("/api/export/pack", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    downloadBlob(await res.blob(), filenameFromResponse(res) || `${title}.zip`);
    setStatus("saved");
    closeExport();
  } catch (err) {
    setStatus("error");
    msg.textContent = "Экспорт не удался: " + errMsg(err);
  } finally {
    btn.disabled = false;
    syncExportForm();
  }
}

// filenameFromResponse reads the name the server chose, so a single-format pack
// arrives as foo.docx rather than foo.zip.
function filenameFromResponse(res: Response): string {
  const m = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "");
  return m ? m[1] : "";
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = el("a", { href: url, download: filename });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

byId("exportForm").addEventListener("submit", (e) => { e.preventDefault(); void runExport(); });
byId("exportToggleAll").addEventListener("click", () => {
  const target = byId("exportToggleAll").textContent === "Выбрать все";
  for (const f of EXPORT_FORMATS) {
    const box = exportBox(f.box);
    if (!box.disabled) box.checked = target;
  }
  syncExportForm();
});
for (const f of EXPORT_FORMATS) exportBox(f.box).addEventListener("change", syncExportForm);
byId("exportCancel").addEventListener("click", closeExport);
exportOverlay.addEventListener("pointerdown", (e) => { if (e.target === exportOverlay) closeExport(); });

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
let handoutsDlUrl: string | null = null;

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
  overlayStack.open({ el: handoutsOverlay, close: hideHandouts });
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
  if (handoutsPdfUrl) { revokeNamedUrl(handoutsPdfUrl); handoutsPdfUrl = null; }
  if (handoutsDlUrl) { URL.revokeObjectURL(handoutsDlUrl); handoutsDlUrl = null; }
}

// handoutFileBase names a generated раздатка after the board and the list it came
// from — «Моя_доска_Тур_1_handouts» — rather than after nothing in particular
// (issue #43). Only path separators and whitespace are folded away: the name is
// the one the editor typed, Cyrillic included, and every download it rides on
// spells it in UTF-8.
function handoutFileBase(): string {
  const clean = (s: string): string => s.trim().replace(/[\\/\s]+/g, "_");
  const list = (handoutsCtx && (handoutsCtx.title || handoutsCtx.list.title)) || "";
  return [clean(state.name), clean(list), "handouts"].filter(Boolean).join("_");
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

function closeHandouts(): void { overlayStack.pop(); }

async function hideHandouts(): Promise<void> {
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
    const name = handoutFileBase() + ".pdf";
    const blob = await res.blob();
    handoutsPdfUrl = await namedUrl(blob, name);
    byId("handoutsPdf").replaceChildren(pdfPreviewNode(handoutsPdfUrl));
    // Only the preview needs /dl/ (the viewer's Save name); Chromium re-issues a
    // download outside the worker, where that path 404s — so the button gets a blob.
    handoutsDlUrl = URL.createObjectURL(blob);
    const dl = byId<HTMLAnchorElement>("handoutsDownload");
    dl.href = handoutsDlUrl;
    dl.setAttribute("download", name);
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
  const targets = gatherTargets(lists, wanted);
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
    downloadBlob(await res.blob(), handoutFileBase() + ".zip");
    msg.textContent = "Готово — zip со всеми PDF скачан.";
  } catch (err) {
    msg.textContent = "Split-fit не удался: " + errMsg(err);
  } finally {
    btn.disabled = false;
  }
}

byId("handoutsGenerate").addEventListener("click", () => { void generateHandoutsPdf(); });
// Edit the .hndt, regenerate, look: Cmd/Ctrl-Enter is that loop without the trip
// to the button.
onCmdEnter(byId("handoutsSource"), () => byId("handoutsGenerate").click());
byId("handoutsSplitFit").addEventListener("click", () => { void generateSplitFitZip(); });
byId("handoutsClose").addEventListener("click", () => { void closeHandouts(); });
handoutsOverlay.addEventListener("pointerdown", (e) => { if (e.target === handoutsOverlay) void closeHandouts(); });

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
  await attachments.resolveImages(cards, imageRefs(cards), (name, url) => {
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
  preview: { renderPreviewCard, resolveImages: attachments.resolveImages, imageRefs, fillPreviewImages, previewList },
  attachments,
  popupMenu,
  readMarkers: { refreshCardUnreadDot, renderNotifBadge },
  timeline: {
    load: (cardId) => timeline.load(cardId),
    events: () => timeline.events(),
    resetFilter: () => timeline.resetFilter(),
    readBuckets: () => timeline.readBuckets(),
    ensureVisible: (type) => timeline.ensureVisible(type),
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

// ---- массовое действие ----
// Ticking cards across the whole board, then doing one thing to all of them.
// The rules (what a select-all covers, board order, how a partly-failed run
// reads) live in massaction.js; this is the DOM, the pickers and the writes.
let massMode = false;
let massSelected: Set<number> = new Set();
let massAction: MassAction | null = null;

function massCards(): BoardCard[] {
  return xyMass.ordered(massSelected, boardCardsInOrder());
}

// boardCardsInOrder flattens the board the way the reader sees it: lists by
// rank, cards by rank inside each. A bulk move must land its cards in that
// order, not in whatever order they were ticked.
function boardCardsInOrder(): BoardCard[] {
  return [...state.lists].sort(byRank).flatMap((l) => cardsOf(l.id));
}

function setMassMode(on: boolean): void {
  massMode = on;
  if (!on) massSelected = new Set();
  document.body.classList.toggle("mass-mode", on);
  render();
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
  for (const box of kanban.querySelectorAll<HTMLInputElement>(".kcard-check input")) {
    box.checked = massSelected.has(Number(box.dataset.cardId));
  }
  for (const box of kanban.querySelectorAll<HTMLInputElement>(".klist-check input")) {
    const ids = cardsOf(Number(box.dataset.listId)).map((c) => c.id);
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
const massOverlay = byId("massOverlay");
let massTarget: { listId: number; ctx: MoveCtx } | null = null;
let massPick: number | null = null;

function closeMass(): void { overlayStack.pop(); }
function hideMass(): void { massOverlay.hidden = true; massAction = null; massTarget = null; massPick = null; }

async function openMass(action: MassAction): Promise<void> {
  massAction = action;
  massPick = null;
  massTarget = null;
  const n = massSelected.size;
  massOverlay.querySelector<HTMLElement>(".appearance-modal-title")!.textContent = `${action.label}: ${xyMass.cardCount(n)}`;
  byId("massMessage").textContent = "";
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
  massOverlay.hidden = false;
  overlayStack.open({ el: massOverlay, close: hideMass });
}

// The label picker is the board's own label list, same chips as the card's —
// reusing the vocabulary rather than inventing a bulk-only one.
function buildMassLabelPick(body: HTMLElement, run: HTMLButtonElement): void {
  if (!state.labels.length) { body.append(el("p", { class: "label-empty", text: "На доске нет меток." })); return; }
  const row = el("div", { class: "label-picker" });
  for (const l of [...state.labels].sort((a, b) => a.name.localeCompare(b.name))) {
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
  paintLabels();
}

function buildMassSessionPick(body: HTMLElement, run: HTMLButtonElement): void {
  if (!state.sessions.length) { body.append(el("p", { class: "label-empty", text: "На доске нет тестов." })); return; }
  const sel = el("select", { class: "input" }) as HTMLSelectElement;
  sel.append(el("option", { value: "", text: "— выберите тест —" }));
  for (const s of state.sessions) sel.append(el("option", { value: String(s.id), text: sessionName(s.id) }));
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
  const boards = await cardDetail.moveBoardOptions();
  for (const b of boards) boardSel.append(el("option", { value: String(b.id), text: b.label }));
  boardSel.value = String(boardId);
  const fillLists = async (): Promise<void> => {
    listSel.replaceChildren();
    run.disabled = true;
    massTarget = null;
    const ctx = await cardDetail.loadMoveBoard(Number(boardSel.value));
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
  const online = action.key === "copy" || (action.key === "move" && massTarget && massTarget.ctx.boardId !== boardId);
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
  render();
  msg.textContent = xyMass.runSummary(ok, failed.size);
  run.disabled = false;
  if (!failed.size) setTimeout(closeMass, 900);
}

async function applyMass(action: MassAction, card: BoardCard): Promise<void> {
  switch (action.key) {
    case "delete":
      await del("deleteCard", `/api/cards/${card.id}`);
      state.cards = state.cards.filter((c) => c.id !== card.id);
      forgetCardLabels([card]);
      return;
    case "label-add":
    case "label-del": {
      if (massPick == null) throw new Error("не выбрана метка");
      const own = state.cardLabels.filter((a) => a.cardId === card.id);
      const keep = action.key === "label-del"
        ? own.filter((a) => !(a.labelId === massPick && a.sessionId == null))
        : own.some((a) => a.labelId === massPick && a.sessionId == null) ? own : [...own, { cardId: card.id, labelId: massPick, sessionId: null }];
      await jput(`/api/cards/${card.id}/labels`, { labels: keep.map((a) => ({ label_id: a.labelId, session_id: a.sessionId })) });
      state.cardLabels = state.cardLabels.filter((a) => a.cardId !== card.id).concat(keep);
      return;
    }
    case "session-add":
    case "session-del": {
      if (massPick == null) throw new Error("не выбран тест");
      const plays = playingsOf(card.id);
      const next = action.key === "session-del"
        ? plays.filter((id) => id !== massPick)
        : plays.includes(massPick) ? plays : [...plays, massPick];
      await jput(`/api/cards/${card.id}/sessions`, { session_ids: next });
      state.cardSessions = state.cardSessions.filter((p) => p.cardId !== card.id)
        .concat(next.map((sessionId) => ({ cardId: card.id, sessionId })));
      // A playing that is gone takes its scoped labels with it (ADR-0004).
      if (action.key === "session-del") {
        state.cardLabels = state.cardLabels.filter((a) => !(a.cardId === card.id && a.sessionId === massPick));
      }
      return;
    }
    case "move":
    case "copy": {
      if (!massTarget) throw new Error("не выбран список");
      await cardDetail.transferCard(card, massTarget.listId, massTarget.ctx, action.key === "move");
      return;
    }
  }
}

byId("massRun").addEventListener("click", () => { void runMass(); });
byId("massClose").addEventListener("click", closeMass);

// ---- labels ----
// The card's «Метки» and «Тесты» are two separate pickers (ADR-0004): a label is
// the author's view of the question, a Playing is where it was tested, and a
// label scoped to a Playing is what the testers thought there. Mixing them into
// one list was what made «взяли» multiply by the number of tests.

// labelLastUsage maps label id → the highest card id currently carrying it.
// Card ids grow monotonically, so the max id is a recency proxy for "last used"
// without scanning per-card timelines. Labels absent from the map were never
// used (or imported with no assignments).
function labelLastUsage(): Map<number, number> {
  const usage = new Map<number, number>();
  for (const a of state.cardLabels) {
    const prev = usage.get(a.labelId);
    if (prev === undefined || a.cardId > prev) usage.set(a.labelId, a.cardId);
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
  const named = list ? tourPicked(list) : new Set<number>();
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
  const pool = sortLabels(state.labels.filter((l) => !taken.has(l.id)));
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

// ---- «Вопросы тестировали» for one tour ----
//
// The test list used to BE this list, one per tour. A board-level Тесты panel
// can only say who tested at all, so a tour compiles its own: each session with
// how many of the tour's questions it saw. The ЧГК custom names those who tested
// MOST of a tour (they should not play it); someone who saw one or two questions
// still may, skipping what they know — so a flat list cannot serve.
interface TourTester { id: number; name: string; seen: number }

function tourCoverage(list: BoardList): { cards: BoardCard[]; rows: TourTester[] } {
  const cards = exportScope(list).cards.filter((c) => c.kind === "question");
  const seen = new Map<number, number>();
  for (const c of cards) {
    for (const sid of playingsOf(c.id)) seen.set(sid, (seen.get(sid) || 0) + 1);
  }
  const rows = [...seen.entries()]
    .map(([id, n]): TourTester => ({ id, name: sessionName(id), seen: n }))
    .sort((a, b) => b.seen - a.seen || a.name.localeCompare(b.name, "ru"));
  return { cards, rows };
}

// Which sessions were ticked last time, per tour. A personal working state on
// the way to a document, so it lives beside the other display prefs rather than
// on the server.
// A tour's Declaration lives on the board, not in this browser: the preamble
// ships with the package, so two editors preparing it see one answer. The ticks
// used to sit in localStorage, where they outlived the sessions they named.
function tourScope(list: BoardList): { listId: number | null; groupId: number | null } {
  return list.groupId != null ? { listId: null, groupId: list.groupId } : { listId: list.id, groupId: null };
}

// null = this tour has no Declaration and falls back to the custom. An empty
// array = it declared, and names nobody.
function declaredFor(list: BoardList): number[] | null {
  const s = tourScope(list);
  const rows = state.tourTesters.filter((d) => d.listId === s.listId && d.groupId === s.groupId);
  if (!rows.length) return null;
  return rows.filter((d) => d.sessionId != null).map((d) => d.sessionId as number);
}

async function declare(list: BoardList, ids: number[]): Promise<void> {
  const s = tourScope(list);
  await put("setTourTesters", `/api/boards/${boardId}/tour-testers`, {
    list_id: s.listId, group_id: s.groupId, session_ids: ids,
  });
  const rest = state.tourTesters.filter((d) => d.listId !== s.listId || d.groupId !== s.groupId);
  state.tourTesters = ids.length
    ? rest.concat(ids.map((sessionId) => ({ ...s, sessionId })))
    : rest.concat([{ ...s, sessionId: null }]);
}

// Undeclared, a tour falls back to the custom: everyone who saw MORE than half
// its questions. Shared with the card's «кроме общих тестеров» line.
function tourPicked(list: BoardList): Set<number> {
  const { cards, rows } = tourCoverage(list);
  const declared = declaredFor(list);
  return new Set(declared ?? rows.filter((r) => r.seen * 2 > cards.length).map((r) => r.id));
}

// Numbering runs over the whole export scope (a group numbers across its member
// lists) and is not always 1..n — a № directive can set a number outright.
function seenQuestions(list: BoardList): SeenQuestion[] {
  const scope = exportScope(list).cards;
  const numbers = xyChgk.numberQuestionCards(scope);
  const out: SeenQuestion[] = [];
  scope.forEach((card, i) => {
    const num = numbers[i];
    if (!num) return;
    const testers = playingsOf(card.id).flatMap((sid) => (sessionMeta(sid) || { testers: [] }).testers || []);
    if (testers.length) out.push({ num, testers });
  });
  return out;
}

function openTesterList(list: BoardList): void {
  const overlay = byId("testerListOverlay");
  const box = byId("testerList");
  const { cards, rows } = tourCoverage(list);
  const total = cards.length;
  const picked = tourPicked(list);

  const line = el("p", { class: "sess-invite" });
  const partial = el("p", { class: "sess-invite" });
  const redraw = (): void => {
    const testers: Tester[] = [];
    for (const r of rows) {
      if (!picked.has(r.id)) continue;
      const m = sessionMeta(r.id);
      if (m) testers.push(...m.testers);
    }
    const names = whoSaw(testers.length ? [{ testers } as SessionMeta] : []);
    line.textContent = names ? `Вопросы тестировали: ${names}.` : "Никто не отмечен.";
    partial.textContent = partialSeen(seenQuestions(list), new Set(testers.map((t) => (t.text || "").trim())));
    partial.hidden = !partial.textContent;
  };

  box.replaceChildren();
  if (!rows.length) box.append(el("p", { class: "label-empty", text: "Вопросы этого тура никто не тестировал." }));
  for (const r of rows) {
    const cb = el("input", { class: "input", type: "checkbox" }) as HTMLInputElement;
    cb.checked = picked.has(r.id);
    cb.addEventListener("change", () => {
      if (cb.checked) picked.add(r.id); else picked.delete(r.id);
      void declare(list, [...picked]).catch((err) => {
        byId("testerListMessage").textContent = errMsg(err);
      });
      redraw();
    });
    box.append(el("label", { class: "sess-row" },
      el("div", { class: "sess-head" }, cb, el("span", { class: "sess-title", text: r.name })),
      el("span", { class: "sess-meta", text: `${r.seen} из ${total}` })));
  }
  const copy = el("button", {
    class: "input", type: "button",
    onclick: () => {
      const text = [line.textContent, partial.textContent].filter(Boolean).join("\n");
      void cardDetail.copyPlain(text);
    },
  }, ...iconed("clipboard", "Скопировать"));
  box.append(el("div", { class: "sess-invite-box" },
    el("div", { class: "sess-invite-lines" }, line, partial), copy));
  redraw();

  byId("testerListMessage").textContent = "";
  overlay.hidden = false;
  overlayStack.open({ el: overlay, close: () => { overlay.hidden = true; } });
}

const testerListOverlay = byId("testerListOverlay");
byId("testerListClose").addEventListener("click", () => { overlayStack.pop(); });
testerListOverlay.addEventListener("pointerdown", (e) => { if (e.target === testerListOverlay) overlayStack.pop(); });

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
  overlayOpen: (node: HTMLElement, close: () => void, confirm?: () => Promise<boolean>) =>
    overlayStack.open({ el: node, close, confirm }),
  overlayClose: () => overlayStack.pop(),
  render,
});

// Every label is editable (issue #25) — there is no such thing as a test label
// whose name comes from somewhere else (ADR-0004). Like the session form, the
// editor has no per-row Сохранить: Готово commits the lot.
interface LabelRow { lbl: BoardLabel; name: HTMLInputElement; color: ColorField }
let labelRows: LabelRow[] = [];
let labelDraft: { name: HTMLInputElement; color: ColorField } | null = null;

// flushLabelsEditor writes whatever the editor is holding — renamed or
// recoloured rows first, then a name left in the create row. It throws, so the
// leave gate can keep the modal open on a failure instead of eating the edit.
async function flushLabelsEditor(): Promise<void> {
  for (const row of labelRows) {
    const name = row.name.value.trim();
    const color = row.color.value();
    // A blanked name is a slip, not a rename: a nameless label is unusable.
    if (!name || (name === row.lbl.name && color === row.lbl.color)) continue;
    await patch("patchLabel", `/api/labels/${row.lbl.id}`, {
      name_enc: await xyCrypto.encField(mustDK(), name),
      color_enc: await xyCrypto.encField(mustDK(), color),
    });
    row.lbl.name = name;
    row.lbl.color = color;
  }
  if (labelDraft && labelDraft.name.value.trim()) {
    await createLabel(labelDraft.name.value.trim(), labelDraft.color.value());
    labelDraft.name.value = "";
  }
}

function renderLabelsEditor(focusNew = false): void {
  const box = byId("labelsEditor");
  const usage = labelUsageCounts();
  box.replaceChildren();
  labelRows = [];

  // The card's add-label popup was the only way to make one, so you had to open
  // a card first — and managing labels is what this modal is for.
  const newName = el("input", { class: "input", type: "text", placeholder: "Новая метка" }) as HTMLInputElement;
  const newColor = colorField(el("div"), LABEL_COLORS[0]);
  labelDraft = { name: newName, color: newColor };
  const add = el("button", { class: "input", type: "button", text: "Добавить" });
  // Добавить is the create affordance, not a save — it commits now so you can
  // type the next one. Leaving with a name still in the box creates it too.
  const submit = async (): Promise<void> => {
    if (!newName.value.trim()) return;
    try {
      await flushLabelsEditor();
      render();
      renderLabelsEditor(true);
    } catch (err) { byId("labelsEditMessage").textContent = errMsg(err); }
  };
  add.addEventListener("click", () => { void submit(); });
  newName.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    e.preventDefault();
    void submit();
  });
  box.append(el("div", { class: "sess-row" },
    el("div", { class: "sess-head" }, newName),
    el("div", { class: "sess-actions" }, newColor.node, add)));

  if (!state.labels.length) box.append(el("p", { class: "label-empty", text: "Меток нет." }));
  for (const lbl of sortLabels(state.labels.slice())) {
    const name = el("input", { class: "input", type: "text", value: lbl.name }) as HTMLInputElement;
    const color = colorField(el("div"), lbl.color);
    const count = el("span", { class: "sess-meta", text: `${usage.get(lbl.id) || 0} карт.` });
    labelRows.push({ lbl, name, color });
    const drop = el("button", { class: "btn btn-danger", type: "button" }, icon("trash-2"));
    drop.addEventListener("click", async () => {
      if (!confirm(`Удалить метку «${lbl.name}»? Она исчезнет со всех карточек.`)) return;
      try {
        // Commit the other rows first — this re-renders, and their edits would
        // go with the old DOM.
        await flushLabelsEditor();
        await del("deleteLabel", `/api/labels/${lbl.id}`);
        state.labels = state.labels.filter((l) => l.id !== lbl.id);
        state.cardLabels = state.cardLabels.filter((a) => a.labelId !== lbl.id);
        render();
        renderLabelsEditor();
      } catch (err) { byId("labelsEditMessage").textContent = errMsg(err); }
    });
    box.append(el("div", { class: "sess-row" },
      el("div", { class: "sess-head" }, name, count),
      el("div", { class: "sess-actions" }, color.node, drop)));
  }
  if (focusNew) newName.focus();
}

async function leaveLabelsEditor(): Promise<boolean> {
  try {
    await flushLabelsEditor();
    render();
    return true;
  } catch (err) {
    byId("labelsEditMessage").textContent = errMsg(err);
    return false;
  }
}

const labelsEditOverlay = byId("labelsEditOverlay");

function openLabelsEditor(): void {
  renderLabelsEditor();
  byId("labelsEditMessage").textContent = "";
  labelsEditOverlay.hidden = false;
  overlayStack.open({
    el: labelsEditOverlay,
    close: () => { labelsEditOverlay.hidden = true; labelRows = []; labelDraft = null; },
    confirm: leaveLabelsEditor,
  });
}

// labelUsageCounts: label id → how many live cards carry it, either way.
function labelUsageCounts(): Map<number, number> {
  const counts = new Map<number, number>();
  const live = new Set(state.cards.map((c) => c.id));
  for (const a of state.cardLabels) {
    if (!live.has(a.cardId)) continue;
    counts.set(a.labelId, (counts.get(a.labelId) || 0) + 1);
  }
  return counts;
}

byId("labelsEditClose").addEventListener("click", () => { overlayStack.pop(); });
labelsEditOverlay.addEventListener("pointerdown", (e) => { if (e.target === labelsEditOverlay) overlayStack.pop(); });

async function createLabel(name: string, color: string): Promise<BoardLabel> {
  const res = await create("createLabel", `/api/boards/${boardId}/labels`, {
    name_enc: await xyCrypto.encField(mustDK(), name),
    color_enc: await xyCrypto.encField(mustDK(), color),
  });
  const lbl: BoardLabel = { id: res.id as number, name, color };
  state.labels.push(lbl);
  return lbl;
}

// NB: `newLabelForm` (the retained node), not getElementById — the form is
// detached from the document above and lives inside the popup while it is open.
newLabelForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = byId<HTMLInputElement>("newLabelName").value.trim();
  if (!name) return;
  try {
    const lbl = await createLabel(name, newLabelColor.value());
    byId<HTMLInputElement>("newLabelName").value = "";
    const card = state.cards.find((c) => c.id === cardDetail.openCardId());
    // The form is reachable only from inside the add-label popup, so naming a
    // label there means you want it ON this card — assign it instead of making
    // the user reopen the popup to pick what they just typed.
    if (card) await setLabel(card, lbl, null, true);
  } catch (err) { byId("cardMessage").textContent = errMsg(err); }
});

void unlock.boot();
