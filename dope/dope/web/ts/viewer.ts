// The EK spectator page (ADR-0001): read-only stages/venues/stats views with
// floating popovers. Converted from the legacy viewer.js; a self-booting
// side-effect module bundled by pages/viewer.ts.

import {DopeTable} from "./match-table.js";
import type {
  CellSpec,
  ClientRecorder,
  EKStage,
  NodeIndex,
  ScopedEventMessage,
  ScrollEdgeBinding,
  StageRef,
  ScoreTableTheme,
  ScoreTableThemeRow,
  ParticipantView,
  ThemeView,
  Venue,
  VenueLike,
} from "./match-table.js";
import {createStageCache} from "./stage-cache.js";
import type {MatchView as CachedMatchView, StageData} from "./stage-cache.js";
import {DopeStatsSync} from "./stats-sync.js";
import type {StatsMatchEvent, StatsSyncGameTable} from "./stats-sync.js";
import {buildFestGrid, buildReseedStagePanel, parseScheme} from "./fest-grid.js";
import {computeGroupRounds} from "./group-stats.js";
import type {FestGridMatch, FestGridStage, FestScheme} from "./fest-grid.js";

interface FestView {
  revision?: unknown;
  schemaJson?: unknown;
  stages?: FestGridStage[];
  venues?: Venue[];
  title?: string;
  gameName?: string;
  gameType?: string;
  [key: string]: unknown;
}

type ViewerTheme = Omit<ThemeView, "answers"> & {answers: Array<string | null | undefined>};

type ViewerParticipant = Omit<ParticipantView, "themes" | "shootoutThemes" | "correctCounts"> & {
  themes: ViewerTheme[];
  shootoutThemes?: ViewerTheme[];
  correctCounts: number[];
};

type ViewerMatchView = {
  code: string;
  seq?: number;
  title?: string;
  stageCode?: string;
  venue?: VenueLike;
  finished?: boolean;
  questionValues: Array<string | number>;
  participants: ViewerParticipant[];
  [key: string]: unknown;
};

type ViewerStageMatch = FestGridMatch & {[key: string]: unknown};

type ViewerStage = Omit<FestGridStage, "code" | "matches"> & {code: string; matches?: ViewerStageMatch[]; [key: string]: unknown};

type ViewerRoute = {
  mode: string;
  festID?: string;
  gameID?: string;
  base?: string;
  apiBase?: string;
  matchCode?: string;
  stageCode?: string;
};

// Stage frames carry their rendered match state / score index as expando
// properties, so a live update can patch the existing table in place.
type StageFrameElement = HTMLElement & {
  __matchState?: ViewerMatchView | null;
  __scoreIndex?: NodeIndex | null;
};

// Page globals the bundle environment provides (the server-inlined
// __VIEWER_INIT__). Accessed via a structural cast, same as match-table.ts.
interface ViewerInit {
  canEdit?: boolean;
  static?: boolean;
  route?: {mode?: string; matchCode?: string; stageCode?: string; gameID?: unknown};
  fest?: FestView | null;
  venues?: Venue[];
  match?: ViewerMatchView | null;
  [key: string]: unknown;
}

const pageWindow = window as Window & {__VIEWER_INIT__?: ViewerInit | null};

