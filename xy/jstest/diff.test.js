import { test } from "node:test";
import assert from "node:assert/strict";
import { xyDiff } from "../web/assets/static/diff.js";

const { diffTokens } = xyDiff;

// reconstruct the "before" text from a diff (eq + del ops).
const before = (ops) => ops.filter((o) => o.type !== "add").map((o) => o.text).join("");
// reconstruct the "after" text (eq + add ops).
const after = (ops) => ops.filter((o) => o.type !== "del").map((o) => o.text).join("");

test("identical text is all equal", () => {
  const ops = diffTokens("привет мир", "привет мир");
  assert.deepEqual(ops, [{ type: "eq", text: "привет мир" }]);
});

test("a single word insertion is isolated", () => {
  const ops = diffTokens("один два", "один новый два");
  assert.ok(ops.some((o) => o.type === "add" && o.text.includes("новый")));
  assert.ok(!ops.some((o) => o.type === "del"));
});

test("a deletion is isolated", () => {
  const ops = diffTokens("один лишний два", "один два");
  assert.ok(ops.some((o) => o.type === "del" && o.text.includes("лишний")));
  assert.ok(!ops.some((o) => o.type === "add"));
});

test("diff reconstructs both sides exactly", () => {
  const a = "? В каком году была основана компания Acme?\n! 1899";
  const b = "? В каком году была основана фирма Acme?\n! 1999\n^ wiki";
  const ops = diffTokens(a, b);
  assert.equal(before(ops), a);
  assert.equal(after(ops), b);
});

test("empty before yields a single add", () => {
  const ops = diffTokens("", "новый текст");
  assert.deepEqual(ops, [{ type: "add", text: "новый текст" }]);
});

test("empty after yields a single del", () => {
  const ops = diffTokens("старый текст", "");
  assert.deepEqual(ops, [{ type: "del", text: "старый текст" }]);
});
