import { expect, test } from "bun:test";
import { formBody, statusText } from "./dist/forms-model.js";
import { isGotQuestions, isLoadable, isSingleLink } from "./dist/nominate-model.js";

test("formBody sends every string field, repeated names included", () => {
  expect(formBody([["like", "1"], ["like", "2"], ["portion", "3"]])).toBe("like=1&like=2&portion=3");
});

test("statusText reads a saved answer, a refusal, and garbage", () => {
  expect(statusText(true, '{"status":"Сохранено."}', "no")).toBe("Сохранено.");
  expect(statusText(false, "Эта порция ещё не открыта.\n", "no")).toBe("Эта порция ещё не открыта.");
  expect(statusText(false, "", "no")).toBe("no");
  expect(statusText(true, "<html>", "no")).toBe("no");
});


test("the source box holds a link to load or a text to parse", () => {
  expect(isGotQuestions("https://gotquestions.online/question/777")).toBe(true);
  expect(isGotQuestions("https://t.me/somechannel/12")).toBe(false);
  expect(isSingleLink("  https://t.me/somechannel/12\n")).toBe(true);
  expect(isSingleLink("Вопрос 1. https://example.com\nОтвет: да")).toBe(false);
  expect(isSingleLink("")).toBe(false);
});

test("a gotquestions link and a telegram post can be loaded, a channel cannot", () => {
  expect(isLoadable("https://gotquestions.online/question/777")).toBe(true);
  expect(isLoadable("https://t.me/omps2026/30")).toBe(true);
  expect(isLoadable("https://t.me/omps2026")).toBe(false);
  expect(isLoadable("https://example.com/a/1")).toBe(false);
});
