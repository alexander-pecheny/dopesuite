import {test} from "node:test";
import assert from "node:assert/strict";
import * as P from "./dist/sheet-pins.js";
import * as T from "./dist/score-table.js";

// A cell as the module touches it: classes and an inline style.
function fakeCell() {
  const classes = new Set();
  const style = {};
  return {
    classes,
    style: {setProperty: (name, value) => { style[name] = value; }, values: style},
    classList: {add: (...names) => names.forEach((n) => classes.add(n))},
  };
}

test("pinOffsets sums the widths before each column", () => {
  assert.deepEqual(P.pinOffsets(["var(--a)", "var(--b)", "12px"]), {
    offsets: ["0px", "var(--a)", "calc(var(--a) + var(--b))"],
    end: "calc(var(--a) + var(--b) + 12px)",
  });
});

test("pinOffsets starts after a gutter", () => {
  assert.deepEqual(P.pinOffsets(["var(--a)", "var(--b)"], "var(--gutter)"), {
    offsets: ["var(--gutter)", "calc(var(--gutter) + var(--a))"],
    end: "calc(var(--gutter) + var(--a) + var(--b))",
  });
  assert.deepEqual(P.pinOffsets([]), {offsets: [], end: "0px"});
});

test("mark pins a declared column and draws the edge on the last one", () => {
  const pins = P.declarePins([{key: "name", width: "var(--team-col)"}, {key: "total", width: "var(--total-col)"}]);
  const name = pins.mark(fakeCell(), "name");
  const total = pins.mark(fakeCell(), "total");
  assert.deepEqual([...name.classes], ["pinned"]);
  assert.deepEqual(name.style.values, {position: "sticky", left: "0px"});
  assert.deepEqual([...total.classes], ["pinned", "pin-edge"]);
  assert.equal(total.style.values.left, "var(--team-col)");
  assert.equal(pins.end, "calc(var(--team-col) + var(--total-col))");
});

test("a column the sheet does not declare is left to scroll", () => {
  const pins = P.declarePins([{key: "name", width: "var(--team-col)"}]);
  const place = pins.mark(fakeCell(), "place");
  assert.equal(place.classes.size, 0);
  assert.deepEqual(place.style.values, {});
  assert.equal(pins.pinned("place"), false);
});

test("markSpan pins a cell over the whole block at its start", () => {
  const pins = P.declarePins([{key: "a", width: "1px"}, {key: "b", width: "2px"}], {start: "var(--gutter)"});
  const lead = pins.markSpan(fakeCell());
  assert.deepEqual([...lead.classes], ["pinned", "pin-edge"]);
  assert.equal(lead.style.values.left, "var(--gutter)");
  assert.equal(P.declarePins([]).markSpan(fakeCell()).classes.size, 0);
});

test("a key declared twice is refused", () => {
  assert.throws(() => P.declarePins([{key: "a", width: "1px"}, {key: "a", width: "2px"}]));
});

test("the score sheet's block: marker, name, Σ, place and its gap, after the corner", () => {
  const pins = T.scoreSheetPins({rowMarker: true});
  assert.deepEqual(pins.keys, ["marker", "name", "total", "place", "place-gap"]);
  assert.deepEqual(pins.offsets, [
    "var(--sheet-corner-col)",
    "calc(var(--sheet-corner-col) + var(--row-marker-col))",
    "calc(var(--sheet-corner-col) + var(--row-marker-col) + var(--team-col))",
    "calc(var(--sheet-corner-col) + var(--row-marker-col) + var(--team-col) + var(--total-col))",
    "calc(var(--sheet-corner-col) + var(--row-marker-col) + var(--team-col) + var(--total-col) + var(--place-col))",
  ]);
});

test("a sheet that leaves its place unpinned ends the block at Σ", () => {
  const pins = T.scoreSheetPins({place: false, total: "var(--hamsa-total-col)"});
  assert.deepEqual(pins.keys, ["name", "total"]);
  assert.equal(pins.end, "calc(var(--sheet-corner-col) + var(--team-col) + var(--hamsa-total-col))");
});

// A head row as the module touches it: its cells, each spanning some rows.
function fakeRow(...spans) {
  return {children: spans.map((rowSpan) => Object.assign(fakeCell(), {rowSpan}))};
}

test("headRowTops sums the heights of the rows above each row", () => {
  assert.deepEqual(P.headRowTops(["28px", "28px", "22px"]), ["0px", "28px", "calc(28px + 28px)"]);
  assert.deepEqual(P.headRowTops(["var(--head-row)", undefined]), ["0px", "var(--head-row)"]);
  assert.deepEqual(P.headRowTops([undefined]), ["0px"]);
  assert.deepEqual(P.headRowTops([]), []);
});

test("a head row above another must declare its height", () => {
  assert.throws(() => P.headRowTops([undefined, "22px"]), /head row 1 of 2 declares no height/);
  assert.throws(() => P.stackHeadRows([{row: fakeRow(1)}, {row: fakeRow(1)}]));
});

test("stackHeadRows sticks each row below the ones above it", () => {
  // Мультиигры: Команда spans both rows, a мини-игра's name the first, its
  // вопросы the second.
  const games = fakeRow(2, 1);
  const values = fakeRow(1, 1);
  P.stackHeadRows([{row: games, height: "var(--head-row)"}, {row: values}]);
  const [team, game] = games.children;
  assert.deepEqual(team.style.values, {position: "sticky", top: "0px"});
  assert.deepEqual(game.style.values, {position: "sticky", top: "0px", height: "var(--head-row)"});
  for (const cell of values.children) assert.deepEqual(cell.style.values, {position: "sticky", top: "var(--head-row)"});
});

test("the third row sticks below the first two, and a row that declares its height gets it", () => {
  // ОД's shootout entry: the rounds, the numbers, the locks; the controls
  // head spans all three.
  const rounds = fakeRow(1, 3);
  const numbers = fakeRow(1);
  const locks = fakeRow(1);
  P.stackHeadRows([{row: rounds, height: "28px"}, {row: numbers, height: "28px"}, {row: locks, height: "22px"}]);
  assert.equal(rounds.children[1].style.values.top, "0px");
  assert.equal(rounds.children[1].style.values.height, undefined);
  assert.deepEqual(numbers.children[0].style.values, {position: "sticky", top: "28px", height: "28px"});
  assert.deepEqual(locks.children[0].style.values, {position: "sticky", top: "calc(28px + 28px)", height: "22px"});
});

test("sheetHead builds the thead from its rows in order and stacks them", () => {
  const made = [];
  globalThis.document = {createElement: (tag) => {
    const node = {tag, children: [], appendChild(child) { this.children.push(child); return child; }};
    made.push(node);
    return node;
  }};
  const round = fakeRow(1);
  const themes = fakeRow(1);
  const thead = P.sheetHead([{row: round, height: "var(--head-row)"}, {row: themes}]);
  assert.equal(thead.tag, "thead");
  assert.deepEqual(thead.children, [round, themes]);
  assert.equal(themes.children[0].style.values.top, "var(--head-row)");
});
