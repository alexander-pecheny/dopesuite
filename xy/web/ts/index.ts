// index.ts — board list + create-board flow.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyFind } from "./find.js";
import { xySearchIndex } from "./searchindex.js";
import type { BoardIndex, Hit } from "./searchindex.js";
import { xySync } from "./sync.js";
import type { SyncStatus } from "./sync.js";
import { iconed } from "./icons_gen.js";

const { fetchJSON, jpost, el, escapeHtml } = xyApp;

interface BoardListItem {
  id: number;
  name: string;
  name_enc: string;
  role: string;
  schema_version: number;
  unread?: boolean;
}

function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`page is missing #${id}`);
  return node as T;
}

const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

const statusNode = byId("status");
const listNode = byId("boardList");
const hitNode = byId("hitList");
const commentNode = byId("commentList");
const searchBox = byId<HTMLInputElement>("boardSearch");
const cardNote = byId("cardNote");
const commentNote = byId("commentNote");
const message = byId("message");
const overlay = byId("createOverlay");
const createForm = byId<HTMLFormElement>("createForm");
const createMessage = byId("createMessage");

let lastOp: "saved" | "saving" | "error" = "saved";
let syncState: Pick<SyncStatus, "online" | "pending" | "syncing"> = { online: true, pending: 0, syncing: false };
function refreshBadge(): void {
  let state: string, title: string;
  if (!syncState.online) { state = "offline"; title = syncState.pending ? `Офлайн · ${syncState.pending} изм. ждут отправки` : "Офлайн"; }
  else if (syncState.syncing || syncState.pending > 0) { state = "pending"; title = syncState.pending ? `Синхронизация · осталось ${syncState.pending}` : "Синхронизация…"; }
  else if (lastOp === "error") { state = "error"; title = "Ошибка"; }
  else if (lastOp === "saving") { state = "saving"; title = "Подождите"; }
  else { state = "saved"; title = "Готово"; }
  statusNode.dataset.state = state;
  statusNode.title = title;
}
function setStatus(state: "saved" | "saving" | "error"): void { lastOp = state; refreshBadge(); }

async function boot(): Promise<void> {
  if (!(await xyApp.requireLogin())) return;
  xySync.start();
  xySync.onStatus((st) => { syncState = st; refreshBadge(); });
  await refresh();
}

// Прогрев lives in the site-wide burger, next to nothing else on this page: it
// is a rare, deliberate act, and it is what makes search cover boards this
// device has not opened.
window.dopeMenu?.setExtras([{
  icon: "cloud-download",
  label: "Прогрев поиска",
  title: "Скачать все доски, от которых есть ключ, чтобы искать по ним и офлайн",
  onClick: () => { void prewarm(); },
}]);

async function refresh(): Promise<void> {
  setStatus("saving");
  try {
    const boards = (await fetchJSON("/api/boards")) as BoardListItem[];
    await xySync.putBoardList(boards);
    allBoards = boards;
    // The index is assembled FROM this list, so a search that ran before it
    // arrived cached an empty one. Drop it and re-answer whatever is in the box.
    indexes = null;
    renderBoards(boards);
    setStatus("saved");
    if (searchBox.value.trim()) await runSearch(searchBox.value);
  } catch (e) {
    // Offline (or the server is unreachable): fall back to the cached board list.
    const cached = await xySync.getBoardList().catch(() => null);
    if (cached) {
      allBoards = cached as BoardListItem[];
      indexes = null;
      renderBoards(allBoards);
      setStatus("saved");
      if (searchBox.value.trim()) await runSearch(searchBox.value);
    } else {
      message.textContent = errMsg(e);
      setStatus("error");
    }
  }
}

// ---- search ----
// The grid above filters to boards this query can NAME; the grid below shows the
// cards it can quote. Every board therefore appears in exactly one place, and
// the top grid keeps meaning one thing.
const HIT_LIMIT = 50;

