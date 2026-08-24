import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

// bundleapply.ts binds the request verbs at import, so a recording API stands in
// for the server before it loads. The key is real: everything the applier sends
// must read back under it.
installDOM([]);
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");

let calls = [];
let nextId = 500;
let failCardAt = 0; // refuse the nth card create (1-based); 0 = refuse nothing
let cardPosts = 0;
xyApp.fetchJSON = async (url) => { calls.push(["GET", String(url)]); return []; };
xyApp.jpost = async (url, body) => {
  calls.push(["POST", String(url), body]);
  if (String(url).endsWith("/cards") && ++cardPosts === failCardAt) throw new Error("сеть недоступна");
  if (String(url).endsWith("/timeline/import")) {
    const ids = {};
    for (const e of body.events) ids[String(e.src_id)] = ++nextId;
    return { ids };
  }
  return { id: ++nextId };
};
xyApp.jput = async (url, body) => { calls.push(["PUT", String(url), body]); return {}; };
xyApp.jdelete = async (url) => { calls.push(["DELETE", String(url)]); return {}; };
globalThis.fetch = async (url) => { calls.push(["FETCH", String(url)]); return { ok: true }; };

const { applyBundle } = await import("../web/assets/static/dist/bundleapply.js");
const { xyBundle } = await import("../web/assets/static/dist/bundle.js");

const { dk } = await xyCrypto.createBoardKeys("target board passphrase here");

function bundle(overrides = {}) {
  return {
    format: xyBundle.BUNDLE_FORMAT,
    exported_at: "2026-08-24T12:00:00Z",
    board: { name: "Синхрон 2026" },
    members: [],
    lists: [
      { id: 1, type: "normal", title: "Тур 1", rank: "a0", group_id: 7 },
      { id: 2, type: "normal", title: "Тур 2", rank: "a1", group_id: 7 },
    ],
    groups: [{ id: 7, name: "Пакет" }],
    cards: [
      { id: 10, list_id: 1, kind: "question", description: "? первый", rank: "a0", handout_meta: null, alias: null, created_at: null },
      { id: 11, list_id: 2, kind: "question", description: "? второй", rank: "a0", handout_meta: null, alias: null, created_at: null },
    ],
    labels: [{ id: 20, name: "взяли", color: "#3aa657" }],
    sessions: [{ id: 30, meta: JSON.stringify({ key: "s-30", date: "2026-03-12", title: "Тест", testers: [], cities: [], time: "", tz: "" }) }],
    card_labels: [{ card_id: 10, label_id: 20, session_id: 30 }],
    card_sessions: [{ card_id: 10, session_id: 30 }],
    tour_testers: [],
    timeline: [
      { id: 40, card_id: 10, session_id: null, type: "comment", author: "аня", created_at: "2026-03-01T00:00:00Z", edited_at: null, is_excerpt: false, reply_to_id: null, payload: "хорош" },
    ],
    attachments: [],
    ...overrides,
  };
}

const quiet = () => {};
const noBytes = async () => null;
const reset = () => { calls = []; failCardAt = 0; cardPosts = 0; };
const posts = (suffix) => calls.filter((c) => c[0] === "POST" && c[1].endsWith(suffix));

test("a fresh board takes the bundle verbatim — every label and session created, ranks kept", async () => {
  reset();
  const b = bundle();
  const r = await applyBundle(b, { boardId: 900, dk, append: null }, noBytes, quiet);
  assert.equal(r.failed, false);
  assert.equal(r.cards, 2);
  assert.deepEqual(posts("/lists").map((c) => c[2].rank), ["a0", "a1"], "the source's own ranks");
  assert.equal(posts("/labels").length, 1);
  assert.equal(posts("/sessions").length, 1);
  const meta = JSON.parse(await xyCrypto.decField(dk, posts("/sessions")[0][2].meta_enc));
  assert.equal(meta.origin, undefined, "a replica is nobody's copy");
});

test("a group is created once, from the lists this unit made", async () => {
  reset();
  await applyBundle(bundle(), { boardId: 900, dk, append: null }, noBytes, quiet);
  const groups = posts("/list-groups");
  assert.equal(groups.length, 1);
  assert.equal(groups[0][2].list_ids.length, 2);
  assert.equal(await xyCrypto.decField(dk, groups[0][2].name_enc), "Пакет");
});

