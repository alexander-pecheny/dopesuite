import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, fakeNode, installDOM } from "./dom.js";

const p = installDOM(["massOverlay", "massBar", "massRun", "massBody", "massMessage", "massClose"]);
p.node("massOverlay").append(fakeNode("h2", { className: "appearance-modal-title" }));
const { createMassPanel } = await import("../web/assets/static/dist/masspanel.js");

function setup() {
  const board = fakeBoard({
    lists: [{ id: 1, rank: "b", title: "Б" }, { id: 2, rank: "a", title: "А" }],
    cards: [
      { id: 10, listId: 1, kind: "question", rank: "a", desc: "? раз" },
      { id: 11, listId: 1, kind: "question", rank: "b", desc: "? два" },
      { id: 20, listId: 2, kind: "question", rank: "a", desc: "? три" },
    ],
    cardLabels: [{ cardId: 10, labelId: 5, sessionId: null }],
  });
  const forgotten = [];
  const mass = createMassPanel(board, {
    kanban: fakeNode("div"),
    transfer: { moveBoardOptions: async () => [], loadMoveBoard: async () => null, transferCard: async () => 0 },
    forgetCardLabels: (cards) => forgotten.push(...cards.map((c) => c.id)),
    paintLabels() {},
    refreshMenu() {},
  });
  return { board, mass, forgotten };
}

test("mass mode is off until asked, ticks accumulate, and leaving the mode drops them", () => {
  const { board, mass } = setup();
  assert.equal(mass.mode, false);
  mass.panel.open();
  assert.equal(mass.mode, true);
  assert.equal(board.renders, 1);
  mass.toggle(10);
  mass.toggleAll([20, 11]);
  assert.deepEqual([...mass.selected].sort(), [10, 11, 20]);
  mass.toggleAll([20, 11]);
  assert.deepEqual([...mass.selected], [10]);
  mass.setMode(false);
  assert.equal(mass.selected.size, 0);
  mass.renderBar();
  assert.equal(p.node("massBar").hidden, true, "and the bar goes with the mode");
});

test("the bar names the count and offers the actions only once something is ticked; prune drops dead cards", () => {
  const { board, mass } = setup();
  mass.setMode(true);
  mass.renderBar();
  const bar = p.node("massBar");
  assert.equal(bar.hidden, false);
  assert.equal(bar.kids[0].text, "Массовое действие");
  assert.equal(bar.kids[1].kids[0].text, "Отметьте карточки");
  mass.toggle(10);
  mass.toggle(11);
  assert.equal(bar.kids[0].text, "Выбрано: 2 карточки");
  assert.ok(bar.kids[1].kids.length >= 5, "one button per mass action");
  board.state.cards = board.state.cards.filter((c) => c.id !== 11);
  mass.prune();
  assert.deepEqual([...mass.selected], [10]);
  mass.setMode(false);
  mass.renderBar();
  assert.equal(bar.hidden, true);
});

test("«Добавить видевших» adds a pasted list to every ticked card, and «Не видели» takes people off", async () => {
  const { board, mass } = setup();
  board.state.sessions = [{ id: 7, meta: JSON.stringify({ key: "t", title: "Тест", testers: [{ text: "Аня" }] }) }];
  board.state.cardSessions = [{ cardId: 10, sessionId: 7 }];
  mass.setMode(true);
  mass.toggle(10);
  mass.toggle(11);
  const act = (label) => p.node("massBar").kids[1].kids.find((b) => b.text === label);
  act("Добавить видевших").fire("click");
  await new Promise((r) => setTimeout(r, 0));
  const area = p.node("massBody").querySelector("textarea");
  assert.equal(p.node("massRun").disabled, true);
  area.value = "1. Гоша\n2. Аня";
  area.fire("input");
  assert.equal(p.node("massRun").disabled, false);
  p.node("massRun").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  const card = (id) => board.state.cards.find((c) => c.id === id);
  // Аня was already at the test that played card 10.
  assert.deepEqual(JSON.parse(card(10).seen).extra.map((t) => t.text), ["Гоша"]);
  assert.deepEqual(JSON.parse(card(11).seen).extra.map((t) => t.text), ["Гоша", "Аня"]);

  // A finished run leaves nothing ticked.
  assert.equal(mass.selected.size, 0);
  mass.toggle(10);
  act("Не видели").fire("click");
  await new Promise((r) => setTimeout(r, 0));
  const offered = p.node("massBody").querySelectorAll("label").map((l) => l.text);
  assert.deepEqual(offered, ["Аня", "Гоша"], "exactly the people who saw a ticked question");
  const cb = p.node("massBody").querySelectorAll("input").find((i) => i.parentElement.text === "Аня");
  cb.checked = true;
  cb.fire("change");
  p.node("massRun").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(JSON.parse(card(10).seen), { extra: [{ text: "Гоша", type: "player" }], absent: { t: ["Аня"] } });
});
