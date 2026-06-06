const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerLink = document.querySelector(".viewer-link");
const ekTabsRoot = document.getElementById("ekTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const {formatVenue, formatBattleVenue, statusLabel, formatNumber, formatPlace, clamp, cssEscape, th, td} = gameTable;
const viewerCounter = gameTable.createViewerCounter(statusNode);
let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let fest = null;
let venues = [];
let stageSelection = null;            // points at the active pane's selection helper
const stageCache = window.DopeStageCache.create({
  container: hostRoot,
  apiBase: () => route.apiBase,
  schemeStages: () => (fest ? ekSchemeStages() : []),
  findStage: (code) => findStage(fest, code),
  stageType: (stage) => stageType(stage),
  getMatches: (stage) => stage?.matches || [],
  // Re-overlay un-acked local edits onto every MatchView the cache stores, so a
  // background refetch (prefetchStage/prefetchAllStages) or an SSE update can
  // never wipe an optimistically-marked cell before the server confirms it.
  overlayMatch: (view) => overlayPendingMatch(view?.code, view),
  buildPaneContent: ({pane, stageCode, stage, data}) => {
    if (stageType(stage) === "reseed") {
      pane.appendChild(buildHostReseedStagePanel(mergedStage(fest, stageCode)));
      return;
    }
    pane.appendChild(buildStageTableStack(data));
    setupStageTableObserver(pane);
    pane._stageSelection = attachStageSelection(pane.querySelector(".stage-table-stack"));
  },
  onStageDataChanged: ({pane, stageCode, data}) => {
    refreshPaneFrames(pane, data);
  },
  onMatchUpdated: ({frame, matchState}) => {
    if (frame.dataset.rendered === "1" || frame.dataset.nearViewport === "1") {
      updateStageFrame(frame, matchState);
    }
  },
  onPaneShown: ({pane, stageCode}) => {
    stageSelection = pane._stageSelection || null;
    bindStageOverflowScroll();
    scheduleEKTeamNameOverflowUpdate(pane);
  },
  cleanupPane: ({pane}) => {
    pane._stageObserver?.disconnect();
    pane._stageObserver = null;
    pane._stageSelection?.unbind();
    pane._stageSelection = null;
  },
});
let renderMatchCode = null;
let activeCell = {matchCode: "", team: 0, shootout: false, theme: 0, answer: 0};
let reloadTimer = null;
const localMatchEchoes = new Set();
let matchTableIndex = null;
let activeAnswerNode = null;
let recorder = null;
let activeTeamRows = [];
const matchSelections = new Map();
const undoStack = [];
const UNDO_LIMIT = 200;
let undoStackContext = null;
let undoApplying = false;
let presence = null;
let seedImport = null;
let seedImportNotice = "";
let gridNameOverflowFrame = 0;
let ekTeamNameOverflowFrame = 0;
let resultsTeamNameOverflowFrame = 0;
let stageOverflowScrollFrame = null;
let stageOverflowScrollListener = null;
let statsScrollFadeFrame = null;
let playerSelectMeasureContext = null;
let ekTabsFadeFrame = 0;

const floatingPopoverSpecs = [
  {
    trigger: ".od-detailed-team-cell-truncated",
    popover: ".od-detailed-team-name-popover",
    anchor: ".od-detailed-team-name-wrap",
  },
  {
    trigger: ".ek-team-cell.od-detailed-team-cell-truncated",
    popover: ".od-detailed-team-name-popover",
    anchor: ".od-detailed-team-name-wrap",
  },
  {
    trigger: ".grid-slot-team-truncated",
    popover: ".grid-slot-team-popover",
    anchor: ".grid-slot-team-name",
  },
  {
    trigger: ".results-team-truncated",
    popover: ".results-team-name-popover",
    anchor: ".results-team-name",
  },
  {
    trigger: ".player-select-truncated",
    popover: ".player-select-popover",
    anchor: ".player-select",
  },
];

document.body.classList.toggle("embedded-match", embedded);
document.addEventListener("keydown", handleGlobalKeydown);
const floatingPopover = gameTable.createFloatingPopover({root: hostRoot, specs: floatingPopoverSpecs});
floatingPopover.bind();
window.addEventListener("resize", () => {
  if (route.mode === "grid") scheduleGridNameOverflowUpdate();
  if (route.mode === "match" || route.mode === "stage") scheduleEKTeamNameOverflowUpdate();
  if (route.mode === "seedImport" || route.mode === "stats") scheduleResultsTeamNameOverflowUpdate();
  floatingPopover.position();
  scheduleEKTabsFadeUpdate();
});

async function loadCurrent() {
  if (consumeHostInit()) {
    recoverMatchPendingEdits();
    return;
  }
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else if (route.mode === "stats") {
    await loadStats();
  } else if (route.mode === "seedImport") {
    await loadSeedImportPage();
  } else {
    await loadFest();
  }
  recoverMatchPendingEdits();
}

// localStorage SWR cache for FestView. We render the previous fest immediately
// on every navigation, then revalidate against the server in the background.
// Skips the cache silently when localStorage is unavailable (private mode,
// quota, disabled cookies).
function festCacheKey() {
  return `host:fest:${route.festID}:${route.gameID}`;
}

function readFestCache() {
  try {
    const raw = localStorage.getItem(festCacheKey());
    return raw ? JSON.parse(raw) : null;
  } catch (_err) {
    return null;
  }
}

function writeFestCache(view) {
  if (!view) return;
  try {
    localStorage.setItem(festCacheKey(), JSON.stringify(view));
  } catch (_err) {
    // ignore (quota / disabled)
  }
}

function adoptFestView(view) {
  fest = view;
  if (Array.isArray(view?.venues)) venues = view.venues;
  stageCache.adoptFest(view);
}

// hydrateFestFromCache returns true if it managed to populate `fest` from
// either memory or localStorage (without hitting the network).
function hydrateFestFromCache() {
  if (fest) return true;
  const cached = readFestCache();
  if (!cached) return false;
  adoptFestView(cached);
  return true;
}

// consumeHostInit renders the first frame from the server-inlined
// window.__HOST_INIT__ payload, skipping the API round trips that loadX would
// otherwise make. Returns true on success; on any shape mismatch, falls back
// to the normal fetch path.
function consumeHostInit() {
  const init = window.__HOST_INIT__;
  if (!init || !init.route || !init.fest) return false;
  if (init.route.mode !== route.mode) return false;
  // Don't compare festID/gameID: the server resolves slugs to numeric ids, so
  // a slug URL like "/host/fest/test/game/ek" produces an inlined int64 that
  // never string-matches the slug. The server only inlines data for the page
  // it just served — trust the route mode + resource codes.
  if (route.mode === "match" && init.route.matchCode !== route.matchCode) return false;
  if (route.mode === "stage" && init.route.stageCode !== route.stageCode) return false;
  window.__HOST_INIT__ = null;

  adoptFestView(init.fest);
  writeFestCache(init.fest);

  if (route.mode === "match") {
    if (!init.match) return false;
    state = init.match;
    render();
    return true;
  }
  if (route.mode === "stage") {
    // Server inlines fest data but not per-match state. Adopt the fest from
    // __HOST_INIT__ (already done above) and fall through so loadStage runs
    // its batched matches fetch. Otherwise placeholders never get replaced.
    return false;
  }
  if (route.mode === "venues") {
    renderVenues();
    return true;
  }
  if (route.mode === "seedImport") {
    if (!init.seedImport) return false;
    seedImport = init.seedImport;
    renderSeedImport();
    return true;
  }
  renderFest();
  return true;
}

async function loadFest() {
  const cached = hydrateFestFromCache();
  if (cached) renderFest();
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  const fresh = await response.json();
  const changed = !cached || fresh.revision !== fest?.revision;
  adoptFestView(fresh);
  writeFestCache(fresh);
  if (changed || !cached) renderFest();
}

async function loadStage() {
  const cached = hydrateFestFromCache();
  if (cached) renderStage();
  const stageCode = route.stageCode;
  // Revalidate fest+venues and fetch this stage's matches in parallel.
  // adoptFestView routes through the cache, which clears caches on revision bump.
  const festPromise = Promise.all([
    fetch(route.apiBase),
    fetch(`${route.festApi}/venues`),
  ]).then(async ([response, venuesResponse]) => {
    if (!response.ok) throw new Error(await response.text());
    if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
    const fresh = await response.json();
    const freshVenues = await venuesResponse.json();
    venues = freshVenues;
    adoptFestView(fresh);
    writeFestCache(fresh);
  });
  const stagePromise = stageCache.prefetchStage(stageCode);
  await Promise.all([festPromise, stagePromise]);
  if (route.mode !== "stage" || route.stageCode !== stageCode) return;
  renderStage();
  // Background prefetch of every other stage. Each payload is small and makes
  // subsequent tab switches instant (data + pane already cached).
  stageCache.prefetchAllStages();
}

async function loadMatch() {
  // Match state changes per cell edit, so we don't cache it — the match table
  // still waits on its fetch.
  hydrateFestFromCache();
  const [matchResponse, venuesResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode)}`),
    fetch(`${route.festApi}/venues`),
    fetch(route.apiBase),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  state = overlayPendingMatch(route.matchCode, await matchResponse.json());
  venues = await venuesResponse.json();
  adoptFestView(await festResponse.json());
  writeFestCache(fest);
  render();
}

async function loadStats() {
  // Stats are an aggregate of every battle, computed from the shared stage
  // cache — the same per-match MatchViews the bracket pages hold. We warm it
  // once here (a single /stages/matches request, deduped with the bracket
  // prefetch); after that, SSE deltas keep the cache live and renderStats reads
  // straight from memory — no per-edit refetch.
  hydrateFestFromCache();
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  adoptFestView(await response.json());
  writeFestCache(fest);
  await stageCache.prefetchAllStages();
  if (route.mode !== "stats") return;
  renderStats();
}

// statsStagesFromCache shapes the live stage cache into the
// [{code, matches:[MatchView]}] form computeEKPlayerStats expects, pulling the
// current in-memory MatchView for every match that has one.
function statsStagesFromCache() {
  const stages = [];
  for (const stage of ekSchemeStages()) {
    const data = stageCache.getData(stage.code);
    if (!data) continue;
    const matches = [];
    for (const match of data.matches || []) {
      const ms = data.stateByCode.get(match.code);
      if (ms) matches.push(ms);
    }
    stages.push({code: stage.code, matches});
  }
  return stages;
}

async function loadVenuesPage() {
  const cached = hydrateFestFromCache();
  if (cached) renderVenues();
  const [venuesResponse, festResponse] = await Promise.all([
    fetch(`${route.festApi}/venues`),
    fetch(route.apiBase),
  ]);
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  const freshVenues = await venuesResponse.json();
  const freshFest = await festResponse.json();
  const changed = !cached || JSON.stringify(freshVenues) !== JSON.stringify(venues);
  venues = freshVenues;
  adoptFestView(freshFest);
  writeFestCache(fest);
  if (changed) renderVenues();
}

async function loadSeedImportPage() {
  // Seed-import payload is small and not cached separately.
  hydrateFestFromCache();
  const [seedResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/seed-import`),
    fetch(route.apiBase),
  ]);
  if (!seedResponse.ok) throw new Error(await seedResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  seedImport = await seedResponse.json();
  adoptFestView(await festResponse.json());
  writeFestCache(fest);
  renderSeedImport();
}

function connectEvents() {
  const events = new EventSource(`/events?fest_id=${encodeURIComponent(route.festID)}`);
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    // On the stats page, fold match edits into the cache in place and recompute
    // from memory — no refetch. Other scopes don't affect the aggregate.
    if (route.mode === "stats") {
      if (message.scope?.startsWith("match:")) applyStatsMatchEvent(message);
      return;
    }
    if (consumeLocalMatchEcho(message)) {
      setStatus("saved");
      return;
    }
    const matchScope = `match:${route.gameID}:${route.matchCode}`;
    const venuesScope = `venues:${route.festID}`;
    // Match-scoped events: always route into cached stage data, regardless of
    // which page we're on. Keeps cached panes for other stages live so a later
    // tab switch sees fresh data without a fetch. Events arrive either as a
    // scoped delta (ops) — the common case since EK broadcasts deltas — or as a
    // full-state snapshot.
    if (message.scope?.startsWith("match:")) {
      if (Array.isArray(message.ops)) {
        applyMatchDelta(message, matchScope);
        return;
      }
      if (message.data?.code) {
        const view = message.data;
        view.seq = Number(message.seq) || 0;
        const result = stageCache.applyMatchUpdate(view);
        if (route.mode === "match" && message.scope === matchScope) {
          applyUpdatedMatch(view, route.matchCode);
        }
        if (!result.found && route.mode === "stage") scheduleReload();
        setStatus("saved");
        return;
      }
      scheduleReload();
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setStatus("saved");
      return;
    }
    if (message.scope?.startsWith("fest:") && message.data?.stages) {
      applyFestViewEvent(message.data);
      setStatus("saved");
      return;
    }
    scheduleReload();
  });
  events.addEventListener("viewers", (event) => {
    try {
      viewerCounter.setCount(JSON.parse(event.data)?.count);
    } catch (_err) {
      // ignore malformed viewer-count payloads
    }
  });
  events.addEventListener("open", () => recorder?.event("sse-open", {mode: route.mode, matchCode: route.matchCode}));
  events.onerror = () => {
    setStatus("reconnecting");
    recorder?.event("sse-error", {mode: route.mode, matchCode: route.matchCode});
  };
}

