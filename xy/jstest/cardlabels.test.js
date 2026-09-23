import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["labelPicker", "cardPlayings", "cardSeen", "labelAddRow", "labelAddBtn", "playingAddRow", "playingAddBtn", "seenAddBtn", "newLabelForm", "newLabelName", "newLabelColor", "cardMessage"]);
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
  addRow: p.node("labelAddRow"), addBtn: p.node("labelAddBtn"), playingAddRow: p.node("playingAddRow"), playingAddBtn: p.node("playingAddBtn"), seenAddBtn: p.node("seenAddBtn"),
  newLabelForm: p.node("newLabelForm"), newLabelName: p.node("newLabelName"), newLabelColor: p.node("newLabelColor"), message: p.node("cardMessage"),
}, {
  mustDK: () => ({ key: "K" }),
  openCardId: () => 10,
  copyPlain: async (t) => { copied.push(t); },
  tourPicked: () => new Set(["Аня", "Боря"]), // the tour's Tester List already names Тест А
  createLabel: async (name, color) => ({ id: 5, name, color }),
  loadTimeline: async (id) => { loaded.push(id); },
  paintLabels() {},
});
const card = board.state.cards[0];
// The names in «Видели», one per row; a row is the name, maybe a «вручную», and its button.
const seenNames = () => (p.node("cardSeen").querySelector(".seen-names")?.kids || []).map((row) => row.kids[0].textContent);

const seenRow = (name) => p.node("cardSeen").querySelector(".seen-names").kids.find((row) => row.kids[0].textContent === name);

test("the pickers show the author's labels, each Playing with its scoped labels, and «Видели» names only the extras", () => {
  labels.render(card);
  assert.deepEqual(p.node("labelPicker").querySelectorAll(".label-pick-name").map((n) => n.textContent), ["взяли"]);
  assert.deepEqual(p.node("cardPlayings").querySelectorAll(".playing-name").map((n) => n.textContent), ["Тест А", "Тест Б"]);
  assert.deepEqual(p.node("cardPlayings").querySelectorAll(".label-pick-name").map((n) => n.textContent), ["снять"]);
  assert.equal(p.node("cardSeen").hidden, false);
  const seen = p.node("cardSeen").querySelector(".seen-names").textContent;
  assert.ok(seen.includes("Вера") && !seen.includes("Аня"), `only the extra tester is named: ${seen}`);
  // One person per line (#72).
  assert.deepEqual(seenNames(), ["Вера"]);
  assert.ok(p.node("cardSeen").querySelector(".seen-label").textContent.startsWith("Видели вопрос, кроме общих"));
});

test("«Показать всех тестеров» brings the common ones back dimmed, and the copy follows the line", async () => {
  labels.render(card);
  const cb = p.node("cardSeen").querySelector("input");
  assert.equal(cb.checked, false);
  cb.checked = true;
  cb.fire("change");
  const seen = p.node("cardSeen").querySelector(".seen-names");
  assert.deepEqual(seenNames(), ["Аня", "Боря", "Вера"]);
  assert.deepEqual(seen.querySelectorAll(".seen-common").map((n) => n.textContent), ["Аня", "Боря"]);
  assert.equal(p.node("cardSeen").querySelector(".seen-label").textContent, "Видели: ");
  p.node("cardSeen").querySelector("button").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(copied.at(-1), "Видели: Аня, Боря, Вера");
  // A peek, not a preference: the next card opened starts folded again.
  labels.render(card);
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

// Fold the common testers away again: the peek above is keyed to this card.
function folded() {
  labels.render(card);
  const cb = p.node("cardSeen").querySelector("input");
  if (cb && cb.checked) { cb.checked = false; cb.fire("change"); }
}

test("× on a tester marks them absent from that test, and ↺ takes the absence back", async () => {
  folded();
  seenRow("Вера").querySelector("button").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(JSON.parse(card.seen), { absent: { "k-Тест Б": ["Вера"] } });
  assert.deepEqual(board.seenOf(card.id).map((t) => t.text), ["Аня", "Боря"]);
  // Still listed, struck out, so it can be undone.
  const row = seenRow("Вера");
  assert.ok(row.kids[0].matches(".seen-absent"));
  row.querySelector("button").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(card.seen, null);
  assert.deepEqual(seenNames(), ["Вера"]);
});

test("the add field takes a name on Enter and a pasted list, and says who was added by hand", async () => {
  folded();
  p.node("seenAddBtn").fire("click");
  // The field sits at the foot of «Видели», under the names it adds to.
  const inp = p.node("cardSeen").querySelector("input[type=text]");
  assert.ok(inp, "the field opens inside the section");
  inp.value = "Гоша";
  inp.fire("keydown", { key: "Enter" });
  await new Promise((r) => setTimeout(r, 5));
  assert.equal(inp.value, "", "cleared for the next name");
  inp.fire("paste", { clipboardData: { getData: () => "1. Дина\n2. Аня" } });
  await new Promise((r) => setTimeout(r, 5));
  // Аня already saw it at Тест А, so she is not added a second time.
  assert.deepEqual(JSON.parse(card.seen).extra.map((t) => t.text), ["Гоша", "Дина"]);
  assert.deepEqual(seenNames(), ["Вера", "Гоша", "Дина"]);
  assert.equal(seenRow("Гоша").kids[1].textContent, "вручную");
  // × on a hand-added person takes them off rather than marking an absence.
  seenRow("Гоша").querySelector("button").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(JSON.parse(card.seen), { extra: [{ text: "Дина", type: "player" }] });
  assert.ok(p.node("cardSeen").querySelector("input[type=text]"), "the field stays open between adds");
  inp.fire("keydown", { key: "Escape", stopPropagation() {} });
  assert.equal(p.node("cardSeen").querySelector("input[type=text]"), null);
});
