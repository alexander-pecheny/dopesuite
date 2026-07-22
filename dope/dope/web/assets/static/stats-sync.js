// stats-sync.js — the live stats-page resync loop (window.DopeStatsSync),
// factored out of host.js and viewer.js, which held byte-identical copies.
//
// The EK stats table stays live off the same SSE stream the bracket uses: each
// match-scoped event folds into the shared stage cache in place (a chained
// delta, or a full snapshot) and the table recomputes from memory — no refetch.
// A delta that can't chain (missing base / seq gap) means a dropped event, so
// the bracket resyncs once. The recompute is throttled and the resync debounced.
//
// It is a create(deps) factory like DopeStageCache: every page-specific piece —
// the stage cache, the table library, whether the stats view is currently
// active, and how to rerender it — is injected, so the throttle/coalesce/gap
// logic is unit-testable with fake timers.
(function () {
  "use strict";

  // create(deps): { stageCache, gameTable, matchCodeFromScope, isActive, rerender,
  //   setTimeout?, throttleMs?, resyncMs? } → { applyMatchEvent, scheduleRerender,
  //   scheduleResync }. isActive() gates work to when the stats view is shown;
  //   rerender() recomputes and swaps in the table.
  function create(deps) {
    var setTimeoutFn = deps.setTimeout || window.setTimeout.bind(window);
    var throttleMs = deps.throttleMs != null ? deps.throttleMs : 400;
    var resyncMs = deps.resyncMs != null ? deps.resyncMs : 400;
    var stageCache = deps.stageCache;
    var gameTable = deps.gameTable;
    var matchCodeFromScope = deps.matchCodeFromScope;
    var isActive = deps.isActive;
    var rerender = deps.rerender;

    var rerenderTimer = null;
    var rerenderPending = false;
    var resyncTimer = null;

    // scheduleRerender throttles the in-memory recompute to once per throttleMs
    // (leading + trailing) so a burst of cell deltas rebuilds a few times a
    // second at most while staying near-live.
    function scheduleRerender() {
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
    function scheduleResync() {
      if (resyncTimer) return;
      resyncTimer = setTimeoutFn(function () {
        resyncTimer = null;
        stageCache.prefetchAllStages()
          .then(function () { if (isActive()) rerender(); })
          .catch(function (error) { console.error(error); });
      }, resyncMs);
    }

    function applyMatchEvent(message) {
      var code = matchCodeFromScope(message.scope);
      if (Array.isArray(message.ops)) {
        var base = stageCache.matchState(code);
        var prev = Number(message.prevSeq) || 0;
        if (base && (Number(message.seq) || 0) <= (Number(base.seq) || 0)) return; // already applied
        if (!base || (Number(base.seq) || 0) !== prev) {
          scheduleResync();
          return;
        }
        var next = gameTable.applyDeltaOps(base, message.ops);
        next.seq = Number(message.seq) || prev;
        stageCache.applyMatchUpdate(next);
      } else if (message.data && message.data.code) {
        var view = message.data;
        view.seq = Number(message.seq) || 0;
        stageCache.applyMatchUpdate(view);
      } else {
        scheduleResync();
        return;
      }
      scheduleRerender();
    }

    return { applyMatchEvent: applyMatchEvent, scheduleRerender: scheduleRerender, scheduleResync: scheduleResync };
  }

  window.DopeStatsSync = { create: create };
})();
