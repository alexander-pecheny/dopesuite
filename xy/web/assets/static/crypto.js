// crypto.js — xy's client-side encryption layer. Sole owner of the wire
// envelope format and the per-board key lifecycle. Pure JS scrypt (vendored
// @noble/hashes, no WASM → runs under iOS Lockdown Mode) + native AES-256-GCM
// via WebCrypto. See PLAN §2.
//
// Loaded as an ES module (CSP script-src 'self'); consumers import from it or
// read window.xyCrypto.
import { scrypt } from "./vendor/scrypt.js";

const subtle = globalThis.crypto.subtle;
const te = new TextEncoder();
const td = new TextDecoder();

// Envelope: magic "xy1" (3) | alg (1) | nonce (12) | ciphertext+tag
const MAGIC = te.encode("xy1");
const ALG_AES_GCM = 1;
const NONCE_LEN = 12;
const HEADER_LEN = MAGIC.length + 1 + NONCE_LEN;

// Default KDF params, stored per board so they can be raised later.
const DEFAULT_KDF = { kdf: "scrypt", N: 32768, r: 8, p: 1, dkLen: 32 };
const VERIFY_PLAINTEXT = "xy-verify-v1";

function randomBytes(n) {
  const b = new Uint8Array(n);
  globalThis.crypto.getRandomValues(b);
  return b;
}

// ---- base64 (over the wire) ----
function toB64(bytes) {
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s);
}
function fromB64(b64) {
  const s = atob(b64);
  const out = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
  return out;
}

// ---- KEK derivation ----
async function deriveKEK(passphrase, salt, params) {
  const raw = scrypt(te.encode(passphrase.normalize("NFKC")), salt, {
    N: params.N, r: params.r, p: params.p, dkLen: params.dkLen || 32,
  });
  return subtle.importKey("raw", raw, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
}

// ---- envelope encrypt/decrypt with a raw AES-GCM key (CryptoKey) ----
async function seal(key, plaintextBytes) {
  const nonce = randomBytes(NONCE_LEN);
  const ct = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv: nonce }, key, plaintextBytes));
  const out = new Uint8Array(HEADER_LEN + ct.length);
  out.set(MAGIC, 0);
  out[MAGIC.length] = ALG_AES_GCM;
  out.set(nonce, MAGIC.length + 1);
  out.set(ct, HEADER_LEN);
  return out;
}
async function open(key, envelope) {
  if (envelope.length < HEADER_LEN) throw new Error("envelope too short");
  for (let i = 0; i < MAGIC.length; i++) {
    if (envelope[i] !== MAGIC[i]) throw new Error("bad envelope magic");
  }
  if (envelope[MAGIC.length] !== ALG_AES_GCM) throw new Error("unknown envelope alg");
  const nonce = envelope.subarray(MAGIC.length + 1, HEADER_LEN);
  const ct = envelope.subarray(HEADER_LEN);
  return new Uint8Array(await subtle.decrypt({ name: "AES-GCM", iv: nonce }, key, ct));
}

// ---- data-key (DK) import ----
async function importDK(raw) {
  return subtle.importKey("raw", raw, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
}

// ---- board key lifecycle ----

// createBoardKeys generates a fresh DK and wraps it under a passphrase-derived
// KEK. Returns the keymeta to persist plus the live DK (CryptoKey + raw).
async function createBoardKeys(passphrase) {
  const params = { ...DEFAULT_KDF };
  const salt = randomBytes(16);
  const dkRaw = randomBytes(32);
  const kek = await deriveKEK(passphrase, salt, params);
  const dk = await importDK(dkRaw);
  const wrapped = await seal(kek, dkRaw);
  const verify = await seal(dk, te.encode(VERIFY_PLAINTEXT));
  return {
    keymeta: {
      kdf_salt: toB64(salt),
      kdf_params: JSON.stringify(params),
      wrapped_key: toB64(wrapped),
      verify_token: toB64(verify),
    },
    dk: { key: dk, raw: dkRaw },
  };
}

// unlockBoard derives the KEK, unwraps DK, and verifies the token. Throws on a
// wrong passphrase.
async function unlockBoard(passphrase, keymeta) {
  const params = JSON.parse(keymeta.kdf_params);
  const salt = fromB64(keymeta.kdf_salt);
  const kek = await deriveKEK(passphrase, salt, params);
  let dkRaw;
  try {
    dkRaw = await open(kek, fromB64(keymeta.wrapped_key));
  } catch (_) {
    throw new Error("Неверный пароль доски");
  }
  const dk = await importDK(dkRaw);
  try {
    const verify = await open(dk, fromB64(keymeta.verify_token));
    if (td.decode(verify) !== VERIFY_PLAINTEXT) throw new Error("verify mismatch");
  } catch (_) {
    throw new Error("Неверный пароль доски");
  }
  return { key: dk, raw: dkRaw };
}

// rewrapKey produces new keymeta (salt/params/wrapped_key) for a passphrase
// change, re-wrapping the SAME dk. Board data is never re-encrypted.
async function rewrapKey(newPassphrase, dk) {
  const params = { ...DEFAULT_KDF };
  const salt = randomBytes(16);
  const kek = await deriveKEK(newPassphrase, salt, params);
  const wrapped = await seal(kek, dk.raw);
  return { kdf_salt: toB64(salt), kdf_params: JSON.stringify(params), wrapped_key: toB64(wrapped) };
}

// ---- field helpers (string <-> base64 envelope) ----
async function encField(dk, str) {
  return toB64(await seal(dk.key, te.encode(str)));
}
async function decField(dk, b64) {
  return td.decode(await open(dk.key, fromB64(b64)));
}
async function encBytes(dk, bytes) {
  return await seal(dk.key, bytes);
}
async function decBytes(dk, bytes) {
  return await open(dk.key, bytes);
}

// ---- IndexedDB key cache (raw DK per board) ----
const DB_NAME = "xy-keys";
const STORE = "dk";

function idb() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}
async function cacheDK(boardId, dk) {
  const db = await idb();
  await new Promise((res, rej) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).put(dk.raw, String(boardId));
    tx.oncomplete = res;
    tx.onerror = () => rej(tx.error);
  });
}
async function loadCachedDK(boardId) {
  const db = await idb();
  const raw = await new Promise((res, rej) => {
    const tx = db.transaction(STORE, "readonly");
    const req = tx.objectStore(STORE).get(String(boardId));
    req.onsuccess = () => res(req.result);
    req.onerror = () => rej(req.error);
  });
  if (!raw) return null;
  const bytes = raw instanceof Uint8Array ? raw : new Uint8Array(raw);
  return { key: await importDK(bytes), raw: bytes };
}
async function forgetDK(boardId) {
  const db = await idb();
  await new Promise((res, rej) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).delete(String(boardId));
    tx.oncomplete = res;
    tx.onerror = () => rej(tx.error);
  });
}

export const xyCrypto = {
  toB64, fromB64,
  createBoardKeys, unlockBoard, rewrapKey,
  encField, decField, encBytes, decBytes,
  cacheDK, loadCachedDK, forgetDK,
  // low-level, exposed for tests
  _seal: seal, _open: open, _deriveKEK: deriveKEK, _importDK: importDK,
  DEFAULT_KDF,
};

// Also expose as a window global for classic scripts.
if (typeof window !== "undefined") window.xyCrypto = xyCrypto;
