import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

installDOM([]);
const { renderPreviewCard, renderRich, fillPreviewImages } = await import("../web/assets/static/dist/preview.js");

const q = { id: 1, kind: "question", desc: "? Что _это_?\n! То.\n= Сё.\n^ - раз\n- два\n@ Аня" };

test("a question renders as the docx would: numbered label, fields in order, sources as a numbered list", () => {
  const node = renderPreviewCard(q, "3", new Map(), false);
  assert.equal(node.tag, "article");
  assert.equal(node.dataset.cardId, 1);
  const labels = node.querySelectorAll(".pv-label").map((n) => n.textContent);
  assert.deepEqual(labels, ["Вопрос 3. ", "Ответ: ", "Зачёт: ", "Источники: ", "Автор: "]);
  assert.deepEqual(node.querySelectorAll(".pv-list-num").map((n) => n.textContent), ["1.", "2."]);
  assert.ok(node.querySelectorAll(".pv-small").length >= 2, "sources and authors are set small");
  assert.ok(!node.querySelectorAll(".pv-edit").length, "no ✏️ unless a builder is passed");
});

test("the ✏️ builder is called for the question line when given", () => {
  let asked = null;
  const node = renderPreviewCard(q, null, new Map(), false, (card) => { asked = card.id; return document.createElement("button"); });
  assert.equal(asked, 1);
  assert.equal(node.querySelectorAll(".pv-q-text")[0].kids[0].tag, "button");
});

test("a heading card is a heading; a meta card a paragraph; numbering directives vanish", () => {
  const node = renderPreviewCard({ id: 2, kind: "meta", desc: "### Тур 1\n#DATE 2026\n№ 5" }, null, new Map(), false);
  assert.equal(node.tag, "div");
  assert.deepEqual(node.querySelectorAll("h2").map((n) => n.textContent), ["Тур 1"]);
  assert.ok(!node.textContent.includes("№ 5"), "the numbering directive is not shown");
});

test("renderRich: marks, a link, a screen alternative in print vs screen mode, an image placeholder that fills in", () => {
  const frag = renderRich("Текст _курсив_ https://x.test/", new Map(), {});
  assert.ok(frag.querySelectorAll(".pv-link").length === 1, "a bare URL is a link");
  assert.ok(frag.querySelectorAll("span").some((n) => n.style?.fontStyle === "italic"));
  const screen = renderRich("(screen печать|экран)", new Map(), { accents: true, brackets: true });
  assert.equal(screen.textContent, "экран");
  assert.equal(renderRich("(screen печать|экран)", new Map(), {}).textContent, "печать");
  const root = document.createElement("div");
  root.append(renderRich("(img pic.png)", new Map(), {}));
  assert.equal(root.querySelectorAll(".pv-img-missing").length, 1);
  fillPreviewImages(root, new Map([["pic.png", "blob:1"]]));
  assert.equal(root.querySelectorAll(".pv-img-missing").length, 0);
  assert.equal(root.querySelectorAll(".pv-img").length, 1);
});
