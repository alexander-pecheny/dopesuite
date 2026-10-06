import {test} from "node:test";
import assert from "node:assert/strict";

// mountBoutPage is driven through its interface: the shell, the stream and
// the fetch are fakes, and so is the sheet it draws into — it only has to
// take children and toggle classes. Short timers (the resync and fest-refresh
// debounces, the write window) fire at once, as in state-sync.test.js. The
// grid and the reseed tab are the module's own and build real elements, so
// the document makes small nodes, as in fest-grid.test.js.
globalThis.window = {
  setTimeout: (fn, ms) => {
    if (!ms || ms < 1000) fn();
    return 0;
  },
  clearTimeout() {},
  addEventListener() {},
  location: {pathname: "/host/fest/f/game/g/", hash: "", search: "", reload() {}},
  history: {replaceState(_state, _title, url) { window.location.hash = url.includes("#") ? url.slice(url.indexOf("#")) : ""; }},
};
globalThis.history = window.history;

function element(tag) {
  const self = {
    tag, children: [], dataset: {}, attributes: {}, listeners: {}, className: "", textContent: "",
    style: {setProperty() {}},
    classList: {
      add(...names) { self.className = [self.className, ...names].filter(Boolean).join(" "); },
      toggle() {},
    },
    setAttribute(name, value) { self.attributes[name] = String(value); },
    appendChild(child) { self.children.push(child); return child; },
    append(...children) { self.children.push(...children); },
    addEventListener(name, handler) { self.listeners[name] = handler; },
    matches: () => false,
    querySelector: () => null,
  };
  return self;
}
globalThis.document = {
  addEventListener() {}, visibilityState: "visible", scrollingElement: {scrollTop: 0}, documentElement: {},
  createElement: element, createElementNS: (_ns, tag) => element(tag), getElementById: () => null,
};
globalThis.HTMLAnchorElement = class {};
globalThis.Node = class {};
globalThis.requestAnimationFrame = (fn) => { fn(); return 1; };
globalThis.cancelAnimationFrame = () => {};
const store = new Map();
window.localStorage = {getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k)};

const {mountBoutPage, tabStages, stageBouts, seatRoster, boutAnchorID, BOUT_BOX_SELECTOR} = await import("./dist/bout-page.js");
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
    else if (url.startsWith(`${API}/matches/`) && url.endsWith("/finish")) answer = {};
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

function walk(root, out = []) {
  out.push(root);
  (root?.children || []).forEach((child) => walk(child, out));
  return out;
}