let allBoards: BoardListItem[] = [];
let indexes: Array<{ board: number; index: BoardIndex }> | null = null;
let searching = false;

// loadIndexes reads every Search Index this device holds, in board-list order
// (last visited first), and takes each board's name from the list rather than
// the index — the list is authoritative and a rename may not have reached the
// index yet.
async function loadIndexes(): Promise<Array<{ board: number; index: BoardIndex }>> {
  if (indexes) return indexes;
  // Nothing to assemble yet — and nothing to cache either, or a query typed
  // while /api/boards was still in flight would leave the page searchless.
  if (!allBoards.length) return [];
  const held = new Map((await xySearchIndex.all()).map((r) => [r.board, r.index]));
  indexes = allBoards.flatMap((b) => {
    const index = held.get(b.id);
    return index ? [{ board: b.id, index: { ...index, name: b.name || index.name } }] : [];
  });
  return indexes;
}

async function runSearch(query: string): Promise<void> {
  const q = query.trim();
  document.body.classList.toggle("searching", !!q);
  hitNode.hidden = !q;
  commentNode.hidden = !q;
  if (!q) {
    hitNode.replaceChildren();
    commentNode.replaceChildren();
    cardNote.textContent = "";
    commentNote.textContent = "";
    listNode.hidden = false;
    renderBoards(allBoards);
    return;
  }
  // A board matches by name without any key — names are plaintext (v2). A legacy
  // board whose name is still encrypted matches nothing until it is migrated.
  const named = allBoards.filter((b) => xyFind.searchSpans(b.name || "", q).length > 0);
  renderBoards(named);
  // An empty grid is not a result — it is padding with a message in it. Each
  // section shows only when it has something, and its count says the rest.
  listNode.hidden = !named.length;
  const held = await loadIndexes();
  const res = xySearchIndex.search(held, q, HIT_LIMIT);
  renderHits(hitNode, res.questions);
  renderHits(commentNode, res.comments);
  cardNote.textContent = note("Вопросы", res.questionTotal, res.questions.length, held.length);
  commentNote.textContent = note("Комментарии", res.commentTotal, res.comments.length, held.length);
  hitNode.hidden = !res.questions.length;
  commentNote.hidden = !res.commentTotal;
  commentNode.hidden = !res.comments.length;
}

function note(what: string, total: number, shown: number, boards: number): string {
  if (!boards) {
    return "Ни одна доска не скачана на это устройство — «Прогрев поиска» в меню ☰ сделает их искомыми.";
  }
  if (!total) return `${what}: ничего не найдено.`;
  if (total > shown) return `${what}: ${total}, показаны первые ${shown}.`;
  return `${what}: ${total}.`;
}

function renderHits(into: HTMLElement, hits: Hit[]): void {
  into.replaceChildren(...hits.map((h) => {
    const href = `/board/${h.board}?card=${h.card}` + (h.comment ? `&comment=${h.comment}` : "");
    const where = h.list ? `${h.boardName} · ${h.list}` : h.boardName;
    // Every match the window reaches is marked — one highlighted and its
    // neighbour left plain reads as a bug, because it is one.
    const parts: Array<Node | string> = [];
    let at = 0;
    for (const m of h.snippet.marks) {
      parts.push(h.snippet.text.slice(at, m.start), el("mark", { text: h.snippet.text.slice(m.start, m.end) }));
      at = m.end;
    }
    parts.push(h.snippet.text.slice(at));
    const snip = el("span", { class: "hit-snippet" }, ...parts);
    if (h.more) snip.append(el("span", { class: "hit-more", text: ` +${h.more}` }));
    return el("a", { class: "board-card hit-card", href },
      el("span", { class: "hit-title", text: h.title }),
      el("span", { class: "hit-where" }, ...(h.comment ? iconed("message-circle", where) : [where])),
      snip);
  }));
}

let searchTimer = 0;
searchBox.addEventListener("input", () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { void runSearch(searchBox.value); }, 120);
});

