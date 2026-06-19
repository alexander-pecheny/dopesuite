// Crypto round-trip + envelope tests. Run with: node --test jstest/
// (or `just test-js`). Uses node's built-in test runner + global WebCrypto.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCrypto } from "../web/assets/static/crypto.js";

test("board create → unlock round-trips a field", async () => {
  const { keymeta, dk } = await xyCrypto.createBoardKeys("correct horse");
  const ct = await xyCrypto.encField(dk, "первый вопрос");
  // a fresh unlock with the same passphrase yields a working DK
  const dk2 = await xyCrypto.unlockBoard("correct horse", keymeta);
  assert.equal(await xyCrypto.decField(dk2, ct), "первый вопрос");
});

test("wrong passphrase is rejected", async () => {
  const { keymeta } = await xyCrypto.createBoardKeys("right");
  await assert.rejects(() => xyCrypto.unlockBoard("wrong", keymeta), /Неверный пароль/);
});

test("envelope has magic + alg header and is non-deterministic", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const a = await xyCrypto._seal(dk.key, new TextEncoder().encode("x"));
  const b = await xyCrypto._seal(dk.key, new TextEncoder().encode("x"));
  assert.deepEqual([...a.subarray(0, 3)], [...new TextEncoder().encode("xy1")]);
  assert.equal(a[3], 1); // alg = AES-GCM
  assert.notDeepEqual([...a], [...b]); // random nonce → different ciphertext
});

test("tampered ciphertext fails authentication", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const env = await xyCrypto._seal(dk.key, new TextEncoder().encode("secret"));
  env[env.length - 1] ^= 0xff; // flip a tag byte
  await assert.rejects(() => xyCrypto._open(dk.key, env));
});

test("passphrase change re-wraps the same DK (no data re-encrypt)", async () => {
  const { keymeta, dk } = await xyCrypto.createBoardKeys("old pass");
  const ct = await xyCrypto.encField(dk, "данные");
  const rewrap = await xyCrypto.rewrapKey("new pass", dk);
  const newKeymeta = { ...keymeta, ...rewrap };
  const dk2 = await xyCrypto.unlockBoard("new pass", newKeymeta);
  // old ciphertext still decrypts because DK is unchanged
  assert.equal(await xyCrypto.decField(dk2, ct), "данные");
  await assert.rejects(() => xyCrypto.unlockBoard("old pass", newKeymeta));
});

test("bytes round-trip for attachments", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const data = new Uint8Array([0, 1, 2, 250, 255]);
  const env = await xyCrypto.encBytes(dk, data);
  assert.deepEqual([...(await xyCrypto.decBytes(dk, env))], [...data]);
});
