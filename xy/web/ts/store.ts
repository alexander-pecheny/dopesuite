// store.ts — xy's offline persistence layer (IndexedDB). Mirrors board
// ciphertext snapshots, the board list, per-card timelines and attachment bytes
// so a board can be opened and edited with no network, plus an ordered mutation
// **outbox** and the temp-id ↔ real-id map the sync engine (sync.ts) replays on
// reconnect. Everything stored here is the same bytes the server holds — ciphertext
// envelopes from crypto.ts, plus plaintext board names (the one un-encrypted field);
// no encrypted *content* is persisted in the clear.
//
// This module owns one IndexedDB database ("xy-offline"); crypto.ts owns a
// separate one ("xy-keys") for the cached data keys.

// ---- persisted record shapes ----

// The JSON body a queued mutation carries. Only the fields the sync engine
// itself inspects are named; anything else rides along untouched.
export interface OpBody {
  type?: string;
  title_enc?: string;
  rank?: string;
  kind?: string;
  description_enc?: string;
  handout_meta_enc?: string;
  alias_enc?: string;
  list_id?: number;
  name_enc?: string;
  color_enc?: string;
  label_ids?: number[];
  name?: string;
  payload_enc?: string;
  reply_to_id?: number | null;
  desc_event_enc?: string;
  events?: Array<{ type: string; payload_enc: string }>;
  [key: string]: unknown;
}

// A queued mutation op. board.js supplies {kind, method, path, body, board,
// mint}; enqueue stamps tempId/ts, and IndexedDB assigns seq on add.
export interface OutboxOp {
  kind: string;
  method: string;
  path: string;
  body?: OpBody | null;
  board?: number;
  mint?: boolean;
  tempId?: number;
  ts?: string;
  seq?: number;
}

// An op read back from the outbox always carries its autoIncrement key.
export type StoredOp = OutboxOp & { seq: number };

// Board snapshot (GET /api/boards/{id} shape), typed as far as the sync engine
// manipulates it; the rest of the payload rides through the index signature.
export interface SnapshotList {
  id: number;
  type?: string;
  title_enc?: string;
  rank?: string;
  [key: string]: unknown;
}
export interface SnapshotCard {
  id: number;
  list_id?: number;
  kind?: string;
  description_enc?: string;
  rank?: string;
  handout_meta_enc?: string | null;
  alias_enc?: string | null;
  [key: string]: unknown;
}
export interface SnapshotLabel {
  id: number;
  name_enc?: string;
  color_enc?: string;
  kind?: string;
  [key: string]: unknown;
}
export interface BoardSnapshot {
  name?: string;
  schema_version?: number;
  lists?: SnapshotList[];
  cards?: SnapshotCard[];
  labels?: SnapshotLabel[];
  card_labels?: Record<string, number[]>;
  [key: string]: unknown;
}

// Timeline event DTO (server shape; sync.ts synthesizes negative-id ones for
// pending ops).
export interface TimelineEvent {
  id: number;
  type: string;
  author_user_id: number | null;
  created_at: string;
  reply_to_id?: number | null;
  payload_enc?: string;
  [key: string]: unknown;
}

export interface AttachmentRecord {
  mime?: string;
  rev?: number;
  bytes: Uint8Array<ArrayBuffer>;
}

const DB_NAME = "xy-offline";
const DB_VERSION = 1;

// Object stores:
//   snapshots  key=boardId(number)   → board snapshot (GET /api/boards/{id} shape)
//   boardlist  key="boards"          → array of board summaries (GET /api/boards)
//   timeline   key=cardId(number)    → array of timeline event DTOs (ciphertext)
//   attachments key=attId(number)    → { mime, bytes:Uint8Array, rev }
//   outbox     keyPath="seq" auto    → queued mutation op
//   idmap      key=tempId(number<0)  → realId(number)
//   meta       key=string            → scalar (e.g. tempCounter)
const STORES = ["snapshots", "boardlist", "timeline", "attachments", "outbox", "idmap", "meta"];

