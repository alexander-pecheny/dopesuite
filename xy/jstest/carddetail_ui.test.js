// The card detail on the DOM shim: it opens on a card, shows version 1, keeps
// Сохранить off until the draft changes, and closes. What it needs from the
// board is faked; the nodes come through the ui record.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeNode, installDOM } from "./dom.js";

const ids = ["cardOverlay", "cardDetailTitle", "cardKind", "cardLink", "cardCopy", "cardCopyMsg", "cardSave", "cardDelete", "cardClose", "cardMessage",
  "cardAlias", "cardAddVersion", "cardVersions", "cardInsStress", "cardTypo", "cardTo4s", "cardEditTools", "cardViewTabs", "cardViewText", "cardViewPreview",
  "cardViewFields", "cardFields", "cardDesc", "cardDescLabel", "cardPreviewScreen", "cardPreviewBody", "copyBtn", "contentUnreadDot", "commentsUnreadDot",
  "timeline", "cardTabPreview", "cardTabFields", "cardTabText", "dirtyOverlay", "dirtyMessage", "dirtySave", "dirtyDiscard", "dirtyCancel",
  "moveBoard", "moveList", "movePos", "moveBtn", "previewOverlay", "previewBody"];
const p = installDOM(ids);
for (const id of ["cardOverlay", "dirtyOverlay", "previewOverlay"]) p.node(id).hidden = true;
globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => [], text: async () => "[]", headers: { get: () => null } });
globalThis.IntersectionObserver = class { observe() {} disconnect() {} };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.navigator = { clipboard: { writeText: async () => {} } };
globalThis.performance = { now: () => 0 };
globalThis.requestAnimationFrame = (fn) => setTimeout(fn, 0);
globalThis.matchMedia = () => ({ matches: false, addEventListener() {} });

const { createCardDetail } = await import("../web/assets/static/dist/carddetail.js");

function ui() {
  const n = (id) => p.node(id);
  const detail = fakeNode("div", { className: "card-detail" });
  return {
    overlay: n("cardOverlay"), detail, title: n("cardDetailTitle"), kind: n("cardKind"), link: n("cardLink"), copy: n("cardCopy"), copyMsg: n("cardCopyMsg"),
    save: n("cardSave"), del: n("cardDelete"), close: n("cardClose"), message: n("cardMessage"), alias: n("cardAlias"), addVersion: n("cardAddVersion"),
    versions: n("cardVersions"), insStress: n("cardInsStress"), typo: n("cardTypo"), to4s: n("cardTo4s"), editTools: n("cardEditTools"), viewTabs: n("cardViewTabs"),
    viewText: n("cardViewText"), viewPreview: n("cardViewPreview"), viewFields: n("cardViewFields"), fields: n("cardFields"), desc: n("cardDesc"), descLabel: n("cardDescLabel"),
    previewScreen: n("cardPreviewScreen"), previewBody: n("cardPreviewBody"), copyBtn: n("copyBtn"), contentUnreadDot: n("contentUnreadDot"), commentsUnreadDot: n("commentsUnreadDot"),
    timeline: n("timeline"), tabs: { preview: n("cardTabPreview"), fields: n("cardTabFields"), text: n("cardTabText") },
    dirty: { overlay: n("dirtyOverlay"), message: n("dirtyMessage"), save: n("dirtySave"), discard: n("dirtyDiscard"), cancel: n("dirtyCancel") },
    move: { board: n("moveBoard"), list: n("moveList"), pos: n("movePos"), btn: n("moveBtn") },
    listPreview: { overlay: n("previewOverlay"), body: n("previewBody") },
  };
}

function setup() {
  const card = { id: 5, listId: 1, kind: "question", rank: "a", desc: "(hidden-comment xy-version:)\n? Первая?\n! Раз\n(hidden-comment xy-version: полегче)\n? Вторая?\n! Два", handoutMeta: null, alias: null, createdAt: "2026-01-01" };
  const state = { name: "Доска", lists: [{ id: 1, title: "Тур", rank: "a", groupId: null }], cards: [card], labels: [], cardLabels: [], cardSessions: [], sessions: [], unread: {}, defaultAuthor: "" };
  const log = [];
  const cd = createCardDetail({
    boardId: 7,
    ui: ui(),
    getState: () => state,
    getDK: () => ({ key: "K" }),
    verbs: { create: async () => ({ id: 9 }), patch: async (k, path, body) => { log.push(["patch", path, body]); }, put: async () => {}, del: async () => {} },
    setStatus() {},
    render() {},
    cardsOf: (id) => state.cards.filter((c) => c.listId === id),
    labelById: () => undefined,
    renderLabelPicker() {},
    paintLabels() {},
    questionNumberFor: () => "1",
    popupMenu() {},
    forgetCardLabels() {},
    preview: { renderPreviewCard: () => fakeNode("div", { className: "pv" }), resolveImages: async () => new Map(), imageRefs: () => new Set(), fillPreviewImages() {}, previewList: async () => {} },
    attachments: { load: async () => {}, imageNames: () => [], clearImageNames() {}, cardAttachments: async () => [], resolveImages: async () => new Map(), attachmentUrl: async () => "", download: async () => {}, imageBlob: async () => null },
    readMarkers: { refreshCardUnreadDot() {}, renderNotifBadge() {} },
    timeline: { load: async () => {}, events: () => [], resetFilter() {}, readBuckets: () => ({}), ensureVisible() {}, commentDraft: () => "", postComment: async () => {}, clearCommentDraft() {} },
    transfer: { moveBoardOptions: async () => [], loadMoveBoard: async () => ({ boardId: 1, dk: {}, lists: [], cardsByList: new Map(), labels: [], sessions: [], name: "" }), transferCard: async () => 0 },
  });
  return { cd, card, state, log };
}

test("a card opens on version 1 with Сохранить off; the draft turns it on; closing hides the overlay", async () => {
  const { cd, card } = setup();
  await cd.openCard(card);
  assert.equal(p.node("cardOverlay").hidden, false);
  assert.equal(p.node("cardDesc").value, "? Первая?\n! Раз", "the editor holds version 1's body");
  assert.equal(p.node("cardKind").value, "question");
  assert.equal(cd.openCardId(), 5);
  assert.equal(p.node("cardSave").disabled, true, "nothing changed yet");
  assert.equal(p.node("cardSave").hidden, true, "and Просмотр is read-only, so the button hides");
  p.node("cardTabText").fire("click");
  assert.equal(p.node("cardSave").hidden, false);
  p.node("cardDesc").value = "? Первая, но иначе?\n! Раз";
  p.node("cardDesc").fire("input");
  assert.equal(p.node("cardSave").disabled, false, "a changed draft can be saved");
  p.node("cardTabPreview").fire("click");
  assert.equal(p.node("cardSave").hidden, false, "a dirty draft keeps the button visible on Просмотр");
  cd.closeCard();
  await new Promise((r) => setTimeout(r, 0));
});
