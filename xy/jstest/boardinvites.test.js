// The invite row's wording: what a link has done, how long it has left, and
// where it points.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyBoardInvites } from "../web/assets/static/dist/boardinvites.js";

const { inviteStateLabel, inviteUsage, inviteTimeLeft, inviteUrl, personName } = xyBoardInvites;

test("a link's state reads in Russian, and an unknown one passes through", () => {
  assert.equal(inviteStateLabel("active"), "\u0430\u043a\u0442\u0438\u0432\u043d\u0430");
  assert.equal(inviteStateLabel("revoked"), "\u043e\u0442\u043e\u0437\u0432\u0430\u043d\u0430");
  assert.equal(inviteStateLabel("expired"), "\u043f\u0440\u043e\u0441\u0440\u043e\u0447\u0435\u043d\u0430");
  assert.equal(inviteStateLabel("exhausted"), "\u0438\u0441\u0447\u0435\u0440\u043f\u0430\u043d\u0430");
  assert.equal(inviteStateLabel("who-knows"), "who-knows");
});

test("an uncapped link counts up; a capped one also counts down", () => {
  assert.equal(inviteUsage({ used: 3, left: null }), "\u0438\u0441\u043f\u043e\u043b\u044c\u0437\u043e\u0432\u0430\u043d\u0438\u0439: 3");
  assert.equal(inviteUsage({ used: 1, left: 4 }), "\u0438\u0441\u043f\u043e\u043b\u044c\u0437\u043e\u0432\u0430\u043d\u0438\u0439: 1, \u043e\u0441\u0442\u0430\u043b\u043e\u0441\u044c: 4");
  assert.equal(inviteUsage({ used: 5, left: 0 }), "\u0438\u0441\u043f\u043e\u043b\u044c\u0437\u043e\u0432\u0430\u043d\u0438\u0439: 5, \u043e\u0441\u0442\u0430\u043b\u043e\u0441\u044c: 0");
});

test("time left is coarse, and no expiry is not the same as a lapsed one", () => {
  const now = Date.parse("2026-08-29T12:00:00Z");
  const at = (h) => new Date(now + h * 3600000).toISOString();
  assert.equal(inviteTimeLeft(undefined, now), "", "a link without a \u0441\u0440\u043e\u043a says nothing");
  assert.equal(inviteTimeLeft(at(-1), now), "\u0441\u0440\u043e\u043a \u0438\u0441\u0442\u0451\u043a");
  assert.equal(inviteTimeLeft(at(0.5), now), "\u043e\u0441\u0442\u0430\u043b\u043e\u0441\u044c \u043c\u0435\u043d\u044c\u0448\u0435 \u0447\u0430\u0441\u0430");
  assert.equal(inviteTimeLeft(at(5), now), "\u043e\u0441\u0442\u0430\u043b\u043e\u0441\u044c 5 \u0447");
  assert.equal(inviteTimeLeft(at(72), now), "\u043e\u0441\u0442\u0430\u043b\u043e\u0441\u044c 3 \u0434\u043d");
  assert.equal(inviteTimeLeft("\u043d\u0435 \u0434\u0430\u0442\u0430", now), "");
});

test("the link is the join page on this origin", () => {
  assert.equal(inviteUrl("https://xy.example", "ABC123"), "https://xy.example/join/ABC123");
});

test("a person without a username still has a name", () => {
  assert.equal(personName({ user_id: 7, username: "\u0430\u043d\u044f" }), "\u0430\u043d\u044f");
  assert.equal(personName({ user_id: 7, username: "" }), "#7");
});
