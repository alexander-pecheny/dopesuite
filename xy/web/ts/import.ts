// import.ts — import a Trello board into a new encrypted xy board.
//
// Two sources, one producer: each becomes a Bundle (trelloBundle), and every
// write is applyBundle's — the same path an archive takes (ADR-0014).
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
//    test cards (date from the card name, testers kept as a comment).
//  - other cards are mapped by trellomodel.js: title → alias, body → 4s text,
//    kind from its chgksuite markers, description history → desc_edit events.
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xyRank } from "./rank.js";
import { xyChgk } from "./chgk.js";
import { xyTrello } from "./trellomodel.js";
import type { RawDescEdit } from "./trellomodel.js";
import { importBundle, createBoardFromBundle } from "./bundleimport.js";
import { attachmentPath, BUNDLE_FORMAT } from "./bundle.js";
import type { Bundle, BundleAttachment, BundleCard, BundleCardLabel, BundleEvent, BundleLabel, BundleList } from "./bundle.js";
import type { AttachmentBytes } from "./bundleapply.js";

const { fetchJSON } = xyApp;
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
  data?: { text?: string; card?: { id?: string }; old?: { desc?: string } };
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
interface History {
  comments: Map<string, CardComment[]>;
  descEdits: Map<string, RawDescEdit[]>;
}
interface ImportSource {
  board: TrelloBoard;
  history: History;
  downloadAttachment: (cardId: string, att: TrelloAttachment) => Promise<Uint8Array<ArrayBuffer> | null>;
}

const { byId, errMsg } = xyApp;

const statusNode = byId("status");
const msg = byId("importMessage");
const form = byId<HTMLFormElement>("importForm");
const importBtn = byId<HTMLButtonElement>("importBtn");

// The passphrase field starts filled (see app.ts). Nothing here is a user
// gesture, so nothing is copied until the dice is clicked.
const passSetup = xyApp.wirePassphraseSetup({
  input: byId<HTMLInputElement>("boardPass"),
  dice: byId("genPassBtn"),
  copied: byId("importPassCopied"),
  saved: byId<HTMLInputElement>("importPassSaved"),
  submit: importBtn,
}, xyCrypto.generatePassphrase);
void passSetup.roll(false);

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

// collectActions folds Trello actions into the two per-card histories xy keeps:
// comments and description edits. Trello has no "description was edited" action
// — every card change is an updateCard, and a description edit is one carrying
// the replaced text in data.old.desc.
function collectActions(actions: TrelloAction[] | undefined, history: History): void {
  for (const a of (actions || [])) {
    const data = a.data || {};
    const cid = data.card && data.card.id;
    if (!cid) continue;
    const mc = a.memberCreator || {};
    const author = mc.fullName || mc.username || "";
    if (a.type === "commentCard") {
      push(history.comments, cid, { text: data.text || "", date: a.date || "", author });
    } else if (a.type === "updateCard" && typeof (data.old || {}).desc === "string") {
      push(history.descEdits, cid, { before: data.old!.desc!, date: a.date || "", author });
    }
  }
}

function push<T>(map: Map<string, T[]>, key: string, item: T): void {
  if (!map.has(key)) map.set(key, []);
  map.get(key)!.push(item);
}

// emptyHistory: comments end up oldest→newest, description edits stay newest
// first — the order xyTrello.descEdits walks the chain of replaced texts in.
const emptyHistory = (): History => ({ comments: new Map(), descEdits: new Map() });

// fetchHistory walks /boards/{id}/actions past Trello's 1000-per-response cap
// using the `before` cursor (actions come newest→oldest; `before=<oldest id>`
// fetches the next older page).
async function fetchHistory(token: string, boardId: string): Promise<History> {
  const history = emptyHistory();
  let before: string | null = null;
  let seen = 0;
  for (;;) {
    const params: Record<string, string> = {
      filter: "commentCard,updateCard", limit: "1000",
      memberCreator: "true", memberCreator_fields: "fullName,username",
    };
    if (before) params.before = before;
    const page = (await trelloGet(token, `/boards/${boardId}/actions`, params)) as TrelloAction[];
    if (!Array.isArray(page) || page.length === 0) break;
    collectActions(page, history);
    seen += page.length;
    log(`Загружаю историю из Trello… (${seen} событий)`);
    if (page.length < 1000) break;
    before = page[page.length - 1].id;
  }
  for (const arr of history.comments.values()) arr.reverse();
  return history;
}

