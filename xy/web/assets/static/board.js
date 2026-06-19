// board.js — kanban board: unlock, render lists/cards (derived titles),
// drag-reorder with fractional ranks, card detail + timeline + labels.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";

const { fetchJSON, jpost, jpatch, jput, jdelete, el, deriveTitle } = xyApp;
const { keyBetween } = xyRank;

const boardId = Number(location.pathname.split("/").pop());

const statusNode = document.getElementById("status");
const kanban = document.getElementById("kanban");
const titleNode = document.getElementById("boardTitle");

const state = { role: "editor", name: "", lists: [], cards: [], labels: [], cardLabels: {} };
let dk = null;

function setStatus(s) {
  const labels = { saved: "Готово", saving: "Подождите", error: "Ошибка" };
  statusNode.dataset.state = s;
  statusNode.title = labels[s] || labels.saved;
}

// ---- boot + unlock ----
async function boot() {
  if (!(await xyApp.requireLogin())) return;
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

document.getElementById("lockBtn").addEventListener("click", async () => {
  await xyCrypto.forgetDK(boardId);
  location.reload();
});

// ---- load + decrypt snapshot ----
async function load() {
  setStatus("saving");
  try {
    const snap = await fetchJSON(`/api/boards/${boardId}`);
    state.role = snap.role;
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
}

function renderList(list) {
  const col = el("div", { class: "klist", draggable: "true", dataset: { listId: list.id } });
  col.append(el("div", { class: "klist-head" },
    el("span", { class: "klist-title", text: list.title || "(без названия)" }),
    el("button", { class: "kadd", title: "Добавить карточку", text: "+", onclick: () => addCard(list) }),
  ));
  const body = el("div", { class: "kcards", dataset: { listId: list.id } });
  for (const card of cardsOf(list.id)) body.append(renderCard(card));
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

function renderCard(card) {
  const node = el("div", { class: "kcard", draggable: "true", dataset: { cardId: card.id }, onclick: () => openCard(card) });
  const labelRow = el("div", { class: "kcard-labels" });
  for (const lid of state.cardLabels[card.id] || []) {
    const lbl = labelById(lid);
    if (lbl) labelRow.append(el("span", { class: "label-chip", title: lbl.name, dataset: { c: lbl.color } }));
  }
  if (labelRow.children.length) node.append(labelRow);
  const title = card.kind === "test" ? testTitle(card.desc) : deriveTitle(card.desc);
  node.append(el("div", { class: "kcard-title", text: title }));
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
      const res = await jpost(`/api/boards/${boardId}/lists`, { title_enc: titleEnc, rank, type });
      state.lists.push({ id: res.id, type, rank, title });
      input.value = "";
      typeRow.querySelector("input").checked = false;
      render();
    } catch (err) { setStatus("error"); }
  });
  wrap.append(form);
  return wrap;
}

async function addCard(list) {
  if (list.type === "test") return addTestCard(list);
  const existing = cardsOf(list.id);
  const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
  try {
    const descEnc = await xyCrypto.encField(dk, "");
    const res = await jpost(`/api/lists/${list.id}/cards`, { description_enc: descEnc, rank });
    const card = { id: res.id, listId: list.id, kind: "normal", rank, desc: "" };
    state.cards.push(card);
    render();
    openCard(card);
  } catch (err) { setStatus("error"); }
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
  const existing = cardsOf(list.id);
  const rank = keyBetween(existing.length ? existing[existing.length - 1].rank : null, null);
  try {
    const desc = JSON.stringify({ datetime: dt, players: [] });
    const res = await jpost(`/api/lists/${list.id}/cards`, {
      description_enc: await xyCrypto.encField(dk, desc), rank, kind: "test",
    });
    state.cards.push({ id: res.id, listId: list.id, kind: "test", rank, desc });
    // auto labels
    for (const [suffix, color, kind] of [["взяли", "#3aa657", "test_taken"], ["не взяли", "#dd3322", "test_missed"]]) {
      const lr = await jpost(`/api/boards/${boardId}/labels`, {
        name_enc: await xyCrypto.encField(dk, `${dt} ${suffix}`),
        color_enc: await xyCrypto.encField(dk, color),
        kind,
      });
      state.labels.push({ id: lr.id, kind, name: `${dt} ${suffix}`, color });
    }
    render();
  } catch (err) { setStatus("error"); }
}

// testTitle renders a test card's derived title from its JSON description.
function testTitle(desc) {
  try {
    const m = JSON.parse(desc);
    const n = (m.players || []).length;
    return `🗓 ${m.datetime}${n ? ` · ${n} игроков` : ""}`;
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
    await jpatch(`/api/cards/${cardId}`, { list_id: targetListId, rank });
    setStatus("saved");
    render();
  } catch (err) { setStatus("error"); load(); }
}

// ---- card detail ----
let openCardId = null;
const cardOverlay = document.getElementById("cardOverlay");

async function openCard(card) {
  openCardId = card.id;
  document.getElementById("cardDesc").value = card.desc;
  document.getElementById("cardMessage").textContent = "";
  cardOverlay.hidden = false;
  renderLabelPicker(card);
  renderCardLabels(card);
  await loadAttachments(card.id);
  await loadTimeline(card.id);
  await populateMoveTargets();
  paintLabels();
}

