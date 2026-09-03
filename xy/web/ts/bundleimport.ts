// bundleimport.ts — reading a Bundle zip, and the one target that needs no open
// board: a new one. The archive is parsed in the browser and handed to
// applyBundle (ADR-0014), which does every write; failure here deletes the
// board this call created, since it was seconds old and nobody else's.
//
// Appending a Bundle to a board that already exists lives in
// bundleimportpanel.ts — it needs that board's key, which only its own page has.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { stampPassCheck } from "./passcheck.js";
import { applyBundle } from "./bundleapply.js";
import type { AttachmentBytes, ApplyResult } from "./bundleapply.js";
import { BOARD_JSON, contentBytes, parseBundle } from "./bundle.js";
import type { Bundle } from "./bundle.js";
import { zipRead } from "./zip.js";
import S from "./i18nstrings.js";

const { fetchJSON, jpost, errMsg } = xyApp;

interface StorageInfo {
  used_bytes: number;
  quota_bytes: number;
  unlimited?: boolean;
}

export async function checkQuota(bundle: Bundle): Promise<void> {
  const s = (await fetchJSON("/api/auth/storage")) as StorageInfo;
  if (s.unlimited) return;
  // Ciphertext sizes match the plaintext's within an envelope's few dozen
  // bytes per field; 10% headroom covers that.
  const need = Math.ceil(contentBytes(bundle) * 1.1);
  const left = s.quota_bytes - s.used_bytes;
  if (need > left) {
    const mb = (n: number): string => (n / (1 << 20)).toFixed(1);
    throw new Error(S.import.bundle.quota(mb(need), mb(Math.max(left, 0))));
  }
}

// readBundleFile turns an untrusted .zip into a validated Bundle and the lazy
// reader its attachments come through — the same pair a live board produces.
export async function readBundleFile(file: File): Promise<{ bundle: Bundle; bytesOf: AttachmentBytes }> {
  const entries = await zipRead(new Uint8Array(await file.arrayBuffer()));
  const files = new Map(entries.map((e) => [e.name, e.data]));
  const boardJson = files.get(BOARD_JSON);
  if (!boardJson) throw new Error(S.import.bundle.noBoardJson(BOARD_JSON));
  const bundle = parseBundle(new TextDecoder().decode(boardJson));
  const bytesOf: AttachmentBytes = (a) => {
    const bytes = files.get(a.path);
    if (!bytes) throw new Error(S.import.bundle.missingAttachment(a.path));
    return Promise.resolve(bytes);
  };
  return { bundle, bytesOf };
}

// sniffBundle answers "is this one of ours?" so the import picker never has to
// ask. A zip that holds no board.json is somebody else's — a question package
// for the server's parser — and a corrupt one is best explained by whoever
// tries to read it next.
export async function sniffBundle(file: File): Promise<{ bundle: Bundle; bytesOf: AttachmentBytes } | null> {
  if (!/\.zip$/i.test(file.name)) return null;
  try {
    return await readBundleFile(file);
  } catch {
    return null;
  }
}

export function summarize(bundle: Bundle, r: ApplyResult): string {
  let out = S.import.bundle.summary(String(r.cards), String(r.units.filter((u) => !u.error).length), String(bundle.sessions.length), String(r.events), String(r.attachments));
  if (r.skipped.length) out += S.import.bundle.summarySkipped(String(r.skipped.length)) + r.skipped.slice(0, 20).join(", ");
  return out;
}

// createBoardFromBundle is the target that needs no open board: a new one under
// a fresh key. Whatever produced the Bundle — an archive, a Trello account —
// arrives here, and a failure takes the board with it (ADR-0013).
export async function createBoardFromBundle(
  bundle: Bundle,
  bytesOf: AttachmentBytes,
  name: string,
  pass: string,
  log: (line: string) => void,
): Promise<{ id: number; summary: string }> {
  await checkQuota(bundle);
  log(S.import.bundle.creating());
  const { keymeta, dk } = await xyCrypto.createBoardKeys(pass);
  const created = (await jpost("/api/boards", { ...keymeta, name: name || bundle.board.name })) as { id: number };
  const boardId = created.id;

  try {
    const result = await applyBundle(bundle, { boardId, dk, append: null }, bytesOf, log);
    if (result.failed) {
      const bad = result.units.find((u) => u.error)!;
      throw new Error(`«${bad.title}»: ${bad.error}`);
    }
    await xyCrypto.cacheDK(boardId, dk);
    stampPassCheck(boardId); // the words are known today; the check is a month off
    const summary = summarize(bundle, result);
    log(summary);
    return { id: boardId, summary };
  } catch (e) {
    // Never leave a half-imported board behind: it was created seconds ago by
    // this same flow, so deleting it is safe, and a retry starts clean.
    log(S.import.bundle.rollback());
    try {
      await xyApp.jdelete(`/api/boards/${boardId}`);
    } catch { /* the original error matters more */ }
    throw new Error(errMsg(e));
  }
}

export async function importBundle(file: File, name: string, pass: string, log: (line: string) => void): Promise<{ id: number; summary: string }> {
  log(S.import.bundle.reading());
  const { bundle, bytesOf } = await readBundleFile(file);
  return await createBoardFromBundle(bundle, bytesOf, name || bundle.board.name, pass, log);
}

export const xyBundleImport = { importBundle, createBoardFromBundle, readBundleFile, sniffBundle, checkQuota };
