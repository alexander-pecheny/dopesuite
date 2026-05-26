const viewerRoot = document.getElementById("viewerTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerTabsRoot = document.getElementById("viewerTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const {formatVenue, formatBattleVenue, formatBattleVenueShort, statusLabel, formatNumber, formatPlace, cssEscape, th, td} = gameTable;
let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
const canEdit = Boolean(window.__VIEWER_INIT__?.canEdit);
const editorLink = canEdit && !embedded ? gameTable.mountEditorLink(statusNode) : null;
let state = null;
let fest = null;
let venues = [];
// Per-stage caches. stageDataByCode owns the live MatchView for every match
// in every stage; stagePaneByCode owns the built DOM. SSE match-update events
// land in stageDataByCode and patch the matching pane in place, so tab
// switches reduce to toggling `hidden` on already-rendered DOM.
const stageDataByCode = new Map();      // stageCode -> {matches: [], stateByCode: Map<code, MatchView>}
const stagePaneByCode = new Map();      // stageCode -> HTMLElement (pane root mounted in viewerRoot)
const stageFetchPromises = new Map();   // stageCode -> in-flight prefetch Promise
const matchCodeToStageCode = new Map(); // matchCode -> stageCode (SSE routing)
let stageCachesRevision = null;         // fest.revision the caches were built against
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
  // Stage caches are tied to a specific fest revision. A revision bump means
  // the stage list or match metadata may have changed under us; drop caches
  // so we rebuild against the new shape.
  if (stageCachesRevision != null && stageCachesRevision !== view?.revision) {
    clearStageCaches();
  }
  if (view?.revision != null) stageCachesRevision = view.revision;
  indexAllStages();
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
  const stagePromise = prefetchStageMatches(stageCode);
  await Promise.all([festPromise, stagePromise]);
  if (route.mode !== "stage" || route.stageCode !== stageCode) return;
  renderStage();
  // Background prefetch of every other stage. Each payload is <10KB and
  // makes subsequent tab switches instant (cache hit + pane already built).
  prefetchAllStages();
}

// indexAllStages walks the scheme stages and seeds stageDataByCode +
// matchCodeToStageCode for every stage. Done eagerly so SSE match-update
// events can find their stage even for stages we haven't fetched yet.
function indexAllStages() {
  if (!fest) return;
  for (const stage of viewerStages()) {
    ensureStageDataShape(stage.code);
  }
}

function ensureStageDataShape(stageCode) {
  let data = stageDataByCode.get(stageCode);
  const stage = findStage(fest, stageCode);
  const matches = stage?.matches || [];
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

function prefetchStageMatches(stageCode) {
  if (!stageCode) return Promise.resolve();
  const inflight = stageFetchPromises.get(stageCode);
  if (inflight) return inflight;
  const url = `${route.apiBase}/stages/${encodeURIComponent(stageCode)}/matches`;
  const promise = fetch(url)
    .then(async (response) => {
      if (!response.ok) throw new Error(await response.text());
      return response.json();
    })
    .then((batchedMatches) => {
      const data = ensureStageDataShape(stageCode);
      if (Array.isArray(batchedMatches)) {
        for (const m of batchedMatches) {
          if (m?.code) data.stateByCode.set(m.code, m);
        }
      }
      // If this stage is already on screen (cached pane mounted and not
      // hidden, or pane built but waiting), repaint its frames in place.
      if (stagePaneByCode.has(stageCode)) repaintStagePane(stageCode);
    })
    .catch((err) => {
      console.error("prefetch stage failed", stageCode, err);
      stageFetchPromises.delete(stageCode); // allow retry on next visit
      throw err;
    });
  stageFetchPromises.set(stageCode, promise);
  return promise;
}

function prefetchAllStages() {
  if (!fest) return;
  for (const stage of viewerStages()) {
    if (stageType(stage) === "reseed") continue; // no per-match data needed
    prefetchStageMatches(stage.code).catch(() => {});
  }
}

function clearStageCaches() {
  for (const pane of stagePaneByCode.values()) pane.remove();
  stageDataByCode.clear();
  stagePaneByCode.clear();
  stageFetchPromises.clear();
  matchCodeToStageCode.clear();
  stageCachesRevision = null;
}

function repaintStagePane(stageCode) {
  const pane = stagePaneByCode.get(stageCode);
  const data = stageDataByCode.get(stageCode);
  if (!pane || !data) return;
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
    frame.replaceChildren(withMatchState(matchState, () => buildReadonlyTable()));
    return;
  }
  const placeholder = document.createElement("div");
  placeholder.className = "stage-match-placeholder";
  placeholder.textContent = descriptor?.title || `Бой ${descriptor?.code || ""}`;
  frame.replaceChildren(placeholder);
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

function connectEvents() {
  const events = new EventSource(`/events?fest_id=${encodeURIComponent(route.festID)}`);
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    const matchScope = `match:${route.gameID}:${route.matchCode}`;
    const venuesScope = `venues:${route.festID}`;
    // Always update cached stage state for any match-scoped event, regardless
    // of which page we're on. Keeps cached panes for other stages live so a
    // later tab switch sees fresh data without a fetch.
    if (message.scope?.startsWith("match:")) {
      if (message.data?.code) {
        applyReadonlyStageMatchUpdate(message.data);
      }
      if (route.mode === "match" && message.scope === matchScope) {
        applyUpdatedMatch(message.data);
      }
      setLive(true);
      if (!message.data?.code) scheduleReload();
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setLive(true);
      return;
    }
    scheduleReload();
  });
  events.onerror = () => setLive(false);
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
  ensureStageDataShape(stageCode);
  let pane = stagePaneByCode.get(stageCode);
  if (!pane) {
    pane = buildStagePane(stageCode);
  }
  // Sync viewerRoot's children to the cached panes without churning DOM:
  // if a previous render put non-pane content here (renderFest/renderVenues/
  // render via replaceChildren), wipe it; then attach any cached panes that
  // aren't already mounted, and toggle hidden so only the active one shows.
  for (const node of Array.from(viewerRoot.children)) {
    if (!stagePaneByCode.has(node.dataset?.stageCode)) node.remove();
  }
  for (const [code, p] of stagePaneByCode) {
    if (!p.isConnected) viewerRoot.appendChild(p);
    p.hidden = code !== stageCode;
  }
  scheduleReadonlyNameOverflowUpdate(pane);
}

