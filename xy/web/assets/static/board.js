// board.js — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { xyApp } from "./app.js";
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

const state = { role: "editor", name: "", lists: [], cards: [], labels: [], cardLabels: {} };
let dk = null;

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

// "Forget board password" lives in the burger (☰) menu — rarely needed, so it
// doesn't warrant a header button. dopeMenu.setExtras renders it as an action.
window.dopeMenu?.setExtras([{
  label: "🔒 Забыть пароль доски",
  title: "Забыть пароль доски на этом устройстве",
  onClick: async () => {
    await xyCrypto.forgetDK(boardId);
    location.reload();
  },
}]);

// ---- load + decrypt snapshot ----
// Source of truth: when online with an empty outbox, fetch the authoritative
// snapshot and refresh the mirror. With local edits queued (or offline), render
// the mirror, which the sync engine keeps current (server snapshot + applied
// pending ops). After the queue drains, onBoardSynced reloads with real ids.
async function load() {
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
    state.name = await xyCrypto.decField(dk, snap.name_enc);
    titleNode.textContent = state.name;
    document.title = state.name + " · xy";
    state.lists = await Promise.all(snap.lists.map(async (l) => ({
      id: l.id, type: l.type, rank: l.rank, title: await xyCrypto.decField(dk, l.title_enc),
    })));
    state.cards = await Promise.all(snap.cards.map(async (c) => ({
      id: c.id, listId: c.list_id, kind: c.kind, rank: c.rank,
      desc: await xyCrypto.decField(dk, c.description_enc),
    })));
    state.labels = await Promise.all(snap.labels.map(async (l) => ({
      id: l.id, kind: l.kind,
      name: await xyCrypto.decField(dk, l.name_enc),
      color: await xyCrypto.decField(dk, l.color_enc),
    })));
    render();
    setStatus("saved");
  } catch (e) {
    setStatus("error");
    console.error(e);
  }
}

const byRank = (a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0);
const cardsOf = (listId) => state.cards.filter((c) => c.listId === listId).sort(byRank);
const labelById = (id) => state.labels.find((l) => l.id === id);

// ---- render ----
function render() {
  kanban.hidden = false;
  kanban.replaceChildren();
  for (const list of [...state.lists].sort(byRank)) {
    kanban.append(renderList(list));
  }
  kanban.append(renderAddList());
  paintLabels();
}

function renderList(list) {
  const col = el("div", { class: "klist", draggable: "true", dataset: { listId: list.id } });
  const menuWrap = el("div", { class: "klist-menu-wrap" });
  const menuBtn = el("button", { class: "kadd", title: "Меню списка", text: "⋯", "aria-haspopup": "true" });
  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    popupMenu(menuWrap, [
      { label: "➕ Добавить карточку", onClick: () => addCard(list) },
      { label: "↔ Переместить список…", onClick: () => moveListToPosition(list) },
      { label: "📄 Экспорт в docx", onClick: () => exportList(list) },
    ]);
  });
  menuWrap.append(menuBtn);
  col.append(el("div", { class: "klist-head" },
    el("span", { class: "klist-title", text: list.title || "(без названия)" }),
    menuWrap,
  ));
  const body = el("div", { class: "kcards", dataset: { listId: list.id } });
  const cards = cardsOf(list.id);
  const numbers = list.type === "test" ? [] : xyChgk.numberQuestionCards(cards);
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
    const cardId = Number(e.dataTransfer.getData("text/xy-card"));
    commitCardMove(cardId, list.id, body);
  });
  return col;
}

// cardTitle derives the short preview shown on a kanban card. Question cards are
// prefixed with their (parsed or auto-assigned) number and stripped of the "? "
// marker; meta/heading cards show their parsed text; test cards show the session.
function cardTitle(card, number) {
  if (card.kind === "test") return testTitle(card.desc);
  const text = xyChgk.previewText(card.kind, card.desc);
  const body = deriveTitle(text);
  if (card.kind === "question" && number) return `${number}. ${body}`;
  return body;
}

