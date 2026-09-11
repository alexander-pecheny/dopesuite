// cryptoworker.ts — a dedicated worker whose only job is the board KDF.
//
// scrypt at N=2^16 needs 64 MiB and ~330 ms of solid compute on a desktop —
// several seconds on a phone — and every board unlock pays it. On the main
// thread that is a frozen tab: no repaint, no scroll, no cancel, right after
// someone typed their passphrase. Here it is time the page spends doing
// something else.
//
// It is the same pure-JS scrypt crypto.ts always used (vendored @noble/hashes),
// deliberately: a wasm build measured only ~1.5× faster, which buys nothing once
// the work is off the thread that draws, and it cost a Rust toolchain on the
// build path plus a 'wasm-unsafe-eval' CSP. The freeze was the bug; the
// milliseconds were not.
//
// Typechecked against the webworker lib (tsconfig.worker.json), so nothing in
// this file's import graph may touch the DOM.
import { scrypt } from "../vendor/scrypt.js";
import type { ScryptParams } from "./cryptoworkerclient.js";

type Req =
  | { id: number; op: "ping" }
  | { id: number; op: "scrypt"; pass: Uint8Array; salt: Uint8Array; params: ScryptParams };

export interface Res {
  id: number;
  ok: boolean;
  value?: Uint8Array<ArrayBuffer>;
  error?: string;
}

const ctx = self as unknown as DedicatedWorkerGlobalScope;

ctx.onmessage = (ev: MessageEvent<Req>) => {
  const req = ev.data;
  try {
    ctx.postMessage(handle(req));
  } catch (e) {
    ctx.postMessage({ id: req.id, ok: false, error: String(e) } satisfies Res);
  }
};

function handle(req: Req): Res {
  switch (req.op) {
    // The handshake: a worker that answers this has loaded its module graph and
    // can be trusted with an unlock. The client gives up on one that does not.
    case "ping":
      return { id: req.id, ok: true };
    case "scrypt":
      return { id: req.id, ok: true, value: scrypt(req.pass, req.salt, req.params) };
  }
}
