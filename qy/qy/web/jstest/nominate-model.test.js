import { expect, test } from "bun:test";
import { fourS, imageLinks, sourceItems } from "./dist/nominate-model.js";

const empty = { text: "", handout: "", answer: "", zachet: "", nezachet: "", comment: "", source: "", author: "" };

test("numbered sources are a list, anything else one source", () => {
  expect(sourceItems("1. https://a\n2. https://b")).toEqual(["https://a", "https://b"]);
  expect(sourceItems("Жан Кокто. Орфей")).toEqual(["Жан Кокто. Орфей"]);
  expect(sourceItems("1. https://a\nи ещё строка")).toEqual(["1. https://a\nи ещё строка"]);
});

test("the fields become the 4s card chgksuite's renderer reads", () => {
  const desc = fourS({ ...empty, text: "Назовите ИКС.", answer: "цирк", source: "1. https://a\n2. https://b", author: "!!Авторка Юлия Дидбаридзе" });
  expect(desc).toBe("? Назовите ИКС.\n! цирк\n^\n- https://a\n- https://b\n@ !!Авторка Юлия Дидбаридзе");
  expect(fourS({ ...empty, text: "Т.", answer: "О.", source: "https://a", author: "Иван Петров" })).toBe("? Т.\n! О.\n^ https://a\n@ Иван Петров");
});

test("a picture's link in the handout is an (img …) the preview shows", () => {
  const desc = fourS({ ...empty, text: "Т.", answer: "О.", handout: "https://cdn.example/pic.jpg" });
  expect(desc).toContain("(img https://cdn.example/pic.jpg)");
  expect(imageLinks("подпись\nhttps://cdn.example/pic.jpg")).toEqual(["https://cdn.example/pic.jpg"]);
});
