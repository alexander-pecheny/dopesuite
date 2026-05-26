const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerLink = document.querySelector(".viewer-link");
const ekTabsRoot = document.getElementById("ekTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const {formatVenue, formatBattleVenue, statusLabel, formatNumber, formatPlace, sameArray, clamp, cssEscape, th, td} = gameTable;
let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let fest = null;
let venues = [];
let stageMatches = [];
let stageStates = [];
let stageStateByCode = new Map();
let stageLoadToken = 0;
let stageTableObserver = null;
let renderMatchCode = null;
let activeCell = {matchCode: "", team: 0, shootout: false, theme: 0, answer: 0};
let reloadTimer = null;
const localMatchEchoes = new Set();
let matchTableIndex = null;
let activeAnswerNode = null;
let activeTeamRows = [];
const matchSelections = new Map();
let stageSelection = null;
let presence = null;
let seedImport = null;
let seedImportNotice = "";
let gridNameOverflowFrame = 0;
let ekTeamNameOverflowFrame = 0;
let resultsTeamNameOverflowFrame = 0;
let stageOverflowScrollFrame = null;
let stageOverflowScrollListener = null;
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
  if (route.mode === "seedImport") scheduleResultsTeamNameOverflowUpdate();
  floatingPopover.position();
  scheduleEKTabsFadeUpdate();
});

async function loadCurrent() {
  if (consumeHostInit()) return;
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else if (route.mode === "seedImport") {
    await loadSeedImportPage();
  } else {
    await loadFest();
  }
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
  venues = Array.isArray(view?.venues) ? view.venues : [];
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
  const token = ++stageLoadToken;
  const cached = hydrateFestFromCache();
  if (cached) {
    const stage = findStage(fest, route.stageCode);
    stageMatches = stage?.matches || [];
    stageStates = [];
    stageStateByCode = new Map();
    renderStage();
  }
  const [response, venuesResponse, batchResponse] = await Promise.all([
    fetch(route.apiBase),
    fetch(`${route.festApi}/venues`),
    fetch(`${route.apiBase}/stages/${encodeURIComponent(route.stageCode)}/matches`),
  ]);
  if (!response.ok) throw new Error(await response.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!batchResponse.ok) throw new Error(await batchResponse.text());
  const fresh = await response.json();
  const freshVenues = await venuesResponse.json();
  const batchedMatches = await batchResponse.json();
  if (token !== stageLoadToken || route.mode !== "stage") return;
  const changed = !cached || fresh.revision !== fest?.revision;
  fest = fresh;
  venues = freshVenues;
  writeFestCache(fresh);
  if (changed) {
    const stage = findStage(fest, route.stageCode);
    stageMatches = stage?.matches || [];
    stageStates = [];
    stageStateByCode = new Map();
  }
  if (Array.isArray(batchedMatches)) {
    for (const m of batchedMatches) {
      if (m?.code) stageStateByCode.set(m.code, m);
    }
    stageStates = stageMatches.map((match) => stageStateByCode.get(match.code)).filter(Boolean);
  }
  // Re-render so rendered placeholder frames pick up the freshly-batched
  // state immediately. Frames not yet rendered will hit the populated map
  // the moment the IntersectionObserver fires for them.
  renderStage();
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
  state = await matchResponse.json();
  venues = await venuesResponse.json();
  fest = await festResponse.json();
  writeFestCache(fest);
  render();
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
  fest = freshFest;
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
  fest = await festResponse.json();
  writeFestCache(fest);
  renderSeedImport();
}

function connectEvents() {
  const events = new EventSource(`/events?fest_id=${encodeURIComponent(route.festID)}`);
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    if (consumeLocalMatchEcho(message)) {
      setStatus("saved");
      return;
    }
    const matchScope = `match:${route.gameID}:${route.matchCode}`;
    const venuesScope = `venues:${route.festID}`;
    if (route.mode === "match" && message.scope === matchScope) {
      applyUpdatedMatch(message.data, route.matchCode);
      setStatus("saved");
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setStatus("saved");
      return;
    }
    if (route.mode === "stage" && message.scope.startsWith("match:")) {
      if (message.data?.code) {
        applyStageMatchUpdate(message.data);
        setStatus("saved");
      } else {
        scheduleReload();
      }
      return;
    }
    scheduleReload();
  });
  events.onerror = () => setStatus("reconnecting");
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

