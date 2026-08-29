// The join page's decision table: every way a link can be dead says something
// different, and a live one offers the right verb.
import { test } from "node:test";
import assert from "node:assert/strict";

const { joinView, codeFromPath } = await import("../web/assets/static/dist/join.js");

const peek = (over) => ({ board_id: 7, board_name: "Синхрон", state: "active", requires_approval: false, ...over });

test("a live link offers to join, and names the board", () => {
  const v = joinView(peek());
  assert.equal(v.action, "join");
  assert.match(v.heading, /Синхрон/);
  assert.match(v.note, /[Пп]ароль/, "and says the passphrase does not travel with the link");
});

test("a link that needs approval asks for a request, not a join", () => {
  const v = joinView(peek({ requires_approval: true }));
  assert.equal(v.action, "join");
  assert.match(v.heading, /Заявка/);
});

test("an existing member is offered the board, not a join", () => {
  assert.equal(joinView(peek({ state: "member" })).action, "open");
});

test("every dead state offers nothing and says why", () => {
  const headings = new Set();
  for (const state of ["pending", "declined", "revoked", "expired", "exhausted", "spent"]) {
    const v = joinView(peek({ state }));
    assert.equal(v.action, "none", state);
    assert.ok(v.heading, state);
    headings.add(v.heading);
  }
  assert.equal(headings.size, 6, "each dead end reads differently");
});

test("an unknown state fails closed", () => {
  assert.equal(joinView(peek({ state: "sideways" })).action, "none");
});

test("the code is the last segment of the path", () => {
  assert.equal(codeFromPath("/join/ABC123"), "ABC123");
  assert.equal(codeFromPath("/join/ABC123/"), "ABC123");
});
