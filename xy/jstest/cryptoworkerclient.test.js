// The crypto worker client's contract, which is mostly about failure: the KDF it
// fronts is the only thing standing between a passphrase and an unlocked board,
// so every way a worker can fail to exist must end in `null` — "derive it on this
// thread" — and never in a rejected unlock.
//
// Drives the real module against a fake Worker: the client reads the global
// constructor at call time, so replacing it here exercises the actual
// `new Worker(url, {type:"module"})` path.
import { test } from "node:test";
import assert from "node:assert/strict";
import { start } from "../web/assets/static/dist/cryptoworkerclient.js";

const realWorker = globalThis.Worker;

// useWorker installs a fake for one test. `behave` gets each posted message and
// decides what (if anything) comes back.
function useWorker(behave) {
  const state = { terminated: false, posted: [], url: null, opts: null };
  globalThis.Worker = class {
    constructor(url, opts) {
      state.url = url;
      state.opts = opts;
      this.onmessage = null;
      this.onerror = null;
      state.self = this;
    }
    postMessage(msg) {
      state.posted.push(msg);
      // A real worker answers on a later task, never synchronously.
      setTimeout(() => behave(msg, this), 0);
    }
    terminate() { state.terminated = true; }
  };
  return state;
}

function restore() {
  if (realWorker) globalThis.Worker = realWorker;
  else delete globalThis.Worker;
}

test("a worker that answers is used, and scrypt round-trips through it", async (t) => {
  t.after(restore);
  const state = useWorker((msg, w) => {
    if (msg.op === "ping") return w.onmessage({ data: { id: msg.id, ok: true } });
    w.onmessage({ data: { id: msg.id, ok: true, value: new Uint8Array([1, 2, 3]) } });
  });
  const worker = await start("/static/dist/cryptoworker.js");
  assert.ok(worker, "a healthy worker must be used");
  assert.equal(state.url, "/static/dist/cryptoworker.js");
  // A classic worker could not import its module graph.
  assert.deepEqual(state.opts, { type: "module" });

  const params = { N: 1024, r: 8, p: 1, dkLen: 32 };
  const out = await worker.scrypt(new Uint8Array([9]), new Uint8Array([8]), params);
  assert.deepEqual([...out], [1, 2, 3]);
  const call = state.posted.find((m) => m.op === "scrypt");
  assert.deepEqual(call.params, params);
});

test("replies are matched to their own request, whatever order they land in", async (t) => {
  t.after(restore);
  const pending = [];
  useWorker((msg, w) => {
    if (msg.op === "ping") return w.onmessage({ data: { id: msg.id, ok: true } });
    pending.push({ msg, w });
    // Answer in reverse once both are in: ids, not arrival order, must pair them.
    if (pending.length === 2) {
      for (const { msg: m, w: ww } of pending.reverse()) {
        ww.onmessage({ data: { id: m.id, ok: true, value: new Uint8Array([m.salt[0]]) } });
      }
    }
  });
  const worker = await start("/w.js");
  const params = { N: 1024, r: 8, p: 1, dkLen: 32 };
  const [a, b] = await Promise.all([
    worker.scrypt(new Uint8Array([0]), new Uint8Array([10]), params),
    worker.scrypt(new Uint8Array([0]), new Uint8Array([20]), params),
  ]);
  assert.deepEqual([[...a], [...b]], [[10], [20]]);
});

test("no Worker constructor at all → null, not a throw", async (t) => {
  t.after(restore);
  delete globalThis.Worker;
  assert.equal(await start("/w.js"), null);
});

test("a constructor that throws → null", async (t) => {
  t.after(restore);
  globalThis.Worker = class { constructor() { throw new Error("module workers unsupported"); } };
  assert.equal(await start("/w.js"), null);
});

test("a worker that errors instead of answering → null, and is terminated", async (t) => {
  t.after(restore);
  // The module-worker case a browser rejects: the constructor succeeds and the
  // failure arrives as an error event, with no reply ever coming.
  const state = useWorker((_msg, w) => w.onerror(new Error("failed to load")));
  assert.equal(await start("/w.js"), null);
  assert.equal(state.terminated, true, "a dead worker must not be left running");
});

test("a scrypt call that fails rejects, so the caller can derive it itself", async (t) => {
  t.after(restore);
  useWorker((msg, w) => {
    if (msg.op === "ping") return w.onmessage({ data: { id: msg.id, ok: true } });
    w.onmessage({ data: { id: msg.id, ok: false, error: "boom" } });
  });
  const worker = await start("/w.js");
  await assert.rejects(
    () => worker.scrypt(new Uint8Array([1]), new Uint8Array([2]), { N: 1024, r: 8, p: 1, dkLen: 32 }),
    /boom/,
  );
});

test("stop() terminates and fails anything still waiting", async (t) => {
  t.after(restore);
  const state = useWorker((msg, w) => {
    if (msg.op === "ping") w.onmessage({ data: { id: msg.id, ok: true } });
    // a scrypt request is simply never answered
  });
  const worker = await start("/w.js");
  const inflight = worker.scrypt(new Uint8Array([1]), new Uint8Array([2]), { N: 1024, r: 8, p: 1, dkLen: 32 });
  worker.stop();
  await assert.rejects(() => inflight, /stopped/);
  assert.equal(state.terminated, true);
});
