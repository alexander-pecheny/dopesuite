// dope's SSE state-sync engine: the scoped event protocol (deltas chained by
// seq, re-baselined by snapshot/resync, reset by server epoch), the durable
// pending-ops overlay, stream lifecycle with iOS-wake recovery, and the client
// recorder that captures its timeline. One implementation for every game page:
// od/si edit through createStateSync, host/viewer dispatch scoped events via
// createLiveEvents.

// EventSource.OPEN, spelled numerically so fake streams need no global.
const SSE_OPEN = 1;

function defaultEventSource(url: string): EventSource {
  return new EventSource(url);
}

// The wire shape of one scoped SSE delta op / pending set-op path segment list.
export type PatchPath = ReadonlyArray<string | number>;

export interface StateDeltaOp {
  op?: string;
  path: Array<string | number>;
  value?: unknown;
}

// A scoped SSE "state" event: either a delta ({ops, seq, prevSeq, epoch}) that
// chains onto the previous seq, or a full snapshot ({data, seq, epoch}).
export interface ScopedEventMessage {
  scope: string;
  revision?: number;
  seq?: number;
  prevSeq?: number;
  epoch?: string;
  emitMs?: number;
  ops?: StateDeltaOp[];
  data?: unknown;
}

export function parseScopedEvent(raw: string): ScopedEventMessage {
  const parsed: unknown = JSON.parse(raw);
  if (parsed && typeof parsed === "object" && typeof (parsed as {scope?: unknown}).scope === "string" &&
      (Object.prototype.hasOwnProperty.call(parsed, "data") ||
       Object.prototype.hasOwnProperty.call(parsed, "ops"))) {
    return parsed as ScopedEventMessage;
  }
  return {scope: "unknown", revision: 0, data: parsed};
}

function cloneJSON(value: unknown): unknown {
  if (value === undefined) return null;
  return JSON.parse(JSON.stringify(value));
}

function normalizePatchPath(path: unknown): Array<string | number> {
  if (!Array.isArray(path) || path.length === 0) {
    throw new Error("state patch path must be a non-empty array");
  }
  return path.map((segment): string | number => {
    if (typeof segment === "string" && segment !== "") return segment;
    if (typeof segment === "number" && Number.isInteger(segment) && segment >= 0) return segment;
    throw new Error("state patch path segments must be strings or non-negative integers");
  });
}

// isPathPrefix reports whether `prefix` is an ancestor-or-equal of `full`
// (both already normalized, so segments compare strictly). Used so a coarse
// op marks every cell under the subtree it rewrote.
function isPathPrefix(prefix: PatchPath, full: PatchPath): boolean {
  if (prefix.length > full.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== full[i]) return false;
  }
  return true;
}

function patchKey(op: {path: PatchPath}): string {
  return JSON.stringify(op.path);
}

export interface PendingOp {
  op: "set";
  path: Array<string | number>;
  value: unknown;
  ts: number;
}

export interface PendingOpsOptions {
  storageKey?: string | null;
  ttlMs?: number;
}

export interface PendingOps {
  add(path: PatchPath, value: unknown): PendingOp;
  take(): PendingOp[];
  ack(ops: PendingOp[]): void;
  requeue(ops: PendingOp[]): void;
  all(): PendingOp[];
  overlay(state: unknown): unknown;
  has(path: PatchPath): boolean;
  queued(): number;
  inFlightCount(): number;
  size(): number;
}

// createPendingOps tracks un-acked local edits as scoped set-ops so they can be
// (a) batched into one request and (b) re-overlaid on top of any server state
// we render before the edit is confirmed — so an optimistically-applied cell
// never regresses while its write is in flight, even across a full resync /
// refetch. Shared by createStateSync (OD/KSI whole-game state) and host.js (EK
// per-match edits) so all three editors get identical durability.
//
// Ops to the same path coalesce, last-write-wins. take() moves the queued batch
// to "in flight"; ack() drops them once the server confirms; requeue() returns
// them for retry (without clobbering a newer queued op for the same path);
// overlay() applies (in-flight then queued) onto a clone of the given state.
// createPendingOps tracks un-acked edits. With opts.storageKey set (and
// localStorage available) the un-acked set is also mirrored to localStorage and
// rehydrated on the next page load, so a refresh/crash mid-sync — exactly when
// edits "don't apply" and the operator reloads — doesn't silently drop edits
// the server never confirmed: they reappear (overlaid + spinner) and re-send.
// Persistence is opt-in (host.js EK pending passes no key) and TTL-bounded so a
// long-abandoned session can't resurrect ancient edits.
export function createPendingOps(opts?: PendingOpsOptions | null): PendingOps {
  opts = opts || {};
  const ttlMs = typeof opts.ttlMs === "number" && Number.isFinite(opts.ttlMs) ? opts.ttlMs : 15 * 60 * 1000;
  let store: Storage | null = null;
  if (opts.storageKey) {
    try {
      store = window.localStorage;
    } catch (_e) {
      store = null;
    }
  }
  const storageKey = store ? opts.storageKey : null;

  let queue = new Map<string, PendingOp>();
  let inFlight: PendingOp[] = [];

  // persist mirrors the current un-acked set (in-flight + queued) to storage.
  // take() is intentionally not persisted: it only moves ops queued->in-flight,
  // so all() — and thus what we'd write — is unchanged. Best-effort.
  function persist(): void {
    if (!storageKey || !store) return;
    try {
      const ops = all();
      if (ops.length === 0) store.removeItem(storageKey);
      else store.setItem(storageKey, JSON.stringify(ops));
    } catch (_e) {
      /* quota / serialization — recovery is best-effort, never break editing */
    }
  }

  function add(path: PatchPath, value: unknown): PendingOp {
    const op: PendingOp = {op: "set", path: normalizePatchPath(path), value: cloneJSON(value), ts: pendingTimestamp()};
    queue.set(patchKey(op), op);
    persist();
    return op;
  }
  function take(): PendingOp[] {
    const ops = Array.from(queue.values());
    queue.clear();
    inFlight = inFlight.concat(ops);
    return ops;
  }
  function ack(ops: PendingOp[]): void {
    const sent = new Set(ops);
    inFlight = inFlight.filter((op) => !sent.has(op));
    persist();
  }
  function requeue(ops: PendingOp[]): void {
    for (const op of ops) {
      const key = patchKey(op);
      if (!queue.has(key)) queue.set(key, op);
    }
    persist();
  }
  function all(): PendingOp[] {
    return inFlight.concat(Array.from(queue.values()));
  }
  function overlay(state: unknown): unknown {
    let next = cloneJSON(state);
    for (const op of all()) next = setAtDeltaPath(next, op.path, op.value);
    return next;
  }
  // has reports whether `path` is covered by an un-acked edit, so the UI can
  // mark that cell pending until the server confirms it. True when a queued/
  // in-flight op targets `path` exactly OR an ANCESTOR of it — so a coarse
  // whole-array patch (e.g. OD's ["entries"]) marks every cell beneath it,
  // while exact-path editors (KSI/EK) behave as a plain equality check.
  function has(path: PatchPath): boolean {
    const norm = normalizePatchPath(path);
    return all().some((op) => isPathPrefix(op.path, norm));
  }

  // Rehydrate un-acked ops persisted by a previous load. Nothing is truly in
  // flight after a reload, so everything re-queues (to be overlaid + re-sent).
  if (storageKey && store) {
    try {
      const saved: unknown = JSON.parse(store.getItem(storageKey) || "[]");
      const now = pendingTimestamp();
      let kept = 0;
      for (const op of Array.isArray(saved) ? (saved as Array<Partial<PendingOp> | null>) : []) {
        if (!op || !Array.isArray(op.path)) continue;
        if (op.ts && now - op.ts > ttlMs) continue; // stale — don't resurrect
        const restored: PendingOp = {op: "set", path: op.path, value: op.value, ts: op.ts || now};
        queue.set(patchKey(restored), restored);
        kept++;
      }
      persist(); // rewrite without the stale entries we filtered out
      if (kept === 0) {
        try {
          store.removeItem(storageKey);
        } catch (_e) {
          /* ignore */
        }
      }
    } catch (_e) {
      /* corrupt payload — ignore, start clean */
    }
  }

  return {
    add, take, ack, requeue, all, overlay, has,
    queued: () => queue.size,
    inFlightCount: () => inFlight.length,
    size: () => queue.size + inFlight.length,
  };
}