// прогрев: fill the Mirror and the Search Index for every board this device can
// unlock, so search stops depending on which boards happened to be opened.
async function prewarm(): Promise<void> {
  if (searching) return;
  if (!xySync.requireOnline("Прогрев доступен только онлайн.", cardNote)) return;
  searching = true;
  try {
    const indexed = await xySearchIndex.prewarm(
      allBoards.map((b) => ({ id: b.id, name: b.name })),
      (done, total) => { cardNote.textContent = `Прогрев: ${done} из ${total}…`; },
    );
    indexes = null;
    cardNote.textContent = `Прогрев закончен: досок скачано ${indexed} из ${allBoards.length}.` +
      (indexed < allBoards.length ? " Остальные заперты — их пароль на этом устройстве не сохранён." : "");
    if (searchBox.value.trim()) await runSearch(searchBox.value);
  } finally {
    searching = false;
  }
}

function renderBoards(boards: BoardListItem[]): void {
  listNode.replaceChildren();
  if (!boards.length) {
    listNode.append(el("p", { class: "empty", text: searchBox.value.trim() ? "Досок с таким названием нет." : "Пока нет досок. Нажмите + чтобы создать." }));
    return;
  }
  // Boards arrive already ordered by the caller's last visit (server-side).
  for (const b of boards) {
    // Migrated (v2) boards carry a plaintext name — shown with no key needed. Legacy
    // (v1) boards still need the cached DK, so start with a locked placeholder.
    const migrated = b.schema_version >= 2;
    const card = el("a", { class: "board-card", href: `/board/${b.id}` },
      el("span", { class: "board-card-name-wrap" },
        el("span", { class: "board-card-name" },
          ...(migrated ? [b.name] : iconed("lock", "доска #" + b.id)))),
      el("span", { class: "board-card-role", text: b.role === "owner" ? "владелец" : "редактор" }),
    );
    if (b.unread) {
      card.append(el("span", { class: "unread-dot unread-dot-corner board-card-unread", title: "Есть непрочитанные изменения" }));
    }
    if (!migrated) {
      // Decrypt the name lazily if we have the cached key, and — since we now hold
      // the plaintext — opportunistically migrate the board off name_enc.
      decryptName(b).then((name) => {
        if (!name) return;
        setCardName(card, name);
        migrateName(b.id, name);
      });
    } else {
      // The name is readable without a key, but opening the board still needs its
      // DK — mark boards that will ask for the passphrase.
      xyCrypto.loadCachedDK(b.id).then((dk) => {
        if (!dk) setCardName(card, b.name, true);
      }).catch(() => {});
    }
    listNode.append(card);
  }
  measureNames();
}

// `locked` marks a board whose name is readable but whose content still needs
// the passphrase — the lock rides ahead of the name.
function setCardName(card: HTMLElement, text: string, locked = false): void {
  const node = card.querySelector(".board-card-name")!;
  if (locked) node.replaceChildren(...iconed("lock", text));
  else node.textContent = text;
  measureNames();
}
// Flag every card whose one-line title overflows, so the CSS fade turns on only
// there (dope's -truncated flag) — and so hover knows which cards get a tooltip.
function measureNames(): void {
  requestAnimationFrame(() => {
    for (const card of listNode.querySelectorAll(".board-card")) {
      const name = card.querySelector(".board-card-name")!;
      card.classList.toggle("board-card-name-truncated", name.scrollWidth > name.clientWidth + 1);
    }
  });
}
let measureRaf = false;
window.addEventListener("resize", () => {
  hideTip();
  if (measureRaf) return;
  measureRaf = true;
  requestAnimationFrame(() => { measureRaf = false; measureNames(); });
});

