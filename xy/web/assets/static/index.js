// index.js — board list + create-board flow.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";

const { fetchJSON, jpost, el, escapeHtml } = xyApp;

const statusNode = document.getElementById("status");
const listNode = document.getElementById("boardList");
const message = document.getElementById("message");
const overlay = document.getElementById("createOverlay");
const createForm = document.getElementById("createForm");
const createMessage = document.getElementById("createMessage");

let lastOp = "saved";
let syncState = { online: true, pending: 0, syncing: false };
function refreshBadge() {
  let state, title;
  if (!syncState.online) { state = "offline"; title = syncState.pending ? `Офлайн · ${syncState.pending} изм. ждут отправки` : "Офлайн"; }
  else if (syncState.syncing || syncState.pending > 0) { state = "pending"; title = syncState.pending ? `Синхронизация · осталось ${syncState.pending}` : "Синхронизация…"; }
  else if (lastOp === "error") { state = "error"; title = "Ошибка"; }
  else if (lastOp === "saving") { state = "saving"; title = "Подождите"; }
  else { state = "saved"; title = "Готово"; }
  statusNode.dataset.state = state;
  statusNode.title = title;
}
function setStatus(state) { lastOp = state; refreshBadge(); }

async function boot() {
  if (!(await xyApp.requireLogin())) return;
  xySync.start();
  xySync.onStatus((st) => { syncState = st; refreshBadge(); });
  await refresh();
}

async function refresh() {
  setStatus("saving");
  try {
    const boards = await fetchJSON("/api/boards");
    await xySync.putBoardList(boards);
    renderBoards(boards);
    setStatus("saved");
  } catch (e) {
    // Offline (or the server is unreachable): fall back to the cached board list.
    const cached = await xySync.getBoardList().catch(() => null);
    if (cached) {
      renderBoards(cached);
      setStatus("saved");
    } else {
      message.textContent = e.message;
      setStatus("error");
    }
  }
}

function renderBoards(boards) {
  listNode.replaceChildren();
  if (!boards.length) {
    listNode.append(el("p", { class: "empty", text: "Пока нет досок. Нажмите + чтобы создать." }));
    return;
  }
  // Boards arrive already ordered by the caller's last visit (server-side).
  for (const b of boards) {
    // Migrated (v2) boards carry a plaintext name — shown with no key needed. Legacy
    // (v1) boards still need the cached DK, so start with a 🔒 placeholder.
    const migrated = b.schema_version >= 2;
    const title = migrated ? b.name : "🔒 доска #" + b.id;
    const card = el("a", { class: "board-card", href: `/board/${b.id}` },
      el("span", { class: "board-card-name-wrap" },
        el("span", { class: "board-card-name", text: title })),
      el("span", { class: "board-card-role", text: b.role === "owner" ? "владелец" : "редактор" }),
      el("span", { class: "popover board-card-name-popover", text: title }),
    );
    if (b.unread) {
      card.classList.add("has-unread");
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
        if (!dk) setCardName(card, "🔒 " + b.name);
      }).catch(() => {});
    }
    listNode.append(card);
  }
  measureNames();
}

// A long title clamps to two lines with a fade; only when it actually overflows do
// we flag the card so the fade + full-name popover switch on (dope's -truncated flag).
function setCardName(card, text) {
  card.querySelector(".board-card-name").textContent = text;
  card.querySelector(".board-card-name-popover").textContent = text;
  measureNames();
}
function measureNames() {
  requestAnimationFrame(() => {
    for (const card of listNode.querySelectorAll(".board-card")) {
      const name = card.querySelector(".board-card-name");
      card.classList.toggle("board-card-name-truncated", name.scrollHeight > name.clientHeight + 1);
    }
  });
}
let measureRaf = false;
window.addEventListener("resize", () => {
  if (measureRaf) return;
  measureRaf = true;
  requestAnimationFrame(() => { measureRaf = false; measureNames(); });
});

async function decryptName(b) {
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
async function migrateName(id, name) {
  if (!xySync.isOnline()) return;
  try { await jpost(`/api/boards/${id}/migrate-name`, { name }); } catch (_) {}
}

// ---- create board ----
xyApp.swapPlusIcon(document.getElementById("newBoardBtn")); // emoji ➕ → SVG plus
document.getElementById("newBoardBtn").addEventListener("click", () => {
  createMessage.textContent = "";
  createForm.reset();
  overlay.hidden = false;
  document.getElementById("boardName").focus();
});
// «🎲»: fill the field with a fresh xkcd-style passphrase and copy it, so the one
// place it's ever shown in the clear (creation) doubles as the moment you stash
// it somewhere safe.
const boardPass = document.getElementById("boardPass");
xyApp.wireGenPassphrase(document.getElementById("genPassBtn"), boardPass, xyCrypto.generatePassphrase);

document.getElementById("createCancel").addEventListener("click", () => { overlay.hidden = true; });
overlay.addEventListener("pointerdown", (e) => { if (e.target === overlay) overlay.hidden = true; });

createForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  createMessage.textContent = "";
  const name = document.getElementById("boardName").value.trim();
  const pass = boardPass.value;
  if (!name || !pass) return;
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) { createMessage.textContent = passErr; return; }
  if (!xySync.isOnline()) { createMessage.textContent = "Создание доски доступно только онлайн."; return; }
  try {
    // The passphrase still mints the board's data key (lists/cards stay encrypted);
    // only the name travels in the clear now.
    const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
    const res = await jpost("/api/boards", { ...keymeta, name });
    await xyCrypto.cacheDK(res.id, dk);
    window.location.href = `/board/${res.id}`;
  } catch (err) {
    createMessage.textContent = err.message;
  }
});

boot();
