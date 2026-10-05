import {test} from "node:test";
import assert from "node:assert/strict";

// A node with a class, a tag, children and text — no layout. Tests assert
// classes and structure, never sizes.
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

const {standingsTable, resultsTeamCell, festLetters, letteredTitle} = await import("./dist/standings.js");

const walk = (root, out = []) => {
  out.push(root);
  (root.children || []).forEach((child) => walk(child, out));
  return out;
};
const classes = (n) => String(n.className || "").split(" ").filter(Boolean);
const withClass = (root, name) => walk(root).filter((n) => classes(n).includes(name));
const byTag = (root, tag) => walk(root).filter((n) => n.tag === tag);

// One builder for every standings-shaped table: place, name, numbers. A caller
// hands over columns and rows; the results-table skin — the head classes, the
// fading name cell, the marked rows — is the builder's, so no table restates it.
test("standingsTable draws the results-table skin from columns and rows", () => {
  const table = standingsTable({
    className: "group-standings-table",
    columns: [
      {label: "М", kind: "place"},
      {label: "Игрок", kind: "name"},
      {label: "Очки", kind: "num", className: "ek-stats-sum"},
      {label: "Круг 1", kind: "num"},
    ],
    rows: [
      [1, "Ктулху", 9, -3],
      [2, "ВШЭстером", 6, 2],
    ],
  });
  assert.equal(table.tag, "table");
  assert.deepEqual(classes(table), ["results-table", "group-standings-table"]);
  const heads = byTag(table, "th");
  assert.deepEqual(heads.map((h) => h.textContent), ["М", "Игрок", "Очки", "Круг 1"]);
  assert.deepEqual(heads.map(classes), [
    ["results-place-head", "pinned"], ["results-team-head", "pinned", "pin-edge"], ["results-num", "ek-stats-sum"], ["results-num"],
  ]);
  const rows = byTag(table, "tr").slice(1);
  assert.deepEqual(rows.map(classes), [
    ["results-row", "results-group-first"], ["results-row", "results-group-last"],
  ]);
  const cells = rows.map((row) => row.children.map(classes));
  assert.deepEqual(cells[0], [["results-place", "pinned"], ["name-cell", "results-team", "pinned", "pin-edge"], ["results-num", "ek-stats-sum"], ["results-num"]]);
  // The place and the name are pinned, the name one place column in.
  assert.deepEqual(rows[0].children.map((c) => c.style.left), ["0px", "var(--results-place-col)", undefined, undefined]);
  assert.deepEqual(rows[0].children.map((c) => c.textContent), ["1", "", "9", "−3"], "a leading minus is typographic");
  // The name column is the fading name cell, not a bare td.
  assert.equal(withClass(rows[0].children[1], "name-cell-text")[0].textContent, "Ктулху");
  assert.equal(withClass(rows[0].children[1], "popover-inline")[0].textContent, "Ктулху");
});

// A caller that needs more than text — an input, a link, a per-cell class —
// passes the cell it built; the builder still dresses it in the column's classes.
test("standingsTable takes a built cell and adds the column's classes", () => {
  const own = node("td");
  own.classList.add("brain-cross-live");
  const table = standingsTable({
    columns: [{label: "№", kind: "place"}, {label: "1", kind: "num", className: "brain-cross"}],
    rows: [[1, own]],
  });
  const [, cell] = byTag(table, "tr")[1].children;
  assert.equal(cell, own);
  assert.deepEqual(classes(cell), ["brain-cross-live", "results-num", "brain-cross"]);
});

