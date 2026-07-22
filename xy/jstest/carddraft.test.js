import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCardDraft } from "../web/assets/static/carddraft.js";

const { create, contentDirty, aliasDirty, normalizeMeta, normalizeAlias } = xyCardDraft;

test("normalizeMeta: blank → null, non-blank kept verbatim (untrimmed)", () => {
  assert.equal(normalizeMeta(null), null);
  assert.equal(normalizeMeta(""), null);
  assert.equal(normalizeMeta("   "), null);
  assert.equal(normalizeMeta("  x "), "  x "); // meta is not trimmed
});

test("normalizeAlias: blank → null, non-blank trimmed", () => {
  assert.equal(normalizeAlias(null), null);
  assert.equal(normalizeAlias("   "), null);
  assert.equal(normalizeAlias("  hi "), "hi");
});

test("contentDirty new card: blank is clean, any content or alias is dirty", () => {
  assert.equal(contentDirty({ isNew: true, desc: "", alias: null }), false);
  assert.equal(contentDirty({ isNew: true, desc: "   ", alias: null }), false);
  assert.equal(contentDirty({ isNew: true, desc: "? q", alias: null }), true);
  assert.equal(contentDirty({ isNew: true, desc: "", alias: "лейбл" }), true);
});

test("contentDirty existing card: desc or meta change, alias excluded", () => {
  const base = { isNew: false, desc: "same", savedDesc: "same", meta: null, savedMeta: null };
  assert.equal(contentDirty(base), false);
  assert.equal(contentDirty({ ...base, desc: "changed" }), true);
  assert.equal(contentDirty({ ...base, meta: "m" }), true);
  // "" meta normalizes to null — not dirty against a null baseline
  assert.equal(contentDirty({ ...base, meta: "" }), false);
});

test("aliasDirty compares normalized-or-null values", () => {
  assert.equal(aliasDirty(null, null), false);
  assert.equal(aliasDirty("a", null), true);
  assert.equal(aliasDirty(null, "a"), true);
  assert.equal(aliasDirty("a", "a"), false);
});

test("create(): open a card → clean until edited, commit resets baseline", () => {
  const d = create();
  d.open("? q\n! a", null, "alias1");
  assert.equal(d.contentDirty(false), false);
  assert.equal(d.aliasDirty("alias1"), false);

  d.desc = "? q2\n! a";
  assert.equal(d.contentDirty(false), true);
  d.commitContent(d.desc, d.normalizedMeta());
  assert.equal(d.contentDirty(false), false);

  assert.equal(d.aliasDirty("alias2"), true);
  d.commitAlias("alias2");
  assert.equal(d.aliasDirty("alias2"), false);
  assert.equal(d.alias, "alias2");
});

test("create(): blank() resets the working draft for a new card", () => {
  const d = create();
  d.open("old", "meta", "al");
  d.blank();
  assert.equal(d.desc, "");
  assert.equal(d.meta, null);
  assert.equal(d.alias, null);
  assert.equal(d.contentDirty(true), false);
});
