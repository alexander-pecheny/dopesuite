// Tests for timeline.js's pure decision helpers — author resolution, reply
// counting/ordering, feed ordering, the stored display preferences, the выписки
// filter and the full-diff pane split. The createTimeline factory itself binds
// to ~20 compiled-page nodes, so it is exercised in the browser, not here.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  eventAuthor, replyCountsOf, orderThreadReplies, orderFeedEvents,
  feedOrderOf, diffViewOf, excerptComments, fullDiffSides,
} from "../web/assets/static/dist/timeline.js";

const me = { user_id: 1, username: "ya" };
const names = { 1: "Я", 2: "Вася" };

test("eventAuthor resolves via the members roster first", () => {
  assert.equal(eventAuthor({ author_user_id: 2 }, me, names), "Вася");
});

test("eventAuthor falls back to me.username when the roster misses me", () => {
  assert.equal(eventAuthor({ author_user_id: 1 }, me, {}), "ya");
});

test("eventAuthor shows #id for an unknown member", () => {
  assert.equal(eventAuthor({ author_user_id: 5 }, me, names), "#5");
});

test("eventAuthor attributes pending (authorless) events to me", () => {
  assert.equal(eventAuthor({ author_user_id: null }, me, names), "Я");
});

test("eventAuthor is blank with no author and no me", () => {
  assert.equal(eventAuthor({ author_user_id: null }, null, names), "");
});

test("replyCountsOf counts replies per root, pending ones included", () => {
  const events = [
    { id: 10, reply_to_id: null },
    { id: 11, reply_to_id: 10 },
    { id: 12, reply_to_id: 10 },
    { id: -1, reply_to_id: 10 }, // queued offline
    { id: 13, reply_to_id: 11 },
  ];
  const counts = replyCountsOf(events);
  assert.equal(counts.get(10), 3);
  assert.equal(counts.get(11), 1);
  assert.equal(counts.get(12), undefined);
});

test("orderThreadReplies: synced by id asc, then pending in queue order", () => {
  const events = [
    { id: 30, reply_to_id: 7 },
    { id: -2, reply_to_id: 7 }, // queued second
    { id: 20, reply_to_id: 7 },
    { id: -1, reply_to_id: 7 }, // queued first
    { id: 40, reply_to_id: 8 }, // other thread
  ];
  assert.deepEqual(orderThreadReplies(events, 7).map((e) => e.id), [20, 30, -1, -2]);
});

test("orderFeedEvents reverses for newest-first and copies for oldest-first", () => {
  const src = [1, 2, 3];
  assert.deepEqual(orderFeedEvents(src, "new"), [3, 2, 1]);
  const old = orderFeedEvents(src, "old");
  assert.deepEqual(old, [1, 2, 3]);
  assert.notEqual(old, src); // a copy, safe to mutate
});

test("stored display preferences fall back on anything unrecognized", () => {
  assert.equal(diffViewOf("full"), "full");
  assert.equal(diffViewOf("brief"), "brief");
  assert.equal(diffViewOf(null), "brief");
  assert.equal(diffViewOf("bogus"), "brief");
  assert.equal(feedOrderOf("old"), "old");
  assert.equal(feedOrderOf(null), "new");
});

test("excerptComments keeps only выписка-flagged comments", () => {
  const events = [
    { id: 1, type: "comment", is_excerpt: true },
    { id: 2, type: "comment" },
    { id: 3, type: "desc_edit", is_excerpt: true },
  ];
  assert.deepEqual(excerptComments(events).map((e) => e.id), [1]);
});

test("fullDiffSides splits ops into before/after panes with change flags", () => {
  const ops = [
    { type: "eq", text: "a " },
    { type: "del", text: "b" },
    { type: "add", text: "c" },
    { type: "eq", text: " d" },
  ];
  assert.deepEqual(fullDiffSides(ops), {
    before: [
      { changed: false, text: "a " },
      { changed: true, text: "b" },
      { changed: false, text: " d" },
    ],
    after: [
      { changed: false, text: "a " },
      { changed: true, text: "c" },
      { changed: false, text: " d" },
    ],
  });
});
