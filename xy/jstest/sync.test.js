import { test } from "node:test";
import assert from "node:assert/strict";
import { xySync } from "../web/assets/static/sync.js";

const { _substituteValue, _substitutePath, _applyOpToSnapshot, _pendingTimeline } = xySync;

test("substituteValue rewrites negative ids, leaves the rest", () => {
  const idmap = { "-1": 10, "-2": 20 };
  assert.equal(_substituteValue(-1, idmap), 10);
  assert.equal(_substituteValue(-2, idmap), 20);
  assert.equal(_substituteValue(-3, idmap), -3); // unmapped temp id untouched
  assert.equal(_substituteValue(5, idmap), 5); // positive id untouched
  assert.deepEqual(_substituteValue({ list_id: -1, label_ids: [-2, 5] }, idmap), { list_id: 10, label_ids: [20, 5] });
  // strings (e.g. base64 envelopes, ranks) pass through
  assert.equal(_substituteValue("a0", idmap), "a0");
});

test("substitutePath rewrites only negative-id segments", () => {
  const idmapStr = { "-5": 42 };
  assert.equal(_substitutePath("/api/cards/-5/labels", idmapStr), "/api/cards/42/labels");
  assert.equal(_substitutePath("/api/cards/7/labels", idmapStr), "/api/cards/7/labels");
  assert.equal(_substitutePath("/api/cards/-9", idmapStr), "/api/cards/-9"); // unmapped left alone
});

test("applyOpToSnapshot: create list and card", () => {
  const snap = { lists: [], cards: [], labels: [], card_labels: {} };
  _applyOpToSnapshot(snap, { kind: "createList", path: "/api/boards/1/lists", body: { title_enc: "T", rank: "a0", type: "normal" } }, -1);
  assert.deepEqual(snap.lists, [{ id: -1, type: "normal", title_enc: "T", rank: "a0" }]);
  _applyOpToSnapshot(snap, { kind: "createCard", path: "/api/lists/-1/cards", body: { description_enc: "D", rank: "a1", kind: "question" } }, -2);
  assert.deepEqual(snap.cards, [{ id: -2, list_id: -1, kind: "question", description_enc: "D", rank: "a1" }]);
});

test("applyOpToSnapshot: patch, move, delete, labels", () => {
  const snap = {
    lists: [{ id: 1, type: "normal", title_enc: "L1", rank: "a0" }, { id: 2, type: "normal", title_enc: "L2", rank: "a1" }],
    cards: [{ id: 5, list_id: 1, kind: "normal", description_enc: "D", rank: "a0" }],
    labels: [{ id: 9, name_enc: "N", color_enc: "C", kind: "normal" }],
    card_labels: { 5: [9] },
  };
  _applyOpToSnapshot(snap, { kind: "patchCard", path: "/api/cards/5", body: { description_enc: "D2", list_id: 2 } });
  assert.equal(snap.cards[0].description_enc, "D2");
  assert.equal(snap.cards[0].list_id, 2);
  _applyOpToSnapshot(snap, { kind: "setCardLabels", path: "/api/cards/5/labels", body: { label_ids: [] } });
  assert.deepEqual(snap.card_labels[5], []);
  _applyOpToSnapshot(snap, { kind: "deleteLabel", path: "/api/labels/9", body: {} });
  assert.equal(snap.labels.length, 0);
  _applyOpToSnapshot(snap, { kind: "deleteCard", path: "/api/cards/5", body: {} });
  assert.equal(snap.cards.length, 0);
  assert.equal(snap.card_labels[5], undefined);
});

test("applyOpToSnapshot: deleteList removes its cards", () => {
  const snap = {
    lists: [{ id: 1 }, { id: 2 }],
    cards: [{ id: 5, list_id: 1 }, { id: 6, list_id: 2 }],
    labels: [], card_labels: {},
  };
  _applyOpToSnapshot(snap, { kind: "deleteList", path: "/api/lists/1", body: {} });
  assert.deepEqual(snap.lists.map((l) => l.id), [2]);
  assert.deepEqual(snap.cards.map((c) => c.id), [6]);
});

test("pendingTimeline synthesizes per-card events from ops", () => {
  const ops = [
    { kind: "comment", path: "/api/cards/5/comments", body: { payload_enc: "P1" }, ts: "t1" },
    { kind: "patchCard", path: "/api/cards/5", body: { description_enc: "x", desc_event_enc: "DE" }, ts: "t2" },
    { kind: "setCardLabels", path: "/api/cards/5/labels", body: { label_ids: [1], events: [{ type: "label_add", payload_enc: "LA" }] }, ts: "t3" },
    { kind: "comment", path: "/api/cards/6/comments", body: { payload_enc: "OTHER" }, ts: "t4" },
  ];
  const tl = _pendingTimeline(ops, 5);
  assert.deepEqual(tl.map((e) => [e.type, e.payload_enc]), [
    ["comment", "P1"],
    ["desc_edit", "DE"],
    ["label_add", "LA"],
  ]);
  // ids are negative & unique
  assert.ok(tl.every((e) => e.id < 0));
  assert.equal(new Set(tl.map((e) => e.id)).size, tl.length);
});

test("end-to-end: a queued card op remaps after its list create resolves", () => {
  // Offline: create list (temp -1), then create a card in list -1.
  const idmap = {}; // numeric keys
  const idmapStr = {}; // negative-string keys for paths
  // list create resolves to real id 10
  idmap[-1] = 10;
  idmapStr["-1"] = 10;
  const cardOp = { method: "POST", path: "/api/lists/-1/cards", body: { description_enc: "D", rank: "a0", kind: "normal" } };
  assert.equal(_substitutePath(cardOp.path, idmapStr), "/api/lists/10/cards");
  assert.deepEqual(_substituteValue(cardOp.body, idmap), { description_enc: "D", rank: "a0", kind: "normal" });
});