function buildStagePane(stageCode) {
  const stage = mergedStage(fest, stageCode);
  const pane = document.createElement("div");
  pane.className = "stage-pane";
  pane.dataset.stageCode = stageCode;
  if (stageType(stage) === "reseed") {
    pane.appendChild(buildReseedStagePanel(stage));
  } else {
    pane.appendChild(buildReadonlyStageTablesFor(stageCode));
  }
  stagePaneByCode.set(stageCode, pane);
  return pane;
}

function buildReadonlyStageTablesFor(stageCode) {
  const data = ensureStageDataShape(stageCode);
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
}

function applyUpdatedMatch(updated) {
  const previous = state;
  state = updated;
  if (canPatchReadonlyMatchTable(previous, updated)) {
    patchReadonlyMatchTable();
    return;
  }
  render();
}

function applyReadonlyStageMatchUpdate(updated) {
  if (!updated?.code) return;
  const stageCode = matchCodeToStageCode.get(updated.code);
  if (!stageCode) {
    // Match not in any known stage — fest scheme probably changed.
    scheduleReload();
    return;
  }
  const data = ensureStageDataShape(stageCode);
  data.stateByCode.set(updated.code, updated);
  const pane = stagePaneByCode.get(stageCode);
  if (!pane) return; // pane not built yet; will pick up the update when built
  const frame = pane.querySelector(`.stage-match-frame[data-match-code="${cssEscape(updated.code)}"]`);
  if (!frame) return;
  const descriptor = data.matches.find((m) => m.code === updated.code);
  paintStageFrame(frame, updated, descriptor);
  if (pane.isConnected && !pane.hidden) scheduleReadonlyNameOverflowUpdate(frame);
}

function canPatchReadonlyMatchTable(previous, next) {
  if (route.mode !== "match" || !readonlyTableIndex || !previous || !next) return false;
  if (previous.code !== next.code || previous.title !== next.title || previous.finished !== next.finished) return false;
  if (matchTitleFor(previous) !== matchTitleFor(next)) return false;
  if (!gameTable.sameArray(previous.questionValues, next.questionValues)) return false;
  if ((previous.teams || []).length !== (next.teams || []).length) return false;
  for (let i = 0; i < next.teams.length; i++) {
    const prevTeam = previous.teams[i];
    const nextTeam = next.teams[i];
    if (prevTeam.name !== nextTeam.name || formatPlace(prevTeam.place) !== formatPlace(nextTeam.place)) return false;
    if ((prevTeam.themes || []).length !== (nextTeam.themes || []).length) return false;
    if (shootoutThemesFor(prevTeam).length !== shootoutThemesFor(nextTeam).length) return false;
  }
  return true;
}

function patchReadonlyMatchTable() {
  state.teams.forEach((team, teamIndex) => {
    setIndexedText("total", {team: teamIndex}, team.total);
    setIndexedText("plus", {team: teamIndex}, team.plus);
    setIndexedText("tiebreak", {team: teamIndex}, team.shootoutTotal ?? team.tiebreak);
    [0, 1, 2, 3, 4].forEach((idx) => {
      setIndexedText("correctCount", {team: teamIndex, valueIndex: idx}, team.correctCounts[4 - idx]);
    });
    team.themes.forEach((theme, themeIndex) => {
      patchReadonlyTheme(teamIndex, themeIndex, false, theme);
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      patchReadonlyTheme(teamIndex, themeIndex, true, theme);
    });
  });
}

function patchReadonlyTheme(teamIndex, themeIndex, isShootout, theme) {
  const shootout = isShootout ? "1" : "0";
  setIndexedText("themeScore", {team: teamIndex, shootout, theme: themeIndex}, theme.score);
  theme.answers.forEach((mark, answerIndex) => {
    const cell = readonlyTableIndex?.get("answer", {team: teamIndex, shootout, theme: themeIndex, answer: answerIndex});
    gameTable.setMarkClass(cell, mark);
  });
}

function setIndexedText(name, values, value) {
  const node = readonlyTableIndex?.get(name, values);
  if (node) gameTable.setNodeText(node, value, formatNumber);
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
loadCurrent()
  .then(() => {
    setLive(true);
    connectEvents();
  })
  .catch((error) => {
    setLive(false);
    console.error(error);
  });
