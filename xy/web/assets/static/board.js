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

const state = { role: "editor", name: "", lists: [], cards: [], labels: [], cardLabels: {}, members: [], memberNames: {}, me: null };
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
  label: "👥 Участники доски",
  title: "Поделиться доской: добавить или убрать участников",
  onClick: () => openMembers(),
}, {
  label: "🔒 Забыть пароль доски",
  title: "Забыть пароль доски на этом устройстве",
  onClick: async () => {
    await xyCrypto.forgetDK(boardId);
    location.reload();
  },
}]);

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
    loadMembers(); // best-effort: populate the author-name map for timelines (online only)
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
      { label: "🔍 Предпросмотр", onClick: () => previewList(list) },
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
    if (cardDragCommitted) return; // ignore a stray second drop from the same gesture
    cardDragCommitted = true;
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

// renderCardTitle builds the title node. For numbered question cards the auto/
// directive number is rendered in a muted span so it reads as scaffolding,
// visually distinct from the question content itself.
function renderCardTitle(card, number) {
  if (card.kind === "question" && number) {
    const body = deriveTitle(xyChgk.previewText(card.kind, card.desc));
    return el("div", { class: "kcard-title" },
      el("span", { class: "kcard-num", text: `${number}. ` }),
      body);
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
// Object URLs minted for the current preview, revoked when it closes.
let previewUrls = [];
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

// resolveImages maps each wanted image name → a decrypted object URL by scanning
// the cards' attachments (online only — mirrors the docx export's image
// gathering). Missing names simply render as a placeholder in renderRich.
async function resolveImages(cards, wanted) {
  const map = new Map();
  if (!wanted.size || !xySync.isOnline()) return map;
  for (const card of cards) {
    if (map.size >= wanted.size) break;
    let atts;
    try { atts = await fetchJSON(`/api/cards/${card.id}/attachments`); } catch (_) { continue; }
    for (const att of atts) {
      let name = "";
      try { name = await xyCrypto.decField(dk, att.filename_enc); } catch (_) { continue; }
      if (!wanted.has(name) || map.has(name)) continue;
      try {
        const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
        if (!res.ok) continue;
        const plain = await xyCrypto.decBytes(dk, new Uint8Array(await res.arrayBuffer()));
        const url = URL.createObjectURL(new Blob([plain], { type: att.mime }));
        previewUrls.push(url);
        map.set(name, url);
      } catch (_) {}
    }
  }
  return map;
}

// renderRich turns a 4s text element into DOM, mirroring the docx print render:
// inline bold/italic/underline/strike/small-caps, links, (screen …) host side,
// explicit (LINEBREAK)/(PAGEBREAK), and (img …) handouts (shown inline). Styling
// is applied via the CSSOM (.style.*) to stay within the strict CSP.
function renderRich(text, imgMap) {
  const frag = document.createDocumentFragment();
  for (const [type, val] of xyChgk.printRuns(text)) {
    if (type === "linebreak") { frag.append(el("br")); continue; }
    if (type === "pagebreak") { frag.append(el("hr", { class: "pv-pagebreak" })); continue; }
    if (type === "img") {
      const name = imgName(val);
      const url = imgMap.get(name);
      if (url) frag.append(el("img", { class: "pv-img", src: url, alt: name }));
      else frag.append(el("span", { class: "pv-img-missing", text: `[изображение: ${name}]` }));
      continue;
    }
    if (type === "screen") { frag.append(document.createTextNode(val.for_print || "")); continue; }
    if (type === "hyperlink") {
      frag.append(el("a", { class: "pv-link", href: val, target: "_blank", rel: "noopener noreferrer", text: val }));
      continue;
    }
    if (!type) { frag.append(document.createTextNode(val)); continue; }
    const span = el("span", { text: val });
    if (type.includes("italic")) span.style.fontStyle = "italic";
    if (type.includes("bold")) span.style.fontWeight = "bold";
    if (type.includes("underline")) span.style.textDecoration = "underline";
    if (type === "strike") span.style.textDecoration = "line-through";
    if (type === "sc") span.classList.add("pv-sc");
    frag.append(span);
  }
  return frag;
}

// pvField renders a "Label: value" line (bold label + rich value).
function pvField(label, text, imgMap, cls) {
  const node = el("div", { class: "pv-field" + (cls ? " " + cls : "") },
    el("strong", { class: "pv-label", text: label + ": " }));
  node.append(renderRich(text, imgMap));
  return node;
}

// renderPreviewCard renders one card the way the docx export would: a question
// card becomes a numbered question with its answer/zachet/etc.; meta/heading/
// section/editor/date cards become their corresponding paragraphs/headings.
function renderPreviewCard(card, number, imgMap) {
  if (card.kind === "test") {
    return el("p", { class: "pv-meta pv-test", text: testTitle(card.desc) });
  }
  const blocks = xyChgk.parseBlocks(card.desc);
  const find = (t) => blocks.find((b) => b.type === t);

  if (card.kind === "question" || find("question")) {
    const wrap = el("article", { class: "pv-q" });
    const handout = find("handout");
    if (handout) {
      const h = el("div", { class: "pv-field pv-handout" },
        el("strong", { class: "pv-label", text: PV_LABELS.handout + ": " }));
      h.append(renderRich(handout.text, imgMap));
      wrap.append(h);
    }
    // Question line: bold "Вопрос N." label + the question text.
    const qline = el("div", { class: "pv-q-text" },
      el("strong", { class: "pv-label", text: number ? `Вопрос ${number}. ` : "Вопрос. " }));
    qline.append(renderRich(xyChgk.questionText(card.desc), imgMap));
    wrap.append(qline);
    for (const f of ["answer", "zachet", "nezachet", "comment", "source", "author"]) {
      const b = find(f);
      if (b) wrap.append(pvField(PV_LABELS[f], b.text, imgMap));
    }
    return wrap;
  }

  // Non-question card: render each block by type.
  const wrap = el("div", { class: "pv-block" });
  for (const b of blocks) {
    if (b.type === "num" || b.type === "numnum") continue; // numbering directive only
    if (b.type === "heading" || b.type === "ljheading") {
      const h = el("h2", { class: "pv-heading" });
      h.append(renderRich(b.text, imgMap));
      wrap.append(h);
    } else if (b.type === "section") {
      const h = el("h3", { class: "pv-section" });
      h.append(renderRich(b.text, imgMap));
      wrap.append(h);
    } else if (PV_LABELS[b.type]) {
      wrap.append(pvField(PV_LABELS[b.type], b.text, imgMap));
    } else {
      const p = el("p", { class: "pv-meta" });
      p.append(renderRich(b.text, imgMap));
      wrap.append(p);
    }
  }
  return wrap;
}

function closePreview() {
  previewOverlay.hidden = true;
  for (const u of previewUrls) URL.revokeObjectURL(u);
  previewUrls = [];
  document.getElementById("previewBody").replaceChildren();
}

// previewList opens the preview modal and renders the whole list. Text renders
// instantly; image handouts are resolved from attachments and filled in after.
async function previewList(list) {
  const cards = cardsOf(list.id);
  document.getElementById("previewTitle").textContent = list.title || "Предпросмотр";
  const body = document.getElementById("previewBody");
  body.replaceChildren();
  previewOverlay.hidden = false;
  if (!cards.length) {
    body.append(el("p", { class: "pv-empty", text: "В списке нет карточек." }));
    return;
  }
  const numbers = list.type === "test" ? cards.map(() => null) : xyChgk.numberQuestionCards(cards);
  const imgMap = await resolveImages(cards, imageRefs(cards));
  // Guard against a close (or another open) during the await.
  if (previewOverlay.hidden) { for (const u of previewUrls) URL.revokeObjectURL(u); previewUrls = []; return; }
  body.replaceChildren();
  cards.forEach((card, i) => body.append(renderPreviewCard(card, numbers[i], imgMap)));
}

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
  document.getElementById("cardDesc").value = "";
  document.getElementById("cardKind").hidden = false;
  document.getElementById("cardKind").value = "question";
  document.getElementById("cardMessage").textContent = "";
  document.querySelector(".card-detail").classList.add("creating");
  document.getElementById("cardCopy").hidden = true; // no number/desc yet
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
  const kindSel = document.getElementById("cardKind");
  kindSel.hidden = isTest;
  if (!isTest) kindSel.value = card.kind || "question";
  // The "copy for testing" action only makes sense for question cards (it shares
  // the numbered, screen-mode question text); hide it otherwise.
  document.getElementById("cardCopy").hidden = card.kind !== "question";
  document.getElementById("cardCopyMsg").hidden = true;
  cardOverlay.hidden = false;
  renderLabelPicker(card);
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

// ---- copy a question to the clipboard for a test session ----
// questionNumberFor returns the display number this question card would show on
// the board (auto-assigned or directive-driven), matching the kanban preview.
function questionNumberFor(card) {
  if (!card || card.kind !== "question") return null;
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

// openLabelAddPopup mounts a custom dropdown under the "+ добавить метку" button:
// a filter field above a scrollable list of the unassigned labels, sorted by last
// usage (sortLabels). A native <select> can't host a search box, hence the
// hand-rolled popup (shares the .menu-dropdown styling of the list "⋯" menu).
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
  const popup = el("div", { class: "menu-dropdown label-add-popup", role: "menu" }, filter, listBox);

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
    wrap.append(el("div", { class: "tl-meta", text: meta(when) }), el("div", { class: "tl-comment", text: payload }));
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

// uploadAttachment encrypts `file` under the saved name and POSTs it to the open
// card. When lossless is false the bytes are re-encoded to WebP q70 first (the
// same recompression the default file-picker upload applies). Online-only —
// callers must gate on xySync.isOnline(). Refreshes the attachment list+timeline.
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
  const lossless = document.getElementById("attachLossless").checked;
  try {
    await uploadAttachment(file, lossless, file.name);
    input.value = "";
    document.getElementById("attachLossless").checked = false;
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
});

// ---- paste-to-attach ----
// Pasting an image while a saved card is open captures it, then asks for a
// filename + whether to WebP-compress (matching the default-upload checkbox)
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
  document.getElementById("pasteCompress").checked = true;
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
