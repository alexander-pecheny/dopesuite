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
    // Minimal stubs for syncs that walk the DOM (e.g. the player popover lookup);
    // tests that need real traversal assert on the node directly instead.
    closest: () => null,
  };
}

// fakeIndex mimics what createScoreTableIndex returns, without a DOM: it carries
// the real `specs` (pass T.scoreCellSpecs(...) so patchScoreTable runs the real
// per-cell sync logic) and lets a test register cells under a spec name with
// their data-* coordinates. register() returns the cell so the test can assert
// what the sync wrote; eachNode/get drive patchScoreTable and lookups.
export function fakeIndex(specs = []) {
  const byName = new Map(); // name -> [cell, ...]
  return {
    specs,
    register: (name, dataset = {}) => {
      const cell = fakeCell();
      for (const [k, v] of Object.entries(dataset)) cell.dataset[k] = String(v);
      if (!byName.has(name)) byName.set(name, []);
      byName.get(name).push(cell);
      return cell;
    },
    eachNode: (name, cb) => (byName.get(name) || []).forEach(cb),
    get: (name) => (byName.get(name) || [])[0] || null,
  };
}
