// sync.js — xy's offline sync engine. Turns every board mutation into an
// idempotent, replayable op: when online it goes straight to the server, when
// offline (or the request fails) it lands in an ordered outbox (store.js) and is
// flushed on reconnect.
//
// Identity model. Server ids are positive autoincrement integers. An entity
// created offline can't have one yet, so the engine mints a **negative temp id**
// and hands it back to the caller immediately. Negative ids flow transparently
// through board.js (which treats ids as opaque numbers). On flush, each create's
// response yields temp→real, recorded in the idmap; every later op has its temp
// id references (in the URL path and JSON body) rewritten to the real id before
// it is sent. After a board's queue drains, the UI reloads a fresh snapshot, so
// temp ids never outlive a sync.
//
// The pure functions (substitute*, applyOpToSnapshot, pendingTimeline) carry the
// tricky logic and take no globals, so jstest can exercise them without IndexedDB.
import { xyStore } from "./store.js";

// OfflineError marks a fetch that failed because the network is unreachable (vs.
// an HTTP error the server actively returned, which is a real failure).
class OfflineError extends Error {
  constructor(msg) { super(msg); this.name = "OfflineError"; }
}

// ---- pure: id substitution ----

// substituteValue deep-clones v, replacing any negative integer that has a
// mapping in `idmap` with its real id. Positive numbers (player ids, ranks are
// strings) and other values pass through untouched.
function substituteValue(v, idmap) {
  if (Array.isArray(v)) return v.map((x) => substituteValue(x, idmap));
  if (v && typeof v === "object") {
    const out = {};
    for (const [k, val] of Object.entries(v)) out[k] = substituteValue(val, idmap);
    return out;
  }
  if (typeof v === "number" && v < 0 && Object.prototype.hasOwnProperty.call(idmap, v)) {
    return idmap[v];
  }
  return v;
}

// substitutePath rewrites negative-id path segments (e.g. "/api/cards/-5") to
// their real ids using the idmap.
function substitutePath(path, idmap) {
  return path
    .split("/")
    .map((seg) => (/^-\d+$/.test(seg) && Object.prototype.hasOwnProperty.call(idmap, seg) ? String(idmap[seg]) : seg))
    .join("/");
}

// ---- pure: apply an op to a cached snapshot ----

// pathIds extracts the numeric ids embedded in a "/api/.../{id}/..." path.
function pathIds(path) {
  return path.split("/").filter((s) => /^-?\d+$/.test(s)).map(Number);
}

// applyOpToSnapshot mutates `snap` (a GET /api/boards/{id} payload) to reflect
// `op`, using `resultId` as the id of any newly-created entity. Keeps the local
// mirror current so a fresh offline open renders pending edits. Returns snap.
function applyOpToSnapshot(snap, op, resultId) {
  if (!snap) return snap;
  snap.lists = snap.lists || [];
  snap.cards = snap.cards || [];
  snap.labels = snap.labels || [];
  snap.card_labels = snap.card_labels || {};
  const body = op.body || {};
  const ids = pathIds(op.path);
  switch (op.kind) {
    case "createList":
      snap.lists.push({ id: resultId, type: body.type || "normal", title_enc: body.title_enc, rank: body.rank });
      break;
    case "patchList": {
      const l = snap.lists.find((x) => x.id === ids[0]);
      if (l) { if (body.title_enc != null) l.title_enc = body.title_enc; if (body.rank != null) l.rank = body.rank; }
      break;
    }
    case "deleteList": {
      const lid = ids[0];
      snap.lists = snap.lists.filter((x) => x.id !== lid);
      snap.cards = snap.cards.filter((c) => c.list_id !== lid);
      break;
    }
    case "createCard":
      snap.cards.push({
        id: resultId, list_id: ids[0], kind: body.kind || "normal",
        description_enc: body.description_enc, rank: body.rank,
        ...(body.handout_meta_enc ? { handout_meta_enc: body.handout_meta_enc } : {}),
        ...(body.alias_enc ? { alias_enc: body.alias_enc } : {}),
      });
      break;
    case "patchCard": {
      const c = snap.cards.find((x) => x.id === ids[0]);
      if (c) {
        if (body.description_enc != null) c.description_enc = body.description_enc;
        if (body.rank != null) c.rank = body.rank;
        if (body.list_id != null) c.list_id = body.list_id;
        if (body.kind != null) c.kind = body.kind;
        if (body.handout_meta_enc != null) {
          if (body.handout_meta_enc === "") delete c.handout_meta_enc;
          else c.handout_meta_enc = body.handout_meta_enc;
        }
        if (body.alias_enc != null) {
          if (body.alias_enc === "") delete c.alias_enc;
          else c.alias_enc = body.alias_enc;
        }
      }
      break;
    }
    case "deleteCard": {
      const cid = ids[0];
      snap.cards = snap.cards.filter((x) => x.id !== cid);
      delete snap.card_labels[cid];
      break;
    }
    case "createLabel":
      snap.labels.push({ id: resultId, name_enc: body.name_enc, color_enc: body.color_enc, kind: body.kind || "normal" });
      break;
    case "patchLabel": {
      const l = snap.labels.find((x) => x.id === ids[0]);
      if (l) { if (body.name_enc != null) l.name_enc = body.name_enc; if (body.color_enc != null) l.color_enc = body.color_enc; }
      break;
    }
    case "deleteLabel": {
      const lid = ids[0];
      snap.labels = snap.labels.filter((x) => x.id !== lid);
      for (const k of Object.keys(snap.card_labels)) {
        snap.card_labels[k] = (snap.card_labels[k] || []).filter((id) => id !== lid);
      }
      break;
    }
    case "setCardLabels":
      snap.card_labels[ids[0]] = (body.label_ids || []).slice();
      break;
    case "patchBoard":
      // Board names are plaintext now; a rename bumps the board to schema_version 2.
      if (body.name != null) { snap.name = body.name; snap.schema_version = 2; }
      break;
    default:
      break; // comment / no snapshot effect
  }
  return snap;
}

