import { test } from "node:test";
import assert from "node:assert/strict";
import { xyVersions } from "../web/assets/static/dist/versions.js";
import { xyChgk } from "../web/assets/static/dist/chgk.js";

// ---- question versions (issue #47) ----
// A Version is a WHOLE 4s body — question, ответ, зачёт, раздатка and all —
// stored in the card's own description, each body introduced by a standalone
// (hidden-comment xy-version: имя) line. The export merges them back into one
// question block, so a versioned card is still one numbered question.
const {
  splitVersions, versionBody, versionName, setVersionBody, setVersionName,
  addVersion, removeVersion, promoteVersion, convertLegacyVersions, composeVersions,
} = xyVersions;
const { splitFields } = xyChgk;

const TWO = [
  "(hidden-comment xy-version:)",
  "? Первая?",
  "! Ответ раз",
  "(hidden-comment xy-version: полегче)",
  "? Вторая?",
  "! Ответ два",
].join("\n");

test("a card with no separator line is one version, and that version is the card", () => {
  assert.deepEqual(splitVersions("? Один вопрос?\n! Ответ"), ["? Один вопрос?\n! Ответ"]);
  assert.equal(versionName("? Один вопрос?", 0), null);
});

test("a version is a whole body, so every field belongs to the version it sits in", () => {
  assert.deepEqual(splitVersions(TWO), ["? Первая?\n! Ответ раз", "? Вторая?\n! Ответ два"]);
  assert.equal(versionBody(TWO, 1), "? Вторая?\n! Ответ два");
  assert.equal(versionName(TWO, 0), null);
  assert.equal(versionName(TWO, 1), "полегче");
});

test("editing one version leaves its siblings alone — answer included", () => {
  const w = setVersionBody(TWO, 1, "? Вторая?\n! Другой ответ");
  assert.equal(versionBody(w, 0), "? Первая?\n! Ответ раз");
  assert.equal(versionBody(w, 1), "? Вторая?\n! Другой ответ");
  assert.equal(versionName(w, 1), "полегче");
});

test("setVersionBody ignores an index that is not there", () => {
  assert.equal(setVersionBody("? Одна?", 3, "нет"), "? Одна?");
});

test("a body cannot smuggle in a version of its own", () => {
  // Pasted or typed under the old scheme: the separator inside would otherwise
  // split into another version on the next read, and the card would grow one
  // every time it was saved.
  const pasted = "(hidden-comment xy-version: полегче)\n? Чужая?";
  const w = setVersionBody(TWO, 0, pasted);
  assert.equal(splitVersions(w).length, 2);
  assert.equal(versionBody(w, 0), "? Чужая?");
  assert.equal(versionName(w, 0), null);
});

test("adding a version clones the whole body, unnamed, and selects the copy", () => {
  const r = addVersion("? Первая?\n! Ответ", 0);
  assert.equal(r.index, 1);
  assert.deepEqual(splitVersions(r.desc), ["? Первая?\n! Ответ", "? Первая?\n! Ответ"]);
  assert.equal(versionName(r.desc, 1), null);
  // independent from the next edit on
  assert.equal(versionBody(setVersionBody(r.desc, 1, "? Другая?"), 0), "? Первая?\n! Ответ");
});

test("deleting a version drops it and steps back", () => {
  const r = removeVersion(TWO, 1);
  assert.equal(r.index, 0);
  assert.deepEqual(splitVersions(r.desc), ["? Первая?\n! Ответ раз"]);
});

test("the last version cannot be deleted", () => {
  assert.deepEqual(removeVersion("? Одна?", 0), { desc: "? Одна?", index: 0 });
});

test("promoting a version moves it to the front with its name", () => {
  const r = promoteVersion(TWO, 1);
  assert.equal(r.index, 0);
  assert.equal(versionBody(r.desc, 0), "? Вторая?\n! Ответ два");
  assert.equal(versionName(r.desc, 0), "полегче");
  assert.deepEqual(promoteVersion(TWO, 0), { desc: TWO, index: 0 });
});

test("naming and unnaming a version rewrites only its own separator line", () => {
  const named = setVersionName(TWO, 0, "посложнее");
  assert.equal(versionName(named, 0), "посложнее");
  assert.equal(versionBody(named, 0), "? Первая?\n! Ответ раз");
  assert.equal(versionName(setVersionName(named, 0, ""), 0), null);
});

test("a name cannot break out of its own directive", () => {
  const w = setVersionName("? Первая?", 0, "смайл :) и\nперенос");
  assert.equal(versionName(w, 0), "смайл : и перенос");
  assert.equal(versionBody(w, 0), "? Первая?");
});

