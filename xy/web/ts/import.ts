// import.ts — import a Trello board into a new encrypted xy board.
//
// Two sources, one import core:
//  - Trello (primary): the user authorizes read access (Trello's implicit OAuth
//    flow), picks a board, and everything is pulled live via the Trello API —
//    lists, cards, labels, ALL comments (paginated past Trello's 1000-action
//    cap) and uploaded attachments (files + photos). Trello calls go through the
//    server proxy (/api/import/trello/proxy): xy's CSP is connect-src 'self' and
//    Trello's download endpoint has no CORS, so the browser can't call it direct.
//  - JSON file (fallback): a Trello "Export as JSON" file. Gets whatever it
//    contains — up to ~1000 comments, no attachments (their bytes aren't in it).
//
// Either way, every field is encrypted client-side under a fresh board key
// before it reaches the server (xy's at-rest encryption is unchanged); the proxy
// is only a transient passthrough to Trello.
//
// Conventions handled:
//  - lists whose name ends in "tests" become xy test-lists; their cards become
//    test cards (date from the card name, testers kept as a comment), and the
//    card's green/red labels are mapped to test_taken/test_missed kinds.
//  - other cards are classified via the chgksuite parser (heading/meta/question).
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";

const { fetchJSON, jpost, jput } = xyApp;
const { keyBetween } = xyRank;

// Public Trello app key (reused from chgksuite, the user's other project). It's
// public by design — it rides in the authorize URL. The implicit token flow
// needs no OAuth secret.
const TRELLO_KEY = "1d4fe71dd193855686196e7768aa4b05";

interface TrelloLabel { id: string; name?: string; color?: string | null }
interface TrelloAttachment { id: string; name?: string; fileName?: string; isUpload?: boolean; bytes?: number; mimeType?: string }
interface TrelloCard {
  id: string;
  name?: string;
  desc?: string;
  closed?: boolean;
  idList: string;
  pos?: number;
  labels?: TrelloLabel[];
  attachments?: TrelloAttachment[];
}
interface TrelloList { id: string; name?: string; closed?: boolean; pos?: number }
interface TrelloAction {
  id: string;
  type?: string;
  date?: string;
  data?: { text?: string; card?: { id?: string } };
  memberCreator?: { fullName?: string; username?: string };
}
interface TrelloBoard {
  name?: string;
  lists?: TrelloList[];
  cards?: TrelloCard[];
  labels?: TrelloLabel[];
  actions?: TrelloAction[];
}
interface TrelloBoardRef { id: string; name?: string; closed?: boolean }

interface CardComment { text: string; date: string; author: string }
interface ImportSource {
  board: TrelloBoard;
  commentsByCard: Map<string, CardComment[]>;
  downloadAttachment: (cardId: string, att: TrelloAttachment) => Promise<Uint8Array<ArrayBuffer> | null>;
}
type EncFn = (s: string) => Promise<string>;
interface Tally { comments: number; attachments: number }

function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`page is missing #${id}`);
  return node as T;
}

const errMsg = (e: unknown): string => (e instanceof Error ? e.message : String(e));

const statusNode = byId("status");
const msg = byId("importMessage");
const form = byId<HTMLFormElement>("importForm");
const importBtn = byId<HTMLButtonElement>("importBtn");

// «🎲»: fill + copy a fresh passphrase (see app.ts).
xyApp.wireGenPassphrase(
  byId("genPassBtn"), byId<HTMLInputElement>("boardPass"), xyCrypto.generatePassphrase,
);

function setStatus(s: string): void {
  statusNode.dataset.state = s;
}
// logPrefix labels every progress line when several boards are imported in a row
// ("Доска 2/7 «Синхрон»: …"); empty for a single board.
let logPrefix = "";
function log(line: string): void {
  msg.textContent = line ? logPrefix + line : "";
}
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