const viewerRoot = document.getElementById("viewerTable")!;
DopeTable.fitScrollFade(viewerRoot.closest(".sheet-frame"));
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector<HTMLElement>(".host-top h1");
const viewerTabsRoot = document.getElementById("viewerTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);
const {formatVenue, formatBattleVenue, formatBattleVenueShort, statusLabel, formatNumber, formatPlace, cssEscape, th, td} = gameTable;
let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
const canEdit = Boolean(pageWindow.__VIEWER_INIT__?.canEdit);
// staticMode: served as a precomputed snapshot under DDoS lockdown. Skip the SSE
// connection entirely and refresh by reloading the page on a jitter. Captured at
// load time because consumeViewerInit nulls window.__VIEWER_INIT__.
const staticMode = Boolean(pageWindow.__VIEWER_INIT__?.static);
// The server scopes SSE events by NUMERIC game id (`match:<id>:<code>`), but the
// URL only carries the game slug. Take the numeric id from the inlined init so
// match-scope comparisons match and the focused match patches in place.
const scopeGameID = pageWindow.__VIEWER_INIT__?.route?.gameID != null
  ? String(pageWindow.__VIEWER_INIT__.route.gameID)
  : route.gameID;
const editorLink = canEdit && !embedded ? gameTable.mountEditorLink() : null;
if (!embedded && route.apiBase) gameTable.mountGameDownloads({apiBase: route.apiBase, canEdit});
let state: ViewerMatchView | null = null;
let recorder: ClientRecorder | null = null;
// Live SSE stream, kept at module scope so the visibility/online recovery below
// can tear down a dead connection and re-establish it. null while disconnected.
let fest: FestView | null = null;

// Личная СИ runs on this page for its bracket, not its blank: a seat of one
// player has no per-theme player cell, so it takes one row (КСИ-shaped) where
// a team's takes two.
function individualGame(): boolean {
  return fest?.gameType === "si";
}

function seatRowSpan(): number {
  return individualGame() ? 1 : 2;
}
let venues: Venue[] = [];

// A fest with no столы answers `null`, not `[]` — normalise wherever it lands.
function venueList(raw: unknown): Venue[] {
  return Array.isArray(raw) ? raw as Venue[] : [];
}
const stageCache = createStageCache({
  container: viewerRoot,
  apiBase: () => route.apiBase!,
  schemeStages: () => (fest ? viewerStages() : []),
  findStage: (code) => viewerStages().find((stage) => stage.code === code) || findStage(fest!, code),
  stageType: (stage) => stageType(stage as ViewerStage | null | undefined),
  getMatches: (stage) => (stage as ViewerStage | null | undefined)?.matches || [],
  stageMembers: (stage) => ((stage as ViewerStage | null | undefined)?.members as string[] | undefined) || [],
  buildPaneContent: ({pane, stageCode, stage, data}) => {
    const kind = stageType(stage as ViewerStage | null | undefined);
    if (kind === "reseed") {
      pane.appendChild(buildReseedPanes(stageCode));
    } else if (kind === "standings") {
      pane.appendChild(buildGroupStandingsPane(stage as ViewerStage));
    } else {
      pane.appendChild(buildReadonlyStageTables(data!));
    }
  },
  onStageDataChanged: ({pane, stageCode, data}) => {
    repaintStagePane(pane, stageCode, data);
  },
  onMatchUpdated: ({pane, frame, matchState, descriptor}) => {
    paintStageFrame(frame as StageFrameElement, matchState as ViewerMatchView, descriptor as ViewerStageMatch | null);
    if (pane.isConnected && !pane.hidden) scheduleReadonlyNameOverflowUpdate(frame);
  },
  onPaneShown: ({pane}) => {
    scheduleReadonlyNameOverflowUpdate(pane);
    stageScroll?.refresh();
  },
});
let reloadTimer: number | undefined;
let readonlyTableIndex: NodeIndex | null = null;
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

async function loadCurrent(): Promise<void> {
  if (consumeViewerInit()) return;
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else if (route.mode === "roster") {
    await loadRoster();
  } else if (route.mode === "stats") {
    await loadStats();
  } else {
    await loadFest();
  }
}

// consumeViewerInit renders the first frame from server-inlined
// window.__VIEWER_INIT__, skipping the cold API round trips. Returns true on
// success; mismatched routes fall back to the network path.
function consumeViewerInit(): boolean {
  const init = pageWindow.__VIEWER_INIT__;
  if (!init || !init.route || !init.fest) return false;
  if (init.route.mode !== route.mode) return false;
  // See consumeHostInit: don't compare festID/gameID. Server resolved slugs
  // to numeric ids, which won't string-match the URL slug.
  if (route.mode === "match" && init.route.matchCode !== route.matchCode) return false;
  if (route.mode === "stage" && init.route.stageCode !== route.stageCode) return false;
  pageWindow.__VIEWER_INIT__ = null;

  adoptFestView(init.fest);
  if (Array.isArray(init.venues)) venues = init.venues;

  writeFestCache(init.fest);

  if (route.mode === "match") {
    if (!init.match) return false;
    state = init.match;
    adoptMatchCode(state);
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

// The cache slot is keyed off the current route, which changes as the viewer
// navigates between games client-side, so resolve it lazily per call.
const festCache = () => gameTable.createLocalCache(`viewer:fest:${route.festID || ""}:${route.gameID || ""}`);
const readFestCache = () => festCache().read();
const writeFestCache = (view: unknown) => festCache().write(view);

function adoptFestView(view: FestView): void {
  fest = view;
  boutLetters = null;
  if (Array.isArray(view?.venues)) venues = view.venues;
  stageCache.adoptFest(view);
}

// Every бой of the game carries a буква — the sheets' A..Z, AA.. handle — dealt
// once per fest view over the scheme's schedule order.
let boutLetters: Map<string, string> | null = null;
function letterMap(): Map<string, string> {
  if (!boutLetters) boutLetters = DopeTable.festLetters(fest?.stages as StageRef[] | undefined);
  return boutLetters;
}
function letteredBoutTitle(matchCode: string | undefined, title: string): string {
  return DopeTable.letteredTitle(title, letterMap().get(matchCode || ""));
}

function hydrateFestFromCache(): boolean {
  if (fest) return true;
  const cached = readFestCache();
  if (!cached) return false;
  adoptFestView(cached as FestView);
  return true;
}

async function loadFest(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderFest();
  const response = await fetch(route.apiBase!);
  if (!response.ok) throw new Error(await response.text());
  const fresh = (await response.json()) as FestView;
  const changed = !cached || fresh.revision !== fest?.revision;
  adoptFestView(fresh);
  writeFestCache(fresh);
  if (changed) renderFest();
}

async function loadStage(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderStage();
  // renderStage may have translated a legacy `@` bookmark; fetch what it shows.
  const stageCode = route.stageCode!;
  // Revalidate fest and fetch this stage's matches in parallel.
  // adoptFestView clears stage caches if the revision changed.
  const festPromise = fetch(route.apiBase!).then(async (response) => {
    if (!response.ok) throw new Error(await response.text());
    const fresh = (await response.json()) as FestView;
    adoptFestView(fresh);
    writeFestCache(fresh);
  });
  const stagePromise = stageCache.prefetchStage(stageCode);
  await Promise.all([festPromise, stagePromise]);
  if (route.mode !== "stage") return;
  // A legacy code survives until the fest arrives and renderStage translates
  // it — that translation must not read as "the user switched tabs".
  if (route.stageCode !== stageCode &&
    DopeTable.canonicalStageCode(viewerStages(), stageCode) !== route.stageCode) return;
  renderStage();
  // Background prefetch of every other stage. Each payload is <10KB and
  // makes subsequent tab switches instant (cache hit + pane already built).
  stageCache.prefetchAllStages();
}

function repaintStagePane(pane: HTMLElement, stageCode: string, data: StageData): void {
  const stage = viewerStages().find((s) => s.code === stageCode) || mergedStage(fest!, stageCode);
  if (stageType(stage) === "reseed") {
    pane.replaceChildren(buildReseedPanes(stageCode));
    return;
  }
  const frames = pane.querySelectorAll<StageFrameElement>(".stage-match-frame");
  for (const frame of frames) {
    const code = frame.dataset.matchCode || "";
    const descriptor = data.matches.find((m) => m.code === code);
    paintStageFrame(frame, data.stateByCode.get(code) as ViewerMatchView | undefined, descriptor as ViewerStageMatch | undefined);
  }
  if (pane.isConnected && !pane.hidden) scheduleReadonlyNameOverflowUpdate(pane);
}

// labelFrameGroup names the группа a бой was played in. A круг pane shows six of
// them together, and every группа has a «Бой 1» — so the бой's own title names
// six different tables. Written from the title rather than prefixed onto
// whatever is there, so a repaint cannot stack «Группа 1. Группа 1. Бой 1».
function labelFrameGroup(frame: StageFrameElement, table: HTMLElement, title: string): void {
  const group = frame.dataset.group;
  if (!group || !title) return;
  const heading = table.querySelector<HTMLElement>(".readonly-battle-title, .battle-title");
  if (heading) heading.textContent = `${group}. ${title}`;
}

function paintStageFrame(frame: StageFrameElement, matchState: ViewerMatchView | null | undefined, descriptor: ViewerStageMatch | null | undefined): void {
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
      labelFrameGroup(frame, table,
        letteredBoutTitle(matchState.code, matchState.title || descriptor?.title || ""));
      frame.__scoreIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
    }
    frame.__matchState = matchState;
    return;
  }
  frame.__scoreIndex = null;
  frame.__matchState = null;
  const placeholder = document.createElement("div");
  placeholder.className = "stage-match-placeholder";
  const title = letteredBoutTitle(descriptor?.code, descriptor?.title || `Бой ${descriptor?.code || ""}`);
  placeholder.textContent = frame.dataset.group ? `${frame.dataset.group}. ${title}` : title;
  frame.replaceChildren(placeholder);
}

function buildReadonlyStageTables(data: StageData): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack";
  for (const match of data.matches) {
    const frame = document.createElement("section") as StageFrameElement;
    frame.className = "stage-match-frame";
    frame.dataset.matchCode = match.code || "";
    const group = (match as {group?: string}).group;
    if (group) frame.dataset.group = group;
    paintStageFrame(frame, data.stateByCode.get(match.code!) as ViewerMatchView | undefined, match as ViewerStageMatch);
    wrapper.appendChild(frame);
  }
  return wrapper;
}

