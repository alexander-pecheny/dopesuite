import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["labelsEditOverlay", "labelsEditor", "labelsEditMessage", "labelsEditClose"]);
p.node("labelsEditOverlay").hidden = true;
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;
const { createLabelsEditor, sortLabels } = await import("../web/assets/static/dist/labelsedit.js");

test("sortLabels: most recently used first, never-used last, those alphabetically descending", () => {
  const labels = [{ id: 1, name: "б" }, { id: 2, name: "а" }, { id: 3, name: "в" }, { id: 4, name: "г" }];
  const cardLabels = [{ cardId: 10, labelId: 1 }, { cardId: 30, labelId: 2 }, { cardId: 20, labelId: 1 }];
  assert.deepEqual(sortLabels(labels, cardLabels).map((l) => l.name), ["а", "б", "г", "в"]);
});

test("the editor lists every label with its usage; Добавить creates one and Готово commits a rename", async () => {
  const board = fakeBoard({
    labels: [{ id: 1, name: "взяли", color: "red" }, { id: 2, name: "снять", color: "blue" }],
    cards: [{ id: 10, listId: 1, kind: "question", rank: "a", desc: "?" }],
    cardLabels: [{ cardId: 10, labelId: 1, sessionId: null }],
  });
  const editor = createLabelsEditor(board);
  editor.panel.open();
  const rows = p.node("labelsEditor").querySelectorAll(".sess-row");
  assert.equal(rows.length, 3, "the create row and one per label");
  assert.deepEqual(rows.slice(1).map((r) => r.querySelector("input").value), ["взяли", "снять"]);
  assert.equal(rows[1].querySelector(".sess-meta").text, "1 карт.");
  // Create through the row: name typed, Добавить pressed.
  rows[0].querySelector("input").value = "проверить";
  rows[0].querySelectorAll("button").find((b) => b.text === "Добавить").fire("click");
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(board.writes.map((w) => [w[0], w[2], w[3].name_enc]), [["create", "/api/boards/7/labels", "e:проверить"]]);
  assert.equal(board.state.labels.length, 3);
  // Rename in place; leaving (Готово → confirm gate) writes it.
  const renamed = p.node("labelsEditor").querySelectorAll(".sess-row")[1].querySelector("input");
  renamed.value = "взяли!";
  await new Promise((r) => setTimeout(r, 0));
  p.node("labelsEditClose").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(board.writes.slice(1).map((w) => [w[0], w[2], w[3].name_enc]), [["patch", "/api/labels/1", "e:взяли!"]]);
  assert.equal(p.node("labelsEditOverlay").hidden, true);
});
