import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

// transfer.ts binds the request verbs at import, so a recording API stands in
// for the server before it loads. Keys are real: a copy across boards must
// decrypt under the source key and read back under the target's.
installDOM([]);
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");
xySync.isOnline = () => true;
xyCrypto.loadCachedDK = async () => null; // no IndexedDB here: no board is unlocked

const calls = [];
let nextId = 500;
const routes = {};
xyApp.fetchJSON = async (url) => { calls.push(["GET", url]); return routes[url] ?? []; };
xyApp.jpost = async (url, body) => { calls.push(["POST", url, body]); return { id: ++nextId }; };
xyApp.jput = async (url, body) => { calls.push(["PUT", url, body]); return {}; };
xyApp.jdelete = async (url) => { calls.push(["DELETE", url]); return {}; };
globalThis.fetch = async (url) => { calls.push(["FETCH", String(url)]); return { ok: false }; };
const { createTransfer } = await import("../web/assets/static/dist/transfer.js");

const { dk: srcDk } = await xyCrypto.createBoardKeys("src");
const { dk: dstDk } = await xyCrypto.createBoardKeys("dst");

function board() {
  const state = {
    name: "Своя", lists: [{ id: 1, title: "Тур 1", rank: "a0" }, { id: 2, title: "Тур 2", rank: "a1" }],
    cards: [{ id: 10, listId: 1, kind: "question", rank: "a0", desc: "Вопрос?", alias: "В1" }, { id: 11, listId: 1, kind: "question", rank: "a1", desc: "Второй" }],
    labels: [{ id: 3, name: "взяли", color: "#0f0" }],
    cardLabels: [{ cardId: 10, labelId: 3, sessionId: 7 }],
    cardSessions: [{ cardId: 10, sessionId: 7 }],
    sessions: [{ id: 7, meta: JSON.stringify({ key: "s-7", date: "2026-08-01", title: "Тест" }) }],
    unread: {}, defaultAuthor: "",
  };
  const writes = [];
  return {
    state, writes,
    transfer: createTransfer({
      boardId: 1, getState: () => state, getDK: () => srcDk,
      verbs: { patch: async (k, path, body) => { writes.push([k, path, body]); } },
      cardsOf: (lid) => state.cards.filter((c) => c.listId === lid),
      labelById: (id) => state.labels.find((l) => l.id === id),
      askPassphrase: () => null,
    }),
  };
}

test("a move within the board is one re-rank through the verbs, after the list's last card", async () => {
  const { state, writes, transfer } = board();
  const ctx = await transfer.loadMoveBoard(1);
  assert.deepEqual(ctx.lists.map((l) => l.id), [1, 2]);
  await transfer.transferCard(state.cards[0], 2, ctx, true);
  assert.deepEqual(writes.map((w) => [w[0], w[1]]), [["patchCard", "/api/cards/10"]]);
  assert.equal(state.cards[0].listId, 2);
  assert.deepEqual(ctx.cardsByList.get(2).map((c) => c.id), [10], "the ctx keeps the order for the next card");
});

test("a copy on the same board repeats the labels and playings verbatim and carries the extras", async () => {
  const { state, transfer } = board();
  calls.length = 0;
  const ctx = await transfer.loadMoveBoard(1);
  const id = await transfer.transferCard(state.cards[0], 2, ctx, false);
  assert.equal(id, nextId);
  const post = calls.find((c) => c[0] === "POST" && c[1] === "/api/lists/2/cards");
  assert.equal(await xyCrypto.decField(srcDk, post[2].description_enc), "Вопрос?");
  assert.equal(await xyCrypto.decField(srcDk, post[2].alias_enc), "В1");
  assert.deepEqual(calls.find((c) => c[1] === `/api/cards/${id}/sessions`)[2], { session_ids: [7] });
  assert.deepEqual(calls.find((c) => c[1] === `/api/cards/${id}/labels`)[2], { labels: [{ label_id: 3, session_id: 7 }] });
  assert.ok(state.cards.some((c) => c.id === id && c.listId === 2), "the copy joins the page's state");
  assert.ok(calls.some((c) => c[1] === "/api/cards/10/timeline"), "comments are read to be carried");
});

test("a copy to another board re-encrypts under its key and reconciles labels by name and playings by session key", async () => {
  const { state, transfer } = board();
  calls.length = 0;
  const ctx = { boardId: 2, dk: dstDk, lists: [{ id: 20, title: "Чужой", rank: "a0" }], cardsByList: new Map(), labels: [{ id: 33, name: "взяли", color: "#0f0" }], sessions: [], name: "Другая" };
  const id = await transfer.transferCard(state.cards[0], 20, ctx, true);
  const post = calls.find((c) => c[0] === "POST" && c[1] === "/api/lists/20/cards");
  assert.equal(await xyCrypto.decField(dstDk, post[2].description_enc), "Вопрос?");
  // The Playing's session does not exist there: it is copied, keyed on the
  // sitting's key, and the label assignment scopes to the copy.
  const sessionPost = calls.find((c) => c[0] === "POST" && c[1] === "/api/boards/2/sessions");
  const copied = JSON.parse(await xyCrypto.decField(dstDk, sessionPost[2].meta_enc));
  assert.equal(copied.key, "s-7");
  assert.equal(copied.origin.board, "Своя");
  assert.equal(ctx.sessions.length, 1);
  assert.deepEqual(calls.find((c) => c[1] === `/api/cards/${id}/labels`)[2], { labels: [{ label_id: 33, session_id: ctx.sessions[0].id }] });
  assert.ok(!calls.some((c) => c[0] === "POST" && c[1] === "/api/boards/2/labels"), "an existing label is matched, not re-created");
  assert.ok(calls.some((c) => c[0] === "DELETE" && c[1] === "/api/cards/10"), "a move removes the source");
  assert.ok(!state.cards.some((c) => c.id === 10));
});

test("loadMoveBoard refuses a locked board when the passphrase is not given", async () => {
  const { transfer } = board();
  await assert.rejects(() => transfer.loadMoveBoard(9), /отменено/);
});
