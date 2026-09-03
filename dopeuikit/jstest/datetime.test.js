import {test} from "node:test";
import assert from "node:assert/strict";

// The calendar builds real elements and listens for real events, so it gets a
// small DOM: nodes that carry children, classes and listeners. Nothing here
// needs layout, so every box is the same box.
function el(tag) {
  const self = {
    tag,
    children: [],
    className: "",
    textContent: "",
    value: "",
    type: "",
    dataset: {},
    attrs: {},
    fired: [],
    focused: false,
    parentElement: null,
    listeners: {},
    style: {},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
      contains(name) {
        return self.className.split(/\s+/).includes(name);
      },
      toggle() {},
    },
    setAttribute(name, v) {
      self.attrs[name] = v;
    },
    addEventListener(type, fn) {
      (self.listeners[type] ||= []).push(fn);
    },
    dispatchEvent(event) {
      self.fired.push(event.type);
      for (const fn of self.listeners[event.type] || []) fn(event);
      return true;
    },
    append(...kids) {
      for (const kid of kids) {
        kid.parentElement = self;
        self.children.push(kid);
      }
    },
    replaceChildren(...kids) {
      for (const kid of self.children) kid.parentElement = null;
      self.children = [];
      self.append(...kids);
    },
    contains(node) {
      for (const kid of self.children) {
        if (kid === node || kid.contains(node)) return true;
      }
      return false;
    },
    remove() {
      const at = self.parentElement ? self.parentElement.children.indexOf(self) : -1;
      if (at >= 0) self.parentElement.children.splice(at, 1);
      self.parentElement = null;
    },
    querySelector(sel) {
      const name = sel.replace(/[[\]]/g, "");
      for (const kid of self.children) {
        if (kid.attrs[name] !== undefined) return kid;
      }
      return null;
    },
    focus() {
      self.focused = true;
    },
  };
  // The outside-click dismissal asks `target instanceof Node`; this stub makes
  // every element answer yes.
  Object.setPrototypeOf(self, Node.prototype);
  return self;
}

globalThis.Node = class Node {};
globalThis.document = {createElement: el, addEventListener() {}, activeElement: null};
globalThis.Event = class {
  constructor(type) {
    this.type = type;
  }
};

const {parseValue, composeValue, gridOf, mountDatetimeField} = await import("../assets/dist/esm/datetime.js");

test("parseValue reads a date with an optional time", () => {
  assert.deepEqual(parseValue("2026-09-04 19:00"), {date: "2026-09-04", time: "19:00"});
  assert.deepEqual(parseValue(" 2026-09-04T19:00 "), {date: "2026-09-04", time: "19:00"});
  assert.deepEqual(parseValue("2026-09-04"), {date: "2026-09-04", time: ""});
  assert.deepEqual(parseValue("завтра"), null);
  assert.deepEqual(parseValue(""), null);
});

test("composeValue merges date and time", () => {
  assert.equal(composeValue("2026-09-03", "19:00"), "2026-09-03 19:00");
  assert.equal(composeValue("2026-09-03", ""), "2026-09-03");
  assert.equal(composeValue("2026-09-03", "  "), "2026-09-03");
});

test("gridOf lays months out Monday-first", () => {
  // 1 September 2026 is a Tuesday: one leading cell, thirty days.
  assert.deepEqual(gridOf(2026, 8), {lead: 1, days: 30});
  // 1 February 2026 is a Sunday: six leading cells, twenty-eight days.
  assert.deepEqual(gridOf(2026, 1), {lead: 6, days: 28});
});

function field(value, tz) {
  const span = el("span");
  const text = el("input");
  text.attrs["data-datetime-text"] = "";
  if (tz) text.dataset.datetimeTz = tz;
  text.value = value;
  const button = el("button");
  button.attrs["data-datetime-open"] = "";
  span.append(text, button);
  mountDatetimeField(span);
  return {span, text, button};
}

const pop = (span) => span.children.find((kid) => kid.className.includes("calendar-pop"));
const timeRow = (p) => p.children[2]; // head, grid, timeRow, foot, [tzRow]
const foot = (p) => p.children[3];
const tzRow = (p) => p.children[4];
const days = (p) => gridOfPop(p).children.filter((kid) => kid.className.includes("calendar-day"));
const gridOfPop = (p) => p.children.find((kid) => kid.className.includes("calendar-grid"));

