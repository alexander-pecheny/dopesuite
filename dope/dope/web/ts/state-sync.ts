// dope's SSE state-sync engine: the scoped event protocol (deltas chained by
// seq, re-baselined by snapshot/resync, reset by server epoch), the durable
// pending-ops overlay, stream lifecycle with iOS-wake recovery, and the client
// recorder that captures its timeline. Two primitives, one implementation for
// every game page: createLiveEvents reads (a scope map of what a delta chains
// onto and who adopts the result), createScopedWriter writes (patches
// coalesced per scope, structural writes with an intent, both overlaid until
// acked). OD and SI register one game-state scope; EK and Brain one per match.

import S from "./i18nstrings.js";

// EventSource.OPEN, spelled numerically so fake streams need no global.
const SSE_OPEN = 1;

function defaultEventSource(url: string): EventSource {
  return new EventSource(url);
}

// Backoff between wake-recovery attempts, in ms; the last entry repeats.
const WAKE_RETRY_MS = [3000, 6000, 12000, 30000];

interface WakeRecoveryOptions {
  // live reports a stream that needs no recovery, so a tab switch on a working
  // page costs nothing.
  live: () => boolean;
  // paused reports a state where the page deliberately has no stream (server
  // lockdown, static snapshot, a latched epoch reload).
  paused: () => boolean;
  // recover re-opens the stream and re-seeds the state, and reports whether that
  // worked; it is retried until it does.
  recover: () => Promise<boolean>;
}

interface WakeRecovery {
  bind(): void;
}