// ---- pure: synthesize pending timeline events from queued ops ----

// pendingTimeline builds timeline-event DTOs (matching the server's shape) for a
// card from queued ops that carry payloads, so the card view shows un-synced
// comments/edits/label changes. Ids are negative and ordered after server events.
function pendingTimeline(ops, cardId) {
  const out = [];
  let n = -1;
  for (const op of ops) {
    const ids = pathIds(op.path);
    if (ids[0] !== cardId) continue;
    const body = op.body || {};
    const when = op.ts || "";
    if (op.kind === "comment" && body.payload_enc) {
      // reply_to_id is always a REAL (synced) id — a reply can only be composed
      // from a rendered thread, and un-synced comments have no thread affordance
      // — so it needs no temp-id remapping and is carried through as-is.
      out.push({
        id: n--, type: "comment", author_user_id: null, created_at: when,
        reply_to_id: body.reply_to_id != null ? body.reply_to_id : undefined,
        payload_enc: body.payload_enc,
      });
    } else if (op.kind === "patchCard" && body.desc_event_enc) {
      out.push({ id: n--, type: "desc_edit", author_user_id: null, created_at: when, payload_enc: body.desc_event_enc });
    } else if (op.kind === "setCardLabels" && Array.isArray(body.events)) {
      for (const ev of body.events) {
        out.push({ id: n--, type: ev.type, author_user_id: null, created_at: when, payload_enc: ev.payload_enc });
      }
    }
  }
  return out;
}

// ---- network ----

// rawSend issues the actual HTTP request. Throws OfflineError when the fetch
// itself fails (no network); throws a normal Error with .httpStatus on a non-2xx
// response (the server rejected it).
async function rawSend(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: body != null ? { "Content-Type": "application/json" } : undefined,
      body: body != null ? JSON.stringify(body) : undefined,
    });
  } catch (e) {
    throw new OfflineError(e.message || "network error");
  }
  if (!res.ok) {
    const text = (await res.text()).trim();
    const err = new Error(text || `HTTP ${res.status}`);
    err.httpStatus = res.status;
    throw err;
  }
  if (res.status === 204) return null;
  const ct = res.headers.get("Content-Type") || "";
  return ct.includes("json") ? res.json() : null;
}

// ---- status broadcasting ----

const listeners = new Set();
let pending = 0; // outbox length (cached for synchronous status reads)
const deadletters = []; // ops the server rejected (surfaced once)

function isOnline() {
  return typeof navigator === "undefined" ? true : navigator.onLine !== false;
}
function status() {
  return { online: isOnline(), pending, syncing: flushing, deadletters: deadletters.slice() };
}
function onStatus(cb) { listeners.add(cb); cb(status()); return () => listeners.delete(cb); }
function emit() { const st = status(); for (const cb of listeners) { try { cb(st); } catch (_) {} } }

// ---- the engine ----

let flushing = false;
const boardListeners = new Set(); // notified (with boardId) when a board's queue drains

function onBoardSynced(cb) { boardListeners.add(cb); return () => boardListeners.delete(cb); }
function emitBoardSynced(boardId) { for (const cb of boardListeners) { try { cb(boardId); } catch (_) {} } }

// applyToMirror updates the cached snapshot for op.board, if one exists.
async function applyToMirror(op, resultId) {
  if (!op.board) return;
  const snap = await xyStore.getSnapshot(op.board);
  if (!snap) return;
  applyOpToSnapshot(snap, op, resultId);
  await xyStore.putSnapshot(op.board, snap);
}

// enqueue persists an op, allocating a temp id for create ops. Returns the temp
// id (or null). Updates the local mirror so reloads stay consistent.
async function enqueue(op) {
  let tempId = null;
  if (op.mint) { tempId = await xyStore.nextTempId(); op.tempId = tempId; }
  op.ts = op.ts || nowISO();
  await xyStore.addOp(op);
  pending = await xyStore.countOps();
  await applyToMirror(op, tempId);
  emit();
  scheduleFlush();
  return tempId;
}

// nowISO returns an ISO timestamp; the engine never depends on it for ordering
// (the outbox seq does that) — only for displaying pending timeline events.
function nowISO() {
  try { return new Date().toISOString(); } catch (_) { return ""; }
}

