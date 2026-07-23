// End-to-end exercise of the real sync.js + store.js against a minimal in-memory
// IndexedDB and a fake fetch. Verifies the whole offline→online lifecycle:
// queued creates get negative temp ids, dependent ops reference them, and on
// flush every temp id is remapped to the server-assigned real id before the
// request is sent. Globals must be installed BEFORE importing the modules.
import { test } from "node:test";
import assert from "node:assert/strict";

// ---- minimal IndexedDB shim (covers exactly what store.js uses) ----
function makeIDB() {
  const dbs = {};
  function fireSuccess(reqObj, result) {
    reqObj.result = result;
    queueMicrotask(() => { if (reqObj.onsuccess) reqObj.onsuccess(); });
  }
  class FakeStore {
    constructor(rec) { this.rec = rec; }
    _key(value, key) {
      if (this.rec.keyPath) {
        if (value[this.rec.keyPath] == null) value[this.rec.keyPath] = ++this.rec.autoSeq;
        return value[this.rec.keyPath];
      }
      return key;
    }
    get(key) { const r = {}; fireSuccess(r, this.rec.map.has(key) ? this.rec.map.get(key) : undefined); return r; }
    put(value, key) { const k = this._key(value, key); this.rec.map.set(k, value); const r = {}; fireSuccess(r, k); return r; }
    add(value, key) { return this.put(value, key); }
    delete(key) { this.rec.map.delete(key); const r = {}; fireSuccess(r, undefined); return r; }
    clear() { this.rec.map.clear(); const r = {}; fireSuccess(r, undefined); return r; }
    count() { const r = {}; fireSuccess(r, this.rec.map.size); return r; }
    getAll() {
      const keys = [...this.rec.map.keys()].sort((a, b) => (a > b ? 1 : a < b ? -1 : 0));
      const r = {}; fireSuccess(r, keys.map((k) => this.rec.map.get(k))); return r;
    }
    getAllKeys() {
      const keys = [...this.rec.map.keys()].sort((a, b) => (a > b ? 1 : a < b ? -1 : 0));
      const r = {}; fireSuccess(r, keys); return r;
    }
  }
  class FakeDB {
    constructor(stores) { this._stores = stores; this.objectStoreNames = { contains: (n) => n in stores }; }
    createObjectStore(name, opts) { this._stores[name] = { map: new Map(), keyPath: opts && opts.keyPath, autoSeq: 0 }; return new FakeStore(this._stores[name]); }
    transaction(name) {
      const tx = {};
      const self = this;
      tx.objectStore = (n) => new FakeStore(self._stores[n]);
      // oncomplete fires after all request microtasks drain (macrotask).
      setTimeout(() => { if (tx.oncomplete) tx.oncomplete(); }, 0);
      return tx;
    }
  }
  return {
    open(name) {
      const req = {};
      const existing = dbs[name];
      const stores = existing || {};
      const db = new FakeDB(stores);
      if (!existing) dbs[name] = stores;
      queueMicrotask(() => {
        if (!existing && req.onupgradeneeded) { req.result = db; req.onupgradeneeded(); }
        req.result = db;
        if (req.onsuccess) req.onsuccess();
      });
      return req;
    },
  };
}

globalThis.indexedDB = makeIDB();

// ---- fake fetch: assigns positive ids to creates, records what was sent ----
let nextRealId = 100;
const sent = [];
// Node 24 exposes a read-only `navigator`; replace it with a mutable stand-in so
// the test can toggle onLine.
const navObj = { onLine: true };
Object.defineProperty(globalThis, "navigator", { value: navObj, configurable: true, writable: true });
globalThis.fetch = async (path, init) => {
  const method = (init && init.method) || "GET";
  const body = init && init.body ? JSON.parse(init.body) : null;
  sent.push({ method, path, body });
  const isCreate = method === "POST" && !path.endsWith("/comments");
  const respBody = isCreate ? { id: ++nextRealId } : null;
  return {
    ok: true,
    status: respBody ? 200 : 204,
    headers: { get: (h) => (h.toLowerCase() === "content-type" && respBody ? "application/json" : "") },
    json: async () => respBody,
    text: async () => "",
  };
};

