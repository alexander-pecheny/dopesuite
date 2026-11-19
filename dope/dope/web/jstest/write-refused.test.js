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

const {showWriteRefused} = await import("./dist/widgets.js");

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
