import {test} from "node:test";
import assert from "node:assert/strict";
import * as ksi from "./dist/ksi-protocol.js";

globalThis.window = {};
globalThis.document = {activeElement: null};

test("rulesOf reads team mode, theme count and stickers", () => {
  const plain = ksi.rulesOf({gameType: "ksi"});
  assert.equal(plain.teamMode, true);
  assert.equal(plain.themesCount, ksi.KSI_THEMES);
  assert.equal(ksi.stickersEnabled(plain), false);
  const player = ksi.rulesOf({});
  assert.equal(player.teamMode, false);
  assert.equal(player.themesCount, 8);
  assert.deepEqual(ksi.schemeParticipants({}), ["Игрок 1", "Игрок 2", "Игрок 3", "Игрок 4"]);
  const stickers = ksi.rulesOf({gameType: "ksi", themes: "3", stickers: {types: [{id: "x2", max: 1}, {id: "neutral"}, null, {label: "no id"}]}});
  assert.equal(stickers.themesCount, 3);
  assert.deepEqual(stickers.stickers.map((t) => [t.id, t.label, t.max]), [["x2", "x2", 1], ["neutral", "neutral", null]]);
  assert.equal(ksi.stickersEnabled(stickers), true);
});

test("parseState pads themes and answer rows, keeps declined, normalises the sticker grid", () => {
  const rules = ksi.rulesOf({gameType: "ksi", themes: 2, stickers: {types: [{id: "x2"}]}});
  const state = ksi.parseState({participants: [{number: 1, name: "A"}, {number: 2, name: "B"}], themes: [{answers: [["right"]]}], declined: "no", stickers: [["x2", "zzz"]]}, rules, []);
  assert.equal(state.themes.length, 2);
  assert.deepEqual(state.themes[0].answers, [["right", "", "", "", ""], ["", "", "", "", ""]]);
  assert.equal(state.finished, false);
  assert.deepEqual(state.declined, {});
  assert.deepEqual(state.stickers, [["x2", ""], ["", ""]]);
  const fallback = ksi.parseState(null, ksi.rulesOf({}), ["X", "Y"]);
  assert.deepEqual(fallback.participants, ["X", "Y"]);
});

test("markContribution mirrors the server's sticker rules", () => {
  assert.equal(ksi.markContribution("neutral", "right", 2), 30);
  assert.equal(ksi.markContribution("neutral", "wrong", 2), -30);
  assert.equal(ksi.markContribution("neutral", "", 2), 0);
  assert.equal(ksi.markContribution("x2", "right", 0), 20);
  assert.equal(ksi.markContribution("x2", "wrong", 0), -20);
  assert.equal(ksi.markContribution("nowrong", "wrong", 4), 0);
  assert.equal(ksi.markContribution("emptywrong", "", 4), -50);
  assert.equal(ksi.markContribution("whatever", "right", 1), 20);
});

test("scoreSheet totals, leaves an unstickered theme unscored and ranks", () => {
  const rules = ksi.rulesOf({gameType: "ksi", themes: 2, stickers: {types: [{id: "neutral"}, {id: "x2"}]}});
  const state = ksi.parseState({
    participants: [{number: 1, name: "A"}, {number: 2, name: "B"}],
    themes: [{answers: [["right", "right"], ["right", "wrong"]]}, {answers: [["right"], ["right"]]}],
    stickers: [["neutral", "x2"], ["neutral", ""]],
  }, rules, []);
  const sheet = ksi.scoreSheet(state, rules);
  assert.deepEqual(sheet.themeScores, [[30, 10], [-20, 0]]);
  assert.deepEqual(sheet.themeScored, [[true, true], [true, false]]);
  assert.deepEqual(sheet.totals, [40, -20]);
  assert.deepEqual(sheet.places, ["1", "2"]);
});

test("rankedResultRows skips a declined team and shares a place on equal metrics", () => {
  const rules = ksi.rulesOf({gameType: "ksi", themes: 1});
  const state = ksi.parseState({
    participants: [{number: 1, name: "A"}, {number: 2, name: "B"}, {number: 3, name: "C"}],
    themes: [{answers: [["right"], ["right"], ["right", "right"]]}],
    declined: {n3: true},
  }, rules, []);
  const rows = ksi.rankedResultRows(state, rules, (i) => ksi.participantName(state, i));
  assert.deepEqual(rows.map((r) => [r.name, r.placeText, r.metrics.total]), [["A", "1–2", 10], ["B", "1–2", 10]]);
  assert.equal(ksi.declinedKey(state, 2), "n3");
  assert.equal(ksi.declinedKey(ksi.parseState({participants: ["Гость"]}, ksi.rulesOf({}), []), 0), "sгость");
});
