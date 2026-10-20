// A local xy has no offline layer: sync.ts sends every write straight to the
// server and keeps nothing in IndexedDB, which this runtime does not even have.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xySync } from "../web/assets/static/dist/sync.js";
import { xyInstance } from "../web/assets/static/dist/instance.js";

test("local: a write goes straight to the server, and nothing is mirrored", async () => {
  xyInstance.setInstance({ mode: "local", off: ["offline"] });
  const realFetch = globalThis.fetch;
  const sent = [];
  globalThis.fetch = async (path, init) => {
    sent.push([init.method, path]);
    return new Response(JSON.stringify({ id: 42 }), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  try {
    const res = await xySync.mutate({ kind: "createList", method: "POST", path: "/api/boards/1/lists", body: { title: "VA==", rank: "m" }, board: 1, mint: true });
    assert.deepEqual(res, { id: 42 });
    assert.deepEqual(sent, [["POST", "/api/boards/1/lists"]]);
    assert.equal(xySync.isOnline(), true);
    assert.equal(await xySync.pendingCountForBoard(1), 0);
    assert.equal(await xySync.loadSnapshot(1), undefined);
    await xySync.saveSnapshot(1, { lists: [] });
    assert.deepEqual(await xySync.timelineFor(5), []);
    await xySync.cacheTimeline(5, [{ id: 1, type: "comment" }]);
    assert.deepEqual(await xySync.timelineFor(5), [{ id: 1, type: "comment" }]);
    assert.equal(await xySync.getBoardList(), undefined);
  } finally {
    globalThis.fetch = realFetch;
    xyInstance.setInstance(null);
  }
});

test("local: a failed write is an error for the caller, never a queued op", async () => {
  xyInstance.setInstance({ mode: "local", off: ["offline"] });
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () => { throw new TypeError("no route"); };
  try {
    await assert.rejects(() => xySync.mutate({ kind: "patchCard", method: "PATCH", path: "/api/cards/1", body: {}, board: 1 }));
  } finally {
    globalThis.fetch = realFetch;
    xyInstance.setInstance(null);
  }
});