// mutate is the single entry point board.js uses for every board mutation.
//   { kind, method, path, body, board, mint }
// Returns { id } — a real id when sent immediately, a negative temp id when
// queued. Non-create ops return { id: null }.
async function mutate(op) {
  // Preserve ordering: if anything is already queued we must queue too, even
  // when online, so dependent ops never overtake their creates.
  const queued = pending > 0 || (await xyStore.countOps()) > 0;
  if (isOnline() && !queued) {
    try {
      const res = await rawSend(op.method, op.path, op.body);
      await applyToMirror(op, res && res.id);
      return { id: res && res.id != null ? res.id : null };
    } catch (e) {
      if (!(e instanceof OfflineError)) throw e; // real server error → surface to caller
      // fell offline mid-request → fall through to enqueue
    }
  }
  const tempId = await enqueue(op);
  return { id: tempId };
}

let flushScheduled = false;
function scheduleFlush() {
  if (flushScheduled) return;
  flushScheduled = true;
  Promise.resolve().then(() => { flushScheduled = false; flush(); });
}

// flush drains the outbox in order while online, remapping temp ids as creates
// resolve. Stops on the first OfflineError (retry later); drops ops the server
// rejects to a dead-letter list so the queue can't wedge.
async function flush() {
  if (flushing || !isOnline()) return;
  flushing = true;
  emit();
  const drainedBoards = new Set();
  try {
    let idmap = await xyStore.allIdMap();
    // negative-string keys for path substitution
    const idmapStr = {};
    for (const [k, v] of Object.entries(idmap)) idmapStr["-" + Math.abs(Number(k))] = v;

    while (isOnline()) {
      const ops = await xyStore.allOps();
      if (!ops.length) break;
      const op = ops[0];
      const path = substitutePath(op.path, idmapStr);
      const body = op.body ? substituteValue(op.body, idmap) : op.body;
      let res;
      try {
        res = await rawSend(op.method, path, body);
      } catch (e) {
        if (e instanceof OfflineError) break; // network dropped — resume on reconnect
        // server rejected this op: drop it, record, keep going
        deadletters.push({ kind: op.kind, path, error: e.message });
        try { console.warn("xy sync: server rejected queued op, dropping", op.kind, path, e.message); } catch (_) {}
        await xyStore.deleteOp(op.seq);
        if (op.board) drainedBoards.add(op.board);
        pending = await xyStore.countOps();
        emit();
        continue;
      }
      if (op.mint && res && res.id != null && op.tempId != null) {
        idmap[op.tempId] = res.id;
        idmapStr["-" + Math.abs(op.tempId)] = res.id;
        await xyStore.putIdMap(op.tempId, res.id);
      }
      await xyStore.deleteOp(op.seq);
      if (op.board) drainedBoards.add(op.board);
      pending = await xyStore.countOps();
      emit();
    }

    if (pending === 0) await xyStore.clearIdMap();
  } finally {
    flushing = false;
    emit();
  }
  // Tell the UI which boards fully reconciled, so it can reload real ids.
  if (pending === 0) for (const b of drainedBoards) emitBoardSynced(b);
}

// ---- snapshot mirror helpers (used by board.js) ----

async function saveSnapshot(boardId, snap) { await xyStore.putSnapshot(boardId, snap); }
async function loadSnapshot(boardId) { return xyStore.getSnapshot(boardId); }
async function pendingCount() { pending = await xyStore.countOps(); return pending; }
async function pendingCountForBoard(board) {
  const ops = await xyStore.allOps();
  return ops.filter((o) => o.board === board).length;
}
async function pendingOps() { return xyStore.allOps(); }

// timelineFor returns the cached server timeline for a card merged with any
// pending (un-synced) events derived from the outbox, oldest→newest.
async function timelineFor(cardId) {
  const cached = (await xyStore.getTimeline(cardId)) || [];
  const ops = await xyStore.allOps();
  return cached.concat(pendingTimeline(ops, Number(cardId)));
}
async function cacheTimeline(cardId, events) { await xyStore.putTimeline(cardId, events); }

// ---- lifecycle ----

let started = false;
function start() {
  if (started || typeof window === "undefined") return;
  started = true;
  window.addEventListener("online", () => { emit(); flush(); });
  window.addEventListener("offline", () => emit());
  pendingCount().then(emit);
  flush();
}

export const xySync = {
  mutate, flush, start,
  isOnline, status, onStatus, onBoardSynced,
  saveSnapshot, loadSnapshot, pendingCount, pendingCountForBoard, pendingOps,
  timelineFor, cacheTimeline,
  getAttachment: xyStore.getAttachment, putAttachment: xyStore.putAttachment,
  getBoardList: xyStore.getBoardList, putBoardList: xyStore.putBoardList,
  // pure helpers (exported for tests)
  _substituteValue: substituteValue, _substitutePath: substitutePath,
  _applyOpToSnapshot: applyOpToSnapshot, _pendingTimeline: pendingTimeline,
  OfflineError,
};

if (typeof window !== "undefined") window.xySync = xySync;
