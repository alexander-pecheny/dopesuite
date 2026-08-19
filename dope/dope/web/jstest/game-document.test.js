import {test} from "node:test";
import assert from "node:assert/strict";

// mountGameDocument composes the loader, the live events and the writer over
// a page's two callbacks; the page is a fake here, and so are the stream, the
// fetch and the storage — the same fakes state-sync.test.js uses.
globalThis.window = {setTimeout: (fn) => { fn(); return 0; }, clearTimeout() {}, addEventListener() {}, location: {search: ""}};
globalThis.document = {addEventListener() {}, visibilityState: "visible", body: {classList: {toggle() {}}}};
const store = new Map();
window.localStorage = {getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k)};

const {mountGameDocument} = await import("./dist/game-shell.js");
const {createSyncIndicator} = await import("./dist/state-sync.js");

function fakeStream() {
  const listeners = new Map();
  return {
    readyState: 1, onerror: null,
    addEventListener(type, fn) { listeners.set(type, fn); },
    close() { this.readyState = 2; },
    emit(type, data) { listeners.get(type)?.({data: JSON.stringify(data)}); },
  };
}

function fakeShell() {
  const calls = {presence: 0, touched: 0, viewers: []};
  return {
    calls,
    viewer: false, canEdit: true, staticMode: false, scopeGameID: "7",
    indicator: Object.assign(createSyncIndicator(() => {}), {touch: () => calls.touched++}),
    viewerCounter: {setCount: (n) => calls.viewers.push(n)},
    recorder: null,
    renderChrome() {}, refreshLinks() {},
    presence: {connect: () => calls.presence++, refresh() {}, publish() {}, fromElement() {}},
  };
}

test("the document is adopted from the init payload, connected, and a remote state applies", async () => {
  window.__GAME_INIT__ = {scheme: {title: "ОД"}, state: {v: 1}, fest: {title: "Фест"}, seq: 5, epoch: "e1"};
  const fetched = [];
  globalThis.fetch = (url) => {
    fetched.push(url);
    const body = url.endsWith("/scheme") ? {title: "ОД"} : url.endsWith("/state") ? {v: 1} : {title: "Фест"};
    return Promise.resolve({ok: true, status: 200, headers: {get: () => null}, json: () => Promise.resolve(body), text: () => Promise.resolve("")});
  };
  const shell = fakeShell();
  const page = {adopted: [], applied: [], current: {scheme: null, state: null, fest: null}};
  const streams = [];
  const doc = mountGameDocument({
    route: {festID: "f", gameID: "g", apiBase: "/api/fest/f/games/g"},
    cachePrefix: "t",
    shell,
    adopt: (snap) => { page.adopted.push(snap); page.current = {scheme: snap.scheme, state: snap.state, fest: snap.fest}; },
    apply: (state) => { page.applied.push(state); page.current.state = state; },
    current: () => page.current,
    newEventSource: () => { const s = fakeStream(); streams.push(s); return s; },
  });
  assert.equal(doc.scope, "game-state:7");
  await doc.load();
  assert.equal(page.adopted.length, 1, "the init snapshot is adopted once");
  assert.deepEqual(page.adopted[0].state, {v: 1});
  assert.equal(window.__GAME_INIT__, null);
  assert.equal(shell.calls.touched, 1);
  assert.equal(shell.calls.presence, 1, "presence connects after the load");
  assert.equal(streams.length, 1, "the live events opened one stream");
  for (let i = 0; i < 5; i++) await Promise.resolve(); // the detached revalidation
  assert.equal(page.adopted.length, 1, "an unchanged revalidation adopts nothing");

  // A delta chaining onto the init seq applies to the page.
  streams[0].emit("state", {scope: "game-state:7", ops: [{op: "set", path: ["v"], value: 2}], seq: 6, prevSeq: 5, epoch: "e1"});
  assert.deepEqual(page.applied.at(-1), {v: 2});
  assert.equal(doc.isPending(["v"]), false);

  // A save goes out as one PATCH to the document's URL.
  doc.save(["v"], 3);
  await Promise.resolve();
  assert.ok(fetched.some((u) => u === "/api/fest/f/games/g/state"), `PATCH reached the state URL: ${fetched}`);
});