async function sendUpdate(payload, matchCode = currentMatchCode()) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(matchCode)}/update`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
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

function renderStage(options = {}) {
  if (!fest) return;
  resetMatchTableIndex();
  const stage = mergedStage(fest, route.stageCode);
  const scrollFrame = hostRoot.closest(".sheet-frame");
  const scrollTop = scrollFrame?.scrollTop || 0;
  const scrollLeft = scrollFrame?.scrollLeft || 0;
  setHostMode("grid");
  setHeading("ЭК");
  setViewerLink(`${route.viewerBase}/stage/${encodeURIComponent(route.stageCode)}`, "Открыть этап для зрителя");
  document.title = pageTitle();
  renderEKTabs();
  if (stageType(stage) === "reseed") {
    hostRoot.replaceChildren(buildReseedStagePanel(stage));
    scheduleResultsTeamNameOverflowUpdate();
  } else {
    hostRoot.replaceChildren(buildStageTables());
    bindStageOverflowScroll();
    setupStageTableObserver();
    attachStageSelection(hostRoot.querySelector(".stage-table-stack"));
  }
  if (options.preserveScroll && scrollFrame) {
    scrollFrame.scrollTop = scrollTop;
    scrollFrame.scrollLeft = scrollLeft;
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
  if (!scrollFrame || stageOverflowScrollFrame === scrollFrame) return;
  unbindStageOverflowScroll();
  stageOverflowScrollListener = () => scheduleEKTeamNameOverflowUpdate(hostRoot);
  scrollFrame.addEventListener("scroll", stageOverflowScrollListener, {passive: true});
  stageOverflowScrollFrame = scrollFrame;
}

function unbindStageOverflowScroll() {
  if (!stageOverflowScrollFrame || !stageOverflowScrollListener) return;
  stageOverflowScrollFrame.removeEventListener("scroll", stageOverflowScrollListener);
  stageOverflowScrollFrame = null;
  stageOverflowScrollListener = null;
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

function buildStageTables() {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack stage-table-stack-lazy";
  if (stageMatches.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "В этом этапе нет боёв.";
    wrapper.appendChild(empty);
    return wrapper;
  }
  stageMatches.forEach((match) => {
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

function setupStageTableObserver() {
  disconnectStageTableObserver();
  const frames = Array.from(hostRoot.querySelectorAll(".stage-match-frame"));
  if (frames.length === 0) return;
  if (!("IntersectionObserver" in window)) {
    renderStageMatchFrames(frames, {force: true});
    return;
  }
  // After the batched /stages/{code}/matches fetch lands, the data for every
  // frame is in stageStateByCode. The observer's only job now is to defer
  // DOM construction for off-screen tables.
  const root = hostRoot.closest(".sheet-frame");
  stageTableObserver = new IntersectionObserver((entries) => {
    const visibleFrames = [];
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return;
      const frame = entry.target;
      frame.dataset.nearViewport = "1";
      visibleFrames.push(frame);
    });
    renderStageMatchFrames(visibleFrames);
    visibleFrames.forEach((frame) => {
      if (frame.dataset.rendered === "1") stageTableObserver?.unobserve(frame);
    });
  }, {root, rootMargin: "900px 0px"});
  frames.forEach((frame) => stageTableObserver.observe(frame));
}

function disconnectStageTableObserver() {
  if (!stageTableObserver) return;
  stageTableObserver.disconnect();
  stageTableObserver = null;
}

function renderStageMatchFrames(frames, options = {}) {
  let rendered = false;
  frames.forEach((frame) => {
    rendered = renderStageMatchFrame(frame, options) || rendered;
  });
  if (rendered) scheduleEKTeamNameOverflowUpdate(hostRoot);
}

function renderStageMatchFrame(frame, options = {}) {
  if (!frame || (!options.force && frame.dataset.rendered === "1")) return false;
  const matchState = stageStateByCode.get(frame.dataset.matchCode || "");
  if (!matchState) return false;
  const hadFocus = document.activeElement?.closest?.(".stage-match-frame") === frame;
  frame.dataset.rendered = "1";
  const stageTable = withMatchState(matchState, () => buildTable({compact: true}));
  frame.replaceChildren(stageTable);
  stageSelection?.refresh();
  if (hadFocus && activeCell.matchCode === matchState.code) {
    focusActiveCell({preventScroll: true});
  }
  return true;
}

function renderStageMatchFrameIfReady(matchCode, options = {}) {
  const frame = stageMatchFrame(matchCode);
  if (!frame) return;
  if (options.force || frame.dataset.nearViewport === "1" || frame.dataset.rendered === "1") {
    renderStageMatchFrames([frame], options);
  }
}

function stageMatchFrame(matchCode) {
  return hostRoot.querySelector(`.stage-match-frame[data-match-code="${cssEscape(matchCode)}"]`);
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

function activeMatchState() {
  if (route.mode === "stage") {
    return stageStateByCode.get(activeCell.matchCode) || stageStates[0] || null;
  }
  return state;
}

function applyStageMatchUpdate(updated, options = {}) {
  const matchCode = updated?.code;
  if (!matchCode) return;
  stageStateByCode.set(matchCode, updated);
  stageStates = stageMatches.map((match) => stageStateByCode.get(match.code)).filter(Boolean);
  renderStageMatchFrameIfReady(matchCode, {
    force: !options.renderOnlyIfNear && stageMatchFrame(matchCode)?.dataset.rendered === "1",
  });
}

function applyUpdatedMatch(updated, matchCode) {
  if (route.mode === "stage") {
    applyStageMatchUpdate(updated);
    return;
  }
  const previous = state;
  state = updated;
  if (canPatchMatchTable(previous, updated)) {
    normalizeActiveCell();
    patchMatchTable(matchCode);
    return;
  }
  render();
}

function canPatchMatchTable(previous, next) {
  if (route.mode !== "match" || !matchTableIndex || !previous || !next) return false;
  if (previous.code !== next.code || previous.title !== next.title || previous.finished !== next.finished) return false;
  if (formatVenue(previous.venue) !== formatVenue(next.venue)) return false;
  if (!sameArray(previous.questionValues, next.questionValues)) return false;
  if ((previous.teams || []).length !== (next.teams || []).length) return false;
  for (let i = 0; i < next.teams.length; i++) {
    const prevTeam = previous.teams[i];
    const nextTeam = next.teams[i];
    if (prevTeam.name !== nextTeam.name) return false;
    if ((prevTeam.themes || []).length !== (nextTeam.themes || []).length) return false;
    if (shootoutThemesFor(prevTeam).length !== shootoutThemesFor(nextTeam).length) return false;
  }
  return true;
}

function patchMatchTable(matchCode) {
  state.teams.forEach((team, teamIndex) => {
    setIndexedText("total", {team: teamIndex}, team.total);
    setIndexedText("plus", {team: teamIndex}, team.plus);
    setIndexedText("tiebreak", {team: teamIndex}, team.shootoutTotal ?? team.tiebreak);
    const placeInput = indexedNode("placeInput", {team: teamIndex}) ||
      document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"]`);
    if (placeInput && document.activeElement !== placeInput) {
      placeInput.value = formatPlace(team.place);
    }
    if (placeInput) {
      placeInput.dataset.committedPlace = String(team.place || 0);
    }
    [0, 1, 2, 3, 4].forEach((idx) => {
      setIndexedText("correctCount", {team: teamIndex, valueIndex: idx}, team.correctCounts[4 - idx]);
    });
    team.themes.forEach((theme, themeIndex) => {
      patchTheme(teamIndex, themeIndex, false, theme, matchCode);
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      patchTheme(teamIndex, themeIndex, true, theme, matchCode);
    });
  });
  markActiveCell();
}

