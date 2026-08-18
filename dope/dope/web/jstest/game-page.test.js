import {test} from "node:test";
import assert from "node:assert/strict";

// renderGameBreadcrumbs only touches createElement/appendChild/replaceChildren
// (plus createElementNS, since the home crumb is an SVG glyph), so a recording
// stub is enough to assert the trail it builds.
function fakeNode(tag) {
  return {
    tag, className: "", textContent: "", title: "",
    attrs: {},
    kids: [],
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...n) { this.kids.push(...n); },
    appendChild(n) { this.kids.push(n); return n; },
  };
}
globalThis.document = {
  createElement: (tag) => fakeNode(tag),
  createElementNS: (_ns, tag) => fakeNode(tag),
};

const {renderGameBreadcrumbs, createLocalCache, createGameDataLoader, notifyEmbeddedResize} = await import("./dist/game-page.js");

// trail renders the crumbs as "tag:class:text", separators dropped. The home
// crumb's text is empty: it is an SVG glyph now, not a 🏠 character.
function trail(options) {
  const kids = [];
  const root = {replaceChildren: () => kids.splice(0), appendChild: (n) => kids.push(n)};
  renderGameBreadcrumbs(root, options);
  return kids.filter((n) => n.className !== "crumb-sep")
    .map((n) => `${n.tag}:${n.className}:${n.textContent}${n.attrs.href ? " → " + n.attrs.href : ""}`);
}

test("the public trail starts at home and ends on the current page", () => {
  assert.deepEqual(trail({
    festTitle: "Кубок Города", festHref: "/fest/12",
    gameTitle: "ОД", gameHref: "/fest/12/game/3/", currentTitle: "Результаты",
  }), [
    "a:crumb crumb-home: → /",
    "a:crumb:Кубок Города → /fest/12",
    "a:crumb:ОД → /fest/12/game/3/",
    "span:crumb crumb-current:Результаты",
  ]);
});

test("the host tree carries the Мои фесты crumb its URL does", () => {
  const got = trail({
    host: true, festTitle: "Кубок Города", festHref: "/host/fest/12",
    gameTitle: "ЭК", gameHref: "/host/fest/12/game/3/", currentTitle: "Площадки",
  });
  assert.equal(got[1], "a:crumb:Мои фесты → /host");
  assert.equal(got.at(-1), "span:crumb crumb-current:Площадки");
});

test("a game with no sub-view ends on the game itself, unlinked", () => {
  assert.deepEqual(trail({festTitle: "Кубок", festHref: "/fest/12", gameTitle: "ОД"}), [
    "a:crumb crumb-home: → /",
    "a:crumb:Кубок → /fest/12",
    "span:crumb crumb-current:ОД",
  ]);
});

test("missing titles fall back rather than rendering blank crumbs", () => {
  assert.deepEqual(trail({}), [
    "a:crumb crumb-home: → /",
    "a:crumb:Фест → /",
    "span:crumb crumb-current:Игра",
  ]);
});

test("a sub-view whose name equals the game's does not repeat it", () => {
  const got = trail({festTitle: "Кубок", festHref: "/fest/12", gameTitle: "ОД", gameHref: "/fest/12/game/3/", currentTitle: "ОД"});
  assert.equal(got.length, 3);
  assert.equal(got.at(-1), "span:crumb crumb-current:ОД");
});

// The loaders read window lazily; give them one shared fake window plus an
// in-memory Storage stand-in for the tests that persist.
globalThis.window = {};
// fakeLocalStorage is an in-memory Storage stand-in for testing persistence;
// assign it to window.localStorage before exercising code that reads it.
function fakeLocalStorage() {
  const store = new Map();
  return {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    get length() {
      return store.size;
    },
  };
}

test("createLocalCache round-trips JSON and degrades safely", () => {
  window.localStorage = fakeLocalStorage();
  const cache = createLocalCache("slot");
  assert.equal(cache.read(), null, "empty slot reads as null");
  cache.write({a: 1});
  assert.deepEqual(cache.read(), {a: 1});
  cache.write(null); // null is a no-op, must not clobber the stored value
  assert.deepEqual(cache.read(), {a: 1});
  window.localStorage.setItem("slot", "{not json");
  assert.equal(cache.read(), null, "corrupt JSON reads as null, not a throw");
});

test("createGameDataLoader hydrates from __GAME_INIT__, caches it, and revalidates", async () => {
  window.localStorage = fakeLocalStorage();
  window.__GAME_INIT__ = {scheme: {s: 1}, state: {t: 2}, fest: {f: 3}, seq: 5};
  const adopted = [];
  let revalidated = 0;
  const loader = createGameDataLoader({
    route: {festID: "f", gameID: "g", apiBase: "/api"},
    cachePrefix: "od",
    adopt: (snap, source) => adopted.push({snap, source}),
    revalidate: () => revalidated++,
  });
  await loader.load();
  assert.equal(adopted.length, 1);
  assert.equal(adopted[0].source, "init");
  assert.equal(adopted[0].snap.init.seq, 5, "raw init payload forwarded on the init path");
  assert.equal(window.__GAME_INIT__, null, "init payload consumed");
  assert.deepEqual(loader.cache.read(), {scheme: {s: 1}, state: {t: 2}, fest: {f: 3}}, "snapshot cached without the init envelope");
  for (let i = 0; i < 3; i++) await Promise.resolve(); // flush the detached revalidation
  assert.equal(revalidated, 1);
});

test("createGameDataLoader falls back to the localStorage snapshot", async () => {
  window.localStorage = fakeLocalStorage();
  window.__GAME_INIT__ = null;
  const route = {festID: "f", gameID: "g", apiBase: "/api"};
  createGameDataLoader({route, cachePrefix: "si", adopt: () => {}, revalidate: () => {}})
    .cache.write({scheme: {s: 1}, state: {t: 2}, fest: null});
  const seen = [];
  let revalidated = 0;
  const loader = createGameDataLoader({
    route,
    cachePrefix: "si",
    adopt: (_snap, source) => seen.push(source),
    revalidate: () => revalidated++,
  });
  await loader.load();
  assert.deepEqual(seen, ["cache"]);
  for (let i = 0; i < 3; i++) await Promise.resolve();
  assert.equal(revalidated, 1, "cache hit still kicks a background revalidation");
});

test("notifyEmbeddedResize stays a no-op outside an embed", () => {
  let posted = 0;
  window.requestAnimationFrame = (cb) => cb();
  window.parent = {postMessage: () => posted++}; // a distinct parent frame...
  notifyEmbeddedResize(false); // ...but the page isn't the embed view
  assert.equal(posted, 0, "not embedded -> no postMessage");
  window.parent = window; // embed flag set, but there is no outer frame to message
  notifyEmbeddedResize(true);
  assert.equal(posted, 0, "no parent frame -> no postMessage");
});
