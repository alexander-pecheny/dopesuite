// The appeals List's model (ADR-0021): the card JSON round trip, the vote
// tally and the status it gives, the refresh merge and the export plan.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyAppeals as A } from "../web/assets/static/dist/appeals.js";

const site = (over = {}) => ({
  id: 12296, type: "A", questionNumber: 28, answer: "аутсайдер", appeal: "Просим\r\nзасчитать",
  status: "N", comment: null, controversial: 178669, issuedAt: "2022-10-06T11:46:12+00:00", ...over,
});
const contro = [
  { id: 178669, questionNumber: 28, answer: "аутсайдер", status: "D", comment: "Недостаточно точно." },
  { id: 178700, questionNumber: 28, answer: "андердог", status: "A", comment: "", appealJuryComment: null },
  { id: 178701, questionNumber: 3, answer: "мир", status: "A", comment: "" },
];

test("appeals: a card reads back what it was written as", () => {
  const a = A.fromSite(site(), contro);
  assert.equal(a.text, "Просим\nзасчитать");
  assert.deepEqual(a.controversials.map((c) => c.answer), ["андердог", "аутсайдер"]);
  assert.deepEqual(A.parseAppeal(A.composeAppeal(a)), a);
  assert.ok(!A.composeAppeal(a).includes("\n"), "one line, so no Version separator can appear in it");
  assert.equal(A.parseAppeal("? вопрос"), null);
  assert.equal(A.parseAppeal("{}"), null);
});

test("appeals: a verdict the site already has is taken as set by hand", () => {
  const a = A.fromSite(site({ status: "D", comment: "Вердикт: отклонить (3:0)" }), contro);
  assert.deepEqual(a.verdict, { status: "D", text: "Вердикт: отклонить (3:0)" });
  assert.equal(A.fromSite(site(), contro).verdict.status, "");
});

test("appeals: the votes decide unless the status is set by hand", () => {
  const a = A.fromSite(site(), contro);
  const votes = [
    { cardId: 1, userId: 1, value: "for" }, { cardId: 1, userId: 2, value: "against" },
    { cardId: 1, userId: 3, value: "for" }, { cardId: 2, userId: 1, value: "against" },
  ];
  const t = A.tally(votes, 1);
  assert.deepEqual(t, { for: 2, against: 1 });
  assert.deepEqual(A.verdictStatus(a, t), { status: "A", byHand: false });
  assert.deepEqual(A.verdictStatus(a, { for: 1, against: 1 }), { status: "", byHand: false });
  assert.deepEqual(A.verdictStatus(a, { for: 0, against: 0 }), { status: "", byHand: false });
  a.verdict.status = "D";
  assert.deepEqual(A.verdictStatus(a, t), { status: "D", byHand: true });
});

test("appeals: a refresh adds what is new in order and keeps the jury's work", () => {
  const first = A.fromSite(site({ id: 1, questionNumber: 2, answer: "б" }), contro);
  const third = A.fromSite(site({ id: 3, questionNumber: 9, answer: "" , type: "R" }), contro);
  first.verdict = { status: "A", text: "принять" };
  const held = [{ cardId: 10, rank: "a0", appeal: first }, { cardId: 30, rank: "a1", appeal: third }];
  const now = [
    site({ id: 1, questionNumber: 2, answer: "б", appeal: "новый текст", status: "N" }),
    site({ id: 2, questionNumber: 5, answer: "в" }),
    site({ id: 4, questionNumber: 12, answer: "г" }),
    site({ id: 3, questionNumber: 9, answer: "", type: "R", appeal: "Просим\r\nзасчитать" }),
  ];
  const plan = A.planRefresh(held, now, contro, "2026-10-18T10:00:00Z");
  assert.deepEqual(plan.create.map((c) => c.appeal.id), [2, 4]);
  const [two, four] = plan.create.map((c) => c.rank);
  assert.ok("a0" < two && two < "a1", two);
  assert.ok("a1" < four, four);
  assert.equal(plan.update.length, 1);
  const upd = plan.update[0];
  assert.equal(upd.cardId, 10);
  assert.equal(upd.appeal.text, "новый текст");
  assert.equal(upd.appeal.textChangedAt, "2026-10-18T10:00:00Z");
  assert.deepEqual(upd.appeal.verdict, { status: "A", text: "принять" });
});