// ---- Trello label colors → hex. Green/red match xy's auto test labels so an
// imported package looks identical to one built in xy. ----
const TRELLO_COLORS: Record<string, string> = {
  green: "#3aa657", lime: "#51e898", yellow: "#f2d600", orange: "#ff9f1a",
  red: "#dd3322", purple: "#c377e0", blue: "#0079bf", sky: "#00c2e0",
  pink: "#ff78cb", black: "#344563", grey: "#b3bac5", gray: "#b3bac5",
};
function colorHex(c: string | null | undefined): string {
  if (!c) return "#b3bac5";
  const base = String(c).split("_")[0];
  return TRELLO_COLORS[base] || "#b3bac5";
}
const isGreen = (c: string | null | undefined): boolean => /^(green|lime)/.test(String(c || ""));
const isRed = (c: string | null | undefined): boolean => /^red/.test(String(c || ""));

// A list is a test-list if its name ends with "tests" (e.g. "harmony2025_tests").
const isTestList = (name: string | null | undefined): boolean => /tests$/i.test(String(name || "").trim());

const byPos = (a: { pos?: number }, b: { pos?: number }): number => (a.pos || 0) - (b.pos || 0);

// ======================= Trello API (via the server proxy) =======================

// proxyFetch GETs a Trello API path through our server, retrying on rate limits.
async function proxyFetch(token: string, path: string, params?: Record<string, string>): Promise<Response> {
  for (let attempt = 0; ; attempt++) {
    const res = await fetch("/api/import/trello/proxy", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token, path, params: params || {} }),
    });
    if (res.status === 429 && attempt < 6) {
      const wait = (Number(res.headers.get("Retry-After")) || 1) * 1000;
      await sleep(wait);
      continue;
    }
    return res;
  }
}

async function trelloGet(token: string, path: string, params?: Record<string, string>): Promise<unknown> {
  const res = await proxyFetch(token, path, params);
  if (!res.ok) throw new Error(`Trello ${res.status}`);
  return res.json();
}

// fetchAllComments walks /boards/{id}/actions?filter=commentCard past Trello's
// 1000-per-response cap using the `before` cursor (actions come newest→oldest;
// `before=<oldest id>` fetches the next older page). Returns a Map keyed by the
// Trello card id, each list ordered oldest→newest.
async function fetchAllComments(token: string, boardId: string): Promise<Map<string, CardComment[]>> {
  const byCard = new Map<string, CardComment[]>();
  let before: string | null = null;
  let total = 0;
  for (;;) {
    const params: Record<string, string> = {
      filter: "commentCard", limit: "1000",
      memberCreator: "true", memberCreator_fields: "fullName,username",
    };
    if (before) params.before = before;
    const page = (await trelloGet(token, `/boards/${boardId}/actions`, params)) as TrelloAction[];
    if (!Array.isArray(page) || page.length === 0) break;
    for (const a of page) {
      const cid = a.data && a.data.card && a.data.card.id;
      if (!cid) continue;
      const mc = a.memberCreator || {};
      if (!byCard.has(cid)) byCard.set(cid, []);
      byCard.get(cid)!.push({
        text: (a.data && a.data.text) || "",
        date: a.date || "",
        author: mc.fullName || mc.username || "",
      });
      total++;
    }
    log(`Загружаю комментарии из Trello… (${total})`);
    if (page.length < 1000) break;
    before = page[page.length - 1].id;
  }
  for (const arr of byCard.values()) arr.reverse();
  return byCard;
}

// trelloDownload fetches an uploaded attachment's bytes. The filename segment is
// cosmetic (Trello ignores it) but must not smuggle ".." past the proxy guard.
async function trelloDownload(token: string, cardId: string, att: TrelloAttachment): Promise<Uint8Array<ArrayBuffer>> {
  const safe = String(att.fileName || att.name || "file").replace(/[^\w.-]/g, "_").replace(/\.\./g, "_");
  const res = await proxyFetch(token, `/cards/${cardId}/attachments/${att.id}/download/${encodeURIComponent(safe)}`, {});
  if (!res.ok) throw new Error(`attachment ${res.status}`);
  return new Uint8Array(await res.arrayBuffer());
}

// trelloSource pulls a whole board (one nested GET) plus all its comments.
async function trelloSource(token: string, boardId: string): Promise<ImportSource> {
  const board = (await trelloGet(token, `/boards/${boardId}`, {
    fields: "name",
    lists: "all", list_fields: "all",
    cards: "all", card_fields: "all",
    card_attachments: "true", card_attachment_fields: "all",
    labels: "all", label_fields: "all",
  })) as TrelloBoard;
  const commentsByCard = await fetchAllComments(token, boardId);
  return {
    board,
    commentsByCard,
    downloadAttachment: (cardId, att) => trelloDownload(token, cardId, att),
  };
}

