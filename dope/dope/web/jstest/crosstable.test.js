import {test} from "node:test";
import assert from "node:assert/strict";

// A node with a class, a tag, children and text — no layout.
function node(tag) {
  const self = {
    tag,
    children: [],
    dataset: {},
    attributes: {},
    className: "",
    textContent: "",
    style: {setProperty(name, value) { self.style[name] = value; }},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
      toggle(name, on) {
        if (on) self.classList.add(name);
      },
    },
    setAttribute(name, value) {
      self.attributes[name] = String(value);
    },
    appendChild(child) {
      self.children.push(child);
      return child;
    },
  };
  return self;
}

globalThis.window = {};
globalThis.document = {createElement: node, activeElement: null};
globalThis.Node = class {}; // cells.ts asks `instanceof Node`; text never is one

const {buildCrosstables, crossSlot} = await import("./dist/crosstable.js");

const walk = (root, out = []) => {
  out.push(root);
  (root.children || []).forEach((child) => walk(child, out));
  return out;
};
const texts = (root, name) => walk(root)
  .filter((n) => String(n.className || "").split(" ").includes(name))
  .map((n) => n.textContent);

// A группа's row carries its seed number once a team sits in it; the row of a
// seat nobody has taken reads «Посев 4» and adds nothing.
test("a group's row shows the seed number of the team sitting there", () => {
  const seat = (n) => crossSlot({label: `Посев ${n}`, seed: {number: n}});
  const table = buildCrosstables({
    groups: [{
      title: "Группа A",
      entrants: [seat(1), seat(4)],
      bouts: [{slots: [seat(1), seat(4)], sides: [{name: "Ктулху", id: 7, score: 0}, {name: "", id: 0, score: 0}], finished: false, started: false}],
      standings: new Map(),
    }],
  });
  assert.deepEqual(texts(table, "seed-number"), ["1"]);
  assert.deepEqual(texts(table, "name-cell-text"), ["Ктулху", "Посев 4"]);
});