function patchTheme(teamIndex, themeIndex, isShootout, theme, matchCode) {
  const shootoutValue = isShootout ? "1" : "0";
  const select = indexedNode("playerSelect", {team: teamIndex, shootout: shootoutValue, theme: themeIndex}) ||
    document.querySelector(`.player-select[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"][data-shootout="${shootoutValue}"][data-theme="${themeIndex}"]`);
  if (select && document.activeElement !== select) {
    if (theme.player && !Array.from(select.options).some((item) => item.value === theme.player)) {
      select.appendChild(option(theme.player, theme.player));
    }
    select.value = theme.player || "";
  }
  updatePlayerSelectOverflow(select?.closest(".player-select-wrap") || hostRoot);
  setIndexedText("themeScore", {team: teamIndex, shootout: shootoutValue, theme: themeIndex}, theme.score);
  theme.answers.forEach((mark, answerIndex) => {
    const cell = indexedNode("answer", {team: teamIndex, shootout: shootoutValue, theme: themeIndex, answer: answerIndex}) ||
      document.querySelector(`.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"][data-shootout="${shootoutValue}"][data-theme="${themeIndex}"][data-answer="${answerIndex}"]`);
    gameTable.setMarkClass(cell, mark);
  });
}

function setIndexedText(name, values, value) {
  const node = indexedNode(name, values);
  if (node) gameTable.setNodeText(node, value, formatNumber);
}