function applyFestViewEvent(view) {
  adoptFestView(view);
  writeFestCache(fest);
  if (route.mode === "stage") {
    renderStage();
  } else if (route.mode === "venues") {
    renderVenues();
  } else if (route.mode === "seedImport") {
    renderSeedImport();
  } else if (route.mode !== "match" && route.mode !== "stats") {
    renderFest();
  }
}

// matchCodeFromScope extracts the match code from a "match:<gameID>:<code>"
// scope (codes never contain ':', but join the tail defensively).
function matchCodeFromScope(scope) {
  return scope.split(":").slice(2).join(":");
}

// matchBase returns the cached full view a delta should apply onto: the focused
// match's `state` when we're on it, else the stage cache. null means we have no
// base (e.g. a match in a stage we haven't fetched yet).
function matchBase(code) {
  if (route.mode === "match" && state?.code === code) return state;
  return stageCache.matchState(code);
}

// matchVisible reports whether a match is currently on screen — the focused
// match in match mode, or any match of the open stage in stage mode. A delta we
// can't apply (no base / seq gap) only needs a reload when it would change what
// the user is looking at; otherwise evicting the stale cache entry is enough.
function matchVisible(code) {
  if (route.mode === "match") return code === route.matchCode;
  if (route.mode === "stage") return stageCache.stageCodeForMatch(code) === route.stageCode;
  return false;
}

// applyMatchDelta reconstructs the full match view from a scoped delta by
// applying its ops to the cached base, but only when the delta chains
// (prevSeq === the base's seq). A missing base or a seq gap can't be applied
// safely, so we evict the cache entry (forcing a fresh fetch) and reload only
// when the affected match is on screen — never repainting the placeholder
// skeleton for an off-screen stage. This is the host-side mirror of viewer.js's
// handleMatchEvent: without it, delta events (the default for EK) fall through
// to a full reload, flashing the stage skeleton on every edit.
function applyMatchDelta(message, matchScope) {
  const code = matchCodeFromScope(message.scope);
  const base = matchBase(code);
  const prev = Number(message.prevSeq) || 0;
  // Already applied: a coalesced viewer delta whose range this view was fetched
  // past arrives with seq <= base.seq. Ignore it rather than treat the older
  // prevSeq as a gap (which would force a needless reload).
  if (base && (Number(message.seq) || 0) <= (Number(base.seq) || 0)) {
    setStatus("saved");
    return;
  }
  if (!base || (Number(base.seq) || 0) !== prev) {
    stageCache.invalidateMatch(code);
    if (matchVisible(code)) scheduleReload();
    setStatus("saved");
    return;
  }
  const next = gameTable.applyDeltaOps(base, message.ops);
  next.seq = Number(message.seq) || prev;
  stageCache.applyMatchUpdate(next);
  if (route.mode === "match" && message.scope === matchScope) {
    applyUpdatedMatch(next, route.matchCode);
  }
  setStatus("saved");
}

// SPA navigation for the EK tab strip: intercept same-origin clicks within
// #ekTabs, update history, re-parse the route, and re-run loadCurrent. Keeps
// the EventSource and presence connections alive across tab switches, so the
// only work on switch is the data fetch and DOM rebuild for the new view.
function bindSPANavigation() {
  if (embedded) return;
  ekTabsRoot?.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    if (event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = event.target?.closest?.("a[href]");
    if (!link || !ekTabsRoot.contains(link)) return;
    if (link.target && link.target !== "" && link.target !== "_self") return;
    const href = link.getAttribute("href");
    if (!href || href.startsWith("#")) return;
    let url;
    try {
      url = new URL(href, window.location.origin);
    } catch (_err) {
      return;
    }
    if (url.origin !== window.location.origin) return;
    if (url.pathname === window.location.pathname && url.search === window.location.search) {
      event.preventDefault();
      return;
    }
    event.preventDefault();
    navigateTo(url.pathname + url.search);
  });
  window.addEventListener("popstate", () => {
    runCurrentRoute();
  });
}

function navigateTo(target) {
  history.pushState(null, "", target);
  runCurrentRoute();
}

function runCurrentRoute() {
  route = currentRoute();
  setStatus("saving");
  loadCurrent()
    .then(() => setStatus("saved"))
    .catch((error) => {
      setStatus("error");
      console.error(error);
    });
}

function scheduleReload() {
  window.clearTimeout(reloadTimer);
  reloadTimer = window.setTimeout(() => {
    loadCurrent()
      .then(() => setStatus("saved"))
      .catch((error) => {
        setStatus("error");
        console.error(error);
      });
  }, 120);
}

// applyStatsMatchEvent keeps the stats page live the same way the bracket does:
// it folds a match-scoped SSE event into the shared stage cache in place (a
// chained delta, or a full snapshot) and recomputes the table from memory — no
// refetch. A delta that can't chain (missing base / seq gap) means we dropped an
// event, so we resync the bracket once, mirroring the bracket's own gap path.
function applyStatsMatchEvent(message) {
  const code = matchCodeFromScope(message.scope);
  if (Array.isArray(message.ops)) {
    const base = stageCache.matchState(code);
    const prev = Number(message.prevSeq) || 0;
    if (base && (Number(message.seq) || 0) <= (Number(base.seq) || 0)) return; // already applied
    if (!base || (Number(base.seq) || 0) !== prev) {
      scheduleStatsResync();
      return;
    }
    const next = gameTable.applyDeltaOps(base, message.ops);
    next.seq = Number(message.seq) || prev;
    stageCache.applyMatchUpdate(next);
  } else if (message.data?.code) {
    const view = message.data;
    view.seq = Number(message.seq) || 0;
    stageCache.applyMatchUpdate(view);
  } else {
    scheduleStatsResync();
    return;
  }
  scheduleStatsRerender();
}

// scheduleStatsRerender throttles the in-memory recompute to once per ~400ms
// (leading + trailing) so a burst of cell deltas rebuilds the table a few times
// a second at most, while staying near-live.
let statsRerenderTimer = null;
let statsRerenderPending = false;
function scheduleStatsRerender() {
  if (route.mode !== "stats") return;
  if (statsRerenderTimer) {
    statsRerenderPending = true;
    return;
  }
  rerenderStatsTable();
  statsRerenderTimer = window.setTimeout(function tick() {
    if (statsRerenderPending && route.mode === "stats") {
      statsRerenderPending = false;
      rerenderStatsTable();
      statsRerenderTimer = window.setTimeout(tick, 400);
    } else {
      statsRerenderTimer = null;
    }
  }, 400);
}

// scheduleStatsResync refetches the bracket once after a dropped SSE event (a
// seq gap), then recomputes. Debounced so a fleet that all gap together doesn't
// stampede the bulk endpoint.
let statsResyncTimer = null;
function scheduleStatsResync() {
  if (statsResyncTimer) return;
  statsResyncTimer = window.setTimeout(() => {
    statsResyncTimer = null;
    stageCache.prefetchAllStages()
      .then(() => {
        if (route.mode === "stats") rerenderStatsTable();
      })
      .catch((error) => console.error(error));
  }, 400);
}

function parseEventData(raw) {
  return gameTable.parseScopedEvent(raw);
}

function matchScopeFor(matchCode) {
  return `match:${route.gameID}:${matchCode}`;
}

function matchEchoKey(scope, revision) {
  return `${scope}:${revision || 0}`;
}

function rememberLocalMatchEcho(matchCode, updated) {
  if (!updated?.revision) return;
  localMatchEchoes.add(matchEchoKey(matchScopeFor(matchCode), updated.revision));
}

function consumeLocalMatchEcho(message) {
  if (!message?.scope?.startsWith("match:")) return false;
  const key = matchEchoKey(message.scope, message.revision);
  if (!localMatchEchoes.has(key)) return false;
  localMatchEchoes.delete(key);
  return true;
}

function setStatus(state) {
  const labels = {
    saved: "Синхронизировано",
    saving: "Синхронизация",
    reconnecting: "Переподключение",
    error: "Ошибка синхронизации",
  };
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state] || labels.saving);
  statusNode.title = labels[state] || labels.saving;
}

