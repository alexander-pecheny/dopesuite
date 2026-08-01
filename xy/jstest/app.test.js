import { test } from "node:test";
import assert from "node:assert/strict";
import { xyApp } from "../web/assets/static/dist/app.js";

const { deriveTitle } = xyApp;

test("short text is returned as-is", () => {
  assert.equal(deriveTitle("Короткий вопрос"), "Короткий вопрос");
});

test("empty text falls back to placeholder", () => {
  assert.equal(deriveTitle(""), "(пусто)");
  assert.equal(deriveTitle("   \n  "), "(пусто)");
});

test("flows across lines instead of stopping at the first", () => {
  // handout-first question: the first line is uninformative on its own
  const desc = "Раздаточный материал:\nфото\nКакой город изображён на снимке?";
  const out = deriveTitle(desc);
  assert.ok(out.includes("Какой город"), out);
  assert.ok(!out.includes("\n"), "no newlines in preview");
});

test("collapses runs of whitespace to single spaces", () => {
  assert.equal(deriveTitle("a\n\n\tb   c"), "a b c");
});

test("truncates long text at a word boundary with an ellipsis", () => {
  const long = "слово ".repeat(40).trim();
  const out = deriveTitle(long, 30);
  assert.ok(out.endsWith("…"), out);
  assert.ok(out.length <= 31, out);
  assert.ok(!/\sслов$/.test(out), "should not cut mid-word: " + out);
});

// ---- onCmdEnter ----
// A stub node rather than a DOM: the helper only ever adds one keydown listener,
// so handing it a recorder and calling that listener is the whole surface.
function stubNode() {
  const node = { handler: null, addEventListener: (_type, fn) => { node.handler = fn; } };
  return node;
}

function press(node, key, mods = {}) {
  let defaultPrevented = false;
  node.handler({ key, metaKey: false, ctrlKey: false, ...mods, preventDefault: () => { defaultPrevented = true; } });
  return defaultPrevented;
}

test("onCmdEnter fires on Ctrl-Enter and on Cmd-Enter", () => {
  const node = stubNode();
  let runs = 0;
  xyApp.onCmdEnter(node, () => { runs++; });
  assert.ok(press(node, "Enter", { ctrlKey: true }), "prevents the default newline");
  assert.equal(runs, 1);
  press(node, "Enter", { metaKey: true });
  assert.equal(runs, 2);
});

test("onCmdEnter ignores a bare Enter and other modified keys", () => {
  const node = stubNode();
  let runs = 0;
  xyApp.onCmdEnter(node, () => { runs++; });
  assert.equal(press(node, "Enter"), false, "a bare Enter must still insert a newline");
  press(node, "s", { ctrlKey: true });
  press(node, "Escape", { metaKey: true });
  assert.equal(runs, 0);
});
