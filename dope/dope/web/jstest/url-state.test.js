import {test} from "node:test";
import assert from "node:assert/strict";

// A browser location, as much of one as url-state reads and writes: the module
// only ever asks for pathname/search/hash/href and only ever calls
// history.replaceState, so the whole address bar is one string.
const listeners = {hashchange: [], popstate: []};
let href = "http://x/host/fest/1/game/od";

function assign(next) {
  href = new URL(next, "http://x").href;
}

globalThis.window = {
  get location() {
    const url = new URL(href);
    return {pathname: url.pathname, search: url.search, hash: url.hash, href};
  },
  addEventListener(name, fn) {
    (listeners[name] ||= []).push(fn);
  },
};
globalThis.history = {replaceState: (_state, _title, next) => assign(next)};

const {onNavigate, param, setHashTab, setParam, tabFromHash} = await import("./dist/url-state.js");

const TABS = [{key: "results"}, {key: "detailed"}, {key: "input"}];

test("tabFromHash takes only a tab the page offers", () => {
  assign("/g#detailed");
  assert.equal(tabFromHash(TABS), "detailed");
  // A host-only tab a viewer cannot see is simply not in the list.
  assign("/g#screen");
  assert.equal(tabFromHash(TABS), null);
  assign("/g");
  assert.equal(tabFromHash(TABS), null);
});

test("tabFromHash migrates a legacy hash through canonicalKey", () => {
  assign("/g#table");
  const canonical = (tabs, key) => (key === "table" ? "results" : key);
  assert.equal(tabFromHash(TABS, {canonical}), "results");
  assert.equal(tabFromHash(TABS), null);
});

test("a tab switch keeps the query string", () => {
  assign("/g?division=%D0%A8%D0%BA%D0%BE%D0%BB#results");
  setHashTab("detailed");
  assert.equal(window.location.hash, "#detailed");
  assert.equal(param("division"), "Школ");
  // Writing the tab already in the hash changes nothing.
  setHashTab("detailed");
  assert.equal(window.location.href, "http://x/g?division=%D0%A8%D0%BA%D0%BE%D0%BB#detailed");
});

test("a query parameter round-trips percent-encoded and keeps the hash", () => {
  assign("/g#results");
  setParam("division", "Школ");
  assert.equal(window.location.search, "?division=%D0%A8%D0%BA%D0%BE%D0%BB");
  assert.equal(window.location.hash, "#results");
  assert.equal(param("division"), "Школ");
  // An empty value removes the parameter rather than leaving it blank.
  setParam("division", "");
  assert.equal(window.location.search, "");
  assert.equal(param("division"), "");
  assert.equal(window.location.hash, "#results");
});

test("onNavigate listens for both the typed hash and the back button", () => {
  let fired = 0;
  onNavigate(() => fired++);
  for (const fn of listeners.hashchange) fn();
  for (const fn of listeners.popstate) fn();
  assert.equal(fired, 2);
});
