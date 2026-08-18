import {test} from "node:test";
import assert from "node:assert/strict";

// The shell touches the document only to paint the trail, set the title and
// mark the body; a recording stub is enough. No ☰ menu is mounted (dopeMenu is
// absent), and the recorder finds no localStorage.
function fakeNode(tag) {
  return {
    tag, className: "", textContent: "", title: "",
    attrs: {}, kids: [], classes: new Set(),
    classList: {toggle(c, on) { on ? this.classes.add(c) : this.classes.delete(c); }, contains(c) { return this.classes.has(c); }},
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...n) { this.kids.push(...n); },
    appendChild(n) { this.kids.push(n); return n; },
    querySelector: () => null,
    contains: () => false,
  };
}
const body = fakeNode("body");
body.classList = {toggle: (c, on) => (on ? body.classes.add(c) : body.classes.delete(c))};
globalThis.window = {location: {search: ""}, addEventListener() {}};
globalThis.location = window.location;
globalThis.document = {
  body,
  title: "",
  createElement: (tag) => fakeNode(tag),
  createElementNS: (_ns, tag) => fakeNode(tag),
  querySelector: () => null,
  addEventListener() {},
};

const {mountGamePage} = await import("./dist/game-shell.js");

function mount(viewer, chrome) {
  const crumbs = fakeNode("nav");
  crumbs.replaceChildren = (...n) => { crumbs.kids = n; };
  const shell = mountGamePage({
    app: "brain",
    root: fakeNode("div"),
    breadcrumbsNode: crumbs,
    festID: "12",
    gameID: "3",
    viewer,
    init: {canEdit: true, gameID: 30},
    chrome: () => chrome,
    cursorKinds: {},
  });
  shell.renderChrome();
  const trail = crumbs.kids.filter((n) => n.className !== "crumb-sep").map((n) => `${n.className}:${n.textContent}${n.attrs.href ? " → " + n.attrs.href : ""}`);
  return {shell, trail};
}

test("the host trail carries Мои фесты, the viewer's does not, and both end on the game", () => {
  const host = mount(false, {festTitle: "Кубок", gameTitle: "Брейн"});
  assert.deepEqual(host.trail, ["crumb crumb-home: → /", "crumb:Мои фесты → /host", "crumb:Кубок → /host/fest/12", "crumb crumb-current:Брейн"]);
  const viewer = mount(true, {festTitle: "Кубок", gameTitle: "Брейн"});
  assert.deepEqual(viewer.trail, ["crumb crumb-home: → /", "crumb:Кубок → /fest/12", "crumb crumb-current:Брейн"]);
});

test("the title is «game · fest», or the section below the game", () => {
  mount(false, {festTitle: "Кубок", gameTitle: "ЭК"});
  assert.equal(document.title, "ЭК · Кубок");
  const {trail} = mount(false, {festTitle: "Кубок", gameTitle: "ЭК", gameHref: "/host/fest/12/game/3/", currentTitle: "Площадки"});
  assert.equal(document.title, "Площадки · Кубок");
  assert.deepEqual(trail.slice(-2), ["crumb:ЭК → /host/fest/12/game/3/", "crumb crumb-current:Площадки"]);
  mount(true, {festTitle: "", gameTitle: "ОД"});
  assert.equal(document.title, "ОД");
});

test("the shell reads the route and the init flags once", () => {
  const {shell} = mount(true, {festTitle: "", gameTitle: ""});
  assert.equal(shell.viewer, true);
  assert.equal(shell.canEdit, true);
  assert.equal(shell.scopeGameID, "30", "the numeric id from init, not the URL's slug");
  assert.equal(body.classes.has("viewer-readonly"), true);
});