const { xySync } = await import("../web/assets/static/dist/sync.js");

test("offline creates queue with temp ids, flush remaps them to real ids", async () => {
  // Seed a mirror snapshot so applyToMirror has something to update.
  await xySync.saveSnapshot(1, { id: 1, role: "owner", name_enc: "N", lists: [], cards: [], labels: [], card_labels: {} });

  // ---- go offline ----
  navigator.onLine = false;

  const list = await xySync.mutate({ kind: "createList", method: "POST", path: "/api/boards/1/lists", body: { title_enc: "L", rank: "a0", type: "normal" }, board: 1, mint: true });
  assert.ok(list.id < 0, "list got a negative temp id");

  const card = await xySync.mutate({ kind: "createCard", method: "POST", path: `/api/lists/${list.id}/cards`, body: { description_enc: "D", rank: "a0", kind: "question" }, board: 1, mint: true });
  assert.ok(card.id < 0 && card.id !== list.id, "card got a distinct temp id");

  const label = await xySync.mutate({ kind: "createLabel", method: "POST", path: "/api/boards/1/labels", body: { name_enc: "LB", color_enc: "C", kind: "normal" }, board: 1, mint: true });
  // assign the (temp) label to the (temp) card
  await xySync.mutate({ kind: "setCardLabels", method: "PUT", path: `/api/cards/${card.id}/labels`, body: { label_ids: [label.id], events: [{ type: "label_add", payload_enc: "P" }] }, board: 1 });

  // nothing should have been sent while offline
  assert.equal(sent.length, 0, "no network calls while offline");

  // the mirror reflects the pending edits, with temp ids
  const mirror = await xySync.loadSnapshot(1);
  assert.equal(mirror.lists.length, 1);
  assert.equal(mirror.cards.length, 1);
  assert.equal(mirror.cards[0].list_id, list.id);
  assert.deepEqual(mirror.card_labels[card.id], [label.id]);
  assert.equal(await xySync.pendingCountForBoard(1), 4);

  // ---- come back online and flush ----
  navigator.onLine = true;
  await xySync.flush();

  assert.equal(await xySync.pendingCount(), 0, "outbox drained");

  // Verify the sent requests had temp ids substituted for real ones.
  const listReq = sent.find((s) => s.path === "/api/boards/1/lists");
  const cardReq = sent.find((s) => s.path.endsWith("/cards"));
  const labelReq = sent.find((s) => s.path === "/api/boards/1/labels");
  const setReq = sent.find((s) => s.path.endsWith("/labels") && s.method === "PUT");

  const listRealId = 101, cardRealId = 102, labelRealId = 103;
  assert.equal(cardReq.path, `/api/lists/${listRealId}/cards`, "card create points at the real list id");
  assert.equal(setReq.path, `/api/cards/${cardRealId}/labels`, "label-set points at the real card id");
  assert.deepEqual(setReq.body.label_ids, [labelRealId], "label id remapped in body");
  assert.ok(labelReq && listReq, "list + label creates were sent");
});

test("a desc edit made offline replays and surfaces in the pending timeline", async () => {
  navigator.onLine = false;
  // edit an existing (server) card 500
  await xySync.saveSnapshot(2, { id: 2, role: "owner", name_enc: "N", lists: [], cards: [{ id: 500, list_id: 1, kind: "question", description_enc: "old", rank: "a0" }], labels: [], card_labels: {} });
  await xySync.mutate({ kind: "patchCard", method: "PATCH", path: "/api/cards/500", body: { description_enc: "new", desc_event_enc: "DIFF" }, board: 2 });

  const tl = await xySync.timelineFor(500);
  assert.equal(tl.length, 1);
  assert.equal(tl[0].type, "desc_edit");
  assert.equal(tl[0].payload_enc, "DIFF");

  const mirror = await xySync.loadSnapshot(2);
  assert.equal(mirror.cards[0].description_enc, "new");

  navigator.onLine = true;
  await xySync.flush();
  assert.equal(await xySync.pendingCount(), 0);
});