test("appeals: two new appeals between the same cards get two ranks", () => {
  const held = [{ cardId: 1, rank: "a0", appeal: A.fromSite(site({ id: 1, questionNumber: 1 }), []) }];
  const plan = A.planRefresh(held, [site({ id: 7, questionNumber: 2 }), site({ id: 8, questionNumber: 3 })], [], "");
  const [x, y] = plan.create.map((c) => c.rank);
  assert.ok("a0" < x && x < y, `${x} ${y}`);
});

test("appeals: the export sends only decided verdicts the site does not have", () => {
  const decided = A.fromSite(site({ id: 1 }), []);
  decided.verdict = { status: "", text: "Вердикт: принять" };
  const same = A.fromSite(site({ id: 2, status: "D", comment: "Вердикт: отклонить" }), []);
  const open = A.fromSite(site({ id: 3 }), []);
  const held = [{ cardId: 10, rank: "a", appeal: decided }, { cardId: 20, rank: "b", appeal: same }, { cardId: 30, rank: "c", appeal: open }];
  const votes = [{ cardId: 10, userId: 1, value: "for" }];
  const now = [site({ id: 1 }), site({ id: 2, status: "D", comment: "Вердикт: отклонить\r\n" }), site({ id: 3 })];
  assert.deepEqual(A.planExport(held, votes, now), [
    { cardId: 10, appealId: 1, status: "A", comment: "Вердикт: принять", siteStatus: "N", siteComment: "" },
  ]);
});

test("appeals: the settings survive a round trip and a broken value", () => {
  const m = { tournamentId: 7696, packetListId: 4, manual: false };
  assert.deepEqual(A.parseMeta(A.composeMeta(m)), m);
  assert.deepEqual(A.parseMeta(A.composeMeta({ ...m, manual: true })), { ...m, manual: true });
  assert.deepEqual(A.parseMeta("oops"), { tournamentId: null, packetListId: null, manual: false });
  assert.deepEqual(A.parseMeta(null), { tournamentId: null, packetListId: null, manual: false });
});

test("appeals: question N is found by its number in the packet", () => {
  assert.equal(A.questionCard(["a", "b", "c"], [null, "1", "2"], 2), "c");
  assert.equal(A.questionCard(["a"], ["1"], 5), null);
});

test("appeals: a travelling list keeps its packet only if the packet came too", () => {
  const meta = A.composeMeta({ tournamentId: 7, packetListId: 4, manual: false });
  assert.deepEqual(A.parseMeta(A.relinkPacket(meta, new Map([[4, 40]]))), { tournamentId: 7, packetListId: 40, manual: false });
  assert.deepEqual(A.parseMeta(A.relinkPacket(meta, new Map())), { tournamentId: 7, packetListId: null, manual: false });
});

test("appeals: the verdict table has a row per appeal, an undecided one with no status", () => {
  const voted = A.fromSite(site({ id: 1 }), []);
  voted.verdict.text = "Вердикт: принять\n";
  const open = A.fromSite(site({ id: 2, type: "R", questionNumber: 3, answer: "" }), []);
  const held = [{ cardId: 10, rank: "a", appeal: voted }, { cardId: 20, rank: "b", appeal: open }];
  assert.deepEqual(A.verdictRows(held, [{ cardId: 10, userId: 1, value: "for" }]), [
    [" ID", "Тип", "№", "Ответ", "Статус", "Комментарий"],
    [1, "A", 28, "аутсайдер", "A", "Вердикт: принять"],
    [2, "R", 3, "", "", ""],
  ]);
});

test("appeals: a hand-made appeal takes the next id and its place by question", () => {
  const held = [
    { cardId: 1, rank: "a0", appeal: A.addManual([], { type: "A", question: 3, answer: "x", text: "t" }, "").appeal },
    { cardId: 2, rank: "a1", appeal: { ...A.addManual([], { type: "R", question: 9, answer: "", text: "t" }, "").appeal, id: 2 } },
  ];
  const { appeal, rank } = A.addManual(held, { type: "R", question: 5, answer: "лишний", text: " снять \n" }, "2026-10-19T12:00:00Z");
  assert.equal(appeal.id, 3);
  assert.equal(appeal.answer, "", "a removal asks for no answer");
  assert.equal(appeal.text, "снять");
  assert.ok("a0" < rank && rank < "a1", rank);
  assert.deepEqual(A.parseAppeal(A.composeAppeal(appeal)), appeal);
});
