import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

const p = installDOM(["moveListOverlay", "moveListBoard", "moveListPos", "moveListMessage", "moveListCopyBtn", "moveListMoveBtn", "moveListClose"]);
p.node("moveListOverlay").hidden = true;
globalThis.fetch = async (url) => ({ ok: true, status: 200, json: async () => (String(url).endsWith("/api/boards") ? [{ id: 7, name: "Доска", schema_version: 2 }, { id: 8, name: "Другая", schema_version: 2 }] : []), text: async () => "" });
const { createMoveListPanel } = await import("../web/assets/static/dist/movelist.js");

const settle = () => new Promise((r) => setTimeout(r, 5));

// One panel: its listeners bind to the page's nodes once.
const { board, panel } = setup();
function setup() {
  const board = fakeBoard({
    lists: [{ id: 1, title: "Тур 1", rank: "a0", groupId: null }, { id: 2, title: "Тур 2", rank: "a1", groupId: null }, { id: 3, title: "Тур 3", rank: "a2", groupId: null }],
    cards: [{ id: 10, listId: 1, kind: "question", rank: "a0", desc: "?" }],
  });
  const ctxOf = (bid) => ({ boardId: bid, dk: { key: "K" }, lists: bid === 7 ? board.state.lists : [{ id: 20, title: "Чужой", rank: "a0" }], cardsByList: new Map(), labels: [], sessions: [], name: "" });
  const panel = createMoveListPanel(board, {
    loadMoveBoard: async (bid) => ctxOf(bid),
    transferCard: async () => 0,
  });
  return { board, panel };
}

test("the dialog offers every board (this one first and selected) and one slot per list of the chosen board", async () => {
  panel.open({ list: board.state.lists[0], grouped: false, group: null, lists: [], cards: [], title: "Тур 1" });
  await settle();
  const boards = p.node("moveListBoard").kids.map((o) => o.text);
  assert.deepEqual(boards, ["Доска (эта доска)", "Другая"]);
  assert.equal(p.node("moveListBoard").value, "7");
  assert.deepEqual(p.node("moveListPos").kids.map((o) => o.text), ["в конец", "позиция 1", "позиция 2"], "the moving list itself is not a slot");
  p.node("moveListBoard").value = "8";
  p.node("moveListBoard").fire("change");
  await settle();
  assert.deepEqual(p.node("moveListPos").kids.map((o) => o.text), ["в конец", "позиция 1"]);
});

test("a move within the board is a re-rank through the outbox and closes the dialog", async () => {
  p.node("moveListOverlay").hidden = true;
  panel.open({ list: board.state.lists[0], grouped: false, group: null, lists: [], cards: [], title: "Тур 1" });
  await settle();
  p.node("moveListPos").value = "end";
  p.node("moveListMoveBtn").fire("click");
  await settle();
  assert.deepEqual(board.writes.map((w) => [w[0], w[1], w[2]]), [["patch", "patchList", "/api/lists/1"]]);
  assert.ok(board.writes[0][3].rank > "a2", "ranked after the last list");
  assert.equal(board.state.lists[0].rank, board.writes[0][3].rank);
  assert.equal(board.renders, 1);
  assert.equal(p.node("moveListOverlay").hidden, true);
});
