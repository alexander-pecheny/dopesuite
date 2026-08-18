import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["replaceOverlay", "replaceScope", "replaceFrom", "replaceTo", "replaceCase", "replaceHits", "replacePage", "replacePrev", "replaceNext", "replaceRun", "replaceClose", "replaceMessage"]);
p.node("replaceOverlay").hidden = true;
p.node("replaceCase").checked = true;
const { createReplacePanel } = await import("../web/assets/static/dist/replace.js");

function setup() {
  const board = fakeBoard({
    lists: [{ id: 1, rank: "b", title: "Второй", groupId: null }, { id: 2, rank: "a", title: "Первый", groupId: null }],
    cards: [
      { id: 10, listId: 1, kind: "question", rank: "a", desc: "? Кот и кот\n! кот" },
      { id: 20, listId: 2, kind: "question", rank: "a", desc: "? Собака" },
      { id: 21, listId: 2, kind: "question", rank: "b", desc: "? Кот" },
    ],
  });
  for (const id of ["replaceFrom", "replaceTo"]) p.node(id).value = "";
  const applied = [];
  const panel = createReplacePanel(board, { apply: async (changes) => { applied.push(...changes.map((c) => [c.card.id, c.desc])); } });
  return { board, panel, applied };
}

test("the plan ticks every occurrence, in board order, and the run rewrites only what is still as planned", async () => {
  const { board, panel, applied } = setup();
  panel.open();
  assert.equal(p.node("replaceOverlay").hidden, false);
  assert.deepEqual(p.node("replaceScope").kids.map((o) => o.attrs.value), ["board", "list:2", "list:1"]);
  p.node("replaceFrom").value = "Кот";
  p.node("replaceCase").fire("input");
  const run = p.node("replaceRun");
  assert.equal(run.text, "Удалить 2 в 2 карточки", "case-sensitive: «кот» twice lower-cased does not count");
  assert.deepEqual(p.node("replaceHits").kids.filter((k) => k.className === "replace-card").map((k) => k.kids[1].text), ["Кот", "Кот и кот"], "the first list first");
  p.node("replaceTo").value = "Пёс";
  p.node("replaceTo").fire("input");
  assert.equal(run.text, "Заменить 2 в 2 карточки");
  // A co-author's edit lands on one card between plan and run: it is skipped and reported.
  board.state.cards[2].desc = "? Кот-кот";
  run.fire("click");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(applied, [[10, "? Пёс и кот\n! кот"]]);
  assert.match(p.node("replaceMessage").textContent, /Готово: 1 карточка\. 1 карточка изменил/);
});

test("nothing found says so; the scope select narrows the search", () => {
  const { panel } = setup();
  panel.open();
  p.node("replaceScope").value = "list:2";
  p.node("replaceFrom").value = "Собака";
  p.node("replaceScope").fire("input");
  assert.equal(p.node("replaceRun").text, "Удалить 1 в 1 карточка");
  p.node("replaceFrom").value = "Слон";
  p.node("replaceScope").fire("input");
  assert.equal(p.node("replaceMessage").textContent, "Ничего не найдено.");
  assert.equal(p.node("replaceRun").disabled, true);
});
