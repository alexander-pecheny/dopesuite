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