async function loadMatch(): Promise<void> {
  hydrateFestFromCache();
  const [matchResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode!)}`),
    fetch(route.apiBase!),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  state = (await matchResponse.json()) as ViewerMatchView;
  adoptMatchCode(state);
  adoptFestView((await festResponse.json()) as FestView);
  writeFestCache(fest);
  render();
}

async function loadRoster(): Promise<void> {
  // The roster view fetches the fest-level team→players list itself; here we only
  // need the fest view for the heading/tabs. Render from cache immediately, then
  // revalidate the fest in the background.
  const cached = hydrateFestFromCache();
  if (cached) renderRoster();
  const response = await fetch(route.apiBase!);
  if (!response.ok) throw new Error(await response.text());
  const freshFest = (await response.json()) as FestView;
  adoptFestView(freshFest);
  writeFestCache(freshFest);
  if (route.mode !== "roster") return;
  renderRoster();
}

function renderRoster(): void {
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle("Составы");
  renderViewerTabs();
  viewerRoot.replaceChildren(gameTable.buildRosterView(route.festID));
}

async function loadVenuesPage(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderVenues();
  const [venuesResponse, festResponse] = await Promise.all([
    fetch(`/api/fest/${route.festID}/venues`),
    fetch(route.apiBase!),
  ]);
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  const freshVenues = venueList(await venuesResponse.json());
  const freshFest = (await festResponse.json()) as FestView;
  const changed = !cached || JSON.stringify(freshVenues) !== JSON.stringify(venues);
  venues = freshVenues;
  adoptFestView(freshFest);
  writeFestCache(freshFest);
  if (changed) renderVenues();
}

async function loadStats(): Promise<void> {
  // Stats are an aggregate of every battle, computed from the shared stage
  // cache (the same per-match MatchViews the bracket holds). Warm it once with a
  // single /stages/matches request, deduped with the bracket prefetch; SSE
  // deltas then keep it live and renderStats reads from memory — no refetch.
  hydrateFestFromCache();
  const response = await fetch(route.apiBase!);
  if (!response.ok) throw new Error(await response.text());
  adoptFestView((await response.json()) as FestView);
  writeFestCache(fest);
  await stageCache.prefetchAllStages();
  if (route.mode !== "stats") return;
  renderStats();
}

// statsStagesFromCache shapes the live stage cache into the
// [{code, matches:[MatchView]}] form computeEKPlayerStats expects.
function statsStagesFromCache(): EKStage[] {
  const stages: EKStage[] = [];
  for (const stage of viewerStages()) {
    const data = stageCache.getData(stage.code);
    if (!data) continue;
    const matches: ViewerMatchView[] = [];
    for (const match of data.matches || []) {
      const ms = data.stateByCode.get(match.code!);
      if (ms) matches.push(ms as ViewerMatchView);
    }
    stages.push({code: stage.code, matches});
  }
  return stages;
}

function renderStats(): void {
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
function rerenderStatsTable(): void {
  // A personal game has no per-theme players — the participant is the player,
  // so the aggregate is per seat.
  const node = individualGame()
    ? gameTable.buildIndividualStatsTable(gameTable.computeIndividualPlayerStats(statsStagesFromCache()))
    : gameTable.buildEKStatsTable(gameTable.computeEKPlayerStats(statsStagesFromCache()));
  viewerRoot.replaceChildren(node);
  scheduleReadonlyNameOverflowUpdate();
}

// SSE lifecycle — stream, epoch-reload latch, lockdown, iOS wake recovery —
// lives in createLiveEvents (state-sync.ts); this page only dispatches scoped
// messages. Static snapshot pages skip streaming and reload jittered instead.
const liveEvents = gameTable.createLiveEvents({
  eventsURL: () => gameTable.gameEventsURL(route.festID!, route.gameID),
  onMessage: dispatchStateMessage,
  onViewers: (count) => viewerCounter.setCount(count),
  onLockdown: () => gameTable.scheduleStaticReload(),
  reload: () => loadCurrent(),
  onDown: () => setStatus("reconnecting"),
  onUp: () => setLive(true),
  onRecoverError: (error) => {
    setLive(false);
    console.error(error);
  },
  onStreamError: () => setLive(false),
  staticMode: () => staticMode,
  recorder: () => recorder,
  recorderTags: () => ({mode: route.mode, matchCode: route.matchCode}),
});

function dispatchStateMessage(message: ScopedEventMessage): void {
  // On the stats page, fold match edits into the cache in place and recompute
  // from memory — no refetch. Other scopes don't affect the aggregate.
  if (route.mode === "stats") {
    if (message.scope?.startsWith("match:")) statsSync.applyMatchEvent(message as unknown as StatsMatchEvent);
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
    venues = venueList(message.data);
    renderVenues();
    setLive(true);
    return;
  }
  if (message.scope?.startsWith("fest:") && (message.data as FestView | null | undefined)?.stages) {
    applyFestViewEvent(message.data as FestView);
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
}

function applyFestViewEvent(view: FestView): void {
  adoptFestView(view);
  writeFestCache(fest);
  if (route.mode === "stage") {
    renderStage();
  } else if (route.mode === "venues") {
    renderVenues();
  } else if (route.mode !== "match" && route.mode !== "stats") {
    renderFest();
  }
}

// Toggles the scrolled-under fade on the frozen-column boundary of the EK stage
// tables (see the .stage-scroll-left rule in styles.css). The .sheet-frame is
// static, so the binding is made once and refreshed thereafter.
let stageScroll: ScrollEdgeBinding | null = null;

function bindStageScrollFade(): void {
  if (stageScroll) {
    stageScroll.refresh();
    return;
  }
  stageScroll = gameTable.bindScrollEdges(viewerRoot.closest(".sheet-frame"), ({left}, frame) => {
    frame.classList.toggle("stage-scroll-left", left);
  });
}

// SPA navigation for the viewer tab strip: same pattern as the host EK page.
// Intercepts same-origin clicks within #viewerTabs, pushes the URL, and runs
// loadCurrent without reloading the page.
function bindViewerSPANavigation(): void {
  if (embedded) return;
  viewerTabsRoot?.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    if (event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = (event.target as Element | null)?.closest?.<HTMLAnchorElement>("a[href]");
    if (!link || !viewerTabsRoot!.contains(link)) return;
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

function runViewerCurrentRoute(): void {
  route = currentRoute();
  setStatus("saving");
  editorLink?.refresh();
  loadCurrent()
    .then(() => setLive(true))
    .catch((error: unknown) => {
      setLive(false);
      console.error(error);
    });
}

function scheduleReload(): void {
  recorder?.event("reload", {mode: route.mode, matchCode: route.matchCode});
  window.clearTimeout(reloadTimer);
  reloadTimer = window.setTimeout(() => {
    loadCurrent()
      .then(() => setLive(true))
      .catch((error: unknown) => {
        setLive(false);
        console.error(error);
      });
  }, 120);
}

// statsSync keeps the stats page live off the same SSE stream the bracket uses:
// each match-scoped event folds into the shared stage cache and the table
// recomputes from memory (throttled); a seq gap resyncs the bracket once
// (debounced). The loop lives in stats-sync.js so it is shared with host.js and
// unit-tested; this file supplies the page-specific pieces.
const statsSync = DopeStatsSync.create({
  stageCache,
  gameTable: gameTable as unknown as StatsSyncGameTable,
  matchCodeFromScope: matchCodeFromScope as (scope: unknown) => string,
  isActive: () => route.mode === "stats",
  rerender: rerenderStatsTable,
});

function setLive(ok: boolean): void {
  setStatus(ok ? "saved" : "error");
}

function renderFest(): void {
  if (!fest) return;
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  viewerRoot.replaceChildren(buildFestGrid(fest, {viewer: true, basePath: route.base}));
}

function renderStage(): void {
  if (!fest) return;
  if (route.stageCode) route.stageCode = DopeTable.canonicalStageCode(viewerStages(), route.stageCode);
  const stageCode = route.stageCode;
  if (!stageCode) return;
  resetReadonlyTableIndex();
  const stage = viewerStages().find((s) => s.code === stageCode) || mergedStage(fest, stageCode);
  setViewerMode(stageType(stage) === "reseed" ? "grid" : "match");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  const pane = stageCache.showStage(stageCode);
  if (stageType(stage) === "reseed") {
    pane?.replaceChildren(buildReseedPanes(stageCode));
  } else if (stageType(stage) === "standings") {
    pane?.replaceChildren(buildGroupStandingsPane(stage as ViewerStage));
  }
}

function renderVenues(): void {
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("ЭК");
  document.title = pageTitle("Площадки");
  renderViewerTabs();
  viewerRoot.replaceChildren(gameTable.buildVenuesTable(venues));
}

function render(): void {
  if (!state) return;
  setViewerMode("match");
  setHeading("ЭК");
  document.title = pageTitle();
  renderViewerTabs();
  const table = buildReadonlyTable();
  readonlyTableIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
  if (embedded) {
    viewerRoot.replaceChildren(table);
    gameTable.notifyEmbeddedResize(embedded);
  } else {
    viewerRoot.replaceChildren(table);
  }
  scheduleReadonlyNameOverflowUpdate();
  stageScroll?.refresh();
}

function applyUpdatedMatch(updated: ViewerMatchView): void {
  const previous = state;
  state = updated;
  if (readonlyTableIndex && canPatchMatchTable(previous, updated)) {
    patchMatchTable(readonlyTableIndex, updated);
    return;
  }
  render();
}

function applyReadonlyStageMatchUpdate(updated: ViewerMatchView): void {
  const result = stageCache.applyMatchUpdate(updated);
  if (!result.found) {
    // Match not in any known stage — fest scheme probably changed.
    scheduleReload();
  }
}

// matchCodeFromScope extracts the match code from a "match:<gameID>:<code>"
// scope (codes never contain ':', but join the tail defensively).
function matchCodeFromScope(scope: string): string {
  return scope.split(":").slice(2).join(":");
}

// A URL names a бой by its буква; every scope, cache key and event names it
// by code. Once the state is here, the route speaks code too.
function adoptMatchCode(view: {code?: string} | null): void {
  if (view?.code) route.matchCode = view.code;
}

function isFocusedMatch(code: string): boolean {
  return route.mode === "match" && code === route.matchCode;
}

// isDisplayed reports whether a match is currently on screen — the focused match
// in match mode, or any match of the open stage in stage mode. A seq gap on a
// displayed match must trigger a resync so the visible pane refreshes; a gap on
// an off-screen match only needs its cache evicted (refetched on navigation).
function isDisplayed(code: string): boolean {
  if (isFocusedMatch(code)) return true;
  if (route.mode === "stage") return stageCache.stageCodeForMatch(code) === route.stageCode;
  return false;
}

// matchBase returns the cached full view a delta should apply onto: the focused
// match's `state` when we're on it, else the stage cache. null means we have no
// base (e.g. a match in a stage we haven't fetched yet).
function matchBase(code: string): ViewerMatchView | null {
  if (isFocusedMatch(code) && state?.code === code) return state;
  return stageCache.matchState(code) as ViewerMatchView | null;
}

// handleMatchEvent applies a match-scope SSE event — a scoped delta when ops are
// present, a full-state snapshot otherwise. Deltas reconstruct the full view by
// applying ops to the cached base, but only when they chain (prevSeq === the
// base's seq); a missing base or a seq gap can't be applied safely, so we evict
// (forcing a fresh fetch) and resync the match we're actually showing. This
// keeps the cached view correct-or-absent — a bug degrades to a refetch, never
// a wrong bracket.
function handleMatchEvent(message: ScopedEventMessage, matchScope: string): void {
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
    const next = gameTable.applyDeltaOps(base, message.ops) as ViewerMatchView;
    next.seq = Number(message.seq) || prev;
    applyMatchView(next, message.scope, matchScope);
    return;
  }
  if ((message.data as ViewerMatchView | null | undefined)?.code) {
    const view = message.data as ViewerMatchView;
    view.seq = Number(message.seq) || 0;
    applyMatchView(view, message.scope, matchScope);
    return;
  }
  // Match event with no usable payload — fall back to a reload.
  scheduleReload();
}

// applyMatchView warms the stage cache for any match and re-renders the focused
// match in place when the event is for the one we're viewing.
function applyMatchView(view: ViewerMatchView, scope: string, matchScope: string): void {
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
function canPatchMatchTable(previous: ViewerMatchView | null | undefined, next: ViewerMatchView | null | undefined): boolean {
  if (!previous || !next) return false;
  if (matchTitleFor(previous) !== matchTitleFor(next)) return false;
  if (!gameTable.canPatchScoreShape(previous, next)) return false;
  const prevTeams = previous.participants || [];
  const nextTeams = next.participants || [];
  for (let i = 0; i < nextTeams.length; i++) {
    if (formatPlace(prevTeams[i].place) !== formatPlace(nextTeams[i].place)) return false;
  }
  return true;
}

function patchMatchTable(index: NodeIndex, matchState: ViewerMatchView): void {
  gameTable.patchScoreTable(index, matchState, {formatNumber});
  // A patched player name may now overflow (or stop overflowing); refresh the
  // truncation/popover chrome. rAF-throttled, so cheap on frequent mark patches.
  scheduleReadonlyNameOverflowUpdate();
}

function resetReadonlyTableIndex(): void {
  readonlyTableIndex = null;
}

function viewerTabItems(): Array<{href: string; label: string; key: string}> {
  const items = [
    {href: route.base! + "/", label: "Сетка", key: "grid"},
    {href: route.base! + "/venues", label: "Площадки", key: "venues"},
  ];
  viewerStages().forEach((stage) => {
    items.push({
      href: `${route.base}/stage/${encodeURIComponent(stage.code)}`,
      label: gameTable.stageTabLabel(stage),
      key: `stage:${stage.code}`,
    });
  });
  // Статистика and Составы sit at the very end, after all stage tabs. An
  // individual game has no составы: its Сетка already names every player.
  items.push({href: route.base! + "/stats", label: "Статистика", key: "stats"});
  if (!individualGame()) items.push({href: route.base! + "/roster", label: "Составы", key: "roster"});
  return items;
}

function renderViewerTabs(): void {
  if (!viewerTabsRoot || embedded || !fest) return;
  viewerTabsRoot.replaceChildren();
  const active = activeViewerTabKey();
  let activeLink: HTMLAnchorElement | null = null;
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

function activeViewerTabKey(): string {
  if (route.mode === "stage") return `stage:${route.stageCode}`;
  if (route.mode === "match") {
    const stageCode = state?.stageCode || stageCodeForMatch(route.matchCode);
    return stageCode ? `stage:${stageCode}` : "grid";
  }
  if (route.mode === "venues") return "venues";
  if (route.mode === "roster") return "roster";
  if (route.mode === "stats") return "stats";
  return "grid";
}

let viewerTabsScroll: ScrollEdgeBinding | null = null;

function bindViewerTabsScrollFade(): void {
  if (!viewerTabsRoot || embedded) return;
  if (!viewerTabsScroll) {
    viewerTabsScroll = gameTable.bindScrollEdges(viewerTabsRoot, ({left, right}, tabs) => {
      tabs.classList.toggle("tabs-scroll-left", left);
      tabs.classList.toggle("tabs-scroll-right", right);
    });
    return;
  }
  viewerTabsScroll.refresh();
}

function scrollActiveViewerTabIntoView(activeLink: HTMLAnchorElement | null): void {
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

function scheduleViewerTabsFadeUpdate(): void {
  viewerTabsScroll?.refresh();
}

function withMatchState<T>(matchState: ViewerMatchView, callback: () => T): T {
  const previousState = state;
  state = matchState;
  try {
    return callback();
  } finally {
    state = previousState;
  }
}

function buildReadonlyTable(): HTMLTableElement {
  const hasShootout = shootoutThemeCount() > 0;
  const themes = readonlyThemeHeaders();
  const rows = state!.participants.map((team, teamIndex) => {
    const themeCells: ScoreTableThemeRow[] = [];
    team.themes.forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, false));
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, true));
    });
    return {
      nameCell: readonlyTeamNameCell(team, teamIndex),
      totalCell: td(team.total, "sticky sticky-total number total-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}}),
      placeCell: td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}}),
      themes: themeCells,
      afterThemeCells: readonlyTrailingCells(team, teamIndex, hasShootout),
    };
  });

  const build = individualGame() ? gameTable.buildFlatScoreTable : gameTable.buildTwoRowScoreTable;
  return build({
    className: `match-table compact-score-table ek-stage-table readonly-table${individualGame() ? " individual-blank" : ""}`,
    nameHeader: {content: readonlyBattleTitleNode(state!), className: "sticky sticky-name battle readonly-battle-head readonly-battle-with-popover"},
    themes,
    afterThemeHeaders: readonlyTrailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
}

function readonlyTeamNameCell(team: ViewerParticipant, teamIndex: number): HTMLElement {
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}});
  const labelText = team.name || "";
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout";
  const nameWrap = document.createElement("span");
  nameWrap.className = "od-detailed-team-name-wrap";
  const label = document.createElement("span");
  label.className = "od-detailed-team-name";
  label.textContent = labelText;
  label.tabIndex = 0;
  label.setAttribute("aria-label", labelText);
  nameWrap.appendChild(label);
  layout.appendChild(nameWrap);
  cell.appendChild(layout);
  const fullName = document.createElement("span");
  fullName.className = "popover popover-inline od-detailed-team-name-popover";
  fullName.textContent = labelText;
  cell.appendChild(fullName);
  return cell;
}

function scheduleReadonlyNameOverflowUpdate(root: ParentNode = viewerRoot): void {
  if (readonlyNameOverflowFrame) cancelAnimationFrame(readonlyNameOverflowFrame);
  readonlyNameOverflowFrame = requestAnimationFrame(() => {
    readonlyNameOverflowFrame = 0;
    updateReadonlyNameOverflow(root);
  });
}

function updateReadonlyNameOverflow(root: ParentNode = viewerRoot): void {
  const ekCells = root.querySelectorAll<HTMLElement>(".ek-team-cell");
  for (const cell of ekCells) {
    const name = cell.querySelector<HTMLElement>(".od-detailed-team-name");
    const truncated = gameTable.fitEKStageTeamName(cell, name);
    cell.classList.toggle("od-detailed-team-cell-truncated", truncated);
  }
  gameTable.markNameOverflow(root, {
    cellSelector: ".readonly-player",
    nameSelector: ".readonly-player-text",
    truncatedClass: "readonly-player-cell-truncated",
  });
  gameTable.markNameOverflow(root, {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
  });
}

function readonlyThemeHeaders(): ScoreTableTheme[] {
  const themes: ScoreTableTheme[] = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({label: `Т${theme + 1}`, questionLabels: state!.questionValues});
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: `П${theme + 1}`,
      questionLabels: state!.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
    });
  }
  return themes;
}

function readonlyTrailingHeaders(hasShootout: boolean): CellSpec[] {
  const headers: CellSpec[] = [];
  if (hasShootout) headers.push({content: "П", className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function readonlyThemeCells(teamIndex: number, theme: ViewerTheme, themeIndex: number, isShootout: boolean): ScoreTableThemeRow {
  const answers = theme.answers.map((mark, answerIndex) => {
    const className = answerIndex === 0
      ? `answer-cell theme-block theme-block-bottom-left ${mark}`
      : `answer-cell theme-block ${mark}`;
    const cell = td("", className);
    cell.dataset.team = String(teamIndex);
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    return cell;
  });
  const scoreCell = td(theme.score, "number theme-score theme-block theme-block-score", {
    rowSpan: seatRowSpan(),
    dataset: {team: teamIndex, shootout: isShootout ? "1" : "0", theme: themeIndex},
  });
  // A player seats himself: his бой row needs no player cell at all.
  if (individualGame()) return {scoreCell, answers};
  const playerCell = document.createElement("td");
  playerCell.colSpan = state!.questionValues.length;
  playerCell.className = "readonly-player theme-block theme-block-top-left";
  const playerLabel = theme.player || "";
  const playerWrap = document.createElement("span");
  playerWrap.className = "readonly-player-text-wrap";
  const playerText = document.createElement("span");
  playerText.className = "readonly-player-text";
  // Coordinates so patchScoreTable's playerText sync can find and update this
  // cell in place when the player changes (keys: team, shootout, theme).
  playerText.dataset.team = String(teamIndex);
  playerText.dataset.shootout = isShootout ? "1" : "0";
  playerText.dataset.theme = String(themeIndex);
  playerText.textContent = playerLabel;
  playerWrap.appendChild(playerText);
  playerCell.appendChild(playerWrap);
  // Always render the popover (even empty) so the sync keeps it in step when the
  // player changes from/to blank, rather than only existing at build time.
  const playerPopover = document.createElement("span");
  playerPopover.className = "popover popover-inline readonly-player-popover";
  playerPopover.textContent = playerLabel;
  playerCell.appendChild(playerPopover);
  return {
    playerCell,
    scoreCell,
    answers,
  };
}

function readonlyTrailingCells(team: ViewerParticipant, teamIndex: number, hasShootout: boolean): HTMLElement[] {
  const cells: HTMLElement[] = [];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    cells.push(td(shootoutTotal, "number tiebreak-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}}));
  }
  cells.push(td(team.plus, "number plus-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}}));
  [0, 1, 2, 3, 4].forEach((idx) => {
    cells.push(td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {
      rowSpan: seatRowSpan(),
      dataset: {team: teamIndex, valueIndex: idx},
    }));
  });
  return cells;
}

function regularThemeCount(): number {
  return state!.participants[0].themes.length;
}

function shootoutThemeCount(): number {
  return shootoutThemesFor(state!.participants[0]).length;
}

function totalThemeCount(): number {
  return regularThemeCount() + shootoutThemeCount();
}

function shootoutThemesFor(team: ViewerParticipant): ViewerTheme[] {
  return team.shootoutThemes || [];
}

function currentRoute(): ViewerRoute {
  const path = window.location.pathname;
  const prefix = path.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const festID = prefix[1];
  const gameID = prefix[2];
  const base = `/fest/${festID}/game/${gameID}`;
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  // A trailing /static segment forces the static snapshot server-side (see
  // handleFestRouter) but leaves the URL in the bar. Strip it before matching the
  // sub-route, else it falls through to mode "missing" and the injected snapshot
  // (route.mode "grid"/"match"/…) is rejected, leaving the page a blank spinner.
  const rest = path.slice(prefix[0].length).replace(/\/static$/, "");
  const stripped = rest.replace(/\/$/, "");
  if (stripped === "" || stripped === "/") {
    return {mode: "grid", festID, gameID, base, apiBase};
  }
  if (stripped === "/venues") return {mode: "venues", festID, gameID, base, apiBase};
  if (stripped === "/roster") return {mode: "roster", festID, gameID, base, apiBase};
  if (stripped === "/stats") return {mode: "stats", festID, gameID, base, apiBase};
  const match = stripped.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), festID, gameID, base, apiBase};
  const stage = stripped.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), festID, gameID, base, apiBase};
  return {mode: "missing"};
}

// The same tabs the host sees: a Block of Groups is shown круг by круг, not
// группа by группа. A spectator and a ведущий disagreeing about what a tab is
// would be worse than either arrangement.
function viewerStages(): ViewerStage[] {
  const scheme = parseScheme(fest?.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : fest?.stages || [];
  return DopeTable.foldReseedStages(DopeTable.roundStages(stages as StageRef[])) as ViewerStage[];
}

function rawViewerStages(): ViewerStage[] {
  const scheme = parseScheme(fest?.schemaJson);
  return (scheme?.stages?.length ? scheme.stages : fest?.stages || []) as ViewerStage[];
}

// buildGroupStandingsPane is the sheets' «Группы» view: every группа of the
// Block on one tab — a player, his очки, and the split by круг, computed from
// the cached бои by the Block's own scoring rule.
function buildGroupStandingsPane(stage: ViewerStage): HTMLElement {
  const groups = ((stage.members as string[] | undefined) || []).map((code) => {
    const schemeStage = rawViewerStages().find((s) => s.code === code);
    const config = (schemeStage?.config || {}) as {rules?: {bout?: {points?: string}}; entrants?: Array<{label?: string}>};
    const planned = (schemeStage?.matches || []) as Array<{code?: string; round?: number}>;
    const roundCount = Math.max(1, ...planned.map((m) => Number(m.round || 1)));
    const matches = planned.map((m) => {
      const view = stageCache.matchState(m.code || "") as ViewerMatchView | null;
      return {round: m.round, finished: Boolean(view?.finished), participants: view?.participants};
    });
    const rows = computeGroupRounds({matches, pointsRule: config.rules?.bout?.points, roundCount});
    if (!rows.length) {
      for (const entrant of config.entrants || []) {
        if (entrant.label) rows.push({name: entrant.label, points: 0, rounds: new Array<number>(roundCount).fill(0)});
      }
    }
    const title = String(schemeStage?.title || "").match(/Группа\s*\S+$/)?.[0] || String(schemeStage?.title || code);
    return {title, roundCount, rows};
  });
  return gameTable.buildGroupStandingsView(groups);
}

// buildReseedPanes fills a reseed pane: the folded «Пересев» tab stacks every
// этап's table, each under the name of the round it seats; a lone reseed keeps
// its single panel.
function buildReseedPanes(stageCode: string): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "reseed-fold";
  const members = stageCode === DopeTable.RESEED_TAB_CODE
    ? (viewerStages().find((stage) => stage.code === stageCode)?.members as string[] | undefined || [])
    : [stageCode];
  const raw = rawViewerStages();
  for (const code of members) {
    if (members.length > 1) {
      const index = raw.findIndex((stage) => stage.code === code);
      const next = raw.slice(index + 1).find((stage) => stageType(stage) !== "reseed");
      const head = document.createElement("h3");
      head.className = "reseed-fold-head";
      head.textContent = String(next?.title || "");
      wrap.appendChild(head);
    }
    wrap.appendChild(buildReseedStagePanel(mergedStage(fest!, code), {letters: letterMap()}));
  }
  return wrap;
}

function findStage(data: FestView, code: string): ViewerStage | undefined {
  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  return (stages as ViewerStage[]).find((stage) => stage.code === code);
}

function findLiveStage(data: FestView | null, code: string): FestGridStage | undefined {
  return (data?.stages || []).find((stage) => stage.code === code);
}

function mergedStage(data: FestView, code: string): FestGridStage {
  const schemeStage: FestGridStage = findStage(data, code) || {};
  const liveStage: FestGridStage = findLiveStage(data, code) || {};
  return {
    ...schemeStage,
    ...liveStage,
    reseedEntries: liveStage.reseedEntries || schemeStage.reseedEntries || [],
  };
}

function stageCodeForMatch(matchCode: string | undefined): string {
  if (!matchCode) return "";
  for (const stage of viewerStages()) {
    if ((stage.matches || []).some((match) => match.code === matchCode)) return stage.code;
  }
  return "";
}

const stageType = gameTable.stageType;

function setHeading(text: string): void {
  if (pageHeading) {
    pageHeading.textContent = "";
    pageHeading.hidden = true;
  }
  renderGameBreadcrumbs();
}

function renderGameBreadcrumbs(): void {
  if (!breadcrumbsNode || !route.festID) return;
  const gameTitle = fest?.gameName || currentGameTitle() || "ЭК";
  gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
    festHref: `/fest/${route.festID}`,
    festTitle: fest?.title || "Фест",
    gameHref: route.mode === "grid" ? "" : route.base + "/",
    gameTitle,
    currentTitle: breadcrumbCurrentTitle(gameTitle),
  });
}

function breadcrumbCurrentTitle(gameTitle: string): string {
  if (route.mode === "grid") return "";
  if (route.mode === "venues") return "Площадки";
  if (route.mode === "stats") return "Статистика";
  if (route.mode === "match") return state?.title || route.matchCode || "";
  if (route.mode === "stage") {
    // The displayed tabs first: a synthetic stage (standings, круг) exists
    // nowhere server-side, and its code is no title for a crumb.
    return viewerStages().find((stage) => stage.code === route.stageCode)?.title ||
      findStage(fest!, route.stageCode!)?.title || route.stageCode || "";
  }
  return gameTitle;
}

function setViewerMode(mode: string): void {
  viewerRoot.classList.toggle("grid-host", mode === "grid");
  // Составы fits the frame and wraps rather than scrolling sideways like a
  // score board, so the host drops its max-content sizing.
  viewerRoot.classList.toggle("fits-frame", mode === "roster");
}

function pageTitle(primary = ""): string {
  const main = String(primary || currentGameTitle() || state?.title || "").trim();
  const festTitle = String(fest?.title || "").trim();
  if (main && festTitle) return `${main} · ${festTitle}`;
  return main || festTitle || "Фест";
}

function currentGameTitle(): string {
  const scheme = parseScheme(fest?.schemaJson) as (FestScheme & {title?: unknown}) | null;
  return String(scheme?.title || "").trim();
}

function readonlyBattleTitleNode(matchState: ViewerMatchView): HTMLElement {
  const fullLabel = matchTitleFor(matchState);
  const title = document.createElement("span");
  title.className = "readonly-battle-title";
  title.tabIndex = 0;
  title.setAttribute("aria-label", fullLabel);
  title.title = fullLabel;

  const battle = document.createElement("span");
  battle.className = "readonly-battle-name";
  battle.textContent = letteredBoutTitle(matchState?.code, matchState?.title || "");
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
  popover.className = "popover readonly-battle-popover";
  popover.textContent = fullLabel;
  title.appendChild(popover);

  return title;
}

function matchTitleFor(matchState: ViewerMatchView | null | undefined): string {
  const venueLabel = formatBattleVenue(matchState?.venue);
  const venue = venueLabel ? ` · ${venueLabel}` : "";
  return `${matchState?.title || ""}${venue}`;
}

bindViewerSPANavigation();
bindStageScrollFade();
recorder = gameTable.installClientRecorder({
  scope: `viewer:${route.festID}:${route.gameID}`,
  getState: () => ({mode: route.mode, matchCode: route.matchCode, state}),
  // Spectators only get the download button when they opt in with ?log.
  showButton: /[?&]log\b/.test(location.search),
});
loadCurrent()
  .then(() => {
    setLive(true);
    liveEvents.connect();
  })
  .catch((error: unknown) => {
    setLive(false);
    console.error(error);
  });

export {};