test("the button opens a Monday-first calendar on the value's month", () => {
  const f = field("2026-09-04 19:00");
  f.button.dispatchEvent(new Event("click"));
  const p = pop(f.span);
  assert.ok(p, "the calendar did not open");
  const grid = gridOfPop(p);
  assert.equal(grid.children[0].textContent, "пн");
  const cells = days(p);
  // September 2026 starts on a Tuesday: the first cell is Monday 31 August,
  // drawn out-of-month, and the 4th carries the selection.
  assert.equal(cells[0].textContent, "31");
  assert.ok(cells[0].className.includes("is-out"));
  assert.equal(cells[1].textContent, "1");
  assert.ok(cells[4].className.includes("is-selected"));
  assert.equal(cells.length, 35);
});

test("a pick writes the date and keeps popover open until Готово", () => {
  const f = field("2026-09-10 19:00");
  f.button.dispatchEvent(new Event("click"));
  const p = pop(f.span);
  assert.ok(p, "popover open");
  // The time input is prefilled from the initial value
  const timeInput = timeRow(p).children.find((c) => c.tag === "input");
  assert.equal(timeInput.value, "19:00");
  // Clicking a day updates the value while keeping popover open
  days(p)[3].dispatchEvent(new Event("click"));
  assert.equal(f.text.value, "2026-09-03 19:00");
  assert.ok(f.text.fired.includes("input"), "the form hears about the updated value");
  assert.ok(pop(f.span), "popover remains open so the user can tweak time");
  // Tweaking time input updates the field value live
  timeInput.value = "20:30";
  timeInput.dispatchEvent(new Event("input"));
  assert.equal(f.text.value, "2026-09-03 20:30");
  // Clicking Готово closes popover and focuses text input
  const doneBtn = foot(p).children.find((c) => c.textContent === "Готово");
  assert.ok(doneBtn, "Готово button exists");
  doneBtn.dispatchEvent(new Event("click"));
  assert.equal(pop(f.span), undefined, "Готово closes the popover");
  assert.ok(f.text.focused, "text input focused after finish");
});

test("the month arrows move without touching the value", () => {
  const f = field("2026-09-04");
  f.button.dispatchEvent(new Event("click"));
  const p = pop(f.span);
  const head = p.children[0]; // the head composes the kit's row utilities
  const title = () => head.children[1].textContent;
  head.children[2].dispatchEvent(new Event("click"));
  assert.equal(title(), "октябрь 2026");
  head.children[0].dispatchEvent(new Event("click"));
  head.children[0].dispatchEvent(new Event("click"));
  assert.equal(title(), "август 2026");
  assert.equal(f.text.value, "2026-09-04");
});

test("Очистить empties the field and closes", () => {
  const f = field("2026-09-04 19:00");
  f.button.dispatchEvent(new Event("click"));
  foot(pop(f.span)).children[0].dispatchEvent(new Event("click"));
  assert.equal(f.text.value, "");
  assert.ok(f.text.fired.includes("input"));
  assert.equal(pop(f.span), undefined);
});

test("the zone a value is written in shows under the grid when the page says one", () => {
  const f = field("", "Europe/Moscow");
  f.button.dispatchEvent(new Event("click"));
  const zone = tzRow(pop(f.span))?.children.find((k) => k.className.includes("calendar-tz"));
  assert.equal(zone.textContent, "Часовой пояс: Europe/Moscow");
});

test("no zone caption when the page names none", () => {
  const f = field("");
  f.button.dispatchEvent(new Event("click"));
  assert.equal(tzRow(pop(f.span)), undefined);
});

test("every button in the calendar is type=button — a default submit inside a form would fire validation on a required field", () => {
  const f = field("");
  f.button.dispatchEvent(new Event("click"));
  const p = pop(f.span);
  const buttons = [];
  const walk = (node) => {
    for (const kid of node.children) {
      if (kid.tag === "button") buttons.push(kid);
      walk(kid);
    }
  };
  walk(p);
  assert.ok(buttons.length >= 10, "nav, day and clear buttons expected");
  for (const b of buttons) {
    assert.equal(b.type, "button", "a typeless button inside the form submits it");
  }
});
