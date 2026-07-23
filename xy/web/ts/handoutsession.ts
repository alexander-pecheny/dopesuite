// handoutsession.ts — the handout image-staging session, lifted out of board.js.
//
// Generating handouts re-uses the referenced images across every PDF / split_fit
// run: they are uploaded to a server session once, and each generate just cites
// the session id. A heartbeat keeps the session alive; the server reaps it after
// ~1 min of silence, so a lapsed session re-stages on demand. That lifecycle —
// stage-once/dedup, the single in-flight guard, the heartbeat, reap→clear, and
// cleanup on close — is a small state machine with injected network + timer
// primitives, so it is unit-testable; board.js supplies the image gather/upload
// and the DOM.

// stage()'s result: the new session id + the names it managed to stage.
export interface StageResult {
  session: string;
  names?: Set<string> | null;
}

// The injected primitives create() runs on. wantedNames extracts the image
// names a 4s source references; stage uploads them to a new server session
// (null when there are none / on error); heartbeat pings a session (false =
// reaped); unstage deletes it. The timer pair + heartbeatMs exist for tests.
export interface HandoutSessionDeps {
  wantedNames(source: string): Set<string>;
  stage(source: string): Promise<StageResult | null>;
  heartbeat(sessionId: string): Promise<boolean>;
  unstage(sessionId: string): Promise<unknown>;
  setInterval?(fn: () => void, ms: number): unknown;
  clearInterval?(handle: unknown): void;
  heartbeatMs?: number;
}

export interface HandoutSession {
  ensure(source: string): Promise<string | null>;
  beat(): Promise<boolean>;
  startHeartbeat(): void;
  stopHeartbeat(): void;
  close(): Promise<void>;
  sessionId(): string | null;
}

export function create(deps: HandoutSessionDeps): HandoutSession {
  const setIntervalFn = deps.setInterval || ((f: () => void, ms: number) => setInterval(f, ms));
  const clearIntervalFn = deps.clearInterval || ((h: unknown) => clearInterval(h as number));
  const heartbeatMs = deps.heartbeatMs != null ? deps.heartbeatMs : 5000;
  const st: {
    sessionId: string | null;
    names: Set<string> | null;
    inflight: Promise<string | null> | null;
    timer: unknown;
  } = { sessionId: null, names: null, inflight: null, timer: null };

  async function doStage(source: string): Promise<string | null> {
    const r = await deps.stage(source);
    if (r && r.session) { st.sessionId = r.session; st.names = r.names || new Set(); return r.session; }
    st.sessionId = null;
    return null;
  }

  // ensure returns a session id whose staged images cover the source's
  // references, staging once if needed (deduped, single in-flight). null when
  // the source references no images.
  async function ensure(source: string): Promise<string | null> {
    const wanted = deps.wantedNames(source);
    if (!wanted.size) return null;
    const names = st.names;
    if (st.sessionId && names && [...wanted].every((n) => names.has(n))) return st.sessionId;
    if (st.inflight) return st.inflight;
    st.inflight = doStage(source).finally(() => { st.inflight = null; });
    return st.inflight;
  }

  // beat pings the session; a reaped session (false) is cleared so the next
  // ensure re-stages.
  async function beat(): Promise<boolean> {
    if (!st.sessionId) return false;
    const ok = await deps.heartbeat(st.sessionId);
    if (!ok) st.sessionId = null;
    return ok;
  }

  function startHeartbeat(): void { stopHeartbeat(); st.timer = setIntervalFn(beat, heartbeatMs); }
  function stopHeartbeat(): void { if (st.timer) { clearIntervalFn(st.timer); st.timer = null; } }

  // close stops the heartbeat and deletes the staged images server-side.
  async function close(): Promise<void> {
    stopHeartbeat();
    const sid = st.sessionId;
    st.sessionId = null;
    st.names = null;
    if (sid) await deps.unstage(sid);
  }

  return { ensure, beat, startHeartbeat, stopHeartbeat, close, sessionId: () => st.sessionId };
}

export const xyHandoutSession = { create };
