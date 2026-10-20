import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

const ids = ["tgExportOverlay", "tgExportForm", "tgExportToken", "tgExportChannel", "tgExportRun", "tgExportCancel", "tgExportMessage"];
const p = installDOM(ids);
p.node("tgExportOverlay").hidden = true;

// The store is IndexedDB in the browser; here it is a map, so the test can read
// back exactly what the modal decided to remember.
const { xyStore } = await import("../web/assets/static/dist/store.js");
const kept = new Map();
xyStore.getTgBot = async () => kept.get("bot");
xyStore.putTgBot = async (t) => { kept.set("bot", t); };
xyStore.getTgTarget = async (id) => kept.get("target:" + id);
xyStore.putTgTarget = async (id, t) => { kept.set("target:" + id, t); };

const { createTelegramExport } = await import("../web/assets/static/dist/tgexport.js");

// stream answers with the given NDJSON lines, in one chunk per line, as the
// endpoint does.
function stream(lines) {
  const chunks = lines.map((l) => new TextEncoder().encode(JSON.stringify(l) + "\n"));
  let i = 0;
  return {
    ok: true,
    body: { getReader: () => ({ read: async () => (i < chunks.length ? { done: false, value: chunks[i++] } : { done: true }) }) },
  };
}

let sent = [];
function panel(lines) {
  sent = [];
  return createTelegramExport({
    boardId: () => 7,
    post: async (body) => { sent.push(body); return stream(lines); },
  });
}

function fill(token, channel) {
  p.node("tgExportToken").value = token;
  p.node("tgExportChannel").value = channel;
}

test("the notes arrive as they are streamed, and the resolved id is kept", async () => {
  const tg = panel([
    { note: "Подключаемся к боту…" },
    {},
    { note: "Перешлите боту любое сообщение из канала «@pack»." },
    { done: true, channel: "-1001111" },
  ]);
  await tg.open(new FormData());
  fill("12345:secret", "@pack");
  await tg.run();

  const log = p.node("tgExportMessage").textContent;
  assert.match(log, /Подключаемся/);
  assert.match(log, /Перешлите боту/);
  assert.match(log, /Опубликовано\./);
  assert.equal(kept.get("bot"), "12345:secret");
  assert.deepEqual(kept.get("target:7"), { channel: "@pack", channelId: "-1001111" });
});

test("the second export of the same package sends the id, not the name", async () => {
  const tg = panel([{ done: true, channel: "-1001111" }]);
  await tg.open(new FormData());
  assert.equal(p.node("tgExportChannel").value, "@pack", "what was typed comes back");
  await tg.run();
  assert.equal(sent[0].get("channel"), "-1001111");
  assert.equal(sent[0].get("chat"), null, "the server finds the group itself");
});

test("renaming the channel drops the remembered id — it belongs to the old one", async () => {
  const tg = panel([{ done: true, channel: "-1003333" }]);
  await tg.open(new FormData());
  fill("12345:secret", "@other");
  await tg.run();
  assert.equal(sent[0].get("channel"), "@other");
  assert.deepEqual(kept.get("target:7"), { channel: "@other", channelId: "-1003333" });
});

test("a stream that stops without a last line says so: some questions may be out", async () => {
  const tg = panel([{ note: "Публикуем…" }]);
  await tg.open(new FormData());
  fill("12345:secret", "@pack");
  await tg.run();
  assert.match(p.node("tgExportMessage").textContent, /Связь оборвалась/);
});

test("an incomplete form posts nothing", async () => {
  const tg = panel([{ done: true }]);
  await tg.open(new FormData());
  fill("12345:secret", "");
  await tg.run();
  assert.equal(sent.length, 0);
});