async function sendUpdateRaw(payload, matchCode) {
  const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(matchCode)}/update`, {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload),
  });
  if (!response.ok) throw new Error(await response.text());
  const updated = await response.json();
  rememberLocalMatchEcho(matchCode, updated);
  return updated;
}

async function sendUpdate(payload, matchCode = currentMatchCode()) {
  setStatus("saving");
  try {
    const updated = await sendUpdateRaw(payload, matchCode);
    applyUpdatedMatch(updated, matchCode);
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

// setMatchFinished toggles a match's finished flag. Unlike a plain sendUpdate it
// (a) applies the new value optimistically so the tick reflects intent at once,
// and (b) records it in ekPendingFinished so overlayPendingMatch re-asserts it on
// every MatchView that lands while the write is in flight — without this, a slow
// finish-write plus an incoming broadcast reverts the checkbox and the operator
// re-clicks, producing the finished/active flapping seen under load. The pending
// intent is cleared only when its OWN write settles and no newer toggle has since
// superseded it (token check), so rapid re-toggles converge to the last intent.
async function setMatchFinished(matchCode, value) {
  const scope = matchScopeFor(matchCode);
  const token = ++ekFinishedToken;
  recorder?.event("ek-finished", {matchCode, value, token});
  ekPendingFinished.set(scope, {value, token});
  if (state && state.code === matchCode) {
    state.finished = value;
    render();
  }
  setStatus("saving");
  try {
    const updated = await sendUpdateRaw({finished: value}, matchCode);
    applyUpdatedMatch(updated, matchCode);
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  } finally {
    const pending = ekPendingFinished.get(scope);
    if (pending && pending.token === token) ekPendingFinished.delete(scope);
  }
}

// ---- EK optimistic edit durability + batching --------------------------------
// EK edits are applied to the DOM + match state instantly (see ekApplyValues),
// then queued here as scoped set-ops per match. Two guarantees, shared with how
// OD/KSI behave via createStateSync:
//   - durability: un-acked ops are re-overlaid on top of any MatchView we render
//     (POST response, SSE delta, or full refetch) via overlayPendingMatch, so an
//     optimistically-marked cell never regresses before the server confirms it —
//     even when a slow server makes the write take seconds.
//   - batching: rapid edits coalesce into ONE atomic /update POST per match per
//     debounce window (one DB write, one broadcast) instead of one per cell.
// Ops to the same cell coalesce (last write wins). Structural edits (finished /
// add/removeShootoutTheme) aren't cell edits — they go straight to sendUpdate.
const EK_EDIT_DEBOUNCE_MS = 150;
const ekPending = new Map(); // matchScope -> {ops, inFlight, timer}

// ekPendingFinished tracks an un-acked finish/unfinish per match so it survives
// incoming MatchViews until the server confirms it. The finished flag is sent
// immediately (it's not a queued cell op) and was otherwise NOT overlay-tracked,
// so under load a slow finish-write plus a co-incident broadcast would visually
// revert the tick — which is what made operators re-click it repeatedly. Tokened
// so an older write completing late never clears a newer toggle's intent.
const ekPendingFinished = new Map(); // matchScope -> {value: boolean, token: number}
let ekFinishedToken = 0;

function ekEntry(matchCode) {
  const scope = matchScopeFor(matchCode);
  let entry = ekPending.get(scope);
  if (!entry) {
    // Persist per-match so a mid-sync refresh recovers un-acked EK edits
    // (recoverMatchPendingEdits re-overlays + re-sends them on the next load).
    entry = {ops: gameTable.createPendingOps({storageKey: `dope.pending:${scope}`}), inFlight: false, timer: null};
    ekPending.set(scope, entry);
  }
  return entry;
}

// overlayPendingMatch re-applies a match's un-acked local edits on top of a
// MatchView. Used everywhere a MatchView enters the render pipeline.
function overlayPendingMatch(matchCode, view) {
  if (!view || !matchCode) return view;
  const scope = matchScopeFor(matchCode);
  const entry = ekPending.get(scope);
  const pendingFinished = ekPendingFinished.get(scope);
  const hasCellOps = entry && entry.ops.size() > 0;
  if (!hasCellOps && !pendingFinished) return view;
  const out = hasCellOps ? entry.ops.overlay(view) : {...view};
  if (pendingFinished) out.finished = pendingFinished.value;
  return out;
}

// refreshMatchPendingMarkers toggles the per-cell pending spinner on the focused
// match's answer cells from that match's un-acked edits — the EK analogue of
// si.js/od.js refreshPendingMarkers. Called after a match renders and after an
// edit/ack so a cell stays marked until the server confirms it.
function refreshMatchPendingMarkers(matchCode) {
  const entry = matchCode ? ekPending.get(matchScopeFor(matchCode)) : null;
  hostRoot.querySelectorAll(".answer-cell").forEach((cell) => {
    let pending = false;
    if (entry) {
      const team = Number(cell.dataset.team);
      const theme = Number(cell.dataset.theme);
      const answer = Number(cell.dataset.answer);
      if (Number.isInteger(team) && Number.isInteger(theme) && Number.isInteger(answer)) {
        const themeKey = cell.dataset.shootout === "1" ? "shootoutThemes" : "themes";
        pending = entry.ops.has(["teams", team, themeKey, theme, "answers", answer]);
      }
    }
    cell.classList.toggle("pending", pending);
  });
}

// recoverMatchPendingEdits, after a (re)load of the focused match, rehydrates any
// un-acked edits persisted by a previous page load (ekEntry reads them from
// localStorage), re-renders the match with them overlaid — showing their pending
// spinner — and re-sends them. No-op when there is nothing to recover.
function recoverMatchPendingEdits() {
  if (route.mode !== "match" || !state || !state.code) return;
  const entry = ekEntry(state.code); // creates + rehydrates persisted ops
  if (entry.ops.size() === 0) return;
  recorder?.event("recovered-pending", {scope: matchScopeFor(state.code), count: entry.ops.size()});
  applyUpdatedMatch(state, state.code); // overlays pending → re-renders with them
  setStatus("saving");
  scheduleEKFlush(state.code, 0);
}

// payloadToOpPath maps an /update cell payload to its MatchView path (matching
// the server's matchDeltaOps shape). Returns null for non-cell (structural)
// payloads, which must not be overlay-tracked.
function payloadToOpPath(payload) {
  if (payload.place !== undefined) return ["teams", payload.team, "place"];
  const themesKey = payload.shootout ? "shootoutThemes" : "themes";
  if (payload.player !== undefined) return ["teams", payload.team, themesKey, payload.theme, "player"];
  if (payload.mark !== undefined) return ["teams", payload.team, themesKey, payload.theme, "answers", payload.answer];
  return null;
}

function payloadToOpValue(payload) {
  if (payload.place !== undefined) return payload.place;
  if (payload.player !== undefined) return payload.player;
  return payload.mark;
}

// opToPayload is the inverse: rebuild the /update payload from a queued op so a
// coalesced batch can be POSTed as {edits: [...]}.
function opToPayload(op) {
  const [, team, key, theme, leaf, answer] = op.path;
  if (op.path.length === 3) return {team, place: op.value}; // ["teams", team, "place"]
  const shootout = key === "shootoutThemes";
  if (leaf === "player") {
    const payload = {team, theme, player: op.value};
    if (shootout) payload.shootout = true;
    return payload;
  }
  const payload = {team, theme, answer, mark: op.value};
  if (shootout) payload.shootout = true;
  return payload;
}

// queueEKEdits records cell edits as pending ops and schedules a batched flush.
// Non-cell (structural) payloads can't be expressed as a tracked op, so they are
// sent immediately to preserve their ordering relative to nothing else.
function queueEKEdits(matchCode, payloads) {
  const entry = ekEntry(matchCode);
  let queued = false;
  for (const payload of payloads) {
    const path = payloadToOpPath(payload);
    if (!path) { sendUpdate(payload, matchCode); continue; }
    entry.ops.add(path, payloadToOpValue(payload));
    queued = true;
  }
  if (queued) {
    setStatus("saving");
    refreshMatchPendingMarkers(matchCode);
    scheduleEKFlush(matchCode, EK_EDIT_DEBOUNCE_MS);
  }
}

function scheduleEKFlush(matchCode, delay) {
  const entry = ekEntry(matchCode);
  window.clearTimeout(entry.timer);
  entry.timer = window.setTimeout(() => {
    entry.timer = null;
    void flushEKEdits(matchCode);
  }, delay);
}

async function flushEKEdits(matchCode) {
  const entry = ekEntry(matchCode);
  if (entry.inFlight || entry.ops.queued() === 0) return;
  const ops = entry.ops.take();
  const payloads = ops.map(opToPayload);
  entry.inFlight = true;
  let saved = false;
  try {
    const body = payloads.length === 1 ? payloads[0] : {edits: payloads};
    const updated = await sendUpdateRaw(body, matchCode);
    entry.ops.ack(ops);
    applyUpdatedMatch(updated, matchCode);
    saved = true;
  } catch (error) {
    // Re-queue for retry (set-ops are idempotent, so re-sending is safe). The
    // overlay keeps the optimistic value on screen meanwhile.
    entry.ops.ack(ops);
    entry.ops.requeue(ops);
    console.error(error);
    setStatus("error");
  } finally {
    entry.inFlight = false;
    if (entry.ops.queued() > 0) {
      if (!entry.timer) scheduleEKFlush(matchCode, saved ? 0 : 2000);
    } else if (saved) {
      setStatus("saved");
    }
  }
}

async function sendVenueChange(number, matchCode = currentMatchCode()) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(matchCode)}/venue`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({number}),
    });
    if (!response.ok) throw new Error(await response.text());
    const updated = await response.json();
    rememberLocalMatchEcho(matchCode, updated);
    applyUpdatedMatch(updated, matchCode);
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

async function updateVenueTitle(number, title) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.festApi}/venues/${encodeURIComponent(number)}`, {
      method: "PUT",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({title}),
    });
    if (!response.ok) throw new Error(await response.text());
    venues = await response.json();
    renderVenues();
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

async function calculateReseed(stageCode) {
  if (!stageCode) return;
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/stages/${encodeURIComponent(stageCode)}/reseed`, {
      method: "POST",
    });
    if (!response.ok) throw new Error((await response.text()).trim() || "Не удалось рассчитать пересев");
    const fresh = await response.json();
    adoptFestView(fresh);
    writeFestCache(fest);
    renderStage();
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

function buildHostReseedStagePanel(stage) {
  return buildReseedStagePanel(stage, {
    editable: true,
    canCalculate: Boolean(stage?.reseedReady),
    onCalculate: () => calculateReseed(stage?.code || route.stageCode),
  });
}

function renderFest() {
  if (!fest) return;
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(route.viewerBase + "/", "Открыть зрительскую сетку");
  document.title = pageTitle();
  renderEKTabs();
  hostRoot.replaceChildren(buildFestGrid(fest, {basePath: route.base}));
  scheduleGridNameOverflowUpdate();
  refreshPresence();
}

function renderStage() {
  if (!fest) return;
  resetMatchTableIndex();
  const stageCode = route.stageCode;
  const stage = mergedStage(fest, stageCode);
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(`${route.viewerBase}/stage/${encodeURIComponent(stageCode)}`, "Открыть этап для зрителя");
  document.title = pageTitle();
  renderEKTabs();
  const pane = stageCache.showStage(stageCode);
  if (stageType(stage) === "reseed") {
    pane?.replaceChildren(buildHostReseedStagePanel(stage));
    scheduleResultsTeamNameOverflowUpdate();
  }
  refreshPresence();
}

function renderVenues() {
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(`${route.viewerBase}/venues`, "Открыть площадки для зрителя");
  document.title = pageTitle("Площадки");
  renderEKTabs();
  hostRoot.replaceChildren(gameTable.buildVenuesTable(venues, {editable: true, onTitleChange: updateVenueTitle}));
  refreshPresence();
}

function renderStats() {
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(`${route.viewerBase}/stats`, "Открыть статистику для зрителя");
  document.title = pageTitle("Статистика");
  renderEKTabs();
  rerenderStatsTable();
  bindStatsScrollFade();
  refreshPresence();
}

// rerenderStatsTable recomputes the table from the live stage cache and swaps it
// in. Cheap (in-memory over the cached MatchViews); no network. Re-runs the
// results-name overflow pass so long player/team names get the fade + popover.
function rerenderStatsTable() {
  const rows = gameTable.computeEKPlayerStats(statsStagesFromCache());
  hostRoot.replaceChildren(gameTable.buildEKStatsTable(rows));
  scheduleResultsTeamNameOverflowUpdate();
}

function renderSeedImport() {
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(route.viewerBase + "/", "Открыть зрительскую сетку");
  document.title = pageTitle("Импорт команд");
  renderEKTabs();
  hostRoot.replaceChildren(buildSeedImportPanel());
  scheduleResultsTeamNameOverflowUpdate();
  refreshPresence();
}

function render() {
  if (!state) return;
  setHostMode("match");
  normalizeActiveCell();
  setHeading("ЭК");
  setViewerLink(`${route.viewerBase}/matches/${encodeURIComponent(state.code || route.matchCode)}`, "Открыть зрительский бой");
  document.title = pageTitle();
  renderEKTabs();

  const focusedPlaceTeam = focusedPlaceTeamIndex();
  const finishToggleFocused = isFinishToggleFocused();
  const table = buildTable();
  matchTableIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
  activeAnswerNode = state.finished ? null : matchTableIndex.get("answer", activeCell);
  markActiveTeamRows(activeAnswerNode);
  attachMatchSelection(table, state, state.code || route.matchCode);
  if (embedded) {
    hostRoot.replaceChildren(table);
    notifyEmbeddedResize();
  } else {
    hostRoot.replaceChildren(table);
  }
  refreshMatchPendingMarkers(state.code || route.matchCode);
  scheduleEKTeamNameOverflowUpdate();
  refreshPresence();
  if (finishToggleFocused) {
    focusFinishToggle({preventScroll: true});
    return;
  }
  if (!state.finished && focusedPlaceTeam !== null) {
    focusPlaceInput(focusedPlaceTeam, {preventScroll: true});
    return;
  }
  if (state.finished) return;
  focusActiveCell({preventScroll: true});
}

function gameSubnavItems() {
  const items = [
    {href: route.base + "/", label: "Сетка", key: "grid"},
    {href: route.base + "/venues", label: "Площадки", key: "venues"},
    {href: route.base + "/seed-import", label: "Импорт команд", key: "seedImport"},
  ];
  ekSchemeStages().forEach((stage) => {
    items.push({
      href: `${route.base}/stage/${encodeURIComponent(stage.code)}`,
      label: gameTable.stageTabLabel(stage),
      key: `stage:${stage.code}`,
    });
  });
  // Статистика sits at the very end, after all stage tabs.
  items.push({href: route.base + "/stats", label: "Статистика", key: "stats"});
  return items;
}

function renderEKTabs() {
  if (!ekTabsRoot || embedded) return;
  ekTabsRoot.replaceChildren();
  const active = activeTabKey();
  for (const item of gameSubnavItems()) {
    const link = document.createElement("a");
    link.className = "match-tab" + (item.key === active ? " active" : "");
    link.href = item.href;
    link.textContent = item.label;
    link.setAttribute("role", "tab");
    link.setAttribute("aria-selected", item.key === active ? "true" : "false");
    ekTabsRoot.appendChild(link);
  }
  bindEKTabsScrollFade();
}

function bindEKTabsScrollFade() {
  if (!ekTabsRoot) return;
  if (ekTabsRoot.dataset.scrollFadeBound !== "1") {
    ekTabsRoot.addEventListener("scroll", scheduleEKTabsFadeUpdate, {passive: true});
    ekTabsRoot.dataset.scrollFadeBound = "1";
  }
  scheduleEKTabsFadeUpdate();
}

function scheduleEKTabsFadeUpdate() {
  if (!ekTabsRoot || embedded) return;
  if (ekTabsFadeFrame) cancelAnimationFrame(ekTabsFadeFrame);
  ekTabsFadeFrame = requestAnimationFrame(() => {
    ekTabsFadeFrame = 0;
    updateEKTabsScrollFade();
  });
}

function updateEKTabsScrollFade() {
  if (!ekTabsRoot) return;
  const hasLeft = ekTabsRoot.scrollLeft > 1;
  const hasRight = ekTabsRoot.scrollLeft + ekTabsRoot.clientWidth < ekTabsRoot.scrollWidth - 1;
  ekTabsRoot.classList.toggle("tabs-scroll-left", hasLeft);
  ekTabsRoot.classList.toggle("tabs-scroll-right", hasRight);
}

function activeTabKey() {
  if (route.mode === "stage") return `stage:${route.stageCode}`;
  if (route.mode === "match") {
    const stageCode = state?.stageCode || stageCodeForMatch(route.matchCode);
    return stageCode ? `stage:${stageCode}` : "grid";
  }
  if (route.mode === "venues") return "venues";
  if (route.mode === "stats") return "stats";
  if (route.mode === "seedImport") return "seedImport";
  return "grid";
}

