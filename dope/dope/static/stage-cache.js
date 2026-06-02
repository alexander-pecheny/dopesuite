// Per-stage data + pane cache shared by the EK host and viewer pages.
//
// The cache owns:
//   - stageDataByCode    stageCode -> {matches, stateByCode: Map<matchCode, MatchView>}
//   - stagePaneByCode    stageCode -> HTMLElement (the pane wrapper mounted in the container)
//   - stageFetchPromises stageCode -> in-flight prefetch Promise (dedupe)
//   - matchCodeToStageCode matchCode -> stageCode (SSE routing)
//   - cachesRevision     fest.revision the caches were built against (drop on bump)
//
// The consumer (host.js / viewer.js) provides callbacks that fill the pane's
// DOM, react to data changes, and read fest scheme. Tab switching then reduces
// to toggling `hidden` on an already-built pane.
(function () {
  function cssEscape(value) {
    return window.CSS?.escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&");
  }

  function createStageCache(options) {
    const {
      container,
      paneClassName = "stage-pane",
      apiBase,
      schemeStages,
      findStage,
      stageType,
      getMatches,
      buildPaneContent,
      onStageDataChanged,
      onMatchUpdated,
      onPaneShown,
      cleanupPane,
    } = options;

    const stageDataByCode = new Map();
    const stagePaneByCode = new Map();
    const stageFetchPromises = new Map();
    const matchCodeToStageCode = new Map();
    let cachesRevision = null;

    function adoptFest(view) {
      // Stage caches are tied to a specific fest revision. A revision bump means
      // the stage list or match metadata may have changed under us; drop caches
      // so we rebuild against the new shape.
      if (cachesRevision != null && cachesRevision !== view?.revision) {
        clear();
      }
      if (view?.revision != null) cachesRevision = view.revision;
      indexAllStages();
    }

    // Walk the scheme stages and seed stageDataByCode + matchCodeToStageCode
    // for every stage. Done eagerly so SSE match-update events can find their
    // stage even for stages we haven't fetched yet.
    function indexAllStages() {
      const stages = schemeStages() || [];
      for (const stage of stages) {
        ensureStageData(stage.code);
      }
    }

    function ensureStageData(stageCode) {
      let data = stageDataByCode.get(stageCode);
      const stage = findStage(stageCode);
      const matches = getMatches(stage) || [];
      if (!data) {
        data = {matches, stateByCode: new Map()};
        stageDataByCode.set(stageCode, data);
      } else if (data.matches !== matches) {
        // Fest revision may have rewritten the match list; keep the same
        // stateByCode entries that still correspond to known codes.
        const known = new Set(matches.map((m) => m.code));
        for (const code of Array.from(data.stateByCode.keys())) {
          if (!known.has(code)) data.stateByCode.delete(code);
        }
        data.matches = matches;
      }
      for (const m of matches) {
        if (m?.code) matchCodeToStageCode.set(m.code, stageCode);
      }
      return data;
    }

    // applyStageBatch folds an array of MatchViews (from a per-stage or bulk
    // fetch) into the cache and notifies any open pane.
    function applyStageBatch(stageCode, batchedMatches) {
      const data = ensureStageData(stageCode);
      if (Array.isArray(batchedMatches)) {
        for (const m of batchedMatches) {
          if (m?.code) data.stateByCode.set(m.code, m);
        }
      }
      const pane = stagePaneByCode.get(stageCode);
      if (pane) onStageDataChanged?.({pane, stageCode, data});
      return data;
    }

    function prefetchStage(stageCode) {
      if (!stageCode) return Promise.resolve();
      const inflight = stageFetchPromises.get(stageCode);
      if (inflight) return inflight;
      const url = `${apiBase()}/stages/${encodeURIComponent(stageCode)}/matches`;
      const promise = fetch(url)
        .then(async (response) => {
          if (!response.ok) throw new Error(await response.text());
          return response.json();
        })
        .then((batchedMatches) => {
          applyStageBatch(stageCode, batchedMatches);
        })
        .catch((err) => {
          console.error("prefetch stage failed", stageCode, err);
          stageFetchPromises.delete(stageCode);
          throw err;
        });
      stageFetchPromises.set(stageCode, promise);
      return promise;
    }

    // prefetchAllStages warms every stage's match data so later tab switches are
    // instant. It pulls the whole game in ONE request (/stages/matches) instead
    // of one request per stage — far fewer round-trips, which matters most when
    // the connection is congested. Falls back to per-stage fetches if the bulk
    // endpoint is unavailable (e.g. an older server).
    function prefetchAllStages() {
      const url = `${apiBase()}/stages/matches`;
      return fetch(url)
        .then(async (response) => {
          if (!response.ok) throw new Error(await response.text());
          return response.json();
        })
        .then((stages) => {
          if (!Array.isArray(stages)) return;
          for (const st of stages) {
            if (!st?.code) continue;
            applyStageBatch(st.code, st.matches);
            // Mark fetched so a later single prefetch dedupes to the cache.
            if (!stageFetchPromises.has(st.code)) {
              stageFetchPromises.set(st.code, Promise.resolve());
            }
          }
        })
        .catch((err) => {
          console.error("bulk prefetch failed; falling back to per-stage", err);
          const stages = schemeStages() || [];
          for (const stage of stages) {
            if (stageType(stage) === "reseed") continue;
            prefetchStage(stage.code).catch(() => {});
          }
        });
    }

    function clear() {
      for (const [stageCode, pane] of stagePaneByCode) {
        cleanupPane?.({pane, stageCode});
        pane.remove();
      }
      stageDataByCode.clear();
      stagePaneByCode.clear();
      stageFetchPromises.clear();
      matchCodeToStageCode.clear();
      cachesRevision = null;
    }

    // showStage attaches a pane for the given stage to the container and hides
    // the rest. Builds the pane on first request via buildPaneContent. Removes
    // any non-pane children left behind by other renders (renderFest etc).
    function showStage(stageCode) {
      if (!stageCode) return null;
      ensureStageData(stageCode);
      let pane = stagePaneByCode.get(stageCode);
      const isFirstBuild = !pane;
      if (!pane) {
        pane = document.createElement("div");
        pane.className = paneClassName;
        pane.dataset.stageCode = stageCode;
        stagePaneByCode.set(stageCode, pane);
        const data = stageDataByCode.get(stageCode);
        const stage = findStage(stageCode);
        buildPaneContent({pane, stageCode, stage, data});
      }
      for (const node of Array.from(container.children)) {
        if (!stagePaneByCode.has(node.dataset?.stageCode)) node.remove();
      }
      for (const [code, p] of stagePaneByCode) {
        if (!p.isConnected) container.appendChild(p);
        p.hidden = code !== stageCode;
      }
      onPaneShown?.({pane, stageCode, isFirstBuild});
      return pane;
    }

    function applyMatchUpdate(updated) {
      if (!updated?.code) return {found: false};
      const stageCode = matchCodeToStageCode.get(updated.code);
      if (!stageCode) return {found: false};
      const data = ensureStageData(stageCode);
      data.stateByCode.set(updated.code, updated);
      const pane = stagePaneByCode.get(stageCode);
      if (pane) {
        const frame = pane.querySelector(
          `.stage-match-frame[data-match-code="${cssEscape(updated.code)}"]`,
        );
        const descriptor = data.matches.find((m) => m.code === updated.code) || null;
        if (frame) onMatchUpdated?.({pane, stageCode, frame, matchState: updated, descriptor, data});
      }
      return {found: true, stageCode, pane};
    }

    // matchState returns the cached MatchView for a code (the delta base), or
    // null if that match's stage hasn't been fetched.
    function matchState(code) {
      const stageCode = matchCodeToStageCode.get(code);
      if (!stageCode) return null;
      return stageDataByCode.get(stageCode)?.stateByCode.get(code) || null;
    }

    // invalidateMatch drops a match's cached view and forces its stage to
    // refetch on next access — used when a delta can't be safely applied (no
    // base or a seq gap) so the next render/prefetch pulls a fresh, correct view.
    function invalidateMatch(code) {
      const stageCode = matchCodeToStageCode.get(code);
      if (!stageCode) return;
      stageDataByCode.get(stageCode)?.stateByCode.delete(code);
      stageFetchPromises.delete(stageCode);
    }

    return {
      adoptFest,
      ensureStageData,
      prefetchStage,
      prefetchAllStages,
      clear,
      showStage,
      applyMatchUpdate,
      matchState,
      invalidateMatch,
      getPane: (stageCode) => stagePaneByCode.get(stageCode) || null,
      getData: (stageCode) => stageDataByCode.get(stageCode) || null,
      stageCodeForMatch: (matchCode) => matchCodeToStageCode.get(matchCode) || null,
      hasStage: (stageCode) => stagePaneByCode.has(stageCode),
    };
  }

  window.DopeStageCache = {create: createStageCache};
})();
