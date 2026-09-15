import {test} from "node:test";
import assert from "node:assert/strict";

// The same paper DOM standings.test.js uses, plus what renderTabBar touches.
function node(tag) {
  const self = {
    tag,
    children: [],
    dataset: {},
    attributes: {},
    className: "",
    textContent: "",
    tabIndex: 0,
    hidden: false,
    listeners: {},
    classList: {
      add(...names) {
        self.className = [self.className, ...names].filter(Boolean).join(" ");
      },
      toggle() {},
    },
    setAttribute(name, value) {
      self.attributes[name] = String(value);
    },
    addEventListener(name, fn) {
      self.listeners[name] = fn;
    },
    appendChild(child) {
      self.children.push(child);
      return child;
    },
    replaceChildren(...kids) {
      self.children = kids;
    },
    querySelector: () => null,
    querySelectorAll: () => [],
    getBoundingClientRect: () => ({width: 0, height: 0}),
  };
  return self;
}

let href = "http://x/fest/1/game/od";
globalThis.window = {
  get location() {
    const url = new URL(href);
    return {pathname: url.pathname, search: url.search, hash: url.hash, href};
  },
  addEventListener() {},
};
globalThis.history = {replaceState: (_s, _t, next) => {
  href = new URL(next, "http://x").href;
}};
globalThis.document = {createElement: node, activeElement: null};
globalThis.Node = class {};

const divisions = await import("./dist/divisions.js");
const {resultsTeamCell, teamFlagBadges} = await import("./dist/standings.js");

const walk = (root, out = []) => {
  out.push(root);
  (root.children || []).forEach((child) => walk(child, out));
  return out;
};

test("divisionsOf keeps every distinct зачёт, in first-seen order", () => {
  assert.deepEqual(
    divisions.divisionsOf([["Школ", "Е"], undefined, ["Студ", "Школ"], [""], ["Е"]]),
    ["Школ", "Е", "Студ"],
  );
  assert.deepEqual(divisions.divisionsOf([undefined, [], [""]]), []);
});

test("«Все» takes everyone; a team with two зачёты stands in both", () => {
  assert.equal(divisions.inDivision(undefined, divisions.ALL_DIVISIONS), true);
  assert.equal(divisions.inDivision(["Школ", "Студ"], "Школ"), true);
  assert.equal(divisions.inDivision(["Школ", "Студ"], "Студ"), true);
  assert.equal(divisions.inDivision(["Школ"], "Студ"), false);
  assert.equal(divisions.inDivision(undefined, "Школ"), false);
});

test("the URL names the зачёт, and an unknown one is cleared as it is read", () => {
  href = "http://x/g?division=%D0%A8%D0%BA%D0%BE%D0%BB#results";
  assert.equal(divisions.divisionFromURL(["Школ", "Студ"]), "Школ");
  // A зачёт this game does not offer falls back to «Все» and leaves the URL clean.
  assert.equal(divisions.divisionFromURL(["Студ"]), divisions.ALL_DIVISIONS);
  assert.equal(window.location.search, "");
  assert.equal(window.location.hash, "#results");

  divisions.setDivisionInURL("Студ");
  assert.equal(window.location.search, "?division=%D0%A1%D1%82%D1%83%D0%B4");
  divisions.setDivisionInURL(divisions.ALL_DIVISIONS);
  assert.equal(window.location.search, "");
});

test("the chip row is «Все» first, then one .match-tab per зачёт", () => {
  const picked = [];
  const row = divisions.divisionChipRow(["Школ", "Студ"], "Студ", (d) => picked.push(d));
  assert.equal(row.className, "match-tabs division-chips");
  assert.equal(row.attributes.role, "tablist");
  const chips = row.children.filter((child) => child.className.includes("match-tab"));
  assert.deepEqual(chips.map((c) => c.textContent), ["Все", "Школ", "Студ"]);
  assert.deepEqual(chips.map((c) => c.attributes["aria-selected"]), ["false", "false", "true"]);
  chips[0].listeners.click();
  assert.deepEqual(picked, [divisions.ALL_DIVISIONS]);
  // Clicking the chip already chosen changes nothing.
  chips[2].listeners.click();
  assert.deepEqual(picked, [divisions.ALL_DIVISIONS]);
});

test("the badges hang off a team name; a team with no зачёт adds no node", () => {
  assert.equal(teamFlagBadges(undefined), null);
  assert.equal(teamFlagBadges([]), null);
  const badges = teamFlagBadges(["Школ", "Е"]);
  assert.deepEqual(badges.children.map((c) => [c.className, c.textContent]), [
    ["team-flag", "Школ"],
    ["team-flag", "Е"],
  ]);

  const cell = resultsTeamCell("Команда", {city: "Ереван", badges: ["Школ"]});
  const nodes = walk(cell);
  assert.deepEqual(nodes.filter((n) => n.className === "team-flag").map((n) => n.textContent), ["Школ"]);
  // The name and its popover say nothing of the зачёт.
  assert.equal(nodes.find((n) => n.className === "results-team-name").textContent, "Команда");
  assert.equal(nodes.find((n) => n.className.includes("results-team-name-popover")).textContent, "Команда");
  // The city is still there, on the same line as the badges.
  assert.equal(nodes.filter((n) => n.className === "results-team-city").length, 1);
  // And a cell with no зачёт is built exactly as it always was.
  const plain = walk(resultsTeamCell("Команда", {city: "Ереван"}));
  assert.equal(plain.filter((n) => n.className.includes("u-row")).length, 0);
});
