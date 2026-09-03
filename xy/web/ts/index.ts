// index.ts — board list + create-board flow.
import { xyApp } from "./app.js";
import { modal } from "./modal.js";
import { xyCrypto } from "./crypto.js";
import { xyFind } from "./find.js";
import { xySearchIndex } from "./searchindex.js";
import type { BoardIndex, Hit } from "./searchindex.js";
import { xySync } from "./sync.js";
import { stampPassCheck } from "./passcheck.js";
import { iconed } from "./icons_gen.js";
import S from "./i18nstrings_ru_gen.js";

const { fetchJSON, jpost, el, escapeHtml } = xyApp;

interface BoardListItem {
  id: number;
  name: string;
  name_enc: string;
  role: string;
  schema_version: number;
  unread?: boolean;
  unread_mentions?: boolean;
}

const { byId, errMsg } = xyApp;

const statusNode = byId("status");
const listNode = byId("boardList");
const hitNode = byId("hitList");
const commentNode = byId("commentList");
const searchBox = byId<HTMLInputElement>("boardSearch");
const cardNote = byId("cardNote");
const commentNote = byId("commentNote");
const moreQuestions = byId("moreQuestions");
const moreComments = byId("moreComments");
const message = byId("message");
const createModal = modal("create");
const createForm = byId<HTMLFormElement>("createForm");
const createMessage = byId("createMessage");

const badge = xyApp.syncBadge(statusNode);
const setStatus = badge.set;

async function boot(): Promise<void> {
  if (!(await xyApp.requireLogin())) return;
  xySync.start();
  xySync.onStatus(badge.onSync);
  await refresh();
}

// Prewarm lives in the site-wide burger, next to nothing else on this page: it
// is a rare, deliberate act, and it is what makes search cover boards this
// device has not opened.
window.dopeMenu?.setExtras([{
  icon: "cloud-download",
  label: S.chrome.prewarm.menuLabel(),
  title: S.chrome.prewarm.menuTitle(),
  onClick: () => { void prewarm(); },
}]);

// show replaces the board list, unless the list is the one already on screen —
// the usual case when the server confirms the cache — so nothing blinks. The
// search index is assembled FROM this list, so a search that ran before it
// arrived cached an empty one: drop it and re-answer whatever is in the box.
let shown: string | null = null;
async function show(boards: BoardListItem[]): Promise<void> {
  const key = JSON.stringify(boards);
  if (key === shown) return;
  shown = key;
  allBoards = boards;
  indexes = null;
  await renderBoards(boards);
  if (searchBox.value.trim()) await runSearch(searchBox.value);
}

// refresh paints the cached list first — this device has seen the boards before,
// so the page need not wait for the network to show them — then swaps in the
// server's answer (fresh order, fresh unread dots).
async function refresh(): Promise<void> {
  setStatus("saving");
  const cached = (await xySync.getBoardList().catch(() => null)) as BoardListItem[] | null;
  if (cached) await show(cached);
  try {
    const boards = (await fetchJSON("/api/boards")) as BoardListItem[];
    await xySync.putBoardList(boards);
    await show(boards);
    setStatus("saved");
  } catch (e) {
    // Offline (or the server is unreachable): the cached list stays.
    if (cached) { setStatus("saved"); return; }
    message.textContent = errMsg(e);
    setStatus("error");
  }
}

// ---- search ----
// The grid above filters to boards this query can NAME; the grid below shows the
// cards it can quote. Every board therefore appears in exactly one place, and
// the top grid keeps meaning one thing.
// One screenful of results at a time, with a button for the next — a query that
// names four hundred questions wants narrowing, but only the first 50 shown with no
// way to see the fifty-first is just a wall.
const HIT_PAGE = 50;
let shownQuestions = HIT_PAGE;
let shownComments = HIT_PAGE;

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

// setNote writes a count line and takes it off the page when there is nothing to
// count: an empty span is still a line box, and two of them under the board grid
// cost it a row.
function setNote(node: HTMLElement, text: string): void {
  node.textContent = text;
  node.hidden = !text;
}

async function runSearch(query: string, keepShown = false): Promise<void> {
  const q = query.trim();
  // A new query starts from the first page again; Show more does not.
  if (!keepShown) { shownQuestions = HIT_PAGE; shownComments = HIT_PAGE; }
  document.body.classList.toggle("searching", !!q);
  hitNode.hidden = !q;
  commentNode.hidden = !q;
  if (!q) {
    hitNode.replaceChildren();
    commentNode.replaceChildren();
    setNote(cardNote, "");
    setNote(commentNote, "");
    moreQuestions.hidden = true;
    moreComments.hidden = true;
    listNode.hidden = false;
    await renderBoards(allBoards);
    return;
  }
  // A board matches by name without any key — names are plaintext (v2). A legacy
  // board whose name is still encrypted matches nothing until it is migrated.
  const named = allBoards.filter((b) => xyFind.searchSpans(b.name || "", q).length > 0);
  await renderBoards(named);
  // An empty grid is not a result — it is padding with a message in it. Each
  // section shows only when it has something, and its count says the rest.
  listNode.hidden = !named.length;
  const held = await loadIndexes();
  const res = xySearchIndex.search(held, q, shownQuestions, shownComments);
  renderHits(hitNode, res.questions);
  renderHits(commentNode, res.comments);
  moreQuestions.hidden = res.questionTotal <= res.questions.length;
  moreComments.hidden = res.commentTotal <= res.comments.length;
  setNote(cardNote, note(S.chrome.search.questions(), res.questionTotal, res.questions.length, held.length));
  setNote(commentNote, res.commentTotal ? note(S.chrome.search.comments(), res.commentTotal, res.comments.length, held.length) : "");
  hitNode.hidden = !res.questions.length;
  commentNode.hidden = !res.comments.length;
}

