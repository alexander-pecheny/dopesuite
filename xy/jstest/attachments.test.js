import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./dom.js";

// The kernel over a fake page: the list render, the per-card cache and the
// decrypted-bytes LRU, with the network and the crypto stubbed at their seams.
const p = installDOM(["cardMessage", "attachments", "attachUpload", "attachFile", "attachCompress", "cardOverlay", "pasteForm", "pasteName", "pasteCompress", "pasteOverlay"]);
const { xyApp } = await import("../web/assets/static/dist/app.js");
const { xyCrypto } = await import("../web/assets/static/dist/crypto.js");
const { xySync } = await import("../web/assets/static/dist/sync.js");

const fetched = [];
const server = {
  "/api/cards/10/attachments": [
    { id: 1, filename_enc: "e:схема.png", mime: "image/png", size: 2048, rev: 0 },
    { id: 2, filename_enc: "e:текст.pdf", mime: "application/pdf", size: 3 * 1024 * 1024, is_excerpt: true },
  ],
};
xyApp.fetchJSON = async (url) => { fetched.push(url); return server[url]; };
xyCrypto.decField = async (_k, s) => s.replace(/^e:/, "");
xyCrypto.decBytes = async (_k, bytes) => bytes;
xySync.getAttachment = async () => null;
xySync.putAttachment = async () => {};
globalThis.fetch = async (url) => { fetched.push(url); return { ok: true, arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer }; };
let urls = 0;
globalThis.URL = { createObjectURL: () => `blob:${++urls}`, revokeObjectURL() {} };
globalThis.Blob = class { constructor(parts, opts) { this.parts = parts; this.type = opts?.type; } };
const { create, extFromMime, gatherTargets, humanSize, withExt } = await import("../web/assets/static/dist/attachments.js");

test("gatherTargets picks the first attachment per wanted name, in card order", () => {
  const lists = [
    [{ id: 1, name: "a.png" }, { id: 2, name: "b.png" }],
    [{ id: 3, name: "a.png" }, { id: 4, name: "" }, { id: 5, name: "c.png" }],
  ];
  const targets = gatherTargets(lists, new Set(["a.png", "c.png", "missing.png"]));
  assert.deepEqual([...targets.keys()].sort(), ["a.png", "c.png"]);
  assert.equal(targets.get("a.png").id, 1);
  assert.equal(targets.get("c.png").id, 5);
});

test("humanSize scales through B/KB/MB", () => {
  assert.equal(humanSize(512), "512 B");
  assert.equal(humanSize(2048), "2.0 KB");
  assert.equal(humanSize(3 * 1024 * 1024), "3.0 MB");
});

test("extFromMime maps known types and sanitizes the rest", () => {
  assert.equal(extFromMime("image/jpeg"), "jpg");
  assert.equal(extFromMime("image/svg+xml"), "svg");
  assert.equal(extFromMime("image/x-weird!"), "xweird");
  assert.equal(extFromMime(""), "png");
});

test("withExt replaces any typed extension with the stored format's", () => {
  assert.equal(withExt("схема.jpeg", "webp"), "схема.webp");
  assert.equal(withExt("noext", "png"), "noext.png");
  assert.equal(withExt("  ", "webp"), "вставка.webp");
});


const att = create({
  ui: {
    message: p.node("cardMessage"), list: p.node("attachments"), upload: p.node("attachUpload"), file: p.node("attachFile"), compress: p.node("attachCompress"),
    cardOverlay: p.node("cardOverlay"), pasteForm: p.node("pasteForm"), pasteName: p.node("pasteName"), pasteCompress: p.node("pasteCompress"),
  },
  mustDK: () => ({ key: "K" }),
  openCardId: () => 10,
  popupMenu() {},
  timeline: { load: async () => {}, setAttachments() {} },
});

test("load renders a row per attachment with the decrypted name and remembers the images", async () => {
  await att.load(10);
  const rows = p.node("attachments").querySelectorAll(".attach-row");
  assert.equal(rows.length, 2);
  assert.deepEqual(p.node("attachments").querySelectorAll(".attach-size").map((n) => n.textContent), ["2.0 KB", "3.0 MB"]);
  assert.equal(p.node("attachments").querySelectorAll(".tl-badge").length, 1);
  assert.deepEqual(att.imageNames(), ["схема.png"]);
});

test("the list is cached per card until a load refreshes it; bytes and URLs are fetched once per id:rev", async () => {
  fetched.length = 0;
  await att.cardAttachments(10);
  await att.cardAttachments(10);
  assert.deepEqual(fetched, []);
  const a = { id: 1, mime: "image/png", rev: 0 };
  const u1 = await att.attachmentUrl(a);
  const u2 = await att.attachmentUrl(a);
  assert.equal(u1, u2);
  assert.deepEqual(fetched, ["/api/attachments/1"]);
  const u3 = await att.attachmentUrl({ ...a, rev: 1 });
  assert.notEqual(u3, u1);
  assert.deepEqual(fetched, ["/api/attachments/1", "/api/attachments/1"]);
  await att.load(10);
  assert.ok(fetched.includes("/api/cards/10/attachments"));
});
