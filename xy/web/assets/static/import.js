// import.js — one-shot import of a Trello board JSON export into a new encrypted
// xy board. Everything (lists, cards, labels, comments) is encrypted client-side
// under a fresh board key before it ever reaches the server. No Trello OAuth: the
// user pastes the JSON they exported from Trello.
//
// Conventions handled:
//  - lists whose name ends in "_tests"/"tests" become xy test-lists; their cards
//    become test cards (date from the card name, testers kept as a comment), and
//    the card's green/red labels are mapped to test_taken/test_missed kinds.
//  - other cards are classified via the chgksuite parser (heading/meta/question).
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";

const { fetchJSON, jpost, jput } = xyApp;
const { keyBetween } = xyRank;

const statusNode = document.getElementById("status");
const msg = document.getElementById("importMessage");
const form = document.getElementById("importForm");
const importBtn = document.getElementById("importBtn");

function setStatus(s) {
  statusNode.dataset.state = s;
}
function log(line) {
  msg.textContent = line;
}

// ---- Trello label colors → hex. Green/red match xy's auto test labels so an
// imported package looks identical to one built in xy. ----
const TRELLO_COLORS = {
  green: "#3aa657", lime: "#51e898", yellow: "#f2d600", orange: "#ff9f1a",
  red: "#dd3322", purple: "#c377e0", blue: "#0079bf", sky: "#00c2e0",
  pink: "#ff78cb", black: "#344563", grey: "#b3bac5", gray: "#b3bac5",
};
function colorHex(c) {
  if (!c) return "#b3bac5";
  const base = String(c).split("_")[0];
  return TRELLO_COLORS[base] || "#b3bac5";
}
const isGreen = (c) => /^(green|lime)/.test(String(c || ""));
const isRed = (c) => /^red/.test(String(c || ""));

// A list is a test-list if its name ends with "tests" (e.g. "harmony2025_tests").
const isTestList = (name) => /tests$/i.test(String(name || "").trim());

const byPos = (a, b) => (a.pos || 0) - (b.pos || 0);

async function runImport(board, name, pass) {
  setStatus("saving");
  log("Создаю доску…");

  // 1. fresh board key + board row
  const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
  const boardName = name || board.name || "Импорт из Trello";
  const created = await jpost("/api/boards", { ...keymeta, name: boardName });
  const boardId = created.id;
  await xyCrypto.cacheDK(boardId, dk);

  const enc = (s) => xyCrypto.encField(dk, s);

  // 2. lists (skip closed), remember test-ness, keep a trello→xy id map
  const openLists = (board.lists || []).filter((l) => !l.closed).sort(byPos);
  const listMap = new Map(); // trelloListId -> { id, test }
  let listRank = null;
  for (const l of openLists) {
    const test = isTestList(l.name);
    listRank = keyBetween(listRank, null);
    const res = await jpost(`/api/boards/${boardId}/lists`, {
      title_enc: await enc(l.name || "(без названия)"), rank: listRank, type: test ? "test" : "normal",
    });
    listMap.set(l.id, { id: res.id, test });
  }

  // group open cards by their (open) list, in board order
  const cardsByList = new Map();
  for (const c of (board.cards || [])) {
    if (c.closed || !listMap.has(c.idList)) continue;
    if (!cardsByList.has(c.idList)) cardsByList.set(c.idList, []);
    cardsByList.get(c.idList).push(c);
  }
  for (const arr of cardsByList.values()) arr.sort(byPos);

  // 3. decide each label's kind: scan test-list cards, where a green label means
  // "взяли" (test_taken) and a red one "не взяли" (test_missed).
  const labelKind = new Map(); // trelloLabelId -> 'normal' | 'test_taken' | 'test_missed'
  for (const l of (board.labels || [])) labelKind.set(l.id, "normal");
  for (const [listId, cards] of cardsByList) {
    if (!listMap.get(listId).test) continue;
    for (const c of cards) {
      for (const lab of (c.labels || [])) {
        if (isGreen(lab.color)) labelKind.set(lab.id, "test_taken");
        else if (isRed(lab.color)) labelKind.set(lab.id, "test_missed");
      }
    }
  }

  // 4. create labels, mapping trello→xy id
  const labelMap = new Map(); // trelloLabelId -> xyLabelId
  for (const l of (board.labels || [])) {
    const nm = l.name || `метка (${l.color || "без цвета"})`;
    const res = await jpost(`/api/boards/${boardId}/labels`, {
      name_enc: await enc(nm), color_enc: await enc(colorHex(l.color)), kind: labelKind.get(l.id) || "normal",
    });
    labelMap.set(l.id, res.id);
  }

  // 5. cards
  const total = [...cardsByList.values()].reduce((n, a) => n + a.length, 0);
  let done = 0;
  const errors = [];
  for (const l of openLists) {
    const info = listMap.get(l.id);
    const cards = cardsByList.get(l.id) || [];
    let cardRank = null;
    for (const c of cards) {
      cardRank = keyBetween(cardRank, null);
      try {
        if (info.test) {
          await importTestCard(boardId, info.id, c, cardRank, enc, labelMap);
        } else {
          await importNormalCard(boardId, info.id, c, cardRank, enc, labelMap);
        }
      } catch (e) {
        errors.push(`«${c.name || c.id}»: ${e.message}`);
      }
      done++;
      log(`Импортировано ${done}/${total} карточек…`);
    }
  }

  setStatus("saved");
  let summary = `Готово: ${done} карточек, ${listMap.size} списков, ${labelMap.size} меток.`;
  if (errors.length) summary += `\n\nОшибки (${errors.length}):\n` + errors.slice(0, 20).join("\n");
  log(summary);
  return boardId;
}

