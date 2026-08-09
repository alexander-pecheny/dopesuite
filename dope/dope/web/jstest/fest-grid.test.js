import {test} from "node:test";
import assert from "node:assert/strict";

// The grid builds real elements, so it gets a small DOM: enough of a node to
// carry a class, a tag, children and text. Nothing here needs layout.
function node(tag) {
  const self = {
    tag,
    children: [],
    dataset: {},
    style: {setProperty() {}},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
    },
    className: "",
    textContent: "",
    setAttribute(name, value) {
      self.attributes[name] = String(value);
    },
    attributes: {},
    appendChild(child) {
      self.children.push(child);
      return child;
    },
  };
  return self;
}

globalThis.window = {addEventListener() {}};
globalThis.requestAnimationFrame = () => 0;
globalThis.document = {createElement: node};
globalThis.HTMLAnchorElement = class {};

const {buildFestGrid} = await import("./dist/fest-grid.js");

function walk(root, out = []) {
  out.push(root);
  (root.children || []).forEach((child) => walk(child, out));
  return out;
}

const withClass = (root, name) => walk(root).filter((n) => String(n.className || "").split(" ").includes(name));

// A группа of nine plays twelve бои. Twelve boxes say less about who is winning
// than nine rows do, and the source sheets draw the rows — so a stage that ranks
// itself renders as a table, and its бои stay for the tab that lists them.
test("a Group renders as a table of place against team", () => {
  const grid = buildFestGrid({
    stages: [{
      code: "s1-g1",
      title: "Группа 1",
      stage_type: "matches",
      standings: [
        {rank: 1, name: "Ктулху", metrics: {place: 1, points: 9, total: 240}},
        {rank: 2, name: "ВШЭстером", metrics: {place: 2, points: 6, total: 180}},
      ],
      matches: [{code: "s1-g1-1", slots: []}, {code: "s1-g1-2", slots: []}],
    }],
  }, {stageHeaderLink: false});

  assert.equal(withClass(grid, "grid-standings").length, 1, "у группы должна быть таблица");
  assert.equal(withClass(grid, "grid-match").length, 0, "боёв в сетке быть не должно");
  const names = withClass(grid, "standings-name").map((cell) => cell.textContent);
  assert.deepEqual(names, ["", "Ктулху", "ВШЭстером"]);
  const heads = withClass(grid, "standings-metric").slice(0, 2).map((cell) => cell.textContent);
  assert.deepEqual(heads, ["Очки", "Σ"], "колонки — то, по чему блок ранжирует");
});

// A stage that ranks nothing — an elimination round — keeps its бои.
test("a round without standings still draws its бои", () => {
  const grid = buildFestGrid({
    stages: [{
      code: "s2-r1",
      title: "Финал",
      stage_type: "matches",
      matches: [{code: "s2-r1-m1", participantCount: 2, slots: [{label: "Ктулху"}, {label: "ВШЭстером"}]}],
    }],
  }, {stageHeaderLink: false});
  assert.equal(withClass(grid, "grid-standings").length, 0);
  assert.equal(withClass(grid, "grid-match").length, 1);
});
