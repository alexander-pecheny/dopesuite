import {test} from "node:test";
import assert from "node:assert/strict";

// A small DOM: elements with classes, attributes, children and a parent, and
// the few selectors the module uses (".a", ".a.b", "[attr]", ":scope > .a", and
// lists of them). Layout is two numbers a test sets on a name: scrollWidth and
// clientWidth.
class FakeElement {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.parentElement = null;
    this.attributes = {};
    this.dataset = {};
    this.style = {};
    this.listeners = {};
    this.classes = new Set();
    this.ownText = "";
    this.scrollWidth = 0;
    this.clientWidth = 0;
    const self = this;
    this.classList = {
      add: (...names) => names.forEach((n) => self.classes.add(n)),
      contains: (name) => self.classes.has(name),
      toggle: (name, on) => (on ? self.classes.add(name) : self.classes.delete(name)),
    };
  }
  get className() { return [...this.classes].join(" "); }
  set className(value) { this.classes = new Set(String(value).split(" ").filter(Boolean)); }
  get textContent() { return this.ownText + this.children.map((c) => c.textContent).join(""); }
  set textContent(value) { this.children = []; this.ownText = String(value); }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  hasAttribute(name) { return name in this.attributes; }
  appendChild(child) { child.parentElement = this; this.children.push(child); return child; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  removeEventListener(name, fn) { this.listeners[name] = (this.listeners[name] || []).filter((f) => f !== fn); }
  dispatch(name, target) { for (const fn of this.listeners[name] || []) fn({target}); }
  get isConnected() {
    let node = this;
    while (node.parentElement) node = node.parentElement;
    return node.connected === true;
  }
  matchesOne(selector) {
    const attr = selector.match(/^\[([\w-]+)\]$/);
    if (attr) return this.hasAttribute(attr[1]);
    return selector.split(".").filter(Boolean).every((name) => this.classes.has(name));
  }
  matches(selector) { return selector.split(",").some((one) => this.matchesOne(one.trim())); }
  closest(selector) {
    for (let node = this; node; node = node.parentElement) if (node.matches(selector)) return node;
    return null;
  }
  descendants(out = []) {
    for (const child of this.children) { out.push(child); child.descendants(out); }
    return out;
  }
  querySelectorAll(selector) {
    const scoped = selector.match(/^:scope > (.+)$/);
    if (scoped) return this.children.filter((c) => c.matches(scoped[1]));
    return this.descendants().filter((n) => n.matches(selector));
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
class FakeSelect extends FakeElement {
  constructor() { super("select"); this.selectedOptions = []; }
}

// The observers are driven by hand: a test says what changed and when a frame
// passes.
let frames = [];
const resizeObservers = [];
const mutationObservers = [];
globalThis.window = {};
globalThis.Element = FakeElement;
globalThis.HTMLSelectElement = FakeSelect;
globalThis.Node = FakeElement;
globalThis.requestAnimationFrame = (fn) => { frames.push(fn); return frames.length; };
globalThis.cancelAnimationFrame = () => {};
globalThis.ResizeObserver = class {
  constructor(callback) { this.callback = callback; this.targets = new Set(); resizeObservers.push(this); }
  observe(target) { this.targets.add(target); }
  unobserve(target) { this.targets.delete(target); }
  disconnect() { this.targets.clear(); }
};
globalThis.MutationObserver = class {
  constructor(callback) { this.callback = callback; this.live = false; mutationObservers.push(this); }
  observe() { this.live = true; }
  disconnect() { this.live = false; }
};
// A control's label is measured in its font: here every letter is 10px wide.
globalThis.getComputedStyle = () => ({paddingLeft: "0", paddingRight: "0", font: "", fontSize: "13px"});
globalThis.document = {
  createElement: (tag) => tag === "canvas"
    ? {getContext: () => ({measureText: (text) => ({width: text.length * 10})})}
    : tag === "select" ? new FakeSelect() : new FakeElement(tag),
};

const {nameCell, bindNameCells, markNameControl, teamFlagBadges} = await import("./dist/name-cell.js");

function nextFrame() {
  const run = frames;
  frames = [];
  run.forEach((fn) => fn());
}
function page() {
  const root = new FakeElement("body");
  root.connected = true;
  return root;
}
const textOf = (cell) => cell.querySelector(".name-cell-text");
function size(cell, scrollWidth, clientWidth) {
  Object.assign(textOf(cell), {scrollWidth, clientWidth});
}
const mutate = (records) => mutationObservers.filter((o) => o.live).forEach((o) => o.callback(records));
const resized = (target) => resizeObservers.forEach((o) => o.targets.has(target) && o.callback([{target}]));

test("nameCell is one vocabulary: the cell, the wrap, the name and the popover's text", () => {
  const cell = nameCell("Ктулху", {className: "results-team"});
  assert.equal(cell.tag, "td");
  assert.equal(cell.className, "name-cell results-team");
  const [wrap, popover] = cell.children;
  assert.equal(wrap.className, "name-cell-wrap");
  assert.equal(popover.className, "popover popover-inline");
  assert.equal(popover.textContent, "Ктулху");
  const name = textOf(cell);
  assert.equal(name.parentElement, wrap);
  assert.equal(name.textContent, "Ктулху");
  assert.equal(name.tabIndex, 0, "a name is focusable, so the popover opens from the keyboard");
  assert.equal(name.attributes["aria-label"], "Ктулху");
  assert.ok(!cell.classList.contains("name-cell-truncated"), "only the pass says a name does not fit");
});

test("the detailed sheets set number, name and badges side by side in one layout", () => {
  const cell = nameCell("Ктулху", {
    tag: "th",
    layout: true,
    number: {text: "7", className: "od-detailed-team-number"},
    badges: ["Школ"],
    ariaLabel: "7. Ктулху",
    popoverText: "7. Ктулху",
  });
  assert.equal(cell.tag, "th");
  const [layout, popover] = cell.children;
  assert.equal(layout.className, "od-detailed-team-layout");
  assert.deepEqual(layout.children.map((c) => c.className), ["od-detailed-team-number", "name-cell-wrap", "u-row u-gap-xs u-align-center team-flags"]);
  assert.equal(layout.children[0].textContent, "7");
  assert.equal(textOf(cell).textContent, "Ктулху");
  assert.equal(textOf(cell).attributes["aria-label"], "7. Ктулху");
  assert.equal(popover.textContent, "7. Ктулху");
});

test("without a layout the city and the badges sit under the name, inside the wrap", () => {
  const cell = nameCell("Ктулху", {city: "Москва", badges: ["Е"]});
  const [wrap] = cell.children;
  const [name, line] = wrap.children;
  assert.equal(name.className, "name-cell-text");
  assert.deepEqual(line.children.map((c) => c.className), ["u-row u-gap-xs u-align-center team-flags", "results-team-city u-clip-fade"]);
  assert.equal(teamFlagBadges([]), null, "no badges, no node");
  assert.equal(cell.querySelector(".popover-inline").textContent, "Ктулху", "the city is not part of the name");
});

test("the pass flags exactly the names wider than their box, and lets go when they fit", () => {
  const root = page();
  const long = root.appendChild(nameCell("Очень длинное название команды"));
  const short = root.appendChild(nameCell("Б"));
  const edge = root.appendChild(nameCell("В"));
  size(long, 200, 80);
  size(short, 20, 80);
  size(edge, 81, 80);
  const pass = bindNameCells(root);
  assert.equal(bindNameCells(root), pass, "a root is bound once");
  nextFrame();
  assert.ok(long.classList.contains("name-cell-truncated"));
  assert.ok(!short.classList.contains("name-cell-truncated"));
  assert.ok(!edge.classList.contains("name-cell-truncated"), "a pixel of rounding is not a clip");

  // The column widens: the resize of the name is all the pass needs.
  size(long, 200, 240);
  resized(textOf(long));
  nextFrame();
  assert.ok(!long.classList.contains("name-cell-truncated"));
  pass.dispose();
});

test("a cell drawn later, or a name renamed, is measured without the page asking", () => {
  const root = page();
  const pass = bindNameCells(root);
  nextFrame();
  const table = new FakeElement("table");
  const cell = table.appendChild(nameCell("Ктулху"));
  size(cell, 200, 80);
  root.appendChild(table);
  mutate([{type: "childList", target: root, addedNodes: [table], removedNodes: []}]);
  nextFrame();
  assert.ok(cell.classList.contains("name-cell-truncated"), "a new table's names are measured");

  const name = textOf(cell);
  name.textContent = "К";
  size(cell, 10, 80);
  mutate([{type: "childList", target: name, addedNodes: [], removedNodes: []}]);
  nextFrame();
  assert.ok(!cell.classList.contains("name-cell-truncated"), "a shorter name is measured again");

  const observer = resizeObservers.at(-1);
  root.children = [];
  table.parentElement = null;
  mutate([{type: "childList", target: root, addedNodes: [], removedNodes: [table]}]);
  assert.ok(!observer.targets.has(name), "a removed cell is no longer observed");
  pass.dispose();
});

test("a seat picker's control is measured by its label, and its popover follows the choice", () => {
  const root = page();
  const wrap = root.appendChild(new FakeElement("span"));
  wrap.className = "player-select-wrap";
  const select = wrap.appendChild(new FakeSelect());
  select.clientWidth = 60;
  const popover = wrap.appendChild(new FakeElement("span"));
  popover.className = "popover popover-inline";
  markNameControl(wrap, select);
  select.selectedOptions = [{textContent: "Иван"}];
  const pass = bindNameCells(root);
  nextFrame();
  assert.ok(!wrap.classList.contains("name-cell-truncated"), "40px of label in 60px fits");
  assert.equal(popover.textContent, "Иван");

  // A <select> takes a new value without touching the DOM: the change event
  // is what the pass hears.
  select.selectedOptions = [{textContent: "Константин"}];
  root.dispatch("change", select);
  nextFrame();
  assert.ok(wrap.classList.contains("name-cell-truncated"));
  assert.equal(popover.textContent, "Константин");
  pass.dispose();
});

test("a name that may shrink steps its font down before it is called clipped", () => {
  const root = page();
  const cell = root.appendChild(nameCell("Длинная команда", {shrink: true}));
  const name = textOf(cell);
  // Two lines fit at 11px or below; the width always fits.
  Object.defineProperty(name, "scrollHeight", {get: () => (parseFloat(name.style.fontSize || "13") > 11 ? 40 : 30)});
  name.clientHeight = 30;
  name.scrollWidth = 50;
  name.clientWidth = 50;
  const pass = bindNameCells(root);
  nextFrame();
  assert.equal(name.style.fontSize, "11px");
  assert.ok(!cell.classList.contains("name-cell-truncated"), "a name that fits once shrunk is not faded");
  pass.dispose();
});