// importNormalCard: classify by chgksuite markup, store the question/heading/meta
// text as the card description, assign its labels.
async function importNormalCard(boardId, listId, c, rank, enc, labelMap) {
  const desc = xyChgk.fixTrelloFormatting((c.desc && c.desc.trim()) ? c.desc : (c.name || ""));
  const kind = detectKind(desc);
  const res = await jpost(`/api/lists/${listId}/cards`, {
    description_enc: await enc(desc), rank, kind,
  });
  await assignLabels(res.id, c, labelMap);
}

// importTestCard: a test session. Date comes from the card name; testers (the
// card body) are preserved as a comment rather than parsed.
async function importTestCard(boardId, listId, c, rank, enc, labelMap) {
  const datetime = (c.name || "").trim() || "тест-сессия";
  const descJson = JSON.stringify({ datetime, players: [] });
  const res = await jpost(`/api/lists/${listId}/cards`, {
    description_enc: await enc(descJson), rank, kind: "test",
  });
  await assignLabels(res.id, c, labelMap);
  const testers = (c.desc || "").trim();
  if (testers) {
    await jpost(`/api/cards/${res.id}/comments`, { payload_enc: await enc("Тестировали: " + testers) });
  }
}

async function assignLabels(cardId, c, labelMap) {
  const ids = (c.labels || []).map((l) => labelMap.get(l.id)).filter((x) => x != null);
  if (ids.length) await jput(`/api/cards/${cardId}/labels`, { label_ids: ids });
}

// detectKind picks the xy card kind from the leading chgksuite marker.
function detectKind(desc) {
  const first = xyChgk.parseBlocks(desc)[0];
  if (first && first.type === "heading") return "heading";
  if (first && first.type === "meta") return "meta";
  return "question";
}

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  log("");
  const pass = document.getElementById("boardPass").value;
  const name = document.getElementById("boardName").value.trim();
  const file = document.getElementById("trelloFile").files[0];
  if (!file) {
    log("Выберите JSON-файл, экспортированный из Trello.");
    return;
  }
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) {
    log(passErr);
    return;
  }
  let board;
  try {
    board = JSON.parse(await file.text());
  } catch (err) {
    log("Не удалось разобрать JSON: " + err.message);
    return;
  }
  if (!board || !Array.isArray(board.lists)) {
    log("Это не похоже на экспорт доски Trello (нет массива lists).");
    return;
  }
  importBtn.disabled = true;
  try {
    const boardId = await runImport(board, name, pass);
    setTimeout(() => { window.location.href = `/board/${boardId}`; }, 1500);
  } catch (err) {
    setStatus("error");
    log("Импорт прерван: " + err.message);
    importBtn.disabled = false;
  }
});

(async () => { await xyApp.requireLogin(); })();
