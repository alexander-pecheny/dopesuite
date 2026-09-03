// stats-sync.ts — the live stats-page recompute loop (DopeStatsSync).
//
// The EK stats table stays live off the same SSE stream the bracket uses: the
// engine folds each match's events into the shared stage cache and the table
// recomputes from memory — no refetch — throttled to a few times a second. A
// delta that couldn't chain means a dropped event, so the bracket resyncs once,
// debounced so a fleet that all gap together doesn't stampede the bulk endpoint.
//
// It is a create(deps) factory like DopeStageCache: the stage cache, whether the
// stats view is currently active, and how to rerender it are injected, so the
// throttle and the debounce are unit-testable with fake timers.

export interface StatsSyncStageCache {
  prefetchAllStages(): Promise<unknown>;
}

export interface StatsSyncDeps {
  stageCache: StatsSyncStageCache;
  isActive: () => boolean;
  rerender: () => void;
  setTimeout?: (handler: () => void, timeoutMs: number) => unknown;
  throttleMs?: number | null;
  resyncMs?: number | null;
}

export interface StatsSync {
  scheduleRerender(): void;
  scheduleResync(): void;
}

// isActive() gates work to when the stats view is shown; rerender() recomputes
// and swaps in the table.
export function create(deps: StatsSyncDeps): StatsSync {
  const setTimeoutFn = deps.setTimeout || window.setTimeout.bind(window);
  const throttleMs = deps.throttleMs != null ? deps.throttleMs : 400;
  const resyncMs = deps.resyncMs != null ? deps.resyncMs : 400;
  const stageCache = deps.stageCache;
  const isActive = deps.isActive;
  const rerender = deps.rerender;

  let rerenderTimer: unknown = null;
  let rerenderPending = false;
  let resyncTimer: unknown = null;

  // scheduleRerender throttles the in-memory recompute to once per throttleMs
  // (leading + trailing) so a burst of cell deltas rebuilds a few times a
  // second at most while staying near-live.
  function scheduleRerender(): void {
    if (!isActive()) return;
    if (rerenderTimer) {
      rerenderPending = true;
      return;
    }
    rerender();
    rerenderTimer = setTimeoutFn(function tick() {
      if (rerenderPending && isActive()) {
        rerenderPending = false;
        rerender();
        rerenderTimer = setTimeoutFn(tick, throttleMs);
      } else {
        rerenderTimer = null;
      }
    }, throttleMs);
  }

  // scheduleResync refetches the bracket once after a dropped SSE event, then
  // recomputes. Debounced so a fleet that all gap together doesn't stampede
  // the bulk endpoint.
  function scheduleResync(): void {
    if (resyncTimer) return;
    resyncTimer = setTimeoutFn(function () {
      resyncTimer = null;
      stageCache.prefetchAllStages()
        .then(function () { if (isActive()) rerender(); })
        .catch(function (error) { console.error(error); });
    }, resyncMs);
  }

  return { scheduleRerender, scheduleResync };
}

export const DopeStatsSync = { create };