// ---- what the rest of the app sees ----
// Only the card editor knows about versions. Everything else reads the card's
// description as it always did and gets version 1, because the separator line
// is xy's own metadata and parseBlocks drops it.

test("the board previews version 1 and never the separator", () => {
  assert.equal(xyChgk.previewText("question", TWO, null), "Первая?");
  assert.equal(xyChgk.questionText(TWO), "Первая?");
  assert.equal(splitFields(TWO).question, "Первая?");
  assert.equal(splitFields(TWO).answer, "Ответ раз");
});

test("a note is still a note — only xy-version lines separate", () => {
  const desc = "? Вопрос?\n(hidden-comment спросить Аню)\n! Ответ";
  assert.equal(splitVersions(desc).length, 1);
});

// ---- the export merges the versions back into one question ----

test("a single version composes to itself", () => {
  assert.equal(composeVersions("? Вопрос?\n! Ответ"), "? Вопрос?\n! Ответ");
});

test("the question carries every version, page-broken and numbered", () => {
  assert.equal(composeVersions(TWO), [
    "? Версия 1: Первая?",
    "(PAGEBREAK)",
    "Версия 2: Вторая?",
    "! версия 1: Ответ раз",
    "версия 2: Ответ два",
  ].join("\n"));
});

test("a field every version agrees on prints once", () => {
  const desc = [
    "(hidden-comment xy-version:)", "? Первая?", "! Один ответ", "/ Общий комментарий",
    "(hidden-comment xy-version:)", "? Вторая?", "! Один ответ", "/ Общий комментарий",
  ].join("\n");
  const out = composeVersions(desc);
  assert.ok(out.includes("! Один ответ"));
  assert.ok(out.includes("/ Общий комментарий"));
});

test("a field one version simply lacks counts as differing", () => {
  const desc = [
    "(hidden-comment xy-version:)", "? Первая?", "/ Есть комментарий",
    "(hidden-comment xy-version:)", "? Вторая?",
  ].join("\n");
  assert.ok(composeVersions(desc).includes("/ версия 1: Есть комментарий"));
});

test("each version takes its own раздатка into the export", () => {
  const desc = [
    "(hidden-comment xy-version:)", "? [Раздаточный материал: схема] Первая?",
    "(hidden-comment xy-version:)", "? [Раздаточный материал: другая схема] Вторая?",
  ].join("\n");
  const out = composeVersions(desc);
  assert.ok(out.includes("? Версия 1: [Раздаточный материал: схема] Первая?"));
  assert.ok(out.includes("Версия 2: [Раздаточный материал: другая схема] Вторая?"));
});

test("authors may differ per version, and are labelled like any other field", () => {
  const desc = [
    "(hidden-comment xy-version:)", "? Первая?", "@ Иванов, Петров",
    "(hidden-comment xy-version:)", "? Вторая?", "@ Иванов",
  ].join("\n");
  assert.ok(composeVersions(desc).includes("@ версия 1: Иванов, Петров\nверсия 2: Иванов"));
});

test("a version's name reaches no export — the label is always the number", () => {
  const out = composeVersions(TWO);
  assert.ok(!out.includes("полегче"));
  assert.ok(!out.includes("xy-version"));
});

// ---- the cards written under the old (PAGEBREAK) scheme ----

test("a page-broken question converts to whole bodies that clone the shared fields", () => {
  const old = "? Первая?\n(PAGEBREAK)\nВторая?\n! Общий ответ";
  const desc = convertLegacyVersions(old);
  assert.deepEqual(splitVersions(desc), [
    "? Первая?\n! Общий ответ",
    "? Вторая?\n! Общий ответ",
  ]);
});

test("conversion carries the old inline name up to the separator", () => {
  const old = "? Первая?\n(PAGEBREAK)\n(hidden-comment xy-version: полегче)\nВторая?";
  const desc = convertLegacyVersions(old);
  assert.equal(versionName(desc, 1), "полегче");
  assert.equal(versionBody(desc, 1), "? Вторая?");
});

test("the shared раздатка reaches every converted version", () => {
  const old = "? [Раздаточный материал: схема] Первая?\n(PAGEBREAK)\nВторая?\n! Общий ответ";
  const bodies = splitVersions(convertLegacyVersions(old));
  assert.ok(bodies[0].includes("[Раздаточный материал: схема]"));
  assert.ok(bodies[1].includes("[Раздаточный материал: схема]"));
});

test("a lone version's name never reaches the export", () => {
  const named = setVersionName("? Одна?\n! Ответ", 0, "полегче");
  assert.equal(composeVersions(named), "? Одна?\n! Ответ");
});

test("nothing to convert leaves the card untouched", () => {
  assert.equal(convertLegacyVersions("? Вопрос?\n! Ответ"), null);
  assert.equal(convertLegacyVersions(TWO), null);
});