// ---- cross-board copy / move (client-side re-encryption, PLAN §6) ----
async function populateMoveTargets() {
  const sel = document.getElementById("moveTarget");
  sel.replaceChildren();
  let boards = [];
  try { boards = await fetchJSON("/api/boards"); } catch (_) {}
  for (const b of boards) {
    if (b.id === boardId) continue;
    sel.append(el("option", { value: b.id, text: "доска #" + b.id }));
  }
  if (!sel.children.length) sel.append(el("option", { value: "", text: "нет других досок" }));
}

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

async function copyCardTo(targetId, remove) {
  const card = state.cards.find((c) => c.id === openCardId);
  if (!card || !targetId) return;
  const msg = document.getElementById("cardMessage");
  msg.textContent = "Перешифровка…";
  try {
    const tdk = await ensureDK(targetId);
    const snap = await fetchJSON(`/api/boards/${targetId}`);
    // pick a destination list (first by rank) or create one
    let listId;
    const tlists = (snap.lists || []).slice().sort((a, b) => (a.rank < b.rank ? -1 : 1));
    if (tlists.length) listId = tlists[0].id;
    else {
      const lr = await jpost(`/api/boards/${targetId}/lists`, {
        title_enc: await xyCrypto.encField(tdk, "Импортировано"), rank: keyBetween(null, null),
      });
      listId = lr.id;
    }
    // re-encrypt description under the target key
    const tcards = (snap.cards || []).filter((c) => c.list_id === listId).map((c) => c.rank).sort();
    const rank = keyBetween(tcards.length ? tcards[tcards.length - 1] : null, null);
    const res = await jpost(`/api/lists/${listId}/cards`, {
      description_enc: await xyCrypto.encField(tdk, card.desc), rank, kind: card.kind,
    });
    // reconcile labels by decrypted name+color
    const srcIds = state.cardLabels[card.id] || [];
    if (srcIds.length) {
      const tLabels = await Promise.all((snap.labels || []).map(async (l) => ({
        id: l.id, name: await xyCrypto.decField(tdk, l.name_enc), color: await xyCrypto.decField(tdk, l.color_enc),
      })));
      const targetIds = [];
      for (const sid of srcIds) {
        const sl = labelById(sid);
        if (!sl) continue;
        let match = tLabels.find((t) => t.name === sl.name && t.color === sl.color);
        if (!match) {
          const lr = await jpost(`/api/boards/${targetId}/labels`, {
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
      render();
    }
    msg.textContent = remove ? "Перемещено." : "Скопировано.";
  } catch (err) { msg.textContent = err.message; }
}

document.getElementById("copyBtn").addEventListener("click", () => copyCardTo(Number(document.getElementById("moveTarget").value), false));
document.getElementById("moveBtn").addEventListener("click", () => copyCardTo(Number(document.getElementById("moveTarget").value), true));

document.getElementById("cardClose").addEventListener("click", () => { cardOverlay.hidden = true; openCardId = null; });
cardOverlay.addEventListener("pointerdown", (e) => { if (e.target === cardOverlay) { cardOverlay.hidden = true; openCardId = null; } });

document.getElementById("cardSave").addEventListener("click", async () => {
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
    await jpatch(`/api/cards/${card.id}`, body);
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
    await jdelete(`/api/cards/${card.id}`);
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

function renderLabelPicker(card) {
  const picker = document.getElementById("labelPicker");
  picker.replaceChildren();
  const assigned = new Set(state.cardLabels[card.id] || []);
  for (const lbl of state.labels) {
    const pick = el("button", {
      class: "label-pick" + (assigned.has(lbl.id) ? " is-on" : ""),
      type: "button", dataset: { c: lbl.color }, text: lbl.name,
      onclick: () => toggleLabel(card, lbl),
    });
    picker.append(pick);
  }
  paintLabels();
}

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
    await jput(`/api/cards/${card.id}/labels`, { label_ids: ids, events });
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
    const res = await jpost(`/api/boards/${boardId}/labels`, {
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
  let events;
  try { events = await fetchJSON(`/api/cards/${cardId}/timeline`); } catch (_) { return; }
  for (const ev of events) {
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
    wrap.append(el("div", { class: "tl-meta", text: "правка описания · " + when }),
      el("div", { class: "tl-diff" },
        el("div", { class: "tl-before", text: diff.before || "" }),
        el("div", { class: "tl-after", text: diff.after || "" })));
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
    await jpost(`/api/cards/${openCardId}/comments`, { payload_enc: await xyCrypto.encField(dk, text) });
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
    const res = await fetch(`/api/attachments/${att.id}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error("не удалось скачать");
    const cipher = new Uint8Array(await res.arrayBuffer());
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
  try {
    const ev = await xyCrypto.encField(dk, JSON.stringify({ file: name }));
    await jdelete(`/api/attachments/${att.id}?event_payload_enc=${encodeURIComponent(ev)}`);
    await loadAttachments(openCardId);
    await loadTimeline(openCardId);
  } catch (err) { document.getElementById("cardMessage").textContent = err.message; }
}

boot();
