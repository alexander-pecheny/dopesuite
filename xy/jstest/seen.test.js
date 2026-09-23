// seen.ts: who saw a question, as the card's Playings corrected by hand.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  parseCardSeen, parseDeclaration, seenBy, seenPeople, serializeCardSeen, serializeDeclaration, sessionRef, withoutSeen, withSeen,
} from "../web/assets/static/dist/seen.js";

const p = (text, type = "player") => ({ text, type });
const plays = [
  { ref: "a", testers: [p("Аня"), p("Боря")] },
  { ref: "b", testers: [p("Боря"), p("Сборная", "team")] },
];
const names = (ts) => ts.map((t) => t.text);

test("with no corrections, the question was seen by everyone at its tests, once each", () => {
  assert.deepEqual(names(seenBy(plays, parseCardSeen(null))), ["Аня", "Боря", "Сборная"]);
});

test("a person added by hand saw it; one already at a test is not added twice", () => {
  const s = withSeen(parseCardSeen(""), [p("Гоша"), p(" Аня ")], plays);
  assert.deepEqual(s, { extra: [p("Гоша")], absent: {} });
  assert.deepEqual(names(seenBy(plays, s)), ["Аня", "Боря", "Сборная", "Гоша"]);
  assert.equal(seenPeople(plays, s).find((x) => x.tester.text === "Гоша").byHand, true);
});

test("taking a tester off marks them absent from every test of the card they were at", () => {
  const s = withoutSeen(parseCardSeen(""), ["Боря"], plays);
  assert.deepEqual(s, { extra: [], absent: { a: ["Боря"], b: ["Боря"] } });
  assert.deepEqual(names(seenBy(plays, s)), ["Аня", "Сборная"]);
  const bor = seenPeople(plays, s).find((x) => x.tester.text === "Боря");
  assert.equal(bor.absent, true, "still listed, so it can be undone");
  // Adding them back clears the absence rather than adding them by hand.
  assert.equal(serializeCardSeen(withSeen(s, [p("Боря")], plays)), "");
});

test("an absence from one test does not hide somebody who saw it at another", () => {
  const s = { extra: [], absent: { a: ["Боря"] } };
  assert.deepEqual(names(seenBy(plays, s)), ["Аня", "Боря", "Сборная"]);
  assert.equal(seenPeople(plays, s).find((x) => x.tester.text === "Боря").absent, false);
});

test("taking off a person added by hand removes them, and writes stop naming tests the card left", () => {
  const s = withoutSeen({ extra: [p("Гоша")], absent: { gone: ["Аня"] } }, ["Гоша"], plays);
  assert.deepEqual(s, { extra: [], absent: {} });
});

test("the blob survives garbage and serializes to nothing when there is nothing to keep", () => {
  assert.deepEqual(parseCardSeen("не json"), { extra: [], absent: {} });
  assert.deepEqual(parseCardSeen('{"extra":[{"text":" "},{"text":"Дина","type":"team"},7],"absent":{"a":["Аня",3],"b":"x"}}'),
    { extra: [p("Дина", "team")], absent: { a: ["Аня"] } });
  assert.equal(serializeCardSeen({ extra: [], absent: { a: [] } }), "");
  const round = { extra: [p("Дина")], absent: { a: ["Аня"] } };
  assert.deepEqual(parseCardSeen(serializeCardSeen(round)), round);
});

test("a session is referred to by its key, or by its row id when it has none", () => {
  assert.equal(sessionRef(9, { key: "k1" }), "k1");
  assert.equal(sessionRef(9, { key: "" }), "#9");
  assert.equal(sessionRef(9, null), "#9");
});

test("a Declaration round-trips as a list of testers", () => {
  const d = [p("Аня"), p("Сборная", "team")];
  assert.deepEqual(parseDeclaration(serializeDeclaration(d)), d);
  assert.deepEqual(parseDeclaration("{}"), []);
});