function pendingTimestamp(): number {
  try {
    return Date.now();
  } catch (_e) {
    return 0;
  }
}

export type SyncStatus = "saved" | "saving" | "reconnecting" | "error";

// The second argument createStateSync hands to onRemoteState: either the SSE
// message that carried the state (delta or snapshot), or a local marker
// ({local: true} after an own confirmed patch, plus {recovered: true} when
// replaying pending edits after a reload), or {resync: true} after a refetch.
export interface StateSyncMeta {
  scope?: string;
  revision?: number;
  seq?: number;
  prevSeq?: number;
  epoch?: string;
  emitMs?: number;
  ops?: StateDeltaOp[];
  data?: unknown;
  local?: boolean;
  recovered?: boolean;
  resync?: boolean;
}

export interface StateSyncWriteError {
  kind: string;
  ops: PendingOp[];
  error: string;
}

export interface StateSyncOptions {
  scope: string;
  stateURL: string;
  eventsURL: string;
  readonly?: boolean;
  debounceMs?: number;
  maxEchoes?: number;
  setStatus?: (state: SyncStatus) => void;
  getState?: () => unknown;
  getInitialSeq?: () => number | string | null | undefined;
  getInitialEpoch?: () => string | null | undefined;
  onRemoteState?: (state: unknown, meta: StateSyncMeta) => void;
  onViewers?: (count: number | undefined) => void;
  onLockdown?: () => void;
  onWriteError?: (info: StateSyncWriteError) => void;
  recorder?: ClientRecorder | null;
  // Test seam: substitute a fake stream for the real EventSource.
  newEventSource?: (url: string) => EventSource;
}

export interface StateSync {
  connect(): EventSource | null;
  flushSave(): Promise<void>;
  flushPatch(): Promise<void>;
  hasPendingSave(): boolean;
  save(): void;
  patch(path: PatchPath, value: unknown): void;
  isPending(path: PatchPath): boolean;
}

