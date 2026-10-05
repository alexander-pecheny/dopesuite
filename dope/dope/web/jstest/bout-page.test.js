import {test} from "node:test";
import assert from "node:assert/strict";

// mountBoutPage is driven through its interface: the shell, the stream and
// the fetch are fakes, and so is the sheet it draws into — it only has to
// take children and toggle classes. Short timers (the resync and fest-refresh
// debounces, the write window) fire at once, as in state-sync.test.js.
globalThis.window = {
  setTimeout: (fn, ms) => {
    if (!ms || ms < 1000) fn();
    return 0;
  },
  clearTimeout() {},
  addEventListener() {},
  location: {pathname: "/host/fest/f/game/g/", hash: "", search: "", reload() {}},
};
globalThis.document = {addEventListener() {}, visibilityState: "visible", scrollingElement: {scrollTop: 0}, documentElement: {}};
globalThis.requestAnimationFrame = (fn) => { fn(); return 1; };
globalThis.cancelAnimationFrame = () => {};
const store = new Map();
window.localStorage = {getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k)};

const {mountBoutPage, tabStages, stageBouts, seatRoster} = await import("./dist/bout-page.js");
const {createSyncIndicator} = await import("./dist/state-sync.js");

const API = "/api/fest/f/games/g";

function fakeStream() {
  const listeners = new Map();
  return {
    readyState: 1, onerror: null,
    addEventListener(type, fn) { listeners.set(type, fn); },
    close() { this.readyState = 2; },
    emit(data) { listeners.get("state")?.({data: JSON.stringify({epoch: "e1", ...data})}); },
  };
}

function fakeNode() {
  return {matches: () => false, querySelector: () => null};
}

function fakeRoot() {
  return {
    node: null,
    replaceChildren(node) { this.node = node; },
    classList: {toggle() {}},
    closest: () => null,
    querySelectorAll: () => [],
    contains: () => false,
    parentElement: null,
  };
}

function fakeShell(viewer) {
  const calls = {presence: 0, touched: 0, failed: 0, events: []};
  return {
    calls,
    viewer, canEdit: !viewer, staticMode: false, scopeGameID: "7",
    indicator: Object.assign(createSyncIndicator(() => {}), {touch: () => calls.touched++, fail: () => calls.failed++}),
    viewerCounter: {setCount() {}},
    recorder: {event: (name, data) => calls.events.push([name, data])},
    renderChrome() {}, refreshLinks() {},
    presence: {connect: () => calls.presence++, refresh() {}, publish() {}, fromElement() {}},
  };
}

// serve answers the page's requests and logs them. The bouts it serves are
// the `bouts` map, so a test moves the server's state by editing it.
function serve(bouts) {
  const calls = [];
  globalThis.fetch = (url, options = {}) => {
    const method = options.method || "GET";
    const body = options.body ? JSON.parse(options.body) : undefined;
    calls.push({url, method, body});
    let answer;
    if (url === `${API}/stages/matches`) answer = [{code: "s1", matches: Object.values(bouts)}];
    else if (url === "/api/fest/f/venues") answer = [{number: 1, title: "Hall"}];
    else if (url === API) answer = {stages: [{code: "s1", title: "Fresh"}]};
    else if (url.startsWith(`${API}/matches/`) && url.endsWith("/state")) {
      const code = url.split("/").at(-2);
      const view = bouts[code];
      for (const op of body.ops) view.state[op.path[0]] = op.value;
      view.seq += 1;
      answer = view;
    } else answer = {};
    return Promise.resolve({ok: true, status: 200, headers: {get: () => null}, json: () => Promise.resolve(structuredClone(answer)), text: () => Promise.resolve("")});
  };
  return calls;
}

async function settle() {
  for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0));
}

function mount({viewer = false, onRoster} = {}) {
  const streams = [];
  const shell = fakeShell(viewer);
  const drawn = [];
  const page = mountBoutPage({
    app: "brain",
    root: fakeRoot(),
    tabsRoot: null,
    init: null,
    scheme: {stages: []},
    fest: {stages: [{code: "s1", title: "Init"}]},
    title: () => "Brain",
    parse: (view) => ({...view.state, parsed: true}),
    blank: () => ({blank: true}),
    buildTab: (tab) => { drawn.push(tab?.key); return fakeNode(); },
    buildRoster: () => fakeNode(),
    fitsFrame: () => false,
    boutSelector: ".bout",
    cursorKinds: {},
    onRoster,
    shell,
    route: {viewer, festID: "f", gameID: "g", apiBase: API},
    newEventSource: () => { const s = fakeStream(); streams.push(s); return s; },
  });
  return {page, shell, streams, drawn};
}

