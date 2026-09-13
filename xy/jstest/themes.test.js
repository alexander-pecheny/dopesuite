import { test } from "node:test";
import assert from "node:assert/strict";
import { xyThemes } from "../web/assets/static/dist/themes.js";

// A Card typed «Тема СИ» holds one theme: a `#T` head and a ladder of `№` slots
// whose values are the point values. The ladder is the order and the points at
// once, which is why nothing here ever renumbers.
const { splitTheme, composeTheme, nextNumber, swapSlots, isFilled, authorsOf, progress } = xyThemes;

const THEME = [
  "#T Острова Тихого океана",
  "@ Александр Рождествин",
  "/ вся тема про острова.",
  "",
  "№ 10",
  "? Первый вопрос.",
  "! Гавайи.",
  "",
  "№ 20",
  "? Второй вопрос.",
  "! Сахалин.",
  "@ Наиль Фарукшин",
  "",
  "№ 30",
  "?",
  "!",
  "",
].join("\n");

test("splitTheme reads the head and the ladder", () => {
  const t = splitTheme(THEME);
  assert.equal(t.name, "Острова Тихого океана");
  assert.equal(t.author, "Александр Рождествин");
  assert.equal(t.comment, "вся тема про острова.");
  assert.deepEqual(t.slots.map((s) => s.number), ["10", "20", "30"]);
  assert.equal(t.slots[0].fields.question, "Первый вопрос.");
  assert.equal(t.slots[1].fields.answer, "Сахалин.");
});

test("composeTheme round-trips", () => {
  const once = composeTheme(splitTheme(THEME));
  assert.equal(composeTheme(splitTheme(once)), once);
  assert.match(once, /^#T Острова Тихого океана\n/);
  assert.match(once, /№ 30\n\?\n!/);
});

// The rule the whole feature hangs on: ↑/↓ moves the CONTENT, and the ladder of
// point values stays exactly where it was — so an odd ladder survives a nudge.
test("swapSlots moves content, never the numbers", () => {
  const odd = splitTheme("#T Тема\n\n№ 10\n? Гавайи?\n\n№ 30\n? Кокос?\n\n№ 50\n? Pan Am?\n");
  const moved = swapSlots(odd, 1, 0);
  assert.deepEqual(moved.slots.map((s) => s.number), ["10", "30", "50"]);
  assert.deepEqual(moved.slots.map((s) => s.fields.question), ["Кокос?", "Гавайи?", "Pan Am?"]);
  assert.equal(swapSlots(odd, 0, -1), odd);
  assert.equal(swapSlots(odd, 0, 3), odd);
});

test("nextNumber fills the ladder, then запас", () => {
  const t = splitTheme(THEME);
  assert.equal(nextNumber(t.slots, false), "40");
  const full = splitTheme("#T Т\n№ 10\n?\n№ 20\n?\n№ 30\n?\n№ 40\n?\n№ 50\n?\n");
  assert.equal(nextNumber(full.slots, false), "запас1");
  assert.equal(nextNumber(full.slots, true), "запас1");
  assert.equal(nextNumber([...full.slots, { number: "запас1", fields: {} }], true), "запас2");
});

// A slot counts as written once it has question text: an answer alone is a note
// to oneself, not something a tester could be asked.
test("progress counts written slots out of at least five", () => {
  assert.deepEqual(progress(THEME), { filled: 2, total: 5 });
  assert.equal(isFilled(splitTheme(THEME).slots[2]), false);
  const six = splitTheme("#T Т\n№ 10\n? a\n№ 20\n? b\n№ 30\n? c\n№ 40\n? d\n№ 50\n? e\n№ запас1\n? f\n");
  assert.deepEqual(progress(composeTheme(six)), { filled: 6, total: 6 });
});

// The theme's own `@` is a default: a question that names nobody inherits it,
// and one that names someone replaces it outright.
test("authorsOf lets a question's own author win", () => {
  const t = splitTheme(THEME);
  assert.deepEqual(authorsOf(t, t.slots[0]), ["Александр Рождествин"]);
  assert.deepEqual(authorsOf(t, t.slots[1]), ["Наиль Фарукшин"]);
  const anon = splitTheme("#T Т\n№ 10\n? q\n");
  assert.deepEqual(authorsOf(anon, anon.slots[0]), []);
});

// An import or a card switched over from ОД holds no ladder at all — Поля then
// draws nothing rather than inventing five slots.
test("a description with no № line is a theme with no slots", () => {
  const t = splitTheme("? Обычный вопрос ОД\n! Ответ");
  assert.deepEqual(t.slots, []);
  assert.equal(t.name, "");
});