export function createStateSync(options: StateSyncOptions): StateSync {
  const debounceMs = typeof options.debounceMs === "number" && Number.isFinite(options.debounceMs) ? options.debounceMs : 250;
  const maxEchoes = typeof options.maxEchoes === "number" && Number.isFinite(options.maxEchoes) ? options.maxEchoes : 12;
  const setSyncStatus = options.setStatus || (() => {});
  const echoSet = new Set<string>();
  const echoOrder: string[] = [];
  let saveTimer: number | null = null;
  let saveQueued = false;
  let saveInFlight = false;
  let patchTimer: number | null = null;
  let patchInFlight = false;
  // Editors persist un-acked edits per scope so a mid-sync refresh recovers
  // them; viewers never edit, so they don't (and can't resurrect stray ops).
  const pending = createPendingOps({
    storageKey: !options.readonly && options.scope ? `dope.pending:${options.scope}` : null,
  });
  // Unified SSE protocol: lastSeq is the per-scope position we have applied.
  // A delta applies only if its prevSeq === lastSeq; otherwise a drop / late
  // join / restart left a gap and we resync the full state. Seeded once from
  // the server-rendered initial seq so the first remote edit chains cleanly.
  let lastSeq = 0;
  let lastSeqSeeded = false;
  let resyncing = false;
  // Active SSE stream, plus guards so connect()'s lifecycle listeners bind
  // once and so recovery never re-opens a stream the server locked down.
  let stream: EventSource | null = null;
  let lifecycleBound = false;
  let lockedDown = false;
  // lastEpoch is the server's per-process token (see server.epoch). The server
  // resets its per-scope seq to 0 on restart, so without this a long-lived
  // client holding a high lastSeq would read every post-restart delta as
  // "seq <= lastSeq" (already applied) and silently stop syncing — the
  // data-loss incident's amplifier. A changed epoch means the seq space reset,
  // so we resync to adopt the new epoch+seq instead of ignoring the deltas.
  let lastEpoch = "";
  let lastEpochSeeded = false;

  // epochReset adopts the first epoch we see as the baseline and reports a
  // reset only on a genuine change. An empty epoch (older server build) is
  // ignored so the protocol degrades gracefully.
  function epochReset(epoch: string | undefined): boolean {
    if (!epoch) return false;
    if (!lastEpochSeeded) {
      lastEpoch = epoch;
      lastEpochSeeded = true;
      return false;
    }
    return epoch !== lastEpoch;
  }

  // Felt-latency instrumentation. Every sample goes into the client recorder
  // ring (downloadable via the log button), and is mirrored to the console when
  // localStorage["dope.editmetrics"] === "1" so a tester with devtools sees it
  // live. monoNow uses the monotonic clock for the own-edit round-trip (immune
  // to wall-clock jumps); delivery latency necessarily uses Date.now() against
  // the server's emit stamp, so it carries clock skew (rough gauge, not exact).
  const feltConsole = (() => {
    try { return window.localStorage.getItem("dope.editmetrics") === "1"; }
    catch (_e) { return false; }
  })();
  const monoNow = () => (typeof performance !== "undefined" && performance.now
    ? performance.now() : Date.now());
  function feltMetric(type: string, data: Record<string, unknown>): void {
    options.recorder?.event(type, data);
    if (feltConsole) {
      try { console.debug(`editmetric ${type} scope=${options.scope}`, data); } catch (_e) {}
    }
  }

  function save(): void {
    if (options.readonly) return;
    saveQueued = true;
    setSyncStatus("saving");
    scheduleSave(debounceMs);
  }

  function patch(path: PatchPath, value: unknown): void {
    if (options.readonly) return;
    try {
      pending.add(path, value);
    } catch (error) {
      console.error(error);
      setSyncStatus("error");
      return;
    }
    setSyncStatus("saving");
    schedulePatch(debounceMs);
  }

  function scheduleSave(delay: number): void {
    window.clearTimeout(saveTimer ?? undefined);
    saveTimer = window.setTimeout(() => {
      saveTimer = null;
      void flushSave();
    }, delay);
  }

  function schedulePatch(delay: number): void {
    window.clearTimeout(patchTimer ?? undefined);
    patchTimer = window.setTimeout(() => {
      patchTimer = null;
      void flushPatch();
    }, delay);
  }

  async function flushSave(): Promise<void> {
    if (options.readonly || saveInFlight || !saveQueued) return;
    saveQueued = false;
    saveInFlight = true;
    let saved = false;
    try {
      const raw = JSON.stringify(options.getState!());
      rememberLocalEcho(raw);
      const response = await fetch(options.stateURL, {
        method: "PUT",
        headers: {"Content-Type": "application/json"},
        body: raw,
      });
      if (!response.ok) throw new Error(await response.text());
      saved = true;
    } catch (error) {
      console.error(error);
      setSyncStatus("error");
    } finally {
      saveInFlight = false;
      if (saveQueued) {
        if (!saveTimer) scheduleSave(0);
      } else if (saved) {
        setSyncStatus("saved");
      }
    }
  }

  async function flushPatch(): Promise<void> {
    if (options.readonly || patchInFlight || pending.queued() === 0) return;
    const ops = pending.take();
    patchInFlight = true;
    let saved = false;
    let retry = true;
    const tSend = monoNow();
    try {
      const response = await fetch(options.stateURL, {
        method: "PATCH",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({ops}),
        // keepalive lets the request complete even if the page is navigating
        // or being backgrounded — without it, edits debounced/in-flight at the
        // moment of a reload are silently dropped. Ops are tiny, well under the
        // 64KB keepalive cap.
        keepalive: true,
      });
      if (!response.ok) {
        retry = response.status >= 500;
        throw new Error(await response.text());
      }
      const updated: unknown = await response.json();
      // Own-edit felt latency: keystroke-batch send to server-confirmed (the
      // moment the optimistic cell stops being "pending"). Single clock.
      feltMetric("patch-rtt", {rtt_ms: Math.round(monoNow() - tSend), ops: ops.length, status: response.status});
      pending.ack(ops);
      rememberLocalEcho(JSON.stringify(updated));
      options.onRemoteState?.(pending.overlay(updated), {local: true});
      saved = true;
    } catch (error) {
      pending.ack(ops);
      if (retry) {
        pending.requeue(ops);
      } else {
        // A 4xx means the server rejected these ops and a retry won't help, so
        // they are dropped — but never silently: log them and notify so the
        // loss is visible (in the console, the client recorder, and the sync
        // status) instead of a cell quietly reverting on the next render.
        console.error("dropped rejected patch ops", {error: String(error), ops});
        options.onWriteError?.({kind: "rejected", ops, error: String(error)});
      }
      setSyncStatus("error");
    } finally {
      patchInFlight = false;
      if (pending.queued() > 0) {
        if (!patchTimer) schedulePatch(saved ? 0 : 2000);
      } else if (saved && !hasPendingSave()) {
        setSyncStatus("saved");
      }
    }
  }

  function rememberLocalEcho(raw: string): void {
    echoSet.add(raw);
    echoOrder.push(raw);
    while (echoOrder.length > maxEchoes) {
      const oldest = echoOrder.shift();
      if (oldest !== undefined) echoSet.delete(oldest);
    }
  }

  function consumeLocalEcho(raw: string): boolean {
    if (!echoSet.has(raw)) return false;
    echoSet.delete(raw);
    const index = echoOrder.indexOf(raw);
    if (index >= 0) echoOrder.splice(index, 1);
    return true;
  }

  function hasPendingSave(): boolean {
    return saveQueued ||
      saveInFlight ||
      saveTimer !== null ||
      patchInFlight ||
      patchTimer !== null ||
      pending.size() > 0;
  }

  function connect(): EventSource | null {
    if (!lastSeqSeeded) {
      lastSeq = Number(options.getInitialSeq?.()) || 0;
      lastSeqSeeded = true;
    }
    if (!lastEpochSeeded) {
      const seededEpoch = options.getInitialEpoch?.();
      if (seededEpoch) {
        lastEpoch = String(seededEpoch);
        lastEpochSeeded = true;
      }
    }
    // Un-acked edits recovered from localStorage by createPendingOps (a previous
    // load refreshed mid-sync): show them overlaid on the seeded state right
    // away — with their pending spinner — then re-send (idempotent set-ops).
    if (!options.readonly && pending.queued() > 0) {
      options.recorder?.event("recovered-pending", {scope: options.scope, count: pending.queued()});
      options.onRemoteState?.(pending.overlay(options.getState ? options.getState() : {}), {local: true, recovered: true});
      setSyncStatus("saving");
      schedulePatch(0);
    }
    openStream();
    bindLifecycle();
    return stream;
  }

  // openStream (re)creates the EventSource, closing any prior one. Split from
  // connect() so recovery can re-open the stream without re-seeding seq/epoch,
  // re-running pending recovery, or re-binding lifecycle listeners.
  function openStream(): void {
    if (stream) {
      try { stream.close(); } catch (_err) { /* already closed */ }
    }
    const events = (options.newEventSource || defaultEventSource)(options.eventsURL);
    stream = events;
    const onViewers = options.onViewers;
    if (onViewers) {
      events.addEventListener("viewers", (event) => {
        try {
          onViewers((JSON.parse((event as MessageEvent<string>).data) as {count?: number} | null)?.count);
        } catch (_error) {
          // ignore malformed viewer-count payloads
        }
      });
    }
    events.addEventListener("state", (event) => {
      let message: ScopedEventMessage;
      try {
        message = parseScopedEvent((event as MessageEvent<string>).data);
      } catch (_error) {
        return;
      }
      if (message.scope !== options.scope) return;

      if (Array.isArray(message.ops)) {
        // Scoped delta: apply the ops in place, but only if they chain onto
        // what we have. A gap means we missed an event, so refetch instead of
        // misapplying. Drop deltas mid-resync; the refetch supersedes them.
        if (resyncing) return;
        // Epoch changed → the server restarted and its seq reset to a low
        // number. Our lastSeq belongs to the dead seq space, so the seq<=lastSeq
        // guard below would silently drop every post-restart delta forever.
        // Resync to adopt the new epoch+seq instead. MUST precede the seq guard.
        if (epochReset(message.epoch)) {
          options.recorder?.event("epoch-change", {scope: options.scope, from: lastEpoch, to: String(message.epoch || ""), seq: Number(message.seq) || 0});
          void resync();
          return;
        }
        // Already applied: a coalesced viewer delta whose seq range we fetched
        // past on connect arrives with seq <= lastSeq. The state already
        // reflects it, so ignore it rather than read the older prevSeq as a gap.
        if ((Number(message.seq) || 0) <= lastSeq) {
          if (!hasPendingSave()) setSyncStatus("saved");
          return;
        }
        if ((Number(message.prevSeq) || 0) !== lastSeq) {
          options.recorder?.event("gap", {scope: options.scope, have: lastSeq, prevSeq: Number(message.prevSeq) || 0, seq: Number(message.seq) || 0});
          void resync();
          return;
        }
        let next = cloneJSON(options.getState ? options.getState() : {});
        for (const op of message.ops) {
          if (op.op && op.op !== "set") continue;
          next = setAtDeltaPath(next, op.path, op.value);
        }
        lastSeq = Number(message.seq) || lastSeq;
        options.recorder?.event("delta", {scope: options.scope, seq: lastSeq, prevSeq: Number(message.prevSeq) || 0, ops: message.ops.length});
        // Delivery leg: server emit (message.emitMs) to this client rendering
        // the delta — the latency a watching co-editor/viewer feels. Carries
        // client/server clock skew; read as a rough gauge.
        if (message.emitMs) {
          feltMetric("delta-latency", {delivery_ms: Date.now() - Number(message.emitMs), seq: lastSeq, ops: message.ops.length});
        }
        options.onRemoteState?.(pending.overlay(next), message);
        if (!hasPendingSave()) setSyncStatus("saved");
        return;
      }

      // Full-state snapshot (initial / wholesale PUT / non-PATCH mutation). It
      // carries the whole state plus its own seq+epoch, so it re-baselines us
      // unconditionally — even across a server restart (changed epoch) there's
      // nothing to resync; we just adopt it.
      const raw = JSON.stringify(message.data);
      if (message.seq) lastSeq = Number(message.seq) || lastSeq;
      if (message.epoch) {
        lastEpoch = String(message.epoch);
        lastEpochSeeded = true;
      }
      options.recorder?.event("snapshot", {scope: options.scope, seq: lastSeq});
      if (consumeLocalEcho(raw)) {
        if (!hasPendingSave()) setSyncStatus("saved");
        return;
      }
      options.onRemoteState?.(pending.overlay(message.data), message);
      if (!hasPendingSave()) setSyncStatus("saved");
    });
    events.addEventListener("lockdown", () => {
      // Server entered static mode: drop the stream so the page reloads into
      // the static snapshot, instead of letting EventSource auto-reconnect.
      // Latch lockedDown so visibility recovery doesn't re-open it meanwhile.
      lockedDown = true;
      events.close();
      options.onLockdown?.();
    });
    events.addEventListener("open", () => options.recorder?.event("sse-open", {scope: options.scope, have: lastSeq}));
    events.onerror = () => {
      setSyncStatus("reconnecting");
      options.recorder?.event("sse-error", {scope: options.scope, have: lastSeq});
    };
  }

  // bindLifecycle wires tab/network listeners exactly once. connect() runs it
  // on first connect; recovery re-opens the stream without touching listeners.
  function bindLifecycle(): void {
    if (lifecycleBound) return;
    lifecycleBound = true;
    document.addEventListener("visibilitychange", () => {
      // Flush debounced edits the moment the tab is hidden or the page is
      // being navigated away from, so the 250ms debounce window can't swallow
      // the operator's last edits on reload. Paired with keepalive on the
      // PATCH, the flushed request still completes during unload.
      if (document.visibilityState === "hidden") {
        if (patchTimer) { window.clearTimeout(patchTimer); patchTimer = null; }
        if (saveTimer) { window.clearTimeout(saveTimer); saveTimer = null; }
        void flushPatch();
        void flushSave();
        return;
      }
      recoverStream();
    });
    window.addEventListener("pageshow", recoverStream);
    window.addEventListener("online", recoverStream);
  }

  // recoverStream re-opens a dead SSE stream and resyncs. iOS aggressively
  // freezes backgrounded tabs, silently killing the socket; native
  // EventSource auto-reconnect frequently never recovers on resume, leaving
  // the status stuck on a spinning "reconnecting". Guarding on
  // readyState === OPEN keeps a healthy stream from ever being churned, so
  // the steady-state cost is zero — we only act on a genuinely dead stream.
  function recoverStream(): void {
    if (lockedDown) return;
    if (document.visibilityState !== "visible") return;
    if (stream && stream.readyState === SSE_OPEN) return;
    options.recorder?.event("sse-recover", {scope: options.scope, readyState: stream?.readyState ?? null});
    openStream();
    void resync();
  }

  // resync refetches the full state after a gap and realigns lastSeq from the
  // X-State-Seq header so the next delta chains. Jittered so a fleet of viewers
  // that all gap on the same dropped event don't refetch in lockstep.
  async function resync(): Promise<void> {
    if (resyncing || !options.stateURL) return;
    resyncing = true;
    try {
      await new Promise((r) => window.setTimeout(r, Math.floor(Math.random() * 400)));
      const response = await fetch(options.stateURL);
      if (!response.ok) return;
      const seqHeader = response.headers.get("X-State-Seq");
      const epochHeader = response.headers.get("X-State-Epoch");
      const data: unknown = await response.json();
      if (seqHeader != null) lastSeq = Number(seqHeader) || 0;
      // Adopt the server's current epoch so post-resync deltas chain instead of
      // re-triggering an epoch reset every event.
      if (epochHeader) {
        lastEpoch = epochHeader;
        lastEpochSeeded = true;
      }
      options.recorder?.event("resync", {scope: options.scope, seq: lastSeq, epoch: lastEpoch});
      options.onRemoteState?.(pending.overlay(data), {scope: options.scope, resync: true});
      if (!hasPendingSave()) setSyncStatus("saved");
    } catch (error) {
      console.error(error);
    } finally {
      resyncing = false;
    }
  }

  return {connect, flushSave, flushPatch, hasPendingSave, save, patch, isPending: (path) => pending.has(path)};
}