// trelloDownload fetches an uploaded attachment's bytes. The filename segment is
// cosmetic (Trello ignores it) but must not smuggle ".." past the proxy guard.
async function trelloDownload(token: string, cardId: string, att: TrelloAttachment): Promise<Uint8Array<ArrayBuffer>> {
  const safe = String(att.fileName || att.name || "file").replace(/[^\w.-]/g, "_").replace(/\.\./g, "_");
  const res = await proxyFetch(token, `/cards/${cardId}/attachments/${att.id}/download/${encodeURIComponent(safe)}`, {});
  if (!res.ok) throw new Error(`attachment ${res.status}`);
  return new Uint8Array(await res.arrayBuffer());
}

// trelloSource pulls a whole board (one nested GET) plus all its history.
async function trelloSource(token: string, boardId: string): Promise<ImportSource> {
  const board = (await trelloGet(token, `/boards/${boardId}`, {
    fields: "name",
    lists: "all", list_fields: "all",
    cards: "all", card_fields: "all",
    card_attachments: "true", card_attachment_fields: "all",
    labels: "all", label_fields: "all",
  })) as TrelloBoard;
  const history = await fetchHistory(token, boardId);
  return { board, history, downloadAttachment: (cardId, att) => trelloDownload(token, cardId, att) };
}

// fileSource reads a Trello "Export as JSON" file: history comes from its
// `actions` array (Trello caps that export at ~1000); attachments aren't in it.
function fileSource(board: TrelloBoard): ImportSource {
  const history = emptyHistory();
  collectActions(board.actions, history);
  for (const arr of history.comments.values()) arr.reverse();
  return { board, history, downloadAttachment: async () => null };
}

// ============================= Trello → Bundle =============================