// createWakeRecovery pulls a dead SSE stream back up when the tab or the network
// returns. iOS freezes backgrounded tabs and silently kills the socket, while
// native EventSource auto-reconnect sits in CONNECTING forever — so a resumed
// page spins on "reconnecting" until something re-opens the stream.
//
// One attempt is not enough, which is what left the spinner running for good:
// iOS wakes the tab with the radio still down, that attempt fails, and no
// further visibility event is coming to trigger another. So once recovery is
// needed the helper retries on a backoff until an attempt actually succeeds —
// it deliberately does not stop at a stream that looks OPEN again, because a
// natively re-connected socket still leaves the page on state it never re-seeded
// (and on the spinner that says so).
function createWakeRecovery(options: WakeRecoveryOptions): WakeRecovery {
  let timer: number | null = null;
  let attempt = 0;
  let running = false;
  let recovering = false;

  function cancelRetry(): void {
    if (timer !== null) {
      window.clearTimeout(timer);
      timer = null;
    }
  }

  function scheduleRetry(): void {
    cancelRetry();
    const delay = WAKE_RETRY_MS[Math.min(attempt, WAKE_RETRY_MS.length - 1)];
    attempt += 1;
    timer = window.setTimeout(() => {
      timer = null;
      void attemptRecover();
    }, delay);
  }

  async function attemptRecover(): Promise<void> {
    if (running) return;
    // A hidden tab is about to be frozen again: hold the chain (recovering stays
    // latched) and pick it up when the page comes back.
    if (options.paused() || document.visibilityState !== "visible") {
      cancelRetry();
      return;
    }
    if (!recovering && options.live()) {
      cancelRetry();
      return;
    }
    recovering = true;
    running = true;
    let recovered = false;
    try {
      recovered = await options.recover();
    } catch (_error) {
      // recover() reports the failure; retrying is the handling.
    } finally {
      running = false;
    }
    if (recovered) {
      recovering = false;
      attempt = 0;
      cancelRetry();
      return;
    }
    scheduleRetry();
  }

  // kick restarts the backoff from zero: a tab or network event is fresh news,
  // not the continuation of a chain that has been failing for a while.
  function kick(): void {
    attempt = 0;
    cancelRetry();
    void attemptRecover();
  }

  return {
    bind(): void {
      document.addEventListener("visibilitychange", kick);
      window.addEventListener("pageshow", kick);
      window.addEventListener("online", kick);
    },
  };
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
// refetch. createScopedWriter keeps one per scope.
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
// Persistence is opt-in (viewers pass no key) and TTL-bounded so a
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

// A SyncIndicator turns what the engine and the writer know into the one
// status dot a page shows: offline beats everything, then a failed write
// nothing has superseded, then any write in flight, else saved.
export interface SyncIndicator {
  busy(key: string): void;
  idle(key: string, ok: boolean): void;
  fail(): void;
  offline(): void;
  online(): void;
  // touch reports a remote update landing while nothing is pending: a stale
  // "error" from an earlier write gives way to "saved".
  touch(): void;
  readonly state: SyncStatus;
}

export function createSyncIndicator(set: (state: SyncStatus) => void = () => {}): SyncIndicator {
  const pending = new Set<string>();
  let failed = false;
  let down = false;
  let state: SyncStatus = "saved";
  function show(): void {
    state = down ? "reconnecting" : failed ? "error" : pending.size > 0 ? "saving" : "saved";
    set(state);
  }
  return {
    busy(key) { pending.add(key); show(); },
    idle(key, ok) { pending.delete(key); if (ok) failed = false; show(); },
    fail() { failed = true; show(); },
    offline() { down = true; show(); },
    online() { down = false; show(); },
    touch() { if (pending.size === 0) failed = false; show(); },
    get state() { return state; },
  };
}

// The wire shape of one op sent to a scope's PATCH endpoint. Cell edits are
// set-ops; a page's encode() may also emit remove-ops.
export interface WireOp {
  op?: "set" | "remove";
  path: Array<string | number>;
  value?: unknown;
}

export interface WriteRequest {
  url: string;
  method?: string;
  body?: unknown;
}

// A structural write's intent: the value the scope's views must show at `path`
// (view-relative) until the write settles (a finish tick), so an incoming
// broadcast can never revert it meanwhile.
export interface WriteIntent {
  path: PatchPath;
  value: unknown;
}

export type SendResult = {ok: true; response: unknown} | {ok: false; error: string};

export interface WriteRejected {
  scope: string;
  ops: PendingOp[];
  error: string;
}

export interface ScopedWriterOptions {
  // Editors persist un-acked edits per scope; viewers never write.
  readonly?: boolean;
  // Where a scope's queued cell ops PATCH to.
  urlOf: (scope: string) => string;
  // A page whose views are keyed differently from its wire (EK: a team's slot
  // on screen, its id on the wire) translates here. null means the scope has
  // no base to translate against yet — the ops stay queued for a later flush.
  // Ops that translate to nothing are dropped as unsendable, not retried.
  encode?: (scope: string, ops: PendingOp[]) => WireOp[] | null;
  // The server's post-write view of the scope, to adopt.
  adopt: (scope: string, response: unknown) => void;
  // Where the edited document sits inside an adopted view (Brain's ops address
  // view.state); queued ops overlay relative to it, intents at the view root.
  docPath?: PatchPath;
  indicator?: SyncIndicator;
  debounceMs?: number;
  recorder?: () => ClientRecorder | null | undefined;
  onRejected?: (info: WriteRejected) => void;
  storagePrefix?: string;
}

export interface ScopedWriter {
  // Queue one cell edit; it coalesces with others to the same scope and goes
  // out as one PATCH after the debounce window.
  patch(scope: string, path: PatchPath, value: unknown): void;
  // A structural write (finish, venue, add a shootout theme): not coalesced,
  // sent at once; with an intent it stays overlaid on the scope's views until
  // its own write settles and no newer intent for that path has superseded it.
  send(scope: string, request: WriteRequest, intent?: WriteIntent): Promise<SendResult>;
  // Re-apply the scope's un-acked edits and intents onto a view about to render.
  overlay<T>(scope: string, view: T): T;
  isPending(scope: string, path: PatchPath): boolean;
  hasPending(): boolean;
  // Un-acked edits a previous page load persisted for this scope: how many;
  // they re-send. Call once the scope has a base to render them onto.
  recover(scope: string): number;
  flush(): Promise<void>;
}

// createScopedWriter is the one write discipline for every game page: an edit
// is applied to the screen at once, queued as a set-op per scope, coalesced
// with its neighbours into ONE PATCH per scope per debounce window, re-overlaid
// on every view the page renders until the server confirms it (so a slow write
// never visibly regresses), retried on 5xx/offline, dropped loudly on 4xx,
// mirrored to localStorage so a reload mid-sync recovers it, and flushed when
// the tab hides (keepalive lets the request finish during unload).
export function createScopedWriter(options: ScopedWriterOptions): ScopedWriter {
  const debounceMs = options.debounceMs ?? 150;
  const indicator = options.indicator || createSyncIndicator();
  const storagePrefix = options.storagePrefix ?? "dope.pending.v2";
  const docPath = options.docPath || [];
  interface Entry { ops: PendingOps; inFlight: boolean; timer: number | null }
  const entries = new Map<string, Entry>();
  const intents = new Map<string, {scope: string; path: PatchPath; value: unknown; token: number}>();
  let token = 0;
  let lifecycleBound = false;

  const monoNow = () => (typeof performance !== "undefined" && performance.now ? performance.now() : Date.now());

  function entry(scope: string): Entry {
    let e = entries.get(scope);
    if (!e) {
      e = {ops: createPendingOps({storageKey: options.readonly ? null : `${storagePrefix}:${scope}`}), inFlight: false, timer: null};
      entries.set(scope, e);
    }
    return e;
  }

  function bindLifecycle(): void {
    if (lifecycleBound) return;
    lifecycleBound = true;
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden") void flush();
    });
  }

  function patch(scope: string, path: PatchPath, value: unknown): void {
    if (options.readonly) return;
    try {
      entry(scope).ops.add(path, value);
    } catch (error) {
      console.error(error);
      indicator.fail();
      return;
    }
    indicator.busy(scope);
    schedule(scope, debounceMs);
    bindLifecycle();
  }

  function schedule(scope: string, delay: number): void {
    const e = entry(scope);
    window.clearTimeout(e.timer ?? undefined);
    e.timer = window.setTimeout(() => {
      e.timer = null;
      void flushScope(scope);
    }, delay);
  }

  async function flushScope(scope: string): Promise<void> {
    const e = entry(scope);
    if (options.readonly || e.inFlight || e.ops.queued() === 0) return;
    const ops = e.ops.take();
    const wire = options.encode ? options.encode(scope, ops) : ops.map((op) => ({path: op.path, value: op.value}));
    if (wire === null) {
      e.ops.ack(ops);
      e.ops.requeue(ops);
      return;
    }
    if (wire.length === 0) {
      e.ops.ack(ops);
      indicator.idle(scope, true);
      return;
    }
    e.inFlight = true;
    let saved = false;
    let retry = true;
    const tSend = monoNow();
    try {
      const response = await fetch(options.urlOf(scope), {
        method: "PATCH",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({ops: wire}),
        keepalive: true,
      });
      if (!response.ok) {
        retry = response.status >= 500;
        throw new Error(await response.text());
      }
      const updated: unknown = await response.json();
      options.recorder?.()?.event("patch-rtt", {scope, rtt_ms: Math.round(monoNow() - tSend), ops: wire.length, status: response.status});
      e.ops.ack(ops);
      options.adopt(scope, updated);
      saved = true;
    } catch (error) {
      e.ops.ack(ops);
      if (retry) {
        e.ops.requeue(ops);
      } else {
        console.error("dropped rejected patch ops", {scope, error: String(error), ops});
        options.onRejected?.({scope, ops, error: String(error)});
      }
      indicator.fail();
    } finally {
      e.inFlight = false;
      if (e.ops.queued() > 0) {
        if (!e.timer) schedule(scope, saved ? 0 : 2000);
      } else {
        indicator.idle(scope, saved);
      }
    }
  }

  async function send(scope: string, request: WriteRequest, intent?: WriteIntent): Promise<SendResult> {
    if (options.readonly) return {ok: false, error: "readonly"};
    const own = ++token;
    const key = `${scope} ${own}`;
    const intentKey = intent ? `${scope} ${JSON.stringify(normalizePatchPath(intent.path))}` : null;
    if (intentKey && intent) intents.set(intentKey, {scope, path: intent.path, value: intent.value, token: own});
    indicator.busy(key);
    let result: SendResult;
    try {
      const response = await fetch(request.url, {
        method: request.method || "POST",
        headers: {"Content-Type": "application/json"},
        body: request.body === undefined ? undefined : JSON.stringify(request.body),
      });
      if (!response.ok) throw new Error((await response.text()).trim());
      const body: unknown = await response.json();
      options.adopt(scope, body);
      result = {ok: true, response: body};
    } catch (error) {
      console.error(error);
      indicator.fail();
      result = {ok: false, error: error instanceof Error ? error.message : String(error)};
    } finally {
      if (intentKey && intents.get(intentKey)?.token === own) intents.delete(intentKey);
    }
    indicator.idle(key, result.ok);
    return result;
  }

  function overlay<T>(scope: string, view: T): T {
    const e = entries.get(scope);
    const own = [...intents.values()].filter((intent) => intent.scope === scope);
    if (!(e && e.ops.size() > 0) && own.length === 0) return view;
    let out: unknown = cloneJSON(view);
    if (e) for (const op of e.ops.all()) out = setAtDeltaPath(out, [...docPath, ...op.path], op.value);
    for (const intent of own) out = setAtDeltaPath(out, intent.path, intent.value);
    return out as T;
  }

  function isPending(scope: string, path: PatchPath): boolean {
    const e = entries.get(scope);
    if (e && e.ops.has(path)) return true;
    return intents.has(`${scope} ${JSON.stringify(normalizePatchPath(path))}`);
  }

  function hasPending(): boolean {
    for (const e of entries.values()) if (e.inFlight || e.timer !== null || e.ops.size() > 0) return true;
    return intents.size > 0;
  }

  function recover(scope: string): number {
    if (options.readonly) return 0;
    const count = entry(scope).ops.queued();
    if (count === 0) return 0;
    options.recorder?.()?.event("recovered-pending", {scope, count});
    indicator.busy(scope);
    schedule(scope, 0);
    bindLifecycle();
    return count;
  }

  async function flush(): Promise<void> {
    for (const e of entries.values()) {
      if (e.timer !== null) { window.clearTimeout(e.timer); e.timer = null; }
    }
    await Promise.all([...entries.keys()].map((scope) => flushScope(scope)));
  }

  return {patch, send, overlay, isPending, hasPending, recover, flush};
}

