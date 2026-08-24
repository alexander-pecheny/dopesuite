// Regenerates internal/xycli/testdata/exportsource.json — the corpus that pins
// xy-cli's Go export-source assembly against the browser's (export.ts +
// versions.ts), which is the only authority on what a List exports as.
//   deno run --allow-read --allow-write scripts/gen_source_fixture.js
import { exportSource } from "../web/assets/static/dist/export.js";

const cases = [
  { name: "one plain question", cards: [
    "? Что это?\n! Envelope\n@ Автор",
  ]},
  { name: "two cards, blank line inside one", cards: [
    "? Первый вопрос\n\nвторая строка\n! Ответ",
    "? Второй вопрос\n! Ответ 2",
  ]},
  { name: "versions differing in question only", cards: [
    "(hidden-comment xy-version:)\n? Версия раз\n! Ответ\n(hidden-comment xy-version:)\n? Версия два\n! Ответ",
  ]},
  { name: "versions differing in answer", cards: [
    "(hidden-comment xy-version:)\n? Вопрос\n! Ответ раз\n(hidden-comment xy-version:)\n? Вопрос\n! Ответ два",
  ]},
  { name: "named version", cards: [
    "(hidden-comment xy-version: полегче)\n? Лёгкий\n! Ответ\n(hidden-comment xy-version: посложнее)\n? Трудный\n! Ответ",
  ]},
  { name: "heading and question", cards: [
    "## Тур 1",
    "? Вопрос\n! Ответ\n= Зачёт\n/ Комментарий\n^ Источник\n@ Автор",
  ]},
  { name: "handout bracket and image", cards: [
    "? [Раздаточный материал: (img pic.png)]\nЧто на картинке?\n! Ничего",
  ]},
  { name: "multiline fields and a source list", cards: [
    "? Первая строка\nвторая строка\n! Ответ\nтоже ответ\n^\n- Источник раз\n- Источник два\n@ !!Авторка Мария",
  ]},
  { name: "present but empty fields", cards: [
    "? Вопрос\n!\n=\n^\n@",
  ]},
  { name: "versions disagreeing on sources and authors", cards: [
    "(hidden-comment xy-version:)\n? Раз\n! Ответ\n^ Первый источник\n@ Пётр\n(hidden-comment xy-version:)\n? Два\n! Ответ\n^ Второй источник\n@ Мария",
  ]},
  { name: "version with a number directive and trailing extra", cards: [
    "(hidden-comment xy-version:)\n№ 5\n? Раз\n! Ответ\n(hidden-comment xy-version:)\n? Два\n! Ответ",
  ]},
  { name: "one version missing a field entirely", cards: [
    "(hidden-comment xy-version:)\n? Раз\n! Ответ\n/ Комментарий\n(hidden-comment xy-version:)\n? Два\n! Ответ",
  ]},
  { name: "card with no marker at all", cards: [
    "Просто текст без маркера",
  ]},
];

const out = cases.map((c) => ({
  name: c.name,
  cards: c.cards,
  source: exportSource(c.cards.map((desc) => ({ desc }))),
}));

const path = new URL("../internal/xycli/testdata/exportsource.json", import.meta.url);
await Deno.writeTextFile(path, JSON.stringify(out, null, 1) + "\n");
console.log("wrote", path.pathname);
