import {assert, assertEquals, assertNotEquals} from "jsr:@std/assert";

// cssEscape asks window.CSS; with none it escapes quotes itself.
globalThis.window ??= {};

const {stackedSheet} = await import("./dist/stacked-sheet.js");
const ek = await import("./dist/ek-protocol.js");
const hamsa = await import("./dist/hamsa-protocol.js");
const troika = await import("./dist/troika-protocol.js");
const brain = await import("./dist/brain-protocol.js");
const {createUndo, valueAt} = await import("./dist/undo.js");

// ---- each page's sheet, built from its Protocol's own address ----

// A bout as the pages hold it: a code and the state the page parsed.
const bout = (code, state, seats = 4) => ({code, state, seats});

function ekSheet(bouts) {
  return stackedSheet({
    selector: ".ek-cell",
    fields: ek.SHEET_FIELDS,
    bouts: () => bouts,
    codeOf: (b) => b.code,
    rowsOf: (b) => Array.from({length: b.seats}, (_, seat) => ({seat})),
    columnsOf: (b) => ek.sheetColumns(b.themes, b.shootouts),
  });
}

const HAMSA_ROUNDS = {rounds: [{themes: 2, values: [100, 200, 300, 400, 500]}, {themes: 1, values: [400, 800, 1200, 1600, 2000]}]};

function hamsaSheet(bouts) {
  return stackedSheet({
    selector: ".hamsa-cell",
    fields: hamsa.SHEET_FIELDS,
    bouts: () => bouts,
    codeOf: (b) => b.code,
    rowsOf: (b) => Array.from({length: b.seats}, (_, seat) => ({seat})),
    columnsOf: (b) => hamsa.sheetColumns(b.state, b.shootout),
  });
}

function troikaSheet(bouts) {
  return stackedSheet({
    selector: ".troika-cell",
    fields: troika.SHEET_FIELDS,
    bouts: () => bouts,
    codeOf: (b) => b.code,
    rowsOf: (b) => troika.sheetRows(b.state),
    columnsOf: (b) => troika.sheetColumns(b.state),
  });
}

function writtenSheet(bouts) {
  return stackedSheet({
    selector: ".troika-count",
    fields: troika.WRITTEN_FIELDS,
    bouts: () => bouts,
    codeOf: (b) => b.code,
    rowsOf: (b) => troika.writtenRows(b.state),
    columnsOf: (b) => troika.sheetColumns(b.state),
  });
}

function brainSheet(bouts) {
  return stackedSheet({
    selector: ".answer-cell",
    fields: brain.SHEET_FIELDS,
    stack: "columns",
    bouts: () => bouts,
    codeOf: (b) => b.code,
    rowsOf: (b) => brain.sheetRows(b.state),
    columnsOf: () => brain.sheetColumns(),
  });
}

const sheets = {
  "ЭК": ekSheet([{code: "s1-m1", seats: 4, themes: 2, shootouts: 1}, {code: "s1-m2", seats: 3, themes: 2, shootouts: 0}]),
  "Хамса": hamsaSheet([
    {code: "h-m1", seats: 4, state: hamsa.parseState(HAMSA_ROUNDS, [1, 2, 3, 4]), shootout: true},
    {code: "h-m2", seats: 4, state: hamsa.parseState(HAMSA_ROUNDS, [5, 6, 7, 8]), shootout: false},
  ]),
  "Тройка": troikaSheet([
    bout("t-m1", troika.parseState({values: [1, 2], shootout: 0})),
    bout("t-m2", troika.parseState({values: [1, 2, 1], shootout: 1})),
  ]),
  "Тройка, письменный": writtenSheet([bout("t-w", troika.parseState({values: [1, 2], written: true}, 5))]),
  "брейн": brainSheet([
    bout("b-m1", brain.parseState(null, 3)),
    bout("b-m2", brain.parseState({tiebreaks: 1}, 3)),
  ]),
};

// presenceKey is what findTarget matches another host's cursor by.
const presenceKey = (sheet, address) => sheet.cursorKind.keys.map((key) => sheet.dataset(address)[key]).join("|");

for (const [name, sheet] of Object.entries(sheets)) {
  Deno.test(`${name}: every cell's address survives its dataset, and its coordinate`, () => {
    const addresses = sheet.addresses();
    assert(addresses.length > 0);
    for (const address of addresses) {
      const dataset = sheet.dataset(address);
      assertEquals(Object.keys(dataset).sort(), [...sheet.cursorKind.keys].sort(), "the dataset is the presence keys");
      assertEquals(Object.keys(address).sort(), [...sheet.cursorKind.keys].sort(), "the rows and columns hold exactly the fields");
      assertEquals(sheet.addressOf({dataset}), address);
      const coord = sheet.coordOf(address);
      assert(coord, JSON.stringify(address));
      assertEquals(sheet.addressAt(coord), address);
    }
  });

  Deno.test(`${name}: another host's cursor names one cell`, () => {
    const seen = new Map();
    for (const address of sheet.addresses()) {
      const key = presenceKey(sheet, address);
      assert(!seen.has(key), `${JSON.stringify(address)} and ${JSON.stringify(seen.get(key))} look the same to presence`);
      seen.set(key, address);
    }
  });
}

