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

export interface MatchView {
  code: string;
  seq?: number;
  [key: string]: unknown;
}

export interface MatchDescriptor {
  code?: string;
  [key: string]: unknown;
}

export interface SchemeStage {
  code: string;
  [key: string]: unknown;
}

export interface StageData {
  matches: MatchDescriptor[];
  stateByCode: Map<string, MatchView>;
}

export interface MatchUpdateResult {
  found: boolean;
  stageCode?: string;
  pane?: HTMLElement;
  stale?: boolean;
}

export interface StageCacheOptions {
  container: HTMLElement;
  paneClassName?: string;
  apiBase: () => string;
  schemeStages: () => SchemeStage[] | null | undefined;
  findStage: (stageCode: string) => SchemeStage | null | undefined;
  stageType: (stage: SchemeStage | null | undefined) => string;
  getMatches: (stage: SchemeStage | null | undefined) => MatchDescriptor[] | null | undefined;
  buildPaneContent: (args: {
    pane: HTMLElement;
    stageCode: string;
    stage: SchemeStage | null | undefined;
    data: StageData | undefined;
  }) => void;
  onStageDataChanged?: (args: { pane: HTMLElement; stageCode: string; data: StageData }) => void;
  onMatchUpdated?: (args: {
    pane: HTMLElement;
    stageCode: string;
    frame: Element;
    matchState: MatchView;
    descriptor: MatchDescriptor | null;
    data: StageData;
  }) => void;
  onPaneShown?: (args: { pane: HTMLElement; stageCode: string; isFirstBuild: boolean }) => void;
  cleanupPane?: (args: { pane: HTMLElement; stageCode: string }) => void;
  overlayMatch?: (view: MatchView) => MatchView;
}

export interface StageCache {
  adoptFest(view?: unknown): void;
  ensureStageData(stageCode: string): StageData;
  prefetchStage(stageCode: string): Promise<void>;
  prefetchAllStages(): Promise<void>;
  clear(): void;
  showStage(stageCode: string): HTMLElement | null;
  applyMatchUpdate(updated: MatchView | null | undefined): MatchUpdateResult;
  matchState(code: string): MatchView | null;
  invalidateMatch(code: string): void;
  getPane(stageCode: string): HTMLElement | null;
  getData(stageCode: string): StageData | null;
  stageCodeForMatch(matchCode: string): string | null;
  hasStage(stageCode: string): boolean;
}

