// Tests for unlock.js — the board boot/unlock/load flow lifted out of board.js.
// Everything the flow touches (DOM nodes, crypto, sync, network, callbacks) is
// injected, so the tests run with plain-object fakes: no DOM, no IndexedDB.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createUnlock } from "../web/assets/static/dist/unlock.js";

const SNAP = {
  role: "owner",
  schema_version: 2,
  name: "Доска",
  sizes: null,
  default_author: "Автор",
  card_title: "answer",
  card_labels: { 2: [3] },
  unread: { 2: { comments: true } },
  lists: [{ id: 1, type: "normal", rank: "a", group_id: 5, title_enc: "e:list" }],
  groups: [{ id: 5, name_enc: "e:group" }],
  cards: [{ id: 2, list_id: 1, kind: "question", rank: "b", description_enc: "e:desc", handout_meta_enc: "e:meta", alias_enc: null, created_at: "2026-01-01" }],
  labels: [{ id: 3, kind: "normal", name_enc: "e:lname", color_enc: "e:lcolor" }],
};

const GOOD_PASS = "correct horse battery staple";

// makeDeps builds a full UnlockDeps object of fakes plus a call log.
// opts: cachedDK, online (default true), pending, mirror, snap, fetchFails, loggedOut.
function makeDeps(opts = {}) {
  const dk = { key: "K", raw: new Uint8Array([1, 2, 3]) };
  const calls = {
    fetches: [], jposts: [], status: [], syncStatus: [], states: [], dks: [],
    saved: [], sizes: [], unavailable: 0, cacheDK: null, started: 0,
  };
  const ui = {
    overlay: { hidden: true },
    form: { handlers: {}, addEventListener(type, h) { this.handlers[type] = h; } },
    pass: { value: "", focused: 0, focus() { this.focused++; } },
    message: { textContent: "" },
  };
  const deps = {
    boardId: 7,
    ui,
    crypto: {
      loadCachedDK: async () => opts.cachedDK || null,
      unlockBoard: async (pass, _keymeta) => {
        if (pass !== GOOD_PASS) throw new Error("Неверный пароль доски");
        return dk;
      },
      cacheDK: async (id, k) => { calls.cacheDK = [id, k]; },
      decField: async (_k, b64) => "d:" + b64,
    },
    sync: {
      start() { calls.started++; },
      onStatus(cb) { calls.onStatusCb = cb; cb({ online: true, pending: 0, syncing: false, deadletters: [] }); },
      onBoardSynced(cb) { calls.boardSyncedCb = cb; },
      isOnline: () => opts.online !== false,
      pendingCountForBoard: async () => opts.pending || 0,
      saveSnapshot: async (_id, snap) => { calls.saved.push(snap); },
      loadSnapshot: async () => (opts.mirror !== undefined ? structuredClone(opts.mirror) : undefined),
    },
    net: {
      requireLogin: async () => (opts.loggedOut ? null : opts.online === false ? { offline: true } : { user_id: 1 }),
      fetchJSON: async (url) => {
        calls.fetches.push(url);
        if (opts.fetchFails) throw new TypeError("network down");
        if (url === "/api/boards/7/keymeta") return { kdf_salt: "s", kdf_params: "{}", wrapped_key: "w", verify_token: "v" };
        if (url === "/api/boards/7") return structuredClone(opts.snap || SNAP);
        throw new Error("unexpected fetch " + url);
      },
      jpost: async (url, body) => { calls.jposts.push([url, body]); return null; },
    },
    status: {
      set: (s) => calls.status.push(s),
      onSync: (st) => calls.syncStatus.push(st),
    },
    applySizes: (s) => calls.sizes.push(s),
    onDK: (k) => calls.dks.push(k),
    onState: (state, info) => calls.states.push({ state, info }),
    onUnavailable: () => calls.unavailable++,
  };
  return { deps, calls, ui, dk };
}

const drain = async () => { for (let i = 0; i < 50; i++) await Promise.resolve(); };

test("not logged in: boot stops before starting sync or showing the overlay", async () => {
  const { deps, calls, ui } = makeDeps({ loggedOut: true });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.started, 0);
  assert.equal(ui.overlay.hidden, true);
  assert.equal(calls.states.length, 0);
});

test("cached-DK fast path: no overlay, onDK + decrypted onState, mirror written, visit pinged", async () => {
  const { deps, calls, ui, dk } = makeDeps({ cachedDK: { key: "K", raw: new Uint8Array([9]) } });
  const u = createUnlock(deps);
  await u.boot();

  assert.equal(ui.overlay.hidden, true);
  assert.equal(ui.pass.focused, 0);
  assert.equal(calls.started, 1);
  assert.equal(calls.dks.length, 1);
  assert.notEqual(dk, calls.dks[0]); // the cached DK, not the unlock one
  assert.equal(calls.states.length, 1);

  const { state, info } = calls.states[0];
  assert.equal(info.offline, false);
  assert.equal(state.role, "owner");
  assert.equal(state.name, "Доска");
  assert.equal(state.defaultAuthor, "Автор");
  assert.equal(state.cardTitle, "answer");
  assert.deepEqual(state.cardLabels, { 2: [3] });
  assert.deepEqual(state.unread, { 2: { comments: true } });
  assert.deepEqual(state.lists, [{ id: 1, type: "normal", rank: "a", groupId: 5, title: "d:e:list" }]);
  assert.deepEqual(state.groups, [{ id: 5, name: "d:e:group" }]);
  assert.deepEqual(state.cards, [{
    id: 2, listId: 1, kind: "question", rank: "b",
    desc: "d:e:desc", handoutMeta: "d:e:meta", alias: null, createdAt: "2026-01-01",
  }]);
  assert.deepEqual(state.labels, [{ id: 3, kind: "normal", name: "d:e:lname", color: "d:e:lcolor" }]);
  // sizes: null in the snapshot → sanitized defaults, applied before render
  assert.equal(state.sizes.boardW, 1512);
  assert.deepEqual(calls.sizes, [state.sizes]);

  assert.equal(calls.saved.length, 1); // fresh snapshot mirrored
  assert.deepEqual(calls.status, ["saving", "saved"]);
  assert.deepEqual(calls.jposts, [["/api/boards/7/visit", {}]]);
});