function stageCodeForMatch(matchCode) {
  if (!matchCode) return "";
  for (const stage of ekSchemeStages()) {
    if ((stage.matches || []).some((match) => match.code === matchCode)) return stage.code;
  }
  return "";
}

function ekSchemeStages() {
  const scheme = parseScheme(fest?.schemaJson);
  return scheme?.stages?.length ? scheme.stages : fest?.stages || [];
}

function scheduleGridNameOverflowUpdate(root = hostRoot) {
  if (gridNameOverflowFrame) cancelAnimationFrame(gridNameOverflowFrame);
  gridNameOverflowFrame = requestAnimationFrame(() => {
    gridNameOverflowFrame = 0;
    updateGridNameOverflow(root);
  });
}

function updateGridNameOverflow(root = hostRoot) {
  const cells = root.querySelectorAll(".grid-slot-team");
  const readings = new Array(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(".grid-slot-team-name");
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle("grid-slot-team-truncated", readings[i]);
  }
}

function scheduleEKTeamNameOverflowUpdate(root = hostRoot) {
  if (ekTeamNameOverflowFrame) cancelAnimationFrame(ekTeamNameOverflowFrame);
  ekTeamNameOverflowFrame = requestAnimationFrame(() => {
    ekTeamNameOverflowFrame = 0;
    updateEKTeamNameOverflow(root);
  });
}

function updateEKTeamNameOverflow(root = hostRoot) {
  updatePlayerSelectOverflow(root);
  const cells = Array.from(root.querySelectorAll(".ek-team-cell"));
  const stageCells = [];
  const stageNames = [];
  const detailedCells = [];
  const detailedReadings = [];
  for (const cell of cells) {
    const name = cell.querySelector(".od-detailed-team-name");
    if (cell.closest(".ek-stage-table")) {
      if (isVisibleInScrollFrame(cell)) {
        stageCells.push(cell);
        stageNames.push(name);
      }
      continue;
    }
    detailedCells.push(cell);
    detailedReadings.push(Boolean(name && name.scrollWidth > name.clientWidth + 1));
  }
  for (let i = 0; i < detailedCells.length; i++) {
    detailedCells[i].classList.toggle("od-detailed-team-cell-truncated", detailedReadings[i]);
  }
  for (let i = 0; i < stageCells.length; i++) {
    fitEKStageTeamName(stageCells[i], stageNames[i]);
  }
}

function updatePlayerSelectOverflow(root = hostRoot) {
  const wraps = root.querySelectorAll(".player-select-wrap");
  const measurements = [];
  for (const wrap of wraps) {
    if (wrap.closest(".ek-stage-table") && !isVisibleInScrollFrame(wrap)) continue;
    const select = wrap.querySelector(".player-select");
    const popover = wrap.querySelector(".player-select-popover");
    const label = selectedPlayerLabel(select);
    measurements.push({wrap, popover, label, truncated: Boolean(label && playerSelectTextOverflows(select, label))});
  }
  for (const m of measurements) {
    if (m.popover) m.popover.textContent = m.label;
    m.wrap.classList.toggle("player-select-truncated", m.truncated);
  }
}

function selectedPlayerLabel(select) {
  if (!select) return "";
  return select.selectedOptions?.[0]?.textContent || select.value || "";
}

function playerSelectTextOverflows(select, label) {
  if (!select || !label) return false;
  const style = getComputedStyle(select);
  const available = select.clientWidth - parseFloat(style.paddingLeft || "0") - parseFloat(style.paddingRight || "0");
  if (available <= 0) return false;
  const context = playerTextMeasureContext();
  context.font = style.font;
  return context.measureText(label).width > available + 1;
}

function playerTextMeasureContext() {
  if (!playerSelectMeasureContext) {
    playerSelectMeasureContext = document.createElement("canvas").getContext("2d");
  }
  return playerSelectMeasureContext;
}

function bindStageOverflowScroll() {
  const scrollFrame = hostRoot.closest(".sheet-frame");
  if (!scrollFrame) return;
  updateStageScrollState(scrollFrame);
  if (stageOverflowScrollFrame === scrollFrame) return;
  unbindStageOverflowScroll();
  stageOverflowScrollListener = () => {
    scheduleEKTeamNameOverflowUpdate(hostRoot);
    updateStageScrollState(scrollFrame);
  };
  scrollFrame.addEventListener("scroll", stageOverflowScrollListener, {passive: true});
  stageOverflowScrollFrame = scrollFrame;
}

// Toggles the scrolled-under fade on the frozen-column boundary (see the
// .stage-scroll-left rule in styles.css), mirroring OD/KSI behaviour.
function updateStageScrollState(frame) {
  if (!frame) return;
  frame.classList.toggle("stage-scroll-left", frame.scrollLeft > 1);
}

function unbindStageOverflowScroll() {
  if (!stageOverflowScrollFrame || !stageOverflowScrollListener) return;
  stageOverflowScrollFrame.removeEventListener("scroll", stageOverflowScrollListener);
  stageOverflowScrollFrame = null;
  stageOverflowScrollListener = null;
}

// Stats page reuses the .stage-scroll-left fade cue for its frozen player
// column. The .sheet-frame is static, so bind the listener once and leave it —
// the toggle is a harmless no-op on routes without an ek-stats-table. (Viewer
// binds the same thing globally via bindStageScrollFade.)
function bindStatsScrollFade() {
  const scrollFrame = hostRoot.closest(".sheet-frame");
  if (!scrollFrame) return;
  updateStageScrollState(scrollFrame);
  if (statsScrollFadeFrame === scrollFrame) return;
  scrollFrame.addEventListener("scroll", () => updateStageScrollState(scrollFrame), {passive: true});
  statsScrollFadeFrame = scrollFrame;
}

function isVisibleInScrollFrame(element) {
  const scrollFrame = element.closest(".sheet-frame");
  if (!scrollFrame) return true;
  const rect = element.getBoundingClientRect();
  const frameRect = scrollFrame.getBoundingClientRect();
  return rect.bottom >= frameRect.top && rect.top <= frameRect.bottom;
}

function fitEKStageTeamName(cell, name) {
  const truncated = gameTable.fitEKStageTeamName(cell, name);
  cell.classList.toggle("od-detailed-team-cell-truncated", truncated);
}

function scheduleResultsTeamNameOverflowUpdate(root = hostRoot) {
  if (resultsTeamNameOverflowFrame) cancelAnimationFrame(resultsTeamNameOverflowFrame);
  resultsTeamNameOverflowFrame = requestAnimationFrame(() => {
    resultsTeamNameOverflowFrame = 0;
    updateResultsTeamNameOverflow(root);
  });
}

function updateResultsTeamNameOverflow(root = hostRoot) {
  const cells = root.querySelectorAll(".results-team");
  const readings = new Array(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(".results-team-name");
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle("results-team-truncated", readings[i]);
  }
}

function buildSeedImportPanel() {
  const panel = document.createElement("section");
  panel.className = "results-wrapper seed-import-panel";

  const actions = document.createElement("div");
  actions.className = "cluster seed-import-actions";
  const importButton = document.createElement("button");
  importButton.type = "button";
  importButton.className = "btn";
  importButton.textContent = "Импортировать из КСИ";
  importButton.addEventListener("click", importSeedsFromKSI);
  actions.appendChild(importButton);
  panel.appendChild(actions);

  if (seedImportNotice) {
    const notice = document.createElement("p");
    notice.className = seedImportNotice.startsWith("Ошибка:") ? "empty" : "muted";
    notice.textContent = seedImportNotice;
    panel.appendChild(notice);
  }

  const rows = seedImport?.rows || [];
  if (rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "Команды ещё не импортированы.";
    panel.appendChild(empty);
    return panel;
  }

  const meta = document.createElement("p");
  meta.className = "muted";
  meta.textContent = `В основном посеве: ${Math.min(seedImport.activeCount || 0, seedImport.drawSize || 0)} из ${seedImport.drawSize || 0}. Всего активных команд: ${seedImport.activeCount || 0}.`;
  panel.appendChild(meta);

  const table = document.createElement("table");
  table.className = "results-table seed-import-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("Посев", "results-place-head seed-number-head"));
  head.appendChild(th("Команда", "results-team-head seed-team-head"));
  head.appendChild(th("Отказалась", "seed-declined-head"));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  let waitlistInserted = false;
  rows.forEach((row, index) => {
    if (row.waitlist && !waitlistInserted) {
      waitlistInserted = true;
      const divider = document.createElement("tr");
      divider.className = "seed-waitlist-row";
      divider.appendChild(td("Лист ожидания", "seed-waitlist-cell", {colSpan: 3}));
      tbody.appendChild(divider);
    }

    const tr = document.createElement("tr");
    const classes = ["results-row"];
    const previousRow = rows[index - 1];
    const nextRow = rows[index + 1];
    if (!previousRow || Boolean(previousRow.waitlist) !== Boolean(row.waitlist)) {
      classes.push("results-group-first");
    }
    if (!nextRow || Boolean(nextRow.waitlist) !== Boolean(row.waitlist)) {
      classes.push("results-group-last");
    }
    if (row.declined) classes.push("seed-declined-row");
    tr.className = classes.join(" ");
    tr.appendChild(td(row.seedNumber || "", "results-place seed-number-cell"));

    const teamCell = document.createElement("td");
    teamCell.className = "results-team seed-team-cell";
    const nameWrap = document.createElement("span");
    nameWrap.className = "results-team-name-wrap";
    const teamLabel = row.name || "";
    const name = document.createElement("span");
    name.className = "results-team-name";
    name.textContent = teamLabel;
    name.tabIndex = 0;
    name.setAttribute("aria-label", teamLabel);
    nameWrap.appendChild(name);
    if (row.city) {
      const city = document.createElement("span");
      city.className = "results-team-city seed-team-city";
      city.textContent = row.city;
      nameWrap.appendChild(city);
    }
    teamCell.appendChild(nameWrap);
    const fullName = document.createElement("span");
    fullName.className = "results-team-name-popover";
    fullName.textContent = teamLabel;
    teamCell.appendChild(fullName);
    tr.appendChild(teamCell);

    const declinedCell = document.createElement("td");
    declinedCell.className = "results-num seed-declined-cell";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = Boolean(row.declined);
    checkbox.setAttribute("aria-label", `Отказалась: ${row.name || "команда"}`);
    checkbox.addEventListener("change", () => {
      setSeedDeclined(row.teamID, checkbox.checked).catch(() => {
        checkbox.checked = !checkbox.checked;
      });
    });
    declinedCell.appendChild(checkbox);
    tr.appendChild(declinedCell);
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  panel.appendChild(table);
  return panel;
}

async function importSeedsFromKSI() {
  setStatus("saving");
  seedImportNotice = "";
  try {
    const response = await fetch(`${route.apiBase}/seed-import/ksi`, {method: "POST"});
    if (!response.ok) throw new Error((await response.text()).trim() || "Не удалось импортировать команды");
    seedImport = await response.json();
    seedImportNotice = `Импортировано команд: ${seedImport.rows?.length || 0}.`;
    renderSeedImport();
    setStatus("saved");
  } catch (error) {
    seedImportNotice = `Ошибка: ${error.message}`;
    renderSeedImport();
    setStatus("error");
    throw error;
  }
}

async function setSeedDeclined(teamID, declined) {
  setStatus("saving");
  seedImportNotice = "";
  try {
    const response = await fetch(`${route.apiBase}/seed-import/decline`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({teamID, declined}),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || "Не удалось сохранить отказ");
    seedImport = await response.json();
    renderSeedImport();
    setStatus("saved");
  } catch (error) {
    seedImportNotice = `Ошибка: ${error.message}`;
    renderSeedImport();
    setStatus("error");
    throw error;
  }
}

function buildStageTableStack(data) {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack stage-table-stack-lazy";
  const matches = data?.matches || [];
  if (matches.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "В этом этапе нет боёв.";
    wrapper.appendChild(empty);
    return wrapper;
  }
  matches.forEach((match) => {
    const frame = document.createElement("section");
    frame.className = "stage-match-frame";
    frame.dataset.matchCode = match.code || "";
    frame.appendChild(buildStageMatchPlaceholder(match));
    wrapper.appendChild(frame);
  });
  return wrapper;
}

function buildStageMatchPlaceholder(match) {
  const placeholder = document.createElement("div");
  placeholder.className = "stage-match-placeholder";
  placeholder.textContent = match.title || `Бой ${match.code}`;
  return placeholder;
}

// setupStageTableObserver installs a per-pane IntersectionObserver that defers
// DOM construction for off-screen match tables. Stored on the pane element so
// cleanupPane (from the cache) can disconnect it on revision invalidation.
function setupStageTableObserver(pane) {
  const frames = Array.from(pane.querySelectorAll(".stage-match-frame"));
  if (frames.length === 0) return;
  if (!("IntersectionObserver" in window)) {
    frames.forEach((frame) => renderStageMatchFrameIfReady(pane, frame, {force: true}));
    return;
  }
  const root = hostRoot.closest(".sheet-frame");
  const observer = new IntersectionObserver((entries) => {
    const visibleFrames = [];
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return;
      const frame = entry.target;
      frame.dataset.nearViewport = "1";
      visibleFrames.push(frame);
    });
    let rendered = false;
    visibleFrames.forEach((frame) => {
      rendered = renderStageMatchFrameIfReady(pane, frame) || rendered;
      if (frame.dataset.rendered === "1") observer.unobserve(frame);
    });
    if (rendered) scheduleEKTeamNameOverflowUpdate(pane);
  }, {root, rootMargin: "900px 0px"});
  frames.forEach((frame) => observer.observe(frame));
  pane._stageObserver = observer;
}