function note(what: string, total: number, shown: number, boards: number): string {
  if (!boards) {
    return S.chrome.search.noteNoBoards();
  }
  if (!total) return S.chrome.search.noteNone(what);
  if (total > shown) return S.chrome.search.notePartial(what, String(total), String(shown));
  return S.chrome.search.noteAll(what, String(total));
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

moreQuestions.addEventListener("click", () => {
  shownQuestions += HIT_PAGE;
  void runSearch(searchBox.value, true);
});
moreComments.addEventListener("click", () => {
  shownComments += HIT_PAGE;
  void runSearch(searchBox.value, true);
});

let searchTimer = 0;
searchBox.addEventListener("input", () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { void runSearch(searchBox.value); }, 120);
});

// Prewarm: fill the Mirror and the Search Index for every board this device can
// unlock, so search stops depending on which boards happened to be opened.
async function prewarm(): Promise<void> {
  if (searching) return;
  if (!xySync.requireOnline(S.chrome.prewarm.offline(), cardNote)) return;
  searching = true;
  try {
    const indexed = await xySearchIndex.prewarm(
      allBoards.map((b) => ({ id: b.id, name: b.name })),
      (done, total) => { setNote(cardNote, S.chrome.prewarm.progress(String(done), String(total))); },
    );
    indexes = null;
    setNote(cardNote, S.chrome.prewarm.done(String(indexed), String(allBoards.length)) +
      (indexed < allBoards.length ? S.chrome.prewarm.lockedNote() : ""));
    if (searchBox.value.trim()) await runSearch(searchBox.value);
  } finally {
    searching = false;
  }
}

// A card is painted once, lock included: the DK lookup happens before the grid
// is touched, so a repaint never shows a name unlocked and then locks it.
async function renderBoards(boards: BoardListItem[]): Promise<void> {
  const locked = await Promise.all(boards.map((b) =>
    b.schema_version >= 2 ? xyCrypto.loadCachedDK(b.id).then((dk) => !dk, () => false) : true));
  listNode.replaceChildren();
  if (!boards.length) {
    listNode.append(el("p", { class: "empty", text: searchBox.value.trim() ? S.chrome.home.emptyNamed() : S.chrome.home.emptyAll() }));
    return;
  }
  // Boards arrive already ordered by the caller's last visit (server-side).
  boards.forEach((b, i) => {
    // Migrated (v2) boards carry a plaintext name — shown with no key needed; the
    // lock marks a board that will still ask for the passphrase. Legacy (v1)
    // boards need the cached DK for the name itself, so start with a placeholder.
    const migrated = b.schema_version >= 2;
    const name = migrated ? b.name : S.chrome.home.boardLockedName(String(b.id));
    const card = el("a", { class: "board-card", href: `/board/${b.id}` },
      el("span", { class: "board-card-name-wrap" },
        el("span", { class: "board-card-name" }, ...(locked[i] ? iconed("lock", name) : [name]))),
      el("span", { class: "board-card-role", text: b.role === "owner" ? S.chrome.home.roleOwner() : S.chrome.home.roleEditor() }),
    );
    if (b.unread) {
      const mention = b.unread_mentions ? " unread-dot-mention" : "";
      const title = b.unread_mentions ? S.chrome.home.unreadMentionTitle() : S.chrome.home.unreadTitle();
      card.append(el("span", { class: "unread-dot unread-dot-corner board-card-unread" + mention, title }));
    }
    if (!migrated) {
      // Decrypt the name lazily if we have the cached key, and — since we now hold
      // the plaintext — opportunistically migrate the board off name_enc.
      decryptName(b).then((name) => {
        if (!name) return;
        setCardName(card, name);
        migrateName(b.id, name);
      });
    }
    listNode.append(card);
  });
  measureNames();
}

function setCardName(card: HTMLElement, text: string): void {
  card.querySelector(".board-card-name")!.textContent = text;
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
const boardPass = byId<HTMLInputElement>("boardPass");
const passSetup = xyApp.wirePassphraseSetup({
  input: boardPass,
  dice: byId("genPassBtn"),
  copied: byId("createPassCopied"),
  saved: byId<HTMLInputElement>("createPassSaved"),
  submit: byId<HTMLButtonElement>("createSubmit"),
}, xyCrypto.generatePassphrase);

// The passphrase is rolled and copied inside the click, not on submit: the
// clipboard only answers to a user gesture, and this is the one moment the words
// are ever shown in the clear.
byId("newBoardBtn").addEventListener("click", () => {
  createForm.reset();
  passSetup.reset();
  void passSetup.roll(true);
  createModal.open();
  byId("boardName").focus();
});

createForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  createMessage.textContent = "";
  const name = byId<HTMLInputElement>("boardName").value.trim();
  const pass = boardPass.value;
  if (!name || !pass) return;
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) { createMessage.textContent = passErr; return; }
  if (!xySync.requireOnline(S.chrome.home.createOffline(), createMessage)) return;
  try {
    // The passphrase still mints the board's data key (lists/cards stay encrypted);
    // only the name travels in the clear now.
    const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
    const res = (await jpost("/api/boards", { ...keymeta, name })) as { id: number };
    await xyCrypto.cacheDK(res.id, dk);
    stampPassCheck(res.id); // the words are known today; the check is a month off
    window.location.href = `/board/${res.id}`;
  } catch (err) {
    createMessage.textContent = errMsg(err);
  }
});

boot();
