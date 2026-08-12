import { test } from "node:test";
import assert from "node:assert/strict";
import { xyChgk } from "../web/assets/static/dist/chgk.js";

const { questionText, blockText, numberQuestionCards, parseBlocks, numberDirective,
  removeAccents, removeSquareBrackets, screenText, parse4sElem } = xyChgk;

test("question text strips the leading '? ' marker", () => {
  const desc = "? В каком году?\n! 1799\n^ источник";
  assert.equal(questionText(desc), "В каком году?");
});

test("question without a marker falls back to the whole text", () => {
  assert.equal(questionText("Просто текст вопроса"), "Просто текст вопроса");
});

test("multi-line question keeps continuation lines", () => {
  const desc = "? Первая строка\nвторая строка\n! ответ";
  assert.equal(questionText(desc), "Первая строка\nвторая строка");
});

test("meta and heading blocks are extracted", () => {
  assert.equal(blockText("# Редактор пакета", "meta"), "Редактор пакета");
  assert.equal(blockText("### Тур 1", "heading"), "Тур 1");
});

test("number directives: № explicit and №№ base", () => {
  assert.deepEqual(numberDirective(parseBlocks("№ 5\n? q")), { value: "5", base: false });
  assert.deepEqual(numberDirective(parseBlocks("№№ 10\n? q")), { value: "10", base: true });
});