// refreshPaneFrames re-runs the frame paint pass after the cache's stage-data
// fetch lands. Frames already rendered or near-viewport pick up the new state
// immediately; the rest stay as placeholders until the observer fires.
function refreshPaneFrames(pane, data) {
  if (!pane || !data) return;
  const stage = mergedStage(fest, pane.dataset.stageCode);
  if (stageType(stage) === "reseed") {
    pane.replaceChildren(buildHostReseedStagePanel(stage));
    return;
  }
  let rebuilt = false;
  pane.querySelectorAll(".stage-match-frame").forEach((frame) => {
    const matchState = data.stateByCode.get(frame.dataset.matchCode || "");
    if (!matchState) return;
    if (frame.dataset.rendered === "1" || frame.dataset.nearViewport === "1") {
      rebuilt = updateStageFrame(frame, matchState) || rebuilt;
    }
  });
  if (rebuilt) scheduleEKTeamNameOverflowUpdate(pane);
}

function renderStageMatchFrameIfReady(pane, frame, options = {}) {
  const data = stageCache.getData(pane.dataset.stageCode);
  const matchState = data?.stateByCode.get(frame.dataset.matchCode || "");
  if (!matchState) return false;
  return renderStageMatchFrame(frame, matchState, options);
}

function renderStageMatchFrame(frame, matchState, options = {}) {
  if (!frame || (!options.force && frame.dataset.rendered === "1")) return false;
  const hadFocus = document.activeElement?.closest?.(".stage-match-frame") === frame;
  frame.dataset.rendered = "1";
  const stageTable = withMatchState(matchState, () => buildTable({compact: true}));
  frame.replaceChildren(stageTable);
  // Per-frame score index + last state, so a later same-shape update patches
  // this frame's cells in place (updateStageFrame) instead of rebuilding it —
  // the rebuild is what flickered the cell being edited.
  frame.__scoreIndex = gameTable.createScoreTableIndex(stageTable, {entity: "team", shootout: true});
  frame.__matchState = matchState;
  stageSelection?.refresh();
  if (hadFocus && activeCell.matchCode === matchState.code) {
    focusActiveCell({preventScroll: true});
  }
  return true;
}

// updateStageFrame applies a fresh MatchView to an already-built stage frame,
// patching cells in place when the battle shape is unchanged (the common case
// for a score edit) and falling back to a full rebuild only on a shape change.
// Patching preserves the DOM, so the edited cell keeps focus and team names
// don't re-fit — no flicker.
function updateStageFrame(frame, matchState) {
  if (!frame || !matchState) return false;
  if (frame.dataset.rendered === "1" && frame.__scoreIndex && frame.__matchState &&
      canPatchMatchShape(frame.__matchState, matchState)) {
    patchHostScoreTable(frame.__scoreIndex, matchState);
    frame.__matchState = matchState;
    return false;
  }
  return renderStageMatchFrame(frame, matchState, {force: frame.dataset.rendered === "1"});
}

function stageMatchFrame(matchCode) {
  const stageCode = stageCache.stageCodeForMatch(matchCode);
  if (!stageCode) return null;
  const pane = stageCache.getPane(stageCode);
  return pane?.querySelector(`.stage-match-frame[data-match-code="${cssEscape(matchCode)}"]`) || null;
}

function withMatchState(matchState, callback) {
  const previousState = state;
  const previousCode = renderMatchCode;
  state = matchState;
  renderMatchCode = matchState.code || route.matchCode;
  try {
    return callback();
  } finally {
    state = previousState;
    renderMatchCode = previousCode;
  }
}

function currentMatchCode() {
  return renderMatchCode || activeCell.matchCode || route.matchCode;
}

function currentStageMatches() {
  return stageCache.getData(route.stageCode)?.matches || [];
}

function currentStageStateByCode() {
  return stageCache.getData(route.stageCode)?.stateByCode || null;
}

function activeMatchState() {
  if (route.mode === "stage") {
    const byCode = currentStageStateByCode();
    if (!byCode) return null;
    if (activeCell.matchCode && byCode.has(activeCell.matchCode)) return byCode.get(activeCell.matchCode);
    for (const match of currentStageMatches()) {
      const ms = byCode.get(match.code);
      if (ms) return ms;
    }
    return null;
  }
  return state;
}

function matchStateFor(matchCode) {
  if (route.mode === "stage") return currentStageStateByCode()?.get(matchCode) || null;
  return state;
}

function applyUpdatedMatch(updated, matchCode) {
  // Re-apply any still-un-acked local edits on top, so a server view that
  // predates them (out-of-order response, delta from another editor, refetch)
  // never regresses an optimistic cell. No-op once everything is acked.
  updated = overlayPendingMatch(matchCode, updated);
  if (route.mode === "stage") {
    stageCache.applyMatchUpdate(updated);
    return;
  }
  // Drop a stale optimistic response: with several edits in flight, POST
  // responses can land out of order and after the ordered SSE deltas have
  // already advanced `state` past this seq. Re-applying the older snapshot
  // would regress the view and gap the next delta (→ resync → flash). Mirrors
  // the seq-monotonic guard in stageCache.applyMatchUpdate.
  if (state && Number(state.seq || 0) > Number(updated.seq || 0)) return;
  const previous = state;
  state = updated;
  if (matchTableIndex && canPatchMatchShape(previous, updated)) {
    normalizeActiveCell();
    patchHostScoreTable(matchTableIndex, updated);
    markActiveCell();
    refreshMatchPendingMarkers(matchCode);
    return;
  }
  render();
}

// canPatchMatchShape: shared shape check plus the host's structural extras. The
// editable table renders the title/venue in a header, so a change there needs a
// rebuild; place is an editable input, so a place change is patched in place
// (unlike the viewer, which rebuilds on place change).
function canPatchMatchShape(previous, next) {
  if (!previous || !next) return false;
  if (previous.title !== next.title) return false;
  if (formatVenue(previous.venue) !== formatVenue(next.venue)) return false;
  return gameTable.canPatchScoreShape(previous, next);
}

// patchHostScoreTable patches a built editable score table in place from a
// MatchView. All cell syncing — including the editable place inputs and player
// selects, which skip a focused control so a live update never steals the cursor
// — lives in the shared scoreCellSpecs; the host only injects the callback that
// refreshes a synced select's overflow chrome.
function patchHostScoreTable(index, matchState) {
  gameTable.patchScoreTable(index, matchState, {
    formatNumber,
    onPlayerSelectSynced: (select) =>
      updatePlayerSelectOverflow(select?.closest(".player-select-wrap") || hostRoot),
  });
}

function indexedNode(name, values) {
  if (route.mode !== "match") return null;
  return matchTableIndex?.get(name, values) || null;
}

function resetMatchTableIndex() {
  matchTableIndex = null;
  activeAnswerNode = null;
  clearActiveTeamRows();
  for (const helper of matchSelections.values()) helper.unbind();
  matchSelections.clear();
  // Stage selections live on cached panes — cleared by cleanupPane when the
  // cache invalidates a pane, not on every render. unbindStageOverflowScroll
  // only matters when leaving stage mode (renderFest/renderVenues/etc).
  stageSelection = null;
  if (route.mode !== "stage") unbindStageOverflowScroll();
}

function stageRowOffset(matchIndex) {
  const matches = currentStageMatches();
  const byCode = currentStageStateByCode();
  let offset = 0;
  for (let i = 0; i < matchIndex && i < matches.length; i++) {
    const s = byCode?.get(matches[i]?.code);
    offset += s?.teams?.length || 0;
  }
  return offset;
}

function stageCoordOf(cell) {
  const team = Number(cell.dataset.team);
  const theme = Number(cell.dataset.theme);
  const answer = Number(cell.dataset.answer);
  if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) return null;
  const matchCode = cell.dataset.matchCode;
  const matches = currentStageMatches();
  const matchIndex = matches.findIndex((m) => m.code === matchCode);
  if (matchIndex < 0) return null;
  const matchState = currentStageStateByCode()?.get(matchCode);
  if (!matchState) return null;
  const answers = answerCountFor(matchState);
  if (answers <= 0) return null;
  const shootout = cell.dataset.shootout === "1";
  const themeOrder = shootout ? regularThemeCountFor(matchState) + theme : theme;
  return {row: stageRowOffset(matchIndex) + team, col: themeOrder * answers + answer};
}

function stageCellAtCoord(coord) {
  if (!coord) return null;
  let remaining = coord.row;
  const byCode = currentStageStateByCode();
  for (const match of currentStageMatches()) {
    const matchState = byCode?.get(match.code);
    if (!matchState) continue;
    const teamCount = matchState.teams?.length || 0;
    if (remaining < teamCount) {
      const team = remaining;
      const answers = answerCountFor(matchState);
      if (answers <= 0) return null;
      const regular = regularThemeCountFor(matchState);
      const themeOrder = Math.floor(coord.col / answers);
      const answer = coord.col % answers;
      const shootout = themeOrder >= regular;
      const theme = shootout ? themeOrder - regular : themeOrder;
      const frame = stageMatchFrame(match.code);
      return frame?.querySelector(
        `.answer-cell[data-team="${cssEscape(team)}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(theme)}"][data-answer="${cssEscape(answer)}"]`,
      ) || null;
    }
    remaining -= teamCount;
  }
  return null;
}

function stageApplyValues(edits) {
  const byCode = currentStageStateByCode();
  // Group edits by match so each match's cells are applied through a single
  // ekApplyValues call (and thus a single coalesced re-render), rather than one
  // call — and one re-render — per cell.
  const groupsByCode = new Map();
  for (const {cell, value} of edits) {
    const matchCode = cell.dataset.matchCode;
    if (!matchCode) continue;
    const matchState = byCode?.get(matchCode);
    if (!matchState || matchState.finished) continue;
    if (!groupsByCode.has(matchCode)) groupsByCode.set(matchCode, {matchState, items: []});
    groupsByCode.get(matchCode).items.push({cell, value});
  }
  if (groupsByCode.size === 0) return;
  if (!undoApplying) {
    const groups = [];
    for (const [matchCode, {items}] of groupsByCode) {
      const reverse = snapshotMatchEdits(matchCode, items);
      if (reverse.length > 0) groups.push({matchCode, items: reverse});
    }
    if (groups.length > 0) {
      pushUndoEntry({
        kind: "match-edits",
        groups,
        selection: captureSelectionFromHelper(stageSelection),
      });
    }
  }
  for (const [matchCode, {matchState, items}] of groupsByCode) {
    ekApplyValues(matchCode, matchState, items, {recordUndo: false});
  }
}

function attachStageSelection(container) {
  if (!container) return null;
  const helper = gameTable.createCellRangeSelection({
    root: container,
    cellSelector: ".answer-cell",
    readonly: () => false,
    coordOf: stageCoordOf,
    cellAtCoord: stageCellAtCoord,
    serialize: ekSerializeMark,
    parse: parseMarkText,
    applyValues: stageApplyValues,
    onActiveChange: (cell) => {
      if (!cell) return;
      const team = Number(cell.dataset.team);
      const theme = Number(cell.dataset.theme);
      const answer = Number(cell.dataset.answer);
      const shootout = cell.dataset.shootout === "1";
      const matchCode = cell.dataset.matchCode || activeCell.matchCode;
      activeCell = {matchCode, team, shootout, theme, answer};
      markActiveCell();
      publishPresence();
    },
  });
  helper.bind();
  return helper;
}

function answerCountFor(matchState) {
  return matchState?.questionValues?.length || 0;
}

function regularThemeCountFor(matchState) {
  return matchState?.teams?.[0]?.themes?.length || 0;
}

function ekCoordOf(cell, matchState) {
  const team = Number(cell.dataset.team);
  const theme = Number(cell.dataset.theme);
  const answer = Number(cell.dataset.answer);
  if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) return null;
  const shootout = cell.dataset.shootout === "1";
  const themeOrder = shootout ? regularThemeCountFor(matchState) + theme : theme;
  const answers = answerCountFor(matchState);
  if (answers <= 0) return null;
  return {row: team, col: themeOrder * answers + answer};
}

