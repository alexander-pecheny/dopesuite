import { test } from "node:test";
import assert from "node:assert/strict";
import { xyFind } from "../web/assets/static/dist/find.js";

const { searchSpans, replaceSpans } = xyFind;

const at = (text, spans) => spans.map((s) => text.slice(s.start, s.end));

test("a search span points at the original text", () => {
  const text = "? В каком году основана компания Acme?";
  const spans = searchSpans(text, "компания");
  assert.equal(spans.length, 1);
  assert.deepEqual(at(text, spans), ["компания"]);
});

// Everything the typography pass inserts must stay findable by an editor typing
// the plain form: a stress accent, a non-breaking space, «ёлочки», an em dash.
test("search sees through what the typography pass wrote", () => {
  const acute = "Слово бра́зер сказано";
  assert.deepEqual(at(acute, searchSpans(acute, "бразер")), ["бра́зер"]);

  const nbsp = "основана в 1899 году";
  assert.deepEqual(at(nbsp, searchSpans(nbsp, "в 1899")), ["в 1899"]);

  const quotes = "компания «Acme» из";
  assert.deepEqual(at(quotes, searchSpans(quotes, '"Acme"')), ["«Acme»"]);

  const dash = "Москва — столица";
  assert.deepEqual(at(dash, searchSpans(dash, "Москва - столица")), ["Москва — столица"]);

  const yo = "ёжик съел ёлку";
  assert.deepEqual(at(yo, searchSpans(yo, "ежик")), ["ёжик"]);

  const caps = "ОТВЕТ: Пушкин";
  assert.deepEqual(at(caps, searchSpans(caps, "ответ")), ["ОТВЕТ"]);
});

test("an empty query matches nothing, a missing one finds nothing", () => {
  assert.deepEqual(searchSpans("что угодно", ""), []);
  assert.deepEqual(searchSpans("что угодно", "Пушкин"), []);
});

test("every occurrence is reported, in order", () => {
  const text = "год, ещё год, и снова год";
  assert.deepEqual(searchSpans(text, "год").map((s) => s.start), [0, 9, 22]);
});

// ---- replace: literal, except for what the pass glued ----

test("a replacement matches literally, accents and case included", () => {
  const acute = "Слово бра́зер сказано";
  assert.deepEqual(replaceSpans(acute, "бразер", true), []);
  assert.deepEqual(at(acute, replaceSpans(acute, "бра́зер", true)), ["бра́зер"]);

  const caps = "Москва и москва";
  assert.deepEqual(at(caps, replaceSpans(caps, "москва", true)), ["москва"]);
  assert.deepEqual(at(caps, replaceSpans(caps, "москва", false)), ["Москва", "москва"]);
});

test("a typed space matches the one the pass made non-breaking", () => {
  const nbsp = "основана в 1899 году";
  assert.deepEqual(at(nbsp, replaceSpans(nbsp, "в 1899", true)), ["в 1899"]);
  const hyphen = "он что‑то знал";
  assert.deepEqual(at(hyphen, replaceSpans(hyphen, "что-то", true)), ["что‑то"]);
});

// ---- replace: what a replacement may never eat ----

test("a marker prefix is unmatchable", () => {
  const src = "? Кто дал ответ\n! Ответ: Пушкин\n- Ответ первый";
  // The text after a marker is fair game; a match that would swallow the marker
  // itself — "! " or a list item's leading "- " — is not.
  assert.deepEqual(replaceSpans(src, "Ответ", true).map((s) => s.start), [18, 34]);
  assert.deepEqual(replaceSpans(src, "! Ответ", true), []);
  assert.deepEqual(replaceSpans(src, "- Ответ", true), []);
});

test("a version separator keeps its head, gives up its name", () => {
  const src = "(hidden-comment xy-version: полегче)\n? Полегче некуда";
  assert.deepEqual(at(src, replaceSpans(src, "полегче", true)), ["полегче"]);
  assert.equal(replaceSpans(src, "polegche", true).length, 0);
  // The directive itself — head and closing bracket — is untouchable.
  assert.deepEqual(replaceSpans(src, "hidden-comment", true), []);
  assert.deepEqual(replaceSpans(src, "xy-version", true), []);
  assert.deepEqual(replaceSpans(src, "полегче)", true), []);
});

test("an ordinary hidden comment is ordinary text", () => {
  const src = "? Вопрос (hidden-comment сомнительная формулировка)";
  assert.deepEqual(at(src, replaceSpans(src, "сомнительная", true)), ["сомнительная"]);
});

// ---- applying, and what a hit looks like on a tile ----

test("applying spans rewrites exactly what was ticked", () => {
  const src = "? Уотсон и Уотсон\n! Уотсон";
  const all = replaceSpans(src, "Уотсон", true);
  assert.equal(xyFind.applySpans(src, all, "Ватсон"), "? Ватсон и Ватсон\n! Ватсон");
  // Ticking one leaves its siblings alone, whatever their order.
  assert.equal(xyFind.applySpans(src, [all[1]], "Ватсон"), "? Уотсон и Ватсон\n! Уотсон");
  assert.equal(xyFind.applySpans(src, [], "Ватсон"), src);
});

test("an empty replacement deletes the span", () => {
  const src = "? Вопрос (уточнение) тут";
  assert.equal(xyFind.applySpans(src, replaceSpans(src, " (уточнение)", true), ""), "? Вопрос тут");
});

test("a snippet centres on the hit and marks where it fell", () => {
  const text = "а".repeat(100) + "Пушкин" + "б".repeat(100);
  const hit = searchSpans(text, "Пушкин")[0];
  const snip = xyFind.snippet(text, hit, 20);
  assert.equal(snip.text.slice(snip.start, snip.end), "Пушкин");
  assert.ok(snip.text.length < 70, snip.text);
  assert.ok(snip.text.startsWith("…") && snip.text.endsWith("…"));
  // A short text is shown whole, with no ellipsis and no offset drift.
  const short = xyFind.snippet("Пушкин жил", { start: 0, end: 6 }, 20);
  assert.deepEqual(short, { text: "Пушкин жил", start: 0, end: 6 });
});
