import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, fakeNode, installDOM } from "./dom.js";

const p = installDOM(["massOverlay", "massBar", "massRun", "massBody", "massMessage", "massClose"]);
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
  });
  return { board, mass, forgotten };
}

test("mass mode is off until asked, ticks accumulate, and leaving the mode drops them", () => {
  const { board, mass } = setup();
  assert.equal(mass.mode, false);
  mass.panel.open();
  assert.equal(mass.mode, true);
  assert.equal(p.doc.body.classes.has("mass-mode"), true);
  assert.equal(board.renders, 1);
  mass.toggle(10);
  mass.toggleAll([20, 11]);
  assert.deepEqual([...mass.selected].sort(), [10, 11, 20]);
  mass.toggleAll([20, 11]);
  assert.deepEqual([...mass.selected], [10]);
  mass.setMode(false);
  assert.equal(mass.selected.size, 0);
  assert.equal(p.doc.body.classes.has("mass-mode"), false);
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