export interface EpochTracker {
  changed(message: {epoch?: unknown} | null | undefined): boolean;
  readonly epoch: string;
}

// createEpochTracker follows the server's per-process epoch token (see
// server.epoch). The per-scope seq resets to 0 on a restart, so cached
// MatchViews keep a high seq the new space never reaches and every post-restart
// delta would be silently dropped as "already applied" — the page freezes. The
// first non-empty epoch becomes the baseline; thereafter changed() reports true
// once the token flips (the cue to reload and re-seed, since the stage cache
// merges monotonically by seq and can't adopt the lower fresh seqs). Empty
// epochs (older server builds) are ignored.
export function createEpochTracker(): EpochTracker {
  let lastEpoch = "";
  return {
    changed(message) {
      const epoch = message?.epoch ? String(message.epoch) : "";
      if (!epoch) return false;
      if (lastEpoch === "") {
        lastEpoch = epoch;
        return false;
      }
      return epoch !== lastEpoch;
    },
    get epoch() {
      return lastEpoch;
    },
  };
}

// gameEventsURL builds the SSE endpoint for a fest/game scope. The game id is
// optional: fest-level pages omit it so the server streams the whole fest.
export function gameEventsURL(festID: string | number, gameID?: string | number | null): string {
  const fest = `fest_id=${encodeURIComponent(festID)}`;
  const game = gameID ? `&game_id=${encodeURIComponent(gameID)}` : "";
  return `/events?${fest}${game}`;
}

