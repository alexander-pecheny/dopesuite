import {test} from "node:test";
import assert from "node:assert/strict";

// The dropdown builds real elements and listens for real events, so it gets a
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
    parentElement: null,
    listeners: {},
    style: {},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
      toggle() {},
    },
    addEventListener(type, fn) {
      (self.listeners[type] ||= []).push(fn);
    },
    dispatchEvent(event) {
      for (const fn of self.listeners[event.type] || []) fn(event);
      return true;
    },
    append(...kids) {
      for (const kid of kids) {
        kid.parentElement = self;
        self.children.push(kid);
      }
    },
    remove() {
      const at = self.parentElement ? self.parentElement.children.indexOf(self) : -1;
      if (at >= 0) self.parentElement.children.splice(at, 1);
      self.parentElement = null;
    },
    getBoundingClientRect: () => ({top: 0, left: 0, bottom: 20, width: 100}),
    scrollIntoView() {},
  };
  return self;
}

globalThis.document = {createElement: el};
globalThis.window = {
  innerWidth: 800,
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (id) => clearTimeout(id),
};
globalThis.Event = class {
  constructor(type) {
    this.type = type;
  }
};
globalThis.MouseEvent = class {
  constructor(type) {
    this.type = type;
  }
  preventDefault() {}
};

const {autocomplete} = await import("../assets/dist/esm/suggest.js");

const settle = () => new Promise((done) => setTimeout(done, 20));

function field(source, onPick) {
  const host = el("div");
  const input = el("input");
  host.append(input);
  autocomplete(input, source, onPick, {debounceMs: 1});
  return {host, input};
}

const popup = (host) => host.children.find((kid) => kid.className.includes("suggest-pop"));

async function type(input, text) {
  input.value = text;
  input.dispatchEvent(new Event("input"));
  await settle();
}

test("a pick hands the callback what was typed, not what it wrote", async () => {
  const picks = [];
  const {host, input} = field(
    () => [{value: " manual", label: "нет в базе"}],
    (choice, query) => picks.push([choice.value, query]),
  );

  await type(input, "Иванов");
  popup(host).children[0].dispatchEvent(new MouseEvent("mousedown"));

  assert.deepEqual(picks, [[" manual", "Иванов"]]);
  assert.equal(input.value, " manual");
});

test("the popup stays shut after a pick", async () => {
  let asked = 0;
  const {host, input} = field(() => {
    asked += 1;
    return [{value: "Печеный Александр", label: "Печеный Александр"}];
  });

  await type(input, "Печ");
  assert.equal(asked, 1);
  popup(host).children[0].dispatchEvent(new MouseEvent("mousedown"));
  await settle();

  assert.equal(asked, 1, "the input event a pick fires is not a search");
  assert.equal(popup(host), undefined);
});

test("typing on after a pick searches again", async () => {
  let asked = 0;
  const {host, input} = field(() => {
    asked += 1;
    return [{value: "Печеный Александр", label: "Печеный Александр"}];
  });

  await type(input, "Печ");
  popup(host).children[0].dispatchEvent(new MouseEvent("mousedown"));
  await settle();
  await type(input, "Печеный А");

  assert.equal(asked, 2);
  assert.ok(popup(host));
});
