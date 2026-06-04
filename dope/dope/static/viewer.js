const viewerRoot = document.getElementById("viewerTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerTabsRoot = document.getElementById("viewerTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);
const {formatVenue, formatBattleVenue, formatBattleVenueShort, statusLabel, formatNumber, formatPlace, cssEscape, th, td} = gameTable;
let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
const canEdit = Boolean(window.__VIEWER_INIT__?.canEdit);
// The server scopes SSE events by NUMERIC game id (`match:<id>:<code>`), but the
// URL only carries the game slug. Take the numeric id from the inlined init so
// match-scope comparisons match and the focused match patches in place.
const scopeGameID = window.__VIEWER_INIT__?.route?.gameID != null
  ? String(window.__VIEWER_INIT__.route.gameID)
  : route.gameID;
const editorLink = canEdit && !embedded ? gameTable.mountEditorLink(statusNode) : null;
let state = null;
let fest = null;
let venues = [];
const stageCache = window.DopeStageCache.create({
  container: viewerRoot,
  apiBase: () => route.apiBase,
  schemeStages: () => (fest ? viewerStages() : []),
  findStage: (code) => findStage(fest, code),
  stageType: (stage) => stageType(stage),
  getMatches: (stage) => stage?.matches || [],
  buildPaneContent: ({pane, stageCode, stage, data}) => {
    if (stageType(stage) === "reseed") {
      pane.appendChild(buildReseedStagePanel(mergedStage(fest, stageCode)));
    } else {
      pane.appendChild(buildReadonlyStageTables(data));
    }
  },
  onStageDataChanged: ({pane, stageCode, data}) => {
    repaintStagePane(pane, stageCode, data);
  },
  onMatchUpdated: ({pane, frame, matchState, descriptor}) => {
    paintStageFrame(frame, matchState, descriptor);
    if (pane.isConnected && !pane.hidden) scheduleReadonlyNameOverflowUpdate(frame);
  },
  onPaneShown: ({pane}) => {
    scheduleReadonlyNameOverflowUpdate(pane);
    updateStageScrollState(viewerRoot.closest(".sheet-frame"));
  },
});
let reloadTimer = null;
let readonlyTableIndex = null;
let viewerTabsFadeFrame = 0;
let readonlyNameOverflowFrame = 0;

const floatingPopoverSpecs = [
  {
    trigger: ".readonly-battle-head.readonly-battle-with-popover",
    popover: ".readonly-battle-popover",
    anchor: ".readonly-battle-title",
  },
  {
    trigger: ".ek-team-cell.od-detailed-team-cell-truncated",
    popover: ".od-detailed-team-name-popover",
    anchor: ".od-detailed-team-name-wrap",
  },
  {
    trigger: ".readonly-player.readonly-player-cell-truncated",
    popover: ".readonly-player-popover",
    anchor: ".readonly-player-text-wrap",
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
];

document.body.classList.toggle("embedded-match", embedded);
const floatingPopover = gameTable.createFloatingPopover({root: viewerRoot, specs: floatingPopoverSpecs});
floatingPopover.bind();
window.addEventListener("resize", () => {
  scheduleReadonlyNameOverflowUpdate();
  floatingPopover.position();
  scheduleViewerTabsFadeUpdate();
});

async function loadCurrent() {
  if (consumeViewerInit()) return;
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else if (route.mode === "stats") {
    await loadStats();
  } else {
    await loadFest();
  }
}

// consumeViewerInit renders the first frame from server-inlined
// window.__VIEWER_INIT__, skipping the cold API round trips. Returns true on
// success; mismatched routes fall back to the network path.
function consumeViewerInit() {
  const init = window.__VIEWER_INIT__;
  if (!init || !init.route || !init.fest) return false;
  if (init.route.mode !== route.mode) return false;
  // See consumeHostInit: don't compare festID/gameID. Server resolved slugs
  // to numeric ids, which won't string-match the URL slug.
  if (route.mode === "match" && init.route.matchCode !== route.matchCode) return false;
  if (route.mode === "stage" && init.route.stageCode !== route.stageCode) return false;
  window.__VIEWER_INIT__ = null;

  adoptFestView(init.fest);
  if (Array.isArray(init.venues)) venues = init.venues;
  writeFestCache(init.fest);

  if (route.mode === "match") {
    if (!init.match) return false;
    state = init.match;
    render();
    return true;
  }
  if (route.mode === "venues") {
    renderVenues();
    return true;
  }
  if (route.mode === "stage") {
    // Stage view needs per-match state which isn't in init. Fall back to the
    // network path but with fest already hydrated, the wait shrinks to one
    // batch of parallel match fetches.
    return false;
  }
  renderFest();
  return true;
}

function festCacheKey() {
  return `viewer:fest:${route.festID || ""}:${route.gameID || ""}`;
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
    // ignore
  }
}

