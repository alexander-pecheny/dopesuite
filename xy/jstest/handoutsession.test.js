import { test } from "node:test";
import assert from "node:assert/strict";
import { xyHandoutSession } from "../web/assets/static/dist/handoutsession.js";

// harness wires the session to fake network + timer primitives and records calls.
function harness(over = {}) {
  const calls = { stage: 0, heartbeat: 0, unstage: [] };
  let heartbeatFn = null;
  const deps = {
    wantedNames: over.wantedNames || ((src) => new Set(src ? src.split(",").filter(Boolean) : [])),
    stage: over.stage || (async () => { calls.stage++; return { session: "s" + calls.stage, names: new Set(["a", "b"]) }; }),
    heartbeat: over.heartbeat || (async () => { calls.heartbeat++; return true; }),
    unstage: async (sid) => { calls.unstage.push(sid); },
    setInterval: (fn) => { heartbeatFn = fn; return 1; },
    clearInterval: () => { heartbeatFn = null; },
  };
  const s = xyHandoutSession.create(deps);
  return { s, calls, tickHeartbeat: () => heartbeatFn && heartbeatFn(), isBeating: () => heartbeatFn !== null };
}

test("ensure returns null and never stages when the source references no images", async () => {
  const { s, calls } = harness();
  assert.equal(await s.ensure(""), null);
  assert.equal(calls.stage, 0);
});

test("ensure stages once, then reuses the session when names are covered", async () => {
  const { s, calls } = harness();
  const first = await s.ensure("a,b");
  assert.equal(first, "s1");
  const second = await s.ensure("a"); // covered by staged {a,b}
  assert.equal(second, "s1");
  assert.equal(calls.stage, 1);
});

test("ensure re-stages when the source needs a name that isn't staged", async () => {
  const { s, calls } = harness();
  await s.ensure("a,b");
  const again = await s.ensure("a,b,c"); // 'c' not covered
  assert.equal(calls.stage, 2);
  assert.equal(again, "s2");
});

test("concurrent ensure calls coalesce into a single stage", async () => {
  const { s, calls } = harness();
  const [x, y] = await Promise.all([s.ensure("a,b"), s.ensure("a,b")]);
  assert.equal(calls.stage, 1);
  assert.equal(x, "s1");
  assert.equal(y, "s1");
});

test("beat keeps a live session and clears a reaped one", async () => {
  let alive = true;
  const { s } = harness({ heartbeat: async () => alive });
  await s.ensure("a,b");
  assert.equal(await s.beat(), true);
  assert.equal(s.sessionId(), "s1");
  alive = false;
  assert.equal(await s.beat(), false);
  assert.equal(s.sessionId(), null); // reaped → cleared so the next ensure re-stages
});

test("startHeartbeat installs a ticking beat; close stops it and unstages", async () => {
  const { s, calls, tickHeartbeat, isBeating } = harness();
  await s.ensure("a,b");
  s.startHeartbeat();
  assert.equal(isBeating(), true);
  await tickHeartbeat();
  assert.equal(calls.heartbeat, 1);
  await s.close();
  assert.equal(isBeating(), false);      // heartbeat stopped
  assert.deepEqual(calls.unstage, ["s1"]); // staged session deleted
  assert.equal(s.sessionId(), null);
});