test("appending reuses a label of the same name and colour, and creates the rest", async () => {
  reset();
  const append = {
    labels: [{ id: 77, name: "взяли", color: "#3aa657" }],
    sessions: [],
    lastRank: "a5",
    sourceName: "Синхрон 2026",
  };
  const r = await applyBundle(bundle(), { boardId: 901, dk, append }, noBytes, quiet);
  assert.equal(r.failed, false);
  assert.equal(posts("/labels").length, 0, "the target already had «взяли»");
  const assigned = calls.find((c) => c[0] === "PUT" && c[1].includes("/labels"));
  assert.equal(assigned[2].labels[0].label_id, 77);
});

test("appending reuses a session with the same key, and stamps origin on a new one", async () => {
  reset();
  const append = {
    labels: [],
    sessions: [{ id: 88, meta: JSON.stringify({ key: "s-30" }) }],
    lastRank: null,
    sourceName: "Синхрон 2026",
  };
  await applyBundle(bundle(), { boardId: 901, dk, append }, noBytes, quiet);
  assert.equal(posts("/sessions").length, 0, "the same sitting is already here");
  const played = calls.find((c) => c[0] === "PUT" && c[1].includes("/sessions"));
  assert.deepEqual(played[2].session_ids, [88]);

  reset();
  const other = { labels: [], sessions: [{ id: 88, meta: JSON.stringify({ key: "s-other" }) }], lastRank: null, sourceName: "Синхрон 2026" };
  await applyBundle(bundle(), { boardId: 901, dk, append: other }, noBytes, quiet);
  const meta = JSON.parse(await xyCrypto.decField(dk, posts("/sessions")[0][2].meta_enc));
  assert.deepEqual(meta.origin, { board: "Синхрон 2026", at: "2026-08-24" });
});

test("appending re-ranks the arrivals after the target's last list, keeping their order", async () => {
  reset();
  const append = { labels: [], sessions: [], lastRank: "a5", sourceName: "X" };
  await applyBundle(bundle(), { boardId: 901, dk, append }, noBytes, quiet);
  const ranks = posts("/lists").map((c) => c[2].rank);
  assert.equal(ranks.length, 2);
  assert.ok(ranks[0] > "a5", "after what is already there");
  assert.ok(ranks[1] > ranks[0], "and in the bundle's own order");
});

test("the timeline keeps its author and timestamp", async () => {
  reset();
  await applyBundle(bundle(), { boardId: 900, dk, append: null }, noBytes, quiet);
  const ev = posts("/timeline/import")[0][2].events[0];
  assert.equal(ev.author_username, "аня");
  assert.equal(ev.created_at, "2026-03-01T00:00:00Z");
  assert.equal(await xyCrypto.decField(dk, ev.payload_enc), "хорош");
});

test("a unit that fails rolls back its own lists and leaves the ones before it", async () => {
  reset();
  const b = bundle({
    lists: [
      { id: 1, type: "normal", title: "Тур 1", rank: "a0", group_id: null },
      { id: 2, type: "normal", title: "Тур 2", rank: "a1", group_id: null },
    ],
    groups: [],
    card_labels: [],
    card_sessions: [],
    sessions: [],
    labels: [],
    timeline: [],
  });
  failCardAt = 2; // the second unit's only card
  const r = await applyBundle(b, { boardId: 901, dk, append: { labels: [], sessions: [], lastRank: null, sourceName: "X" } }, noBytes, quiet);

  assert.equal(r.failed, true);
  assert.deepEqual(r.units.map((u) => u.title), ["Тур 1", "Тур 2"]);
  assert.equal(r.units[0].error, undefined, "the finished tour stays");
  assert.match(r.units[1].error, /сеть/);
  const deleted = calls.filter((c) => c[0] === "DELETE");
  assert.equal(deleted.length, 1, "only the list of the unit that failed");
});

test("an attachment the producer cannot hand over is skipped and named", async () => {
  reset();
  const b = bundle({
    attachments: [{ id: 50, card_id: 10, filename: "раздатка.png", mime: "image/png", size: 9, lossless: true, is_excerpt: false, path: "attachments/50-раздатка.png" }],
  });
  const r = await applyBundle(b, { boardId: 900, dk, append: null }, noBytes, quiet);
  assert.equal(r.failed, false);
  assert.deepEqual(r.skipped, ["раздатка.png"]);
  assert.equal(r.attachments, 0);
});