let dbPromise: Promise<IDBDatabase> | null = null;
function db(): Promise<IDBDatabase> {
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
async function tx<T>(
  storeName: string,
  mode: IDBTransactionMode,
  fn: (store: IDBObjectStore) => T | Promise<T>,
): Promise<T> {
  const d = await db();
  return new Promise((resolve, reject) => {
    const t = d.transaction(storeName, mode);
    const store = t.objectStore(storeName);
    let result!: T;
    Promise.resolve(fn(store)).then((r) => { result = r; }).catch(reject);
    t.oncomplete = () => resolve(result);
    t.onerror = () => reject(t.error);
    t.onabort = () => reject(t.error);
  });
}

// req wraps an IDBRequest as a promise (for use inside a tx fn).
function req<T>(r: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

// ---- snapshots ----
const getSnapshot = (boardId: number | string): Promise<BoardSnapshot | undefined> =>
  tx("snapshots", "readonly", (s) => req<BoardSnapshot | undefined>(s.get(Number(boardId))));
const putSnapshot = (boardId: number | string, snap: BoardSnapshot): Promise<IDBValidKey> =>
  tx("snapshots", "readwrite", (s) => req(s.put(snap, Number(boardId))));
const deleteSnapshot = (boardId: number | string): Promise<undefined> =>
  tx("snapshots", "readwrite", (s) => req(s.delete(Number(boardId))));

// ---- board list ----
const getBoardList = (): Promise<unknown[] | undefined> =>
  tx("boardlist", "readonly", (s) => req<unknown[] | undefined>(s.get("boards")));
const putBoardList = (boards: unknown[]): Promise<IDBValidKey> =>
  tx("boardlist", "readwrite", (s) => req(s.put(boards, "boards")));

// ---- timeline ----
const getTimeline = (cardId: number | string): Promise<TimelineEvent[] | undefined> =>
  tx("timeline", "readonly", (s) => req<TimelineEvent[] | undefined>(s.get(Number(cardId))));
const putTimeline = (cardId: number | string, events: TimelineEvent[]): Promise<IDBValidKey> =>
  tx("timeline", "readwrite", (s) => req(s.put(events, Number(cardId))));

// ---- attachments (cached bytes for offline read) ----
const getAttachment = (attId: number | string): Promise<AttachmentRecord | undefined> =>
  tx("attachments", "readonly", (s) => req<AttachmentRecord | undefined>(s.get(Number(attId))));
const putAttachment = (attId: number | string, rec: AttachmentRecord): Promise<IDBValidKey> =>
  tx("attachments", "readwrite", (s) => req(s.put(rec, Number(attId))));

// ---- outbox ----
function addOp(op: OutboxOp): Promise<IDBValidKey> {
  return tx("outbox", "readwrite", (s) => req(s.add(op)));
}
function allOps(): Promise<StoredOp[]> {
  return tx("outbox", "readonly", (s) => req<StoredOp[]>(s.getAll())); // sorted by seq (the key)
}
function deleteOp(seq: number): Promise<undefined> {
  return tx("outbox", "readwrite", (s) => req(s.delete(seq)));
}
function putOp(op: StoredOp): Promise<IDBValidKey> {
  return tx("outbox", "readwrite", (s) => req(s.put(op)));
}
async function countOps(): Promise<number> {
  return tx("outbox", "readonly", (s) => req(s.count()));
}

// ---- idmap (temp → real) ----
const putIdMap = (tempId: number, realId: number): Promise<IDBValidKey> =>
  tx("idmap", "readwrite", (s) => req(s.put(Number(realId), Number(tempId))));
const allIdMap = (): Promise<Record<number, number>> =>
  tx("idmap", "readonly", async (s) => {
    const keys = await req(s.getAllKeys());
    const vals = await req<number[]>(s.getAll());
    const m: Record<number, number> = {};
    for (let i = 0; i < keys.length; i++) m[keys[i] as number] = vals[i];
    return m;
  });
const clearIdMap = (): Promise<undefined> => tx("idmap", "readwrite", (s) => req(s.clear()));

// ---- meta / temp-id counter ----
async function nextTempId(): Promise<number> {
  return tx("meta", "readwrite", async (s) => {
    const cur = (await req<number | undefined>(s.get("tempCounter"))) || 0;
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
