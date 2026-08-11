import {test} from "node:test";
import assert from "node:assert/strict";

// The grid builds real elements, so it gets a small DOM: enough of a node to
// carry a class, a tag, children and text. Nothing here needs layout.
function node(tag) {
  const self = {
    tag,
    children: [],
    dataset: {},
    props: {},
    style: {
      setProperty(name, value) {
        self.props[name] = value;
      },
    },
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
      matches: [
        {code: "s1-g1-1", slots: [], participants: [{name: "ВШЭстером"}, {name: "Ктулху"}]},
        {code: "s1-g1-2", slots: [], participants: [{name: "Ктулху"}, {name: "ВШЭстером"}]},
      ],
    }],
  }, {stageHeaderLink: false});

  assert.equal(withClass(grid, "grid-standings").length, 1, "у группы должна быть таблица");
  assert.equal(withClass(grid, "grid-match").length, 0, "боёв в сетке быть не должно");
  // Names lead and wear the box treatment — fade + popover, never «…» —
  // and the rows sit in seating order, not place order: a live группа must
  // not reshuffle under the reader with every закрытый бой.
  const names = withClass(grid, "standings-name").map((cell) =>
    walk(cell).find((n) => String(n.className || "").includes("grid-slot-team-name"))?.textContent ?? cell.textContent);
  assert.deepEqual(names, ["", "ВШЭстером", "Ктулху"]);
  const popovers = withClass(grid, "grid-slot-team-popover").map((n) => n.textContent);
  assert.deepEqual(popovers, ["ВШЭстером", "Ктулху"]);
  // One metric column, then М last — команда, очки, место.
  const heads = withClass(grid, "standings-metric").map((cell) => cell.textContent);
  assert.deepEqual(heads, ["О", "6", "9"], "колонка — то, по чему блок ранжирует первым");
  const places = withClass(grid, "standings-place").map((cell) => cell.textContent);
  assert.deepEqual(places, ["М", "2", "1"]);
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

// Every stage gets a column, so the layout has to be told how many there are —
// six was hard-coded, and a game with more crushed the rest into slivers.
test("the grid reports how many stages it drew", () => {
  const stage = (code) => ({code, title: code, stage_type: "matches", matches: []});
  const grid = buildFestGrid({
    stages: ["a", "b", "c", "d", "e", "f", "g"].map(stage),
  }, {stageHeaderLink: false});
  assert.equal(grid.props["--fest-stages"], "7");
});

// A ranking Block is one column: its groups' tables stack under one header
// rather than sprawling six columns wide. The Сетка is a glance.
test("a Block's groups share one column", () => {
  const group = (n) => ({
    code: `s1-g${n}`,
    title: `Групповой этап. Группа ${n}`,
    stage_type: "matches",
    grain: {block: "s1", group: String(n)},
    standings: [{rank: 1, name: `Лидер ${n}`, metrics: {place: 1, points: 9}}],
    matches: [],
  });
  const grid = buildFestGrid({stages: [group(1), group(2), group(3)]}, {stageHeaderLink: false});
  assert.equal(grid.props["--fest-stages"], "1", "групповой этап — одна колонка");
  assert.equal(withClass(grid, "grid-standings").length, 3, "таблица каждой группы на месте");
  const blockHeads = withClass(grid, "grid-stage-head");
  assert.equal(blockHeads.length, 1, "у колонки один заголовок блока");
  assert.equal(walk(blockHeads[0]).find((n) => n.tag === "h2").textContent, "Групповой этап");
  const groupHeads = withClass(grid, "grid-stage-subhead").map((n) => n.textContent);
  assert.deepEqual(groupHeads, ["Группа 1", "Группа 2", "Группа 3"]);
});

