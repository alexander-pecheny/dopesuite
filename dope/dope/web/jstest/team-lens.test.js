import {test} from "node:test";
import assert from "node:assert/strict";

// The paper DOM divisions.test.js uses: enough for renderTabBar.
function node(tag) {
  const self = {
    tag,
    children: [],
    dataset: {},
    attributes: {},
    className: "",
    textContent: "",
    tabIndex: 0,
    hidden: false,
    listeners: {},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
      toggle() {},
    },
    setAttribute(name, value) {
      self.attributes[name] = String(value);
    },
    addEventListener(name, fn) {
      self.listeners[name] = fn;
    },
    appendChild(child) {
      self.children.push(child);
      return child;
    },
    replaceChildren(...kids) {
      self.children = kids;
    },
    querySelector: () => null,
    querySelectorAll: () => [],
    getBoundingClientRect: () => ({width: 0, height: 0}),
  };
  return self;
}

let href = "http://x/fest/1/game/od";
const windowListeners = {};
globalThis.window = {
  get location() {
    const url = new URL(href);
    return {pathname: url.pathname, search: url.search, hash: url.hash, href};
  },
  addEventListener(name, fn) {
    (windowListeners[name] ||= []).push(fn);
  },
};
globalThis.history = {replaceState: (_s, _t, next) => {
  href = new URL(next, "http://x").href;
}};
globalThis.document = {createElement: node, activeElement: null};
globalThis.Node = class {};

const {createTeamLens} = await import("./dist/team-lens.js");
const {ALL_DIVISIONS} = await import("./dist/divisions.js");
const {setHashTab} = await import("./dist/url-state.js");

// The server's order: by name, a team without a number and the guest teams
// (below zero) after the fest's teams.
const TEAMS = [
  {name: "Альфа", number: 3, flags: ["Школ", "ЧР"]},
  {name: "Бета", number: 0, flags: ["Студ"]},
  {name: "Гамма", number: 1, flags: ["ЧР"]},
  {name: "Дельта", number: 2, flags: []},
  {name: "Гость", number: -1},
];

function lensOver(teams = TEAMS, hidden = ["ЧР"]) {
  const calls = {moved: 0, render: 0};
  const lens = createTeamLens({
    teams: () => teams,
    hidden: () => hidden,
    moved: () => calls.moved++,
    render: () => calls.render++,
  });
  return {lens, calls};
}

const chipLabels = (row) => row.children.filter((c) => c.className.includes("match-tab")).map((c) => c.textContent);

test("a hidden Flag is never a chip and never a badge", () => {
  href = "http://x/g";
  const {lens} = lensOver();
  assert.deepEqual(lens.divisions(), ["Школ", "Студ"]);
  assert.deepEqual(chipLabels(lens.chips()), ["Все", "Школ", "Студ"]);
  assert.deepEqual(lens.badges(0), ["Школ"]);
  assert.deepEqual(lens.badges(2), []);
  assert.deepEqual(lens.badges(4), []);
});

test("a game whose teams carry no Flag shows no chips", () => {
  const {lens} = lensOver([{number: 1}, {number: 2, flags: ["ЧР"]}]);
  assert.equal(lens.chips(), null);
});

test("inside one Division there are no badges, and members are its rows", () => {
  href = "http://x/g?division=%D0%A8%D0%BA%D0%BE%D0%BB";
  const {lens, calls} = lensOver();
  assert.equal(lens.members(), undefined);
  assert.equal(lens.adopt(), true);
  assert.equal(calls.moved, 1);
  assert.equal(lens.active, "Школ");
  assert.deepEqual(lens.members(), [0]);
  assert.equal(lens.badges(0), undefined);
  // Nothing moved the second time.
  assert.equal(lens.adopt(), false);
  assert.equal(calls.moved, 1);
});

test("a stale ?division= is cleared, and so is a hidden one", () => {
  href = "http://x/g?division=Nope#results";
  const {lens} = lensOver();
  assert.equal(lens.adopt(), false);
  assert.equal(lens.active, ALL_DIVISIONS);
  assert.equal(window.location.search, "");
  assert.equal(window.location.hash, "#results");

  href = "http://x/g?division=%D0%A7%D0%A0#results";
  lens.adopt();
  assert.equal(lens.active, ALL_DIVISIONS);
  assert.equal(window.location.search, "");
});

test("before the document arrives the URL is left alone", () => {
  href = "http://x/g?division=%D0%A8%D0%BA%D0%BE%D0%BB";
  const lens = createTeamLens({teams: () => null, hidden: () => [], render() {}});
  assert.equal(lens.adopt(), false);
  assert.equal(window.location.search, "?division=%D0%A8%D0%BA%D0%BE%D0%BB");
});

test("picking a chip writes the URL, tells the page and draws it once", () => {
  href = "http://x/g#results";
  const {lens, calls} = lensOver();
  const chips = lens.chips().children.filter((c) => c.className.includes("match-tab"));
  chips[2].listeners.click();
  assert.equal(lens.active, "Студ");
  assert.equal(window.location.search, "?division=%D0%A1%D1%82%D1%83%D0%B4");
  assert.equal(window.location.hash, "#results");
  assert.deepEqual(calls, {moved: 1, render: 1});
});

test("name order is the server's order", () => {
  const {lens} = lensOver();
  assert.deepEqual(lens.order("name"), [0, 1, 2, 3, 4]);
  assert.deepEqual(lens.order("name", [3, 0]), [0, 3]);
});

test("number order puts the teams without a number, guests too, last in the server's order", () => {
  const {lens} = lensOver();
  assert.deepEqual(lens.order("number"), [2, 3, 0, 1, 4]);
  assert.deepEqual(lens.order("number", [4, 1, 0]), [0, 1, 4]);
});

test("a tab change keeps the Division; a moved URL is drawn once", () => {
  href = "http://x/g?division=%D0%A8%D0%BA%D0%BE%D0%BB#results";
  for (const name of Object.keys(windowListeners)) delete windowListeners[name];
  const {lens, calls} = lensOver();
  lens.adopt();
  let tab = "results";
  lens.follow(() => {
    const next = window.location.hash.slice(1);
    if (next === tab) return false;
    tab = next;
    return true;
  });
  const navigate = () => windowListeners.hashchange.forEach((fn) => fn());

  setHashTab("detailed");
  navigate();
  assert.equal(tab, "detailed");
  assert.equal(lens.active, "Школ");
  assert.equal(window.location.search, "?division=%D0%A8%D0%BA%D0%BE%D0%BB");
  assert.equal(calls.render, 1);

  // Back to the whole field, as a back step would.
  href = "http://x/g#detailed";
  navigate();
  assert.equal(lens.active, ALL_DIVISIONS);
  assert.equal(calls.render, 2);

  // Nothing moved: nothing is drawn.
  navigate();
  assert.equal(calls.render, 2);
});
