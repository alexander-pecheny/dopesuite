// The «🧪 Тесты» panel on the DOM shim: the list sorts by date, the form reads
// back as a patch only when something changed, and a delete closes the form.
import { test } from "node:test";
import assert from "node:assert/strict";
import { fakeStack, installDOM } from "./dom.js";

const p = installDOM(["sessionsList", "sessionForm", "sessionAddBtn", "sessionsOverlay", "sessionEditOverlay", "sessionEditMessage"]);
for (const id of ["sessionsOverlay", "sessionEditOverlay"]) p.node(id).hidden = true;
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.confirm = () => true;
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { createModal } = await import("../web/assets/static/dist/modal.js");
const { parseSession, serializeSession } = await import("../web/assets/static/dist/sessions.js");
const { createSessionsPanel } = await import("../web/assets/static/dist/sessionspanel.js");

const meta = (date, title, testers = []) => serializeSession({ date, time: "", tz: "Europe/Moscow", title, testers, cities: [], key: "k-" + date });
const sessions = [
  { id: 1, meta: meta("2026-08-01", "Ранний", [{ text: "Аня", type: "player" }]) },
  { id: 2, meta: meta("2026-08-10", "Поздний", [{ text: "Сборная", type: "team" }, { text: "Боря", type: "player" }]) },
];
const log = [];
let activeTest = null;
const stack = fakeStack();
// leave is what the page's stack does on a back/close: ask the frame's gate, then pop.
const leave = async () => { const f = stack.frames.at(-1); if (!f.confirm || await f.confirm()) await stack.pop(); };
const panel = createSessionsPanel({
  boardId: 7,
  el: xyApp.el,
  byId: p.byId,
  sessions: () => sessions,
  boardName: () => "Доска",
  defaultTimezone: () => "Europe/Moscow",
  defaultCities: () => [],
  playedCount: (id) => (id === 2 ? 12 : 0),
  createSession: async (m) => { log.push(["create", m]); sessions.push({ id: 3, meta: m }); return 3; },
  patchSession: async (id, m) => { log.push(["patch", id, m]); sessions.find((s) => s.id === id).meta = m; },
  deleteSession: async (id) => { log.push(["delete", id]); },
  copyText: async (t) => { log.push(["copy", t]); },
  activeTestSession: () => activeTest,
  setTestMode: (id) => { activeTest = id; log.push(["testmode", id]); },
  loadNotes: async () => [],
  addNote: async () => {},
  modal: (stem) => createModal(stem, { byId: p.byId, stack }),
  render: () => log.push(["render"]),
});

test("the list shows the newest test first with its counts, and copies the invite line", () => {
  panel.open();
  const rows = p.node("sessionsList").querySelectorAll(".sess-row");
  assert.equal(rows.length, 2);
  assert.deepEqual(p.node("sessionsList").querySelectorAll(".sess-title").map((n) => n.textContent), ["10 августа · Поздний", "1 августа · Ранний"]);
  assert.deepEqual(p.node("sessionsList").querySelectorAll(".sess-meta").map((n) => n.textContent), ["1 игр., 1 ком. · 12 вопр.", "1 игр."]);
  rows[0].querySelectorAll(".input")[1].fire("click");
  assert.equal(log.at(-1)[0], "copy");
  assert.ok(log.at(-1)[1].includes("10 августа"), log.at(-1)[1]);
  assert.equal(p.node("sessionsOverlay").hidden, false);
});

test("the row's ▶ starts test mode for its session, and reads ⏹ once it is on", () => {
  const toggle = () => p.node("sessionsList").querySelectorAll(".sess-row")[0]
    .querySelectorAll("[aria-pressed]")[0];
  panel.open();
  assert.equal(toggle().getAttribute("aria-pressed"), "false");
  toggle().fire("click");
  assert.deepEqual(log.at(-1), ["testmode", 2]); // the newest session sorts first
  assert.equal(toggle().getAttribute("aria-pressed"), "true");
  toggle().fire("click");
  assert.deepEqual(log.at(-1), ["testmode", null]);
  activeTest = null;
});

test("leaving an untouched form sends nothing; a retitled one is patched and the board re-rendered", async () => {
  log.length = 0;
  panel.openSession(1);
  assert.equal(p.node("sessionEditOverlay").hidden, false);
  await leave();
  assert.deepEqual(log, []);
  panel.openSession(1);
  const title = p.node("sessionForm").querySelectorAll(".input").find((n) => n.value === "Ранний");
  title.value = "Переименованный";
  await leave();
  assert.equal(log[0][0], "patch");
  assert.equal(log[0][1], 1);
  assert.equal(parseSession(log[0][2]).title, "Переименованный");
  assert.deepEqual(parseSession(log[0][2]).testers, [{ text: "Аня", type: "player" }]);
  assert.deepEqual(log[1], ["render"]);
});

test("«Удалить тест» asks, deletes and closes the form without a save", async () => {
  log.length = 0;
  panel.openSession(2);
  p.node("sessionForm").querySelector(".btn-danger").fire("click");
  await new Promise((r) => setTimeout(r, 5));
  assert.deepEqual(log.map((e) => e[0]), ["delete", "render"]);
  assert.equal(p.node("sessionEditOverlay").hidden, true);
});
