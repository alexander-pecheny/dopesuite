// index.js — board list + create-board flow.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";

const { fetchJSON, jpost, el, escapeHtml } = xyApp;

const statusNode = document.getElementById("status");
const listNode = document.getElementById("boardList");
const message = document.getElementById("message");
const overlay = document.getElementById("createOverlay");
const createForm = document.getElementById("createForm");
const createMessage = document.getElementById("createMessage");

function setStatus(state) {
  const labels = { saved: "Готово", saving: "Подождите", error: "Ошибка" };
  statusNode.dataset.state = state;
  statusNode.title = labels[state] || labels.saved;
}

async function boot() {
  if (!(await xyApp.requireLogin())) return;
  await refresh();
}

async function refresh() {
  setStatus("saving");
  try {
    const boards = await fetchJSON("/api/boards");
    renderBoards(boards);
    setStatus("saved");
  } catch (e) {
    message.textContent = e.message;
    setStatus("error");
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
