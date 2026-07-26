import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canonicalLabel,
  DEFAULT_MARKS,
  humanDate,
  inviteLine,
  markLabel,
  parseSession,
  serializeSession,
  sessionLabel,
  whoSaw,
} from "../web/assets/static/dist/sessions.js";

const base = {
  date: "2026-07-20",
  time: "19:00",
  tz: "Europe/Moscow",
  title: "Алиев и др.",
  testers: [{ text: "Иванов Иван", type: "player" }],
  cities: [],
  key: "k",
};

test("parseSession folds the legacy {datetime} shape forward", () => {
  // migrateV18 could not decrypt, so a pre-refactor session arrives verbatim.
  const legacy = JSON.stringify({
    datetime: "2026-07-20 19:00",
    title: "Алиев и др.",
    testers: [{ text: "Иванов Иван", type: "player" }],
  });
  const m = parseSession(legacy);
  assert.equal(m.date, "2026-07-20");
  assert.equal(m.time, "19:00");
  assert.equal(m.title, "Алиев и др.");
  assert.equal(m.testers.length, 1);
});

test("parseSession folds the oldest {players:[ids]} shape forward too", () => {
  const m = parseSession(JSON.stringify({ datetime: "2026-07-20", players: [17, 42] }));
  assert.deepEqual(m.testers.map((t) => t.text), ["17", "42"]);
  assert.equal(m.time, "");
});

test("parseSession survives junk", () => {
  const m = parseSession("not json at all");
  assert.equal(m.date, "");
  assert.deepEqual(m.testers, []);
});

test("a date-only session keeps an empty time", () => {
  const m = parseSession(JSON.stringify({ date: "2026-07-20", title: "т" }));
  assert.equal(m.time, "");
});

test("serializeSession round-trips and drops blank testers", () => {
  const m = parseSession(serializeSession({ ...base, testers: [{ text: "  ", type: "player" }, { text: "Пётр", type: "team" }] }));
  assert.deepEqual(m.testers, [{ text: "Пётр", type: "team" }]);
  assert.equal(m.key, "k");
});

test("the origin stamp survives a round trip", () => {
  const withOrigin = { ...base, origin: { board: "Синхрон", at: "2026-07-25" } };
  assert.deepEqual(parseSession(serializeSession(withOrigin)).origin, { board: "Синхрон", at: "2026-07-25" });
});

test("sessionLabel honours the reader's title mode", () => {
  assert.equal(sessionLabel(base, "date-title"), "20 июля · Алиев и др.");
  assert.equal(sessionLabel(base, "title"), "Алиев и др.");
  assert.equal(sessionLabel(base, "date"), "20 июля");
});

test("a titleless session falls back to its date under every mode", () => {
  const m = { ...base, title: "" };
  assert.equal(sessionLabel(m, "title"), "20 июля");
  assert.equal(sessionLabel(m, "date-title"), "20 июля");
});

test("markLabel names the session and the mark", () => {
  assert.equal(markLabel(base, "taken", DEFAULT_MARKS), "20 июля · Алиев и др. · взяли");
});

test("an unknown mark falls back to its own key", () => {
  assert.equal(markLabel(base, "seen", DEFAULT_MARKS), "20 июля · Алиев и др. · seen");
});

test("canonicalLabel ignores the reader's preference — chgksuite reads it", () => {
  assert.equal(canonicalLabel(base, "taken", DEFAULT_MARKS), markLabel(base, "taken", DEFAULT_MARKS, "date-title"));
});

test("the invite line converts from the anchor zone, not from UTC", () => {
  const m = {
    ...base,
    cities: [
      { zone: "Europe/Berlin", name: "Берлин" },
      { zone: "Europe/Moscow", name: "Москва" },
      { zone: "Asia/Almaty", name: "Алматы" },
    ],
  };
  assert.equal(inviteLine(m), "20 июля, 18:00 (Берлин) / 19:00 (Москва) / 21:00 (Алматы)");
});

test("a city whose local date differs carries its own", () => {
  const m = {
    ...base,
    time: "22:00",
    cities: [{ zone: "Europe/Moscow", name: "Москва" }, { zone: "Asia/Vladivostok", name: "Владивосток" }],
  };
  assert.equal(inviteLine(m), "20 июля, 22:00 (Москва) / 05:00 (Владивосток) — 21 июля");
});

test("a date-only session's invite line is just the date", () => {
  assert.equal(inviteLine({ ...base, time: "" }), "20 июля");
});

test("with no cities the invite line still states the anchor", () => {
  assert.equal(inviteLine(base), "20 июля, 19:00 (Europe/Moscow)");
});

test("humanDate leaves an unparseable date alone", () => {
  assert.equal(humanDate("завтра"), "завтра");
});

test("whoSaw unions testers across sessions and dedupes", () => {
  const a = { ...base, testers: [{ text: "Иванов Иван", type: "player" }] };
  const b = { ...base, testers: [{ text: "Иванов Иван", type: "player" }, { text: "Петров Пётр", type: "player" }] };
  assert.equal(whoSaw([a, b]), "Иванов Иван, Петров Пётр");
});
