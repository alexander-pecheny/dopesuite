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
    listNode.append(el("p", { class: "auth-hint", text: "Пока нет досок. Нажмите + чтобы создать." }));
    return;
  }
  for (const b of boards) {
    const card = el("a", { class: "board-card", href: `/board/${b.id}` },
      el("span", { class: "board-card-name", text: "🔒 доска #" + b.id }),
      el("span", { class: "board-card-role", text: b.role === "owner" ? "владелец" : "редактор" }),
    );
    // Decrypt the name lazily if we have the cached key.
    decryptName(b).then((name) => {
      if (name) card.querySelector(".board-card-name").textContent = name;
    });
    listNode.append(card);
  }
}

async function decryptName(b) {
  try {
    const dk = await xyCrypto.loadCachedDK(b.id);
    if (!dk) return null;
    return await xyCrypto.decField(dk, b.name_enc);
  } catch (_) {
    return null;
  }
}

// ---- create board ----
document.getElementById("newBoardBtn").addEventListener("click", () => {
  createMessage.textContent = "";
  createForm.reset();
  overlay.hidden = false;
  document.getElementById("boardName").focus();
});
document.getElementById("createCancel").addEventListener("click", () => { overlay.hidden = true; });
overlay.addEventListener("pointerdown", (e) => { if (e.target === overlay) overlay.hidden = true; });

createForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  createMessage.textContent = "";
  const name = document.getElementById("boardName").value.trim();
  const pass = document.getElementById("boardPass").value;
  if (!name || !pass) return;
  if (!xySync.isOnline()) { createMessage.textContent = "Создание доски доступно только онлайн."; return; }
  try {
    const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
    const nameEnc = await xyCrypto.encField(dk, name);
    const res = await jpost("/api/boards", { ...keymeta, name_enc: nameEnc });
    await xyCrypto.cacheDK(res.id, dk);
    window.location.href = `/board/${res.id}`;
  } catch (err) {
    createMessage.textContent = err.message;
  }
});

boot();