// trelloBundle is the Trello producer (ADR-0014): a board pulled from the API
// (or read out of a JSON export) becomes a plain Bundle, which applyBundle then
// writes exactly as it writes one out of an archive. Trello's string ids become
// the Bundle's own numbering; nothing here touches the server.
function trelloBundle(source: ImportSource, name: string): { bundle: Bundle; bytesOf: AttachmentBytes } {
  const board = source.board;
  let nextId = 0;
  const id = (): number => ++nextId;

  const openLists = (board.lists || []).filter((l) => !l.closed).sort(byPos);
  const lists: BundleList[] = [];
  const listOf = new Map<string, { id: number; test: boolean }>();
  let listRank: string | null = null;
  for (const l of openLists) {
    const test = isTestList(l.name);
    listRank = keyBetween(listRank, null);
    const row = { id: id(), type: test ? "test" : "normal", title: l.name || "(без названия)", rank: listRank, group_id: null };
    lists.push(row);
    listOf.set(l.id, { id: row.id, test });
  }

  const labels: BundleLabel[] = [];
  const labelOf = new Map<string, number>();
  for (const l of (board.labels || [])) {
    const row = { id: id(), name: l.name || `метка (${l.color || "без цвета"})`, color: colorHex(l.color) };
    labels.push(row);
    labelOf.set(l.id, row.id);
  }

  const cardsByList = new Map<string, TrelloCard[]>();
  for (const c of (board.cards || [])) {
    if (c.closed || !listOf.has(c.idList)) continue;
    if (!cardsByList.has(c.idList)) cardsByList.set(c.idList, []);
    cardsByList.get(c.idList)!.push(c);
  }
  for (const arr of cardsByList.values()) arr.sort(byPos);

  const cards: BundleCard[] = [];
  const cardLabels: BundleCardLabel[] = [];
  const timeline: BundleEvent[] = [];
  const attachments: BundleAttachment[] = [];
  const downloads = new Map<string, { cardId: string; att: TrelloAttachment }>();

  // Trello authors are not xy users, so their names fold into the payload and
  // `author` stays null — the same choice the live import always made.
  const event = (e: Omit<BundleEvent, "id" | "author" | "edited_at" | "is_excerpt" | "reply_to_id">): void => {
    timeline.push({ id: id(), author: null, edited_at: null, is_excerpt: false, reply_to_id: null, ...e });
  };

  for (const l of openLists) {
    const info = listOf.get(l.id)!;
    let cardRank: string | null = null;
    for (const c of (cardsByList.get(l.id) || [])) {
      cardRank = keyBetween(cardRank, null);
      const cardId = id();
      if (info.test) {
        // A test list's card is the legacy test-session shape: the date is its
        // title, and the testers stay as a comment rather than being parsed.
        cards.push({
          id: cardId, list_id: info.id, kind: "test", rank: cardRank,
          description: JSON.stringify({ datetime: (c.name || "").trim() || "тест-сессия", players: [] }),
          handout_meta: null, alias: null, created_at: null,
        });
        const testers = (c.desc || "").trim();
        if (testers) {
          event({ card_id: cardId, session_id: null, type: "comment", created_at: new Date().toISOString(), payload: "Тестировали: " + testers });
        }
      } else {
        const { desc, alias, kind } = xyTrello.mapCard(c.name, c.desc);
        cards.push({ id: cardId, list_id: info.id, kind, rank: cardRank, description: desc, handout_meta: null, alias: alias || null, created_at: null });
        // Description history is a question's editing record; on a heading or a
        // note it is noise, so only question cards carry it over.
        if (kind === "question") {
          for (const e of xyTrello.descEdits(source.history.descEdits.get(c.id), desc)) {
            event({
              card_id: cardId, session_id: null, type: "desc_edit", created_at: e.date,
              payload: JSON.stringify({ before: e.before, after: e.after, author: e.author }),
            });
          }
        }
      }
      for (const lab of (c.labels || [])) {
        const lid = labelOf.get(lab.id);
        if (lid != null) cardLabels.push({ card_id: cardId, label_id: lid, session_id: null });
      }
      for (const cm of (source.history.comments.get(c.id) || [])) {
        const body = xyChgk.fixTrelloFormatting(cm.text || "");
        event({
          card_id: cardId, session_id: null, type: "comment",
          created_at: cm.date || new Date().toISOString(),
          payload: cm.author ? `${cm.author}:\n${body}` : body,
        });
      }
      for (const att of (c.attachments || [])) {
        if (!att.isUpload) continue;
        const nm = att.name || att.fileName || "файл";
        if (att.bytes && att.bytes > 50 * 1024 * 1024) continue; // the server would refuse it anyway
        const aid = id();
        attachments.push({
          id: aid, card_id: cardId, filename: nm, mime: att.mimeType || "application/octet-stream",
          size: att.bytes || 0, lossless: false, is_excerpt: false, path: attachmentPath(aid, nm),
        });
        downloads.set(String(aid), { cardId: c.id, att });
      }
    }
  }

  timeline.sort((a, b) => String(a.created_at).localeCompare(String(b.created_at)));

  const bundle: Bundle = {
    format: BUNDLE_FORMAT,
    exported_at: new Date().toISOString(),
    board: { name: name || board.name || "Импорт из Trello" },
    members: [], lists, groups: [], cards, labels, sessions: [],
    card_labels: cardLabels, card_sessions: [], tour_testers: [],
    timeline, attachments,
  };
  // A file source has no bytes to give and a live download may simply fail —
  // either way the attachment is skipped and named, never fatal to its list.
  const bytesOf: AttachmentBytes = async (a) => {
    const d = downloads.get(String(a.id));
    if (!d) return null;
    try { return await source.downloadAttachment(d.cardId, d.att); } catch { return null; }
  };
  return { bundle, bytesOf };
}

async function runImport(source: ImportSource, name: string, pass: string): Promise<{ id: number; summary: string }> {
  setStatus("saving");
  const { bundle, bytesOf } = trelloBundle(source, name);
  const out = await createBoardFromBundle(bundle, bytesOf, bundle.board.name, pass, log);
  setStatus("saved");
  return out;
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
  const bundleFile = byId<HTMLInputElement>("bundleFile").files?.[0];

  // A Board Bundle from another xy instance (ADR-0013) — its own import path,
  // sharing only the name/passphrase fields with the Trello flows.
  if (bundleFile) {
    importBtn.disabled = true;
    setStatus("saving");
    try {
      const { id } = await importBundle(bundleFile, name, pass, log);
      setStatus("saved");
      setTimeout(() => { window.location.href = `/board/${id}`; }, 1500);
    } catch (err) {
      setStatus("error");
      log("Импорт прерван: " + errMsg(err));
      importBtn.disabled = false;
    }
    return;
  }

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
