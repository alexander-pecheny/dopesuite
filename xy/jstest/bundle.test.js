import { test } from "node:test";
import assert from "node:assert/strict";
import { xyBundle } from "../web/assets/static/dist/bundle.js";

const { parseBundle, attachmentPath, attachmentsTotal, contentBytes, BUNDLE_FORMAT } = xyBundle;

function minimal(overrides = {}) {
  return {
    format: BUNDLE_FORMAT,
    exported_at: "2026-08-24T00:00:00Z",
    board: { name: "Доска" },
    members: [],
    lists: [],
    groups: [],
    cards: [],
    labels: [],
    sessions: [],
    card_labels: [],
    card_sessions: [],
    tour_testers: [],
    timeline: [],
    attachments: [],
    ...overrides,
  };
}

test("accepts a minimal bundle", () => {
  const b = parseBundle(JSON.stringify(minimal()));
  assert.equal(b.board.name, "Доска");
});

test("accepts a consistent full bundle", () => {
  const b = minimal({
    lists: [{ id: 1, type: "normal", title: "Тур 1", rank: "a0", group_id: 7 }],
    groups: [{ id: 7, name: "Пакет" }],
    cards: [{ id: 2, list_id: 1, kind: "question", description: "? Вопрос", rank: "a0", handout_meta: null, alias: null, created_at: null }],
    labels: [{ id: 3, name: "взяли", color: "#3aa657" }],
    sessions: [{ id: 4, meta: "{}", created_at: null }],
    card_labels: [{ card_id: 2, label_id: 3, session_id: 4 }],
    card_sessions: [{ card_id: 2, session_id: 4 }],
    tour_testers: [{ list_id: 1, group_id: null, session_id: 4 }],
    timeline: [
      { id: 10, card_id: 2, session_id: null, type: "comment", author: "vasya", created_at: "2026-01-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "норм" },
      { id: 11, card_id: null, session_id: 4, type: "comment", author: null, created_at: "2026-01-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "про тест" },
      { id: 12, card_id: 2, session_id: null, type: "desc_edit", author: "vasya", created_at: "2026-01-02T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: '{"before":"a","after":"b"}' },
    ],
    attachments: [{ id: 5, card_id: 2, filename: "р.png", mime: "image/png", size: 10, lossless: true, is_excerpt: false, path: "attachments/5-р.png" }],
  });
  assert.equal(parseBundle(JSON.stringify(b)).timeline.length, 3);
  assert.equal(attachmentsTotal(b), 10);
  // attachments + titles + descriptions + labels + sessions + payloads, in UTF-8
  assert.ok(contentBytes(b) > 10 + "? Вопрос".length, String(contentBytes(b)));
});

test("rejects wrong format, dangling refs, bad events", () => {
  assert.throws(() => parseBundle("not json"), /не JSON/);
  assert.throws(() => parseBundle(JSON.stringify(minimal({ format: "xy.board.v99" }))), /неизвестный формат/);
  assert.throws(
    () => parseBundle(JSON.stringify(minimal({ cards: [{ id: 1, list_id: 5, description: "x", rank: "a0" }] }))),
    /несуществующий id 5/,
  );
  assert.throws(
    () => parseBundle(JSON.stringify(minimal({ timeline: [{ id: 1, card_id: null, session_id: null, type: "comment", payload: "x" }] }))),
    /ни на карточке/,
  );
  assert.throws(
    () => parseBundle(JSON.stringify(minimal({ timeline: [{ id: 1, card_id: null, session_id: null, type: "hack", payload: "x" }] }))),
    /неизвестный тип/,
  );
  assert.throws(
    () => parseBundle(JSON.stringify(minimal({ tour_testers: [{ list_id: null, group_id: null, session_id: null }] }))),
    /ровно один/,
  );
  assert.throws(
    () => parseBundle(JSON.stringify(minimal({ lists: [{ id: 1, title: "a", rank: "a0" }, { id: 1, title: "b", rank: "a1" }] }))),
    /повторный id/,
  );
});

test("attachmentPath keeps unicode, strips traversal", () => {
  assert.equal(attachmentPath(5, "раздатка.png"), "attachments/5-раздатка.png");
  const evil = attachmentPath(6, "../../etc/passwd").slice("attachments/".length);
  assert.ok(!evil.includes("/") && !evil.includes("\\") && !evil.startsWith("6-."), evil);
  assert.ok(!attachmentPath(6, "a/b\\c").includes("/b"));
  assert.equal(attachmentPath(7, ""), "attachments/7-file");
});

// ---------------- units and slicing ----------------

const { unitsOf, bundleUnits, sliceBundle } = xyBundle;

function board() {
  return minimal({
    lists: [
      { id: 1, type: "normal", title: "Разминка", rank: "a0", group_id: null },
      { id: 2, type: "normal", title: "Тур 1", rank: "a1", group_id: 7 },
      { id: 3, type: "normal", title: "Тур 2", rank: "a2", group_id: 7 },
      { id: 4, type: "normal", title: "Черновики", rank: "a3", group_id: null },
    ],
    groups: [{ id: 7, name: "Синхрон 2026" }],
    cards: [
      { id: 10, list_id: 1, kind: "question", description: "? разминочный", rank: "a0", handout_meta: null, alias: null, created_at: null },
      { id: 11, list_id: 2, kind: "question", description: "? первый", rank: "a0", handout_meta: null, alias: null, created_at: null },
      { id: 12, list_id: 4, kind: "question", description: "? черновой", rank: "a0", handout_meta: null, alias: null, created_at: null },
    ],
    labels: [
      { id: 20, name: "взяли", color: "#3aa657" },
      { id: 21, name: "черновик", color: "#888888" },
    ],
    sessions: [
      { id: 30, meta: '{"key":"aaa"}', created_at: null },
      { id: 31, meta: '{"key":"bbb"}', created_at: null },
      { id: 32, meta: '{"key":"ccc"}', created_at: null },
    ],
    card_labels: [
      { card_id: 11, label_id: 20, session_id: 30 },
      { card_id: 12, label_id: 21, session_id: null },
    ],
    card_sessions: [
      { card_id: 11, session_id: 30 },
      { card_id: 12, session_id: 31 },
    ],
    tour_testers: [{ list_id: null, group_id: 7, session_id: 32 }],
    timeline: [
      { id: 40, card_id: 11, session_id: null, type: "comment", author: null, created_at: "2026-01-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "по первому" },
      { id: 41, card_id: 12, session_id: null, type: "comment", author: null, created_at: "2026-01-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "по черновому" },
      { id: 42, card_id: null, session_id: 31, type: "comment", author: null, created_at: "2026-01-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "про второй тест" },
    ],
    attachments: [
      { id: 50, card_id: 11, filename: "раздатка.png", mime: "image/png", size: 10, lossless: true, is_excerpt: false, path: "attachments/50-раздатка.png" },
      { id: 51, card_id: 12, filename: "черновая.png", mime: "image/png", size: 10, lossless: true, is_excerpt: false, path: "attachments/51-черновая.png" },
    ],
  });
}

test("a List Group is one unit, in board order", () => {
  const units = bundleUnits(board());
  assert.deepEqual(units.map((u) => u.title), ["Разминка", "Синхрон 2026", "Черновики"]);
  const group = units[1];
  assert.equal(group.group, true);
  assert.deepEqual(group.listIds, [2, 3]);
  assert.equal(group.key, "g7");
});

test("unitsOf names a group by its members when the group row is missing", () => {
  const units = unitsOf(
    [{ id: 2, title: "Тур 1", rank: "a1", group_id: 7 }, { id: 3, title: "Тур 2", rank: "a2", group_id: 7 }],
    [],
  );
  assert.deepEqual(units.map((u) => u.title), ["Тур 1 + Тур 2"]);
});

test("a slice carries only what its cards reach", () => {
  const s = sliceBundle(board(), [2, 3]);
  assert.deepEqual(s.lists.map((l) => l.id), [2, 3]);
  assert.deepEqual(s.groups.map((g) => g.id), [7]);
  assert.deepEqual(s.cards.map((c) => c.id), [11]);
  assert.deepEqual(s.labels.map((l) => l.name), ["взяли"], "an unassigned label stays home");
  assert.deepEqual(s.attachments.map((a) => a.id), [50]);
  assert.deepEqual(s.timeline.map((e) => e.id), [40], "another tour's лента does not travel");
});

test("a slice keeps the sessions its questions were played at, and its tour declares", () => {
  const s = sliceBundle(board(), [2, 3]);
  assert.deepEqual(s.sessions.map((x) => x.id).sort(), [30, 32], "31 was another tour's sitting");
  assert.deepEqual(s.tour_testers, [{ list_id: null, group_id: 7, session_id: 32 }]);
});

test("a slice of one ungrouped list drops the group and its declaration", () => {
  const s = sliceBundle(board(), [1]);
  assert.deepEqual(s.groups, []);
  assert.deepEqual(s.tour_testers, []);
  assert.deepEqual(s.sessions, [], "nothing this list's card played at");
});

test("slicing every list is the whole board minus what nothing reaches", () => {
  const b = board();
  const s = sliceBundle(b, [1, 2, 3, 4]);
  assert.deepEqual(s.cards, b.cards);
  assert.deepEqual(s.timeline, b.timeline);
  assert.deepEqual(s.labels, b.labels);
  assert.deepEqual(s.sessions.map((x) => x.id).sort(), [30, 31, 32]);
});
