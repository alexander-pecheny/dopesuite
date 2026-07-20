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

test("briefOps keeps context either side of a change and elides the rest", () => {
  const before = "один два три четыре пять шесть семь восемь девять десять";
  const after = "один два три четыре ПЯТЬ шесть семь восемь девять десять";
  const brief = xyDiff.briefOps(xyDiff.diffTokens(before, after), 2);
  const text = brief.map((o) => (o.type === "gap" ? "…" : o.text)).join("");
  // the far ends are dropped, the words around the change survive
  assert.ok(!text.includes("один"));
  assert.ok(!text.includes("десять"));
  assert.ok(text.includes("три четыре"));
  assert.ok(text.includes("шесть семь"));
  // elisions are marked at BOTH ends, including the leading one
  assert.equal(brief[0].type, "gap");
  assert.equal(brief[brief.length - 1].type, "gap");
  assert.deepEqual(brief.filter((o) => o.type === "del").map((o) => o.text), ["пять"]);
  assert.deepEqual(brief.filter((o) => o.type === "add").map((o) => o.text), ["ПЯТЬ"]);
});

test("briefOps leaves a short diff untouched", () => {
  const ops = xyDiff.diffTokens("а б в", "а Б в");
  assert.deepEqual(xyDiff.briefOps(ops, 4), ops); // nothing worth eliding
});

test("briefOps elides between two distant changes but not between close ones", () => {
  const before = "aa bb cc dd ee ff gg hh ii jj kk ll";
  const far = xyDiff.briefOps(xyDiff.diffTokens(before, "XX bb cc dd ee ff gg hh ii jj kk YY"), 2);
  assert.equal(far.filter((o) => o.type === "gap").length, 1); // one gap, in the middle
  const near = xyDiff.briefOps(xyDiff.diffTokens("aa bb cc dd", "XX bb cc YY"), 2);
  assert.equal(near.filter((o) => o.type === "gap").length, 0); // changes too close to elide
});
