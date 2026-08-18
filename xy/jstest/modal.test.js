import { test } from "node:test";
import assert from "node:assert/strict";
import { createModal } from "../web/assets/static/dist/modal.js";
import { page, fakeStack } from "./dom.js";

function setup(ids = ["xOverlay", "xClose", "xMessage"]) {
  const p = page(ids);
  p.node("xOverlay").hidden = true; // a modal block is compiled hidden
  const stack = fakeStack();
  const m = createModal("x", { byId: p.byId, stack });
  return { p, stack, m, overlay: p.node("xOverlay") };
}

test("open shows the overlay and registers it on the stack; close pops and hides", async () => {
  const { stack, m, overlay } = setup();
  m.open();
  assert.equal(overlay.hidden, false);
  assert.equal(stack.isTop(overlay), true);
  m.close();
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(overlay.hidden, true);
  assert.equal(stack.depth(), 0);
});

test("the ✕ button and the backdrop dismiss through the stack; a click inside does not", async () => {
  const { p, stack, m, overlay } = setup();
  m.open();
  overlay.fire("pointerdown", { target: p.node("xClose") });
  assert.equal(stack.depth(), 1, "a click inside the dialog is not a dismissal");
  overlay.fire("pointerdown", { target: overlay });
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(stack.depth(), 0);
  m.open();
  p.node("xClose").fire("click");
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(overlay.hidden, true);
});

test("onClose runs on dismissal, and the message node is cleared on open", async () => {
  const { p, m } = setup();
  const log = [];
  m.message("старое");
  m.open({ onClose: () => log.push("closed") });
  assert.equal(p.node("xMessage").textContent, "");
  m.message("ошибка");
  assert.equal(p.node("xMessage").textContent, "ошибка");
  m.close();
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(log, ["closed"]);
});

test("open while open does not push twice; close when not top is a no-op", async () => {
  const { stack, m, overlay } = setup();
  m.open();
  m.open();
  assert.equal(stack.depth(), 1);
  stack.open({ el: {}, close() {} });
  m.close();
  assert.equal(stack.depth(), 2, "another overlay is on top; this modal stays");
  assert.equal(overlay.hidden, false);
});

test("a modal without a close button or message node still works; Cancel is honoured", async () => {
  const { p, stack, m } = setup(["xOverlay", "xCancel"]);
  m.open();
  p.node("xCancel").fire("click");
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(stack.depth(), 0);
  assert.throws(() => m.message("x"), /xMessage/);
});

test("confirm gates the dismissal", () => {
  const { stack, m } = setup();
  const confirm = async () => false;
  m.open({ confirm });
  assert.equal(stack.frames[0].confirm, confirm);
});
