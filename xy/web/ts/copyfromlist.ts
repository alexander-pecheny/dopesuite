// copyfromlist.ts — clone a board from the / board list (the string ids say
// copy; only the wording is clone). The board page's ☰
// copy panel works because its source is already decrypted; here the device may
// not hold this board's key, so the flow asks its passphrase first, then the
// copy form (a create-board twin: a copy is a new board under a fresh key, only
// its name is prefilled and the verb differs). The copy itself is the same pair
// the board page uses — decrypt the snapshot under the source key, buildBundle
// it, createBoardFromBundle re-encrypts under the new key — so nothing new has
// to reach the server: the board's content and its data key both live in this
// browser.
//
// Both forms live in index.dopeui as static modals (index.ts wires the create
// form the same way), so their submit handlers are attached once here, and the
// per-attempt state — which board, and the key the unlock step produced —
// travels in module variables.

import { xyApp } from "./app.js";
import type { PassphraseSetup } from "./app.js";
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { modal } from "./modal.js";
import { xySync } from "./sync.js";
import { decryptSnapshot } from "./unlock.js";
import type { BoardKeymeta, Snapshot } from "./unlock.js";
import { buildBundle } from "./bundleexport.js";
import { createBoardFromBundle } from "./bundleimport.js";
import { suggestCopyName, takenBoardNames } from "./copyname.js";
import S from "./i18nstrings.js";

const { byId, fetchJSON, errMsg } = xyApp;

// A copy's source as the list knows it: the id, and the name the tile shows
// (plaintext for migrated boards; the "board #id" placeholder for legacy ones,
// whose name is still name_enc on the row). schema_version is what tells the two
// apart: a migrated board keeps its old name_enc on the row — neither the rename
// nor the backfill clears it — so the ciphertext alone would resurrect a name the
// board was renamed away from.
interface CopySource {
  id: number;
  name: string;
  name_enc?: string;
  schema_version?: number;
}

const unlockModal = modal("copyUnlock");
const copyModal = modal("copy");

const unlockForm = byId<HTMLFormElement>("copyUnlockForm");
const unlockMessage = byId<HTMLElement>("copyUnlockMessage");
const unlockPass = byId<HTMLInputElement>("copyUnlockPass");

const copyForm = byId<HTMLFormElement>("copyForm");
const copyName = byId<HTMLInputElement>("copyName");
const copyPass = byId<HTMLInputElement>("copyPass");
const copyCheck = byId<HTMLInputElement>("copyPassSaved");
const copySubmit = byId<HTMLButtonElement>("copySubmit");
const copyMessage = byId<HTMLElement>("copyMessage");

const passSetup: PassphraseSetup = xyApp.wirePassphraseSetup({
  input: copyPass,
  dice: byId("copyGenPassBtn"),
  copied: byId("copyPassCopied"),
  saved: copyCheck,
  submit: copySubmit,
}, xyCrypto.generatePassphrase);

// Per-attempt state: the board the two forms serve, the key the unlock step
// produced, whether a copy is running (a double-submit would mint two), and
// the unlock outcome the modal's dismissal settles with (null = abandoned).
let current: CopySource | null = null;
let currentDk: DataKey | null = null;
let busy = false;
let unlockResult: DataKey | null = null;
// One attempt at a time. Both forms are static nodes with module-wide state, and
// modal.open() is a no-op on an already-open modal — so a second entrant would
// steal `current` and wait on a dismissal that already belongs to the first.
// A double-click on a tile's button is enough: the key lookup below is async.
let running = false;

export async function copyBoardFromList(b: CopySource): Promise<void> {
  if (running) return;
  if (!xySync.requireOnline(S.board.copy.offline(), byId("message"))) return;
  running = true;
  try {
    let dk: DataKey | null = await xyCrypto.loadCachedDK(b.id).catch(() => null);
    if (!dk) {
      dk = await askUnlock(b);
      if (!dk) return;
    }
    await askCopy(b, dk);
  } finally {
    running = false;
  }
}

// ---- the unlock prompt: only a board whose key this device hasn't got stops
// here. The modal dismisses exactly once (any gesture) — success closes it with
// its DK in unlockResult, an abandonment leaves null, and one settle wins.
function askUnlock(b: CopySource): Promise<DataKey | null> {
  current = b;
  byId("copyUnlockHint").textContent = S.chrome.home.copyUnlockHint(b.name);
  unlockForm.reset();
  unlockResult = null;
  return new Promise<DataKey | null>((settle) => {
    unlockModal.open({ onClose: () => settle(unlockResult) });
    unlockPass.focus();
  });
}

unlockForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const board = current;
  if (!board) return;
  unlockMessage.textContent = "";
  void (async () => {
    try {
      if (!xySync.requireOnline(S.board.copy.offline(), unlockMessage)) return;
      const keymeta = (await fetchJSON(`/api/boards/${board.id}/keymeta`)) as BoardKeymeta;
      const dk = await xyCrypto.unlockBoard(unlockPass.value, keymeta);
      await xyCrypto.cacheDK(board.id, dk);
      unlockResult = dk;
      unlockPass.value = ""; // done its work; the page outlives the prompt
      unlockModal.close(); // onClose settles the wait with dk
    } catch (err) {
      unlockMessage.textContent = errMsg(err);
    }
  })();
});

// ---- the copy form: name prefilled from the source, a fresh passphrase.
function askCopy(b: CopySource, dk: DataKey): Promise<void> {
  current = b;
  currentDk = dk;
  // A fresh open is a fresh attempt: an earlier in-flight copy has no bearing
  // on this one (it is racing its own navigation).
  busy = false;
  copyForm.reset();
  copyName.value = b.name + S.board.copy.nameSuffix();
  // The counted suffix needs the other boards' names, which is a round trip, so
  // the field opens on the plain one and settles a moment later — refined only
  // while it still holds what this function put there, never over typing.
  void refineName(b.name);
  // A legacy board's tile name is the "board #id" placeholder; now that we hold
  // the key, drop its real name in once decrypted. Only a legacy board: a
  // migrated one already shows the authoritative plaintext name, and its
  // leftover name_enc is whatever it was called before the migration.
  if (b.name_enc && (b.schema_version ?? 0) < 2) {
    xyCrypto.decField(dk, b.name_enc).then((n) => {
      if (!n) return;
      copyName.value = n + S.board.copy.nameSuffix();
      void refineName(n);
    }).catch(() => {});
  }
  // The passphrase is rolled inside this click, like create's: the clipboard
  // only answers to a user gesture, and this is the one moment the words are
  // ever shown in the clear.
  passSetup.reset();
  void passSetup.roll(true);
  return new Promise<void>((settle) => {
    copyModal.open({ onClose: () => settle() });
    copyName.focus();
  });
}

// refineName swaps the plain copy suffix for a counted one once the names are
// known. It writes only over the prefill it is refining: a reader who started
// typing during the round trip keeps what they typed.
async function refineName(base: string): Promise<void> {
  const prefill = base + S.board.copy.nameSuffix();
  if (copyName.value !== prefill) return;
  const suggested = suggestCopyName(base, await takenBoardNames());
  if (copyName.value === prefill) copyName.value = suggested;
}

copyForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const board = current;
  const dk = currentDk;
  if (!board || !dk) return;
  copyMessage.textContent = "";
  const name = copyName.value.trim();
  const pass = copyPass.value;
  if (!name) { copyMessage.textContent = S.board.copy.nameRequired(); return; }
  const passErr = xyCrypto.validatePassphrase(pass);
  if (passErr) { copyMessage.textContent = passErr; return; }
  if (!xySync.requireOnline(S.board.copy.offline(), copyMessage)) return;
  if (busy) return;
  busy = true;
  copySubmit.disabled = true;
  const log = (line: string): void => { copyMessage.textContent = line; };
  void (async () => {
    try {
      // The source decrypts right here (decryptSnapshot, no board page needed),
      // becomes a Bundle, and createBoardFromBundle re-encrypts it under the new
      // board's fresh key — deleting the copy if it dies part-way (the same
      // atomicity the board page's copy panel relies on).
      const snap = (await fetchJSON(`/api/boards/${board.id}`)) as Snapshot;
      const { state } = await decryptSnapshot(dk, snap, xyCrypto);
      const { bundle, bytesOf } = await buildBundle({ id: board.id, dk: () => dk, state }, null, log);
      const { id } = await createBoardFromBundle(bundle, bytesOf, name, pass, log);
      log(S.board.copy.done());
      copyModal.close();
      window.location.href = `/board/${id}`;
    } catch (err) {
      copyMessage.textContent = S.board.copy.failed(errMsg(err));
      // Stay truthful to the promise gate: enable only when still promised.
      copySubmit.disabled = !copyCheck.checked;
      busy = false;
    }
  })();
});