export interface EpochTracker {
  changed(message: {epoch?: unknown} | null | undefined): boolean;
  // adopt takes a token a full state carried (snapshot, resync) as the baseline.
  adopt(epoch: string): void;
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
    adopt(epoch) {
      if (epoch) lastEpoch = epoch;
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
// button. Returns the recorder — pass it to the engine so its SSE timeline
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
  btn.textContent = label || S.widgets.recorder.label();
  btn.title = S.widgets.recorder.title();
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

// A scope's view as the engine sees it: the data a delta applies onto and the
// seq that data is at.
export interface Versioned<T = unknown> {
  data: T;
  seq: number;
}

// One family of scopes a page listens to. `prefix` matches the scope string
// exactly or as a prefix ("game-state:7", "match:7:", "fest:").
export interface ScopeSpec<T = unknown> {
  prefix: string;
  // What a delta for `scope` chains onto; null when the page holds no view for
  // it. Omit for scopes that only ever arrive as snapshots.
  base?: (scope: string) => Versioned<T> | null;
  // A full view for `scope`: a snapshot, a delta the engine applied, or a resync.
  adopt: (scope: string, view: Versioned<T>, message: ScopedEventMessage | null) => void;
  // A delta that cannot chain (no base, or a seq gap): the page evicts and
  // refetches what it shows. Default: resync via stateURL when set, else the
  // engine's reload().
  gap?: (scope: string, message: ScopedEventMessage) => void;
  // GET endpoint whose body is the scope's state and whose X-State-Seq /
  // X-State-Epoch headers realign the engine — the game-state protocol.
  stateURL?: (scope: string) => string;
}

export interface LiveEventsOptions {
  eventsURL: () => string;
  scopes: ScopeSpec[];
  // The engine's own game: game-state events of sibling games sharing the fest
  // stream are dropped, everything else unregistered goes to onUnhandled.
  gameID?: string | number | null;
  onUnhandled?: (message: ScopedEventMessage) => void;
  // The server epoch the page rendered under, so a restart between render and
  // the first event still reads as a reset.
  epoch?: string | null;
  indicator?: SyncIndicator;
  onViewers?: (count: number | undefined) => void;
  onLockdown?: () => void;
  // Wake recovery re-seeds the page from a fresh fetch before reopening the
  // stream. Default: resync every scope that has a stateURL.
  reload?: () => Promise<void>;
  onRecoverError?: (error: unknown) => void;
  // Static snapshot pages don't stream; connect() schedules the jittered
  // reload instead and wake recovery stays off.
  staticMode?: () => boolean;
  recorder?: () => ClientRecorder | null | undefined;
  newEventSource?: (url: string) => EventSource;
}

export interface LiveEvents {
  connect(): void;
  // Refetch one scope's state (or every stateURL scope) and adopt it, realigned
  // to the server's seq and epoch. Resolves whether the state was re-seeded.
  resync(scope?: string): Promise<boolean>;
}

// createLiveEvents owns the read side of the scoped SSE protocol for every game
// page: it opens the stream, drops what was already applied (seq <= have),
// applies a delta that chains (prevSeq === have) onto the page's base and hands
// the page the full view, reports a gap for the page to refetch, adopts
// snapshots as they come, and carries the shared invariants — a changed server
// epoch means the seq space reset (a game-state page resyncs from its state
// endpoint; a multi-view page reloads, since its caches merge monotonically by
// seq and cannot adopt the lower fresh seqs), a lockdown drops the stream, and
// any non-OPEN stream is dropped and re-seeded by the shared wake recovery.
export function createLiveEvents(options: LiveEventsOptions): LiveEvents {
  const epochTracker = createEpochTracker();
  if (options.epoch) epochTracker.changed({epoch: options.epoch});
  const indicator = options.indicator || createSyncIndicator();
  const recorder = () => options.recorder?.();
  const resyncable = options.scopes.length > 0 && options.scopes.every((s) => s.stateURL);
  let stream: EventSource | null = null;
  let epochReloadScheduled = false;
  const resyncing = new Set<string>();

  function specFor(scope: string): ScopeSpec | null {
    let best: ScopeSpec | null = null;
    for (const spec of options.scopes) {
      if (scope === spec.prefix || scope.startsWith(spec.prefix)) {
        if (!best || spec.prefix.length > best.prefix.length) best = spec;
      }
    }
    return best;
  }

  function dispatch(message: ScopedEventMessage): void {
    const scope = message.scope;
    const spec = specFor(scope);
    if (!spec) {
      const sibling = scope.startsWith("game-state:") && options.gameID != null && scope !== `game-state:${options.gameID}`;
      if (!sibling) options.onUnhandled?.(message);
      return;
    }
    if (resyncing.has(scope)) return;
    const seq = Number(message.seq) || 0;
    const base = spec.base ? spec.base(scope) : null;
    if (Array.isArray(message.ops)) {
      if (base && seq <= base.seq) {
        indicator.touch();
        return;
      }
      const prev = Number(message.prevSeq) || 0;
      if (!base || base.seq !== prev) {
        recorder()?.event("gap", {scope, have: base?.seq ?? null, prevSeq: prev, seq});
        gap(spec, scope, message);
        return;
      }
      const data = applyDeltaOps(base.data, message.ops);
      recorder()?.event("delta", {scope, seq, prevSeq: prev, ops: message.ops.length});
      if (message.emitMs) recorder()?.event("delta-latency", {scope, delivery_ms: Date.now() - Number(message.emitMs), seq});
      spec.adopt(scope, {data, seq: seq || prev}, message);
      indicator.touch();
      return;
    }
    if (base && seq && seq <= base.seq) {
      indicator.touch();
      return;
    }
    recorder()?.event("snapshot", {scope, seq});
    spec.adopt(scope, {data: message.data, seq: seq || base?.seq || 0}, message);
    indicator.touch();
  }

  function gap(spec: ScopeSpec, scope: string, message: ScopedEventMessage): void {
    if (spec.gap) spec.gap(scope, message);
    else if (spec.stateURL) void resync(scope);
    else void reload();
  }

  async function reload(): Promise<void> {
    if (options.reload) return options.reload();
    if (!(await resync())) throw new Error("resync failed");
  }

  // resync refetches a scope's full state and realigns seq/epoch from the
  // headers so the next delta chains. Jittered so a fleet of viewers that all
  // gap on the same dropped event don't refetch in lockstep; deadlined because
  // a frozen tab can leave the fetch hanging, and deltas are dropped meanwhile.
  async function resync(scope?: string): Promise<boolean> {
    if (scope === undefined) {
      const results = await Promise.all(options.scopes.filter((s) => s.stateURL).map((s) => resync(s.prefix)));
      return results.every(Boolean);
    }
    const spec = specFor(scope);
    if (!spec?.stateURL) return false;
    if (resyncing.has(scope)) return true;
    resyncing.add(scope);
    const abort = new AbortController();
    const deadline = window.setTimeout(() => abort.abort(), 20000);
    try {
      await new Promise((r) => window.setTimeout(r, Math.floor(Math.random() * 400)));
      const response = await fetch(spec.stateURL(scope), {signal: abort.signal});
      if (!response.ok) return false;
      const seq = Number(response.headers.get("X-State-Seq")) || 0;
      const epoch = response.headers.get("X-State-Epoch");
      const data: unknown = await response.json();
      if (epoch) epochTracker.adopt(epoch);
      recorder()?.event("resync", {scope, seq, epoch: epochTracker.epoch});
      spec.adopt(scope, {data, seq}, null);
      indicator.touch();
      return true;
    } catch (error) {
      console.error(error);
      return false;
    } finally {
      window.clearTimeout(deadline);
      resyncing.delete(scope);
    }
  }

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
    events.addEventListener("lockdown", () => {
      // Server entered static mode: drop the stream and reload into the static
      // page (otherwise native EventSource would just auto-reconnect).
      events.close();
      epochReloadScheduled = true;
      options.onLockdown?.();
    });
    events.addEventListener("state", (event) => {
      let message: ScopedEventMessage;
      try {
        message = parseScopedEvent((event as MessageEvent<string>).data);
      } catch (_error) {
        return;
      }
      // A snapshot carries its own seq and epoch and re-baselines a game-state
      // page on its own; a delta from a new epoch cannot chain, so resync. A
      // multi-view page reloads on either.
      if (resyncable && !Array.isArray(message.ops)) epochTracker.adopt(String(message.epoch || ""));
      if (epochTracker.changed(message)) {
        if (resyncable) {
          recorder()?.event("epoch-change", {from: epochTracker.epoch, to: String(message.epoch || "")});
          void resync();
          return;
        }
        if (!epochReloadScheduled) {
          epochReloadScheduled = true;
          recorder()?.event("epoch-reload", {from: epochTracker.epoch});
          scheduleStaticReload();
        }
        return;
      }
      dispatch(message);
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
    events.addEventListener("open", () => {
      indicator.online();
      recorder()?.event("sse-open", {epoch: epochTracker.epoch});
    });
    events.onerror = () => {
      indicator.offline();
      recorder()?.event("sse-error", {});
    };
  }

  async function recover(): Promise<boolean> {
    recorder()?.event("sse-recover", {readyState: stream?.readyState ?? null});
    indicator.offline();
    try {
      await reload();
    } catch (error: unknown) {
      options.onRecoverError?.(error);
      return false;
    }
    indicator.online();
    connect();
    return true;
  }

  createWakeRecovery({
    live: () => stream !== null && stream.readyState === SSE_OPEN,
    paused: () => epochReloadScheduled || Boolean(options.staticMode?.()),
    recover,
  }).bind();

  return {connect, resync};
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
