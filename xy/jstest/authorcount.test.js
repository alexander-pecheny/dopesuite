import { test } from "node:test";
import assert from "node:assert/strict";
import { xyAuthorCount } from "../web/assets/static/dist/authorcount.js";

const { countAuthors, formatShare } = xyAuthorCount;

const q = (authors, pre = "") => ({ kind: "question", desc: `${pre}? текст\n! ответ${authors == null ? "" : `\n@ ${authors}`}` });

const CARDS = [
  q("Вася"),                       // 1
  { kind: "heading", desc: "Тур 1" },
  q("Ва́ся, Петя"),                 // 2
  q("вася"),                       // 3
  q(null),                         // 4
  q("Ёлкин"),                      // 5
  q("Петя"),                       // 6
];

test("counts one per name and splits the доля of a co-authored question", () => {
  const r = countAuthors(CARDS, "6", false);
  assert.equal(r.cutoffFound, true);
  assert.equal(r.questions, 6);
  assert.deepEqual(r.rows.map((x) => [x.name, x.count, x.share, x.numbers]), [
    ["Вася", 3, 2.5, ["1", "2", "3"]],
    ["Петя", 2, 1.5, ["2", "6"]],
    ["Ёлкин", 1, 1, ["5"]],
  ]);
  assert.deepEqual(r.unauthored, { name: "без автора", count: 1, share: 1, numbers: ["4"] });
});

test("the cutoff is a display number, inclusive; an unknown one finds nothing", () => {
  const r = countAuthors(CARDS, "2", false);
  assert.deepEqual(r.rows.map((x) => [x.name, x.count]), [["Вася", 2], ["Петя", 1]]);
  assert.equal(r.unauthored, null);
  assert.equal(countAuthors(CARDS, "9", false).cutoffFound, false);
  assert.equal(countAuthors(CARDS, "", false).cutoffFound, false);
});

test("spellings fold like a search, and the row shows the commonest one without accents", () => {
  const r = countAuthors([q("Ёлкин"), q("елкин"), q("Ёлкин"), q("Ёлкин"), q("Ва́ся Пупкин"), q("Вася  Пупкин")], "6", false);
  assert.deepEqual(r.rows.map((x) => [x.name, x.count]), [["Ёлкин", 4], ["Вася Пупкин", 2]]);
});

test("нулевые are skipped unless asked for, and their presence is reported", () => {
  const cards = [q("Вася", "№ 0\n"), q("Петя"), q("Вася")];
  const off = countAuthors(cards, "2", false);
  assert.equal(off.hasZero, true);
  assert.deepEqual(off.rows.map((x) => [x.name, x.count]), [["Вася", 1], ["Петя", 1]]);
  const on = countAuthors(cards, "2", true);
  assert.deepEqual(on.rows.map((x) => [x.name, x.count, x.numbers]), [["Вася", 2, ["0", "2"]], ["Петя", 1, ["1"]]]);
  assert.equal(countAuthors([q("Вася")], "1", false).hasZero, false);
});

test("only the first version's authors count", () => {
  const two = { kind: "question", desc: "(hidden-comment xy-version:)\n? а\n! б\n@ Вася\n(hidden-comment xy-version: полегче)\n? в\n! г\n@ Петя" };
  assert.deepEqual(countAuthors([two], "1", false).rows.map((x) => x.name), ["Вася"]);
});

test("formatShare keeps one decimal", () => {
  assert.equal(formatShare(2), "2");
  assert.equal(formatShare(2.5), "2.5");
  assert.equal(formatShare(1 / 3), "0.3");
  assert.equal(formatShare(2 / 3 + 1), "1.7");
});