test("no cached DK: boot shows the overlay and focuses the passphrase input", async () => {
  const { deps, calls, ui } = makeDeps();
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(ui.overlay.hidden, false);
  assert.equal(ui.pass.focused, 1);
  assert.equal(calls.states.length, 0);
  assert.equal(calls.dks.length, 0);
});

test("successful unlock: submit fetches keymeta, caches DK, hides overlay, loads", async () => {
  const { deps, calls, ui, dk } = makeDeps();
  const u = createUnlock(deps);
  await u.boot();

  ui.pass.value = GOOD_PASS;
  await ui.form.handlers.submit({ preventDefault() {} });

  assert.ok(calls.fetches.includes("/api/boards/7/keymeta"));
  assert.deepEqual(calls.cacheDK, [7, dk]);
  assert.equal(ui.overlay.hidden, true);
  assert.deepEqual(calls.dks, [dk]);
  assert.equal(calls.states.length, 1);
  assert.equal(ui.message.textContent, "");
});

test("wrong passphrase: error text shown, overlay stays, no state; retry succeeds", async () => {
  const { deps, calls, ui } = makeDeps();
  const u = createUnlock(deps);
  await u.boot();

  ui.pass.value = "wrong";
  await ui.form.handlers.submit({ preventDefault() {} });
  assert.equal(ui.message.textContent, "Неверный пароль доски");
  assert.equal(ui.overlay.hidden, false);
  assert.equal(calls.states.length, 0);
  assert.equal(calls.dks.length, 0);
  assert.equal(calls.cacheDK, null);

  ui.pass.value = GOOD_PASS;
  await ui.form.handlers.submit({ preventDefault() {} });
  assert.equal(ui.message.textContent, ""); // cleared on the new attempt
  assert.equal(ui.overlay.hidden, true);
  assert.equal(calls.states.length, 1);
});

test("offline boot: renders from the cached mirror, no fetch, no visit ping", async () => {
  const { deps, calls } = makeDeps({ online: false, cachedDK: { key: "K", raw: new Uint8Array([9]) }, mirror: SNAP });
  const u = createUnlock(deps);
  await u.boot();

  assert.equal(calls.fetches.length, 0);
  assert.equal(calls.states.length, 1);
  assert.equal(calls.states[0].info.offline, true);
  assert.equal(calls.states[0].state.name, "Доска");
  assert.deepEqual(calls.jposts, []);
  assert.deepEqual(calls.status, ["saving", "saved"]);
});

test("online but fetch fails: falls back to the mirror, marked offline", async () => {
  const { deps, calls } = makeDeps({ fetchFails: true, cachedDK: { key: "K", raw: new Uint8Array([9]) }, mirror: SNAP });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.states.length, 1);
  assert.equal(calls.states[0].info.offline, true);
  assert.equal(calls.saved.length, 0);
});

test("pending local edits: loads the mirror even when online", async () => {
  const { deps, calls } = makeDeps({ pending: 2, cachedDK: { key: "K", raw: new Uint8Array([9]) }, mirror: SNAP });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.fetches.length, 0);
  assert.equal(calls.states[0].info.offline, true);
});

test("offline with no mirror: onUnavailable, status error, no state", async () => {
  const { deps, calls } = makeDeps({ online: false, cachedDK: { key: "K", raw: new Uint8Array([9]) } });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.unavailable, 1);
  assert.equal(calls.states.length, 0);
  assert.deepEqual(calls.status, ["saving", "error"]);
});

test("legacy board (schema_version 1): name decrypted from name_enc and backfilled", async () => {
  const snap = { ...structuredClone(SNAP) };
  delete snap.name;
  snap.schema_version = 1;
  snap.name_enc = "e:name";
  const { deps, calls } = makeDeps({ cachedDK: { key: "K", raw: new Uint8Array([9]) }, snap });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.states[0].state.name, "d:e:name");
  assert.deepEqual(calls.jposts[0], ["/api/boards/7/migrate-name", { name: "d:e:name" }]);
});

test("visit ping fires once per page session across reloads", async () => {
  const { deps, calls } = makeDeps({ cachedDK: { key: "K", raw: new Uint8Array([9]) } });
  const u = createUnlock(deps);
  await u.boot();
  await u.load();
  assert.equal(calls.states.length, 2);
  assert.deepEqual(calls.jposts, [["/api/boards/7/visit", {}]]);
});

test("onBoardSynced reloads this board only", async () => {
  const { deps, calls } = makeDeps({ cachedDK: { key: "K", raw: new Uint8Array([9]) } });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.states.length, 1);
  calls.boardSyncedCb(8);
  await drain();
  assert.equal(calls.states.length, 1);
  calls.boardSyncedCb(7);
  await drain();
  assert.equal(calls.states.length, 2);
});

test("boot forwards sync status to the badge seam", async () => {
  const { deps, calls } = makeDeps({ cachedDK: { key: "K", raw: new Uint8Array([9]) } });
  const u = createUnlock(deps);
  await u.boot();
  assert.equal(calls.syncStatus.length, 1);
  calls.onStatusCb({ online: false, pending: 3, syncing: false, deadletters: [] });
  assert.equal(calls.syncStatus.length, 2);
  assert.equal(calls.syncStatus[1].pending, 3);
});
