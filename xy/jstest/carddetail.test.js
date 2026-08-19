// Tests for carddetail.js's pure exports — the test-card derived title, the
// 4s question stub and the tester summary line. The createCardDetail factory
// takes its nodes as a ui record and runs under the DOM shim in
// carddetail_ui.test.js.
import { test } from "node:test";
import assert from "node:assert/strict";
import { questionStub, nowStamp } from "../web/assets/static/dist/carddetail.js";

test("questionStub seeds the five markers and the default author", () => {
  assert.equal(questionStub("Автор А"), "? \n! \n/ \n^ \n@ Автор А");
  assert.equal(questionStub(""), "? \n! \n/ \n^ \n@ ");
});

test("nowStamp is a parseable ISO timestamp", () => {
  const t = Date.parse(nowStamp());
  assert.ok(Number.isFinite(t) && Math.abs(Date.now() - t) < 5000);
});
