import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeBoard, fakeNode, installDOM } from "./dom.js";

const p = installDOM(["importPickOverlay", "importPickForm", "importFile", "importPickHint", "importPickWhat", "importGame", "importSplitTours", "importPickCancel", "importOverlay", "importTitle", "importSource", "importPreview", "importCount", "importCommit", "importClose"]);
for (const id of ["importPickOverlay", "importOverlay"]) p.node(id).hidden = true;
p.node("importPickForm").reset = () => {};
// The server's parse of the upload, then the lists and cards the client posts.
const posts = [];
let parsed = { name: "Пакет", source: "", images: [] };
// What the desktop app's dialog hands back, and the upload the page then sends.
let pickedFiles = [];
let parseBody = null;
globalThis.fetch = async (url, init) => {
  if (String(url).endsWith("/api/import/pick")) return { ok: true, status: 200, json: async () => ({ files: pickedFiles }), text: async () => "" };
  if (String(url).endsWith("/api/import/parse")) {
    parseBody = init.body;
    return { ok: true, status: 200, json: async () => parsed, text: async () => "" };
  }
  posts.push([String(url), init && init.body ? JSON.parse(init.body) : null]);
  return { ok: true, status: 200, json: async () => ({ id: 100 + posts.length }), text: async () => "" };
};
globalThis.prompt = () => "Импорт";
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
xyCrypto.encField = async (_k, s) => "e:" + s;
const { xySync } = await import("../web/assets/static/dist/sync.js");
xySync.requireOnline = () => true;
const { createImportPanel } = await import("../web/assets/static/dist/importpack.js");

const board = fakeBoard({ lists: [{ id: 1, title: "Тур 1", rank: "a0", groupId: null }] });
const panel = createImportPanel(board, () => fakeNode("div"));
const settle = () => new Promise((r) => setTimeout(r, 10));

test("a .4s imports straight into one new list, one card per block, kinds by their fields", async () => {
  parsed = { name: "Пакет", source: "### Тур\n\n? Раз?\n! А\n\n# просто мета\n\n? Два?\n! Б\n", images: [] };
  panel.open();
  assert.equal(p.node("importPickOverlay").hidden, false);
  p.node("importFile").files = [new File([], "pack.4s")];
  p.node("importSplitTours").checked = false;
  p.node("importPickForm").fire("submit");
  await settle();
  assert.equal(p.node("importPickOverlay").hidden, true);
  assert.deepEqual(posts.map((x) => x[0]), ["/api/boards/7/lists", "/api/lists/101/cards", "/api/lists/101/cards", "/api/lists/101/cards", "/api/lists/101/cards"]);
  assert.equal(posts[0][1].title, "e:Импорт");
  assert.deepEqual(posts.slice(1).map((x) => x[1].kind), ["heading", "question", "meta", "question"]);
  assert.equal(board.state.lists.length, 2);
  assert.equal(board.state.cards.length, 4);
  assert.ok(board.state.lists[1].rank > "a0", "the new list lands after the last");
  assert.equal(globalThis.__alerts.at(-1), "Импортировано: 4 карточек, 0 изображений.");
});

test("split by tours makes one list per «## …» section and links them into a group, then reloads", async () => {
  posts.length = 0;
  parsed = { name: "Пакет", source: "## Тур 1\n\n? Раз?\n! А\n\n## Тур 2\n\n? Два?\n! Б\n", images: [] };
  panel.open();
  p.node("importFile").files = [new File([], "pack.4s")];
  p.node("importSplitTours").checked = true;
  p.node("importPickForm").fire("submit");
  await settle();
  const lists = posts.filter((x) => x[0] === "/api/boards/7/lists").map((x) => x[1].title);
  assert.deepEqual(lists, ["e:Тур 1", "e:Тур 2"]);
  const group = posts.find((x) => x[0] === "/api/boards/7/list-groups");
  assert.equal(group[1].name, "e:Импорт");
  assert.equal(board.reloads, 1);
});