// The first name pins and a second one scrolls (EK's stats: the player, then
// his team); a table without a place pins its name at the edge; a head can
// ride just past the pinned columns while its cells scroll.
test("standingsTable pins the first place and name it has", () => {
  const table = standingsTable({
    columns: [
      {label: "Player", kind: "name"},
      {label: "Team", kind: "name"},
      {label: "Players", trailHead: true},
    ],
    rows: [["a", "b", "c"]],
  });
  const [head, row] = byTag(table, "tr");
  assert.deepEqual(head.children.map((c) => c.style.left), ["0px", undefined, "var(--results-team-col)"]);
  assert.deepEqual(head.children.map((c) => classes(c).includes("pinned")), [true, false, false]);
  assert.deepEqual(row.children.map((c) => c.style.left), ["0px", undefined, undefined]);
});

// resultsTeamCell is the one name cell: a name that clips into a fade with the
// full text on a popover; a city under it, a flag before it, a link out of it.
test("resultsTeamCell carries the city, the flag and the link", () => {
  const cell = resultsTeamCell("Ктулху", {className: "reseed-team", city: "Москва", flag: "🇷🇺", href: "https://rating.chgk.info/teams/1"});
  assert.deepEqual(classes(cell), ["name-cell", "results-team", "reseed-team"]);
  const name = withClass(cell, "name-cell-text")[0];
  assert.equal(name.tag, "a");
  assert.equal(name.href, "https://rating.chgk.info/teams/1");
  assert.equal(name.textContent, "🇷🇺 Ктулху");
  assert.equal(name.attributes["aria-label"], "Ктулху", "the flag is decoration");
  assert.deepEqual(classes(name), ["name-cell-text", "quiet-link"]);
  assert.equal(withClass(cell, "results-team-city")[0].textContent, "Москва");
  assert.equal(withClass(cell, "popover-inline")[0].textContent, "🇷🇺 Ктулху");
  const plain = resultsTeamCell("Ктулху");
  assert.equal(withClass(plain, "name-cell-text")[0].tag, "span");
  assert.equal(withClass(plain, "results-team-city").length, 0);
});

// A бой's буква is the compiler's, carried on the fest view; the page reads
// it off by code and never counts. A бой the scheme left letterless (ТПШ's
// письменный отбор) has none.
test("festLetters reads each бой's letter off the fest view", () => {
  const letters = festLetters([
    {code: "s1", matches: [{code: "s1-m1", title: "Письменный отбор"}]},
    null,
    {code: "s2-r1", matches: [{code: "s2-r1-m1", letter: "A"}, {code: "s2-r1-m2", letter: "B"}]},
  ]);
  assert.equal(letters.get("s1-m1"), undefined);
  assert.equal(letters.get("s2-r1-m1"), "A");
  assert.equal(letters.get("s2-r1-m2"), "B");
});

test("letteredTitle rewrites the «Бой N» part and leaves the rest", () => {
  assert.equal(letteredTitle("Бой 3", "C"), "Бой C");
  assert.equal(letteredTitle("Группа 1. Бой 3", "C"), "Группа 1. Бой C");
  assert.equal(letteredTitle("Финал. Бой 2", "EA"), "Финал. Бой EA");
  assert.equal(letteredTitle("Письменный отбор", "A"), "Письменный отбор");
  assert.equal(letteredTitle("Бой 3", undefined), "Бой 3");
});

test("a statistics column sorts numbers biggest first and names from А, either way round", async () => {
  const {sortedOrder, sortValue} = await import("./dist/standings.js");
  const sums = ["300", "−10", "1,5", "300"].map((text) => sortValue({textContent: text}));
  assert.deepEqual(sums, [300, -10, 1.5, 300]);
  assert.deepEqual(sortedOrder(sums, false), {order: [0, 3, 2, 1], numeric: true}, "a tie keeps its order");
  assert.deepEqual(sortedOrder(sums, true).order, [1, 2, 0, 3]);
  const names = ["Вера", "анна", "Борис"].map((text) => sortValue({textContent: text}));
  assert.deepEqual(sortedOrder(names, false), {order: [1, 2, 0], numeric: false});
  assert.deepEqual(sortedOrder(names, true).order, [0, 2, 1]);
});