function renderCard(card, number) {
  const node = el("div", { class: "kcard kcard-" + (card.kind || "normal"), draggable: "true", dataset: { cardId: card.id }, onclick: () => openCard(card) });
  const labelRow = el("div", { class: "kcard-labels" });
  for (const lid of state.cardLabels[card.id] || []) {
    const lbl = labelById(lid);
    if (lbl) labelRow.append(el("span", { class: "label-chip", title: lbl.name, dataset: { c: lbl.color } }));
  }
  if (labelRow.children.length) node.append(labelRow);
  node.append(el("div", { class: "kcard-title", text: cardTitle(card, number) }));
  node.addEventListener("dragstart", (e) => {
    e.stopPropagation();
    e.dataTransfer.setData("text/xy-card", String(card.id));
    node.classList.add("dragging");
  });
  node.addEventListener("dragend", () => node.classList.remove("dragging"));
  // color the chips via inline style is disallowed by CSP? inline style attr is allowed (style-src governs <style>/<link>, not the style attribute under CSP3 'unsafe-inline' for attributes? Actually attribute styles need style-src 'unsafe-inline'). Use dataset + a post-pass with CSSOM:
  return node;
}

// Apply label colors through the CSSOM (avoids inline-style CSP issues).
function paintLabels() {
  for (const chip of document.querySelectorAll(".label-chip[data-c]")) {
    chip.style.backgroundColor = chip.dataset.c;
  }
  for (const sw of document.querySelectorAll(".label-pick[data-c]")) {
    sw.style.borderLeftColor = sw.dataset.c;
  }
}

function dragAfter(container, y) {
  const cards = [...container.querySelectorAll(".kcard:not(.dragging)")];
  let closest = null, closestOffset = -Infinity;
  for (const c of cards) {
    const box = c.getBoundingClientRect();
    const offset = y - box.top - box.height / 2;
    if (offset < 0 && offset > closestOffset) { closestOffset = offset; closest = c; }
  }
  return closest;
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

// moveListToPosition re-ranks a list to a 1-based slot chosen via prompt. Handy
// for long boards where dragging a column across the viewport is awkward.
async function moveListToPosition(list) {
  const ordered = [...state.lists].sort(byRank);
  const n = ordered.length;
  const cur = ordered.findIndex((l) => l.id === list.id) + 1;
  const raw = prompt(`Переместить список на позицию (1–${n}):`, String(cur));
  if (raw == null) return;
  let pos = parseInt(raw, 10);
  if (Number.isNaN(pos)) return;
  pos = Math.max(1, Math.min(n, pos));
  const others = ordered.filter((l) => l.id !== list.id);
  const prev = pos >= 2 ? others[pos - 2] : null;
  const next = pos - 1 < others.length ? others[pos - 1] : null;
  let rank;
  try { rank = keyBetween(prev ? prev.rank : null, next ? next.rank : null); }
  catch (_) { rank = keyBetween(prev ? prev.rank : null, null); }
  list.rank = rank;
  setStatus("saving");
  try {
    await patch("patchList", `/api/lists/${list.id}`, { rank });
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); load(); }
}

