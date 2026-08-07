// The browser's typography pass, checked against the SAME fixtures the Go
// implementation is checked against (internal/chgk/typoedit/testdata). The Go
// package is the oracle — the port was made from it — but the browser is where
// the pass actually runs, because question text must never be posted to a server
// that is not allowed to see it. If the two ever disagree, one of these two
// suites goes red.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyTypo } from "../web/assets/static/dist/typo.js";
import cases from "../internal/chgk/typoedit/testdata/pass_cases.json" with { type: "json" };

for (const c of cases.pass) {
  test(`pass: ${c.name}`, () => {
    assert.equal(xyTypo.pass(c.in), c.want);
  });
}

test("the pass is idempotent — a button is something you press twice", () => {
  for (const src of cases.idempotent) {
    const once = xyTypo.pass(src);
    assert.equal(xyTypo.pass(once), once);
  }
});

test("empty and marker-only lines survive untouched", () => {
  assert.equal(xyTypo.pass(""), "");
  assert.equal(xyTypo.pass("?\n!\n"), "?\n!\n");
  assert.equal(xyTypo.pass(null), "");
});

// The parts, so a failure says WHICH rule broke rather than just "the pass".
test("quotes nest the Russian way, and unbalanced ones are left alone", () => {
  assert.equal(xyTypo.getQuotesRight('Он сказал "да".'), "Он сказал «да».");
  assert.equal(xyTypo.getQuotesRight('В "спектакле "Гамлет"" играли'), "В «спектакле „Гамлет“» играли");
  assert.equal(xyTypo.getQuotesRight('Открыл "и не закрыл'), 'Открыл "и не закрыл');
});

test("a hyphen run between spaces is an em dash, a hyphenated word is not", () => {
  assert.equal(xyTypo.getDashesRight("Он - кот"), "Он — кот");
  assert.equal(xyTypo.getDashesRight("Он -- кот"), "Он — кот");
  assert.equal(xyTypo.getDashesRight("что-то"), "что-то");
  assert.equal(xyTypo.getDashesRight("Он – кот"), "Он — кот");
});

test("percent-escapes decode only when they are UTF-8", () => {
  assert.equal(xyTypo.percentDecode("/wiki/%D0%91%D0%B5%D0%B3%D0%B5%D0%BC%D0%BE%D1%82"), "/wiki/Бегемот");
  assert.equal(xyTypo.percentDecode("100%FF"), "100%FF");
  assert.equal(xyTypo.percentDecode("нет escape"), "нет escape");
});

// Stress detection is a heuristic on capitalisation, so what it leaves ALONE is
// half the specification: an all-caps ИХ is chgk's own convention for «them».
for (const c of cases.passAccents) {
  test(`accents: ${c.name}`, () => {
    assert.equal(xyTypo.passAccents(c.in), c.want);
  });
}

// ---- the board's review list ----
test("accentPicks reports what the pass would accent, without doing it", () => {
  const cards = ["? Слово брАзер.", "? Холдинг ГазпромИнвест и снова брАзер."];
  const picks = xyTypo.accentPicks(cards);
  assert.deepEqual(picks.map((p) => p.from).sort(), ["ГазпромИнвест", "брАзер"]);
  assert.equal(picks.find((p) => p.from === "брАзер").to, "бра́зер");
  // reporting must not rewrite anything
  assert.equal(cards[0], "? Слово брАзер.");
});

test("an unticked word is left alone everywhere, the ticked ones still land", () => {
  const src = "? Слово брАзер в холдинге ГазпромИнвест.";
  const out = xyTypo.passAccents(src, { allow: new Set(["брАзер"]) });
  assert.ok(out.includes("бра́зер"));
  assert.ok(out.includes("ГазпромИнвест"));
});

test("the rest of the pass runs whatever the review decides", () => {
  const src = '? Он сказал "да" - и ушёл, брАзер.';
  const out = xyTypo.passAccents(src, { allow: new Set() });
  assert.ok(out.includes("«да»"));
  assert.ok(out.includes("—"));
  assert.ok(out.includes("брАзер")); // untouched: nothing was ticked
});
