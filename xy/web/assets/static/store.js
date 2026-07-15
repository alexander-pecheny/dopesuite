// store.js — xy's offline persistence layer (IndexedDB). Mirrors board
// ciphertext snapshots, the board list, per-card timelines and attachment bytes
// so a board can be opened and edited with no network, plus an ordered mutation
// **outbox** and the temp-id ↔ real-id map the sync engine (sync.js) replays on
// reconnect. Everything stored here is the same bytes the server holds — ciphertext
// envelopes from crypto.js, plus plaintext board names (the one un-encrypted field);
// no encrypted *content* is persisted in the clear. See PLAN §8.
//
// This module owns one IndexedDB database ("xy-offline"); crypto.js owns a
// separate one ("xy-keys") for the cached data keys.

const DB_NAME = "xy-offline";
const DB_VERSION = 1;

// Object stores:
//   snapshots  key=boardId(number)   → board snapshot (GET /api/boards/{id} shape)
//   boardlist  key="boards"          → array of board summaries (GET /api/boards)
//   timeline   key=cardId(number)    → array of timeline event DTOs (ciphertext)
//   attachments key=attId(number)    → { meta, bytes:Uint8Array }
//   outbox     keyPath="seq" auto    → queued mutation op
//   idmap      key=tempId(number<0)  → realId(number)
//   meta       key=string            → scalar (e.g. tempCounter)
const STORES = ["snapshots", "boardlist", "timeline", "attachments", "outbox", "idmap", "meta"];

let dbPromise = null;
function db() {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const d = req.result;
      for (const name of STORES) {
        if (d.objectStoreNames.contains(name)) continue;
        if (name === "outbox") d.createObjectStore(name, { keyPath: "seq", autoIncrement: true });
        else d.createObjectStore(name);
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

// tx runs fn(store) inside a transaction and resolves with fn's return value
// once the transaction completes.
async function tx(storeName, mode, fn) {
  const d = await db();
  return new Promise((resolve, reject) => {
    const t = d.transaction(storeName, mode);
    const store = t.objectStore(storeName);
    let result;
    Promise.resolve(fn(store)).then((r) => { result = r; }).catch(reject);
    t.oncomplete = () => resolve(result);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error);
  });
}

// req wraps an IDBRequest as a promise (for use inside a tx fn).
function req(r) {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

// ---- snapshots ----
const getSnapshot = (boardId) => tx("snapshots", "readonly", (s) => req(s.get(Number(boardId))));
const putSnapshot = (boardId, snap) => tx("snapshots", "readwrite", (s) => req(s.put(snap, Number(boardId))));
const deleteSnapshot = (boardId) => tx("snapshots", "readwrite", (s) => req(s.delete(Number(boardId))));

// ---- board list ----
const getBoardList = () => tx("boardlist", "readonly", (s) => req(s.get("boards")));
const putBoardList = (boards) => tx("boardlist", "readwrite", (s) => req(s.put(boards, "boards")));

// ---- timeline ----
const getTimeline = (cardId) => tx("timeline", "readonly", (s) => req(s.get(Number(cardId))));
const putTimeline = (cardId, events) => tx("timeline", "readwrite", (s) => req(s.put(events, Number(cardId))));

// ---- attachments (cached bytes for offline read) ----
const getAttachment = (attId) => tx("attachments", "readonly", (s) => req(s.get(Number(attId))));
const putAttachment = (attId, rec) => tx("attachments", "readwrite", (s) => req(s.put(rec, Number(attId))));

// ---- outbox ----
function addOp(op) {
  return tx("outbox", "readwrite", (s) => req(s.add(op)));
}
function allOps() {
  return tx("outbox", "readonly", (s) => req(s.getAll())); // sorted by seq (the key)
}
function deleteOp(seq) {
  return tx("outbox", "readwrite", (s) => req(s.delete(seq)));
}
function putOp(op) {
  return tx("outbox", "readwrite", (s) => req(s.put(op)));
}
async function countOps() {
  return tx("outbox", "readonly", (s) => req(s.count()));
}

// ---- idmap (temp → real) ----
const putIdMap = (tempId, realId) => tx("idmap", "readwrite", (s) => req(s.put(Number(realId), Number(tempId))));
const allIdMap = () =>
  tx("idmap", "readonly", async (s) => {
    const keys = await req(s.getAllKeys());
    const vals = await req(s.getAll());
    const m = {};
    for (let i = 0; i < keys.length; i++) m[keys[i]] = vals[i];
    return m;
  });
const clearIdMap = () => tx("idmap", "readwrite", (s) => req(s.clear()));

// ---- meta / temp-id counter ----
async function nextTempId() {
  return tx("meta", "readwrite", async (s) => {
    const cur = (await req(s.get("tempCounter"))) || 0;
    const next = cur - 1; // temp ids are negative, descending
    await req(s.put(next, "tempCounter"));
    return next;
  });
}

export const xyStore = {
  getSnapshot, putSnapshot, deleteSnapshot,
  getBoardList, putBoardList,
  getTimeline, putTimeline,
  getAttachment, putAttachment,
  addOp, allOps, deleteOp, putOp, countOps,
  putIdMap, allIdMap, clearIdMap,
  nextTempId,
};

if (typeof window !== "undefined") window.xyStore = xyStore;
