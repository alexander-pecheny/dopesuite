import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["labelPicker", "cardPlayings", "cardSeen", "labelAddRow", "labelAddBtn", "playingAddRow", "playingAddBtn", "newLabelForm", "newLabelName", "newLabelColor", "cardMessage"]);
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "enc:" + s;
const { createCardLabels } = await import("../web/assets/static/dist/cardlabels.js");

const meta = (title, testers) => JSON.stringify({ key: "k-" + title, title, date: "2026-08-01", testers: testers.map((t) => ({ text: t })) });
const board = fakeBoard({
  lists: [{ id: 1, title: "Тур 1", rank: "a0", groupId: null }],
  cards: [{ id: 10, listId: 1, kind: "question", rank: "a0", desc: "?" }],
  labels: [{ id: 3, name: "взяли", color: "#0f0" }, { id: 4, name: "снять", color: "#f00" }],
  cardLabels: [{ cardId: 10, labelId: 3, sessionId: null }, { cardId: 10, labelId: 4, sessionId: 7 }],
  cardSessions: [{ cardId: 10, sessionId: 7 }, { cardId: 10, sessionId: 8 }],
  sessions: [{ id: 7, meta: meta("Тест А", ["Аня", "Боря"]) }, { id: 8, meta: meta("Тест Б", ["Вера"]) }],
});
const copied = [];
const loaded = [];
const labels = createCardLabels(board, {
  picker: p.node("labelPicker"), playings: p.node("cardPlayings"), seen: p.node("cardSeen"),
  addRow: p.node("labelAddRow"), addBtn: p.node("labelAddBtn"), playingAddRow: p.node("playingAddRow"), playingAddBtn: p.node("playingAddBtn"),
  newLabelForm: p.node("newLabelForm"), newLabelName: p.node("newLabelName"), newLabelColor: p.node("newLabelColor"), message: p.node("cardMessage"),
}, {
  mustDK: () => ({ key: "K" }),
  openCardId: () => 10,
  copyPlain: async (t) => { copied.push(t); },
  tourPicked: () => new Set([7]), // the tour's Tester List already names Тест А
  createLabel: async (name, color) => ({ id: 5, name, color }),
  loadTimeline: async (id) => { loaded.push(id); },
  paintLabels() {},
});
const card = board.state.cards[0];

test("the pickers show the author's labels, each Playing with its scoped labels, and «Видели» names only the extras", () => {
  labels.render(card);
  assert.deepEqual(p.node("labelPicker").querySelectorAll(".label-pick-name").map((n) => n.textContent), ["взяли"]);
  assert.deepEqual(p.node("cardPlayings").querySelectorAll(".playing-name").map((n) => n.textContent), ["Тест А", "Тест Б"]);
  assert.deepEqual(p.node("cardPlayings").querySelectorAll(".label-pick-name").map((n) => n.textContent), ["снять"]);
  assert.equal(p.node("cardSeen").hidden, false);
  const seen = p.node("cardSeen").querySelector(".seen-names").textContent;
  assert.ok(seen.includes("Вера") && !seen.includes("Аня"), `only the extra tester is named: ${seen}`);
  assert.ok(p.node("cardSeen").querySelector(".seen-label").textContent.startsWith("Видели вопрос, кроме общих"));
});

test("removing a label sends the card's whole remaining set and reloads the лента", async () => {
  labels.render(card);
  const x = p.node("labelPicker").querySelector(".label-pick-x");
  x.fire("click");
  await new Promise((r) => setTimeout(r, 5));
  const w = board.writes.at(-1);
  assert.equal(w[2], "/api/cards/10/labels");
  assert.deepEqual(w[3].labels, [{ label_id: 4, session_id: 7 }]);
  assert.equal(w[3].events[0].type, "label_remove");
  assert.deepEqual(board.state.cardLabels, [{ cardId: 10, labelId: 4, sessionId: 7 }]);
  assert.deepEqual(loaded, [10]);
});

test("the add-label popup offers what this scope lacks, and picking one writes the set", async () => {
  labels.render(card);
  p.node("labelAddBtn").fire("click");
  const popup = p.node("labelAddRow").querySelector(".label-add-popup");
  assert.ok(popup, "the popup mounts under the add row");
  assert.deepEqual(popup.querySelectorAll(".label-add-name").map((n) => n.textContent).sort(), ["взяли", "снять"]);
  const pick = popup.querySelectorAll(".label-add-item").find((b) => b.textContent.includes("взяли"));
  assert.ok(popup.querySelectorAll("#newLabelForm").length || popup.kids.includes(p.node("newLabelForm")), "the create-label form sits at the foot");
  pick.fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(board.writes.at(-1)[3].labels, [{ label_id: 4, session_id: 7 }, { label_id: 3, session_id: null }]);
});
