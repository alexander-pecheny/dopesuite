// The browser's Card Kind rules against the same table internal/cardkind's
// TestKindTable spells out: every kind against every question cardkind.ts
// answers.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCardKind } from "../web/assets/static/dist/cardkind.js";

const K = xyCardKind;

// kind, numbered, exported, carriesHandouts, versioned, exportMarker,
// restartsThemes, setsBase, game of a scope holding it, pickable
const TABLE = [
  ["question", true, true, true, true, "", false, false, "chgk", true],
  ["theme", true, true, false, false, "", false, false, "si", true],
  ["meta", false, true, false, false, "#", false, true, "chgk", true],
  ["heading", false, true, false, false, "##", true, true, "chgk", true],
  ["other", false, true, false, false, "", false, false, "chgk", true],
  ["normal", false, true, false, false, "", false, false, "chgk", false],
  ["test", false, true, false, false, "", false, false, "chgk", false],
  ["handouts_preamble", false, false, false, false, "", false, false, "chgk", false],
  ["no-such-kind", false, false, false, false, "", false, false, "chgk", false],
];

for (const row of TABLE) {
  test(`cardkind: ${row[0]}`, () => {
    const k = row[0];
    assert.deepEqual([
      k, K.numbered(k), K.exported(k), K.carriesHandouts(k), K.versioned(k), K.exportMarker(k),
      K.restartsThemes(k), K.setsBase(k), K.gameOf([{ kind: k }]), K.pickable.includes(k),
    ], row);
  });
}

test("cardkind: the table covers every kind", () => {
  assert.equal(Object.keys(K.KIND).length, TABLE.length - 1);
});

test("cardkind: a Test Session plays what is numbered, a theme included", () => {
  for (const [k] of TABLE) assert.equal(K.testable(k), K.numbered(k), k);
  assert.ok(K.testable("theme"));
});

test("cardkind: counters", () => {
  assert.equal(K.counter("question"), "question");
  assert.equal(K.counter("theme"), "theme");
  assert.equal(K.counter("heading"), "");
  assert.equal(K.counter(undefined), "");
});

test("cardkind: a List Type picks the new card's kind", () => {
  assert.equal(K.forListType("si"), "theme");
  assert.equal(K.forListType("normal"), "question");
  assert.equal(K.forListType("test"), "question");
});

test("cardkind: one theme makes a scope SI", () => {
  assert.equal(K.gameOf([]), "chgk");
  assert.equal(K.gameOf([{ kind: "question" }, { kind: "theme" }]), "si");
});

test("cardkind: the kinds people pick or see have a label, the others none", () => {
  for (const k of K.pickable) assert.ok(K.label(k), k);
  assert.ok(K.label("handouts_preamble"));
  assert.equal(K.label("normal"), null);
  assert.equal(K.label("test"), null);
});