function indexedNode(name, values) {
  if (route.mode !== "match") return null;
  return matchTableIndex?.get(name, values) || null;
}

function resetMatchTableIndex() {
  disconnectStageTableObserver();
  unbindStageOverflowScroll();
  matchTableIndex = null;
  activeAnswerNode = null;
  clearActiveTeamRows();
  for (const helper of matchSelections.values()) helper.unbind();
  matchSelections.clear();
  if (stageSelection) {
    stageSelection.unbind();
    stageSelection = null;
  }
}

function stageRowOffset(matchIndex) {
  let offset = 0;
  for (let i = 0; i < matchIndex && i < stageMatches.length; i++) {
    const s = stageStateByCode.get(stageMatches[i]?.code);
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
  const matchIndex = stageMatches.findIndex((m) => m.code === matchCode);
  if (matchIndex < 0) return null;
  const matchState = stageStateByCode.get(matchCode);
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
  for (const match of stageMatches) {
    const matchState = stageStateByCode.get(match.code);
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
  for (const {cell, value} of edits) {
    const matchCode = cell.dataset.matchCode;
    if (!matchCode) continue;
    const matchState = stageStateByCode.get(matchCode);
    if (!matchState || matchState.finished) continue;
    ekApplyValues(matchCode, matchState, [{cell, value}]);
  }
}

function attachStageSelection(container) {
  if (stageSelection) {
    stageSelection.unbind();
    stageSelection = null;
  }
  if (!container) return null;
  stageSelection = gameTable.createCellRangeSelection({
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
  stageSelection.bind();
  return stageSelection;
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

function ekApplyValues(matchCode, matchState, edits) {
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
    sendUpdate(payload, matchCode);
  }
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
    const matchIndex = stageMatches.findIndex((m) => m.code === matchCode);
    if (matchIndex < 0) return null;
    const matchState = stageStateByCode.get(matchCode);
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
    sendUpdate({team: teamIndex, place}, matchCode);
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
    sendUpdate(payload, matchCode);
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
    sendUpdate({finished: checkbox.checked}, matchCode);
  });
  label.append(checkbox);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function openVenueDialog(matchCode) {
  const dialog = document.createElement("dialog");
  dialog.className = "venue-dialog";
  const form = document.createElement("form");
  form.className = "venue-dialog-form";

  const title = document.createElement("h2");
  title.textContent = state.title || matchTitle();
  form.appendChild(title);

  const select = document.createElement("select");
  select.className = "venue-dialog-select";
  venues.forEach((venue) => {
    select.appendChild(option(String(venue.number), `${venue.number}: ${venue.title}`));
  });
  select.value = state.venue ? String(state.venue.number) : "";
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
    if (number > 0 && number !== state.venue?.number) {
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
    activeCell = {matchCode, team: 0, shootout: true, theme: shootoutThemeCount(), answer: 0};
    sendUpdate({action: "addShootoutTheme"}, matchCode);
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
      removeLastShootoutTheme(matchCode);
    });
    node.appendChild(deleteButton);
  }

  return node;
}

function handleGlobalKeydown(event) {
  if ((route.mode !== "match" && route.mode !== "stage") || isFormControl(event.target)) return;
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
    const siblingState = stageStateByCode.get(nextMatchCode);
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
  const index = stageMatches.findIndex((match) => match.code === matchCode);
  if (index < 0) return null;
  const targetIndex = index + direction;
  const targetMatch = stageMatches[targetIndex];
  if (!targetMatch) return null;
  const targetState = stageStateByCode.get(targetMatch.code);
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
  const payload = {
    team: activeCell.team,
    theme: activeCell.theme,
    answer: activeCell.answer,
    mark,
  };
  if (activeCell.shootout) payload.shootout = true;
  sendUpdate(payload, currentMatchCode());
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
  const gameTitle = currentGameTitle() || "ЭК";
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

function matchTitle() {
  const venue = state.venue ? ` · ${formatBattleVenue(state.venue)}` : "";
  return `${state.title}${venue}`;
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
