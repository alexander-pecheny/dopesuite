import { test } from "node:test";
import assert from "node:assert/strict";
import { xyBoardMembers } from "../web/assets/static/boardmembers.js";

const { memberName, roleLabel } = xyBoardMembers;

test("memberName prefers the username, falling back to #id", () => {
  assert.equal(memberName({ user_id: 7, username: "аня" }), "аня");
  assert.equal(memberName({ user_id: 7, username: "" }), "#7");
  assert.equal(memberName({ user_id: 7 }), "#7");
});

test("roleLabel names the two roles in Russian", () => {
  assert.equal(roleLabel("owner"), "владелец");
  assert.equal(roleLabel("editor"), "редактор");
  assert.equal(roleLabel("anything-else"), "редактор");
});
