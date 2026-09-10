// copyfromlist.test.js — «Скопировать доску» on the / board list (copyfromlist.ts).
// The flow is the board page's copy panel minus its board page: the source key
// may not be in this browser, so the copy asks the passphrase first, then shows
// the create-board twin with the name prefilled, and the copy itself is the
// same pair — decrypt the source snapshot (decryptSnapshot), buildBundle it,
// createBoardFromBundle re-encrypts it under the new board's fresh key.
// Real dist modules and real WebCrypto (envelopes decrypt in both directions);
// only the network and the IndexedDB key cache are fakes.
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

const BOARD_PASS = "correct horse battery staple";
const NEW_BOARD_ID = 90;
let NEXT_ID = 500;

const ids = [
  "message",
  // the unlock prompt (a copy may start without this device holding the key)
  "copyUnlockOverlay", "copyUnlockForm", "copyUnlockMessage", "copyUnlockPass", "copyUnlockHint", "copyUnlockCancel",
  // the copy form, a create-board twin
  "copyOverlay", "copyForm", "copyName", "copyPass", "copyGenPassBtn", "copyPassCopied", "copyPassSaved", "copySubmit", "copyCancel", "copyMessage",
];
const page = installDOM(ids);
for (const id of ["copyUnlockForm", "copyForm"]) page.node(id).reset = function reset() { this.value = ""; };
page.node("copyUnlockOverlay").hidden = true;
page.node("copyOverlay").hidden = true;
const node = (id) => page.node(id);

// The modules bind the request helpers and modal ids at import, before any copy
// could start, so the recording API and the key-cache stubs stand in already.
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");
const { decryptSnapshot } = await import("../web/assets/static/dist/unlock.js");
const { copyBoardFromList } = await import("../web/assets/static/dist/copyfromlist.js");

// The modules destructure their helpers off xyApp at import, so overriding the
// object's methods is too late — the originals go through globalThis.fetch.
// A fake server answers the routes, records every call, and hands out ids.
const calls = [];
const cachedDks = [];
const routes = {};
const ok = (body) => ({ ok: true, status: 200, json: async () => body, text: async () => "" });
globalThis.fetch = async (url, init = {}) => {
  const method = (init.method || "GET").toUpperCase();
  const u = String(url);
  calls.push([method, u, init.body ? JSON.parse(init.body) : undefined]);
  if (method === "GET") {
    if (!(u in routes)) throw new Error("unexpected GET " + u);
    return ok(routes[u]);
  }
  if (method === "POST" && u === "/api/boards") return ok({ id: NEW_BOARD_ID });
  if (method === "POST") return ok({ id: ++NEXT_ID });
  return ok({});
};
// The IndexedDB key cache is the one real edge the browser owns: load is
// directed per test, and writes are recorded.
xyCrypto.loadCachedDK = async () => null;
xyCrypto.cacheDK = async (id, dk) => { cachedDks.push([id, dk]); };

// The source board under its (real) key: a snapshot the copy flow decrypts.
async function makeSnap(dk, name) {
  const enc = (s) => xyCrypto.encField(dk, s);
  return {
    role: "owner",
    schema_version: 2,
    name,
    sizes: null,
    default_author: "",
    card_title: "question",
    card_labels: [],
    card_sessions: [],
    tour_testers: [],
    unread: {},
    lists: [{ id: 1, type: "normal", rank: "a0", group_id: null, title_enc: await enc("Тур 1") }],
    cards: [{
      id: 10, list_id: 1, kind: "question", rank: "a0",
      description_enc: await enc("Вопрос один?"), handout_meta_enc: null, alias_enc: await enc("В1"),
      created_at: "2026-01-01",
    }],
    labels: [],
    sessions: [],
  };
}

// Real crypto (scrypt) takes real time, so a shared-condition wait cannot be
// microtask-only: poll with a timer, give it a generous budget.
const until = async (pred, what, ms = 8000) => {
  const start = Date.now();
  while (!pred() && Date.now() - start < ms) await new Promise((r) => setTimeout(r, 20));
  assert.ok(pred(), what);
};

test("decryptSnapshot reads the snapshot the copy side decodes (modern and legacy names)", async () => {
  const { dk } = await xyCrypto.createBoardKeys(BOARD_PASS);
  const snap = await makeSnap(dk, "Доска");

  const { state, name, migrated } = await decryptSnapshot(dk, snap, xyCrypto);
  assert.equal(name, "Доска");
  assert.equal(migrated, true);
  assert.deepEqual(state.lists.map((l) => [l.id, l.title]), [[1, "Тур 1"]]);
  assert.deepEqual(state.cards.map((c) => [c.desc, c.alias]), [["Вопрос один?", "В1"]]);

  const legacy = { ...structuredClone(snap), schema_version: 1 };
  delete legacy.name;
  legacy.name_enc = await xyCrypto.encField(dk, "Старая");
  const { name: lname, migrated: lmig } = await decryptSnapshot(dk, legacy, xyCrypto);
  assert.equal(lname, "Старая");
  assert.equal(lmig, false);
});