// A СИ package is one card per тема, not per question: every block after a
// "#T …" belongs to it until the next one, and the list it lands in is typed СИ
// so «Добавить карточку» goes on making themes.
test("СИ import folds each #T block and its questions into one theme card", async () => {
  posts.length = 0;
  parsed = {
    name: "ЭК",
    source: [
      "### Чемпионат",
      "",
      "#T Острова",
      "",
      "№ 10",
      "? Раз?",
      "! А",
      "",
      "№ 20",
      "? Два?",
      "! Б",
      "",
      "#T Оружие",
      "",
      "№ 10",
      "? Три?",
      "! В",
      "",
    ].join("\n"),
    images: [],
  };
  panel.open();
  p.node("importFile").files = [new File([], "ek.docx")];
  p.node("importGame").value = "si";
  p.node("importSplitTours").checked = false;
  p.node("importPickForm").fire("submit");
  await settle();
  // .docx opens the verification screen first; commit from there.
  p.node("importSource").value = parsed.source;
  p.node("importCommit").fire("click");
  await settle();

  const [list, ...cards] = posts;
  assert.equal(list[0], "/api/boards/7/lists");
  assert.equal(list[1].type, "si");
  assert.deepEqual(cards.map((c) => c[1].kind), ["heading", "theme", "theme"]);
  assert.match(cards[1][1].description, /^e:#T Острова/);
  assert.match(cards[1][1].description, /№ 20/);
  assert.match(cards[2][1].description, /^e:#T Оружие/);
});

// A troika's themes sit inside battles and sections, and the host's notes sit
// between them. A note above a theme's first question is the theme's; a battle,
// a section, or a note after the last question is a card of its own.
test("troika import keeps battles, sections and trailing notes out of the themes", async () => {
  posts.length = 0;
  parsed = {
    name: "Сова",
    source: [
      "## Групповой этап 1", "",
      "#B Бой 1", "",
      "#T ТЕМА 1. Числа", "", "@ Автор", "", "# Ведущему: прочтите медленно", "",
      "№ 1", "? Раз?", "! А", "", "№ 2", "? Два?", "! Б", "",
      "# Ведущему: смена порядка", "",
      "#T ТЕМА 2. Буквы", "", "№ 1", "? Три?", "! В", "",
      "#B Бой 2", "",
      "#T ТЕМА 1. Звуки", "", "№ 1", "? Четыре?", "! Г", "",
    ].join("\n"),
    images: [],
  };
  panel.open();
  p.node("importFile").files = [new File([], "sova.tr4s")];
  p.node("importGame").value = "normal";
  p.node("importSplitTours").checked = false;
  p.node("importPickForm").fire("submit");
  await settle();
  assert.equal(parseBody.get("game"), "troika", "the extension names the game, whatever was picked");
  const [list, ...cards] = posts;
  assert.equal(list[1].type, "troika");
  assert.deepEqual(cards.map((c) => c[1].kind), ["heading", "battle", "troika_theme", "meta", "troika_theme", "battle", "troika_theme"]);
  assert.match(cards[2][1].description, /прочтите медленно[\s\S]*№ 2/);
  assert.doesNotMatch(cards[2][1].description, /смена порядка/);
});

// A brain package is ChGK questions in battles: each battle a card, the
// questions their own, and the list typed brain.
test("brain import makes battle cards between ordinary questions", async () => {
  posts.length = 0;
  parsed = { name: "Брейн", source: "#B Бой 1\n\n? Раз?\n! А\n\n#B Бой 2\n\n? Два?\n! Б\n", images: [] };
  panel.open();
  p.node("importFile").files = [new File([], "brain.4s")];
  p.node("importGame").value = "brain";
  p.node("importSplitTours").checked = false;
  p.node("importPickForm").fire("submit");
  await settle();
  assert.equal(parseBody.get("game"), "brain");
  const [list, ...cards] = posts;
  assert.equal(list[1].type, "brain");
  assert.deepEqual(cards.map((c) => c[1].kind), ["battle", "question", "battle", "question"]);
});

test("in the desktop app the native pick stands in for the file field, and its pictures go up with it", async () => {
  posts.length = 0;
  const { xyInstance } = await import("../web/assets/static/dist/instance.js");
  xyInstance.setInstance({ mode: "local", off: [], pick: true });
  pickedFiles = [
    { name: "тур.4s", mime: "application/octet-stream", data: btoa("x") },
    { name: "a.png", mime: "image/png", data: btoa("png") },
  ];
  parsed = { name: "тур", source: "? Раз (img a.png) (img b.png)\n! А\n", images: [] };
  panel.open();
  await settle();
  assert.equal(p.node("importPickOverlay").hidden, false);
  assert.equal(p.node("importFile").hidden, true);
  assert.match(p.node("importPickWhat").textContent, /«тур\.4s», картинок рядом с ним: 1/);
  p.node("importSplitTours").checked = false;
  p.node("importGame").value = "normal";
  p.node("importPickForm").fire("submit");
  await settle();
  assert.deepEqual(parseBody.getAll("image").map((f) => f.name), ["a.png"]);
  assert.match(globalThis.__alerts.at(-1), /Не нашлось картинок \(2\): a\.png, b\.png$/);
  xyInstance.setInstance(null);
});
