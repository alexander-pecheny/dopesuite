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
globalThis.Node = class {}; // match-table's cell helpers ask `instanceof Node`; text never is one

const {buildFestGrid, buildReseedStagePanel, planGrid, packBlock} = await import("./dist/fest-grid.js");

function walk(root, out = []) {
  out.push(root);
  (root.children || []).forEach((child) => walk(child, out));
  return out;
}

const withClass = (root, name) => walk(root).filter((n) => String(n.className || "").split(" ").includes(name));
const texts = (root, name) => withClass(root, name).map((n) => n.textContent);
// column reads a бой box column top to bottom: its head cell, then its cells.
const headText = (cell) => texts(cell, "grid-head-metric")[0] ?? cell.textContent;
const column = (root, name) => [...withClass(root, `slot-${name}-head`).map(headText), ...texts(root, `slot-${name}`)];
// tableColumn reads a results table's column under the head that says `label`,
// or null when no head does.
function tableColumn(root, label) {
  const [head, ...rows] = walk(root).filter((n) => n.tag === "tr");
  const index = head.children.findIndex((th) => th.textContent === label);
  return index < 0 ? null : rows.map((tr) => tr.children[index].textContent);
}

// A группа of nine plays twelve бои. Twelve boxes say less about who is winning
// than nine rows do, and the source sheets draw the rows — so a stage that ranks
// itself renders as a table, and its бои stay for the tab that lists them.
test("a Group renders as a table of place against team", () => {
  const grid = buildFestGrid({
    stages: [{
      code: "s1-g1",
      title: "Группа 1",
      stage_type: "matches",
      grain: {block: "s1", group: "1"},
      sort: [{metric: "points", dir: "desc"}, {metric: "total", dir: "desc"}],
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
  assert.deepEqual(texts(grid, "grid-slot-team-name"), ["ВШЭстером", "Ктулху"]);
  assert.deepEqual(texts(grid, "grid-slot-team-popover"), ["ВШЭстером", "Ктулху"]);
  // One metric column, then М last — команда, очки, место: the first of the
  // Ranker's sort rules the server sent, never guessed from the numbers.
  assert.deepEqual(column(grid, "total"), ["О", "6", "9"], "колонка — то, по чему блок ранжирует первым");
  assert.deepEqual(column(grid, "place"), ["М", "2", "1"]);
});

// The group table is a бой box: the same article, the same cells, the same
// head — only its metric and place columns are wider. One cell class, so the
// phone's tokens reach both by construction and neither can restate the skin.
test("a Group's table is built of the бой box's cells", () => {
  const grid = buildFestGrid({
    stages: [
      {
        code: "s1-g1", title: "Группа 1", stage_type: "matches", grain: {block: "s1", group: "1"},
        sort: [{metric: "points", dir: "desc"}],
        standings: [{rank: 1, name: "Ктулху", metrics: {place: 1, points: 9}}],
        matches: [],
      },
      {
        code: "s2-r1", title: "Финал", stage_type: "matches",
        matches: [{code: "s2-r1-m1", participantCount: 2, slots: [{label: "Ктулху"}, {label: "ВШЭстером"}]}],
      },
    ],
  }, {stageHeaderLink: false});
  const [table] = withClass(grid, "grid-standings");
  const [bout] = withClass(grid, "grid-match");
  assert.equal(table.tag, bout.tag);
  assert.ok(withClass(grid, "grid-box").includes(table) && withClass(grid, "grid-box").includes(bout), "одна кожа");
  const cells = (box) => walk(box).filter((n) => String(n.className).split(" ").includes("grid-slot-cell"));
  assert.equal(cells(table).length, 6, "заголовок и одна строка, по три ячейки");
  assert.equal(cells(bout).length, 9);
  assert.equal(withClass(table, "grid-slot-grid").length, 1);
  assert.equal(withClass(table, "grid-standings-bare").length, 0, "у таблицы есть колонка метрики");
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

// Every stage gets a column: seven stages, seven sections, and the CSS sizes
// the tracks by token — nothing here counts them.
test("every stage gets a column", () => {
  const stage = (code) => ({code, title: code, stage_type: "matches", matches: []});
  const grid = buildFestGrid({
    stages: ["a", "b", "c", "d", "e", "f", "g"].map(stage),
  }, {stageHeaderLink: false});
  assert.equal(withClass(grid, "grid-stage").length, 7);
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
  assert.equal(withClass(grid, "grid-stage").length, 1, "групповой этап — одна колонка");
  assert.equal(withClass(grid, "grid-standings").length, 3, "таблица каждой группы на месте");
  const blockHeads = withClass(grid, "grid-stage-head");
  assert.equal(blockHeads.length, 1, "у колонки один заголовок блока");
  assert.equal(walk(blockHeads[0]).find((n) => n.tag === "h2").textContent, "Групповой этап");
  const groupHeads = withClass(grid, "grid-standings").map((table) => texts(table, "grid-match-title")[0]);
  assert.deepEqual(groupHeads, ["Группа 1", "Группа 2", "Группа 3"]);
});

// A Block of pods draws compact tables too — the Сетка shows who finished
// where, not the бои; those belong to the block's own tab. The места are the
// server's: the pod Kind ranks on every finish and the view carries its table.
test("a Block of pods draws место against team, not бои", () => {
  const bout = (code, a, b) => ({code, participantCount: 2, slots: [{label: a}, {label: b}]});
  const pod = {
    code: "s2-g1",
    title: "DE 1",
    stage_type: "matches",
    kind: "de",
    grain: {block: "s2", group: "1"},
    matches: [bout("s2-g1-m1", "А", "Б"), bout("s2-g1-m2", "В", "Г")],
    standings: [
      {rank: 1, name: "А", metrics: {place: 1, losses: 0}},
      {rank: 2, name: "В", metrics: {place: 2, losses: 1}},
      {rank: 3, name: "Б", metrics: {place: 3, losses: 2}},
      {rank: 4, name: "Г", metrics: {place: 4, losses: 2}},
    ],
  };
  const grid = buildFestGrid({stages: [pod]}, {stageHeaderLink: false});
  assert.equal(withClass(grid, "grid-stage").length, 1);
  assert.equal(withClass(grid, "grid-match").length, 0, "бои в Сетке не рисуются");
  assert.equal(withClass(grid, "grid-standings").length, 1);
  assert.deepEqual(texts(grid, "grid-slot-team-name"), ["А", "Б", "В", "Г"], "ряды в порядке посева, не мест");
  assert.deepEqual(column(grid, "place"), ["М", "1", "3", "2", "4"]);
  // A pod's table is М alone: its Ranker sends no sort rules.
  assert.deepEqual(column(grid, "total"), []);
  assert.equal(withClass(grid, "grid-standings-bare").length, 1);
});

// A Group whose Ranker has not written a table yet — nothing finished — draws
// placeless rows in seating order, so the map of who sits where is there
// before a бой is played.
test("a Group without a table yet draws placeless rows", () => {
  const pod = {
    code: "s2-g1", title: "DE 1", stage_type: "matches", kind: "de",
    grain: {block: "s2", group: "1"},
    matches: [{code: "s2-g1-m1", participantCount: 2, slots: [{label: "А"}, {label: "Б"}]}],
  };
  const grid = buildFestGrid({stages: [pod]}, {stageHeaderLink: false});
  assert.deepEqual(column(grid, "place"), ["М", "", ""]);
});

// A stage the compiler never grained — a code that merely looks grouped,
// kind unknown — is no Group: it keeps its бои in a column of its own.
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
  assert.equal(withClass(grid, "grid-stage").length, 2);
});

// A бой's venue used to vanish when the бой in the same row of the previous
// column sat on the same table — readers took the blank for «no table».
test("every бой names its venue, however the previous column read", () => {
  const round = (code) => ({
    code, title: code, stage_type: "matches",
    matches: [{code: `${code}-m1`, venue: 1, participantCount: 2, slots: [{label: "А"}, {label: "Б"}]}],
  });
  const grid = buildFestGrid({stages: [round("s1-r1"), round("s1-r2")]}, {stageHeaderLink: false});
  const venues = withClass(grid, "grid-match-venue").map((n) => n.textContent);
  assert.deepEqual(venues, ["пл. 1", "пл. 1"]);
});

// A группа's table wears its name in its own head row, the way a бой box wears
// «Бой A · пл. 1» — one head, not a sub-heading over a headless table. A Group
// holds one table, so the venue is the Group's to show.
test("a Group's table head names the группа and its table", () => {
  const group = (n) => ({
    code: `s1-g${n}`,
    title: `Групповой этап. Группа ${n}`,
    stage_type: "matches",
    grain: {block: "s1", group: String(n)},
    standings: [{rank: 1, name: `Лидер ${n}`, metrics: {place: 1, points: 9}}],
    matches: [{code: `s1-g${n}-1`, venue: n + 2, slots: [], participants: [{name: `Лидер ${n}`}]}],
  });
  const grid = buildFestGrid({stages: [group(1), group(2)]}, {stageHeaderLink: false});
  assert.equal(withClass(grid, "grid-stage-subhead").length, 0, "подзаголовков больше нет");
  const heads = withClass(grid, "grid-standings").map((table) => {
    const head = withClass(table, "grid-match-head-cell")[0];
    return walk(head).filter((n) => n.tag === "span" && n.textContent).map((n) => n.textContent);
  });
  assert.deepEqual(heads, [["Группа 1", "пл. 3"], ["Группа 2", "пл. 4"]]);
});

// A lone table — a flat Block — has no группа to name, so its head carries the
// Block's title, as the ЭК sheet's stage table does.
test("a lone table's head carries the Block title", () => {
  const grid = buildFestGrid({
    stages: [{
      code: "s1", title: "Письменный отбор", stage_type: "matches",
      standings: [{rank: 1, name: "Ктулху", metrics: {place: 1, total: 470}}],
      matches: [{code: "s1-m1", slots: [], participants: [{name: "Ктулху"}]}],
    }],
  }, {stageHeaderLink: false});
  const head = withClass(grid, "grid-match-head-cell")[0];
  const spans = walk(head).filter((n) => n.tag === "span" && n.textContent).map((n) => n.textContent);
  assert.deepEqual(spans, ["Письменный отбор"]);
});

// The Сетка's rows are shared across columns like the sheet's. A бой that
// names its row sits there — the DE board puts each pod's бои in the pod's
// band, so a round with one бой per pod leaves the pod's second slot blank.
test("a бой sits on the row it names", () => {
  const grid = buildFestGrid({
    stages: [{
      code: "s2-r3", title: "Раунд 3", stage_type: "matches",
      matches: [
        {code: "a", row: 1, participantCount: 2, slots: [{label: "А"}, {label: "Б"}]},
        {code: "b", row: 3, participantCount: 2, slots: [{label: "В"}, {label: "Г"}]},
      ],
    }],
  }, {stageHeaderLink: false});
  const rows = withClass(grid, "grid-match").map((box) => box.props["grid-row"]);
  assert.deepEqual(rows, ["1 / span 1", "3 / span 1"]);
});

// A row is one бой box tall — a head and four seats. A группа of nine is two
// of them, so the group after it starts level with the third бой beside it.
test("a table taller than a бой spans as many rows as it needs", () => {
  const nine = Array.from({length: 9}, (_, i) => ({rank: i + 1, name: `Игрок ${i + 1}`, metrics: {place: i + 1, points: 9 - i}}));
  const group = (n) => ({
    code: `s1-g${n}`, title: `Группа ${n}`, stage_type: "matches",
    grain: {block: "s1", group: String(n)},
    standings: nine, matches: [],
  });
  const grid = buildFestGrid({stages: [group(1), group(2)]}, {stageHeaderLink: false});
  const spans = withClass(grid, "grid-standings").map((table) => table.props["grid-row"]);
  assert.deepEqual(spans, ["span 2", "span 2"]);
});

// The row is the grid's tallest box, up to a head and four seats: a board of
// two-seat бои packs three rows to the unit, a Сетка with a group of four five.
test("the row is as tall as the grid's tallest box, up to a head and four seats", () => {
  const bout = {code: "m", participantCount: 2, slots: [{label: "А"}, {label: "Б"}]};
  const board = buildFestGrid({stages: [{code: "r1", title: "Раунд 1", stage_type: "matches", matches: [bout]}]}, {stageHeaderLink: false});
  assert.equal(board.props["--grid-unit-rows"], "3");
  const four = Array.from({length: 4}, (_, i) => ({rank: i + 1, name: `К${i}`, metrics: {place: i + 1, points: 1}}));
  const grid = buildFestGrid({stages: [
    {code: "r1", title: "Финал", stage_type: "matches", matches: [bout]},
    {code: "s1-g1", title: "Группа 1", stage_type: "matches", grain: {block: "s1", group: "1"}, standings: four, matches: []},
  ]}, {stageHeaderLink: false});
  assert.equal(grid.props["--grid-unit-rows"], "5");
});

// The Пересев's «Бой» column speaks the sheet's language — буквы, not the
// stored s1-g5-2 — and a player of личная СИ lists the four бои the sum came
// from. A column that reads the same in every row (ТПШ's отбор seats all 24
// from one бой) says nothing and is dropped.
test("the Пересев names source бои by буква and drops a column that says one thing", () => {
  const letters = new Map([["s1-g5-2", "AB"], ["s1-g5-6", "AF"], ["s1-m1", "A"]]);
  const stage = (entries) => ({code: "s2", stage_type: "reseed", sort: [{metric: "total", dir: "desc"}], reseedEntries: entries});
  const many = buildReseedStagePanel(stage([
    {rank: 1, name: "Пётр", metrics: {match: "s1-g5-2+s1-g5-6", total: 600}},
    {rank: 2, name: "Олег", metrics: {match: "s1-g5-6", total: 290}},
  ]), {letters});
  assert.deepEqual(tableColumn(many, "Бой"), ["AB, AF", "AF"]);
  const same = buildReseedStagePanel(stage([
    {rank: 1, name: "Пётр", metrics: {match: "s1-m1", total: 600}},
    {rank: 2, name: "Олег", metrics: {match: "s1-m1", total: 290}},
  ]), {letters});
  assert.deepEqual(tableColumn(same, "Бой"), null);
});

// --- the plan: layout without a DOM --------------------------------------

const groupOf = (n, size) => ({
  code: `s1-g${n}`, title: `Группа ${n}`, stage_type: "matches", kind: "rr",
  grain: {block: "s1", group: String(n)},
  standings: Array.from({length: size}, (_, i) => ({rank: i + 1, name: `К${n}-${i}`, metrics: {place: i + 1, points: 1}})),
  matches: [],
});

// A group of nine is a head and nine rows: two units of a five-row grid. Its
// units are known before a box is drawn.
test("planGrid spans a nine-row group over two units", () => {
  const plan = planGrid([groupOf(1, 9), groupOf(2, 4)]);
  assert.equal(plan.unitRows, 5);
  assert.equal(plan.sections[0].kind, "block");
  assert.deepEqual(plan.sections[0].entries.map((entry) => entry.item.units), [2, 1]);
});

// Twelve групп of four at five to a column pack 4+4+4, not 5+5+2: the fewest
// rows that still take the columns the screen asked for.
test("packBlock evens the columns out", () => {
  assert.deepEqual(packBlock(Array(12).fill(1), 5), {rows: 4, cols: 3});
  const plan = planGrid(Array.from({length: 12}, (_, i) => groupOf(i + 1, 4)));
  assert.deepEqual([plan.sections[0].rows, plan.sections[0].cols], [12, 1]);
});

// A Block of one Group is its own column and stands alone in it, however tall
// the screen; before the screen is measured every Block is one column.
test("packBlock leaves a lone Group alone", () => {
  assert.deepEqual(packBlock([2], 40), {rows: 2, cols: 1});
  assert.deepEqual(packBlock([1, 1, 1], undefined), {rows: 3, cols: 1});
  const plan = planGrid([groupOf(1, 9)]);
  assert.equal(plan.sections[0].kind, "standings");
});

// Two grids on one page — the брейн Сетка and its pod board — keep their own
// rows and буквы: building the second changes nothing about the first.
test("two grids keep their own rows and letters", () => {
  const bout = {code: "s2-m1", participantCount: 2, slots: [{label: "А"}, {label: "Б"}]};
  const four = Array.from({length: 4}, (_, i) => ({rank: i + 1, name: `К${i}`, metrics: {place: i + 1, points: 1}}));
  const wide = buildFestGrid({stages: [
    {code: "s1-g1", title: "Группа 1", stage_type: "matches", grain: {block: "s1", group: "1"}, standings: four, matches: []},
    {code: "s2", title: "Финал", stage_type: "matches", matches: [{...bout, slots: [{label: "Бой 1, м. 1", fromMatch: {match: "s1-g1-1", place: 1}}, {label: "Б"}]}]},
  ]}, {stageHeaderLink: false, letters: new Map([["s1-g1-1", "A"], ["s2-m1", "Z"]])});
  const board = buildFestGrid({stages: [{code: "r1", title: "Раунд 1", stage_type: "matches", matches: [bout]}]}, {stageHeaderLink: false});
  assert.equal(wide.props["--grid-unit-rows"], "5");
  assert.equal(board.props["--grid-unit-rows"], "3");
  assert.deepEqual(texts(wide, "grid-match-title"), ["Группа 1", "Бой Z"]);
  assert.deepEqual(texts(board, "grid-match-title"), ["Бой s2-m1"]);
  assert.equal(texts(wide, "grid-slot-team-name")[4], "Бой A, м. 1");
});