function cssEscape(value: string): string {
  const css = (window as { CSS?: { escape?: (ident: string) => string } }).CSS;
  return css?.escape ? css.escape(value) : String(value).replace(/["\\]/g, "\\$&");
}

export function createStageCache(options: StageCacheOptions): StageCache {
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
    // overlayMatch optionally transforms a MatchView before it is cached, used
    // by the host to re-apply un-acked local edits so a refetch/update can't
    // regress an optimistic cell. Defaults to identity (the viewer has no
    // local edits to preserve).
    overlayMatch = (view) => view,
  } = options;

  const stageDataByCode = new Map<string, StageData>();
  const stagePaneByCode = new Map<string, HTMLElement>();
  const stageFetchPromises = new Map<string, Promise<void>>();
  const matchCodeToStageCode = new Map<string, string>();
  let cachesSignature: string | null = null;

  // stageStructureSignature captures only what determines the pane/frame DOM
  // shape: the ordered stages (code + type) and, per stage, its ordered match
  // codes. A score edit bumps the fest revision WITHOUT changing this.
  function stageStructureSignature(): string {
    const stages = schemeStages() || [];
    return stages
      .map((s) => `${s.code}#${stageType(s)}:${(getMatches(s) || []).map((m) => m.code).join(",")}`)
      .join("|");
  }

  function adoptFest(_view?: unknown): void {
    // Drop the caches (tearing down panes) only when the stage/match STRUCTURE
    // changed — not on every revision bump. Every match edit bumps the fest
    // revision; keying invalidation on the raw revision used to clear() on each
    // edit-driven refetch, so showStage rebuilt panes with title-only
    // placeholders — the "skeleton" flash. ensureStageData already reconciles
    // an in-place match-list change, so a same-structure bump needs no teardown.
    const signature = stageStructureSignature();
    if (cachesSignature != null && cachesSignature !== signature) {
      clear();
    }
    cachesSignature = signature;
    indexAllStages();
  }

  // Walk the scheme stages and seed stageDataByCode + matchCodeToStageCode
  // for every stage. Done eagerly so SSE match-update events can find their
  // stage even for stages we haven't fetched yet.
  function indexAllStages(): void {
    const stages = schemeStages() || [];
    for (const stage of stages) {
      ensureStageData(stage.code);
    }
  }

  function ensureStageData(stageCode: string): StageData {
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
  //
  // A fetch can race live SSE deltas: it may have read the match BEFORE an
  // edit committed, so its `seq` lags what we've already applied in place.
  // Merging by seq (never replace a cached view with an older-or-equal-seq
  // one) keeps a slow/background prefetch from clobbering newer delta state —
  // which would otherwise desync lastSeq and make the next delta look like a
  // gap. Views without a seq (legacy/no broadcasts yet) always apply.
  function applyStageBatch(stageCode: string, batchedMatches: unknown): StageData {
    const data = ensureStageData(stageCode);
    if (Array.isArray(batchedMatches)) {
      for (const m of batchedMatches as Array<MatchView | null | undefined>) {
        if (!m?.code) continue;
        const existing = data.stateByCode.get(m.code);
        if (existing && Number(existing.seq || 0) > Number(m.seq || 0)) continue;
        data.stateByCode.set(m.code, overlayMatch(m));
      }
    }
    const pane = stagePaneByCode.get(stageCode);
    if (pane) onStageDataChanged?.({pane, stageCode, data});
    return data;
  }

  function prefetchStage(stageCode: string): Promise<void> {
    if (!stageCode) return Promise.resolve();
    const inflight = stageFetchPromises.get(stageCode);
    if (inflight) return inflight;
    const url = `${apiBase()}/stages/${encodeURIComponent(stageCode)}/matches`;
    const promise = fetch(url)
      .then(async (response) => {
        if (!response.ok) throw new Error(await response.text());
        return response.json();
      })
      .then((batchedMatches: unknown) => {
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
  function prefetchAllStages(): Promise<void> {
    const url = `${apiBase()}/stages/matches`;
    return fetch(url)
      .then(async (response) => {
        if (!response.ok) throw new Error(await response.text());
        return response.json();
      })
      .then((stages: unknown) => {
        if (!Array.isArray(stages)) return;
        for (const st of stages as Array<{code?: string; matches?: unknown} | null | undefined>) {
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

  function clear(): void {
    for (const [stageCode, pane] of stagePaneByCode) {
      cleanupPane?.({pane, stageCode});
      pane.remove();
    }
    stageDataByCode.clear();
    stagePaneByCode.clear();
    stageFetchPromises.clear();
    matchCodeToStageCode.clear();
    cachesSignature = null;
  }

  // showStage attaches a pane for the given stage to the container and hides
  // the rest. Builds the pane on first request via buildPaneContent. Removes
  // any non-pane children left behind by other renders (renderFest etc).
  function showStage(stageCode: string): HTMLElement | null {
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
      const code = (node as Partial<HTMLElement>).dataset?.stageCode;
      if (code == null || !stagePaneByCode.has(code)) node.remove();
    }
    for (const [code, p] of stagePaneByCode) {
      if (!p.isConnected) container.appendChild(p);
      p.hidden = code !== stageCode;
    }
    onPaneShown?.({pane, stageCode, isFirstBuild});
    return pane;
  }

  // applyMatchUpdate folds a single MatchView into the cache and re-renders
  // its frame. Monotonic by seq (same rule as applyStageBatch): never regress
  // to an older-seq view. Several host edits can be in flight at once, and an
  // optimistic POST response carries its OWN seq + snapshot — which can land
  // AFTER the ordered SSE delta stream has already advanced the cached view
  // past it. Re-applying that older snapshot would both flash stale scores and
  // desync the seq, making the next delta look like a gap (→ refetch → stage
  // skeleton flash). Seqless views (0) always apply (legacy / pre-broadcast).
  function applyMatchUpdate(updated: MatchView | null | undefined): MatchUpdateResult {
    if (!updated?.code) return {found: false};
    const stageCode = matchCodeToStageCode.get(updated.code);
    if (!stageCode) return {found: false};
    const data = ensureStageData(stageCode);
    const existing = data.stateByCode.get(updated.code);
    if (existing && Number(existing.seq || 0) > Number(updated.seq || 0)) {
      return {found: true, stageCode, pane: stagePaneByCode.get(stageCode), stale: true};
    }
    updated = overlayMatch(updated);
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
  function matchState(code: string): MatchView | null {
    const stageCode = matchCodeToStageCode.get(code);
    if (!stageCode) return null;
    return stageDataByCode.get(stageCode)?.stateByCode.get(code) || null;
  }

  // invalidateMatch forces a match's stage to refetch on next access, used
  // when a delta can't be safely applied (no base or a seq gap). It KEEPS the
  // last-good cached view so the frame keeps showing real (if briefly stale)
  // data until the refetch lands — dropping it would repaint the title-only
  // placeholder ("skeleton") on every gap. The seq-aware merge in
  // applyStageBatch then adopts the fresh view.
  function invalidateMatch(code: string): void {
    const stageCode = matchCodeToStageCode.get(code);
    if (!stageCode) return;
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

export const DopeStageCache = {create: createStageCache};
