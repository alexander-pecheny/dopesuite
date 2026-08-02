import { test } from "node:test";
import assert from "node:assert/strict";
import {
  humanDate,
  inviteLine,
  parseSession,
  serializeSession,
  sessionLabel,
  whoSaw,
  partialSeen,
  formatDate,
  parseDate,
  parseTime,
  zoneOffset,
  allZones,
} from "../web/assets/static/dist/sessions.js";
import { TOWNS } from "../web/assets/static/dist/towns.js";

const base = {
  date: "2026-07-20",
  time: "19:00",
  tz: "Europe/Moscow",
  title: "Иван Иванов и др.",
  testers: [{ text: "Иванов Иван", type: "player" }],
  cities: [],
  key: "k",
};

test("parseSession folds the legacy {datetime} shape forward", () => {
  // migrateV18 could not decrypt, so a pre-refactor session arrives verbatim.
  const legacy = JSON.stringify({
    datetime: "2026-07-20 19:00",
    title: "Иван Иванов и др.",
    testers: [{ text: "Иванов Иван", type: "player" }],
  });
  const m = parseSession(legacy);
  assert.equal(m.date, "2026-07-20");
  assert.equal(m.time, "19:00");
  assert.equal(m.title, "Иван Иванов и др.");
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
  assert.equal(sessionLabel(base, "date-title"), "20 июля · Иван Иванов и др.");
  assert.equal(sessionLabel(base, "title"), "Иван Иванов и др.");
  assert.equal(sessionLabel(base, "date"), "20 июля");
});