// fileSource reads a Trello "Export as JSON" file: comments come from its
// `actions` array (Trello caps that export at ~1000); attachments aren't in it.
function fileSource(board: TrelloBoard): ImportSource {
  const byCard = new Map<string, CardComment[]>();
  for (const a of (board.actions || [])) {
    if (a.type !== "commentCard") continue;
    const cid = a.data && a.data.card && a.data.card.id;
    if (!cid) continue;
    const mc = a.memberCreator || {};
    if (!byCard.has(cid)) byCard.set(cid, []);
    byCard.get(cid)!.push({
      text: (a.data && a.data.text) || "",
      date: a.date || "",
      author: mc.fullName || mc.username || "",
    });
  }
  for (const arr of byCard.values()) arr.reverse();
  return { board, commentsByCard: byCard, downloadAttachment: async () => null };
}

// ============================= import core =============================

async function runImport(source: ImportSource, name: string, pass: string): Promise<{ id: number; summary: string }> {
  const board = source.board;
  setStatus("saving");
  log("Создаю доску…");

  // 1. fresh board key + board row
  const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
  const boardName = name || board.name || "Импорт из Trello";
  const created = (await jpost("/api/boards", { ...keymeta, name: boardName })) as { id: number };
  const boardId = created.id;
  await xyCrypto.cacheDK(boardId, dk);

  const enc: EncFn = (s) => xyCrypto.encField(dk, s);

  // 2. lists (skip closed), remember test-ness, keep a trello→xy id map
  const openLists = (board.lists || []).filter((l) => !l.closed).sort(byPos);
  const listMap = new Map<string, { id: number; test: boolean }>(); // trelloListId -> { id, test }
  let listRank: string | null = null;
  for (const l of openLists) {
    const test = isTestList(l.name);
    listRank = keyBetween(listRank, null);
    const res = (await jpost(`/api/boards/${boardId}/lists`, {
      title_enc: await enc(l.name || "(без названия)"), rank: listRank, type: test ? "test" : "normal",
    })) as { id: number };
    listMap.set(l.id, { id: res.id, test });
  }

  // group open cards by their (open) list, in board order
  const cardsByList = new Map<string, TrelloCard[]>();
  for (const c of (board.cards || [])) {
    if (c.closed || !listMap.has(c.idList)) continue;
    if (!cardsByList.has(c.idList)) cardsByList.set(c.idList, []);
    cardsByList.get(c.idList)!.push(c);
  }
  for (const arr of cardsByList.values()) arr.sort(byPos);

  // 3. decide each label's kind: scan test-list cards, where a green label means
  // "взяли" (test_taken) and a red one "не взяли" (test_missed).
  const labelKind = new Map<string, string>(); // trelloLabelId -> 'normal' | 'test_taken' | 'test_missed'
  for (const l of (board.labels || [])) labelKind.set(l.id, "normal");
  for (const [listId, cards] of cardsByList) {
    if (!listMap.get(listId)!.test) continue;
    for (const c of cards) {
      for (const lab of (c.labels || [])) {
        if (isGreen(lab.color)) labelKind.set(lab.id, "test_taken");
        else if (isRed(lab.color)) labelKind.set(lab.id, "test_missed");
      }
    }
  }

  // 4. create labels, mapping trello→xy id
  const labelMap = new Map<string, number>(); // trelloLabelId -> xyLabelId
  for (const l of (board.labels || [])) {
    const nm = l.name || `метка (${l.color || "без цвета"})`;
    const res = (await jpost(`/api/boards/${boardId}/labels`, {
      name_enc: await enc(nm), color_enc: await enc(colorHex(l.color)), kind: labelKind.get(l.id) || "normal",
    })) as { id: number };
    labelMap.set(l.id, res.id);
  }

  // 5. cards (+ their comments and attachments)
  const total = [...cardsByList.values()].reduce((n, a) => n + a.length, 0);
  let done = 0;
  const tally: Tally = { comments: 0, attachments: 0 };
  const errors: string[] = [];
  for (const l of openLists) {
    const info = listMap.get(l.id)!;
    const cards = cardsByList.get(l.id) || [];
    let cardRank: string | null = null;
    for (const c of cards) {
      cardRank = keyBetween(cardRank, null);
      try {
        const cardId = info.test
          ? await importTestCard(boardId, info.id, c, cardRank, enc, labelMap)
          : await importNormalCard(boardId, info.id, c, cardRank, enc, labelMap);
        await importCardExtras(cardId, c, source, enc, dk, tally, errors);
      } catch (e) {
        errors.push(`«${c.name || c.id}»: ${errMsg(e)}`);
      }
      done++;
      log(`Импортировано ${done}/${total} карточек…`);
    }
  }

  setStatus("saved");
  let summary = `Готово: ${done} карточек, ${listMap.size} списков, ${labelMap.size} меток, `
    + `${tally.comments} комментариев, ${tally.attachments} вложений.`;
  if (errors.length) summary += `\n\nОшибки (${errors.length}):\n` + errors.slice(0, 20).join("\n");
  log(summary);
  return { id: boardId, summary };
}