Deno.test("a field may not take a name the presence cursor uses itself", () => {
  // The cursor is {app, kind, gameID, ...address}: a field called kind once
  // replaced the cursor's kind, and another host's cursor went nowhere.
  for (const field of ["app", "kind", "gameID", "match"]) {
    let threw = false;
    try {
      stackedSheet({selector: ".x", fields: [field], bouts: () => [], codeOf: () => "", rowsOf: () => [], columnsOf: () => []});
    } catch {
      threw = true;
    }
    assert(threw, field);
  }
});

Deno.test("Хамса: the bet is not the first question of the first theme", () => {
  const sheet = sheets["Хамса"];
  const bet = {match: "h-m1", seat: 2, cellKind: "bet", theme: 0, q: 0};
  const first = {match: "h-m1", seat: 2, cellKind: "theme", theme: 0, q: 0};
  assertNotEquals(presenceKey(sheet, bet), presenceKey(sheet, first));
  assertNotEquals(sheet.selectorOf(bet), sheet.selectorOf(first));
  assertEquals(sheet.addressOf({dataset: sheet.dataset(bet)}), bet);
});

Deno.test("ЭК: a bout with a shootout is a longer row than one without", () => {
  const sheet = sheets["ЭК"];
  assertEquals(sheet.rows(0), 7);
  assertEquals(sheet.cols(0), 15);
  assertEquals(sheet.cols(4), 10);
  assertEquals(sheet.addressAt({row: 4, col: 0}), {match: "s1-m2", seat: 0, cellKind: "themes", theme: 0, q: 0});
  assertEquals(sheet.addressAt({row: 4, col: 12}), null, "the second bout has no shootout column");
  assertEquals(sheet.addressAt({row: 7, col: 0}), null);
});

Deno.test("брейн: the columns are only the bouts the tab draws", () => {
  // A Block's tab draws its own two bouts; the bouts of the other Blocks are
  // not in its sheet, so a column never leads to a bout off the screen.
  const drawn = [bout("b2-m1", brain.parseState(null, 3)), bout("b2-m2", brain.parseState({tiebreaks: 2}, 3))];
  const sheet = brainSheet(drawn);
  assertEquals(sheet.cols(0), 4);
  assertEquals(sheet.addressAt({row: 0, col: 0}), {match: "b2-m1", side: 0, q: 0}, "the first arrow lands on this tab's first bout");
  assertEquals(sheet.addressAt({row: 0, col: 4}), null);
  assertEquals(sheet.rows(0), 3);
  assertEquals(sheet.rows(3), 5, "a bout with tiebreaks is taller");
  assertEquals(sheet.addressOf({dataset: {match: "b1-m1", side: "0", q: "0"}}), null, "a bout of another tab has no place here");
});

// ---- where a cell's mark goes ----

Deno.test("the path of each kind of cell", () => {
  assertEquals(ek.answerPath(11, "themes", 3, 4), ["participants", "11", "themes", 3, "answers", 4]);
  assertEquals(ek.answerPath(11, "shootoutThemes", 0, 2), ["participants", "11", "shootoutThemes", 0, "answers", 2]);
  assertEquals(ek.shootoutThemePath(11, 1), ["participants", "11", "shootoutThemes", 1]);
  assertEquals(hamsa.markPath(5, {cellKind: "theme", theme: 2, q: 3}), ["participants", "5", "themes", 2, "answers", 3]);
  assertEquals(hamsa.markPath(5, {cellKind: "bet", theme: 0, q: 0}), ["participants", "5", "bet", "answer"]);
  assertEquals(hamsa.markPath(5, {cellKind: "shootout", theme: 0, q: 1}), ["participants", "5", "shootout", 0, "answers", 1]);
  assertEquals(troika.markPath({side: 1, chair: 2, theme: 3, q: 0}), ["sides", 1, "themes", 3, "answers", 0, 2]);
  assertEquals(troika.countPath({side: 4, theme: 1, q: 2}), ["sides", 4, "counts", 1, 2]);
  assertEquals(brain.markPath({side: 1, q: 5}), ["teams", 1, "rows", 5, "mark"]);
});

