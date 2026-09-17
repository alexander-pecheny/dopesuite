import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeNode, installDOM } from "./dom.js";

const page = installDOM(["scope"]);
globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
// measure() reads on the next frame, as the browser lays out.
const measured = async (o) => { o.measure(); await new Promise((r) => setTimeout(r, 1)); };
globalThis.innerWidth = 1000;
globalThis.innerHeight = 800;
const { createNameOverflow } = await import("../web/assets/static/dist/nameoverflow.js");

const root = page.byId("scope");

// An item whose name reports `scroll` wide inside a `client`-wide box.
function item(text, scroll, client) {
  const name = fakeNode("span", { class: "nm", classes: new Set(["nm"]), scrollWidth: scroll, clientWidth: client });
  name.textContent = text;
  const node = fakeNode("div", { class: "it", classes: new Set(["it"]) });
  node.append(name);
  return node;
}

const overflow = createNameOverflow({ root, item: ".it", name: ".nm", truncatedClass: "cut" });

test("only the item whose name does not fit is flagged", async () => {
  const long = item("pecheny_firsova_synch_removed", 400, 240);
  const short = item("tour 1", 60, 240);
  root.replaceChildren(long, short);
  await measured(overflow);
  assert.ok(long.classList.contains("cut"));
  assert.ok(!short.classList.contains("cut"));

  // Re-measuring after the box grows clears the flag — the fade is layout, not state.
  long.querySelector(".nm").clientWidth = 400;
  await measured(overflow);
  assert.ok(!long.classList.contains("cut"));
});

test("hovering a flagged item floats its full name; an unflagged one shows nothing", async () => {
  const long = item("pecheny_firsova_synch_removed", 400, 240);
  const short = item("tour 1", 60, 240);
  root.replaceChildren(long, short);
  await measured(overflow);

  root.fire("pointerover", { target: long.querySelector(".nm") });
  const tip = document.body.kids.find((n) => n.className.includes("floating-name-popover"));
  assert.ok(tip, "the popover is one shared node on <body>");
  assert.equal(tip.textContent, "pecheny_firsova_synch_removed");
  assert.ok(tip.classes.has("visible"));

  root.fire("pointerout", { target: long, relatedTarget: short });
  assert.ok(!tip.classes.has("visible"));

  root.fire("pointerover", { target: short });
  assert.ok(!tip.classes.has("visible"), "a name that fits needs no popover");
});

test("a rebuild that strands the anchor hides the popover", async () => {
  const long = item("pecheny_firsova_synch_removed", 400, 240);
  root.replaceChildren(long);
  await measured(overflow);
  root.fire("pointerover", { target: long });
  const tip = document.body.kids.find((n) => n.className.includes("floating-name-popover"));
  assert.ok(tip.classes.has("visible"));

  root.replaceChildren(item("tour 1", 60, 240));
  await measured(overflow);
  assert.ok(!tip.classes.has("visible"));
});