// runImportAll imports every open Trello board, one xy board each, all under the
// same passphrase. The board-name field is ignored — names come from Trello. One
// board's failure never stops the rest; the final report lists every board.
async function runImportAll(token: string, pass: string): Promise<void> {
  const boards = openBoards.slice();
  const report: string[] = [];
  for (let i = 0; i < boards.length; i++) {
    const b = boards[i];
    logPrefix = `[${i + 1}/${boards.length}] «${b.name || b.id}» — `;
    try {
      log("загружаю из Trello…");
      const source = await trelloSource(token, b.id);
      const { summary } = await runImport(source, b.name || "", pass);
      report.push(`«${b.name || b.id}» — ${summary}`);
    } catch (err) {
      report.push(`«${b.name || b.id}» — НЕ ИМПОРТИРОВАНА: ${errMsg(err)}`);
    }
  }
  logPrefix = "";
  const failed = report.filter((r) => r.includes("НЕ ИМПОРТИРОВАНА")).length;
  setStatus(failed ? "error" : "saved");
  log(`Импортировано досок: ${boards.length - failed} из ${boards.length}.\n\n` + report.join("\n\n"));
}

// importNormalCard: classify by chgksuite markup, store the question/heading/meta
// text as the card description, assign its labels.
async function importNormalCard(boardId: number, listId: number, c: TrelloCard, rank: string, enc: EncFn, labelMap: Map<string, number>): Promise<number> {
  const desc = xyChgk.fixTrelloFormatting((c.desc && c.desc.trim()) ? c.desc : (c.name || ""));
  const kind = detectKind(desc);
  const res = (await jpost(`/api/lists/${listId}/cards`, {
    description_enc: await enc(desc), rank, kind,
  })) as { id: number };
  await assignLabels(res.id, c, labelMap);
  return res.id;
}

// importTestCard: a test session. Date comes from the card name; testers (the
// card body) are preserved as a comment rather than parsed.
async function importTestCard(boardId: number, listId: number, c: TrelloCard, rank: string, enc: EncFn, labelMap: Map<string, number>): Promise<number> {
  const datetime = (c.name || "").trim() || "тест-сессия";
  const descJson = JSON.stringify({ datetime, players: [] });
  const res = (await jpost(`/api/lists/${listId}/cards`, {
    description_enc: await enc(descJson), rank, kind: "test",
  })) as { id: number };
  await assignLabels(res.id, c, labelMap);
  const testers = (c.desc || "").trim();
  if (testers) {
    await jpost(`/api/cards/${res.id}/comments`, { payload_enc: await enc("Тестировали: " + testers) });
  }
  return res.id;
}