function adoptFestView(view) {
  fest = view;
  if (Array.isArray(view?.venues)) venues = view.venues;
  stageCache.adoptFest(view);
}

function hydrateFestFromCache() {
  if (fest) return true;
  const cached = readFestCache();
  if (!cached) return false;
  adoptFestView(cached);
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
  if (changed) renderFest();
}

async function loadStage() {
  const cached = hydrateFestFromCache();
  if (cached) renderStage();
  const stageCode = route.stageCode;
  // Revalidate fest and fetch this stage's matches in parallel.
  // adoptFestView clears stage caches if the revision changed.
  const festPromise = fetch(route.apiBase).then(async (response) => {
    if (!response.ok) throw new Error(await response.text());
    const fresh = await response.json();
    adoptFestView(fresh);
    writeFestCache(fresh);
  });
  const stagePromise = stageCache.prefetchStage(stageCode);
  await Promise.all([festPromise, stagePromise]);
  if (route.mode !== "stage" || route.stageCode !== stageCode) return;
  renderStage();
  // Background prefetch of every other stage. Each payload is <10KB and
  // makes subsequent tab switches instant (cache hit + pane already built).
  stageCache.prefetchAllStages();
}

function repaintStagePane(pane, stageCode, data) {
  const stage = mergedStage(fest, stageCode);
  if (stageType(stage) === "reseed") {
    pane.replaceChildren(buildReseedStagePanel(stage));
    return;
  }
  const frames = pane.querySelectorAll(".stage-match-frame");
  for (const frame of frames) {
    const code = frame.dataset.matchCode || "";
    const descriptor = data.matches.find((m) => m.code === code);
    paintStageFrame(frame, data.stateByCode.get(code), descriptor);
  }
  if (pane.isConnected && !pane.hidden) scheduleReadonlyNameOverflowUpdate(pane);
}

function paintStageFrame(frame, matchState, descriptor) {
  if (matchState) {
    // Patch scores/marks into the existing table when only those changed, so a
    // live update doesn't tear down and re-render the whole battle. Fall back
    // to a full rebuild (and re-index) when the table shape changes.
    const previous = frame.__matchState;
    if (previous && frame.__scoreIndex && canPatchMatchTable(previous, matchState)) {
      patchMatchTable(frame.__scoreIndex, matchState);
    } else {
      const table = withMatchState(matchState, () => buildReadonlyTable());
      frame.replaceChildren(table);
      frame.__scoreIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
    }
    frame.__matchState = matchState;
    return;
  }
  frame.__scoreIndex = null;
  frame.__matchState = null;
  const placeholder = document.createElement("div");
  placeholder.className = "stage-match-placeholder";
  placeholder.textContent = descriptor?.title || `Бой ${descriptor?.code || ""}`;
  frame.replaceChildren(placeholder);
}

function buildReadonlyStageTables(data) {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack";
  for (const match of data.matches) {
    const frame = document.createElement("section");
    frame.className = "stage-match-frame";
    frame.dataset.matchCode = match.code || "";
    paintStageFrame(frame, data.stateByCode.get(match.code), match);
    wrapper.appendChild(frame);
  }
  return wrapper;
}

