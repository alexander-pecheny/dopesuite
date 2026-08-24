import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, installDOM } from "./dom.js";

installDOM(["accentOverlay", "accentPicks", "accentRun"]);
const { createRewrites } = await import("../web/assets/static/dist/rewrites.js");
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;

test("collect keeps only cards a transform changes; apply patches each with a desc_edit entry", async () => {
  const board = fakeBoard({ cards: [
    { id: 1, listId: 1, kind: "question", rank: "a", desc: "? Раз" },
    { id: 2, listId: 1, kind: "question", rank: "b", desc: "? Два" },
  ] });
  const rw = createRewrites(board);
  const changes = rw.collect((c) => (c.id === 2 ? c.desc + "!" : null));
  assert.deepEqual(changes.map((ch) => [ch.card.id, ch.desc]), [[2, "? Два!"]]);
  await rw.apply(changes);
  assert.deepEqual(board.writes.map((w) => [w[0], w[2], w[3].description_enc]), [["patch", "/api/cards/2", "e:? Два!"]]);
  assert.equal(JSON.parse(board.writes[0][3].desc_event_enc.slice(2)).before, "? Два");
  assert.equal(board.state.cards[1].desc, "? Два!");
  assert.equal(board.renders, 1);
});

test("the ☰ offers the typography pass", () => {
  const rw = createRewrites(fakeBoard());
  assert.deepEqual([rw.typograph.menu, rw.typograph.id], ["board", "typograph"]);
  assert.equal(rw.fixTrello, undefined, "the Trello clean-up is the importer's job now");
});

// A test card's description is JSON, not 4s: a board-wide rewrite that touched
// it would turn its quotes into «ёлочки» and leave parseSession nothing to read.
test("a board-wide rewrite skips cards that are not questions", () => {
  const board = fakeBoard({ cards: [
    { id: 1, listId: 1, kind: "question", rank: "a", desc: "? Раз" },
    { id: 2, listId: 1, kind: "test", rank: "b", desc: '{"datetime":"20.07.2026","players":[]}' },
    { id: 3, listId: 1, kind: "heading", rank: "c", desc: "## Тур" },
  ] });
  const changes = createRewrites(board).collect((c) => c.desc + "!");
  assert.deepEqual(changes.map((ch) => ch.card.id), [1]);
});
