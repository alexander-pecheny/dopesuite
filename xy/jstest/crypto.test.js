// Crypto round-trip + envelope tests. Run with: node --test jstest/
// (or `just test-js`). Uses node's built-in test runner + global WebCrypto.
//
// These run on the in-thread path: there is no origin to load a worker script
// from under deno, so crypto.ts derives the KEK itself — which is also the
// fallback every browser keeps when a worker will not start. The worker is the
// same scrypt behind a postMessage; its client's contract is covered in
// cryptoworkerclient.test.js.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCrypto } from "../web/assets/static/dist/crypto.js";

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

test("validatePassphrase enforces length and word count on set", () => {
  const ok = xyCrypto.validatePassphrase;
  // Clears the floor (>=16 chars, >=3 non-empty words) on each separator.
  assert.equal(ok("alpha bravo charlie"), null);
  assert.equal(ok("alpha-bravo_charlie-x"), null);
  // Too short even with enough words.
  assert.match(ok("one two three"), /16 символов/);
  // Long enough but too few words (a single 16-char token).
  assert.match(ok("abcdefghijklmnop"), /минимум 3 слова/);
  // Trailing/repeated separators do not invent words.
  assert.match(ok("aaaaaaaaaaaaaaaa----"), /минимум 3 слова/);
  // Empty.
  assert.match(ok(""), /16 символов/);
});

test("generatePassphrase yields valid, varied, unlockable passphrases", async () => {
  const gen = xyCrypto.generatePassphrase;
  const seen = new Set();
  for (let i = 0; i < 200; i++) {
    const p = gen();
    assert.equal(p.split("-").length, 4);          // default 4 words
    assert.equal(xyCrypto.validatePassphrase(p), null); // clears the floor
    seen.add(p);
  }
  assert.ok(seen.size > 190, "generated passphrases should almost never collide");
  assert.equal(gen(4).split("-").length, 4);       // honours the word count
  // A generated passphrase actually works as a board passphrase.
  const p = gen();
  const { keymeta, dk } = await xyCrypto.createBoardKeys(p);
  const ct = await xyCrypto.encField(dk, "тест");
  assert.equal(await xyCrypto.decField(await xyCrypto.unlockBoard(p, keymeta), ct), "тест");
});

test("bytes round-trip for attachments", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const data = new Uint8Array([0, 1, 2, 250, 255]);
  const env = await xyCrypto.encBytes(dk, data);
  assert.deepEqual([...(await xyCrypto.decBytes(dk, env))], [...data]);
});

// ---- the batch forms ----
//
// encFields/decFields are what every loop over a board's cards, comments or
// events calls. They must behave exactly like a loop of encField/decField would,
// order included — the callers pair the results back up by index.

test("encFields/decFields round-trip a whole column, in order", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const texts = ["", "один", "два", "…три", "x".repeat(3000)];
  assert.deepEqual(await xyCrypto.decFields(dk, await xyCrypto.encFields(dk, texts)), texts);
  assert.deepEqual(await xyCrypto.encFields(dk, []), []);
  assert.deepEqual(await xyCrypto.decFields(dk, []), []);
});

test("a batch of wildly different sizes keeps the caller's order", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const texts = ["мал", "y".repeat(100000), "ещё мал", "z".repeat(9000)];
  assert.deepEqual(await xyCrypto.decFields(dk, await xyCrypto.encFields(dk, texts)), texts);
});

test("one bad field in a batch does not take down the others", async () => {
  const { dk } = await xyCrypto.createBoardKeys("pw");
  const cts = await xyCrypto.encFields(dk, ["ok1", "bad", "ok2"]);
  const broken = xyCrypto.fromB64(cts[1]);
  broken[broken.length - 1] ^= 0xff;
  assert.deepEqual(
    await xyCrypto.decFields(dk, [cts[0], xyCrypto.toB64(broken), cts[2], "", "not base64!!"]),
    ["ok1", null, "ok2", null, null],
  );
});