// importCardExtras carries a Trello card's comments (preserving author +
// timestamp) and uploaded attachments (files + photos) onto the new xy card.
// Comments and attachments never abort the card: each failure is recorded.
async function importCardExtras(xyCardId: number, c: TrelloCard, source: ImportSource, enc: EncFn, dk: DataKey, tally: Tally, errors: string[]): Promise<void> {
  // Comments — oldest→newest, re-encrypted, author name folded into the body
  // since Trello authors aren't xy users (author_user_id stays null).
  const cms = source.commentsByCard.get(c.id) || [];
  if (cms.length) {
    const comments: { author_user_id: null; created_at: string; payload_enc: string }[] = [];
    for (const cm of cms) {
      const body = xyChgk.fixTrelloFormatting(cm.text || "");
      const text = cm.author ? `${cm.author}:\n${body}` : body;
      comments.push({ author_user_id: null, created_at: cm.date || "", payload_enc: await enc(text) });
    }
    try {
      await jpost(`/api/cards/${xyCardId}/comments/import`, { comments });
      tally.comments += comments.length;
    } catch (e) {
      errors.push(`«${c.name || c.id}» комментарии: ${errMsg(e)}`);
    }
  }

  // Attachments — only uploaded files (links are external URLs, not files).
  for (const att of (c.attachments || [])) {
    if (!att.isUpload) continue;
    const nm = att.name || att.fileName || "файл";
    if (att.bytes && att.bytes > 50 * 1024 * 1024) {
      errors.push(`«${nm}»: файл больше 50 МБ, пропущен`);
      continue;
    }
    try {
      const bytes = await source.downloadAttachment(c.id, att);
      if (!bytes) continue; // source can't fetch bytes (file import)
      await uploadAttachment(xyCardId, nm, bytes, att.mimeType, dk);
      tally.attachments++;
    } catch (e) {
      errors.push(`«${nm}» вложение: ${errMsg(e)}`);
    }
  }
}

// uploadAttachment encrypts the plain bytes under the board key and POSTs them as
// a new xy attachment (mirrors board.ts#copyCardExtras).
async function uploadAttachment(xyCardId: number, name: string, bytes: Uint8Array<ArrayBuffer>, mime: string | undefined, dk: DataKey): Promise<void> {
  const recipher = await xyCrypto.encBytes(dk, bytes);
  const lossless = /^image\/(png|gif|webp|bmp|svg)/i.test(mime || "");
  const fd = new FormData();
  fd.append("meta", JSON.stringify({
    filename_enc: await xyCrypto.encField(dk, name),
    mime: mime || "application/octet-stream",
    lossless,
    event_payload_enc: await xyCrypto.encField(dk, JSON.stringify({ file: name })),
  }));
  fd.append("blob", new Blob([recipher], { type: "application/octet-stream" }), "blob");
  const res = await fetch(`/api/cards/${xyCardId}/attachments`, { method: "POST", credentials: "same-origin", body: fd });
  if (!res.ok) throw new Error(`upload ${res.status}`);
}

async function assignLabels(cardId: number, c: TrelloCard, labelMap: Map<string, number>): Promise<void> {
  const ids = (c.labels || []).map((l) => labelMap.get(l.id)).filter((x) => x != null);
  if (ids.length) await jput(`/api/cards/${cardId}/labels`, { label_ids: ids });
}

// detectKind picks the xy card kind from the leading chgksuite marker.
function detectKind(desc: string): string {
  const first = xyChgk.parseBlocks(desc)[0];
  if (first && first.type === "heading") return "heading";
  if (first && first.type === "meta") return "meta";
  return "question";
}

// ============================= Trello connect (OAuth) =============================

// No return_url: the chgksuite app key allows only wildcard origins, which Trello
// no longer accepts for redirects. So we use the manual flow — Trello displays
// the token, the user copies it and pastes it back (same as chgksuite).
function authorizeURL(): string {
  const p = new URLSearchParams({
    expiration: "1day", scope: "read", response_type: "token", name: "xy", key: TRELLO_KEY,
  });
  return "https://trello.com/1/authorize?" + p.toString();
}

// ALL_BOARDS is the picker's synthetic first option: import every open board,
// each into its own xy board (all sharing the one passphrase typed below).
const ALL_BOARDS = "__all__";
let openBoards: TrelloBoardRef[] = []; // the picker's boards, kept for the ALL_BOARDS run