test("a start fetches the bouts, opens the stream, connects presence and reads the venues", async () => {
  store.clear();
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page, shell, streams} = mount();
  assert.deepEqual(page.stateOf("m1"), {blank: true}, "a bout not yet seen is blank");
  page.start();
  await settle();
  assert.equal(streams.length, 1, "one stream");
  assert.deepEqual(page.stateOf("m1"), {a: 1, parsed: true}, "the bout is parsed by the page");
  assert.equal(shell.calls.presence, 1, "presence connects after the first fetch");
  assert.ok(shell.calls.touched >= 1);
  assert.deepEqual(page.venues, [{number: 1, title: "Hall"}], "a host reads the fest's venues");
  assert.ok(calls.some((c) => c.url === `${API}/stages/matches`));
});

test("a spectator reads no venues when the Game has no venues tab, and connects no writes", async () => {
  store.clear();
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page} = mount({viewer: true});
  page.start();
  await settle();
  assert.ok(!calls.some((c) => c.url.endsWith("/venues")));
  page.patch("m1", ["a"], 2);
  await settle();
  assert.ok(!calls.some((c) => c.method === "PATCH"), "a spectator never writes");
});

test("a sibling Game's bout events are ignored; this Game's delta applies", async () => {
  store.clear();
  serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page, streams} = mount();
  page.start();
  await settle();
  streams[0].emit({scope: "match:8:m1", data: {code: "m1", state: {a: 99}}, seq: 50});
  assert.deepEqual(page.stateOf("m1"), {a: 1, parsed: true}, "Game 8's bout m1 is not ours");
  streams[0].emit({scope: "match:7:m1", ops: [{op: "set", path: ["state", "a"], value: 2}], seq: 4, prevSeq: 3});
  assert.deepEqual(page.stateOf("m1"), {a: 2, parsed: true});
  assert.equal(page.view("m1").seq, 4);
});

test("a delta that does not chain fetches the bouts again", async () => {
  store.clear();
  const bouts = {m1: {code: "m1", seq: 3, state: {a: 1}}};
  const calls = serve(bouts);
  const {page, streams} = mount();
  page.start();
  await settle();
  const before = calls.filter((c) => c.url === `${API}/stages/matches`).length;
  bouts.m1 = {code: "m1", seq: 9, state: {a: 5}};
  streams[0].emit({scope: "match:7:m1", ops: [{op: "set", path: ["state", "a"], value: 5}], seq: 9, prevSeq: 8});
  await settle();
  assert.equal(calls.filter((c) => c.url === `${API}/stages/matches`).length, before + 1, "the gap refetched");
  assert.deepEqual(page.stateOf("m1"), {a: 5, parsed: true});
});

test("edits a previous load left un-acked are re-sent and shown once the bouts arrive", async () => {
  store.clear();
  store.set("dope.pending.v2:match:7:m1", JSON.stringify([{op: "set", path: ["a"], value: 9, ts: Date.now()}]));
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page, shell} = mount();
  page.start();
  await settle();
  const patch = calls.find((c) => c.method === "PATCH");
  assert.ok(patch, "the recovered edit went out");
  assert.equal(patch.url, `${API}/matches/m1/state`);
  assert.deepEqual(patch.body.ops, [{path: ["a"], value: 9}]);
  assert.deepEqual(page.stateOf("m1"), {a: 9, parsed: true});
  assert.ok(shell.calls.events.some(([name]) => name === "recovered-pending"), "the recorder heard of it");
  assert.equal(store.get("dope.pending.v2:match:7:m1"), undefined, "acked, so no longer persisted");
});

test("a venues event replaces the venues and reads the bouts and the fest view again", async () => {
  store.clear();
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page, streams} = mount();
  page.start();
  await settle();
  const venues = page.venues;
  const fetched = calls.length;
  streams[0].emit({scope: "venues:f", data: [{number: 2, title: "Annex"}], seq: 1});
  await settle();
  assert.equal(page.venues, venues, "the one array, kept in place");
  assert.deepEqual(page.venues, [{number: 2, title: "Annex"}]);
  const after = calls.slice(fetched).map((c) => c.url);
  assert.ok(after.includes(`${API}/stages/matches`), "the bouts are read again");
  assert.ok(after.includes(API), "the fest view is read again");
  assert.equal(page.festStage("s1").title, "Fresh");
});