// scheduleStaticReload reloads the page after ~5s (jittered 4-7s) so a fleet of
// static viewers spreads its reloads across the window instead of stampeding the
// server the instant lockdown lifts.
export function scheduleStaticReload(): void {
  window.setTimeout(() => window.location.reload(), 4000 + Math.floor(Math.random() * 3000));
}

// applyDeltaOps returns a deep clone of `base` with scoped set-ops applied,
// via the shared setAtDeltaPath (also used by createPendingOps.overlay), so the
// read-only viewer can reconstruct a full match view from a delta without the
// host sync controller. Non-"set" ops are skipped.
export function applyDeltaOps(base: unknown, ops: Array<StateDeltaOp | null | undefined> | null | undefined): unknown {
  let next: unknown = base == null ? {} : JSON.parse(JSON.stringify(base));
  for (const op of ops || []) {
    if (op && op.op && op.op !== "set") continue;
    next = setAtDeltaPath(next, op?.path || [], op?.value);
  }
  return next;
}

function setAtDeltaPath(root: unknown, path: PatchPath, value: unknown): unknown {
  if (!path || path.length === 0) return value;
  const [segment, ...rest] = path;
  if (typeof segment === "number") {
    const arr: unknown[] = Array.isArray(root) ? root : [];
    while (arr.length <= segment) arr.push(null);
    arr[segment] = setAtDeltaPath(arr[segment], rest, value);
    return arr;
  }
  const obj: Record<string, unknown> = root && typeof root === "object" && !Array.isArray(root)
    ? (root as Record<string, unknown>) : {};
  obj[segment] = setAtDeltaPath(obj[segment], rest, value);
  return obj;
}

// ---- Client-side state recorder ------------------------------------------
// A best-effort black box for diagnosis: a ring of timeline EVENTS (SSE
// open/close, applied deltas/snapshots, resyncs, sent/rejected patches) and a
// ring of periodic STATE snapshots, persisted to localStorage so an operator
// can download a JSON log after something looked wrong. It pairs with the two
// other evidence sources: the server audit is what COMMITTED, a HAR is what
// crossed the WIRE, and this is what THIS client believed and rendered — the
// only one that can reveal optimistic-but-never-committed state. Every
// localStorage touch is guarded; a quota error trims the oldest half instead
// of throwing.

const RECORDER_EVENT_CAP = 1500;
const RECORDER_SNAPSHOT_CAP = 40;

function recorderNow(): string {
  try {
    return new Date().toISOString();
  } catch (_e) {
    return "";
  }
}

function cheapHash(str: string): number {
  let h = 5381;
  for (let i = 0; i < str.length; i++) h = ((h << 5) + h + str.charCodeAt(i)) | 0;
  return h;
}

export interface RecorderDump {
  scope: string;
  session: string;
  ua: string;
  href: string;
  exportedAt: string;
  events: unknown[];
  snapshots: unknown[];
}

export interface ClientRecorder {
  scope: string;
  session: string;
  event(type: string, data?: object | null): void;
  snapshot(reason: string, state?: unknown, meta?: object | null): void;
  dump(): RecorderDump;
  download(): void;
  clear(): void;
  enabled: boolean;
}