test("a titleless session falls back to its date under every mode", () => {
  const m = { ...base, title: "" };
  assert.equal(sessionLabel(m, "title"), "20 июля");
  assert.equal(sessionLabel(m, "date-title"), "20 июля");
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

// ---- the inverse: who to warn about which questions ----

const saw = (name) => ({ text: name, type: "player" });

test("partialSeen groups question numbers under each tester", () => {
  const qs = [
    { num: "1", testers: [saw("Анна Петрова")] },
    { num: "2", testers: [saw("Анна Петрова")] },
    { num: "3", testers: [saw("Александр Иванов")] },
    { num: "5", testers: [saw("Александр Иванов")] },
  ];
  assert.equal(
    partialSeen(qs, new Set()),
    "Видели отдельные вопросы: Александр Иванов: 3, 5; Анна Петрова: 1, 2.",
  );
});

test("partialSeen drops whoever the preamble already names", () => {
  const qs = [
    { num: "1", testers: [saw("Сидоров Пётр"), saw("Анна Петрова")] },
    { num: "2", testers: [saw("Сидоров Пётр")] },
  ];
  assert.equal(partialSeen(qs, new Set(["Сидоров Пётр"])), "Видели отдельные вопросы: Анна Петрова: 1.");
  assert.equal(partialSeen(qs, new Set(["Сидоров Пётр", "Анна Петрова"])), "");
});

test("partialSeen dedupes a tester who saw one question at two tests", () => {
  const qs = [{ num: "7", testers: [saw("Анна Петрова"), saw("Анна Петрова")] }];
  assert.equal(partialSeen(qs, new Set()), "Видели отдельные вопросы: Анна Петрова: 7.");
});

test("partialSeen keeps the caller's numbering, not a 1..n count", () => {
  const qs = [{ num: "12", testers: [saw("Анна Петрова")] }, { num: "3", testers: [saw("Анна Петрова")] }];
  assert.equal(partialSeen(qs, new Set()), "Видели отдельные вопросы: Анна Петрова: 12, 3.");
});

test("partialSeen ignores blank tester names", () => {
  assert.equal(partialSeen([{ num: "1", testers: [saw("  ")] }], new Set()), "");
});

// ---- date, time and zones as the UI writes them ----

test("formatDate/parseDate round-trip in dd.mm.yyyy", () => {
  assert.equal(formatDate("2026-02-23"), "23.02.2026");
  assert.equal(parseDate("23.02.2026"), "2026-02-23");
  assert.equal(parseDate("23.2.2026"), "2026-02-23");
  assert.equal(parseDate("23/02/2026"), "2026-02-23");
});

test("a half-typed date passes through rather than being eaten", () => {
  assert.equal(formatDate("2026-02"), "2026-02");
  assert.equal(parseDate("23.02"), "");
});

test("parseDate rejects a day that month does not have", () => {
  assert.equal(parseDate("31.02.2026"), "");
  assert.equal(parseDate("29.02.2024"), "2024-02-29"); // a real leap day
});

test("parseTime takes 24-hour only — the AM/PM it replaces is rejected", () => {
  assert.equal(parseTime("19:00"), "19:00");
  assert.equal(parseTime("9:05"), "09:05");
  assert.equal(parseTime("07:00 PM"), "");
  assert.equal(parseTime("25:00"), "");
  assert.equal(parseTime("19:60"), "");
});

test("zoneOffset labels a zone the way a picker should", () => {
  assert.equal(zoneOffset("UTC", new Date("2026-01-15T12:00:00Z")), "UTC+0");
  assert.equal(zoneOffset("Europe/Moscow", new Date("2026-01-15T12:00:00Z")), "UTC+3");
  assert.equal(zoneOffset("Asia/Kolkata", new Date("2026-01-15T12:00:00Z")), "UTC+5:30");
});

test("allZones returns the platform's IANA list", () => {
  const zones = allZones();
  assert.ok(zones.length > 100, `only ${zones.length} zones`);
  assert.ok(zones.includes("Europe/Moscow"));
});

test("every bundled town name is non-empty and zones are IANA-shaped", () => {
  assert.ok(TOWNS.length > 1000, `only ${TOWNS.length} towns`);
  for (const t of TOWNS) {
    assert.ok(t.name && t.name.trim(), "blank town name");
    if (t.zone) assert.match(t.zone, /^[A-Za-z_]+\/[A-Za-z_+-]+/);
  }
});

test("DST is handled because we store wall clock + zone, not an instant", () => {
  // The same 19:00 Moscow is 17:00 in Berlin in winter and 18:00 in summer:
  // Moscow has no DST, Berlin does. Nothing here knows that — Intl applies the
  // zone's rules FOR THAT DATE, which is the whole reason a session stores a
  // date, a wall clock and a zone rather than a timestamp.
  const winter = { ...base, date: "2026-01-15", cities: [{ zone: "Europe/Berlin", name: "Берлин" }] };
  const summer = { ...base, date: "2026-07-15", cities: [{ zone: "Europe/Berlin", name: "Берлин" }] };
  assert.equal(inviteLine(winter), "15 января, 17:00 (Берлин)");
  assert.equal(inviteLine(summer), "15 июля, 18:00 (Берлин)");
});

test("a zone that abolished DST converts flat across the year", () => {
  // Kazakhstan runs no DST, so 19:00 Moscow is 21:00 Алматы in both seasons.
  const cities = [{ zone: "Asia/Almaty", name: "Алматы" }];
  assert.equal(inviteLine({ ...base, date: "2026-01-15", cities }), "15 января, 21:00 (Алматы)");
  assert.equal(inviteLine({ ...base, date: "2026-07-15", cities }), "15 июля, 21:00 (Алматы)");
});

// A card's assignments live in a FLAT list, so forgetting a dead card must
// filter on cardId. `delete list[id]` punches a hole at that array index —
// dropping an unrelated card's assignment and keeping the dead card's own.
test("forgetting a dead card drops its own assignments and nobody else's", () => {
  const cardLabels = [
    { cardId: 1, labelId: 10, sessionId: null },
    { cardId: 2, labelId: 11, sessionId: null },
    { cardId: 2, labelId: 12, sessionId: 7 },
    { cardId: 3, labelId: 13, sessionId: null },
  ];
  const dead = new Set([2]);
  const kept = cardLabels.filter((a) => !dead.has(a.cardId));
  assert.deepEqual(kept.map((a) => a.cardId), [1, 3]);

  // What the buggy shape did instead: index 2 is card 3's row, not card 2's.
  const wrong = cardLabels.slice();
  delete wrong[2];
  assert.equal(wrong[2], undefined, "the hole lands at the INDEX, not the card");
  assert.ok(wrong.some((a) => a && a.cardId === 2), "card 2's rows survive the wrong form");
});

// The zone picker has to be usable by someone who knows «Алматы» and not
// «Asia/Almaty» — the town list already carries that mapping.
test("every common ЧГК city resolves to a zone the picker can offer", () => {
  const byName = new Map(TOWNS.filter((t) => t.zone).map((t) => [t.name, t.zone]));
  for (const [city, zone] of [
    ["Москва", "Europe/Moscow"],
    ["Алматы", "Asia/Almaty"],
    ["Тбилиси", "Asia/Tbilisi"],
    ["Берлин", "Europe/Berlin"],
    ["Ереван", "Asia/Yerevan"],
    ["Владивосток", "Asia/Vladivostok"],
  ]) {
    assert.equal(byName.get(city), zone, `${city} should map to ${zone}`);
  }
});

test("a Cyrillic prefix matches towns that a raw IANA search never would", () => {
  const hits = TOWNS.filter((t) => t.zone && t.name.toLowerCase().startsWith("алма"));
  assert.ok(hits.length > 0, "«алма» matches no town");
  assert.ok(hits.some((t) => t.zone === "Asia/Almaty"));
  // The point: the zone id itself contains no Cyrillic, so without the town
  // index this query returns nothing at all.
  assert.equal(hits[0].zone.toLowerCase().includes("алма"), false);
});