function ekCellAtCoord(table, coord, matchState) {
  if (!coord) return null;
  const answers = answerCountFor(matchState);
  if (answers <= 0) return null;
  const themeOrder = Math.floor(coord.col / answers);
  const answer = coord.col % answers;
  const regular = regularThemeCountFor(matchState);
  const shootout = themeOrder >= regular;
  const theme = shootout ? themeOrder - regular : themeOrder;
  return table.querySelector(
    `.answer-cell[data-team="${cssEscape(coord.row)}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(theme)}"][data-answer="${cssEscape(answer)}"]`,
  );
}

function ekSerializeMark(cell) {
  if (cell.classList.contains("right")) return "+";
  if (cell.classList.contains("wrong")) return "-";
  return "";
}

function parseMarkText(text) {
  const value = String(text || "").trim().toLowerCase();
  if (value === "") return "";
  if (["+", "1", "right", "y", "yes", "✓", "v", "да", "п", "п."].includes(value)) return "right";
  if (["-", "−", "0", "wrong", "n", "no", "x", "✗", "нет", "м", "м."].includes(value)) return "wrong";
  return "";
}

function ekApplyValues(matchCode, matchState, edits, options = {}) {
  if (options.recordUndo !== false && !undoApplying) {
    const reverse = snapshotMatchEdits(matchCode, edits);
    if (reverse.length > 0) {
      const helper = activeMatchSelection();
      pushUndoEntry({
        kind: "match-edits",
        groups: [{matchCode, items: reverse}],
        selection: captureSelectionFromHelper(helper),
      });
    }
  }
  const payloads = [];
  for (const {cell, value} of edits) {
    const mark = value === "right" ? "right" : value === "wrong" ? "wrong" : "";
    gameTable.setMarkClass(cell, mark);
    const team = Number(cell.dataset.team);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    const shootout = cell.dataset.shootout === "1";
    const target = shootout ? shootoutThemesFor(matchState.teams[team])[theme] : matchState.teams[team]?.themes?.[theme];
    if (target?.answers) target.answers[answer] = mark;
    const payload = {team, theme, answer, mark};
    if (shootout) payload.shootout = true;
    payloads.push(payload);
  }
  queueEKEdits(matchCode, payloads);
}

function snapshotMatchEdits(matchCode, edits) {
  const out = [];
  for (const {cell, value} of edits) {
    const team = Number(cell.dataset.team);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) continue;
    const shootout = cell.dataset.shootout === "1";
    const previous = cell.classList.contains("right") ? "right"
      : cell.classList.contains("wrong") ? "wrong" : "";
    const target = value === "right" ? "right" : value === "wrong" ? "wrong" : "";
    if (previous === target) continue;
    out.push({team, theme, answer, shootout, previous});
  }
  return out;
}

function captureSelectionFromHelper(helper) {
  if (!helper) return null;
  const anchor = helper.anchor;
  const focus = helper.focus;
  if (!anchor || !focus) return null;
  return {
    anchor: {row: anchor.row, col: anchor.col},
    focus: {row: focus.row, col: focus.col},
  };
}

function currentUndoContext() {
  if (route.mode === "match") return {mode: "match", matchCode: route.matchCode || null, stageCode: null};
  if (route.mode === "stage") return {mode: "stage", matchCode: null, stageCode: route.stageCode || null};
  return null;
}

function ensureUndoContext() {
  const next = currentUndoContext();
  if (!next) {
    undoStack.length = 0;
    undoStackContext = null;
    return null;
  }
  if (!undoStackContext ||
      undoStackContext.mode !== next.mode ||
      undoStackContext.matchCode !== next.matchCode ||
      undoStackContext.stageCode !== next.stageCode) {
    undoStack.length = 0;
    undoStackContext = next;
  }
  return next;
}

function pushUndoEntry(entry) {
  if (!ensureUndoContext()) return;
  undoStack.push(entry);
  while (undoStack.length > UNDO_LIMIT) undoStack.shift();
}

function performUndo() {
  if (!ensureUndoContext() || undoStack.length === 0) return false;
  const entry = undoStack.pop();
  if (!entry || entry.kind !== "match-edits") return false;
  undoApplying = true;
  try {
    for (const group of entry.groups) {
      const matchCode = group.matchCode;
      const matchState = matchStateFor(matchCode);
      if (!matchState) continue;
      const edits = [];
      for (const item of group.items) {
        const cell = findAnswerCell(matchCode, item);
        if (cell) edits.push({cell, value: item.previous});
      }
      if (edits.length > 0) ekApplyValues(matchCode, matchState, edits, {recordUndo: false});
    }
  } finally {
    undoApplying = false;
  }
  restoreSelectionFromUndoEntry(entry);
  return true;
}

function findAnswerCell(matchCode, {team, theme, answer, shootout}) {
  if (route.mode === "match") {
    const node = indexedNode("answer", {team, theme, answer, shootout: shootout ? "1" : "0"});
    if (node) return node;
  }
  return document.querySelector(
    `.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${cssEscape(team)}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(theme)}"][data-answer="${cssEscape(answer)}"]`,
  );
}

function restoreSelectionFromUndoEntry(entry) {
  if (!entry.selection) return;
  const helper = route.mode === "stage" ? stageSelection : matchSelections.get(undoStackContext?.matchCode || currentMatchCode());
  if (!helper) return;
  helper.setSelection(entry.selection.anchor, entry.selection.focus, {focus: true});
}

function attachMatchSelection(table, matchState, matchCode) {
  if (!table || !matchState) return;
  if (matchSelections.has(matchCode)) {
    matchSelections.get(matchCode).unbind();
    matchSelections.delete(matchCode);
  }
  const helper = gameTable.createCellRangeSelection({
    root: table,
    cellSelector: ".answer-cell",
    readonly: () => Boolean(matchState.finished),
    coordOf: (cell) => ekCoordOf(cell, matchState),
    cellAtCoord: (coord) => ekCellAtCoord(table, coord, matchState),
    serialize: ekSerializeMark,
    parse: parseMarkText,
    applyValues: (edits) => ekApplyValues(matchCode, matchState, edits),
    onActiveChange: (cell) => {
      if (!cell) return;
      const team = Number(cell.dataset.team);
      const theme = Number(cell.dataset.theme);
      const answer = Number(cell.dataset.answer);
      const shootout = cell.dataset.shootout === "1";
      activeCell = {matchCode, team, shootout, theme, answer};
      markActiveCell();
      publishPresence();
    },
  });
  helper.bind();
  matchSelections.set(matchCode, helper);
  return helper;
}

function activeMatchSelection() {
  if (route.mode === "stage") return stageSelection;
  return matchSelections.get(activeCell.matchCode || currentMatchCode()) || null;
}

function activeSelectionCoord() {
  if (route.mode === "stage") {
    const matchCode = activeCell.matchCode || currentMatchCode();
    const matchIndex = currentStageMatches().findIndex((m) => m.code === matchCode);
    if (matchIndex < 0) return null;
    const matchState = currentStageStateByCode()?.get(matchCode);
    if (!matchState) return null;
    const answers = answerCountFor(matchState);
    if (answers <= 0) return null;
    const themeOrder = activeCell.shootout ? regularThemeCountFor(matchState) + activeCell.theme : activeCell.theme;
    return {row: stageRowOffset(matchIndex) + activeCell.team, col: themeOrder * answers + activeCell.answer};
  }
  const matchState = activeMatchState();
  if (!matchState) return null;
  return ekCoordOf({
    dataset: {
      team: String(activeCell.team),
      theme: String(activeCell.theme),
      answer: String(activeCell.answer),
      shootout: activeCell.shootout ? "1" : "0",
    },
  }, matchState);
}

function syncSelectionToActiveCell() {
  const helper = activeMatchSelection();
  if (!helper) return;
  const coord = activeSelectionCoord();
  if (!coord) return;
  helper.setSelection(coord, coord, {focus: false});
}

