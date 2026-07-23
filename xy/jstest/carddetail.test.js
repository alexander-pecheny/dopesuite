// Tests for carddetail.js's pure exports — the test-card derived title, the
// 4s question stub and the tester summary line. The createCardDetail factory
// binds to the card modal's ~30 compiled-page nodes, so it is exercised in the
// browser, not here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { testTitle, questionStub, testerSummaryLine, nowStamp } from "../web/assets/static/dist/carddetail.js";

test("testTitle: datetime + counts, players and teams tallied separately", () => {
  const desc = JSON.stringify({
    datetime: "2026-07-01 19:00", title: "",
    testers: [
      { text: "А Б", type: "player" },
      { text: "В Г", type: "player" },
      { text: "Сфинксы", type: "team" },
    ],
  });
  assert.equal(testTitle(desc), "🗓️ 2026-07-01 19:00 · 2 игр., 1 ком.");
});

test("testTitle: a session title leads, datetime follows", () => {
  const desc = JSON.stringify({ datetime: "2026-07-01 19:00", title: "Алиев и др.", testers: [] });
  assert.equal(testTitle(desc), "🗓️ Алиев и др. · 2026-07-01 19:00");
});

test("questionStub seeds the five markers and the default author", () => {
  assert.equal(questionStub("Автор А"), "? \n! \n/ \n^ \n@ Автор А");
  assert.equal(questionStub(""), "? \n! \n/ \n^ \n@ ");
});

test("testerSummaryLine terminates with a period; empty list stays empty", () => {
  assert.equal(testerSummaryLine([]), "");
  assert.equal(
    testerSummaryLine([{ text: "А Б", type: "player" }, { text: "В Г", type: "player" }]),
    "Вопросы тестировали: А Б, В Г.",
  );
  assert.equal(
    testerSummaryLine([{ text: "Сфинксы", type: "team" }]),
    "Вопросы тестировали команды: Сфинксы.",
  );
});

test("nowStamp is a parseable ISO timestamp", () => {
  const t = Date.parse(nowStamp());
  assert.ok(Number.isFinite(t) && Math.abs(Date.now() - t) < 5000);
});