async function loadBoards(token: string): Promise<void> {
  const boards = (await trelloGet(token, "/members/me/boards", { fields: "name,closed", filter: "open" })) as TrelloBoardRef[];
  const sel = byId<HTMLSelectElement>("trelloBoard");
  sel.innerHTML = "";
  openBoards = (boards || []).filter((b) => !b.closed);
  const option = (value: string, text: string): void => {
    const o = document.createElement("option");
    o.value = value;
    o.textContent = text;
    sel.appendChild(o);
  };
  if (!openBoards.length) {
    option("", "(нет открытых досок)");
    return;
  }
  if (openBoards.length > 1) option(ALL_BOARDS, `★ Все доски (${openBoards.length})`);
  for (const b of openBoards) option(b.id, b.name || b.id);
}

// stage switches the connect area between: "connect" (offer the button),
// "token" (paste the token Trello showed), "picker" (choose a board).
function stage(s: "connect" | "token" | "picker"): void {
  byId("trelloConnectBtn").hidden = s === "picker";
  byId("trelloTokenArea").hidden = s !== "token";
  byId("trelloPickArea").hidden = s !== "picker";
}

async function useToken(token: string): Promise<void> {
  sessionStorage.setItem("trelloToken", token);
  await loadBoards(token); // throws on a bad/expired token
  stage("picker");
  log("");
}

// ============================= wiring =============================

form.addEventListener("submit", async (e) => {
  e.preventDefault();
  log("");
  const pass = byId<HTMLInputElement>("boardPass").value;
  const name = byId<HTMLInputElement>("boardName").value.trim();
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) {
    log(passErr);
    return;
  }

  const token = sessionStorage.getItem("trelloToken");
  const boardSel = byId<HTMLSelectElement>("trelloBoard");
  const pickerActive = !byId("trelloPickArea").hidden;
  const file = byId<HTMLInputElement>("trelloFile").files?.[0];

  // "Все доски": import each open board in turn, under the one passphrase. A
  // board that fails is reported and the rest still go through.
  if (token && pickerActive && boardSel.value === ALL_BOARDS) {
    importBtn.disabled = true;
    await runImportAll(token, pass);
    importBtn.disabled = false;
    return;
  }

  let source: ImportSource;
  try {
    if (token && pickerActive && boardSel.value) {
      log("Загружаю доску из Trello…");
      source = await trelloSource(token, boardSel.value);
    } else if (file) {
      const board = JSON.parse(await file.text()) as TrelloBoard;
      if (!board || !Array.isArray(board.lists)) {
        log("Это не похоже на экспорт доски Trello (нет массива lists).");
        return;
      }
      source = fileSource(board);
    } else {
      log("Подключите Trello и выберите доску — или выберите JSON-файл ниже.");
      return;
    }
  } catch (err) {
    setStatus("error");
    log("Не удалось загрузить доску из Trello: " + errMsg(err));
    return;
  }

  importBtn.disabled = true;
  try {
    const { id } = await runImport(source, name, pass);
    setTimeout(() => { window.location.href = `/board/${id}`; }, 1500);
  } catch (err) {
    setStatus("error");
    log("Импорт прерван: " + errMsg(err));
    importBtn.disabled = false;
  }
});

(async () => {
  await xyApp.requireLogin();

  // Connect opens Trello's authorize page in a new tab and reveals the paste box.
  const connectBtn = byId<HTMLAnchorElement>("trelloConnectBtn");
  connectBtn.href = authorizeURL();
  connectBtn.target = "_blank";
  connectBtn.rel = "noopener";
  connectBtn.addEventListener("click", () => stage("token"));

  const tokenInput = byId<HTMLInputElement>("trelloTokenInput");
  const confirmToken = async (): Promise<void> => {
    const tok = tokenInput.value.trim();
    if (!tok) { log("Вставьте токен из Trello."); return; }
    try {
      await useToken(tok);
    } catch (e) {
      sessionStorage.removeItem("trelloToken");
      log("Токен не подошёл. Проверьте и вставьте снова.");
    }
  };
  byId("trelloTokenBtn").addEventListener("click", confirmToken);
  tokenInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); confirmToken(); }
  });

  byId("trelloResetBtn").addEventListener("click", () => {
    sessionStorage.removeItem("trelloToken");
    window.location.href = "/import";
  });

  // Returning within the session: reuse the stored token, else start at connect.
  const token = sessionStorage.getItem("trelloToken");
  if (token) {
    try { await useToken(token); }
    catch (e) { sessionStorage.removeItem("trelloToken"); stage("connect"); }
  } else {
    stage("connect");
  }
})();