function buildTable(options = {}) {
  const matchCode = currentMatchCode();
  const hasShootout = shootoutThemeCount() > 0;
  const showPlaceColumn = true;
  const themes = renderedThemeHeaders();
  const rows = state.teams.map((team, teamIndex) => {
    const themeCellsList = [];
    team.themes.forEach((theme, themeIndex) => {
      themeCellsList.push(themeCells(team, teamIndex, theme, themeIndex, false));
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      themeCellsList.push(themeCells(team, teamIndex, theme, themeIndex, true));
    });
    return {
      rowClassName: isActiveMatchRow(matchCode, teamIndex) ? "active-team-row" : "",
      nameCell: teamNameCell(team, teamIndex),
      totalCell: totalCell(team, teamIndex),
      placeCell: showPlaceColumn ? placeCell(team, teamIndex, matchCode) : null,
      themes: themeCellsList,
      afterThemeCells: trailingCells(team, teamIndex, hasShootout),
    };
  });

  const table = gameTable.buildTwoRowScoreTable({
    className: options.compact ? "match-table compact-score-table ek-stage-table" : "match-table",
    attrs: {dataset: {matchCode}},
    rowMarkerColumn: !options.compact,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
    nameHeader: battleHeader(),
    placeColumn: showPlaceColumn,
    themes,
    afterThemeHeaders: trailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
  table.classList.toggle("match-finished", state.finished);
  return table;
}

function renderedThemeHeaders() {
  const themes = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({
      label: `Т${theme + 1}`,
      questionLabels: state.questionValues,
      gapHeaderClassName: isLastRenderedTheme(false, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(false, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: `П${theme + 1}`,
      questionLabels: state.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
      gapHeaderClassName: isLastRenderedTheme(true, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(true, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  return themes;
}

function trailingHeaders(hasShootout) {
  const headers = [shootoutControlsHeader()];
  if (hasShootout) headers.push({content: "П", className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function teamNameCell(team, teamIndex) {
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {rowSpan: 2});
  cell.dataset.team = String(teamIndex);
  const labelText = team.name || "";
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout";

  const nameWrap = document.createElement("span");
  nameWrap.className = "od-detailed-team-name-wrap";
  const label = document.createElement("span");
  label.className = "readonly-team-name od-detailed-team-name";
  label.textContent = labelText;
  label.tabIndex = 0;
  label.setAttribute("aria-label", labelText);
  nameWrap.appendChild(label);
  layout.appendChild(nameWrap);
  cell.appendChild(layout);

  const fullName = document.createElement("span");
  fullName.className = "od-detailed-team-name-popover";
  fullName.textContent = labelText;
  cell.appendChild(fullName);
  return cell;
}

function totalCell(team, teamIndex) {
  const cell = td(team.total, "sticky sticky-total number total-cell", {rowSpan: 2});
  cell.dataset.team = String(teamIndex);
  return cell;
}

function placeCell(team, teamIndex, matchCode) {
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "decimal";
  input.value = formatPlace(team.place);
  input.className = "place-input";
  input.disabled = state.finished;
  input.dataset.matchCode = matchCode;
  input.dataset.team = String(teamIndex);
  input.dataset.committedPlace = String(team.place || 0);
  const commitPlace = () => {
    const place = parsePlace(input.value);
    if (place === null) {
      input.value = formatPlace(team.place);
      return;
    }
    input.value = formatPlace(place);
    if (place === Number(input.dataset.committedPlace)) {
      return true;
    }
    input.dataset.committedPlace = String(place);
    queueEKEdits(matchCode, [{team: teamIndex, place}]);
    return true;
  };
  input.addEventListener("change", commitPlace);
  input.addEventListener("keydown", (event) => {
    const isForward = event.key === "ArrowDown" || (event.key === "Tab" && !event.shiftKey);
    const isBackward = event.key === "ArrowUp" || (event.key === "Tab" && event.shiftKey);
    if (event.key !== "Enter" && !isForward && !isBackward) return;

    event.preventDefault();
    if (!commitPlace()) return;
    if (isForward || isBackward) {
      const direction = isForward ? 1 : -1;
      const nextTeam = clamp(teamIndex + direction, 0, state.teams.length - 1);
      focusPlaceInput(nextTeam, {select: true, matchCode});
    }
  });
  const cell = document.createElement("td");
  cell.className = "sticky sticky-place number place-cell";
  cell.rowSpan = 2;
  cell.dataset.team = String(teamIndex);
  cell.appendChild(input);
  return cell;
}

function themeCells(team, teamIndex, theme, themeIndex, isShootout) {
  const matchCode = currentMatchCode();
  const playerCell = document.createElement("td");
  playerCell.colSpan = state.questionValues.length;
  playerCell.className = "player-cell theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }

  const editor = document.createElement("div");
  editor.className = "player-editor";

  const selectWrap = document.createElement("span");
  selectWrap.className = "player-select-wrap";
  const select = document.createElement("select");
  select.className = "player-select";
  select.dataset.matchCode = matchCode;
  select.dataset.team = String(teamIndex);
  select.dataset.shootout = isShootout ? "1" : "0";
  select.dataset.theme = String(themeIndex);
  select.appendChild(option("", ""));
  const roster = team.roster || [];
  roster.forEach((player) => select.appendChild(option(player, player)));
  if (theme.player && !roster.includes(theme.player)) {
    select.appendChild(option(theme.player, theme.player));
  }
  select.value = theme.player;
  select.disabled = state.finished;
  select.addEventListener("change", () => {
    const payload = {team: teamIndex, theme: themeIndex, player: select.value};
    if (isShootout) payload.shootout = true;
    updatePlayerSelectOverflow(selectWrap);
    queueEKEdits(matchCode, [payload]);
  });
  selectWrap.appendChild(select);
  const playerPopover = document.createElement("span");
  playerPopover.className = "player-select-popover";
  playerPopover.textContent = selectedPlayerLabel(select);
  selectWrap.appendChild(playerPopover);
  editor.appendChild(selectWrap);

  playerCell.appendChild(editor);
  const scoreCell = td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2});
  scoreCell.dataset.team = String(teamIndex);
  scoreCell.dataset.shootout = isShootout ? "1" : "0";
  scoreCell.dataset.theme = String(themeIndex);
  const gapClass = isLastRenderedTheme(isShootout, themeIndex) ? "gap shootout-adjacent-gap" : "gap";

  const answers = theme.answers.map((mark, answerIndex) => {
    const cell = document.createElement("td");
    cell.className = `answer-cell theme-block ${mark}`;
    if (isShootout) {
      cell.classList.add("shootout-block");
    }
    if (answerIndex === 0) {
      cell.classList.add("theme-block-bottom-left");
    }
    if (!state.finished && isActiveCell(teamIndex, isShootout, themeIndex, answerIndex)) {
      cell.classList.add("active");
    }
    cell.tabIndex = state.finished ? -1 : 0;
    cell.dataset.team = String(teamIndex);
    cell.dataset.matchCode = matchCode;
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    cell.title = `${team.name}, ${isShootout ? "П" : "Т"}${themeIndex + 1}, ${state.questionValues[answerIndex]}`;
    if (!state.finished) {
      cell.addEventListener("click", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {matchCode});
      });
      cell.addEventListener("focus", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {focus: false, matchCode});
      });
    }
    return cell;
  });

  return {playerCell, scoreCell, gapClassName: gapClass, answers};
}

function trailingCells(team, teamIndex, hasShootout) {
  const cells = [td("", "shootout-controls-cell", {rowSpan: 2})];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    const tiebreakCell = td(shootoutTotal, "number tiebreak-cell", {rowSpan: 2});
    tiebreakCell.dataset.team = String(teamIndex);
    cells.push(tiebreakCell);
  }
  const plusCell = td(team.plus, "number plus-cell", {rowSpan: 2});
  plusCell.dataset.team = String(teamIndex);
  cells.push(plusCell);
  [0, 1, 2, 3, 4].forEach((idx) => {
    const correctCell = td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {rowSpan: 2});
    correctCell.dataset.team = String(teamIndex);
    correctCell.dataset.valueIndex = String(idx);
    cells.push(correctCell);
  });
  return cells;
}

function battleHeader() {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "sticky sticky-name battle";

  const layout = document.createElement("span");
  layout.className = "battle-layout";

  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = state.title || matchTitle();
  layout.appendChild(title);

  if (venues.length > 0) {
    const venueButton = document.createElement("button");
    venueButton.type = "button";
    venueButton.className = "venue-edit-button";
    venueButton.dataset.matchCode = matchCode;
    venueButton.textContent = "✏️";
    venueButton.title = "Изменить площадку";
    venueButton.setAttribute("aria-label", "Изменить площадку");
    venueButton.addEventListener("click", () => openVenueDialog(matchCode));
    layout.appendChild(venueButton);
  }

  const label = document.createElement("label");
  label.className = "finish-control";
  label.title = "Закончен";
  label.setAttribute("aria-label", "Закончен");

  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.dataset.matchCode = matchCode;
  checkbox.checked = Boolean(state.finished);
  checkbox.addEventListener("change", () => {
    setMatchFinished(matchCode, checkbox.checked);
  });
  label.append(checkbox);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function openVenueDialog(matchCode) {
  const matchState = matchStateFor(matchCode);
  if (!matchState) return;
  const dialog = document.createElement("dialog");
  dialog.className = "venue-dialog";
  const form = document.createElement("form");
  form.className = "venue-dialog-form";

  const title = document.createElement("h2");
  title.textContent = matchState.title || matchTitle(matchState);
  form.appendChild(title);

  const select = document.createElement("select");
  select.className = "venue-dialog-select";
  venues.forEach((venue) => {
    select.appendChild(option(String(venue.number), `${venue.number}: ${venue.title}`));
  });
  select.value = matchState.venue ? String(matchState.venue.number) : "";
  form.appendChild(select);

  const actions = document.createElement("div");
  actions.className = "venue-dialog-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = "Отмена";
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = "Сохранить";
  actions.append(cancel, save);
  form.appendChild(actions);

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const number = Number(select.value);
    dialog.close();
    const current = matchStateFor(matchCode) || matchState;
    if (number > 0 && number !== current.venue?.number) {
      sendVenueChange(number, matchCode);
    }
  });
  dialog.addEventListener("close", () => dialog.remove());
  dialog.appendChild(form);
  document.body.appendChild(dialog);
  dialog.showModal();
  select.focus();
}

function shootoutControlsHeader() {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "shootout-controls-head";

  const addShootout = document.createElement("button");
  addShootout.type = "button";
  addShootout.className = "shootout-add-button";
  addShootout.textContent = "+П";
  addShootout.title = "Добавить тему перестрелки";
  addShootout.setAttribute("aria-label", "Добавить тему перестрелки");
  addShootout.disabled = state.finished;
  addShootout.addEventListener("click", () => {
    const ms = matchStateFor(matchCode);
    if (!ms) return;
    withMatchState(ms, () => {
      activeCell = {matchCode, team: 0, shootout: true, theme: shootoutThemeCount(), answer: 0};
      sendUpdate({action: "addShootoutTheme"}, matchCode);
    });
  });
  node.appendChild(addShootout);

  if (shootoutThemeCount() > 0) {
    const deleteButton = document.createElement("button");
    deleteButton.type = "button";
    deleteButton.className = "theme-delete-button";
    deleteButton.textContent = "−П";
    deleteButton.title = "Удалить тему перестрелки";
    deleteButton.setAttribute("aria-label", "Удалить тему перестрелки");
    deleteButton.disabled = state.finished;
    deleteButton.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (!window.confirm("Удалить тему перестрелки?")) return;
      const ms = matchStateFor(matchCode);
      if (!ms) return;
      withMatchState(ms, () => removeLastShootoutTheme(matchCode));
    });
    node.appendChild(deleteButton);
  }

  return node;
}

function handleGlobalKeydown(event) {
  if ((route.mode !== "match" && route.mode !== "stage") || isFormControl(event.target)) return;
  // event.code is the physical key (layout-independent), so Cmd/Ctrl-Z fires on a
  // Russian layout too — there the Z key reports event.key "я", which the old
  // key-based check missed, so undo did nothing for Cyrillic-keyboard users.
  const isUndoKey = event.code === "KeyZ" || event.key.toLowerCase() === "z" || event.key === "я" || event.key === "Я";
  if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && isUndoKey) {
    if (performUndo()) event.preventDefault();
    return;
  }
  const matchState = activeMatchState();
  if (!matchState) return;

  withMatchState(matchState, () => {
    const key = event.key.toLowerCase();
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      moveActiveCell(0, -1, event.shiftKey);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      moveActiveCell(0, 1, event.shiftKey);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      moveActiveCell(-1, 0, event.shiftKey);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      moveActiveCell(1, 0, event.shiftKey);
    } else if (key === "q" || key === "й" || key === "1") {
      event.preventDefault();
      setMarkForSelection("right");
    } else if (key === "w" || key === "ц" || key === "-" || key === "2") {
      event.preventDefault();
      setMarkForSelection("wrong");
    } else if (key === "backspace" || key === "delete" || event.key === " ") {
      event.preventDefault();
      setMarkForSelection("");
    }
  });
}

function setMarkForSelection(mark) {
  if (state?.finished && route.mode === "match") return;
  const helper = activeMatchSelection();
  const cells = helper?.selectedCells() || [];
  if (cells.length > 1) {
    if (route.mode === "stage") {
      stageApplyValues(cells.map((cell) => ({cell, value: mark})));
      return;
    }
    const matchState = activeMatchState();
    if (!matchState || matchState.finished) return;
    ekApplyValues(activeCell.matchCode || currentMatchCode(), matchState, cells.map((cell) => ({cell, value: mark})));
    return;
  }
  setActiveMark(mark);
}

function isFormControl(target) {
  return gameTable.isFormControl(target);
}

function selectAnswerCell(team, shootout, theme, answer, options = {}) {
  activeCell = {matchCode: options.matchCode || currentMatchCode(), team, shootout, theme, answer};
  markActiveCell();
  publishPresence();
  if (options.focus !== false) {
    focusActiveCell();
  }
  if (options.syncSelection) syncSelectionToActiveCell();
}

function moveActiveCell(teamDelta, answerDelta, extend = false) {
  const maxTeam = state.teams.length - 1;
  const maxColumn = totalThemeCount() * state.questionValues.length - 1;
  const column = activeCellColumn();
  let nextTeam = activeCell.team + teamDelta;
  let nextColumn = column + answerDelta;
  let nextMatchCode = activeCell.matchCode || currentMatchCode();
  if (route.mode === "stage" && (nextTeam < 0 || nextTeam > maxTeam)) {
    const sibling = adjacentStageMatch(nextMatchCode, nextTeam < 0 ? -1 : 1);
    if (sibling) {
      nextMatchCode = sibling.code;
      const siblingMaxTeam = (sibling.state?.teams?.length || 1) - 1;
      nextTeam = nextTeam < 0 ? siblingMaxTeam : 0;
    } else {
      nextTeam = clamp(nextTeam, 0, maxTeam);
    }
  } else {
    nextTeam = clamp(nextTeam, 0, maxTeam);
  }
  nextColumn = clamp(nextColumn, 0, maxColumn);
  const previousMatchCode = activeCell.matchCode || currentMatchCode();
  if (nextMatchCode !== previousMatchCode) {
    const siblingState = currentStageStateByCode()?.get(nextMatchCode);
    if (siblingState) {
      withMatchState(siblingState, () => {
        activeCell = {...cellFromColumn(nextTeam, nextColumn), matchCode: nextMatchCode};
      });
    }
  } else {
    activeCell = cellFromColumn(nextTeam, nextColumn);
  }
  markActiveCell();
  focusActiveCell();
  if (extend) {
    extendSelectionToActiveCell();
  } else {
    syncSelectionToActiveCell();
  }
}

function adjacentStageMatch(matchCode, direction) {
  if (route.mode !== "stage") return null;
  const matches = currentStageMatches();
  const index = matches.findIndex((match) => match.code === matchCode);
  if (index < 0) return null;
  const targetMatch = matches[index + direction];
  if (!targetMatch) return null;
  const targetState = currentStageStateByCode()?.get(targetMatch.code);
  if (!targetState) return null;
  return {code: targetMatch.code, state: targetState};
}

function extendSelectionToActiveCell() {
  const helper = activeMatchSelection();
  if (!helper) return;
  const coord = activeSelectionCoord();
  if (!coord) return;
  const currentAnchor = helper.anchor || coord;
  helper.setSelection(currentAnchor, coord, {focus: false});
}

function setActiveMark(mark) {
  if (state.finished) return;
  const matchCode = currentMatchCode();
  const matchState = matchStateFor(matchCode);
  const cell = findActiveCell();
  if (matchState && cell) {
    ekApplyValues(matchCode, matchState, [{cell, value: mark}]);
    return;
  }
  const payload = {
    team: activeCell.team,
    theme: activeCell.theme,
    answer: activeCell.answer,
    mark,
  };
  if (activeCell.shootout) payload.shootout = true;
  queueEKEdits(matchCode, [payload]);
}

function markActiveCell() {
  clearActiveTeamRows();
  if (route.mode === "match" && activeAnswerNode) {
    activeAnswerNode.classList.remove("active");
    activeAnswerNode = null;
  } else {
    document.querySelectorAll(".answer-cell.active").forEach((cell) => cell.classList.remove("active"));
  }
  const cell = findActiveCell();
  if (cell) {
    cell.classList.add("active");
    markActiveTeamRows(cell);
    if (route.mode === "match") activeAnswerNode = cell;
  }
}