test("no cached key: unlock prompt → copy form → a fresh board re-encrypted under a new key + redirect", async () => {
  const { keymeta, dk } = await xyCrypto.createBoardKeys(BOARD_PASS);
  routes["/api/boards/7/keymeta"] = keymeta;
  routes["/api/boards/7"] = await makeSnap(dk, "Доска");
  routes["/api/boards/7/members"] = [];
  routes["/api/boards/7/timeline"] = [];
  routes["/api/boards/7/attachments"] = [];
  routes["/api/auth/storage"] = { unlimited: true };
  calls.length = 0;
  cachedDks.length = 0;

  const promise = copyBoardFromList({ id: 7, name: "Доска" });
  await until(() => !node("copyUnlockOverlay").hidden, "the unlock prompt opens");
  assert.equal(node("copyOverlay").hidden, true, "the copy form waits its turn");
  assert.equal(node("copyUnlockHint").textContent, "Введите пароль доски «Доска», чтобы прочитать её содержимое.");

  node("copyUnlockPass").value = BOARD_PASS;
  node("copyUnlockForm").fire("submit", { preventDefault() {} });
  await until(() => !node("copyOverlay").hidden, "the unlock resolves into the copy form");
  assert.equal(node("copyUnlockOverlay").hidden, true);
  assert.equal(node("copyName").value, "Доска (копия)", "the name is prefilled");

  const copyPass = () => node("copyPass").value;
  node("copyForm").fire("submit", { preventDefault() {} });
  await promise; // resolves when the copy lands and the modal closes

  assert.deepEqual(calls.filter((c) => c[0] === "GET").map((c) => c[1]), [
    "/api/boards/7/keymeta",
    "/api/boards/7",
    "/api/boards/7/members", "/api/boards/7/timeline", "/api/boards/7/attachments",
    "/api/auth/storage",
  ], "the reads a copy makes, in order");

  const boardPost = calls.find((c) => c[0] === "POST" && c[1] === "/api/boards");
  assert.ok(boardPost, "a new board is created");
  assert.equal(boardPost[2].name, "Доска (копия)");

  // The content travels under the COPY's key (the boots body's own keymeta),
  // not the source's: unwrap the copy's key from what the flow posted and read
  // the recorded card body back with it.
  const cardPost = calls.find((c) => c[0] === "POST" && /\/api\/lists\/\d+\/cards$/.test(c[1]));
  const copyDk = await xyCrypto.unlockBoard(copyPass(), boardPost[2]);
  assert.equal(await xyCrypto.decField(copyDk, cardPost[2].description_enc), "Вопрос один?");
  assert.equal(await xyCrypto.decField(copyDk, cardPost[2].alias_enc), "В1");
  await assert.rejects(() => xyCrypto.decField(dk, cardPost[2].description_enc), "the source key cannot read it");

  assert.deepEqual(cachedDks.map((c) => c[0]), [7, NEW_BOARD_ID], "source cached on unlock, the copy on create");
  assert.equal(node("copyOverlay").hidden, true, "the copy form closed");
  assert.equal(globalThis.location.href, `/board/${NEW_BOARD_ID}`, "the browser follows the copy");
});

test("a held key skips the unlock prompt; a dismissed copy mints nothing", async () => {
  const { dk } = await xyCrypto.createBoardKeys(BOARD_PASS);
  routes["/api/boards/7"] = await makeSnap(dk, "Вторая");
  xyCrypto.loadCachedDK = async () => dk;
  calls.length = 0;

  const promise = copyBoardFromList({ id: 7, name: "Вторая" });
  await until(() => !node("copyOverlay").hidden, "the copy form opens straight away");
  assert.equal(node("copyUnlockOverlay").hidden, true, "no unlock prompt");
  assert.ok(!calls.some((c) => c[1] === "/api/boards/7/keymeta"), "no keymeta read — the key came from the cache");
  assert.equal(node("copyName").value, "Вторая (копия)");

  node("copyCancel").click(); // the ghost button, the same gesture as ✕ and back
  await promise;
  assert.ok(!calls.some((c) => c[0] === "POST" && c[1] === "/api/boards"), "a dismissed copy creates nothing");
});

test("a legacy board (name_enc) drops its real name into the prefill once the key is held", async () => {
  const { dk } = await xyCrypto.createBoardKeys(BOARD_PASS);
  routes["/api/boards/7"] = await makeSnap(dk, "Старая");
  xyCrypto.loadCachedDK = async () => dk;
  calls.length = 0;

  const promise = copyBoardFromList({ id: 7, name: "board #7", name_enc: await xyCrypto.encField(dk, "Старая") });
  await until(() => !node("copyOverlay").hidden && node("copyName").value === "Старая (копия)",
    "the tile's placeholder is replaced by the decrypted name once the key is held");

  node("copyCancel").click();
  await promise;
});

test("the unlock prompt abandoned: no copy, nothing written", async () => {
  const { keymeta, dk } = await xyCrypto.createBoardKeys(BOARD_PASS);
  routes["/api/boards/7/keymeta"] = keymeta;
  routes["/api/boards/7"] = await makeSnap(dk, "Третья");
  xyCrypto.loadCachedDK = async () => null;
  calls.length = 0;

  const promise = copyBoardFromList({ id: 7, name: "Третья" });
  await until(() => !node("copyUnlockOverlay").hidden, "the unlock prompt opens");
  node("copyUnlockCancel").click();
  await promise;
  assert.equal(node("copyOverlay").hidden, true, "the copy form never opened");
  assert.ok(!calls.some((c) => c[0] === "POST"), "nothing minted");
});

test("offline: the whole flow stops at the gate, nothing opens", async () => {
  const real = xySync.requireOnline;
  xySync.requireOnline = (message, where) => { where.textContent = message; return false; };
  try {
    calls.length = 0;
    await copyBoardFromList({ id: 7, name: "Доска" });
    assert.ok(node("message").textContent.length > 0, "the offline message shows where the page writes it");
    assert.equal(node("copyUnlockOverlay").hidden, true, "nothing opens");
    assert.equal(node("copyOverlay").hidden, true);
    assert.deepEqual(calls, [], "no reads, no writes");
  } finally {
    xySync.requireOnline = real;
  }
});