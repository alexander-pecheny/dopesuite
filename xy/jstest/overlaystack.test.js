import { test } from "node:test";
import assert from "node:assert/strict";
import { createOverlayStack } from "../web/assets/static/dist/overlaystack.js";

// A fake history whose back() drives popstate synchronously enough to await,
// plus the two listener seams the stack registers on.
function harness() {
  const entries = [{ url: "/board/1" }];
  let pop = () => {};
  let key = () => {};
  const log = [];
  const stack = createOverlayStack({
    history: {
      pushState(state, _t, url) { entries.push({ url: url || entries[entries.length - 1].url }); },
      replaceState(state, _t, url) { entries[entries.length - 1] = { url: url || entries[entries.length - 1].url }; },
      back() {
        if (entries.length > 1) entries.pop();
        queueMicrotask(() => pop());
      },
    },
    location: { get href() { return entries[entries.length - 1].url; } },
    addPopListener(fn) { pop = fn; },
    addKeyListener(fn) { key = fn; },
  });
  const overlay = (name) => ({ el: { name }, close: () => log.push("close:" + name) });
  const settle = () => new Promise((r) => setTimeout(r, 0));
  return { stack, entries, log, overlay, settle, esc: () => key({ key: "Escape", stopPropagation() {} }) };
}

test("opening an overlay adds a history entry, back closes it", async () => {
  const h = harness();
  h.stack.open(h.overlay("card"));
  assert.equal(h.entries.length, 2);
  assert.equal(h.stack.depth(), 1);

  h.stack.pop();
  await h.settle();
  assert.deepEqual(h.log, ["close:card"]);
  assert.equal(h.stack.depth(), 0);
  assert.equal(h.entries.length, 1); // back at the board
});

test("nested overlays close one at a time, innermost first", async () => {
  const h = harness();
  h.stack.open(h.overlay("card"));
  h.stack.open(h.overlay("excerpts"));
  assert.equal(h.stack.depth(), 2);

  h.stack.pop();
  await h.settle();
  assert.deepEqual(h.log, ["close:excerpts"]);
  assert.equal(h.stack.depth(), 1);

  h.stack.pop();
  await h.settle();
  assert.deepEqual(h.log, ["close:excerpts", "close:card"]);
  assert.equal(h.entries.length, 1);
});

test("Escape is the same gesture as back", async () => {
  const h = harness();
  h.stack.open(h.overlay("card"));
  h.esc();
  await h.settle();
  assert.deepEqual(h.log, ["close:card"]);
});

test("Escape with nothing open leaves history alone", async () => {
  const h = harness();
  h.esc();
  await h.settle();
  assert.equal(h.entries.length, 1);
  assert.deepEqual(h.log, []);
});

test("a declined confirm keeps the overlay open and its history entry intact", async () => {
  const h = harness();
  h.stack.open({ el: { name: "card" }, close: () => h.log.push("close:card"), confirm: async () => false });

  h.stack.pop();
  await h.settle();
  assert.deepEqual(h.log, []);
  assert.equal(h.stack.depth(), 1);
  assert.equal(h.entries.length, 2); // still one deep, so back works again

  // …and a second attempt still asks, rather than remembering the refusal.
  h.stack.pop();
  await h.settle();
  assert.equal(h.stack.depth(), 1);
});

test("a declined confirm restores the overlay's own address", async () => {
  const h = harness();
  h.stack.open({ el: { name: "card" }, close() {}, confirm: async () => false }, "/board/1?card=7");
  assert.equal(h.entries[1].url, "/board/1?card=7");

  // The browser reverts the address before popstate fires, so the stack has to
  // put the card's own URL back — not whatever is current when it asks.
  h.stack.pop();
  await h.settle();
  assert.equal(h.entries.length, 2);
  assert.equal(h.entries[1].url, "/board/1?card=7");
});

test("an accepted confirm closes the overlay and is asked only once", async () => {
  const h = harness();
  let asked = 0;
  h.stack.open({
    el: { name: "card" },
    close: () => h.log.push("close:card"),
    confirm: async () => { asked++; return true; },
  });

  h.stack.pop();
  await h.settle();
  await h.settle(); // the confirm path takes a second trip through back()
  assert.equal(asked, 1);
  assert.deepEqual(h.log, ["close:card"]);
  assert.equal(h.entries.length, 1);
});

test("replace swaps the top overlay without changing depth", async () => {
  const h = harness();
  const preview = h.overlay("preview");
  h.stack.open(preview, "/board/1");
  h.stack.replace(preview.el, h.overlay("card"), "/board/1?card=7");
  assert.equal(h.stack.depth(), 1);
  assert.equal(h.entries.length, 2);
  assert.equal(h.entries[1].url, "/board/1?card=7");

  h.stack.pop();
  await h.settle();
  assert.deepEqual(h.log, ["close:card"]); // the preview's close never runs
  assert.equal(h.entries.length, 1);
});

test("replace on an overlay that is not top opens instead of swapping", () => {
  const h = harness();
  h.stack.open(h.overlay("card"));
  h.stack.replace({ name: "preview" }, h.overlay("excerpts"));
  assert.equal(h.stack.depth(), 2);
});

test("isTop tracks the live overlay", () => {
  const h = harness();
  const card = { name: "card" };
  h.stack.open({ el: card, close() {} });
  assert.equal(h.stack.isTop(card), true);
  h.stack.open({ el: { name: "excerpts" }, close() {} });
  assert.equal(h.stack.isTop(card), false);
});