Deno.test("Хамса: a cell reads the mark its kind keeps", () => {
  const state = hamsa.parseState({...HAMSA_ROUNDS, participants: {"7": {
    themes: [{player: 0, answers: ["right", "", "", "", ""]}],
    bet: {amount: 100, answer: "wrong"},
    shootout: [{player: 0, answers: ["", "right", "", "", ""]}],
  }}}, [7]);
  assertEquals(hamsa.cellMark(state, 7, {cellKind: "theme", theme: 0, q: 0}), "right");
  assertEquals(hamsa.cellMark(state, 7, {cellKind: "bet", theme: 0, q: 0}), "wrong");
  assertEquals(hamsa.cellMark(state, 7, {cellKind: "shootout", theme: 0, q: 1}), "right");
});

// ---- writing marks ----

function fakeCell(dataset) {
  const classes = new Set();
  return {dataset, classList: {toggle: (name, on) => (on ? classes.add(name) : classes.delete(name)), contains: (name) => classes.has(name)}, classes};
}

Deno.test("a mark goes through the page's patch, and the cell shows it", () => {
  const marks = new Map();
  const patches = [];
  const written = [];
  const sheet = stackedSheet({
    selector: ".troika-cell",
    fields: troika.SHEET_FIELDS,
    bouts: () => [bout("t-m1", troika.parseState({values: [1]})), bout("t-m2", troika.parseState({values: [1]}))],
    codeOf: (b) => b.code,
    rowsOf: (b) => troika.sheetRows(b.state),
    columnsOf: (b) => troika.sheetColumns(b.state),
    marks: {
      markOf: (cell) => (cell.match === "t-m2" ? null : marks.get(presenceKey(sheet, cell)) || ""),
      setMark: (cell, mark) => marks.set(presenceKey(sheet, cell), mark),
      pathOf: troika.markPath,
      patch: (code, path, value) => patches.push([code, path, value]),
      onWritten: (codes) => written.push(codes),
    },
  });
  const cell = fakeCell(sheet.dataset({match: "t-m1", side: 1, chair: 2, theme: 0, q: 1}));
  const finished = fakeCell(sheet.dataset({match: "t-m2", side: 0, chair: 0, theme: 0, q: 0}));
  const stranger = fakeCell({match: "t-m9", side: "0", chair: "0", theme: "0", q: "0"});
  sheet.applyMarks([{cell, value: "+"}, {cell: finished, value: "+"}, {cell: stranger, value: "+"}]);
  assertEquals(patches, [["t-m1", ["sides", 1, "themes", 0, "answers", 1, 2], "right"]]);
  assert(cell.classes.has("right"));
  assertEquals(written, [["t-m1"]]);
  sheet.applyMarks([{cell, value: "+"}]);
  assertEquals(patches.length, 1, "the same mark again writes nothing");
});

// ---- ЭК's shootout theme, written as patches ----

Deno.test("ЭК: adding and dropping a shootout theme are patches a host can undo", () => {
  // The document as the bout page's overlay holds it, and its undo over it.
  const doc = {participants: {"11": {themes: [], shootoutThemes: []}, "22": {themes: [], shootoutThemes: []}}};
  const steps = [];
  const undo = createUndo({
    current: (_code, path) => valueAt(doc, path),
    apply: (_code, path, value) => set(doc, path, value ?? null),
    schedule: (close) => steps.push(close),
  });
  const patch = (path, value) => {
    undo.record("m", path, valueAt(doc, path), value);
    set(doc, path, value);
  };
  const gesture = () => steps.splice(0).forEach((close) => close());
  const shootouts = () => [...ek.parseState(doc, [11, 22], 0).sections.values()].map((section) => section.shootoutThemes.length);

  for (const id of [11, 22]) patch(ek.shootoutThemePath(id, 0), {answers: ["", "", "", "", ""]});
  gesture();
  assertEquals(shootouts(), [1, 1]);
  undo.undo();
  assertEquals(shootouts(), [0, 0], "undo takes the added theme back off every seat");

  for (const id of [11, 22]) patch(ek.shootoutThemePath(id, 0), {answers: ["", "", "", "", ""]});
  gesture();
  patch(ek.answerPath(11, "shootoutThemes", 0, 1), "right");
  gesture();
  for (const id of [11, 22]) patch(ek.shootoutThemePath(id, 0), null);
  gesture();
  assertEquals(shootouts(), [0, 0], "a dropped theme is gone before the server answers");
  undo.undo();
  assertEquals(shootouts(), [1, 1]);
  assertEquals(ek.parseState(doc, [11, 22], 0).sections.get(11).shootoutThemes[0].answers[1], "right", "and comes back with its marks");
});

function set(doc, path, value) {
  let node = doc;
  for (const part of path.slice(0, -1)) node = node[part] ??= {};
  node[path[path.length - 1]] = value;
}