// The full name floats in one shared node appended to <body> (position:fixed), so
// the grid's scroll clip and neighbouring tiles never crop it — dope's floating
// popover, pared to xy's single trigger. Shown below the title, flipped above when
// it would fall off the bottom, clamped into the viewport.
let tipEl: HTMLElement | null = null, tipCard: HTMLElement | null = null;
function showTip(card: HTMLElement): void {
  if (tipCard === card) return;
  tipCard = card;
  const name = card.querySelector(".board-card-name")!;
  if (!tipEl) { tipEl = el("div", { class: "popover board-card-name-popover" }); document.body.append(tipEl); }
  tipEl.textContent = name.textContent;
  tipEl.classList.add("visible");
  const r = name.getBoundingClientRect();
  const left = Math.max(8, Math.min(r.left, window.innerWidth - tipEl.offsetWidth - 8));
  let top = r.bottom + 2;
  if (top + tipEl.offsetHeight > window.innerHeight - 8) top = r.top - tipEl.offsetHeight - 2;
  tipEl.style.left = `${left}px`;
  tipEl.style.top = `${top}px`;
}
function hideTip(): void { if (tipEl) tipEl.classList.remove("visible"); tipCard = null; }

listNode.addEventListener("pointerover", (e) => {
  const card = e.target instanceof Element ? e.target.closest<HTMLElement>(".board-card") : null;
  if (card && card.classList.contains("board-card-name-truncated")) showTip(card);
});
listNode.addEventListener("pointerout", (e) => {
  const card = e.target instanceof Element ? e.target.closest<HTMLElement>(".board-card") : null;
  if (card && !card.contains(e.relatedTarget instanceof Node ? e.relatedTarget : null)) hideTip();
});
listNode.addEventListener("focusin", (e) => {
  const card = e.target instanceof Element ? e.target.closest<HTMLElement>(".board-card") : null;
  if (card && card.classList.contains("board-card-name-truncated")) showTip(card);
});
listNode.addEventListener("focusout", hideTip);
window.addEventListener("scroll", hideTip, true);

async function decryptName(b: BoardListItem): Promise<string | null> {
  try {
    const dk = await xyCrypto.loadCachedDK(b.id);
    if (!dk) return null;
    return await xyCrypto.decField(dk, b.name_enc);
  } catch (_) {
    return null;
  }
}

// Backfill a legacy board's plaintext name once we've decrypted it. Best-effort and
// online-only; the server ignores it if the board is already migrated (no clobber).
async function migrateName(id: number, name: string): Promise<void> {
  if (!xySync.isOnline()) return;
  try { await jpost(`/api/boards/${id}/migrate-name`, { name }); } catch (_) {}
}

// ---- create board ----
byId("newBoardBtn").addEventListener("click", () => {
  createMessage.textContent = "";
  createForm.reset();
  overlay.hidden = false;
  byId("boardName").focus();
});
// «🎲»: fill the field with a fresh xkcd-style passphrase and copy it, so the one
// place it's ever shown in the clear (creation) doubles as the moment you stash
// it somewhere safe.
const boardPass = byId<HTMLInputElement>("boardPass");
xyApp.wireGenPassphrase(byId("genPassBtn"), boardPass, xyCrypto.generatePassphrase);

byId("createCancel").addEventListener("click", () => { overlay.hidden = true; });
overlay.addEventListener("pointerdown", (e) => { if (e.target === overlay) overlay.hidden = true; });

createForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  createMessage.textContent = "";
  const name = byId<HTMLInputElement>("boardName").value.trim();
  const pass = boardPass.value;
  if (!name || !pass) return;
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) { createMessage.textContent = passErr; return; }
  if (!xySync.requireOnline("Создание доски доступно только онлайн.", createMessage)) return;
  try {
    // The passphrase still mints the board's data key (lists/cards stay encrypted);
    // only the name travels in the clear now.
    const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
    const res = (await jpost("/api/boards", { ...keymeta, name })) as { id: number };
    await xyCrypto.cacheDK(res.id, dk);
    window.location.href = `/board/${res.id}`;
  } catch (err) {
    createMessage.textContent = errMsg(err);
  }
});

boot();