function mount({viewer = false, onRoster, app = "brain", scheme = {stages: []}, fest = {stages: [{code: "s1", title: "Init"}]}, hash = "", buildTab, shape, repaintCells} = {}) {
  window.location.hash = hash;
  const streams = [];
  const shell = fakeShell(viewer);
  const drawn = [];
  const root = fakeRoot();
  const page = mountBoutPage({
    app,
    root,
    tabsRoot: null,
    init: null,
    scheme,
    fest,
    title: () => "Brain",
    parse: (view) => ({...view.state, parsed: true}),
    blank: () => ({blank: true}),
    buildTab: (tab) => { drawn.push(tab?.key); return buildTab ? buildTab(page, tab) : fakeNode(); },
    buildRoster: () => fakeNode(),
    fitsFrame: () => false,
    boutSelector: ".bout",
    cursorKinds: {},
    shape,
    repaintCells,
    onRoster,
    shell,
    route: {viewer, festID: "f", gameID: "g", apiBase: API},
    newEventSource: () => { const s = fakeStream(); streams.push(s); return s; },
  });
  return {page, shell, streams, drawn, root};
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

// Every format's grid tab is the module's: a host's draw panel seats the slot
// through the page's draw, whatever the format.
test("a host's draw on the grid reaches the draw on every format", async () => {
  const drawStage = {
    code: "s1", title: "Round 2", stage_type: "matches",
    matches: [{code: "s1-m1", letter: "A", participantCount: 2, slots: [{label: "A1"}, {label: "Lot"}],
      participants: [{name: "One"}, {name: "", draw: {code: "s1-m1-d1", seated: 0, candidates: [{id: 7, name: "Seven"}]}}]}],
  };
  for (const app of ["ek", "hamsa", "troika", "brain"]) {
    store.clear();
    const calls = serve({});
    const {page, root} = mount({app, fest: {stages: [drawStage]}});
    page.start();
    await settle();
    assert.equal(page.tab()?.key, "grid", `${app} opens on the grid`);
    const select = walk(root.node).find((n) => n.tag === "select");
    assert.ok(select, `${app}'s grid draws the panel for a host`);
    select.value = "7";
    select.listeners.change();
    await settle();
    assert.ok(calls.some((c) => c.method === "PUT" && c.url === `${API}/draw` && c.body.participant === 7), `${app}'s draw went out`);
  }
});

// An old /matches/<letter> address arrives as #@A: the page opens the tab that
// holds bout A, and keeps A as the anchor to scroll to.
test("#@A opens the tab that holds bout A", () => {
  store.clear();
  serve({});
  const stage = {code: "s1", title: "Game 1", stage_type: "matches", matches: [{code: "s1-m1"}]};
  const {page} = mount({
    app: "hamsa",
    scheme: {stages: [stage]},
    fest: {stages: [{...stage, matches: [{code: "s1-m1", letter: "A"}]}]},
    hash: "#@A",
  });
  assert.equal(page.tab()?.key, "protocol:s1");
  assert.equal(window.location.hash, "#protocol:s1@A");
  assert.equal(page.boutHref("s1-m1"), "#protocol:s1@A");
  window.location.hash = "";
});

// The server says whether a reseed can be calculated; when it refuses all the
// same, its reason stands under the panel until the next try.
test("a refused reseed shows the server's reason on its panel", async () => {
  store.clear();
  const calls = serve({});
  const reseedStage = {code: "r1", title: "Reseed", stage_type: "reseed", matches: []};
  const final = {code: "s2", title: "Final", stage_type: "matches", matches: [{code: "s2-m1"}]};
  const {page, root} = mount({
    app: "hamsa",
    scheme: {stages: [reseedStage, final]},
    fest: {stages: [{...reseedStage, reseedReady: true}, final]},
    hash: "#reseed:r1",
  });
  const refuse = globalThis.fetch;
  globalThis.fetch = (url, options = {}) => {
    if (url === `${API}/stages/r1/reseed`) {
      calls.push({url, method: options.method || "GET"});
      return Promise.resolve({ok: false, status: 400, text: () => Promise.resolve("Bout A is not finished\n")});
    }
    return refuse(url, options);
  };
  page.start();
  await settle();
  const button = walk(root.node).find((n) => n.tag === "button");
  assert.equal(button.disabled, false, "the server said it is ready");
  button.listeners.click();
  await settle();
  const errors = walk(root.node).filter((n) => n.className === "hint hint-danger").map((n) => n.textContent);
  assert.deepEqual(errors, ["Bout A is not finished"]);
  globalThis.fetch = refuse;
});

// Two stages of bouts, each its own tab, bout A on the first.
const SHEET_STAGES = [
  {code: "s1", title: "Game 1", stage_type: "matches", matches: [{code: "m1"}, {code: "m3"}]},
  {code: "s2", title: "Game 2", stage_type: "matches", matches: [{code: "m2"}]},
];
const SHEET_FEST = {stages: SHEET_STAGES.map((stage) => ({...stage, matches: stage.matches.map((match, i) => ({...match, letter: `${stage.code}${i}`}))}))};
SHEET_FEST.stages[0].matches[0].letter = "A";

// sheetTab draws the boxes of the bouts a tab's stages hold, the one way a
// page makes a box.
function sheetTab(page, tab) {
  const wrap = element("div");
  for (const stage of tabStages(SHEET_STAGES, tab)) {
    for (const bout of stageBouts(page, stage)) wrap.appendChild(page.boutBox(bout.code, "x-bout"));
  }
  return wrap;
}

function sheetBouts() {
  return {
    m1: {code: "m1", seq: 3, state: {a: 1, seats: "one"}},
    m3: {code: "m3", seq: 3, state: {a: 1, seats: "one"}},
    m2: {code: "m2", seq: 3, state: {a: 1, seats: "one"}},
  };
}

const boxesOf = (node) => walk(node).filter((n) => n.dataset?.bout);

test("every bout box carries the id boutAnchorID gives it, on every format", async () => {
  assert.equal(BOUT_BOX_SELECTOR, "[data-bout]", "the steady redraw finds the boxes by their code");
  for (const app of ["ek", "hamsa", "troika", "brain"]) {
    store.clear();
    serve(sheetBouts());
    const {page, root} = mount({app, scheme: {stages: SHEET_STAGES}, fest: SHEET_FEST, hash: "#@A", buildTab: sheetTab});
    page.start();
    await settle();
    const boxes = boxesOf(root.node);
    // Troika keeps a Block's stages on one protocols tab, so m2 comes too.
    assert.deepEqual(boxes.map((box) => box.dataset.bout).slice(0, 2), ["m1", "m3"], `${app} draws the bouts of the tab that holds A`);
    for (const box of boxes) assert.equal(box.id, boutAnchorID(box.dataset.bout), `${app}'s box of ${box.dataset.bout}`);
  }
  window.location.hash = "";
});

// The repaint contract: a remote delta that leaves a bout's shape as drawn is
// repainted in place; one that changes it draws the tab again; one for a bout
// on another tab leaves the tab alone.
test("a remote delta of the same shape repaints the bout, a new shape draws the tab again", async () => {
  store.clear();
  serve(sheetBouts());
  const repainted = [];
  const {page, streams, drawn} = mount({
    app: "hamsa", scheme: {stages: SHEET_STAGES}, fest: SHEET_FEST, hash: "#@A", buildTab: sheetTab,
    shape: (code) => String(page.stateOf(code).seats),
    repaintCells: (code) => repainted.push(code),
  });
  page.start();
  await settle();
  const draws = drawn.length;
  streams[0].emit({scope: "match:7:m1", ops: [{op: "set", path: ["state", "a"], value: 2}], seq: 4, prevSeq: 3});
  assert.deepEqual(repainted, ["m1"], "a mark repaints the bout");
  assert.equal(drawn.length, draws, "and draws no tab");
  streams[0].emit({scope: "match:7:m2", ops: [{op: "set", path: ["state", "seats"], value: "two"}], seq: 4, prevSeq: 3});
  assert.equal(drawn.length, draws, "a bout on another tab draws nothing here");
  assert.deepEqual(repainted, ["m1"]);
  streams[0].emit({scope: "match:7:m3", ops: [{op: "set", path: ["state", "seats"], value: "two"}], seq: 4, prevSeq: 3});
  assert.equal(drawn.length, draws + 1, "a new seating draws the tab again");
  assert.deepEqual(repainted, ["m1"]);
  streams[0].emit({scope: "match:7:m3", ops: [{op: "set", path: ["state", "a"], value: 5}], seq: 5, prevSeq: 4});
  assert.deepEqual(repainted, ["m1", "m3"], "against the shape drawn last, the next mark repaints again");
  window.location.hash = "";
});

test("this host's own edit goes through the same contract", async () => {
  store.clear();
  serve(sheetBouts());
  const repainted = [];
  const {page, drawn} = mount({
    app: "ek", scheme: {stages: SHEET_STAGES}, fest: SHEET_FEST, hash: "#@A", buildTab: sheetTab,
    shape: (code) => String(page.stateOf(code).seats),
    repaintCells: (code) => repainted.push(code),
  });
  page.start();
  await settle();
  const draws = drawn.length;
  page.stateOf("m1").a = 3;
  page.refresh("m1");
  assert.deepEqual(repainted, ["m1"]);
  page.stateOf("m1").seats = "two";
  page.refresh("m1");
  assert.equal(drawn.length, draws + 1, "an edit of the seating draws the tab again");
  window.location.hash = "";
});

// Unticking a finished bout reopens it at once: the sheet reads the flag from
// the bout's view, and a mark right after the untick must not be refused
// while the server's answer is on its way.
test("a host's untick reopens the bout before the server answers", async () => {
  store.clear();
  const bouts = sheetBouts();
  bouts.m1.finished = true;
  serve(bouts);
  const {page} = mount({app: "brain", scheme: {stages: SHEET_STAGES}, fest: SHEET_FEST, hash: "#@A", buildTab: sheetTab});
  page.start();
  await settle();
  assert.equal(page.view("m1").finished, true);
  page.finish("m1", false);
  assert.equal(page.view("m1").finished, false, "the view says open at once");
  window.location.hash = "";
});
