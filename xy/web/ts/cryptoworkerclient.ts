// cryptoworkerclient.ts — the main-thread half of cryptoworker.ts: start it,
// number the requests, resolve the replies. It knows nothing about the KDF; it
// moves byte arrays.
//
// Every failure mode here — no Worker constructor, no module workers, a worker
// that never answers — resolves to `null` from start(), and the caller then
// derives the key itself. A crypto layer that refused to unlock a board because
// a worker would not start would be a worse bug than a slow one.

// The KDF parameters, as both halves of the protocol see them. Declared here
// rather than in the worker because tsconfig.json excludes that file — it is
// checked against the webworker lib instead — and a type import would pull it
// back into the main program regardless.
export interface ScryptParams { N: number; r: number; p: number; dkLen: number }

export interface CryptoWorker {
  scrypt(pass: Uint8Array, salt: Uint8Array, params: ScryptParams): Promise<Uint8Array<ArrayBuffer>>;
  stop(): void;
}

interface Res {
  id: number;
  ok: boolean;
  value?: Uint8Array<ArrayBuffer>;
  error?: string;
}

// How long to wait for the worker's answer to "ping". A worker that has not
// answered by then is one the browser could not really start — no module
// workers, a blocked or 404ing script — and the constructor does not say so.
// Give up on it rather than hanging the first unlock behind it.
const PING_TIMEOUT_MS = 5000;

// start boots the worker and waits for it to answer. Resolves null when there is
// no working worker to be had.
export async function start(workerUrl: string): Promise<CryptoWorker | null> {
  if (typeof Worker === "undefined") return null;
  let w: Worker;
  try {
    w = new Worker(workerUrl, { type: "module" });
  } catch (_) {
    return null;
  }

  let nextId = 1;
  const pending = new Map<number, { resolve: (r: Res) => void; reject: (e: Error) => void }>();

  const failAll = (err: Error): void => {
    for (const p of pending.values()) p.reject(err);
    pending.clear();
  };
  w.onmessage = (ev: MessageEvent<Res>) => {
    const slot = pending.get(ev.data.id);
    if (!slot) return;
    pending.delete(ev.data.id);
    if (ev.data.ok) slot.resolve(ev.data);
    else slot.reject(new Error(ev.data.error || "crypto worker failed"));
  };
  // onerror fires for a worker script the browser would not load at all — the
  // module-worker case an old browser rejects — and no reply is coming after it.
  w.onerror = () => failAll(new Error("crypto worker failed to start"));

  const send = (msg: Record<string, unknown>, timeoutMs = 0): Promise<Res> =>
    new Promise<Res>((resolve, reject) => {
      const id = nextId++;
      pending.set(id, { resolve, reject });
      if (timeoutMs) {
        setTimeout(() => {
          if (pending.delete(id)) reject(new Error("crypto worker timed out"));
        }, timeoutMs);
      }
      w.postMessage({ ...msg, id });
    });

  try {
    await send({ op: "ping" }, PING_TIMEOUT_MS);
  } catch (_) {
    w.terminate();
    return null;
  }

  return {
    async scrypt(pass, salt, params) {
      return (await send({ op: "scrypt", pass, salt, params })).value!;
    },
    stop() {
      failAll(new Error("crypto worker stopped"));
      w.terminate();
    },
  };
}
