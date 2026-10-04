import {test} from "node:test";
import assert from "node:assert/strict";

// A small DOM: elements with classes, attributes, children and listeners, the
// few selectors the picker uses (".a", "[attr]"), a <select> whose value picks
// among its options, and a body the tickbox panel parks on.
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
    this.disabled = false;
    const self = this;
    this.classList = {
      add: (...names) => names.forEach((n) => self.classes.add(n)),
      remove: (...names) => names.forEach((n) => self.classes.delete(n)),
      contains: (name) => self.classes.has(name),
      toggle: (name, on) => (on ? self.classes.add(name) : self.classes.delete(name)),
    };
  }
  get className() { return [...this.classes].join(" "); }
  set className(value) { this.classes = new Set(String(value).split(" ").filter(Boolean)); }
  get textContent() { return this.ownText + this.children.map((c) => c.textContent).join(""); }
  set textContent(value) { this.children = []; this.ownText = String(value); }
  get isConnected() {
    let node = this;
    while (node.parentElement) node = node.parentElement;
    return node === document.body;
  }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return this.attributes[name] ?? null; }
  hasAttribute(name) { return name in this.attributes; }
  appendChild(child) { child.remove(); child.parentElement = this; this.children.push(child); return child; }
  append(...children) { children.forEach((child) => this.appendChild(child)); }
  remove() {
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((c) => c !== this);
    this.parentElement = null;
  }
  contains(node) { for (let n = node; n; n = n.parentElement) if (n === this) return true; return false; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  removeEventListener(name, fn) { this.listeners[name] = (this.listeners[name] || []).filter((f) => f !== fn); }
  fire(name) { for (const fn of this.listeners[name] || []) fn({target: this, preventDefault() {}}); }
  focus() { document.activeElement = this; }
  blur() { if (document.activeElement === this) document.activeElement = null; }
  matches(selector) {
    const attr = selector.match(/^\[([\w-]+)\]$/);
    if (attr) return this.hasAttribute(attr[1]) || attr[1].replace(/^data-/, "").replace(/-(\w)/g, (_, c) => c.toUpperCase()) in this.dataset;
    return selector.split(".").filter(Boolean).every((name) => this.classes.has(name));
  }
  closest(selector) {
    for (let node = this; node; node = node.parentElement) if (node.matches(selector)) return node;
    return null;
  }
  descendants(out = []) {
    for (const child of this.children) { out.push(child); child.descendants(out); }
    return out;
  }
  querySelectorAll(selector) { return this.descendants().filter((n) => n.matches(selector)); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  getBoundingClientRect() { return {left: 10, top: 10, right: 110, bottom: 30, width: 100, height: 20}; }
  get offsetWidth() { return 180; }
  get offsetHeight() { return 100; }
}

class FakeSelect extends FakeElement {
  constructor() { super("select"); this.selectedIndex = -1; }
  get options() { return this.children.filter((c) => c.tag === "option"); }
  get value() { return this.options[this.selectedIndex]?.value ?? ""; }
  set value(value) { this.selectedIndex = this.options.findIndex((o) => o.value === String(value)); }
  get selectedOptions() { return this.selectedIndex < 0 ? [] : [this.options[this.selectedIndex]]; }
  // The host picks an option, as the browser would.
  pick(value) { this.value = value; this.fire("change"); }
}

const listeners = {};
globalThis.window = {
  innerWidth: 800,
  innerHeight: 600,
  addEventListener() {},
  removeEventListener() {},
};
globalThis.Element = FakeElement;
globalThis.Node = FakeElement;
globalThis.HTMLSelectElement = FakeSelect;
globalThis.document = {
  activeElement: null,
  body: new FakeElement("body"),
  documentElement: new FakeElement("html"),
  createElement: (tag) => (tag === "select" ? new FakeSelect() : new FakeElement(tag)),
  addEventListener: (name, fn) => { (listeners[name] ||= []).push(fn); },
  removeEventListener: (name, fn) => { listeners[name] = (listeners[name] || []).filter((f) => f !== fn); },
};

const {seatPicker, seatPickerOf, SEAT_PICKER_SELECTOR} = await import("./dist/seat-picker.js");

const ROSTER = [
  {id: "7", name: "Иван Петров"},
  {id: "8", name: "Анна Сидорова"},
  {id: "9", name: "Олег Котов"},
  {id: "10", name: "Мария Лис"},
];

function mount(spec) {
  const changes = [];
  const picker = seatPicker({roster: ROSTER, seated: [], onChange: (seated) => changes.push(seated), ...spec});
  const cell = new FakeElement("td");
  cell.appendChild(picker.element);
  document.body.appendChild(cell);
  const control = picker.element.querySelector(".seat-picker-control");
  const popover = picker.element.querySelector(".popover-inline");
  return {picker, control, popover, changes};
}

const panel = () => document.body.querySelector(".seat-picker-panel");
const boxes = () => panel().querySelectorAll(".seat-picker-option").map((row) => row.children[0]);

test("one seat is a select over the roster, with the nobody choice first", () => {
  const {picker, control, popover} = mount({seated: ["8"], nobody: "—", dataset: {team: "1", theme: "2"}});
  assert.equal(control.tag, "select");
  assert.ok(picker.element.classList.contains("seat-picker"));
  assert.ok(picker.element.classList.contains("name-cell"), "the wrap is a name cell, faded by the shell's pass");
  assert.ok(control.hasAttribute("data-name-control"), "measured by its label");
  assert.equal(popover.className, "popover popover-inline");
  assert.deepEqual(control.options.map((o) => [o.value, o.textContent]),
    [["", "—"], ["7", "Иван Петров"], ["8", "Анна Сидорова"], ["9", "Олег Котов"], ["10", "Мария Лис"]]);
  assert.equal(control.value, "8");
  assert.deepEqual(picker.seated(), ["8"]);
  assert.equal(control.dataset.team, "1", "the page's coordinates sit on the control");
  assert.ok(control.matches(SEAT_PICKER_SELECTOR));
  assert.equal(seatPickerOf(control), picker, "the control leads back to its picker");
});

test("picking writes the whole seating, and nobody is the empty list", () => {
  const {control, changes} = mount({seated: ["8"]});
  control.focus();
  control.pick("9");
  assert.deepEqual(changes, [["9"]]);
  assert.equal(document.activeElement, null, "the select lets the focus go back to the sheet");
  control.pick("");
  assert.deepEqual(changes, [["9"], []]);
  const blank = control.options[0];
  assert.equal(blank.value, "");
  assert.equal(blank.textContent, "", "no nobody label: an empty line");
});

test("update shows a remote seating in place, except under an open select", () => {
  const {picker, control, changes} = mount({seated: []});
  picker.update(["10"]);
  assert.equal(control.value, "10");
  picker.update([]);
  assert.equal(control.value, "");
  assert.deepEqual(picker.seated(), []);
  control.focus();
  picker.update(["7"]);
  assert.equal(control.value, "", "a select the host has open is not clobbered");
  control.blur();
  assert.deepEqual(changes, [], "an update is not a choice");
});

test("a seated id the roster lacks still shows, its id its label", () => {
  const {picker, control} = mount({seated: ["Гость"]});
  assert.equal(control.value, "Гость");
  assert.equal(control.selectedOptions[0].textContent, "Гость");
  picker.update(["Новенький"]);
  assert.equal(control.value, "Новенький");
});

test("three seats are a button with one line and a panel of tickboxes capped at three", () => {
  const {picker, control, popover, changes} = mount({
    cap: 3,
    seated: ["7", "8"],
    title: "Кто выходит на тему",
    line: (names) => names.map((n) => n.split(" ")[1]).join(", "),
  });
  assert.equal(control.tag, "button");
  assert.equal(control.attributes["aria-label"], "Кто выходит на тему");
  assert.equal(control.querySelector(".seat-picker-text").textContent, "Петров, Сидорова");
  assert.equal(popover.textContent, "Иван Петров\nАнна Сидорова", "the popover lists them whole, one per line");

  control.fire("click");
  assert.ok(panel(), "the panel parks on <body>");
  assert.equal(panel().attributes["aria-label"], "Кто выходит на тему");
  assert.equal(control.attributes["aria-expanded"], "true");
  assert.ok(document.documentElement.classList.contains("seat-picker-open"));
  assert.deepEqual(boxes().map((b) => b.checked), [true, true, false, false]);

  const [, , third, fourth] = boxes();
  third.checked = true;
  third.fire("change");
  assert.deepEqual(changes, [["7", "8", "9"]]);
  assert.equal(fourth.disabled, true, "a full seat takes no fourth");
  assert.equal(control.querySelector(".seat-picker-text").textContent, "Петров, Сидорова, Котов");
  assert.deepEqual(picker.seated(), ["7", "8", "9"]);

  const [first] = boxes();
  first.checked = false;
  first.fire("change");
  assert.deepEqual(changes.at(-1), ["8", "9"]);
  assert.equal(fourth.disabled, false);

  control.fire("click");
  assert.equal(panel(), null, "a second click closes it");
  assert.equal(control.attributes["aria-expanded"], "false");
  assert.ok(!document.documentElement.classList.contains("seat-picker-open"));
});

test("the panel opens on the seating the page last told the picker", () => {
  const {picker, control} = mount({cap: 3, seated: []});
  picker.update(["10", "9"]);
  assert.equal(control.querySelector(".seat-picker-text").textContent, "Мария Лис, Олег Котов", "names joined when no line is given");
  control.fire("click");
  assert.deepEqual(boxes().map((b) => b.checked), [false, false, true, true]);
  control.fire("click");
});

test("a team with no roster says so in the panel", () => {
  const {control} = mount({cap: 3, roster: [], seated: []});
  control.fire("click");
  assert.equal(boxes().length, 0);
  assert.ok(panel().textContent.length > 0, "the empty line, from the catalog");
  control.fire("click");
});

test("a disabled picker is disabled in either form", () => {
  assert.equal(mount({disabled: true}).control.disabled, true);
  assert.equal(mount({disabled: true, cap: 3}).control.disabled, true);
  assert.equal(mount({}).control.disabled, false);
});