async function loadMatch() {
  hydrateFestFromCache();
  const [matchResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode)}`),
    fetch(route.apiBase),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  state = await matchResponse.json();
  adoptFestView(await festResponse.json());
  writeFestCache(fest);
  render();
}

async function loadVenuesPage() {
  const cached = hydrateFestFromCache();
  if (cached) renderVenues();
  const [venuesResponse, festResponse] = await Promise.all([
    fetch(`/api/fest/${route.festID}/venues`),
    fetch(route.apiBase),
  ]);
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  const freshVenues = await venuesResponse.json();
  const freshFest = await festResponse.json();
  const changed = !cached || JSON.stringify(freshVenues) !== JSON.stringify(venues);
  venues = freshVenues;
  adoptFestView(freshFest);
  writeFestCache(freshFest);
  if (changed) renderVenues();
}

async function loadStats() {
  // Stats are an aggregate of every battle, computed from the shared stage
  // cache (the same per-match MatchViews the bracket holds). Warm it once with a
  // single /stages/matches request, deduped with the bracket prefetch; SSE
  // deltas then keep it live and renderStats reads from memory — no refetch.
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
// [{code, matches:[MatchView]}] form computeEKPlayerStats expects.
function statsStagesFromCache() {
  const stages = [];
  for (const stage of viewerStages()) {
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

function renderStats() {
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle("Статистика");
  renderViewerTabs();
  rerenderStatsTable();
}

// rerenderStatsTable recomputes the table from the live stage cache and swaps it
// in. Cheap (in-memory over the cached MatchViews); no network. Re-runs the
// name overflow pass so long player/team names get the fade + popover.
function rerenderStatsTable() {
  const rows = gameTable.computeEKPlayerStats(statsStagesFromCache());
  viewerRoot.replaceChildren(gameTable.buildEKStatsTable(rows));
  scheduleReadonlyNameOverflowUpdate();
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
    const matchScope = `match:${scopeGameID}:${route.matchCode}`;
    const venuesScope = `venues:${route.festID}`;
    // Always update cached stage state for any match-scoped event, regardless
    // of which page we're on. Keeps cached panes for other stages live so a
    // later tab switch sees fresh data without a fetch.
    if (message.scope?.startsWith("match:")) {
      handleMatchEvent(message, matchScope);
      setLive(true);
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setLive(true);
      return;
    }
    // Sibling games (e.g. OD/KSI) share this fest's SSE stream and emit
    // game-state:<theirID> events that don't affect the EK view. Ignore them —
    // otherwise editing a sibling game reloads (and flashes) the whole bracket.
    if (message.scope?.startsWith("game-state:") && message.scope !== `game-state:${scopeGameID}`) {
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
  events.onerror = () => setLive(false);
}

// Toggles the scrolled-under fade on the frozen-column boundary of the EK
// stage tables (see the .stage-scroll-left rule in styles.css), mirroring the
// OD/KSI behaviour. The .sheet-frame is static, so we bind the scroll listener
// once and let updateStageScrollState run on every scroll.
function bindStageScrollFade() {
  const scrollFrame = viewerRoot.closest(".sheet-frame");
  if (!scrollFrame) return;
  updateStageScrollState(scrollFrame);
  scrollFrame.addEventListener("scroll", () => updateStageScrollState(scrollFrame), {passive: true});
}

function updateStageScrollState(frame) {
  if (!frame) return;
  frame.classList.toggle("stage-scroll-left", frame.scrollLeft > 1);
}

// SPA navigation for the viewer tab strip: same pattern as the host EK page.
// Intercepts same-origin clicks within #viewerTabs, pushes the URL, and runs
// loadCurrent without reloading the page.
function bindViewerSPANavigation() {
  if (embedded) return;
  viewerTabsRoot?.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    if (event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = event.target?.closest?.("a[href]");
    if (!link || !viewerTabsRoot.contains(link)) return;
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
    history.pushState(null, "", url.pathname + url.search);
    runViewerCurrentRoute();
  });
  window.addEventListener("popstate", () => {
    runViewerCurrentRoute();
  });
}

function runViewerCurrentRoute() {
  route = currentRoute();
  setStatus("saving");
  editorLink?.refresh();
  loadCurrent()
    .then(() => setLive(true))
    .catch((error) => {
      setLive(false);
      console.error(error);
    });
}

function scheduleReload() {
  window.clearTimeout(reloadTimer);
  reloadTimer = window.setTimeout(() => {
    loadCurrent()
      .then(() => setLive(true))
      .catch((error) => {
        setLive(false);
        console.error(error);
      });
  }, 120);
}

// applyStatsMatchEvent keeps the stats page live the same way the bracket does:
// it folds a match-scoped SSE event into the shared stage cache in place (a
// chained delta, or a full snapshot) and recomputes the table from memory — no
// refetch. A delta that can't chain (missing base / seq gap) means a dropped
// event, so we resync the bracket once, mirroring the bracket's gap path.
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

function setLive(ok) {
  setStatus(ok ? "saved" : "error");
}

function renderFest() {
  if (!fest) return;
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  viewerRoot.replaceChildren(buildFestGrid(fest, {viewer: true, basePath: route.base}));
}

function renderStage() {
  if (!fest) return;
  const stageCode = route.stageCode;
  if (!stageCode) return;
  resetReadonlyTableIndex();
  const stage = mergedStage(fest, stageCode);
  setViewerMode(stageType(stage) === "reseed" ? "grid" : "match");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  stageCache.showStage(stageCode);
}

function renderVenues() {
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle("Площадки");
  renderViewerTabs();
  viewerRoot.replaceChildren(gameTable.buildVenuesTable(venues));
}

function render() {
  if (!state) return;
  setViewerMode("match");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  const table = buildReadonlyTable();
  readonlyTableIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
  if (embedded) {
    viewerRoot.replaceChildren(table);
    notifyEmbeddedResize();
  } else {
    viewerRoot.replaceChildren(table);
  }
  scheduleReadonlyNameOverflowUpdate();
  updateStageScrollState(viewerRoot.closest(".sheet-frame"));
}

function applyUpdatedMatch(updated) {
  const previous = state;
  state = updated;
  if (readonlyTableIndex && canPatchMatchTable(previous, updated)) {
    patchMatchTable(readonlyTableIndex, updated);
    return;
  }
  render();
}

function applyReadonlyStageMatchUpdate(updated) {
  const result = stageCache.applyMatchUpdate(updated);
  if (!result.found) {
    // Match not in any known stage — fest scheme probably changed.
    scheduleReload();
  }
}

// matchCodeFromScope extracts the match code from a "match:<gameID>:<code>"
// scope (codes never contain ':', but join the tail defensively).
function matchCodeFromScope(scope) {
  return scope.split(":").slice(2).join(":");
}

function isFocusedMatch(code) {
  return route.mode === "match" && code === route.matchCode;
}

// isDisplayed reports whether a match is currently on screen — the focused match
// in match mode, or any match of the open stage in stage mode. A seq gap on a
// displayed match must trigger a resync so the visible pane refreshes; a gap on
// an off-screen match only needs its cache evicted (refetched on navigation).
function isDisplayed(code) {
  if (isFocusedMatch(code)) return true;
  if (route.mode === "stage") return stageCache.stageCodeForMatch(code) === route.stageCode;
  return false;
}

// matchBase returns the cached full view a delta should apply onto: the focused
// match's `state` when we're on it, else the stage cache. null means we have no
// base (e.g. a match in a stage we haven't fetched yet).
function matchBase(code) {
  if (isFocusedMatch(code) && state?.code === code) return state;
  return stageCache.matchState(code);
}

// handleMatchEvent applies a match-scope SSE event — a scoped delta when ops are
// present, a full-state snapshot otherwise. Deltas reconstruct the full view by
// applying ops to the cached base, but only when they chain (prevSeq === the
// base's seq); a missing base or a seq gap can't be applied safely, so we evict
// (forcing a fresh fetch) and resync the match we're actually showing. This
// keeps the cached view correct-or-absent — a bug degrades to a refetch, never
// a wrong bracket.
function handleMatchEvent(message, matchScope) {
  if (Array.isArray(message.ops)) {
    const code = matchCodeFromScope(message.scope);
    const base = matchBase(code);
    const prev = Number(message.prevSeq) || 0;
    // Already applied: a coalesced delta whose range we fetched past on connect
    // arrives with seq <= base.seq. Ignore it instead of reloading on the gap.
    if (base && (Number(message.seq) || 0) <= (Number(base.seq) || 0)) return;
    if (!base || (Number(base.seq) || 0) !== prev) {
      stageCache.invalidateMatch(code);
      if (isDisplayed(code)) scheduleReload();
      return;
    }
    const next = gameTable.applyDeltaOps(base, message.ops);
    next.seq = Number(message.seq) || prev;
    applyMatchView(next, message.scope, matchScope);
    return;
  }
  if (message.data?.code) {
    const view = message.data;
    view.seq = Number(message.seq) || 0;
    applyMatchView(view, message.scope, matchScope);
    return;
  }
  // Match event with no usable payload — fall back to a reload.
  scheduleReload();
}

// applyMatchView warms the stage cache for any match and re-renders the focused
// match in place when the event is for the one we're viewing.
function applyMatchView(view, scope, matchScope) {
  applyReadonlyStageMatchUpdate(view);
  if (route.mode === "match" && scope === matchScope) {
    applyUpdatedMatch(view);
  }
}

// canPatchMatchTable reports whether `next` differs from `previous` only in
// scores/marks, so an existing rendered table can be patched cell-by-cell
// instead of rebuilt. Used for both the focused match view and each stage
// frame (so live updates don't tear down and re-render the whole battle).
// canPatchMatchTable: shared shape check plus the viewer's structural extras —
// the read-only table renders the venue/title in a header and place as text
// (with medal styling), so a change there needs a rebuild rather than a patch.
function canPatchMatchTable(previous, next) {
  if (!previous || !next) return false;
  if (matchTitleFor(previous) !== matchTitleFor(next)) return false;
  if (!gameTable.canPatchScoreShape(previous, next)) return false;
  const prevTeams = previous.teams || [];
  const nextTeams = next.teams || [];
  for (let i = 0; i < nextTeams.length; i++) {
    if (formatPlace(prevTeams[i].place) !== formatPlace(nextTeams[i].place)) return false;
  }
  return true;
}

function patchMatchTable(index, matchState) {
  gameTable.patchScoreTable(index, matchState, {formatNumber});
}

function resetReadonlyTableIndex() {
  readonlyTableIndex = null;
}

function viewerTabItems() {
  const items = [
    {href: route.base + "/", label: "Сетка", key: "grid"},
    {href: route.base + "/venues", label: "Площадки", key: "venues"},
  ];
  viewerStages().forEach((stage) => {
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

function renderViewerTabs() {
  if (!viewerTabsRoot || embedded || !fest) return;
  viewerTabsRoot.replaceChildren();
  const active = activeViewerTabKey();
  let activeLink = null;
  for (const item of viewerTabItems()) {
    const link = document.createElement("a");
    link.className = "match-tab" + (item.key === active ? " active" : "");
    link.href = item.href;
    link.textContent = item.label;
    link.setAttribute("role", "tab");
    link.setAttribute("aria-selected", item.key === active ? "true" : "false");
    if (item.key === active) activeLink = link;
    viewerTabsRoot.appendChild(link);
  }
  bindViewerTabsScrollFade();
  scrollActiveViewerTabIntoView(activeLink);
}

function activeViewerTabKey() {
  if (route.mode === "stage") return `stage:${route.stageCode}`;
  if (route.mode === "match") {
    const stageCode = state?.stageCode || stageCodeForMatch(route.matchCode);
    return stageCode ? `stage:${stageCode}` : "grid";
  }
  if (route.mode === "venues") return "venues";
  if (route.mode === "stats") return "stats";
  return "grid";
}

function bindViewerTabsScrollFade() {
  if (!viewerTabsRoot) return;
  if (viewerTabsRoot.dataset.scrollFadeBound !== "1") {
    viewerTabsRoot.addEventListener("scroll", scheduleViewerTabsFadeUpdate, {passive: true});
    viewerTabsRoot.dataset.scrollFadeBound = "1";
  }
  scheduleViewerTabsFadeUpdate();
}

function scrollActiveViewerTabIntoView(activeLink) {
  if (!viewerTabsRoot || !activeLink) return;
  requestAnimationFrame(() => {
    const margin = 8;
    const currentLeft = viewerTabsRoot.scrollLeft;
    const currentRight = currentLeft + viewerTabsRoot.clientWidth;
    const activeLeft = activeLink.offsetLeft;
    const activeRight = activeLeft + activeLink.offsetWidth;
    const maxScroll = Math.max(0, viewerTabsRoot.scrollWidth - viewerTabsRoot.clientWidth);
    let target = currentLeft;
    if (activeLeft < currentLeft + margin) {
      target = activeLeft - margin;
    } else if (activeRight > currentRight - margin) {
      target = activeRight - viewerTabsRoot.clientWidth + margin;
    }
    viewerTabsRoot.scrollLeft = gameTable.clamp(target, 0, maxScroll);
    scheduleViewerTabsFadeUpdate();
  });
}

function scheduleViewerTabsFadeUpdate() {
  if (!viewerTabsRoot || embedded) return;
  if (viewerTabsFadeFrame) cancelAnimationFrame(viewerTabsFadeFrame);
  viewerTabsFadeFrame = requestAnimationFrame(() => {
    viewerTabsFadeFrame = 0;
    updateViewerTabsScrollFade();
  });
}

function updateViewerTabsScrollFade() {
  if (!viewerTabsRoot) return;
  const hasLeft = viewerTabsRoot.scrollLeft > 1;
  const hasRight = viewerTabsRoot.scrollLeft + viewerTabsRoot.clientWidth < viewerTabsRoot.scrollWidth - 1;
  viewerTabsRoot.classList.toggle("tabs-scroll-left", hasLeft);
  viewerTabsRoot.classList.toggle("tabs-scroll-right", hasRight);
}

function withMatchState(matchState, callback) {
  const previousState = state;
  state = matchState;
  try {
    return callback();
  } finally {
    state = previousState;
  }
}

function buildReadonlyTable() {
  const hasShootout = shootoutThemeCount() > 0;
  const themes = readonlyThemeHeaders();
  const rows = state.teams.map((team, teamIndex) => {
    const themeCells = [];
    team.themes.forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, false));
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, true));
    });
    return {
      nameCell: readonlyTeamNameCell(team, teamIndex),
      totalCell: td(team.total, "sticky sticky-total number total-cell", {rowSpan: 2, dataset: {team: teamIndex}}),
      placeCell: td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: 2, dataset: {team: teamIndex}}),
      themes: themeCells,
      afterThemeCells: readonlyTrailingCells(team, teamIndex, hasShootout),
    };
  });

  return gameTable.buildTwoRowScoreTable({
    className: "match-table compact-score-table ek-stage-table readonly-table",
    nameHeader: {content: readonlyBattleTitleNode(state), className: "sticky sticky-name battle readonly-battle-head readonly-battle-with-popover"},
    themes,
    afterThemeHeaders: readonlyTrailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
}

function readonlyTeamNameCell(team, teamIndex) {
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {rowSpan: 2, dataset: {team: teamIndex}});
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

function scheduleReadonlyNameOverflowUpdate(root = viewerRoot) {
  if (readonlyNameOverflowFrame) cancelAnimationFrame(readonlyNameOverflowFrame);
  readonlyNameOverflowFrame = requestAnimationFrame(() => {
    readonlyNameOverflowFrame = 0;
    updateReadonlyNameOverflow(root);
  });
}

function updateReadonlyNameOverflow(root = viewerRoot) {
  const ekCells = root.querySelectorAll(".ek-team-cell");
  for (const cell of ekCells) {
    const name = cell.querySelector(".od-detailed-team-name");
    const truncated = gameTable.fitEKStageTeamName(cell, name);
    cell.classList.toggle("od-detailed-team-cell-truncated", truncated);
  }
  const playerCells = root.querySelectorAll(".readonly-player");
  for (const cell of playerCells) {
    const text = cell.querySelector(".readonly-player-text");
    const truncated = Boolean(text && text.scrollWidth > text.clientWidth + 1);
    cell.classList.toggle("readonly-player-cell-truncated", truncated);
  }
  const resultsCells = root.querySelectorAll(".results-team");
  for (const cell of resultsCells) {
    const name = cell.querySelector(".results-team-name");
    cell.classList.toggle("results-team-truncated", Boolean(name && name.scrollWidth > name.clientWidth + 1));
  }
}

function readonlyThemeHeaders() {
  const themes = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({label: `Т${theme + 1}`, questionLabels: state.questionValues});
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: `П${theme + 1}`,
      questionLabels: state.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
    });
  }
  return themes;
}

function readonlyTrailingHeaders(hasShootout) {
  const headers = [];
  if (hasShootout) headers.push({content: "П", className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function readonlyThemeCells(teamIndex, theme, themeIndex, isShootout) {
  const playerCell = document.createElement("td");
  playerCell.colSpan = state.questionValues.length;
  playerCell.className = "readonly-player theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }
  const playerLabel = theme.player || "";
  const playerWrap = document.createElement("span");
  playerWrap.className = "readonly-player-text-wrap";
  const playerText = document.createElement("span");
  playerText.className = "readonly-player-text";
  playerText.textContent = playerLabel;
  playerWrap.appendChild(playerText);
  playerCell.appendChild(playerWrap);
  if (playerLabel) {
    const playerPopover = document.createElement("span");
    playerPopover.className = "readonly-player-popover";
    playerPopover.textContent = playerLabel;
    playerCell.appendChild(playerPopover);
  }
  const answers = theme.answers.map((mark, answerIndex) => {
    const className = answerIndex === 0
      ? `answer-cell theme-block theme-block-bottom-left ${mark}`
      : `answer-cell theme-block ${mark}`;
    const cell = td("", className);
    cell.dataset.team = String(teamIndex);
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    if (isShootout) {
      cell.classList.add("shootout-block");
    }
    return cell;
  });
  return {
    playerCell,
    scoreCell: td(theme.score, "number theme-score theme-block theme-block-score", {
      rowSpan: 2,
      dataset: {team: teamIndex, shootout: isShootout ? "1" : "0", theme: themeIndex},
    }),
    answers,
  };
}

function readonlyTrailingCells(team, teamIndex, hasShootout) {
  const cells = [];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    cells.push(td(shootoutTotal, "number tiebreak-cell", {rowSpan: 2, dataset: {team: teamIndex}}));
  }
  cells.push(td(team.plus, "number plus-cell", {rowSpan: 2, dataset: {team: teamIndex}}));
  [0, 1, 2, 3, 4].forEach((idx) => {
    cells.push(td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {
      rowSpan: 2,
      dataset: {team: teamIndex, valueIndex: idx},
    }));
  });
  return cells;
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

function currentRoute() {
  const path = window.location.pathname;
  const prefix = path.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const festID = prefix[1];
  const gameID = prefix[2];
  const base = `/fest/${festID}/game/${gameID}`;
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  const rest = path.slice(prefix[0].length);
  const stripped = rest.replace(/\/$/, "");
  if (stripped === "" || stripped === "/") {
    return {mode: "grid", festID, gameID, base, apiBase};
  }
  if (stripped === "/venues") return {mode: "venues", festID, gameID, base, apiBase};
  if (stripped === "/stats") return {mode: "stats", festID, gameID, base, apiBase};
  const match = stripped.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), festID, gameID, base, apiBase};
  const stage = stripped.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), festID, gameID, base, apiBase};
  return {mode: "missing"};
}

function viewerStages() {
  const scheme = parseScheme(fest?.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : fest?.stages || [];
  return stages;
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

function stageCodeForMatch(matchCode) {
  if (!matchCode) return "";
  for (const stage of viewerStages()) {
    if ((stage.matches || []).some((match) => match.code === matchCode)) return stage.code;
  }
  return "";
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
  const gameTitle = currentGameTitle() || "ЭК";
  gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
    festHref: `/fest/${route.festID}`,
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
  if (route.mode === "match") return state?.title || route.matchCode || "";
  if (route.mode === "stage") return findStage(fest, route.stageCode)?.title || route.stageCode || "";
  return gameTitle;
}

function setViewerMode(mode) {
  viewerRoot.classList.toggle("grid-host", mode === "grid");
  viewerRoot.classList.toggle("fight-host", mode === "match");
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

function readonlyBattleTitleNode(matchState) {
  const fullLabel = matchTitleFor(matchState);
  const title = document.createElement("span");
  title.className = "readonly-battle-title";
  title.tabIndex = 0;
  title.setAttribute("aria-label", fullLabel);
  title.title = fullLabel;

  const battle = document.createElement("span");
  battle.className = "readonly-battle-name";
  battle.textContent = matchState?.title || "";
  title.appendChild(battle);

  if (matchState?.venue) {
    const venueLabel = formatBattleVenueShort(matchState.venue);
    if (venueLabel) {
      const venue = document.createElement("span");
      venue.className = "readonly-battle-venue";
      venue.textContent = venueLabel;
      title.appendChild(venue);
    }
  }

  const popover = document.createElement("span");
  popover.className = "readonly-battle-popover";
  popover.textContent = fullLabel;
  title.appendChild(popover);

  return title;
}

function matchTitleFor(matchState) {
  const venueLabel = formatBattleVenue(matchState?.venue);
  const venue = venueLabel ? ` · ${venueLabel}` : "";
  return `${matchState?.title || ""}${venue}`;
}

bindViewerSPANavigation();
bindStageScrollFade();
loadCurrent()
  .then(() => {
    setLive(true);
    connectEvents();
  })
  .catch((error) => {
    setLive(false);
    console.error(error);
  });
