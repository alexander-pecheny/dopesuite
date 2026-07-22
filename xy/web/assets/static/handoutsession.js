// handoutsession.js — the handout image-staging session, lifted out of board.js.
//
// Generating handouts re-uses the referenced images across every PDF / split_fit
// run: they are uploaded to a server session once, and each generate just cites
// the session id. A heartbeat keeps the session alive; the server reaps it after
// ~1 min of silence, so a lapsed session re-stages on demand. That lifecycle —
// stage-once/dedup, the single in-flight guard, the heartbeat, reap→clear, and
// cleanup on close — is a small state machine with injected network + timer
// primitives, so it is unit-testable; board.js supplies the image gather/upload
// and the DOM.

// create(deps): { wantedNames(source)->Set, stage(source)->Promise<{session,names}|null>,
//   heartbeat(sessionId)->Promise<bool>, unstage(sessionId)->Promise,
//   setInterval?, clearInterval?, heartbeatMs? }.
export function create(deps) {
  const setIntervalFn = deps.setInterval || ((f, ms) => setInterval(f, ms));
  const clearIntervalFn = deps.clearInterval || ((h) => clearInterval(h));
  const heartbeatMs = deps.heartbeatMs != null ? deps.heartbeatMs : 5000;
  const st = { sessionId: null, names: null, inflight: null, timer: null };

  async function doStage(source) {
    const r = await deps.stage(source);
    if (r && r.session) { st.sessionId = r.session; st.names = r.names || new Set(); return r.session; }
    st.sessionId = null;
    return null;
  }

  // ensure returns a session id whose staged images cover the source's
  // references, staging once if needed (deduped, single in-flight). null when
  // the source references no images.
  async function ensure(source) {
    const wanted = deps.wantedNames(source);
    if (!wanted.size) return null;
    if (st.sessionId && st.names && [...wanted].every((n) => st.names.has(n))) return st.sessionId;
    if (st.inflight) return st.inflight;
    st.inflight = doStage(source).finally(() => { st.inflight = null; });
    return st.inflight;
  }

  // beat pings the session; a reaped session (false) is cleared so the next
  // ensure re-stages.
  async function beat() {
    if (!st.sessionId) return false;
    const ok = await deps.heartbeat(st.sessionId);
    if (!ok) st.sessionId = null;
    return ok;
  }

  function startHeartbeat() { stopHeartbeat(); st.timer = setIntervalFn(beat, heartbeatMs); }
  function stopHeartbeat() { if (st.timer) { clearIntervalFn(st.timer); st.timer = null; } }

  // close stops the heartbeat and deletes the staged images server-side.
  async function close() {
    stopHeartbeat();
    const sid = st.sessionId;
    st.sessionId = null;
    st.names = null;
    if (sid) await deps.unstage(sid);
  }

  return { ensure, beat, startHeartbeat, stopHeartbeat, close, sessionId: () => st.sessionId };
}

export const xyHandoutSession = { create };

if (typeof window !== "undefined") window.xyHandoutSession = xyHandoutSession;