export function createClientRecorder(options?: {scope?: string} | null): ClientRecorder {
  const scope = (options && options.scope) || "page";
  const evKey = `dope.rec.ev:${scope}`;
  const snapKey = `dope.rec.snap:${scope}`;
  // Per page-load id so a downloaded log spanning a reload stays separable.
  const session = Math.random().toString(36).slice(2, 10);
  let store: Storage | null = null;
  try {
    store = window.localStorage;
  } catch (_e) {
    store = null;
  }

  function load(key: string): unknown[] {
    if (!store) return [];
    try {
      return JSON.parse(store.getItem(key) || "[]") as unknown[];
    } catch (_e) {
      return [];
    }
  }
  function save(key: string, arr: unknown[]): void {
    if (!store) return;
    try {
      store.setItem(key, JSON.stringify(arr));
    } catch (_e) {
      try {
        store.setItem(key, JSON.stringify(arr.slice(Math.floor(arr.length / 2))));
      } catch (_e2) {
        /* give up silently — recording must never break the page */
      }
    }
  }
  function push(key: string, cap: number, record: object): void {
    const arr = load(key);
    arr.push(record);
    while (arr.length > cap) arr.shift();
    save(key, arr);
  }

  function event(type: string, data?: object | null): void {
    push(evKey, RECORDER_EVENT_CAP, {t: recorderNow(), s: session, type, ...(data || {})});
  }

  let lastSnapshotHash: number | null = null;
  function snapshot(reason: string, state?: unknown, meta?: object | null): void {
    let json: string | null = null;
    try {
      json = state === undefined ? null : JSON.stringify(state);
    } catch (_e) {
      json = null;
    }
    const hash = json ? cheapHash(json) : null;
    // Skip an idle "tick" that changed nothing, so quiet periods don't fill
    // the ring with identical copies.
    if (reason === "tick" && hash !== null && hash === lastSnapshotHash) return;
    lastSnapshotHash = hash;
    push(snapKey, RECORDER_SNAPSHOT_CAP, {
      t: recorderNow(),
      s: session,
      reason,
      len: json ? json.length : 0,
      ...(meta || {}),
      state: json ? JSON.parse(json) : null,
    });
  }

  function dump(): RecorderDump {
    return {
      scope,
      session,
      ua: typeof navigator !== "undefined" ? navigator.userAgent : "",
      href: typeof location !== "undefined" ? location.href : "",
      exportedAt: recorderNow(),
      events: load(evKey),
      snapshots: load(snapKey),
    };
  }
  function download(): void {
    try {
      const blob = new Blob([JSON.stringify(dump(), null, 2)], {type: "application/json"});
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `dope-log-${scope.replace(/[^\w.-]+/g, "_")}-${recorderNow().replace(/[:.]/g, "-")}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (_e) {
      /* download is best-effort */
    }
  }
  function clear(): void {
    try {
      if (store) {
        store.removeItem(evKey);
        store.removeItem(snapKey);
      }
    } catch (_e) {
      /* ignore */
    }
  }

  return {scope, session, event, snapshot, dump, download, clear, enabled: Boolean(store)};
}

export interface InstallClientRecorderOptions {
  scope?: string;
  getState?: () => unknown;
  getMeta?: () => object | null;
  intervalMs?: number;
  showButton?: boolean;
  label?: string;
}

// installClientRecorder wires a recorder for a page: periodic state snapshots,
// lifecycle markers, and (when showButton) a small floating "download log"
// button. Returns the recorder — pass it to createStateSync so its SSE timeline
// is captured too — or null when localStorage is unavailable.
export function installClientRecorder(options?: InstallClientRecorderOptions | null): ClientRecorder | null {
  const opts = options || {};
  const recorder = createClientRecorder({scope: opts.scope});
  if (!recorder.enabled) return null;
  const getState = typeof opts.getState === "function" ? opts.getState : null;
  const intervalMs = typeof opts.intervalMs === "number" && Number.isFinite(opts.intervalMs) ? opts.intervalMs : 5000;
  const snap = (reason: string) => recorder.snapshot(reason, getState ? getState() : undefined, opts.getMeta ? opts.getMeta() : null);
  snap("init");
  if (intervalMs > 0) window.setInterval(() => snap("tick"), intervalMs);
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", () => {
      recorder.event("visibility", {state: document.visibilityState});
      if (document.visibilityState === "hidden") snap("hidden");
    });
    if (opts.showButton !== false) mountRecorderButton(recorder, opts.label);
  }
  if (typeof window !== "undefined") {
    window.addEventListener("pagehide", () => recorder.event("pagehide", {}));
  }
  return recorder;
}

function mountRecorderButton(recorder: ClientRecorder, label?: string): void {
  if (document.querySelector(".dope-rec-btn")) return; // one per page
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "dope-rec-btn";
  btn.textContent = label || "Скачать лог";
  btn.title = "Скачать журнал состояния этой вкладки (для диагностики)";
  Object.assign(btn.style, {
    position: "fixed",
    bottom: "8px",
    right: "8px",
    zIndex: "2147483000",
    font: "12px/1.2 system-ui, sans-serif",
    padding: "4px 8px",
    background: "var(--diag-bg)",
    color: "var(--diag-fg)",
    border: "0",
    borderRadius: "6px",
    cursor: "pointer",
    opacity: "0.5",
  });
  btn.addEventListener("mouseenter", () => (btn.style.opacity = "1"));
  btn.addEventListener("mouseleave", () => (btn.style.opacity = "0.5"));
  btn.addEventListener("click", () => recorder.download());
  document.body.appendChild(btn);
}

export interface LiveEventsOptions {
  eventsURL: () => string;
  // Scoped-message dispatch, called after the epoch guard has passed.
  onMessage: (message: ScopedEventMessage) => void;
  onViewers?: (count: number | undefined) => void;
  onLockdown?: () => void;
  // Wake recovery re-seeds from a fresh fetch before reopening the stream.
  reload: () => Promise<void>;
  onDown?: () => void;
  onUp?: () => void;
  onRecoverError?: (error: unknown) => void;
  onStreamError?: () => void;
  // Static snapshot pages don't stream; connect() schedules the jittered
  // reload instead and wake recovery stays off.
  staticMode?: () => boolean;
  recorder?: () => ClientRecorder | null | undefined;
  recorderTags?: () => Record<string, unknown>;
  newEventSource?: (url: string) => EventSource;
}

export interface LiveEvents {
  connect(): void;
}

// createLiveEvents owns the read-side SSE lifecycle for pages that dispatch
// scoped events across stages/venues/fests (host, viewer) rather than sync one
// state blob (createStateSync). It carries the shared invariants: a changed
// server epoch means the seq space reset, so the page reloads to re-seed
// (jittered, latched — cached views merge monotonically by seq and can't adopt
// the lower fresh seqs); iOS freezes backgrounded tabs and silently kills the
// socket while native auto-reconnect sits in CONNECTING forever, so on
// visibility/network return any non-OPEN stream is dropped and re-seeded from a
// fresh fetch. Guarding on readyState === OPEN keeps a healthy stream untouched.
export function createLiveEvents(options: LiveEventsOptions): LiveEvents {
  const epochTracker = createEpochTracker();
  const recorder = () => options.recorder?.();
  const tags = () => options.recorderTags?.() || {};
  let stream: EventSource | null = null;
  let epochReloadScheduled = false;

  function connect(): void {
    if (options.staticMode?.()) {
      scheduleStaticReload();
      return;
    }
    if (stream) {
      try { stream.close(); } catch (_err) { /* already closed */ }
    }
    const events = (options.newEventSource || defaultEventSource)(options.eventsURL());
    stream = events;
    if (options.onLockdown) {
      events.addEventListener("lockdown", () => {
        // Server entered static mode: drop the stream and reload into the
        // static page (otherwise native EventSource would just auto-reconnect).
        events.close();
        options.onLockdown!();
      });
    }
    events.addEventListener("state", (event) => {
      const message = parseScopedEvent((event as MessageEvent<string>).data);
      if (epochTracker.changed(message)) {
        if (!epochReloadScheduled) {
          epochReloadScheduled = true;
          recorder()?.event("epoch-reload", {...tags(), from: epochTracker.epoch});
          scheduleStaticReload();
        }
        return;
      }
      options.onMessage(message);
    });
    events.addEventListener("viewers", (event) => {
      const onViewers = options.onViewers;
      if (!onViewers) return;
      try {
        onViewers((JSON.parse((event as MessageEvent<string>).data) as {count?: number} | null)?.count);
      } catch (_err) {
        // ignore malformed viewer-count payloads
      }
    });
    events.addEventListener("open", () => recorder()?.event("sse-open", {...tags(), have: epochTracker.epoch}));
    events.onerror = () => {
      options.onStreamError?.();
      recorder()?.event("sse-error", tags());
    };
  }

  function recover(): void {
    if (epochReloadScheduled || options.staticMode?.()) return;
    if (document.visibilityState !== "visible") return;
    if (stream && stream.readyState === SSE_OPEN) return;
    recorder()?.event("sse-recover", {...tags(), readyState: stream?.readyState ?? null});
    options.onDown?.();
    options.reload()
      .then(() => {
        options.onUp?.();
        connect();
      })
      .catch((error: unknown) => {
        options.onRecoverError?.(error);
      });
  }

  document.addEventListener("visibilitychange", recover);
  window.addEventListener("pageshow", recover);
  window.addEventListener("online", recover);

  return {connect};
}

export interface HostPresenceOptions {
  root?: HTMLElement;
  eventsURL?: string;
  presenceURL?: string;
  postDelayMs?: number;
  heartbeatMs?: number;
  staleMs?: number;
  cursorFromElement?: (element: Element | EventTarget | null) => unknown;
  getCursor?: () => unknown;
  findTarget?: (cursor: unknown) => Element | null | undefined;
}

export interface HostPresence {
  connect(): void;
  disconnect(): void;
  publish(cursor: unknown): void;
  publishCurrent(): void;
  publishFromElement(element: Element | EventTarget | null): void;
  refresh(): void;
}

interface PresenceMessage {
  userID?: number | string;
  username?: string;
  color?: string;
  active?: boolean;
  cursor?: unknown;
}

interface RemotePresence {
  userID: number | string;
  username: string;
  color: string;
  cursor: unknown;
  seenAt: number;
  node?: HTMLElement;
}

export function createHostPresence(options: HostPresenceOptions): HostPresence {
  const root = options.root || document.body;
  const postDelayMs = typeof options.postDelayMs === "number" && Number.isFinite(options.postDelayMs) ? options.postDelayMs : 80;
  const heartbeatMs = typeof options.heartbeatMs === "number" && Number.isFinite(options.heartbeatMs) ? options.heartbeatMs : 5000;
  const staleMs = typeof options.staleMs === "number" && Number.isFinite(options.staleMs) ? options.staleMs : 16000;
  const remotes = new Map<number | string, RemotePresence>();
  let selfUserID: number | string | null = null;
  let source: EventSource | null = null;
  let layer: HTMLElement | null = null;
  let publishTimer: number | null = null;
  let heartbeatTimer: number | null = null;
  let staleTimer: number | null = null;
  let lastCursor: unknown = null;
  let connected = false;
  let refreshFrame = 0;
  let stickyStyleCache: WeakMap<Element, CSSStyleDeclaration> | null = null;

  function connect(): void {
    if (connected || !options.eventsURL || !options.presenceURL) return;
    connected = true;
    ensureLayer();
    void loadSelf();
    source = new EventSource(options.eventsURL);
    source.addEventListener("presence", (event) => {
      try {
        applyPresence(JSON.parse((event as MessageEvent<string>).data) as PresenceMessage | null);
      } catch (error) {
        console.error(error);
      }
    });
    root.addEventListener("focusin", handleFocusOrClick, true);
    root.addEventListener("click", handleFocusOrClick, true);
    document.addEventListener("keydown", handleKeydown, true);
    document.addEventListener("scroll", scheduleRefresh, {capture: true, passive: true});
    window.addEventListener("scroll", scheduleRefresh, {passive: true});
    window.addEventListener("resize", scheduleRefresh);
    window.addEventListener("beforeunload", sendInactive);
    heartbeatTimer = window.setInterval(() => {
      if (lastCursor) void postPresence(true, lastCursor);
    }, heartbeatMs);
    staleTimer = window.setInterval(pruneStale, 1000);
    publishCurrentSoon();
  }

  async function loadSelf(): Promise<void> {
    try {
      const response = await fetch("/api/auth/me", {headers: {"Accept": "application/json"}});
      if (!response.ok) return;
      const me = await response.json() as {user_id?: number | string; userID?: number | string};
      selfUserID = me.user_id || me.userID || null;
      if (selfUserID && remotes.has(selfUserID)) {
        removeRemote(selfUserID);
      }
    } catch (error) {
      console.error(error);
    }
  }

  function handleFocusOrClick(event: Event): void {
    publishFromElement(event.target);
  }

  function handleKeydown(): void {
    window.requestAnimationFrame(publishCurrent);
  }

  function publishFromElement(element: Element | EventTarget | null): void {
    const cursor = options.cursorFromElement?.(element);
    if (cursor) publish(cursor);
  }

  function publishCurrentSoon(): void {
    window.requestAnimationFrame(publishCurrent);
  }

  function publishCurrent(): void {
    const cursor = options.getCursor?.() || options.cursorFromElement?.(document.activeElement);
    if (cursor) publish(cursor);
  }

  function publish(cursor: unknown): void {
    if (!cursor) return;
    lastCursor = cursor;
    window.clearTimeout(publishTimer ?? undefined);
    publishTimer = window.setTimeout(() => {
      publishTimer = null;
      void postPresence(true, cursor);
    }, postDelayMs);
  }

  async function postPresence(active: boolean, cursor?: unknown): Promise<void> {
    if (!options.presenceURL) return;
    const body = active ? {active: true, cursor} : {active: false};
    try {
      await fetch(options.presenceURL, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(body),
      });
    } catch (error) {
      console.error(error);
    }
  }

  function sendInactive(): void {
    if (!options.presenceURL) return;
    const payload = JSON.stringify({active: false});
    if (navigator.sendBeacon) {
      navigator.sendBeacon(options.presenceURL, new Blob([payload], {type: "application/json"}));
      return;
    }
    void fetch(options.presenceURL, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: payload,
      keepalive: true,
    });
  }

  function applyPresence(message: PresenceMessage | null): void {
    if (!message || !message.userID) return;
    if (selfUserID && message.userID === selfUserID) return;
    if (!message.active || !message.cursor) {
      removeRemote(message.userID);
      return;
    }
    const remote = remotes.get(message.userID) || ({} as RemotePresence);
    remote.userID = message.userID;
    remote.username = message.username || `user-${message.userID}`;
    remote.color = message.color || "var(--blue)";
    remote.cursor = message.cursor;
    remote.seenAt = Date.now();
    remotes.set(message.userID, remote);
    renderRemote(remote);
  }

  function ensureLayer(): HTMLElement {
    if (layer) return layer;
    layer = document.createElement("div");
    layer.className = "collab-cursor-layer";
    document.body.appendChild(layer);
    return layer;
  }

  function renderRemote(remote: RemotePresence): void {
    ensureLayer();
    const target = options.findTarget?.(remote.cursor);
    const node = ensureRemoteNode(remote);
    if (!target || !document.documentElement.contains(target)) {
      node.hidden = true;
      return;
    }
    const rect = target.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) {
      node.hidden = true;
      return;
    }
    if (isHiddenByScrollFrame(target, rect) || isHiddenByStickyLayer(target, rect)) {
      node.hidden = true;
      return;
    }
    node.hidden = false;
    node.style.left = `${Math.round(rect.left)}px`;
    node.style.top = `${Math.round(rect.top)}px`;
    node.style.width = `${Math.round(rect.width)}px`;
    node.style.height = `${Math.round(rect.height)}px`;
    node.style.setProperty("--cursor-color", remote.color);
    const marker = node.querySelector<HTMLElement>(".collab-cursor-marker");
    const label = node.querySelector<HTMLElement>(".collab-cursor-label");
    if (marker) marker.title = remote.username;
    if (label) label.textContent = remote.username;
  }

  function ensureRemoteNode(remote: RemotePresence): HTMLElement {
    if (remote.node) return remote.node;
    const node = document.createElement("div");
    node.className = "collab-cursor";
    const marker = document.createElement("span");
    marker.className = "collab-cursor-marker";
    const label = document.createElement("span");
    label.className = "collab-cursor-label";
    marker.appendChild(label);
    node.appendChild(marker);
    ensureLayer().appendChild(node);
    remote.node = node;
    return node;
  }

  function isHiddenByScrollFrame(target: Element, rect: DOMRect): boolean {
    const frame = target.closest?.(".sheet-frame");
    if (!frame) return false;
    const frameRect = frame.getBoundingClientRect();
    return rect.left < frameRect.left - 1 ||
      rect.right > frameRect.right + 1 ||
      rect.top < frameRect.top - 1 ||
      rect.bottom > frameRect.bottom + 1;
  }

  function isHiddenByStickyLayer(target: Element, rect: DOMRect): boolean {
    const frame = target.closest?.(".sheet-frame");
    if (!frame || target.closest?.(".sticky")) return false;
    const frameRect = frame.getBoundingClientRect();
    let stickyRight = frameRect.left;
    let stickyBottom = frameRect.top;
    const probes = stickyProbes(frame);
    for (const probe of probes) {
      const sticky = probe.node;
      if (sticky === target || sticky.contains(target) || target.contains(sticky)) continue;
      const style = probe.style;
      if (style.position !== "sticky") continue;
      const stickyRect = sticky.getBoundingClientRect();
      if (stickyRect.width <= 0 || stickyRect.height <= 0) continue;
      if (stickyRect.right <= frameRect.left || stickyRect.left >= frameRect.right || stickyRect.bottom <= frameRect.top || stickyRect.top >= frameRect.bottom) continue;

      const overlapsY = stickyRect.top < rect.bottom && stickyRect.bottom > rect.top;
      const isLeftSticky = style.left !== "auto" && stickyRect.left >= frameRect.left - 2 && stickyRect.left < frameRect.right;
      if (overlapsY && isLeftSticky) {
        stickyRight = Math.max(stickyRight, stickyRect.right);
      }

      const overlapsX = stickyRect.left < rect.right && stickyRect.right > rect.left;
      const isTopSticky = style.top !== "auto" && stickyRect.top >= frameRect.top - 2 && stickyRect.top < frameRect.bottom;
      if (overlapsX && isTopSticky) {
        stickyBottom = Math.max(stickyBottom, stickyRect.bottom);
      }
    }
    return rect.left < stickyRight - 1 || rect.top < stickyBottom - 1;
  }

  function scheduleRefresh(): void {
    if (refreshFrame) return;
    refreshFrame = requestAnimationFrame(() => {
      refreshFrame = 0;
      refresh();
    });
  }

  function refresh(): void {
    stickyStyleCache = new WeakMap();
    try {
      for (const remote of remotes.values()) {
        renderRemote(remote);
      }
    } finally {
      stickyStyleCache = null;
    }
  }

  function stickyProbes(frame: Element): Array<{node: Element; style: CSSStyleDeclaration}> {
    const nodes = frame.querySelectorAll(".sticky, thead th");
    const out: Array<{node: Element; style: CSSStyleDeclaration}> = [];
    const cache = stickyStyleCache;
    for (const node of nodes) {
      let style: CSSStyleDeclaration;
      if (cache) {
        const cached = cache.get(node);
        if (cached) {
          style = cached;
        } else {
          style = window.getComputedStyle(node);
          cache.set(node, style);
        }
      } else {
        style = window.getComputedStyle(node);
      }
      out.push({node, style});
    }
    return out;
  }

  function pruneStale(): void {
    const cutoff = Date.now() - staleMs;
    for (const [userID, remote] of remotes.entries()) {
      if (remote.seenAt < cutoff) {
        removeRemote(userID);
      }
    }
  }

  function removeRemote(userID: number | string): void {
    const remote = remotes.get(userID);
    if (remote?.node) remote.node.remove();
    remotes.delete(userID);
  }

  function disconnect(): void {
    if (!connected) return;
    connected = false;
    window.clearTimeout(publishTimer ?? undefined);
    window.clearInterval(heartbeatTimer ?? undefined);
    window.clearInterval(staleTimer ?? undefined);
    if (refreshFrame) {
      cancelAnimationFrame(refreshFrame);
      refreshFrame = 0;
    }
    source?.close();
    source = null;
    sendInactive();
    root.removeEventListener("focusin", handleFocusOrClick, true);
    root.removeEventListener("click", handleFocusOrClick, true);
    document.removeEventListener("keydown", handleKeydown, true);
    document.removeEventListener("scroll", scheduleRefresh, {capture: true});
    window.removeEventListener("scroll", scheduleRefresh);
    window.removeEventListener("resize", scheduleRefresh);
    for (const userID of Array.from(remotes.keys())) removeRemote(userID);
  }

  return {connect, disconnect, publish, publishCurrent, publishFromElement, refresh: scheduleRefresh};
}
