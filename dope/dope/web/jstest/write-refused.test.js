import {test} from "node:test";
import assert from "node:assert/strict";

// A DOM just big enough for the banner: elements that hold children, take
// listeners and can be removed from the body they were prepended to.
function fakeNode(tag) {
  const node = {
    tag, textContent: "", dataset: {}, style: {}, attrs: {}, kids: [], listeners: {}, parent: null,
    setAttribute(k, v) { node.attrs[k] = v; },
    append(...kids) { for (const kid of kids) { kid.parent = node; node.kids.push(kid); } },
    addEventListener(type, fn) { node.listeners[type] = fn; },
    remove() { if (node.parent) node.parent.kids = node.parent.kids.filter((kid) => kid !== node); node.parent = null; },
  };
  return node;
}
const body = fakeNode("body");
body.prepend = (kid) => { kid.parent = body; body.kids.unshift(kid); };
globalThis.document = {
  body,
  createElement: (tag) => fakeNode(tag),
  querySelector: (selector) => selector === "[data-write-refused]" ? body.kids.find((kid) => "writeRefused" in kid.dataset) ?? null : null,
};

const {refusedPlaces, showWriteRefused} = await import("./dist/widgets.js");

test("a refused edit puts the server's reason in a banner the host can close", () => {
  const bar = showWriteRefused("бой уже завершён\n");
  assert.equal(bar.attrs.role, "alert");
  assert.match(bar.kids[0].textContent, /Правка не сохранилась: бой уже завершён\. /);
  assert.equal(body.kids[0], bar);

  const next = showWriteRefused("");
  assert.equal(body.kids.length, 1, "a newer refusal replaces the banner");
  assert.match(next.kids[0].textContent, /Правка не сохранилась: Ошибка\./);

  next.kids[1].listeners.click();
  assert.equal(body.kids.length, 0);
});

test("the banner names where the refused edits were, the first few and a count of the rest", () => {
  const bar = showWriteRefused("Ошибка", ["вопрос 3, команда 7 «Сова»", "вопрос 4", "вопрос 5", "вопрос 6", "вопрос 7"]);
  assert.match(bar.kids[0].textContent, /^Правка не сохранилась \(вопрос 3, команда 7 «Сова»; вопрос 4; вопрос 5; и ещё 2\): Ошибка\. /);
  bar.kids[1].listeners.click();
});

test("a batch's ops become one place per distinct label, and an op the page cannot name none", () => {
  const ops = [
    {path: ["entries", 2, 0], value: 7},
    {path: ["entries", 2, 0], value: 8},
    {path: ["entries", 3, 1], value: 0},
    {path: ["players"], value: {}},
  ];
  const describe = (path, value) => path[0] !== "entries" ? null : {label: value ? `вопрос ${path[1] + 1}, команда ${value}` : `вопрос ${path[1] + 1}`};
  assert.deepEqual(refusedPlaces(ops, describe).map((place) => place.label), ["вопрос 3, команда 7", "вопрос 3, команда 8", "вопрос 4"]);
  assert.deepEqual(refusedPlaces(ops), []);
});