test("auto-numbers questions 1,2,3 in order", () => {
  const cards = [
    { kind: "question", desc: "? a" },
    { kind: "question", desc: "? b" },
    { kind: "question", desc: "? c" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["1", "2", "3"]);
});

test("headings and meta do not consume numbers", () => {
  const cards = [
    { kind: "heading", desc: "### Тур 1" },
    { kind: "question", desc: "? a" },
    { kind: "meta", desc: "# инфо" },
    { kind: "question", desc: "? b" },
  ];
  assert.deepEqual(numberQuestionCards(cards), [null, "1", null, "2"]);
});

test("№№ resets the running base and subsequent questions continue", () => {
  const cards = [
    { kind: "question", desc: "№№ 4\n? a" },
    { kind: "question", desc: "? b" },
    { kind: "question", desc: "? c" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["4", "5", "6"]);
});

test("explicit № overrides but a zero number does not advance the counter", () => {
  const cards = [
    { kind: "question", desc: "№ 0\n? warmup" },
    { kind: "question", desc: "? first real" },
    { kind: "question", desc: "№ 7\n? seven" },
    { kind: "question", desc: "? eight" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["0", "1", "7", "8"]);
});

// ── screen-mode transforms ──────────────────────────────────────────────────
test("removeAccents strips U+0301 stress marks", () => {
  assert.equal(removeAccents("при́вет мо́ре"), "привет море");
});

test("removeAccents keeps accents inside handout brackets", () => {
  assert.equal(
    removeAccents("сло́во [Раздаточный материал: за́мок]"),
    "слово [Раздаточный материал: за́мок]",
  );
});

test("removeSquareBrackets drops host notes but keeps handouts", () => {
  assert.equal(
    removeSquareBrackets("текст [пауза для ведущего] дальше"),
    "текст дальше",
  );
  assert.equal(
    removeSquareBrackets("вопрос [Раздаточный материал: фото] и всё"),
    "вопрос [Раздаточный материал: фото] и всё",
  );
});

test("removeSquareBrackets unescapes literal brackets", () => {
  assert.equal(removeSquareBrackets("массив a\\[i\\]"), "массив a[i]");
});

test("screenText applies both transforms", () => {
  assert.equal(
    screenText("Назови́те [для ведущего: не торопясь] го́род."),
    "Назовите город.",
  );
});

test("a legacy \"> \" handout block is still offered as its own paste", () => {
  const t = xyChgk.copyTargets("> Схема ме́тро\n? Что на схеме?\n! круг", "3");
  assert.deepEqual(t.map((x) => x.text), [
    "Раздаточный материал:\nСхема метро",
    "Вопрос 3. Что на схеме?",
    "Что на схеме?",
    "Раздаточный материал:\nСхема метро\n\nВопрос 3. Что на схеме?",
    "Раздаточный материал:\nСхема метро\n\nВопрос 3. Что на схеме?\n\nОтвет: круг",
  ]);
});

test("numberQuestionCards: №№ on a heading card resets the base for following questions", () => {
  const cards = [
    { kind: "question", desc: "? a" },
    { kind: "heading", desc: "### Тур 2\n№№ 10" },
    { kind: "question", desc: "? b" },
    { kind: "question", desc: "? c" },
  ];
  assert.deepEqual(numberQuestionCards(cards), ["1", null, "10", "11"]);
});

test("numberQuestionCards: №№ on a meta card resets, but an 'other' card is ignored", () => {
  const cards = [
    { kind: "meta", desc: "# редактор\n№№ 7" },
    { kind: "question", desc: "? a" },
    { kind: "other", desc: "№№ 99" },
    { kind: "question", desc: "? b" },
  ];
  assert.deepEqual(numberQuestionCards(cards), [null, "7", null, "8"]);
});

test("screenText resolves (LINEBREAK) to a newline", () => {
  assert.equal(screenText("До(LINEBREAK)после"), "До\nпосле");
});

test("screenText keeps the for_screen side of a (screen …) directive", () => {
  assert.equal(screenText("(screen печать|экран)"), "экран");
  assert.equal(screenText("текст (screen А|Б) хвост"), "текст Б хвост");
});

test("screenText strips inline formatting markers but keeps the text", () => {
  assert.equal(screenText("_курсив_ и __жирный__"), "курсив и жирный");
  assert.equal(screenText("~зачёркнутый~ текст"), "зачёркнутый текст");
});

test("screenText does not corrupt underscores inside URLs", () => {
  assert.equal(
    screenText("см. http://example.com/a_b_c дальше"),
    "см. http://example.com/a_b_c дальше",
  );
});

test("screenText backtick adds a combining stress accent (chgksuite applies it after accent removal, so it survives)", () => {
  assert.equal(screenText("сл`ово"), "сло́во");
});

test("screenText drops (img …) and (PAGEBREAK) directives", () => {
  assert.equal(screenText("текст (PAGEBREAK)ещё").includes("PAGEBREAK"), false);
  assert.equal(screenText("(img foo.jpg)подпись").includes("img"), false);
});

test("parse4sElem tags the inline directives", () => {
  const runs = parse4sElem("a (LINEBREAK)b (screen p|s)");
  const types = runs.map((r) => r[0]);
  assert.ok(types.includes("linebreak"));
  assert.ok(types.includes("screen"));
  const screenRun = runs.find((r) => r[0] === "screen");
  assert.deepEqual(screenRun[1], { for_print: "p", for_screen: "s" });
});

test("printRuns keeps host-only square brackets and accents (print mode)", () => {
  const runs = xyChgk.printRuns("текст [реплика ведущего] сл`ово");
  const flat = runs.map((r) => (typeof r[1] === "string" ? r[1] : "")).join("");
  assert.ok(flat.includes("[реплика ведущего]"), "host brackets preserved");
  assert.ok(flat.includes("сло́во"), "backtick stress resolved");
});

test("printRuns unescapes \\[ and \\] to literal brackets", () => {
  const runs = xyChgk.printRuns("\\[не директива\\]");
  const flat = runs.map((r) => (typeof r[1] === "string" ? r[1] : "")).join("");
  assert.equal(flat, "[не директива]");
});

test("printRuns tags an (img …) run with its filename as the last token", () => {
  const runs = xyChgk.printRuns("(img w=300 cat.jpg)");
  const img = runs.find((r) => r[0] === "img");
  assert.ok(img, "img run present");
  assert.equal(String(img[1]).trim().split(/\s+/).pop(), "cat.jpg");
});

test("applyOverride peels a !!Label override (~ → space) off a field value", () => {
  assert.deepEqual(xyChgk.applyOverride("!!Авторка Анна Смирнова"),
    { label: "Авторка", text: "Анна Смирнова" });
  assert.deepEqual(xyChgk.applyOverride("!!Верный~ответ Москва"),
    { label: "Верный ответ", text: "Москва" });
});

test("applyOverride leaves a normal value untouched", () => {
  assert.deepEqual(xyChgk.applyOverride("Анна Смирнова"), { label: null, text: "Анна Смирнова" });
  assert.deepEqual(xyChgk.applyOverride("- один\n- два"), { label: null, text: "- один\n- два" });
});

test("splitList turns '- ' lines into numbered items (with preamble)", () => {
  assert.deepEqual(xyChgk.splitList("см.:\n- источник один\n- источник два"),
    { preamble: "см.:", items: ["источник один", "источник два"] });
});

test("splitList: a single '- ' item is not a list (marker stripped)", () => {
  assert.deepEqual(xyChgk.splitList("- единственный"), { preamble: "единственный", items: null });
});

test("splitList: no dash → plain text", () => {
  assert.deepEqual(xyChgk.splitList("обычный текст"), { preamble: "обычный текст", items: null });
});

test("splitList handles a blitz question (no preamble, multiple items)", () => {
  const r = xyChgk.splitList("- Первый вопрос?\n- Второй вопрос?\n- Третий вопрос?");
  assert.equal(r.preamble, "");
  assert.deepEqual(r.items, ["Первый вопрос?", "Второй вопрос?", "Третий вопрос?"]);
});

test("renderRuns screen mode strips host brackets and accents", () => {
  const runs = xyChgk.renderRuns("текст [ведущему] сл́ово", { accents: true, brackets: true });
  const flat = runs.map((r) => (typeof r[1] === "string" ? r[1] : "")).join("");
  assert.ok(!flat.includes("["), "host brackets removed");
  assert.ok(!flat.includes("́"), "accent removed");
});

test("renderRuns answer-style (accents only) keeps brackets", () => {
  const runs = xyChgk.renderRuns("Москва [и область]", { accents: true, brackets: false });
  const flat = runs.map((r) => (typeof r[1] === "string" ? r[1] : "")).join("");
  assert.ok(flat.includes("[и область]"), "brackets kept for answer/zachet");
});

test("replaceNoBreak glues short prepositions and particles with NBSP", () => {
  assert.equal(xyChgk.replaceNoBreak("в лесу"), "в лесу");
  assert.equal(xyChgk.replaceNoBreak("сделал бы"), "сделал бы");
  assert.equal(xyChgk.replaceNoBreak("то да сё"), "то да сё");
});

test("replaceNoBreak uses a non-breaking hyphen in short hyphenated words", () => {
  assert.equal(xyChgk.replaceNoBreak("из-за"), "из‑за");
  // a stray spaced hyphen must NOT turn every hyphen non-breaking
  assert.equal(xyChgk.replaceNoBreak("кто - то"), "кто - то");
});

test("replaceNoBreak leaves URLs untouched", () => {
  assert.equal(xyChgk.replaceNoBreak("см. http://a.com/x_y и тут"), "см. http://a.com/x_y и тут");
});

test("fixTrelloFormatting collapses double line breaks and unescapes markers", () => {
  const raw = "\\### Тур 1\n\n? Вопрос\n\n\\@ Автор\n\n  отступ\n\\- пункт";
  const out = xyChgk.fixTrelloFormatting(raw);
  assert.equal(out, "### Тур 1\n? Вопрос\n@ Автор\nотступ\n- пункт");
});

test("fixTrelloFormatting collapses Trello smart-link [url](url) to a bare url", () => {
  const raw = "см. [https://example.com/a_b](https://example.com/a_b) тут";
  assert.equal(xyChgk.fixTrelloFormatting(raw), "см. https://example.com/a_b тут");
});

test("fixTrelloFormatting keeps real markdown links (text != url) intact", () => {
  const raw = "[пример](https://example.com)";
  assert.equal(xyChgk.fixTrelloFormatting(raw), "[пример](https://example.com)");
});

test("fixTrelloFormatting strips code fences", () => {
  assert.equal(xyChgk.fixTrelloFormatting("```\n? q\n```"), "\n? q\n");
});

// ── structured fields ────────────────────────────────────────────────────────
const { splitFields, composeFields, generateHndt, parseHndtMetaByQuestion } = xyChgk;

test("splitFields separates known question fields", () => {
  const desc = "№№ 5\n> (img map.png)\n? Что на схеме?\n! круг\n= окружность\n!= квадрат\n/ комментарий\n^ книга\n@ Иванов, Пётр";
  const f = splitFields(desc);
  assert.equal(f.preMarkup, "№№ 5");
  assert.deepEqual(f.handout, { kind: "image", name: "map.png" });
  assert.equal(f.question, "Что на схеме?");
  assert.equal(f.answer, "круг");
  assert.equal(f.zachet, "окружность");
  assert.equal(f.nezachet, "квадрат");
  assert.equal(f.comment, "комментарий");
  assert.deepEqual(f.sources, ["книга"]);
  assert.deepEqual(f.authors, ["Иванов", "Пётр"]);
});

test("splitFields distinguishes absent vs present-empty fields", () => {
  const f = splitFields("? Вопрос\n!\n="); // answer + zachet present but empty; others absent
  assert.equal(f.answer, "");
  assert.equal(f.zachet, "");
  assert.equal(f.nezachet, null);
  assert.equal(f.comment, null);
  assert.equal(f.sources, null);
  assert.equal(f.authors, null);
  assert.equal(f.handout, null);
});

test("composeFields round-trips a structured question", () => {
  const desc = "? Что на схеме?\n! круг\n^ книга\n@ Иванов";
  assert.equal(composeFields(splitFields(desc)), desc);
});

// The handout lives INSIDE the question as the chgksuite-style bracket — the
// old standalone "> " block never reached the docx/PDF exporters.
test("multi-line handout composes to the block bracket and round-trips", () => {
  const desc = "? [Раздаточный материал:\nСхема:\nкруг\n]\nЧто на схеме?\n! круг";
  const f = splitFields(desc);
  assert.deepEqual(f.handout, { kind: "text", text: "Схема:\nкруг" });
  assert.equal(f.question, "Что на схеме?");
  assert.equal(composeFields(f), desc);
});

test("one-line text and image handouts use the single-line bracket", () => {
  for (const [desc, handout] of [
    ["? [Раздаточный материал: (img map.png)]\nЧто тут?\n! х", { kind: "image", name: "map.png" }],
    ["? [Раздаточный материал: АБВ]\nЧто тут?\n! х", { kind: "text", text: "АБВ" }],
  ]) {
    const f = splitFields(desc);
    assert.deepEqual(f.handout, handout);
    assert.equal(f.question, "Что тут?");
    assert.equal(composeFields(f), desc);
  }
});

test("legacy '> ' handout still parses and migrates to the inline form", () => {
  const f = splitFields("> Схема\n? Что на схеме?\n! круг");
  assert.deepEqual(f.handout, { kind: "text", text: "Схема" });
  assert.equal(composeFields(f), "? [Раздаточный материал: Схема]\nЧто на схеме?\n! круг");
});

test("the handout lands after a leading host note, and extracts from there", () => {
  const desc = "? [Ведущему: не торопитесь]\n[Раздаточный материал: АБВ]\nЧто это?\n! х";
  const f = splitFields(desc);
  assert.deepEqual(f.handout, { kind: "text", text: "АБВ" });
  assert.equal(f.question, "[Ведущему: не торопитесь]\nЧто это?");
  assert.equal(composeFields(f), desc);
});

test("a mid-question handout extracts, anchors, and returns to its spot", () => {
  const desc = "? Взгляните на [Раздаточный материал: АБВ] и ответьте.\n! х";
  const f = splitFields(desc);
  assert.deepEqual(f.handout, { kind: "text", text: "АБВ" });
  assert.equal(f.question, "Взгляните на [Раздаточный материал] и ответьте.");
  assert.equal(composeFields(f), desc); // verbatim, bracket back mid-sentence
});

test("editing an anchored handout keeps its position", () => {
  const f = splitFields("? Взгляните на [Раздаточный материал: АБВ] и ответьте.\n! х");
  f.handout = { kind: "text", text: "ГДЕ" };
  assert.equal(composeFields(f), "? Взгляните на [Раздаточный материал: ГДЕ] и ответьте.\n! х");
});

test("removing the handout field tidies the anchor away", () => {
  const f = splitFields("? Взгляните на [Раздаточный материал: АБВ] и ответьте.\n! х");
  f.handout = null;
  assert.equal(composeFields(f), "? Взгляните на и ответьте.\n! х");
});

test("composeFields keeps a bare marker for present-empty fields", () => {
  const f = splitFields("? Вопрос\n!");
  assert.equal(composeFields(f), "? Вопрос\n!");
});

test("source list of several lines composes a '- ' list", () => {
  const f = splitFields("? Q\n^\n- один\n- два");
  assert.deepEqual(f.sources, ["один", "два"]);
  assert.equal(composeFields(f), "? Q\n^\n- один\n- два");
});

test("unmodelled blocks survive as extra", () => {
  const f = splitFields("? Q\n! A\n## секция");
  assert.equal(f.extra, "## секция");
  assert.equal(composeFields(f), "? Q\n! A\n## секция");
});

// ── handout generation ───────────────────────────────────────────────────────
test("generateHndt emits a block per question with a handout", () => {
  const cards = [
    { id: 1, kind: "question", desc: "> Текст раздатки\n? Вопрос 1\n! ответ" },
    { id: 2, kind: "question", desc: "? Без раздатки\n! ответ" },
    { id: 3, kind: "question", desc: "> (img foto.png)\n? Что тут?\n! х" },
  ];
  const numbers = ["1", "2", "3"];
  const out = generateHndt(cards, numbers, {});
  const blocks = out.split("\n---\n");
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0], "for_question: 1\ncolumns: 3\n\nТекст раздатки");
  assert.equal(blocks[1], "for_question: 3\ncolumns: 3\n\nimage: foto.png");
});

test("generateHndt uses saved per-question settings", () => {
  const cards = [{ id: 7, kind: "question", desc: "> Раздатка\n? Q\n! a" }];
  const out = generateHndt(cards, ["4"], { 7: "columns: 2\nrows: 5" });
  assert.equal(out, "for_question: 4\ncolumns: 2\nrows: 5\n\nРаздатка");
});

test("generateHndt reads a legacy inline handout bracket", () => {
  const cards = [{ id: 1, kind: "question", desc: "? Текст [Раздаточный материал: листок] вопроса\n! a" }];
  const out = generateHndt(cards, ["1"], {});
  assert.equal(out, "for_question: 1\ncolumns: 3\n\nлисток");
});

test("parseHndtMetaByQuestion strips content, keeps settings by question", () => {
  const hndt = "for_question: 1\ncolumns: 2\nrows: 3\n\nтекст\n---\nfor_question: 4\ncolumns: 3\n\nimage: a.png";
  const m = parseHndtMetaByQuestion(hndt);
  assert.equal(m["1"], "columns: 2\nrows: 3");
  assert.equal(m["4"], "columns: 3");
});

// ---- test cards: tester lists ----
const { parseTestCard, serializeTestCard, testersToText, testersFromText, testerCopyText } = xyChgk;

test("parseTestCard reads the new {testers} shape", () => {
  const desc = JSON.stringify({ datetime: "2026-06-29 12:00", title: "Иван Иванов", testers: [
    { text: "Александр Иванов", type: "player" }, { text: "Ромашка", type: "team" }] });
  const m = parseTestCard(desc);
  assert.equal(m.datetime, "2026-06-29 12:00");
  assert.equal(m.title, "Иван Иванов");
  assert.deepEqual(m.testers, [
    { text: "Александр Иванов", type: "player" }, { text: "Ромашка", type: "team" }]);
});

test("parseTestCard migrates legacy {players:[ids]} to player strings", () => {
  const m = parseTestCard(JSON.stringify({ datetime: "d", players: [12, 34] }));
  assert.deepEqual(m.testers, [
    { text: "12", type: "player" }, { text: "34", type: "player" }]);
});

test("parseTestCard tolerates garbage and bad types", () => {
  assert.deepEqual(parseTestCard("not json").testers, []);
  const m = parseTestCard(JSON.stringify({ testers: [{ text: "X", type: "weird" }, null, { text: 5 }] }));
  assert.deepEqual(m.testers, [{ text: "X", type: "player" }, { text: "5", type: "player" }]);
});

test("testersToText / testersFromText round-trip", () => {
  const testers = [{ text: "Александр Иванов", type: "player" }, { text: "Ромашка", type: "team" }];
  assert.equal(testersToText(testers), "- Александр Иванов\n-T Ромашка");
  assert.deepEqual(testersFromText("- Александр Иванов\n-T Ромашка"), testers);
});

test("testersFromText skips blank lines and trims, tolerates missing space", () => {
  assert.deepEqual(testersFromText("\n-  Имя  \n\n-T  Тим \n"), [
    { text: "Имя", type: "player" }, { text: "Тим", type: "team" }]);
  // a name starting with T is still a player (the -T marker needs no inner letter)
  assert.deepEqual(testersFromText("- Tom"), [{ text: "Tom", type: "player" }]);
});

test("serializeTestCard drops blank rows and keeps datetime/title", () => {
  const json = serializeTestCard({ datetime: "d", title: "t", testers: [
    { text: " A ", type: "player" }, { text: "", type: "team" }] });
  assert.deepEqual(JSON.parse(json), { datetime: "d", title: "t", testers: [{ text: "A", type: "player" }] });
});

test("testerCopyText sorts players by surname then given, teams alphabetically", () => {
  const testers = [
    { text: "Борис Иванов", type: "player" },
    { text: "Александр Иванов", type: "player" },
    { text: "Яна Архипова", type: "player" },
    { text: "Ромашка", type: "team" },
    { text: "Авангард", type: "team" },
  ];
  assert.equal(testerCopyText(testers),
    "Вопросы тестировали: Яна Архипова, Александр Иванов, Борис Иванов" +
    ", а также команды: Авангард, Ромашка");
});

test("testerCopyText dedupes and handles players-only / teams-only / empty", () => {
  assert.equal(testerCopyText([
    { text: "Иван Иванов", type: "player" }, { text: "Иван Иванов", type: "player" }]),
    "Вопросы тестировали: Иван Иванов");
  assert.equal(testerCopyText([{ text: "Альфа", type: "team" }]),
    "Вопросы тестировали команды: Альфа");
  assert.equal(testerCopyText([]), "");
});

// ---- card-preview modes (users.card_title) ----

test("answerText returns the '! ' block, or '' when there is none", () => {
  assert.equal(xyChgk.answerText("? Кто?\n! Пушкин\n/ комментарий"), "Пушкин");
  assert.equal(xyChgk.answerText("? Кто?"), "");
});

test("previewText in answer mode previews a question by its answer", () => {
  const desc = "? Длинный текст вопроса\n! Пушкин";
  assert.equal(xyChgk.previewText("question", desc, "answer"), "Пушкин");
  assert.equal(xyChgk.previewText("question", desc, "question"), "Длинный текст вопроса");
  // No mode is the historic default.
  assert.equal(xyChgk.previewText("question", desc), "Длинный текст вопроса");
});

test("an answerless question falls back to its text rather than previewing blank", () => {
  assert.equal(xyChgk.previewText("question", "? Кто?", "answer"), "Кто?");
});

test("answer mode does not touch non-question cards", () => {
  assert.equal(xyChgk.previewText("heading", "### Тур 1", "answer"), "Тур 1");
  assert.equal(xyChgk.previewText("meta", "# Дата", "answer"), "Дата");
});

// ---- question versions (issue #47) ----
// A Version is a WHOLE 4s body — question, ответ, зачёт, раздатка and all —
// stored in the card's own description, each body introduced by a standalone
// (hidden-comment xy-version: имя) line. The export merges them back into one
// question block, so a versioned card is still one numbered question.
const {
  splitVersions, versionBody, versionName, setVersionBody, setVersionName,
  addVersion, removeVersion, promoteVersion, convertLegacyVersions, composeVersions,
} = xyChgk;

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

// ---- what a card offers to copy (issue #45) ----
const { copyTargets } = xyChgk;

test("a numberless question with nothing else offers exactly one thing to copy", () => {
  const t = copyTargets("? Простой вопрос?", null);
  assert.deepEqual(t, [{ label: "Вопрос", text: "Простой вопрос?" }]);
});

test("a numbered question also offers itself without the number", () => {
  const t = copyTargets("? Простой вопрос?", 12);
  assert.deepEqual(t, [
    { label: "Вопрос", text: "Вопрос 12. Простой вопрос?" },
    { label: "Вопрос без номера", text: "Простой вопрос?" },
  ]);
});

test("an answer earns its own target, always last", () => {
  const t = copyTargets("? Простой вопрос?\n! Ответ", 12);
  assert.deepEqual(t.map((x) => x.label), ["Вопрос", "Вопрос без номера", "Вопрос с ответом"]);
  assert.equal(t.at(-1).text, "Вопрос 12. Простой вопрос?\n\nОтвет: Ответ");
});

test("a handout is its own paste, and no longer rides along with the question", () => {
  const t = copyTargets("? [Раздаточный материал: схема] Что на схеме?\n! круг", 3);
  assert.deepEqual(t.map((x) => x.label),
    ["Раздатка", "Вопрос", "Вопрос без номера", "Вопрос целиком", "Вопрос с ответом"]);
  assert.equal(t[0].text, "Раздаточный материал:\nсхема");
  assert.equal(t[1].text, "Вопрос 3. Что на схеме?");
});

test("a text handout and its question are also offered as one paste", () => {
  const t = copyTargets("? [Раздаточный материал: схема] Что на схеме?\n! круг", 3);
  const whole = t.find((x) => x.label === "Вопрос целиком");
  assert.equal(whole.text, "Раздаточный материал:\nсхема\n\nВопрос 3. Что на схеме?");
});

test("an image handout offers the picture, not its filename", () => {
  const t = copyTargets("? [Раздаточный материал: (img map.png)] Что тут?\n! круг", 1);
  assert.deepEqual(t[0], { label: "Раздатка", image: "map.png" });
});

test("a picture cannot be pasted with the text, so «целиком» points at it instead", () => {
  const t = copyTargets("? [Раздаточный материал: (img map.png)] Что тут?\n! круг", 1);
  assert.equal(t.find((x) => x.label === "Вопрос целиком").text,
    "[Раздаточный материал: см. изображение]\n\nВопрос 1. Что тут?");
});

test("a blitz offers one paste per leg, the lead-in only on the first", () => {
  const desc = "? Блиц:\n- Первый вопрос?\n- Второй вопрос?\n- Третий вопрос?\n! ответы";
  const t = copyTargets(desc, 12);
  assert.deepEqual(t.map((x) => x.label),
    ["Вопрос 1", "Вопрос 2", "Вопрос 3", "Вопрос без номера", "Вопрос целиком", "Вопрос с ответом"]);
  const legs = t.filter((x) => /^Вопрос \d+$/.test(x.label));
  assert.equal(legs[0].text, "Вопрос 12. Блиц:\n1. Первый вопрос?");
  assert.equal(legs[1].text, "2. Второй вопрос?");
  assert.equal(legs[2].text, "3. Третий вопрос?");
});

test("«без номера» is the whole blitz with the number peeled off", () => {
  const desc = "? Блиц:\n- Первый вопрос?\n- Второй вопрос?\n! ответы";
  assert.equal(copyTargets(desc, 12).find((x) => x.label === "Вопрос без номера").text,
    "Блиц:\n1. Первый вопрос?\n2. Второй вопрос?");
});

test("«целиком» is every leg of a blitz in one paste", () => {
  const desc = "? Блиц:\n- Первый вопрос?\n- Второй вопрос?\n! ответы";
  assert.equal(copyTargets(desc, 12).find((x) => x.label === "Вопрос целиком").text,
    "Вопрос 12. Блиц:\n1. Первый вопрос?\n2. Второй вопрос?");
});

test("«с ответом» carries every field the exports print, in their order", () => {
  const desc = "? Вопрос?\n! Ответ\n= Зачёт\n!= Незачёт\n/ Комментарий\n^ Источник\n@ Иванов";
  assert.equal(copyTargets(desc, 7).at(-1).text,
    "Вопрос 7. Вопрос?\n\nОтвет: Ответ\nЗачёт: Зачёт\nНезачёт: Незачёт\n" +
    "Комментарий: Комментарий\nИсточник: Источник\nАвтор: Иванов");
});

test("«с ответом» numbers a list answer and pluralises several sources", () => {
  const desc = "? Блиц:\n- Раз?\n- Два?\n! - Один\n- Два\n^ - книга\n- сайт";
  assert.equal(copyTargets(desc, 4).find((x) => x.label === "Вопрос с ответом").text,
    "Вопрос 4. Блиц:\n1. Раз?\n2. Два?\n\nОтвет:\n1. Один\n2. Два\nИсточники:\n1. книга\n2. сайт");
});

test("«с ответом» honours a !!Label caption, on a field and on the authors", () => {
  const desc = "? Вопрос?\n! !!Верный~ответ Москва\n@ !!Авторка Мария";
  assert.equal(copyTargets(desc, 1).at(-1).text,
    "Вопрос 1. Вопрос?\n\nВерный ответ: Москва\nАвторка: Мария");
});

test("an answer keeps its bracketed alternatives — they are part of the answer", () => {
  const desc = "? Вопрос?\n! Москва [или Москова]\n= Подмосковье [тоже]\n/ Комментарий [для ведущего]";
  assert.equal(copyTargets(desc, 1).at(-1).text,
    "Вопрос 1. Вопрос?\n\nОтвет: Москва [или Москова]\nЗачёт: Подмосковье [тоже]\nКомментарий: Комментарий");
});

test("a field that is a bare marker prints nothing rather than an empty caption", () => {
  const t = copyTargets("? Вопрос?\n! Ответ\n=\n/", 1);
  assert.equal(t.at(-1).text, "Вопрос 1. Вопрос?\n\nОтвет: Ответ");
});

test("copying takes the version you are looking at", () => {
  const q = (desc) => copyTargets(desc, 5).find((x) => x.label === "Вопрос").text;
  assert.equal(q(versionBody(TWO, 0)), "Вопрос 5. Первая?");
  assert.equal(q(versionBody(TWO, 1)), "Вопрос 5. Вторая?");
});

test("screen mode still applies — host notes and accents are not sent to testers", () => {
  const t = copyTargets("? [Ведущему: не читать] Вопро́с?\n! Ответ", 1);
  assert.equal(t.find((x) => x.label === "Вопрос").text, "Вопрос 1. Вопрос?");
});

// ---- author caption (issue #44) ----
// chgksuite prints «Автор» and never pluralises, so every other caption is
// spelled out in the 4s as a "!!override" the exporters already honour.

test("the author caption is peeled off the names, not left in the first one", () => {
  const f = splitFields("? Вопрос\n@ !!Авторка Мария Петрова");
  assert.equal(f.authorLabel, "Авторка");
  assert.deepEqual(f.authors, ["Мария Петрова"]);
});

test("Автор is the default and writes no override", () => {
  const f = splitFields("? Вопрос\n@ Иванов");
  assert.equal(f.authorLabel, null);
  assert.equal(composeFields({ ...f, authorLabel: "Автор" }), "? Вопрос\n@ Иванов");
});

test("a chosen caption round-trips through compose and split", () => {
  for (const label of ["Авторка", "Авторы", "Авторки"]) {
    const desc = composeFields({ question: "Вопрос", authors: ["А", "Б"], authorLabel: label });
    assert.equal(desc, `? Вопрос\n@ !!${label} А, Б`);
    const back = splitFields(desc);
    assert.equal(back.authorLabel, label);
    assert.deepEqual(back.authors, ["А", "Б"]);
  }
});

test("a caption we do not know survives untouched", () => {
  const desc = "? Вопрос\n@ !!Составитель Иванов";
  const f = splitFields(desc);
  assert.equal(f.authorLabel, "Составитель");
  assert.equal(composeFields(f), desc);
});

test("a multi-word caption keeps its ~ separator", () => {
  const desc = composeFields({ question: "В", authors: ["И"], authorLabel: "Автор вопроса" });
  assert.equal(desc, "? В\n@ !!Автор~вопроса И");
  assert.equal(splitFields(desc).authorLabel, "Автор вопроса");
});

// The importer — ours and chgksuite's alike — glues the caption onto the name
// with no space, which is unsplittable by the generic override rule.
test("an imported «!!АвторкаМария» is recovered rather than shown as a name", () => {
  const f = splitFields("? Вопрос\n@ !!АвторкаМария");
  assert.equal(f.authorLabel, "Авторка");
  assert.deepEqual(f.authors, ["Мария"]);
});

// ---- hidden comments (chgksuite 1.4.0b1) ----
// (hidden-comment …) is an editor's note that reaches no rendering. Only Текст
// (the source verbatim) and Поля (whose fields are raw 4s) show one.
const { previewText } = xyChgk;

test("a hidden comment reaches no rendering", () => {
  assert.equal(screenText("Текст (hidden-comment проверить у Ани) дальше."), "Текст дальше.");
  assert.deepEqual(parse4sElem("Ответ\n(hidden-comment записка)\nещё"), [["", "Ответ\nещё"]]);
});

test("an unterminated hidden comment stays literal, hiding nothing by accident", () => {
  const s = "Текст (hidden-comment забыл скобку";
  assert.equal(parse4sElem(s).map((p) => p[1]).join(""), s);
});

test("a hidden comment swallows the directives inside it", () => {
  assert.equal(screenText("Текст (hidden-comment см. (img foo.jpg)) дальше."), "Текст дальше.");
});

test("a card on the board shows no hidden comment in its title", () => {
  assert.equal(previewText("question", "? Вопрос (hidden-comment спросить Аню)?", null), "Вопрос?");
});

test("a раздатка carries no hidden comment to the print", () => {
  const desc = "? [Раздаточный материал: текст (hidden-comment вырезать)] Вопрос?\n! Ответ";
  assert.deepEqual(xyChgk.handoutForCard(desc), { kind: "text", text: "текст" });
});
