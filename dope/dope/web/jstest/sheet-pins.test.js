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