function isActiveMatchRow(matchCode, teamIndex) {
  return !state.finished &&
    activeCell.matchCode === matchCode &&
    activeCell.team === teamIndex;
}

function clearActiveTeamRows() {
  if (activeTeamRows.length > 0) {
    activeTeamRows.forEach((row) => row.classList.remove("active-team-row"));
    activeTeamRows = [];
    return;
  }
  hostRoot.querySelectorAll(".active-team-row").forEach((row) => row.classList.remove("active-team-row"));
}

function markActiveTeamRows(cell) {
  clearActiveTeamRows();
  if (!cell) return;
  const table = cell.closest(".match-table");
  const team = cell.dataset.team;
  if (!table || team == null) return;
  const rows = new Set();
  table.querySelectorAll(`[data-team="${cssEscape(team)}"]`).forEach((node) => {
    const row = node.closest("tr");
    if (row?.parentElement?.tagName === "TBODY") rows.add(row);
  });
  activeTeamRows = Array.from(rows);
  activeTeamRows.forEach((row) => row.classList.add("active-team-row"));
}

function focusActiveCell(options = {}) {
  const cell = findActiveCell();
  if (cell) cell.focus(options);
}

function focusPlaceInput(team, options = {}) {
  const matchCode = options.matchCode || currentMatchCode();
  const input = indexedNode("placeInput", {team}) ||
    document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${team}"]`);
  if (!input) return;
  input.focus({preventScroll: options.preventScroll});
  if (options.select) input.select();
}

function focusFinishToggle(options = {}) {
  const input = document.querySelector(".finish-toggle");
  if (input) input.focus({preventScroll: options.preventScroll});
}

function focusedPlaceTeamIndex() {
  const element = document.activeElement;
  if (!(element instanceof HTMLInputElement) || !element.classList.contains("place-input")) {
    return null;
  }
  const team = Number(element.dataset.team);
  return Number.isInteger(team) ? team : null;
}

function isFinishToggleFocused() {
  const element = document.activeElement;
  return element instanceof HTMLInputElement && element.classList.contains("finish-toggle");
}

function findActiveCell() {
  const matchCode = currentMatchCode();
  const indexed = indexedNode("answer", {
    team: activeCell.team,
    shootout: activeCell.shootout ? "1" : "0",
    theme: activeCell.theme,
    answer: activeCell.answer,
  });
  if (indexed) return indexed;
  return document.querySelector(
    `.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${activeCell.team}"][data-shootout="${activeCell.shootout ? "1" : "0"}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActiveCell(team, shootout, theme, answer) {
  return activeCell.matchCode === currentMatchCode() &&
    activeCell.team === team &&
    activeCell.shootout === shootout &&
    activeCell.theme === theme &&
    activeCell.answer === answer;
}

function normalizeActiveCell() {
  if (!state?.teams?.length || totalThemeCount() === 0) return;
  const team = clamp(activeCell.team, 0, state.teams.length - 1);
  const column = clamp(activeCellColumn(), 0, totalThemeCount() * state.questionValues.length - 1);
  activeCell = cellFromColumn(team, column);
}

function activeCellColumn() {
  const themeOffset = activeCell.shootout
    ? regularThemeCount() + activeCell.theme
    : activeCell.theme;
  return themeOffset * state.questionValues.length + activeCell.answer;
}

function cellFromColumn(team, column) {
  const themeOffset = Math.floor(column / state.questionValues.length);
  const answer = column % state.questionValues.length;
  if (themeOffset < regularThemeCount()) {
    return {matchCode: currentMatchCode(), team, shootout: false, theme: themeOffset, answer};
  }
  return {matchCode: currentMatchCode(), team, shootout: true, theme: themeOffset - regularThemeCount(), answer};
}

function removeLastShootoutTheme(matchCode = currentMatchCode()) {
  const lastTheme = shootoutThemeCount() - 1;
  if (lastTheme < 0) return;
  activeCell = {...activeCell, matchCode};
  if (activeCell.shootout && activeCell.theme >= lastTheme) {
    if (lastTheme > 0) {
      activeCell = {...activeCell, theme: lastTheme - 1};
    } else {
      activeCell = {matchCode: currentMatchCode(), team: activeCell.team, shootout: false, theme: regularThemeCount() - 1, answer: 0};
    }
  }
  sendUpdate({action: "removeShootoutTheme"}, matchCode);
}

function regularThemeCount() {
  return state.teams[0].themes.length;
}

function shootoutThemeCount() {
  return shootoutThemesFor(state.teams[0]).length;
}

function totalThemeCount() {
  return regularThemeCount() + shootoutThemeCount();
}

function shootoutThemesFor(team) {
  return team.shootoutThemes || [];
}

function isLastRenderedTheme(isShootout, themeIndex) {
  if (isShootout) {
    return themeIndex === shootoutThemeCount() - 1;
  }
  return shootoutThemeCount() === 0 && themeIndex === regularThemeCount() - 1;
}

function currentRoute() {
  const path = window.location.pathname;
  const prefix = path.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const festID = prefix[1];
  const gameID = prefix[2];
  const base = `/host/fest/${festID}/game/${gameID}`;
  const viewerBase = `/fest/${festID}/game/${gameID}`;
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  const festApi = `/api/fest/${festID}`;
  const rest = path.slice(prefix[0].length).replace(/\/$/, "");
  if (rest === "" || rest === "/") {
    return {mode: "grid", festID, gameID, base, viewerBase, apiBase, festApi};
  }
  if (rest === "/venues") return {mode: "venues", festID, gameID, base, viewerBase, apiBase, festApi};
  if (rest === "/stats") return {mode: "stats", festID, gameID, base, viewerBase, apiBase, festApi};
  if (rest === "/seed-import") return {mode: "seedImport", festID, gameID, base, viewerBase, apiBase, festApi};
  const match = rest.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), festID, gameID, base, viewerBase, apiBase, festApi};
  const stage = rest.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), festID, gameID, base, viewerBase, apiBase, festApi};
  return {mode: "missing"};
}

function findStage(data, code) {
  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  return stages.find((stage) => stage.code === code);
}

function findLiveStage(data, code) {
  return (data?.stages || []).find((stage) => stage.code === code);
}

function mergedStage(data, code) {
  const schemeStage = findStage(data, code) || {};
  const liveStage = findLiveStage(data, code) || {};
  return {
    ...schemeStage,
    ...liveStage,
    config: liveStage.config || schemeStage.config,
    reseedEntries: liveStage.reseedEntries || schemeStage.reseedEntries || [],
  };
}

const stageType = gameTable.stageType;

function setHeading(text) {
  if (pageHeading) {
    pageHeading.textContent = "";
    pageHeading.hidden = true;
  }
  renderGameBreadcrumbs();
}

function renderGameBreadcrumbs() {
  if (!breadcrumbsNode || !route.festID) return;
  const gameTitle = fest?.gameName || currentGameTitle() || "ЭК";
  gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
    festHref: `/host/fest/${route.festID}`,
    festTitle: fest?.title || "Фест",
    gameHref: route.mode === "grid" ? "" : route.base + "/",
    gameTitle,
    currentTitle: breadcrumbCurrentTitle(gameTitle),
  });
}

function breadcrumbCurrentTitle(gameTitle) {
  if (route.mode === "grid") return "";
  if (route.mode === "venues") return "Площадки";
  if (route.mode === "stats") return "Статистика";
  if (route.mode === "seedImport") return "Импорт команд";
  if (route.mode === "match") return state?.title || route.matchCode || "";
  if (route.mode === "stage") return findStage(fest, route.stageCode)?.title || route.stageCode || "";
  return gameTitle;
}

function setViewerLink(href, title) {
  if (!viewerLink) return;
  viewerLink.href = href;
  viewerLink.title = title;
  viewerLink.setAttribute("aria-label", title);
}

function setHostMode(mode) {
  hostRoot.classList.toggle("grid-host", mode === "grid");
  hostRoot.classList.toggle("fight-host", mode === "match");
}

function pageTitle(primary = "") {
  const main = String(primary || currentGameTitle() || state?.title || "").trim();
  const festTitle = String(fest?.title || "").trim();
  if (main && festTitle) return `${main} · ${festTitle}`;
  return main || festTitle || "Фест";
}

function currentGameTitle() {
  const scheme = parseScheme(fest?.schemaJson);
  return String(scheme?.title || "").trim();
}

function notifyEmbeddedResize() {
  if (!embedded || window.parent === window) return;
  window.requestAnimationFrame(() => {
    window.parent.postMessage({
      type: "dope:resize",
      height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
    }, window.location.origin);
  });
}

function matchTitle(matchState = state) {
  const venue = matchState.venue ? ` · ${formatBattleVenue(matchState.venue)}` : "";
  return `${matchState.title}${venue}`;
}

function parsePlace(value) {
  const normalized = value.trim().replace(",", ".");
  if (normalized === "") return 0;
  const place = Number(normalized);
  if (!Number.isFinite(place) || place < 0) return null;
  return place;
}

function connectPresence() {
  if (presence || embedded || !route.festID) return;
  presence = gameTable.createHostPresence({
    root: hostRoot,
    eventsURL: `/host-events?fest_id=${encodeURIComponent(route.festID)}`,
    presenceURL: `${route.festApi}/presence`,
    cursorFromElement: hostPresenceCursorFromElement,
    getCursor: currentHostPresenceCursor,
    findTarget: findHostPresenceTarget,
  });
  presence.connect();
}

function refreshPresence() {
  presence?.refresh();
}

function publishPresence() {
  presence?.publishCurrent();
}

function currentHostPresenceCursor() {
  const focused = hostPresenceCursorFromElement(document.activeElement);
  if (focused) return focused;
  if (route.mode !== "match" && route.mode !== "stage") return null;
  return {
    app: "ek",
    kind: "answer",
    gameID: route.gameID,
    matchCode: activeCell.matchCode || currentMatchCode(),
    team: activeCell.team,
    shootout: Boolean(activeCell.shootout),
    theme: activeCell.theme,
    answer: activeCell.answer,
  };
}

function hostPresenceCursorFromElement(element) {
  const target = element?.closest?.(".answer-cell,.player-select,.place-input,.finish-toggle,.venue-edit-button");
  if (!target || !hostRoot.contains(target)) return null;
  const matchCode = target.dataset.matchCode || currentMatchCode();
  if (target.classList.contains("answer-cell")) {
    return {
      app: "ek",
      kind: "answer",
      gameID: route.gameID,
      matchCode,
      team: Number(target.dataset.team),
      shootout: target.dataset.shootout === "1",
      theme: Number(target.dataset.theme),
      answer: Number(target.dataset.answer),
    };
  }
  if (target.classList.contains("player-select")) {
    return {
      app: "ek",
      kind: "player",
      gameID: route.gameID,
      matchCode,
      team: Number(target.dataset.team),
      shootout: target.dataset.shootout === "1",
      theme: Number(target.dataset.theme),
    };
  }
  if (target.classList.contains("place-input")) {
    return {app: "ek", kind: "place", gameID: route.gameID, matchCode, team: Number(target.dataset.team)};
  }
  if (target.classList.contains("finish-toggle")) {
    return {app: "ek", kind: "finish", gameID: route.gameID, matchCode};
  }
  if (target.classList.contains("venue-edit-button")) {
    return {app: "ek", kind: "venue", gameID: route.gameID, matchCode};
  }
  return null;
}

function findHostPresenceTarget(cursor) {
  if (!cursor || cursor.app !== "ek" || String(cursor.gameID) !== String(route.gameID)) return null;
  const matchCode = cssEscape(cursor.matchCode || route.matchCode || "");
  switch (cursor.kind) {
  case "answer":
    return hostRoot.querySelector(
      `.answer-cell[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"][data-shootout="${cursor.shootout ? "1" : "0"}"][data-theme="${cssEscape(cursor.theme)}"][data-answer="${cssEscape(cursor.answer)}"]`,
    );
  case "player":
    return hostRoot.querySelector(
      `.player-select[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"][data-shootout="${cursor.shootout ? "1" : "0"}"][data-theme="${cssEscape(cursor.theme)}"]`,
    );
  case "place":
    return hostRoot.querySelector(`.place-input[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"]`);
  case "finish":
    return hostRoot.querySelector(`.finish-toggle[data-match-code="${matchCode}"]`);
  case "venue":
    return hostRoot.querySelector(`.venue-edit-button[data-match-code="${matchCode}"]`);
  default:
    return null;
  }
}

function option(value, label) {
  return gameTable.option(value, label);
}

bindSPANavigation();
recorder = gameTable.installClientRecorder({
  scope: `ek:${route.festID}:${route.gameID}`,
  getState: () => ({mode: route.mode, matchCode: route.matchCode, stageCode: route.stageCode, state}),
});
loadCurrent()
  .then(() => {
    setStatus("saved");
    connectEvents();
    connectPresence();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
