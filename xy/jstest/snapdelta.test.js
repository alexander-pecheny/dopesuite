import { test } from "node:test";
import assert from "node:assert/strict";
import { xySnapDelta } from "../web/assets/static/dist/snapdelta.js";

const { mergeSnapshot, fetchSnapshot } = xySnapDelta;

const card = (id, list, rank, description) => ({ id, list_id: list, kind: "question", description, rank, created_at: "t" });

const base = () => ({
  id: 7, rev: 10, name: "Доска", lists: [{ id: 1 }, { id: 2 }],
  cards: [card(1, 1, "a", "один"), card(2, 1, "b", "два"), card(3, 2, "c", "три")],
  card_labels: [{ card_id: 1, label_id: 5 }, { card_id: 2, label_id: 5 }],
  card_sessions: [{ card_id: 3, session_id: 9 }],
  votes: [],
  unread: { 1: { content: true } },
});

test("a delta replaces changed cards, moves the ones that only moved and drops deleted ones", () => {
  const delta = {
    id: 7, rev: 14, since: 10, name: "Доска", lists: [{ id: 1 }, { id: 2 }],
    cards: [card(1, 1, "a", "один, исправлен"), { id: 3, list_id: 1, rank: "d", same: true }, card(4, 2, "e", "четыре")],
    gone_cards: [2],
    card_labels: [{ card_id: 4, label_id: 5 }],
    card_sessions: [],
    votes: [],
    unread: {},
  };
  const merged = mergeSnapshot(base(), delta);
  assert.deepEqual(merged.cards.map((c) => [c.id, c.list_id, c.rank, c.description]), [
    [1, 1, "a", "один, исправлен"],
    [3, 1, "d", "три"],
    [4, 2, "e", "четыре"],
  ]);
  // Card 1 changed and came with no labels, so it has none now; card 2 is gone.
  assert.deepEqual(merged.card_labels, [{ card_id: 4, label_id: 5 }]);
  // Card 3 moved, which counts as a change, and its Playings came empty.
  assert.deepEqual(merged.card_sessions, []);
  assert.equal(merged.rev, 14);
  assert.deepEqual(merged.unread, {});
  assert.equal("since" in merged, false);
  assert.equal("gone_cards" in merged, false);
});

test("a card a delta keeps must be in the base, and the base must be the one asked about", () => {
  assert.throws(() => mergeSnapshot(base(), { rev: 11, since: 10, cards: [{ id: 99, list_id: 1, rank: "z", same: true }] }), /does not have/);
  assert.throws(() => mergeSnapshot(base(), { rev: 11, since: 9, cards: [] }), /onto rev 10/);
});

test("a full snapshot is taken as it is", () => {
  const full = { rev: 3, cards: [card(8, 1, "a", "x")] };
  assert.equal(mergeSnapshot(base(), full), full);
});

function io(responses, stored) {
  const calls = { urls: [], saved: undefined };
  return {
    calls,
    fetchJSON: async (url) => { calls.urls.push(url); return structuredClone(responses[url]); },
    getBase: async () => (stored ? structuredClone(stored) : undefined),
    putBase: async (_id, snap) => { calls.saved = snap; },
  };
}

test("fetchSnapshot asks for a delta when it holds a base, and keeps the merge as the next base", async () => {
  const delta = { ...base(), rev: 12, since: 10, cards: [{ id: 2, list_id: 2, rank: "z", same: true }], gone_cards: [], card_labels: [], card_sessions: [], votes: [] };
  const net = io({ "/api/boards/7?since=10": delta }, base());
  const snap = await fetchSnapshot(7, net);
  assert.deepEqual(net.calls.urls, ["/api/boards/7?since=10"]);
  assert.equal(snap.cards.find((c) => c.id === 2).description, "два");
  assert.equal(snap.cards.find((c) => c.id === 2).list_id, 2);
  assert.equal(net.calls.saved, snap);
});

test("fetchSnapshot reads the whole board on a first open, and again when a delta does not fit", async () => {
  const full = { ...base(), rev: 12 };
  const first = io({ "/api/boards/7": full }, undefined);
  assert.deepEqual((await fetchSnapshot(7, first)).rev, 12);
  assert.deepEqual(first.calls.urls, ["/api/boards/7"]);

  const misfit = { rev: 12, since: 10, cards: [{ id: 99, list_id: 1, rank: "z", same: true }] };
  const net = io({ "/api/boards/7?since=10": misfit, "/api/boards/7": full }, base());
  assert.equal((await fetchSnapshot(7, net)).rev, 12);
  assert.deepEqual(net.calls.urls, ["/api/boards/7?since=10", "/api/boards/7"]);
});
