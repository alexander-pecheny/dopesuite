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

test("the ☰ offers the two rewrites, Trello clean-up first", () => {
  const rw = createRewrites(fakeBoard());
  assert.deepEqual(rw.panels.map((p) => [p.menu, p.id]), [["board", "fix-trello"], ["board", "typograph"]]);
});
