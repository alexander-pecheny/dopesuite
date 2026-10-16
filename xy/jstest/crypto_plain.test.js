// The plaintext key: what a local xy (the desktop app's) uses for every board,
// whose content columns hold the bytes themselves.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCrypto } from "../web/assets/static/dist/crypto.js";
import { xyInstance } from "../web/assets/static/dist/instance.js";

const te = new TextEncoder();

test("plaintext key passes fields through as base64 of their UTF-8", async () => {
  const dk = xyCrypto.PLAINTEXT_KEY;
  const b64 = await xyCrypto.encField(dk, "Вопрос 1. Ответ: Толстой.");
  assert.deepEqual(xyCrypto.fromB64(b64), te.encode("Вопрос 1. Ответ: Толстой."));
  assert.equal(await xyCrypto.decField(dk, b64), "Вопрос 1. Ответ: Толстой.");
  assert.deepEqual(await xyCrypto.decFields(dk, [b64, ""]), ["Вопрос 1. Ответ: Толстой.", ""]);
});

test("plaintext key passes bytes through", async () => {
  const bytes = te.encode("PNG");
  assert.deepEqual(await xyCrypto.encBytes(xyCrypto.PLAINTEXT_KEY, bytes), bytes);
  assert.deepEqual(await xyCrypto.decBytes(xyCrypto.PLAINTEXT_KEY, bytes), bytes);
});

test("a plain keymeta unlocks with no passphrase; an encrypted one is not plain", async () => {
  const plain = xyCrypto.plainKeymeta();
  assert.equal(plain.kdf_params, '{"kdf":"none"}');
  assert.ok(xyCrypto.isPlainKeymeta(plain));
  assert.equal(await xyCrypto.unlockBoard("", plain), xyCrypto.PLAINTEXT_KEY);
  const { keymeta } = await xyCrypto.createBoardKeys("correct horse battery");
  assert.ok(!xyCrypto.isPlainKeymeta(keymeta));
  assert.ok(!xyCrypto.isPlainKeymeta({ ...keymeta, kdf_params: "not json" }));
});

test("on a local xy every board is plaintext: new keys, the cached key, and no cache write", async () => {
  xyInstance.setInstance({ mode: "local", off: ["passphrase", "offline"] });
  try {
    const { keymeta, dk } = await xyCrypto.createBoardKeys("ignored");
    assert.ok(xyCrypto.isPlainKeymeta(keymeta));
    assert.equal(dk, xyCrypto.PLAINTEXT_KEY);
    assert.equal(await xyCrypto.loadCachedDK(7), xyCrypto.PLAINTEXT_KEY);
    await xyCrypto.cacheDK(7, dk); // would throw without IndexedDB if it tried
  } finally {
    xyInstance.setInstance(null);
  }
});