// A Block of pods draws compact tables too — the Сетка shows who finished
// where, not the бои; those belong to the block's own tab. Места come from the
// бои: the winner never eliminated, then by how late the second Loss came.
test("a Block of pods draws место against team, not бои", () => {
  const bout = (code, round, a, pa, b, pb, status = "finished") => ({
    code, round, status, participantCount: 2, slots: [{label: a}, {label: b}],
    participants: [{name: a, place: pa}, {name: b, place: pb}],
  });
  const pod = {
    code: "s2-g1",
    title: "DE 1",
    stage_type: "matches",
    kind: "matches",
    grain: {block: "s2", group: "1"},
    matches: [
      bout("s2-g1-m1", 1, "А", 1, "Б", 2),
      bout("s2-g1-m2", 1, "В", 1, "Г", 2),
      bout("s2-g1-m3", 2, "А", 1, "В", 2),
      bout("s2-g1-m4", 2, "Б", 1, "Г", 2),
      bout("s2-g1-m5", 3, "В", 1, "Б", 2),
    ],
  };
  const grid = buildFestGrid({stages: [pod]}, {stageHeaderLink: false});
  assert.equal(grid.props["--fest-stages"], "1");
  assert.equal(withClass(grid, "grid-match").length, 0, "бои в Сетке не рисуются");
  assert.equal(withClass(grid, "grid-standings").length, 1);
  const names = withClass(grid, "standings-name").map((cell) =>
    walk(cell).find((n) => String(n.className || "").includes("grid-slot-team-name"))?.textContent ?? cell.textContent);
  assert.deepEqual(names, ["", "А", "Б", "В", "Г"], "ряды в порядке посева, не мест");
  const places = withClass(grid, "standings-place").map((cell) => cell.textContent);
  // А won the winners' final and lost nothing; В took the last бой; Б fell in
  // it; Г lost twice by round 2.
  assert.deepEqual(places, ["М", "1", "3", "2", "4"]);
});

// A legacy grouped stage — matched by its -gN code alone, kind unknown — keeps
// its бои: the pod rules would misrank a round-robin группа.
test("a legacy group without standings keeps its бои", () => {
  const legacy = (n) => ({
    code: `s1-g${n}`,
    title: `Группа ${n}`,
    stage_type: "matches",
    matches: [{
      code: `s1-g${n}-1`, status: "finished", participantCount: 2,
      slots: [{label: "А"}, {label: "Б"}],
      participants: [{name: "А", place: 1}, {name: "Б", place: 2}],
    }],
  });
  const grid = buildFestGrid({stages: [legacy(1), legacy(2)]}, {stageHeaderLink: false});
  assert.equal(withClass(grid, "grid-standings").length, 0);
  assert.equal(withClass(grid, "grid-match").length, 2, "legacy бои остаются в Сетке");
});

// A pod still mid-play ranks only the eliminated: survivors' места are not
// invented while their бои are open.
test("an unfinished pod leaves survivors unplaced", () => {
  const bout = (code, round, a, pa, b, pb, status) => ({
    code, round, status, participantCount: 2, slots: [{label: a}, {label: b}],
    participants: [{name: a, place: pa}, {name: b, place: pb}],
  });
  const pod = {
    code: "s2-g1",
    title: "DE 1",
    stage_type: "matches",
    kind: "matches",
    grain: {block: "s2", group: "1"},
    matches: [
      bout("s2-g1-m1", 1, "А", 1, "Б", 2, "finished"),
      bout("s2-g1-m2", 1, "В", 1, "Г", 2, "finished"),
      bout("s2-g1-m3", 2, "А", 0, "В", 0, "pending"),
      bout("s2-g1-m4", 2, "Б", 1, "Г", 2, "finished"),
    ],
  };
  const grid = buildFestGrid({stages: [pod]}, {stageHeaderLink: false});
  const places = withClass(grid, "standings-place").map((cell) => cell.textContent);
  assert.deepEqual(places, ["М", "", "", "", "4"]);
});

// Bracket rounds carry no group, so they keep a column each — ЭК's Сетка
// stays a column per заход.
test("rounds without groups keep their own columns", () => {
  const round = (code, title) => ({
    code, title, stage_type: "matches",
    grain: {block: "s1", wave: 1},
    matches: [{code: `${code}-m1`, participantCount: 2, slots: [{label: "А"}, {label: "Б"}]}],
  });
  const grid = buildFestGrid({
    stages: [round("s1-r1-w1", "1/16, заход 1"), round("s1-r1-w2", "1/16, заход 2")],
  }, {stageHeaderLink: false});
  assert.equal(grid.props["--fest-stages"], "2");
});
