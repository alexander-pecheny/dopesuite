// The Timeline payload codec against internal/xycli/testdata/eventpayload.json,
// the corpus internal/xycli/eventpayload_test.go reads too: the same stored
// rows (historic shapes included) decode to the same values, and the same
// values encode to the same bytes, in the browser and in xy-cli.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCrypto } from "../web/assets/static/dist/crypto.js";
import {
  UNREADABLE, commentText, decodePayload, encodePayload, openPayload, openPayloads, remapCommentImages, sealPayload,
} from "../web/assets/static/dist/eventpayload.js";
import fixture from "../internal/xycli/testdata/eventpayload.json" with { type: "json" };

for (const c of fixture.decode) {
  test(`decode: ${c.name}`, () => {
    assert.deepEqual(decodePayload(c.kind, c.raw), c.want);
  });
}

for (const c of fixture.encode) {
  test(`encode: ${c.name}`, () => {
    const raw = encodePayload(c.kind, c.data);
    assert.equal(raw, c.raw);
    assert.deepEqual(decodePayload(c.kind, raw), { kind: c.kind, ...c.data });
  });
}

async function testKey(byte) {
  const raw = new Uint8Array(32).fill(byte);
  return { key: await xyCrypto._importDK(raw), raw };
}

test("every kind survives seal and open under a test key", async () => {
  const dk = await testKey(7);
  for (const c of fixture.encode) {
    const b64 = await sealPayload(dk, c.kind, c.data);
    assert.deepEqual(await openPayload(dk, c.kind, b64), { kind: c.kind, ...c.data }, c.name);
  }
});

test("a payload that will not open is unreadable, never a throw", async () => {
  const dk = await testKey(7);
  const sealed = await sealPayload(dk, "reaction", { emoji: "x" });
  assert.deepEqual(await openPayload(await testKey(8), "reaction", sealed), UNREADABLE);
  assert.deepEqual(await openPayload(null, "reaction", sealed), UNREADABLE);
  const run = await openPayloads(dk, [
    { type: "comment", payload_enc: await sealPayload(dk, "comment", { text: "hi", images: [] }) },
    { type: "comment", payload_enc: "" },
    { type: "comment" },
  ]);
  assert.deepEqual(run, [{ kind: "comment", text: "hi", images: [] }, UNREADABLE, UNREADABLE]);
});

test("commentText gives a comment's words and a reaction's emoji, nothing else", () => {
  assert.equal(commentText(decodePayload("comment", '{"xy":1,"t":"words","img":[1]}')), "words");
  assert.equal(commentText({ kind: "reaction", emoji: "x" }), "x");
  assert.equal(commentText(decodePayload("label_add", '{"label":"l"}')), "");
  assert.equal(commentText(UNREADABLE), "");
});

test("remapCommentImages points images at the copies and drops the ones not copied", () => {
  const ids = new Map([[5, 105], [6, 106]]);
  assert.equal(remapCommentImages('{"xy":1,"t":"глянь","img":[5,7,6]}', ids), '{"xy":1,"t":"глянь","img":[105,106]}');
  assert.equal(remapCommentImages('{"xy":1,"t":"глянь","img":[7]}', ids), "глянь");
  assert.equal(remapCommentImages("просто текст", ids), "просто текст");
  assert.equal(remapCommentImages('{"img":[5]}', ids), '{"img":[5]}', "a hand-typed JSON comment is not the envelope");
});
