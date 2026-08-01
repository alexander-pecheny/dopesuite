import {test} from "node:test";
import assert from "node:assert/strict";

// renderGameBreadcrumbs only touches createElement/appendChild/replaceChildren
// (plus createElementNS, since the home crumb is an SVG glyph), so a recording
// stub is enough to assert the trail it builds.
function fakeNode(tag) {
  return {
    tag, className: "", textContent: "", title: "",
    attrs: {},
    kids: [],
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...n) { this.kids.push(...n); },
    appendChild(n) { this.kids.push(n); return n; },
  };
}
globalThis.document = {
  createElement: (tag) => fakeNode(tag),
  createElementNS: (_ns, tag) => fakeNode(tag),
};

const {renderGameBreadcrumbs} = await import("./dist/game-page.js");

// trail renders the crumbs as "tag:class:text", separators dropped. The home
// crumb's text is empty: it is an SVG glyph now, not a 🏠 character.
function trail(options) {
  const kids = [];
  const root = {replaceChildren: () => kids.splice(0), appendChild: (n) => kids.push(n)};
  renderGameBreadcrumbs(root, options);
  return kids.filter((n) => n.className !== "crumb-sep")
    .map((n) => `${n.tag}:${n.className}:${n.textContent}${n.attrs.href ? " → " + n.attrs.href : ""}`);
}

test("the public trail starts at home and ends on the current page", () => {
  assert.deepEqual(trail({
    festTitle: "Кубок Города", festHref: "/fest/12",
    gameTitle: "ОД", gameHref: "/fest/12/game/3/", currentTitle: "Результаты",
  }), [
    "a:crumb crumb-home: → /",
    "a:crumb:Кубок Города → /fest/12",
    "a:crumb:ОД → /fest/12/game/3/",
    "span:crumb crumb-current:Результаты",
  ]);
});

test("the host tree carries the Мои фесты crumb its URL does", () => {
  const got = trail({
    host: true, festTitle: "Кубок Города", festHref: "/host/fest/12",
    gameTitle: "ЭК", gameHref: "/host/fest/12/game/3/", currentTitle: "Площадки",
  });
  assert.equal(got[1], "a:crumb:Мои фесты → /host");
  assert.equal(got.at(-1), "span:crumb crumb-current:Площадки");
});

test("a game with no sub-view ends on the game itself, unlinked", () => {
  assert.deepEqual(trail({festTitle: "Кубок", festHref: "/fest/12", gameTitle: "ОД"}), [
    "a:crumb crumb-home: → /",
    "a:crumb:Кубок → /fest/12",
    "span:crumb crumb-current:ОД",
  ]);
});

test("missing titles fall back rather than rendering blank crumbs", () => {
  assert.deepEqual(trail({}), [
    "a:crumb crumb-home: → /",
    "a:crumb:Фест → /",
    "span:crumb crumb-current:Игра",
  ]);
});

test("a sub-view whose name equals the game's does not repeat it", () => {
  const got = trail({festTitle: "Кубок", festHref: "/fest/12", gameTitle: "ОД", gameHref: "/fest/12/game/3/", currentTitle: "ОД"});
  assert.equal(got.length, 3);
  assert.equal(got.at(-1), "span:crumb crumb-current:ОД");
});