// ---- export a list to .docx via chgksuite (PLAN §8) ----
// Concatenate the list's card descriptions (in board order) into a chgksuite
// "4s" document, gather any images referenced by `(img ...)` directives from the
// cards' attachments, and hand both to the server, which composes the docx and
// wipes the plaintext scratch files. See internal/server/export.go.
async function exportList(list) {
  const cards = cardsOf(list.id);
  if (!cards.length) { alert("В списке нет карточек."); return; }
  if (!xySync.isOnline()) { alert("Экспорт в docx доступен только онлайн."); return; }
  setStatus("saving");
  try {
    const source = cards.map((c) => c.desc.trim()).filter(Boolean).join("\n\n") + "\n";
    // collect (img NAME ...) references
    const wanted = new Set();
    for (const m of source.matchAll(/\(img\s+([^\s)]+)/g)) wanted.add(m[1]);

    const fd = new FormData();
    fd.append("source", source);
    fd.append("filename", list.title || "export");

    // resolve referenced images from the cards' attachments (decrypt + attach)
    if (wanted.size) {
      const seen = new Set();
      for (const card of cards) {
        let atts;
        try { atts = await fetchJSON(`/api/cards/${card.id}/attachments`); } catch (_) { continue; }
        for (const att of atts) {
          let name = "";
          try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) { continue; }
          if (!wanted.has(name) || seen.has(name)) continue;
          seen.add(name);
          const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
          if (!res.ok) continue;
          const plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
          fd.append("img", new Blob([plain], { type: att.mime }), name);
        }
      }
      const missing = [...wanted].filter((n) => !seen.has(n));
      if (missing.length && !confirm(`Не найдены изображения: ${missing.join(", ")}. Продолжить?`)) {
        setStatus("saved");
        return;
      }
    }

    const res = await fetch("/api/export/docx", { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = el("a", { href: url, download: (list.title || "export") + ".docx" });
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

// addCard opens the card detail in "create mode" — only the description editor
// is shown (the card isn't persisted until you save a description, so we never
// create empty cards). Labels/attachments/move/timeline appear only when editing
// an existing card.
function addCard(list) {
  if (list.type === "test") return addTestCard(list);
  pendingList = list;
  openCardId = null;
  document.getElementById("cardDesc").value = "";
  document.getElementById("cardKindLabel").hidden = false;
  document.getElementById("cardKind").hidden = false;
  document.getElementById("cardKind").value = "question";
  document.getElementById("cardMessage").textContent = "";
  document.querySelector(".card-detail").classList.add("creating");
  cardOverlay.hidden = false;
  document.getElementById("cardDesc").focus();
}

// addTestCard: a test card's "description" is JSON {datetime, players:[ids]}.
// Creating it also auto-creates two board labels ("{dt} взяли" green / "не
// взяли" red) for the user to assign to questions later (OVERVIEW / PLAN §6).
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
    const desc = JSON.stringify({ datetime: dt, players: [], title });
    const res = await create("createCard", `/api/lists/${list.id}/cards`, {
      description_enc: await xyCrypto.encField(dk, desc), rank, kind: "test",
    });
    state.cards.push({ id: res.id, listId: list.id, kind: "test", rank, desc });
    // auto labels
    for (const [suffix, color, kind] of [["взяли", "#3aa657", "test_taken"], ["не взяли", "#dd3322", "test_missed"]]) {
      const lr = await create("createLabel", `/api/boards/${boardId}/labels`, {
        name_enc: await xyCrypto.encField(dk, `${tag} ${suffix}`),
        color_enc: await xyCrypto.encField(dk, color),
        kind,
      });
      state.labels.push({ id: lr.id, kind, name: `${tag} ${suffix}`, color });
    }
    render();
  } catch (err) { setStatus("error"); }
}

// testTitle renders a test card's derived title from its JSON description.
function testTitle(desc) {
  try {
    const m = JSON.parse(desc);
    const n = (m.players || []).length;
    const head = m.title ? `${m.title} · ${m.datetime}` : m.datetime;
    return `🗓 ${head}${n ? ` · ${n} игроков` : ""}`;
  } catch (_) { return "тест-сессия"; }
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
const cardOverlay = document.getElementById("cardOverlay");

async function openCard(card) {
  pendingList = null;
  openCardId = card.id;
  document.querySelector(".card-detail").classList.remove("creating");
  document.getElementById("cardDesc").value = card.desc;
  document.getElementById("cardMessage").textContent = "";
  // Kind selector: editable for ordinary cards, hidden for test cards (their
  // "kind" is fixed and their description is JSON, not 4s markup).
  const isTest = card.kind === "test";
  document.getElementById("cardKindLabel").hidden = isTest;
  const kindSel = document.getElementById("cardKind");
  kindSel.hidden = isTest;
  if (!isTest) kindSel.value = card.kind || "question";
  cardOverlay.hidden = false;
  renderLabelPicker(card);
  renderCardLabels(card);
  await loadAttachments(card.id);
  await loadTimeline(card.id);
  await populateMoveBoards();
  paintLabels();
}

// ---- move / copy a card (same board → relocate/duplicate; other board →
// client-side re-encryption, PLAN §6). Boards are chosen by (decrypted) name and
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
  msg.textContent = sameBoard ? "Сохранение…" : "Перешифровка…";
  try {
    if (sameBoard) {
      if (remove) {
        await patch("patchCard", `/api/cards/${card.id}`, { list_id: targetListId, rank });
        card.listId = targetListId;
        card.rank = rank;
      } else {
        const res = await create("createCard", `/api/lists/${targetListId}/cards`, {
          description_enc: await xyCrypto.encField(dk, card.desc), rank, kind: card.kind,
        });
        state.cards.push({ id: res.id, listId: targetListId, kind: card.kind, rank, desc: card.desc });
        const ids = state.cardLabels[card.id] || [];
        if (ids.length) { await put("setCardLabels", `/api/cards/${res.id}/labels`, { label_ids: ids }); state.cardLabels[res.id] = ids.slice(); }
      }
    } else {
      // Cross-board copy/move re-encrypts under the target board's key and touches
      // a second board's structure — inherently an online operation.
      if (!xySync.isOnline()) { msg.textContent = "Перенос между досками доступен только онлайн."; return; }
      const tdk = moveCtx.dk;
      const res = await jpost(`/api/lists/${targetListId}/cards`, {
        description_enc: await xyCrypto.encField(tdk, card.desc), rank, kind: card.kind,
      });
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
  if (pendingList || openCardId == null) return; // create mode → no-op here
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  const kind = e.target.value;
  const msg = document.getElementById("cardMessage");
  try {
    await patch("patchCard", `/api/cards/${card.id}`, { kind });
    card.kind = kind;
    render();
    msg.textContent = "Тип изменён.";
  } catch (err) { msg.textContent = err.message; }
});

function closeCard() { cardOverlay.hidden = true; openCardId = null; pendingList = null; }
document.getElementById("cardClose").addEventListener("click", closeCard);
cardOverlay.addEventListener("pointerdown", (e) => { if (e.target === cardOverlay) closeCard(); });

document.getElementById("cardSave").addEventListener("click", async () => {
  // create mode: persist a new card with the typed description, then switch to
  // the full edit view.
  if (pendingList) {
    const text = document.getElementById("cardDesc").value;
    const msg = document.getElementById("cardMessage");
    if (!text.trim()) { msg.textContent = "Введите описание."; return; }
    const list = pendingList;
    const kind = document.getElementById("cardKind").value || "question";
    const existing = cardsOf(list.id);
    const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
    try {
      const res = await create("createCard", `/api/lists/${list.id}/cards`, { description_enc: await xyCrypto.encField(dk, text), rank, kind });
      const card = { id: res.id, listId: list.id, kind, rank, desc: text };
      state.cards.push(card);
      render();
      await openCard(card);
    } catch (err) { msg.textContent = err.message; }
    return;
  }
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card) return;
  const newDesc = document.getElementById("cardDesc").value;
  const msg = document.getElementById("cardMessage");
  msg.textContent = "";
  try {
    const body = { description_enc: await xyCrypto.encField(dk, newDesc) };
    if (newDesc !== card.desc) {
      body.desc_event_enc = await xyCrypto.encField(dk, JSON.stringify({ before: card.desc, after: newDesc }));
    }
    await patch("patchCard", `/api/cards/${card.id}`, body);
    card.desc = newDesc;
    render();
    await loadTimeline(card.id);
    msg.textContent = "Сохранено.";
  } catch (err) { msg.textContent = err.message; }
});

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
function renderCardLabels(card) {
  const box = document.getElementById("cardLabels");
  box.replaceChildren();
  for (const lid of state.cardLabels[card.id] || []) {
    const lbl = labelById(lid);
    if (lbl) box.append(el("span", { class: "label-chip label-chip-lg", title: lbl.name, dataset: { c: lbl.color }, text: lbl.name }));
  }
  paintLabels();
}

// renderLabelPicker shows only the labels actually assigned to this card (each
// removable on click). Boards can have dozens of labels (e.g. one green/red pair
// per test session), so the rest live behind an "add label" dropdown rather than
// being dumped on screen.
function renderLabelPicker(card) {
  const picker = document.getElementById("labelPicker");
  picker.replaceChildren();
  const assigned = state.cardLabels[card.id] || [];
  const assignedSet = new Set(assigned);
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

  // dropdown of the remaining (unassigned) labels
  const sel = document.getElementById("labelAdd");
  sel.replaceChildren(el("option", { value: "", text: "+ добавить метку…" }));
  const unassigned = state.labels
    .filter((l) => !assignedSet.has(l.id))
    .sort((a, b) => a.name.localeCompare(b.name, "ru"));
  for (const lbl of unassigned) sel.append(el("option", { value: String(lbl.id), text: lbl.name }));
  paintLabels();
}

document.getElementById("labelAdd").addEventListener("change", (e) => {
  const id = Number(e.target.value);
  e.target.value = "";
  if (!id) return;
  const card = state.cards.find((c) => c.id === openCardId);
  const lbl = labelById(id);
  if (card && lbl) toggleLabel(card, lbl);
});

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
    renderCardLabels(card);
    render();
    await loadTimeline(card.id);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

document.getElementById("newLabelForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = document.getElementById("newLabelName").value.trim();
  const color = document.getElementById("newLabelColor").value;
  if (!name) return;
  try {
    const res = await create("createLabel", `/api/boards/${boardId}/labels`, {
      name_enc: await xyCrypto.encField(dk, name),
      color_enc: await xyCrypto.encField(dk, color),
    });
    state.labels.push({ id: res.id, kind: "normal", name, color });
    document.getElementById("newLabelName").value = "";
    const card = state.cards.find((c) => c.id === openCardId);
    if (card) renderLabelPicker(card);
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
  // Newest first: events are oldest→newest (by id); show them reversed.
  for (const ev of [...events].reverse()) {
    let payload = "";
    try { payload = await xyCrypto.decField(dk, ev.payload_enc); } catch (_) {}
    tl.append(renderEvent(ev, payload));
  }
}

function renderEvent(ev, payload) {
  const when = new Date(ev.created_at).toLocaleString("ru-RU");
  const wrap = el("div", { class: "tl-event tl-" + ev.type });
  if (ev.type === "comment") {
    wrap.append(el("div", { class: "tl-meta", text: when }), el("div", { class: "tl-comment", text: payload }));
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
    wrap.append(el("div", { class: "tl-meta", text: "правка описания · " + when }),
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
    wrap.append(el("div", { class: "tl-meta", text: `${verb}${detail ? ": " + detail : ""} · ${when}` }));
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
async function loadAttachments(cardId) {
  const box = document.getElementById("attachments");
  box.replaceChildren();
  let list;
  try { list = await fetchJSON(`/api/cards/${cardId}/attachments`); } catch (_) { return; }
  for (const att of list) {
    let name = "файл";
    try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) {}
    const row = el("div", { class: "attach-row" },
      el("button", { class: "attach-name", type: "button", text: `📎 ${name}`, onclick: () => download(att, name) }),
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

// recompressToWebp re-encodes an image File to WebP q70 unless lossless.
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

document.getElementById("attachUpload").addEventListener("click", async () => {
  const input = document.getElementById("attachFile");
  const file = input.files[0];
  if (!file || !openCardId) return;
  if (!xySync.isOnline()) { document.getElementById("cardMessage").textContent = "Загрузка вложений доступна только онлайн."; return; }
  const lossless = document.getElementById("attachLossless").checked;
  const msg = document.getElementById("cardMessage");
  msg.textContent = "Шифрование…";
  try {
    let bytes, mime;
    if (lossless) { bytes = new Uint8Array(await file.arrayBuffer()); mime = file.type || "application/octet-stream"; }
    else ({ bytes, mime } = await recompressToWebp(file));
    const cipher = await xyCrypto.encBytes(dk, bytes);
    const fd = new FormData();
    fd.append("meta", JSON.stringify({
      filename_enc: await xyCrypto.encField(dk, file.name),
      mime, lossless,
      event_payload_enc: await xyCrypto.encField(dk, JSON.stringify({ file: file.name })),
    }));
    fd.append("blob", new Blob([cipher], { type: "application/octet-stream" }), "blob");
    const res = await fetch(`/api/cards/${openCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
    if (!res.ok) throw new Error((await res.text()) || "ошибка загрузки");
    input.value = "";
    document.getElementById("attachLossless").checked = false;
    msg.textContent = "";
    await loadAttachments(openCardId);
    await loadTimeline(openCardId);
  } catch (err) { msg.textContent = err.message; }
});

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
