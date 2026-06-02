// Test support for the browser IIFE modules in dope/static. Each runs as
// `(function () { ... window.DopeX = {...} })()` in the page; here we eval one
// in a fake window/document and return the populated `window`, so its pure
// logic (cell patching, shape checks, cache invalidation, delta application)
// can be unit-tested without a real DOM. Anything that touches real layout is
// covered by the app itself, not here.
import {join} from "node:path";

export function loadStaticModule(filename) {
  const src = Deno.readTextFileSync(join(import.meta.dirname, "..", "static", filename));
  const window = {};
  const document = {activeElement: null, createElement: () => fakeCell()};
  new Function("window", "document", src)(window, document);
  return window;
}

// fakeCell is a minimal stand-in for a DOM node: textContent + classList + value.
export function fakeCell() {
  const classes = new Set();
  return {
    textContent: "",
    dataset: {},
    value: "",
    classList: {
      add: (...xs) => xs.forEach((x) => classes.add(x)),
      remove: (...xs) => xs.forEach((x) => classes.delete(x)),
      contains: (x) => classes.has(x),
    },
  };
}

// fakeIndex mimics createScoreTableIndex: get(name, values) -> node. register()
// adds a node under a key so a test can assert what patchScoreTable wrote.
export function fakeIndex() {
  const store = new Map();
  const key = (name, values) => name + ":" + JSON.stringify(values || {});
  return {
    get: (name, values) => store.get(key(name, values)) || null,
    register: (name, values) => {
      const cell = fakeCell();
      store.set(key(name, values), cell);
      return cell;
    },
  };
}
