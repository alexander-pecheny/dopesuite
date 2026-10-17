import { expect, test } from "bun:test";
import { formBody, statusText } from "./dist/forms-model.js";

test("formBody sends every string field, repeated names included", () => {
  expect(formBody([["like", "1"], ["like", "2"], ["portion", "3"]])).toBe("like=1&like=2&portion=3");
});

test("statusText reads a saved answer, a refusal, and garbage", () => {
  expect(statusText(true, '{"status":"Сохранено."}', "no")).toBe("Сохранено.");
  expect(statusText(false, "Эта порция ещё не открыта.\n", "no")).toBe("Эта порция ещё не открыта.");
  expect(statusText(false, "", "no")).toBe("no");
  expect(statusText(true, "<html>", "no")).toBe("no");
});