test("a roster change of this Game reads the bouts again and tells the page", async () => {
  store.clear();
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  let told = 0;
  const {page, streams} = mount({onRoster: () => told++});
  page.start();
  await settle();
  const fetched = calls.length;
  streams[0].emit({scope: "game-roster:8", data: {}, seq: 1});
  assert.equal(told, 0, "another Game's roster is not ours");
  streams[0].emit({scope: "game-roster:7", data: {}, seq: 1});
  await settle();
  assert.equal(told, 1);
  assert.ok(calls.slice(fetched).some((c) => c.url === `${API}/stages/matches`));
});

test("finish, the draw and the reseed go out as the host's structural writes", async () => {
  store.clear();
  const calls = serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page} = mount();
  page.start();
  await settle();
  page.finish("m1", true);
  await page.draw("d1", 42);
  const sent = await page.reseed("r1");
  await settle();
  assert.ok(sent.ok);
  const writes = calls.filter((c) => c.method !== "GET").map((c) => [c.method, c.url, c.body]);
  assert.deepEqual(writes, [
    ["POST", `${API}/matches/m1/finish`, {finished: true}],
    ["PUT", `${API}/draw`, {slot: "d1", participant: 42}],
    ["POST", `${API}/stages/r1/reseed`, undefined],
  ]);
});

test("a tab's stages and a stage's bouts come in the scheme's order", () => {
  const stages = [{code: "s1", matches: [{code: "s1-m2"}, {code: "s1-m1"}]}, {code: "s2", matches: [{code: "s2-m1"}]}];
  const tab = {stages: ["s2", "s1"]};
  assert.deepEqual(tabStages(stages, tab).map((stage) => stage.code), ["s1", "s2"]);
  const views = {"s1-m1": {code: "s1-m1"}, "s1-m2": {code: "s1-m2"}};
  const page = {view: (code) => views[code]};
  assert.deepEqual(stageBouts(page, stages[0]).map((bout) => bout.code), ["s1-m2", "s1-m1"]);
  // A bout the page has no view of yet is left out.
  assert.deepEqual(stageBouts(page, stages[1]), []);
});

test("a seat fields only the people with a real player id", () => {
  const view = {participants: [{roster: [{id: 7, name: "Анна"}, {id: 0, name: "Никто"}, {name: "Без id"}]}]};
  assert.deepEqual(seatRoster(view, 0), [{id: 7, name: "Анна"}]);
  assert.deepEqual(seatRoster(view, 1), []);
});

test("undo takes back this host's edit, and never another host's", async () => {
  store.clear();
  const bouts = {m1: {code: "m1", seq: 3, state: {a: 1, b: 1}}};
  const calls = serve(bouts);
  const {page, streams} = mount();
  page.start();
  await settle();
  // This host sets a and b in one action; the writer sends them.
  page.patch("m1", ["a"], 2);
  page.patch("m1", ["b"], 2);
  await settle();
  assert.deepEqual(page.stateOf("m1"), {a: 2, b: 2, parsed: true});
  // Another host then changes b.
  streams[0].emit({scope: "match:7:m1", ops: [{op: "set", path: ["state", "b"], value: 7}], seq: bouts.m1.seq + 1, prevSeq: bouts.m1.seq});
  bouts.m1.state.b = 7;
  bouts.m1.seq += 1;
  const result = page.undo();
  assert.deepEqual(result, {codes: ["m1"], skipped: 1}, "b is the other host's now, so it is skipped");
  await settle();
  const undone = calls.filter((c) => c.method === "PATCH").at(-1);
  assert.deepEqual(undone.body.ops.map((op) => [op.path, op.value]), [[["a"], 1]], "only a goes back");
  assert.deepEqual(page.stateOf("m1"), {a: 1, b: 7, parsed: true});
  assert.equal(page.undo(), null, "nothing is left to undo");
});

test("a spectator has nothing to undo", async () => {
  store.clear();
  serve({m1: {code: "m1", seq: 3, state: {a: 1}}});
  const {page} = mount({viewer: true});
  page.start();
  await settle();
  page.patch("m1", ["a"], 2);
  assert.equal(page.undo(), null);
});
