// ek.ts — the EK page, host and spectator alike: match score editing, undo,
// stage tabs, SSE sync. A /host/… URL edits and a /fest/… URL reads (the
// `viewer` flag), the way brain.ts serves both. Bundled by pages/ek.ts.

import {cssEscape, formatNumber, formatPlace, isFormControl, option, td, th} from "./cells.js";
import {buildFlatScoreTable, buildTwoRowScoreTable, canPatchScoreShape, createScoreTableIndex, patchScoreTable, setMarkClass} from "./score-table.js";
import type {NodeIndex, ParticipantView, ThemeView} from "./score-table.js";
import {buildVenuesTable, formatBattleVenue, formatBattleVenueShort, formatVenue} from "./venue.js";
import type {Venue} from "./venue.js";
import {buildGroupStandingsView, festLetters, letteredTitle, resultsTeamCell, stageType} from "./standings.js";
import type {StageRef} from "./standings.js";
import {buildRosterView} from "./fest-roster.js";
import {buildEKStatsTable, buildIndividualStatsTable, computeEKPlayerStats, computeIndividualPlayerStats} from "./ek-stats.js";
import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import type {PendingOp, WireOp} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import {createLocalCache, notifyEmbeddedResize} from "./game-page.js";
import {bindScrollEdges, clamp, createFloatingPopover, fitEKStageTeamName, fitScrollFade, installCellNavBar, isClipped, markNameOverflow} from "./widgets.js";
import type {CellNavBar, ScrollEdgeBinding} from "./widgets.js";
import {createSheetCursor} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import { createStageCache } from "./stage-cache.js";
import type { MatchDescriptor, MatchView as CachedMatchView, StageData } from "./stage-cache.js";
import { create as createStatsSync } from "./stats-sync.js";
import { buildFestGrid, buildReseedStagePanel, parseScheme } from "./fest-grid.js";
import { computeGroupRounds } from "./group-stats.js";
import { gameTabs, canonicalKey, groupLabel, RESEED_TAB_CODE } from "./game-tabs.js";
import type { GameTab, TabKind } from "./game-tabs.js";
import type { ReseedEntry } from "./fest-grid.js";
import { icon } from "./icons_gen.js";
import S from "./i18nstrings.js";

type EKMode = "grid" | "venues" | "roster" | "stats" | "seedImport" | "match" | "stage" | "missing";

interface EKRoute {
  mode: EKMode;
  viewer: boolean;
  festID: string;
  gameID: string;
  base: string;
  viewerBase: string;
  apiBase: string;
  festApi: string;
  matchCode?: string;
  stageCode?: string;
}

interface HostThemeView extends ThemeView {
  player: string;
  answers: string[];
}

interface HostRosterMember {
  id: number;
  name: string;
}

interface HostParticipantView extends ParticipantView {
  id?: number;
  roster?: HostRosterMember[];
  themes: HostThemeView[];
  shootoutThemes?: HostThemeView[];
  correctCounts: number[];
}

interface HostMatchView extends CachedMatchView {
  title?: string;
  finished: boolean;
  revision?: number;
  stageCode?: string;
  venue?: {number: number; title?: string} | null;
  questionValues: number[];
  participants: HostParticipantView[];
}

type HostStageMatch = {
  code?: string;
  title?: string;
  round?: number;
  // group labels a match inside a round-robin pane, where six groups are shown
  // together and "Match 1" alone names three of them.
  group?: string;
};

type HostStage = {
  code: string;
  title?: string;
  stage_type?: string;
  type?: string;
  status?: string;
  config?: unknown;
  grain?: {block?: string; wave?: number; group?: string};
  reseedReady?: boolean;
  reseedEntries?: ReseedEntry[];
  matches?: HostStageMatch[];
  // members names the server stages a round-robin round is assembled from; empty on an
  // ordinary stage, which is its own.
  members?: string[];
};

type HostFestView = {
  slug?: string;
  title?: string;
  gameName?: string;
  gameType?: string;
  revision?: number;
  schemaJson?: unknown;
  venues?: Venue[];
  stages?: HostStage[];
};

type SeedImportRow = {
  teamID?: number;
  seedNumber?: number;
  name?: string;
  city?: string;
  declined?: boolean;
  waitlist?: boolean;
};

type SeedImportView = {
  rows?: SeedImportRow[];
  activeCount?: number;
  drawSize?: number;
};

interface EKInitPayload {
  route?: {mode?: string; matchCode?: string; stageCode?: string; gameID?: unknown};
  fest?: HostFestView | null;
  match?: HostMatchView | null;
  seedImport?: SeedImportView | null;
  teamsUnnumbered?: boolean;
  canEdit?: boolean;
  static?: boolean;
}

const pageWindow = window as Window & {__EK_INIT__?: EKInitPayload | null};

type ActiveCell = {matchCode: string; team: number; shootout: boolean; theme: number; answer: number};

type StagePane = HTMLElement & {
  _stageObserver?: IntersectionObserver | null;
};

type StageFrame = HTMLElement & {
  __scoreIndex?: NodeIndex;
  __matchState?: HostMatchView;
};

type EKCellPayload = {
  team: number;
  theme?: number;
  answer?: number;
  mark?: string;
  player?: string;
  place?: number;
  shootout?: boolean;
};

// BlobOp is one wire operation against a match's Protocol state blob: a path of
// object keys / array indices, and the value to set there. Team sections are
// keyed by team id (a string), theme players are player ids, and a host's place
// is a pin — the server resolves nothing by name (ADR-0005).
type BlobOp = {op?: "set" | "remove"; path: Array<string | number>; value?: unknown};


type UndoEditItem = {team: number; theme: number; answer: number; shootout: boolean; previous: string};
type UndoGroup = {matchCode: string; items: UndoEditItem[]};
type UndoEntry = {
  kind: "match-edits";
  groups: UndoGroup[];
  selection: {anchor: CellCoord; focus: CellCoord} | null;
};
type UndoContext = {mode: string; matchCode: string | null; stageCode: string | null};

const ekRoot = document.getElementById("ekTable") as HTMLElement;
fitScrollFade(ekRoot.closest(".sheet-frame"));
const statusNode = document.getElementById("status") as HTMLElement;
const ekTabsRoot = document.getElementById("ekTabs");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

let route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
// A spectator reads what a host edits: the same page, with every control
// and every write path gated on the shell's one flag. The server scopes SSE
// events by NUMERIC game id (`match:<id>:<code>`) while the URL may carry the
// slug; the shell takes the id from the inlined init.
const shell = mountGamePage({
  app: "ek",
  root: ekRoot,
  statusNode,
  breadcrumbsNode,
  festID: route.festID,
  gameID: route.gameID,
  viewer: Boolean(route.viewer),
  apiBase: route.apiBase,
  init: pageWindow.__EK_INIT__ ? {...pageWindow.__EK_INIT__, gameID: pageWindow.__EK_INIT__.route?.gameID} : null,
  embedded,
  chrome: () => {
    const gameTitle = fest?.gameName || currentGameTitle() || S.ek.title();
    return {
      festTitle: fest?.title || "",
      gameTitle,
      gameHref: route.mode === "grid" ? "" : route.base + "/",
      currentTitle: breadcrumbCurrentTitle(gameTitle),
    };
  },
  cursorKinds: {
    answer: {selector: ".answer-cell", keys: ["matchCode", "team", "shootout", "theme", "answer"]},
    player: {selector: "[data-player-select]", keys: ["matchCode", "team", "shootout", "theme"]},
    place: {selector: ".place-input", keys: ["matchCode", "team"]},
    finish: {selector: ".finish-toggle", keys: ["matchCode"]},
    venue: {selector: ".venue-edit-button", keys: ["matchCode"]},
  },
  activeCursorElement: () => sheet.activeCell,
  recorderState: () => ({mode: route.mode, matchCode: route.matchCode, stageCode: route.stageCode, state}),
});
const {viewer, staticMode, scopeGameID, indicator, viewerCounter, recorder} = shell;
let state: HostMatchView | null = null;
let fest: HostFestView | null = null;
let venues: Venue[] = [];

// Individual SI runs on this page for its bracket, not its blank: a seat of one
// player has no per-theme player cell, so it takes one row (KSI-shaped) where
// a team's takes two.
function individualGame(): boolean {
  return fest?.gameType === "si";
}

function seatRowSpan(): number {
  return individualGame() ? 1 : 2;
}

// A fest that declares no tables answers `null`, not `[]`. One such answer used
// to take the whole page down on the first `venues.length`, so the list is
// normalised at every seam it enters by.
function venueList(raw: unknown): Venue[] {
  return Array.isArray(raw) ? raw as Venue[] : [];
}
const stageCache = createStageCache({
  container: ekRoot,
  apiBase: () => route.apiBase,
  schemeStages: () => (fest ? ekSchemeStages() : []),
  findStage: (code) => ekSchemeStages().find((stage) => stage.code === code) || findStage(fest, code),
  stageType: (stage) => stageType(stage as HostStage | null | undefined),
  getMatches: (stage) => (stage as HostStage | null | undefined)?.matches || [],
  stageMembers: (stage) => (stage as HostStage | null | undefined)?.members || [],
  // Re-overlay un-acked local edits onto every MatchView the cache stores, so a
  // background refetch (prefetchStage/prefetchAllStages) or an SSE update can
  // never wipe an optimistically-marked cell before the server confirms it.
  overlayMatch: (view) => overlayPendingMatch(view?.code, view as HostMatchView),
  buildPaneContent: ({pane, stageCode, stage, data}) => {
    if (stageType(stage as HostStage | null | undefined) === "reseed") {
      pane.appendChild(buildReseedPanes(stageCode));
      return;
    }
    if (stageType(stage as StageRef) === "standings") {
      pane.appendChild(buildGroupStandingsPane(stage as HostStage));
      return;
    }
    pane.appendChild(buildStageTableStack(data));
    setupStageTableObserver(pane);
  },
  onStageDataChanged: ({pane, stageCode, data}) => {
    refreshPaneFrames(pane, data);
  },
  onMatchUpdated: ({frame, matchState}) => {
    if ((frame as StageFrame).dataset.rendered === "1" || (frame as StageFrame).dataset.nearViewport === "1") {
      updateStageFrame(frame as StageFrame, matchState as HostMatchView);
    }
  },
  onPaneShown: ({pane, stageCode}) => {
    // The cursor follows the active cell into the pane, or has nothing there.
    seatCursor({focus: false});
    bindStageOverflowScroll();
    scheduleEKTeamNameOverflowUpdate(pane);
  },
  cleanupPane: ({pane}) => {
    (pane as StagePane)._stageObserver?.disconnect();
    (pane as StagePane)._stageObserver = null;
  },
});
let renderMatchCode: string | null = null;
let activeCell: ActiveCell = {matchCode: "", team: 0, shootout: false, theme: 0, answer: 0};
let reloadTimer: number | null = null;
let matchTableIndex: NodeIndex | null = null;
// Live SSE stream, kept at module scope so the visibility/online recovery below
// can tear down a dead connection and re-establish it. null while disconnected.
const undoStack: UndoEntry[] = [];
const UNDO_LIMIT = 200;
let undoStackContext: UndoContext | null = null;
let undoApplying = false;
let seedImport: SeedImportView | null = null;
let seedImportNotice = "";
let gridNameOverflowFrame = 0;
let ekTeamNameOverflowFrame = 0;
let resultsTeamNameOverflowFrame = 0;
let stageOverflowScrollFrame: Element | null = null;
let stageScroll: ScrollEdgeBinding | null = null;
let ekTabsScroll: ScrollEdgeBinding | null = null;
let playerSelectMeasureContext: CanvasRenderingContext2D | null = null;

const floatingPopoverSpecs = [
  {
    trigger: ".readonly-battle-head.readonly-battle-with-popover",
    popover: ".readonly-battle-popover",
    anchor: ".readonly-battle-title",
  },
  {
    trigger: ".readonly-player.readonly-player-cell-truncated",
    popover: ".readonly-player-popover",
    anchor: ".readonly-player-text-wrap",
  },
  {
    trigger: ".od-detailed-team-cell-truncated",
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
    anchor: "[data-player-select]",
  },
];

document.body.classList.toggle("embedded-match", embedded);
if (!viewer) document.addEventListener("keydown", handleGlobalKeydown);
const floatingPopover = createFloatingPopover({root: ekRoot, specs: floatingPopoverSpecs});
floatingPopover.bind();
window.addEventListener("resize", () => {
  if (route.mode === "grid") scheduleGridNameOverflowUpdate();
  if (route.mode === "match" || route.mode === "stage") scheduleEKTeamNameOverflowUpdate();
  if (route.mode === "seedImport" || route.mode === "stats") scheduleResultsTeamNameOverflowUpdate();
  floatingPopover.position();
  ekTabsScroll?.refresh();
});

async function loadCurrent(): Promise<void> {
  if (consumeInit()) {
    if (!viewer) recoverMatchPendingEdits();
    return;
  }
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
  } else if (route.mode === "seedImport") {
    await loadSeedImportPage();
  } else {
    await loadFest();
  }
  if (!viewer) recoverMatchPendingEdits();
}

// localStorage SWR cache for FestView. We render the previous fest immediately
// on every navigation, then revalidate against the server in the background.
// Skips the cache silently when localStorage is unavailable (private mode,
// quota, disabled cookies).
// The cache slot is keyed off the current route, which changes as the host
// navigates between games client-side, so resolve it lazily per call.
const festCache = () => createLocalCache(`ek:fest:${route.festID}:${route.gameID}`);
const readFestCache = () => festCache().read();
const writeFestCache = (view: HostFestView | null) => festCache().write(view);

function adoptFestView(view: HostFestView): void {
  fest = view;
  boutLetters = null;
  if (Array.isArray(view?.venues)) venues = view.venues;
  stageCache.adoptFest(view);
}

// hydrateFestFromCache returns true if it managed to populate `fest` from
// either memory or localStorage (without hitting the network).
function hydrateFestFromCache(): boolean {
  if (fest) return true;
  const cached = readFestCache() as HostFestView | null;
  if (!cached) return false;
  adoptFestView(cached);
  return true;
}

// A URL names a match by its letter; every scope, cache key and event names it
// by code. Once the state is here, the route speaks code too.
function adoptMatchCode(view: {code?: string} | null): void {
  if (view?.code) route.matchCode = view.code;
}

// consumeInit renders the first frame from the server-inlined
// window.__EK_INIT__ payload, skipping the API round trips that loadX would
// otherwise make. Returns true on success; on any shape mismatch, falls back
// to the normal fetch path.
function consumeInit(): boolean {
  const init = pageWindow.__EK_INIT__;
  if (!init || !init.route || !init.fest) return false;
  if (init.route.mode !== route.mode) return false;
  // Don't compare festID/gameID: the server resolves slugs to numeric ids, so
  // a slug URL like "/host/fest/test/game/ek" produces an inlined int64 that
  // never string-matches the slug. The server only inlines data for the page
  // it just served — trust the route mode + resource codes.
  if (route.mode === "match" && init.route.matchCode !== route.matchCode) return false;
  if (route.mode === "stage" && init.route.stageCode !== route.stageCode) return false;
  pageWindow.__EK_INIT__ = null;

  adoptFestView(init.fest);
  writeFestCache(init.fest);

  if (route.mode === "match") {
    if (!init.match) return false;
    state = init.match;
    adoptMatchCode(state);
    render();
    return true;
  }
  if (route.mode === "stage") {
    // Server inlines fest data but not per-match state. Adopt the fest from
    // __EK_INIT__ (already done above) and fall through so loadStage runs
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

async function loadFest(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderFest();
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  const fresh = await response.json() as HostFestView;
  const changed = !cached || fresh.revision !== fest?.revision;
  adoptFestView(fresh);
  writeFestCache(fresh);
  if (changed || !cached) renderFest();
}

async function loadStage(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderStage();
  const stageCode = route.stageCode!;
  // Revalidate fest+venues and fetch this stage's matches in parallel.
  // adoptFestView routes through the cache, which clears caches on revision bump.
  const festPromise = Promise.all([
    fetch(route.apiBase),
    fetch(`${route.festApi}/venues`),
  ]).then(async ([response, venuesResponse]) => {
    if (!response.ok) throw new Error(await response.text());
    if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
    const fresh = await response.json() as HostFestView;
    const freshVenues = venueList(await venuesResponse.json());
    venues = freshVenues;
    adoptFestView(fresh);
    writeFestCache(fresh);
  });
  const stagePromise = stageCache.prefetchStage(stageCode);
  await Promise.all([festPromise, stagePromise]);
  if (route.mode !== "stage") return;
  // A legacy code survives until the fest arrives and renderStage translates
  // it — that translation must not read as "the user switched tabs".
  if (route.stageCode !== stageCode &&
    canonicalStageCode(stageCode) !== route.stageCode) return;
  renderStage();
  // Background prefetch of every other stage. Each payload is small and makes
  // subsequent tab switches instant (data + pane already cached).
  stageCache.prefetchAllStages();
}

async function loadMatch(): Promise<void> {
  // Match state changes per cell edit, so we don't cache it — the match table
  // still waits on its fetch.
  hydrateFestFromCache();
  const [matchResponse, venuesResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode!)}`),
    fetch(`${route.festApi}/venues`),
    fetch(route.apiBase),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  const loaded = await matchResponse.json() as HostMatchView;
  adoptMatchCode(loaded);
  state = overlayPendingMatch(route.matchCode, loaded);
  venues = venueList(await venuesResponse.json());
  adoptFestView(await festResponse.json() as HostFestView);
  writeFestCache(fest);
  render();
}

async function loadStats(): Promise<void> {
  // Stats are an aggregate of every battle, computed from the shared stage
  // cache — the same per-match MatchViews the bracket pages hold. We warm it
  // once here (a single /stages/matches request, deduped with the bracket
  // prefetch); after that, SSE deltas keep the cache live and renderStats reads
  // straight from memory — no per-edit refetch.
  hydrateFestFromCache();
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  adoptFestView(await response.json() as HostFestView);
  writeFestCache(fest);
  await stageCache.prefetchAllStages();
  if (route.mode !== "stats") return;
  renderStats();
}

// statsStagesFromCache shapes the live stage cache into the
// [{code, matches:[MatchView]}] form computeEKPlayerStats expects, pulling the
// current in-memory MatchView for every match that has one.
function statsStagesFromCache(): Array<{code: string; matches: HostMatchView[]}> {
  const stages: Array<{code: string; matches: HostMatchView[]}> = [];
  for (const stage of ekSchemeStages()) {
    const data = stageCache.getData(stage.code);
    if (!data) continue;
    const matches: HostMatchView[] = [];
    for (const match of data.matches || []) {
      const ms = data.stateByCode.get(match.code ?? "") as HostMatchView | undefined;
      if (ms) matches.push(ms);
    }
    stages.push({code: stage.code, matches});
  }
  return stages;
}

async function loadRoster(): Promise<void> {
  // The roster view fetches the fest-level team→players list itself; here we only
  // need the fest view for the heading/breadcrumbs. Render from cache immediately,
  // then revalidate the fest in the background.
  const cached = hydrateFestFromCache();
  if (cached) renderRoster();
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  adoptFestView(await response.json() as HostFestView);
  writeFestCache(fest);
  if (route.mode !== "roster") return;
  renderRoster();
}

async function loadVenuesPage(): Promise<void> {
  const cached = hydrateFestFromCache();
  if (cached) renderVenues();
  const [venuesResponse, festResponse] = await Promise.all([
    fetch(`${route.festApi}/venues`),
    fetch(route.apiBase),
  ]);
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  const freshVenues = venueList(await venuesResponse.json());
  const freshFest = await festResponse.json() as HostFestView;
  const changed = !cached || JSON.stringify(freshVenues) !== JSON.stringify(venues);
  venues = freshVenues;
  adoptFestView(freshFest);
  writeFestCache(fest);
  if (changed) renderVenues();
}

async function loadSeedImportPage(): Promise<void> {
  // Seed-import payload is small and not cached separately.
  hydrateFestFromCache();
  const [seedResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/seed-import`),
    fetch(route.apiBase),
  ]);
  if (!seedResponse.ok) throw new Error(await seedResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  seedImport = await seedResponse.json() as SeedImportView;
  adoptFestView(await festResponse.json() as HostFestView);
  writeFestCache(fest);
  renderSeedImport();
}

// SSE lifecycle — stream, seq chaining, epoch-reload latch, iOS wake recovery
// — lives in createLiveEvents (state-sync.ts); this page says what each scope
// chains onto and adopts what the engine hands back. Un-acked edits survive the
// epoch reload via the writer's durable pending storage.
const liveEvents = createLiveEvents({
  eventsURL: () => gameEventsURL(route.festID, route.gameID),
  gameID: scopeGameID,
  scopes: [{
    // Match-scoped events always route into the cached stage data, whatever
    // page we're on, so a later tab switch sees fresh data without a fetch. On
    // the stats page they fold into the cache in place and the table recomputes
    // from memory — no refetch.
    prefix: matchScopeFor(""),
    base: (scope) => {
      const view = matchBase(matchCodeFromScope(scope));
      return view ? {data: view, seq: Number(view.seq) || 0} : null;
    },
    adopt: (scope, view) => {
      const code = matchCodeFromScope(scope);
      const next = view.data as HostMatchView | null | undefined;
      if (!next?.code) {
        if (route.mode === "stats") statsSync.scheduleResync();
        else scheduleReload();
        return;
      }
      next.seq = view.seq;
      const result = stageCache.applyMatchUpdate(next);
      if (route.mode === "stats") {
        statsSync.scheduleRerender();
        return;
      }
      if (route.mode === "match" && code === route.matchCode) applyUpdatedMatch(next, code);
      if (!result.found && route.mode === "stage") scheduleReload();
    },
    // A delta we can't apply only needs a reload when it would change what
    // the user is looking at; otherwise evicting the stale cache entry is
    // enough — never repaint the placeholder skeleton for an off-screen stage.
    gap: (scope) => {
      const code = matchCodeFromScope(scope);
      if (route.mode === "stats") {
        statsSync.scheduleResync();
        return;
      }
      stageCache.invalidateMatch(code);
      if (matchVisible(code)) scheduleReload();
    },
  }, {
    prefix: `venues:${route.festID}`,
    adopt: (_scope, view) => {
      if (route.mode !== "venues") {
        reloadUnlessStats();
        return;
      }
      venues = venueList(view.data);
      renderVenues();
    },
  }, {
    prefix: "fest:",
    adopt: (_scope, view) => {
      const festView = view.data as HostFestView | null | undefined;
      if (festView?.stages) applyFestViewEvent(festView);
      else reloadUnlessStats();
    },
  }],
  // Anything else on the fest stream — this game's own state, an event shape
  // we don't know — may have moved what's on screen; sibling games are dropped
  // by the engine, and the stats page needs only match events.
  onUnhandled: reloadUnlessStats,
  indicator,
  onViewers: (count) => viewerCounter.setCount(count),
  onLockdown: () => scheduleStaticReload(),
  reload: () => loadCurrent(),
  onRecoverError: (error) => {
    indicator.fail();
    console.error(error);
  },
  staticMode: () => staticMode,
  recorder: () => recorder,
});

function reloadUnlessStats(): void {
  if (route.mode !== "stats") scheduleReload();
}

function applyFestViewEvent(view: HostFestView): void {
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
function matchCodeFromScope(scope: unknown): string {
  return (scope as string).split(":").slice(2).join(":");
}

// matchBase returns the cached full view a delta should apply onto: the focused
// match's `state` when we're on it, else the stage cache. null means we have no
// base (e.g. a match in a stage we haven't fetched yet).
function matchBase(code: string): HostMatchView | null {
  if (route.mode === "match" && state?.code === code) return state;
  return stageCache.matchState(code) as HostMatchView | null;
}

// matchVisible reports whether a match is currently on screen — the focused
// match in match mode, or any match of the open stage in stage mode. A delta we
// can't apply (no base / seq gap) only needs a reload when it would change what
// the user is looking at; otherwise evicting the stale cache entry is enough.
function matchVisible(code: string): boolean {
  if (route.mode === "match") return code === route.matchCode;
  if (route.mode === "stage") return stageCache.stageCodeForMatch(code) === route.stageCode;
  return false;
}

// SPA navigation for the EK tab strip: intercept same-origin clicks within
// #ekTabs, update history, re-parse the route, and re-run loadCurrent. Keeps
// the EventSource and presence connections alive across tab switches, so the
// only work on switch is the data fetch and DOM rebuild for the new view.
function bindSPANavigation(): void {
  if (embedded) return;
  ekTabsRoot?.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    if (event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const link = (event.target as Element | null)?.closest?.<HTMLAnchorElement>("a[href]");
    if (!link || !ekTabsRoot!.contains(link)) return;
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

function navigateTo(target: string): void {
  history.pushState(null, "", target);
  runCurrentRoute();
}

function runCurrentRoute(): void {
  route = currentRoute();
  shell.refreshLinks();
  void tracked("route", loadCurrent());
}

function scheduleReload(): void {
  window.clearTimeout(reloadTimer ?? undefined);
  reloadTimer = window.setTimeout(() => void tracked("reload", loadCurrent()), 120);
}

// tracked shows a load on the status dot the way a write shows: saving while it
// runs, error if it fails.
async function tracked(key: string, work: Promise<unknown>): Promise<void> {
  indicator.busy(key);
  try {
    await work;
    indicator.idle(key, true);
  } catch (error) {
    indicator.idle(key, false);
    indicator.fail();
    console.error(error);
  }
}

// statsSync keeps the stats page live off the same SSE stream the bracket uses:
// each match-scoped event folds into the shared stage cache and the table
// recomputes from memory (throttled); a seq gap resyncs the bracket once
// (debounced). The loop lives in stats-sync.js so it is shared with viewer.js
// and unit-tested; this file supplies the page-specific pieces.
const statsSync = createStatsSync({
  stageCache,
  isActive: () => route.mode === "stats",
  rerender: rerenderStatsTable,
});

function matchScopeFor(matchCode: string): string {
  return `match:${scopeGameID}:${matchCode}`;
}

// The one write discipline (state-sync.ts): cell edits are applied to the DOM
// and match state instantly (ekApplyValues), queued per match, coalesced into ONE
// PATCH per match per debounce window, re-overlaid on every MatchView the page
// renders until the server confirms them, retried, and persisted per match so a
// mid-sync refresh recovers them. Structural writes (finish, venue, a shootout
// theme) go out on their own; a finish carries its intent so a slow write plus
// a co-incident broadcast can't visually revert the tick.
const writer = createScopedWriter({
  readonly: viewer,
  urlOf: (scope) => `${route.apiBase}/matches/${encodeURIComponent(matchCodeFromScope(scope))}/state`,
  encode: (scope, ops) => {
    const view = matchBase(matchCodeFromScope(scope));
    if (!view) return null; // no base to resolve team/player ids against; retry on reload
    return ops.map((op) => opToBlobOp(op, view)).filter((op): op is WireOp => op !== null);
  },
  adopt: (scope, response) => {
    if (scope.startsWith(matchScopeFor(""))) applyUpdatedMatch(response as HostMatchView, matchCodeFromScope(scope));
  },
  indicator,
  recorder: () => recorder,
  onRejected: (info) => recorder?.event("write-rejected", info),
});

function matchURL(matchCode: string, suffix: string): string {
  return `${route.apiBase}/matches/${encodeURIComponent(matchCode)}/${suffix}`;
}

// shootoutThemeOps builds the ops that add or drop one shootout theme across
// every team of a match — the grid stays in lockstep, so one theme index is
// touched on every team at once.
function shootoutThemeOps(matchCode: string, themeIndex: number, remove: boolean): BlobOp[] {
  const view = matchBase(matchCode);
  const ops: BlobOp[] = [];
  for (const team of view?.participants || []) {
    if (!team.id) continue;
    const path = ["participants", String(team.id), "shootoutThemes", themeIndex];
    ops.push(remove ? {op: "remove", path} : {path, value: {answers: ["", "", "", "", ""]}});
  }
  return ops;
}

// sendStructuralOps applies ops that aren't tracked cell edits (adding or
// dropping a shootout theme) — they have no optimistic overlay, so they go out
// on their own rather than through the pending queue.
function sendStructuralOps(matchCode: string, ops: BlobOp[]): void {
  if (ops.length === 0) return;
  void writer.send(matchScopeFor(matchCode), {url: matchURL(matchCode, "state"), method: "PATCH", body: {ops}});
}

// setMatchFinished toggles a match's finished flag: the new value shows at once
// and the writer holds it as the match's intent until this write settles.
function setMatchFinished(matchCode: string, value: boolean): void {
  recorder?.event("ek-finished", {matchCode, value});
  if (state && state.code === matchCode) {
    state.finished = value;
    render();
  }
  void writer.send(matchScopeFor(matchCode), {url: matchURL(matchCode, "finish"), body: {finished: value}}, {path: ["finished"], value});
}

// overlayPendingMatch re-applies a match's un-acked local edits on top of a
// MatchView. Used everywhere a MatchView enters the render pipeline.
function overlayPendingMatch(matchCode: string | undefined, view: HostMatchView): HostMatchView {
  if (!view || !matchCode) return view;
  return writer.overlay(matchScopeFor(matchCode), view);
}

// refreshMatchPendingMarkers toggles the per-cell pending spinner on the focused
// match's answer cells from that match's un-acked edits. Called after a match
// renders and after an edit/ack so a cell stays marked until the server
// confirms it.
function refreshMatchPendingMarkers(matchCode: string | undefined): void {
  if (!matchCode) return;
  const scope = matchScopeFor(matchCode);
  // Scope to THIS match's cells: in a stage view many battles are on screen at
  // once and every battle has cells at the same (team-slot, theme, answer)
  // coordinates, so an unscoped selector would mark — and never clear — the
  // same-positioned cells in every other battle.
  ekRoot.querySelectorAll<HTMLElement>(`.answer-cell[data-match-code="${cssEscape(matchCode)}"]`).forEach((cell) => {
    const team = Number(cell.dataset.team);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    let pending = false;
    if (Number.isInteger(team) && Number.isInteger(theme) && Number.isInteger(answer)) {
      const themeKey = cell.dataset.shootout === "1" ? "shootoutThemes" : "themes";
      pending = writer.isPending(scope, ["participants", team, themeKey, theme, "answers", answer]);
    }
    cell.classList.toggle("pending", pending);
  });
}

// recoverMatchPendingEdits, after a (re)load of the focused match, re-renders
// it with any un-acked edits a previous page load persisted overlaid — showing
// their pending spinner — while the writer re-sends them. No-op when there is
// nothing to recover.
function recoverMatchPendingEdits(): void {
  if (route.mode !== "match" || !state || !state.code) return;
  if (writer.recover(matchScopeFor(state.code)) === 0) return;
  applyUpdatedMatch(state, state.code); // overlays pending → re-renders with them
}

// payloadToOpPath maps an /update cell payload to its MatchView path (matching
// the server's matchDeltaOps shape). Returns null for non-cell (structural)
// payloads, which must not be overlay-tracked.
function payloadToOpPath(payload: EKCellPayload): Array<string | number> | null {
  if (payload.place !== undefined) return ["participants", payload.team, "place"];
  const themesKey = payload.shootout ? "shootoutThemes" : "themes";
  if (payload.player !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "player"];
  if (payload.mark !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "answers", payload.answer!];
  return null;
}

function payloadToOpValue(payload: EKCellPayload): unknown {
  if (payload.place !== undefined) return payload.place;
  if (payload.player !== undefined) return payload.player;
  return payload.mark;
}

// opToBlobOp translates a queued view-path op into the blob path the server
// stores it at: the team's slot index becomes its id, a place becomes a pin,
// and a player name becomes that player's id. Returns null when the view the op
// was queued against no longer holds that team — the op is then unsendable and
// is dropped rather than retried forever.
function opToBlobOp(op: PendingOp, view: HostMatchView): BlobOp | null {
  const [, slot, key, theme, leaf, answer] = op.path;
  const team = view.participants?.[slot as number];
  if (!team?.id) return null;
  const teamKey = String(team.id);
  if (op.path.length === 3) {
    // Emptying the place box clears the pin rather than pinning zero, handing
    // the place back to the scorer at the next recompute.
    const path = ["participants", teamKey, "pin"];
    return op.value ? {path, value: op.value} : {op: "remove", path};
  }
  if (leaf === "player") {
    const name = op.value as string;
    const member = (team.roster || []).find((player) => player.name === name);
    if (name && !member) return null;
    return {path: ["participants", teamKey, key as string, theme as number, "player"], value: member?.id ?? 0};
  }
  return {path: ["participants", teamKey, key as string, theme as number, "answers", answer as number], value: op.value};
}

// queueEKEdits records cell edits as pending ops; the writer batches the flush.
function queueEKEdits(matchCode: string, payloads: EKCellPayload[]): void {
  const scope = matchScopeFor(matchCode);
  let queued = false;
  for (const payload of payloads) {
    const path = payloadToOpPath(payload);
    if (!path) continue;
    writer.patch(scope, path, payloadToOpValue(payload));
    queued = true;
  }
  if (queued) refreshMatchPendingMarkers(matchCode);
}

function sendVenueChange(number: number, matchCode: string = currentMatchCode()): void {
  void writer.send(matchScopeFor(matchCode), {url: matchURL(matchCode, "venue"), body: {number}});
}

async function updateVenueTitle(number: number, title: string): Promise<void> {
  const sent = await writer.send(`venues:${route.festID}`, {url: `${route.festApi}/venues/${encodeURIComponent(number)}`, method: "PUT", body: {title}});
  if (!sent.ok) return;
  venues = sent.response as Venue[];
  renderVenues();
}

async function calculateReseed(stageCode: string | undefined): Promise<void> {
  if (!stageCode) return;
  const sent = await writer.send(`stage:${stageCode}`, {url: `${route.apiBase}/stages/${encodeURIComponent(stageCode)}/reseed`});
  if (!sent.ok) return;
  adoptFestView(sent.response as HostFestView);
  writeFestCache(fest);
  renderStage();
}

function reseedStagePanel(stage: HostStage): HTMLElement {
  return buildReseedStagePanel(stage, {
    letters: letterMap(),
    editable: !viewer,
    canCalculate: Boolean(stage?.reseedReady),
    onCalculate: () => calculateReseed(stage?.code || route.stageCode),
  });
}

function renderFest(): void {
  if (!fest) return;
  resetMatchTableIndex();
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  ekRoot.replaceChildren(buildFestGrid(fest, {viewer, basePath: route.base}));
  scheduleGridNameOverflowUpdate();
  shell.presence.refresh();
}

function renderStage(): void {
  if (!fest) return;
  resetMatchTableIndex();
  if (route.stageCode) route.stageCode = canonicalStageCode(route.stageCode);
  const stageCode = route.stageCode!;
  const stage = ekSchemeStages().find((s) => s.code === stageCode) || mergedStage(fest, stageCode);
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  const pane = stageCache.showStage(stageCode);
  if (stageType(stage) === "reseed") {
    pane?.replaceChildren(buildReseedPanes(stageCode));
    scheduleResultsTeamNameOverflowUpdate();
  } else if (stageType(stage) === "standings") {
    pane?.replaceChildren(buildGroupStandingsPane(stage));
    scheduleResultsTeamNameOverflowUpdate();
  }
  shell.presence.refresh();
}

function renderVenues(): void {
  resetMatchTableIndex();
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  ekRoot.replaceChildren(buildVenuesTable(venues, {editable: !viewer, onTitleChange: updateVenueTitle}));
  shell.presence.refresh();
}

function renderRoster(): void {
  resetMatchTableIndex();
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  ekRoot.replaceChildren(buildRosterView(route.festID));
  shell.presence.refresh();
}

function renderStats(): void {
  resetMatchTableIndex();
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  rerenderStatsTable();
  bindStatsScrollFade();
  shell.presence.refresh();
}

// rerenderStatsTable recomputes the table from the live stage cache and swaps it
// in. Cheap (in-memory over the cached MatchViews); no network. Re-runs the
// results-name overflow pass so long player/team names get the fade + popover.
function rerenderStatsTable(): void {
  // A personal game has no per-theme players — the participant is the player,
  // so the aggregate is per seat.
  const node = individualGame()
    ? buildIndividualStatsTable(computeIndividualPlayerStats(statsStagesFromCache()))
    : buildEKStatsTable(computeEKPlayerStats(statsStagesFromCache()));
  ekRoot.replaceChildren(node);
  scheduleResultsTeamNameOverflowUpdate();
}

function renderSeedImport(): void {
  resetMatchTableIndex();
  setPageMode("grid");
  shell.renderChrome();
  renderEKTabs();
  ekRoot.replaceChildren(buildSeedImportPanel());
  scheduleResultsTeamNameOverflowUpdate();
  shell.presence.refresh();
}

function render(): void {
  if (!state) return;
  setPageMode("match");
  normalizeActiveCell();
  shell.renderChrome();
  renderEKTabs();

  const focusedPlaceTeam = focusedPlaceTeamIndex();
  const finishToggleFocused = isFinishToggleFocused();
  const table = buildTable();
  matchTableIndex = createScoreTableIndex(table, {entity: "team", shootout: true});
  ekRoot.replaceChildren(table);
  notifyEmbeddedResize(embedded);
  scheduleEKTeamNameOverflowUpdate();
  if (viewer) {
    // The spectator's match is a stage-skinned table with a frozen column, so
    // it wants the same scrolled-under cue and name refits a stage pane gets.
    bindStageOverflowScroll();
    return;
  }
  seatCursor({focus: false});
  refreshMatchPendingMarkers(state.code || route.matchCode);
  shell.presence.refresh();
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

const TAB_PATHS: Partial<Record<TabKind, string>> = {grid: "/", venues: "/venues", seedImport: "/seed-import", stats: "/stats", roster: "/roster"};

function gameSubnavItems(): Array<{href: string; label: string; key: string}> {
  return ekTabs().map((tab) => ({
    key: tab.key,
    label: tab.label,
    href: route.base + (tab.stage ? `/stage/${encodeURIComponent(tab.stage.code)}` : TAB_PATHS[tab.kind] || "/"),
  }));
}

function renderEKTabs(): void {
  if (!ekTabsRoot || embedded) return;
  ekTabsRoot.replaceChildren();
  const active = activeTabKey();
  let activeLink: HTMLAnchorElement | null = null;
  for (const item of gameSubnavItems()) {
    const link = document.createElement("a");
    link.className = "match-tab" + (item.key === active ? " active" : "");
    link.href = item.href;
    link.textContent = item.label;
    link.setAttribute("role", "tab");
    link.setAttribute("aria-selected", item.key === active ? "true" : "false");
    if (item.key === active) activeLink = link;
    ekTabsRoot.appendChild(link);
  }
  bindEKTabsScrollFade();
  scrollActiveTabIntoView(activeLink);
}

function scrollActiveTabIntoView(activeLink: HTMLAnchorElement | null): void {
  if (!ekTabsRoot || !activeLink) return;
  requestAnimationFrame(() => {
    const margin = 8;
    const currentLeft = ekTabsRoot.scrollLeft;
    const currentRight = currentLeft + ekTabsRoot.clientWidth;
    const activeLeft = activeLink.offsetLeft;
    const activeRight = activeLeft + activeLink.offsetWidth;
    const maxScroll = Math.max(0, ekTabsRoot.scrollWidth - ekTabsRoot.clientWidth);
    let target = currentLeft;
    if (activeLeft < currentLeft + margin) {
      target = activeLeft - margin;
    } else if (activeRight > currentRight - margin) {
      target = activeRight - ekTabsRoot.clientWidth + margin;
    }
    ekTabsRoot.scrollLeft = clamp(target, 0, maxScroll);
    ekTabsScroll?.refresh();
  });
}

function bindEKTabsScrollFade(): void {
  if (!ekTabsRoot || embedded) return;
  if (!ekTabsScroll) {
    ekTabsScroll = bindScrollEdges(ekTabsRoot, ({left, right}, tabs) => {
      tabs.classList.toggle("tabs-scroll-left", left);
      tabs.classList.toggle("tabs-scroll-right", right);
    });
    return;
  }
  ekTabsScroll.refresh();
}

function activeTabKey(): string {
  if (route.mode === "stage") return `stage:${route.stageCode}`;
  if (route.mode === "match") {
    const stageCode = state?.stageCode || stageCodeForMatch(route.matchCode);
    return stageCode ? `stage:${stageCode}` : "grid";
  }
  if (route.mode === "venues") return "venues";
  if (route.mode === "roster") return "roster";
  if (route.mode === "stats") return "stats";
  if (route.mode === "seedImport") return "seedImport";
  return "grid";
}

function stageCodeForMatch(matchCode: string | undefined): string {
  if (!matchCode) return "";
  for (const stage of ekSchemeStages()) {
    if ((stage.matches || []).some((match) => match.code === matchCode)) return stage.code;
  }
  return "";
}

function ekTabs(): GameTab[] {
  return gameTabs(rawSchemeStages() as StageRef[], {game: individualGame() ? "si" : "ek", viewer});
}

// ekSchemeStages is what the tabs show, in tab order — a round-robin round or
// the folded reseed is a synthetic stage; the rest are the scheme's own.
function ekSchemeStages(): HostStage[] {
  return ekTabs().flatMap((tab) => tab.stage ? [tab.stage as HostStage] : []);
}

function canonicalStageCode(code: string): string {
  return canonicalKey(ekTabs(), `stage:${code}`).replace(/^stage:/, "");
}

// buildGroupStandingsPane is the sheets' groups view: every group of the
// Block on one tab — a player, his points, and the split by round-robin
// round, computed from the cached matches by the Block's own scoring rule.
function buildGroupStandingsPane(stage: HostStage): HTMLElement {
  const groups = ((stage.members as string[] | undefined) || []).map((code) => {
    const schemeStage = rawSchemeStages().find((s) => s.code === code);
    const config = (schemeStage?.config || {}) as {rules?: {bout?: {points?: string}}; entrants?: Array<{label?: string}>};
    const planned = (schemeStage?.matches || []) as Array<{code?: string; round?: number}>;
    const roundCount = Math.max(1, ...planned.map((m) => Number(m.round || 1)));
    const matches = planned.map((m) => {
      const view = stageCache.matchState(m.code || "") as HostMatchView | null;
      return {round: m.round, finished: Boolean(view?.finished), participants: view?.participants};
    });
    const rows = computeGroupRounds({matches, pointsRule: config.rules?.bout?.points, roundCount});
    if (!rows.length) {
      for (const entrant of config.entrants || []) {
        if (entrant.label) rows.push({name: entrant.label, points: 0, rounds: new Array<number>(roundCount).fill(0)});
      }
    }
    const title = schemeStage ? groupLabel(schemeStage as StageRef) : code;
    return {title, roundCount, rows};
  });
  return buildGroupStandingsView(groups);
}

// buildReseedPanes fills a reseed pane: the folded reseed tab stacks every
// stage's table, each under the name of the round it seats; a lone reseed keeps
// its single panel.
function buildReseedPanes(stageCode: string): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "reseed-fold";
  const members = stageCode === RESEED_TAB_CODE
    ? (ekSchemeStages().find((stage) => stage.code === stageCode)?.members || [])
    : [stageCode];
  const raw = rawSchemeStages();
  for (const code of members) {
    if (members.length > 1) {
      const index = raw.findIndex((stage) => stage.code === code);
      const next = raw.slice(index + 1).find((stage) => stageType(stage as StageRef) !== "reseed");
      const head = document.createElement("h3");
      head.className = "reseed-fold-head";
      head.textContent = String(next?.title || "");
      wrap.appendChild(head);
    }
    wrap.appendChild(reseedStagePanel(mergedStage(fest, code)));
  }
  return wrap;
}

function rawSchemeStages(): HostStage[] {
  const scheme = parseScheme(fest?.schemaJson);
  return (scheme?.stages?.length ? scheme.stages : fest?.stages || []) as HostStage[];
}

// Every match of the game carries a letter — the sheets' A..Z, AA.. handle — dealt
// once per fest view over the scheme's schedule order.
let boutLetters: Map<string, string> | null = null;
function letterMap(): Map<string, string> {
  if (!boutLetters) boutLetters = festLetters(fest?.stages as StageRef[] | undefined);
  return boutLetters;
}
function letteredBoutTitle(matchCode: string | undefined, title: string): string {
  return letteredTitle(title, letterMap().get(matchCode || ""));
}

function scheduleGridNameOverflowUpdate(root: ParentNode = ekRoot): void {
  if (gridNameOverflowFrame) cancelAnimationFrame(gridNameOverflowFrame);
  gridNameOverflowFrame = requestAnimationFrame(() => {
    gridNameOverflowFrame = 0;
    updateGridNameOverflow(root);
  });
}

function updateGridNameOverflow(root: ParentNode = ekRoot): void {
  markNameOverflow(root, {
    cellSelector: ".grid-slot-team",
    nameSelector: ".grid-slot-team-name",
    truncatedClass: "grid-slot-team-truncated",
  });
}

function scheduleEKTeamNameOverflowUpdate(root: ParentNode = ekRoot): void {
  if (ekTeamNameOverflowFrame) cancelAnimationFrame(ekTeamNameOverflowFrame);
  ekTeamNameOverflowFrame = requestAnimationFrame(() => {
    ekTeamNameOverflowFrame = 0;
    updateEKTeamNameOverflow(root);
  });
}

function updateEKTeamNameOverflow(root: ParentNode = ekRoot): void {
  updatePlayerSelectOverflow(root);
  markNameOverflow(root, {
    cellSelector: ".readonly-player",
    nameSelector: ".readonly-player-text",
    truncatedClass: "readonly-player-cell-truncated",
  });
  const cells = Array.from(root.querySelectorAll<HTMLElement>(".ek-team-cell"));
  const stageCells: HTMLElement[] = [];
  const stageNames: Array<HTMLElement | null> = [];
  const detailedCells: HTMLElement[] = [];
  const detailedReadings: boolean[] = [];
  for (const cell of cells) {
    const name = cell.querySelector<HTMLElement>(".od-detailed-team-name");
    if (cell.closest(".ek-stage-table")) {
      if (isVisibleInScrollFrame(cell)) {
        stageCells.push(cell);
        stageNames.push(name);
      }
      continue;
    }
    detailedCells.push(cell);
    detailedReadings.push(isClipped(name));
  }
  for (let i = 0; i < detailedCells.length; i++) {
    detailedCells[i].classList.toggle("od-detailed-team-cell-truncated", detailedReadings[i]);
  }
  for (let i = 0; i < stageCells.length; i++) {
    markStageTeamNameTruncated(stageCells[i], stageNames[i]);
  }
}

function updatePlayerSelectOverflow(root: ParentNode = ekRoot): void {
  const wraps = root.querySelectorAll<HTMLElement>(".player-select-wrap");
  const measurements: Array<{wrap: HTMLElement; popover: HTMLElement | null; label: string; truncated: boolean}> = [];
  for (const wrap of wraps) {
    if (wrap.closest(".ek-stage-table") && !isVisibleInScrollFrame(wrap)) continue;
    const select = wrap.querySelector<HTMLSelectElement>("[data-player-select]");
    const popover = wrap.querySelector<HTMLElement>(".player-select-popover");
    const label = selectedPlayerLabel(select);
    measurements.push({wrap, popover, label, truncated: Boolean(label && playerSelectTextOverflows(select, label))});
  }
  for (const m of measurements) {
    if (m.popover) m.popover.textContent = m.label;
    m.wrap.classList.toggle("player-select-truncated", m.truncated);
  }
}

function selectedPlayerLabel(select: HTMLSelectElement | null): string {
  if (!select) return "";
  return select.selectedOptions?.[0]?.textContent || select.value || "";
}

function playerSelectTextOverflows(select: HTMLSelectElement | null, label: string): boolean {
  if (!select || !label) return false;
  const style = getComputedStyle(select);
  const available = select.clientWidth - parseFloat(style.paddingLeft || "0") - parseFloat(style.paddingRight || "0");
  if (available <= 0) return false;
  const context = playerTextMeasureContext();
  context.font = style.font;
  return context.measureText(label).width > available + 1;
}

function playerTextMeasureContext(): CanvasRenderingContext2D {
  if (!playerSelectMeasureContext) {
    playerSelectMeasureContext = document.createElement("canvas").getContext("2d");
  }
  return playerSelectMeasureContext!;
}

function bindStageOverflowScroll(): void {
  const scrollFrame = ekRoot.closest(".sheet-frame");
  if (!scrollFrame) return;
  if (stageOverflowScrollFrame === scrollFrame) {
    stageScroll?.refresh();
    return;
  }
  unbindStageOverflowScroll();
  stageScroll = bindScrollEdges(scrollFrame, ({left}, frame) => {
    scheduleEKTeamNameOverflowUpdate(ekRoot);
    frame.classList.toggle("stage-scroll-left", left);
  });
  stageOverflowScrollFrame = scrollFrame;
}

function unbindStageOverflowScroll(): void {
  stageScroll?.dispose();
  stageScroll = null;
  stageOverflowScrollFrame = null;
}

// The stats page wants the same .stage-scroll-left cue for its frozen player
// column, on the same .sheet-frame — so it reuses the one binding rather than
// adding a second listener that toggles the same class and is never removed.
function bindStatsScrollFade(): void {
  bindStageOverflowScroll();
}

function isVisibleInScrollFrame(element: Element): boolean {
  const scrollFrame = element.closest(".sheet-frame");
  if (!scrollFrame) return true;
  const rect = element.getBoundingClientRect();
  const frameRect = scrollFrame.getBoundingClientRect();
  return rect.bottom >= frameRect.top && rect.top <= frameRect.bottom;
}

function markStageTeamNameTruncated(cell: HTMLElement, name: HTMLElement | null): void {
  const truncated = fitEKStageTeamName(cell, name);
  cell.classList.toggle("od-detailed-team-cell-truncated", truncated);
}

function scheduleResultsTeamNameOverflowUpdate(root: ParentNode = ekRoot): void {
  if (resultsTeamNameOverflowFrame) cancelAnimationFrame(resultsTeamNameOverflowFrame);
  resultsTeamNameOverflowFrame = requestAnimationFrame(() => {
    resultsTeamNameOverflowFrame = 0;
    updateResultsTeamNameOverflow(root);
  });
}

function updateResultsTeamNameOverflow(root: ParentNode = ekRoot): void {
  markNameOverflow(root, {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
  });
}

function buildSeedImportPanel(): HTMLElement {
  const panel = document.createElement("section");
  panel.className = "results-wrapper seed-import-panel";

  const actions = document.createElement("div");
  actions.className = "cluster";
  const importButton = document.createElement("button");
  importButton.type = "button";
  importButton.className = "btn";
  importButton.textContent = S.ek.seed.import();
  importButton.addEventListener("click", importSeedsFromKSI);
  actions.appendChild(importButton);
  panel.appendChild(actions);

  if (seedImportNotice) {
    const notice = document.createElement("p");
    notice.className = seedImportNotice.startsWith(S.ek.seed.errorPrefix()) ? "empty" : "muted";
    notice.textContent = seedImportNotice;
    panel.appendChild(notice);
  }

  const rows = seedImport?.rows || [];
  if (rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.ek.seed.empty();
    panel.appendChild(empty);
    return panel;
  }

  const meta = document.createElement("p");
  meta.className = "muted";
  meta.textContent = S.ek.seed.summary(String(Math.min(seedImport!.activeCount || 0, seedImport!.drawSize || 0)), String(seedImport!.drawSize || 0), String(seedImport!.activeCount || 0));
  panel.appendChild(meta);

  const table = document.createElement("table");
  table.className = "results-table seed-import-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th(S.ek.seedHead.seed(), "results-place-head seed-number-head"));
  head.appendChild(th(S.ek.seedHead.team(), "results-team-head seed-team-head"));
  head.appendChild(th(S.ek.seedHead.declined(), "seed-declined-head"));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  let waitlistInserted = false;
  rows.forEach((row, index) => {
    if (row.waitlist && !waitlistInserted) {
      waitlistInserted = true;
      const divider = document.createElement("tr");
      divider.appendChild(td(S.ek.seed.waitlist(), "seed-waitlist-cell", {colSpan: 3}));
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

    tr.appendChild(resultsTeamCell(row.name || "", {city: row.city}));

    const declinedCell = document.createElement("td");
    declinedCell.className = "results-num seed-declined-cell";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = Boolean(row.declined);
    checkbox.setAttribute("aria-label", S.ek.seed.declinedAria(row.name || S.ek.seed.teamPlaceholder()));
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

async function importSeedsFromKSI(): Promise<void> {
  seedImportNotice = "";
  const sent = await writer.send("seed-import", {url: `${route.apiBase}/seed-import/ksi`});
  if (sent.ok) {
    seedImport = sent.response as SeedImportView;
    seedImportNotice = S.ek.seed.imported(String(seedImport.rows?.length || 0));
  } else {
    seedImportNotice = S.ek.seed.error(sent.error || S.ek.seed.importFailed());
  }
  renderSeedImport();
}

async function setSeedDeclined(teamID: number | undefined, declined: boolean): Promise<void> {
  seedImportNotice = "";
  const sent = await writer.send("seed-import", {url: `${route.apiBase}/seed-import/decline`, body: {teamID, declined}});
  if (sent.ok) seedImport = sent.response as SeedImportView;
  else seedImportNotice = S.ek.seed.error(sent.error || S.ek.seed.declineFailed());
  renderSeedImport();
}

function buildStageTableStack(data: StageData | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack";
  const matches = (data?.matches || []) as HostStageMatch[];
  if (matches.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.ek.stage.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  matches.forEach((match) => {
    const frame = document.createElement("section");
    frame.className = "stage-match-frame";
    frame.dataset.matchCode = match.code || "";
    if (match.group) frame.dataset.group = match.group;
    frame.appendChild(buildStageMatchPlaceholder(match));
    wrapper.appendChild(frame);
  });
  return wrapper;
}

function buildStageMatchPlaceholder(match: HostStageMatch): HTMLElement {
  const placeholder = document.createElement("div");
  placeholder.className = "stage-match-placeholder";
  const title = letteredBoutTitle(match.code, match.title || S.ek.stage.matchFallback(String(match.code)));
  placeholder.textContent = match.group ? `${match.group}. ${title}` : title;
  return placeholder;
}

// setupStageTableObserver installs a per-pane IntersectionObserver that defers
// DOM construction for off-screen match tables. Stored on the pane element so
// cleanupPane (from the cache) can disconnect it on revision invalidation.
function setupStageTableObserver(pane: StagePane): void {
  const frames = Array.from(pane.querySelectorAll<HTMLElement>(".stage-match-frame"));
  if (frames.length === 0) return;
  if (!("IntersectionObserver" in window)) {
    frames.forEach((frame) => renderStageMatchFrameIfReady(pane, frame, {force: true}));
    return;
  }
  const root = ekRoot.closest(".sheet-frame");
  const observer = new IntersectionObserver((entries) => {
    const visibleFrames: HTMLElement[] = [];
    entries.forEach((entry) => {
      if (!entry.isIntersecting) return;
      const frame = entry.target as HTMLElement;
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
function refreshPaneFrames(pane: StagePane, data: StageData): void {
  if (!pane || !data) return;
  const paneStage = ekSchemeStages().find((s) => s.code === pane.dataset.stageCode) || mergedStage(fest, pane.dataset.stageCode!);
  if (stageType(paneStage) === "reseed") {
    pane.replaceChildren(buildReseedPanes(pane.dataset.stageCode!));
    return;
  }
  let rebuilt = false;
  pane.querySelectorAll<HTMLElement>(".stage-match-frame").forEach((frame) => {
    const matchState = data.stateByCode.get(frame.dataset.matchCode || "") as HostMatchView | undefined;
    if (!matchState) return;
    if (frame.dataset.rendered === "1" || frame.dataset.nearViewport === "1") {
      rebuilt = updateStageFrame(frame, matchState) || rebuilt;
    }
  });
  if (rebuilt) scheduleEKTeamNameOverflowUpdate(pane);
}

function renderStageMatchFrameIfReady(pane: StagePane, frame: StageFrame, options: {force?: boolean} = {}): boolean {
  const data = stageCache.getData(pane.dataset.stageCode!);
  const matchState = data?.stateByCode.get(frame.dataset.matchCode || "") as HostMatchView | undefined;
  if (!matchState) return false;
  return renderStageMatchFrame(frame, matchState, options);
}

function renderStageMatchFrame(frame: StageFrame, matchState: HostMatchView, options: {force?: boolean} = {}): boolean {
  if (!frame || (!options.force && frame.dataset.rendered === "1")) return false;
  const hadFocus = document.activeElement?.closest?.(".stage-match-frame") === frame;
  frame.dataset.rendered = "1";
  const stageTable = withMatchState(matchState, () => buildTable({compact: true}));
  frame.replaceChildren(stageTable);
  // In a round-robin pane six groups sit together, and every one has a
  // "Match 1" — so the match says which table it was. Written from the title
  // rather than prefixed onto whatever is there, so a repaint cannot stack
  // the group twice.
  const group = frame.dataset.group;
  const heading = group ? stageTable.querySelector<HTMLElement>(".readonly-battle-name, .battle-title") : null;
  if (heading && matchState.title) {
    heading.textContent = `${group}. ${letteredBoutTitle(matchState.code, matchState.title)}`;
  }
  // Per-frame score index + last state, so a later same-shape update patches
  // this frame's cells in place (updateStageFrame) instead of rebuilding it —
  // the rebuild is what flickered the cell being edited.
  frame.__scoreIndex = createScoreTableIndex(stageTable, {entity: "team", shootout: true});
  frame.__matchState = matchState;
  sheet.refresh();
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
function updateStageFrame(frame: StageFrame, matchState: HostMatchView): boolean {
  if (!frame || !matchState) return false;
  if (frame.dataset.rendered === "1" && frame.__scoreIndex && frame.__matchState &&
      canPatchMatchShape(frame.__matchState, matchState)) {
    patchHostScoreTable(frame.__scoreIndex, matchState);
    frame.__matchState = matchState;
    return false;
  }
  return renderStageMatchFrame(frame, matchState, {force: frame.dataset.rendered === "1"});
}

function stageMatchFrame(matchCode: string): StageFrame | null {
  const stageCode = stageCache.stageCodeForMatch(matchCode);
  if (!stageCode) return null;
  const pane = stageCache.getPane(stageCode);
  return pane?.querySelector<HTMLElement>(`.stage-match-frame[data-match-code="${cssEscape(matchCode)}"]`) || null;
}

function withMatchState<T>(matchState: HostMatchView, callback: () => T): T {
  const previousState = state;
  const previousCode = renderMatchCode;
  state = matchState;
  renderMatchCode = matchState.code || route.matchCode!;
  try {
    return callback();
  } finally {
    state = previousState;
    renderMatchCode = previousCode;
  }
}

function currentMatchCode(): string {
  return renderMatchCode || activeCell.matchCode || route.matchCode!;
}

function currentStageMatches(): MatchDescriptor[] {
  return stageCache.getData(route.stageCode!)?.matches || [];
}

function currentStageStateByCode(): Map<string, HostMatchView> | null {
  return (stageCache.getData(route.stageCode!)?.stateByCode || null) as Map<string, HostMatchView> | null;
}

function activeMatchState(): HostMatchView | null {
  if (route.mode === "stage") {
    const byCode = currentStageStateByCode();
    if (!byCode) return null;
    if (activeCell.matchCode && byCode.has(activeCell.matchCode)) return byCode.get(activeCell.matchCode)!;
    for (const match of currentStageMatches()) {
      const ms = byCode.get(match.code ?? "");
      if (ms) return ms;
    }
    return null;
  }
  return state;
}

function matchStateFor(matchCode: string): HostMatchView | null {
  if (route.mode === "stage") return currentStageStateByCode()?.get(matchCode) || null;
  return state;
}

function applyUpdatedMatch(updated: HostMatchView, matchCode: string | undefined): void {
  // Re-apply any still-un-acked local edits on top, so a server view that
  // predates them (out-of-order response, delta from another editor, refetch)
  // never regresses an optimistic cell. No-op once everything is acked.
  updated = overlayPendingMatch(matchCode, updated);
  if (route.mode === "stage") {
    stageCache.applyMatchUpdate(updated);
    // Re-evaluate this match's spinners against the now-acked ops; without this
    // the stage path never clears the per-cell pending markers (it returns
    // before the single-match path's refresh below).
    refreshMatchPendingMarkers(matchCode);
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
    sheet.refresh();
    refreshMatchPendingMarkers(matchCode);
    return;
  }
  render();
}

// canPatchMatchShape: shared shape check plus the page's structural extras. The
// table renders the title/venue in a header, so a change there needs a rebuild.
function canPatchMatchShape(previous: HostMatchView | null | undefined, next: HostMatchView | null | undefined): boolean {
  if (!previous || !next) return false;
  if (previous.title !== next.title) return false;
  if (formatVenue(previous.venue) !== formatVenue(next.venue)) return false;
  // The host's place is an input the patch fills; the spectator's is text.
  if (viewer && next.participants.some((team, i) => formatPlace(team.place) !== formatPlace(previous.participants[i]?.place))) return false;
  return canPatchScoreShape(previous, next);
}

// patchHostScoreTable patches a built editable score table in place from a
// MatchView. All cell syncing — including the editable place inputs and player
// selects, which skip a focused control so a live update never steals the cursor
// — lives in the shared scoreCellSpecs; the host only injects the callback that
// refreshes a synced select's overflow chrome.
function patchHostScoreTable(index: NodeIndex | null | undefined, matchState: HostMatchView): void {
  patchScoreTable(index, matchState, {
    formatNumber,
    onPlayerSelectSynced: (select) =>
      updatePlayerSelectOverflow(select?.closest(".player-select-wrap") || ekRoot),
  });
  if (viewer) scheduleEKTeamNameOverflowUpdate();
}

function indexedNode(name: string, values: Record<string, unknown>): HTMLElement | null {
  if (route.mode !== "match") return null;
  return matchTableIndex?.get(name, values) || null;
}

function resetMatchTableIndex(): void {
  matchTableIndex = null;
  // unbindStageOverflowScroll only matters when leaving stage mode
  // (renderFest/renderVenues/etc).
  if (route.mode !== "stage") unbindStageOverflowScroll();
}

function stageRowOffset(matchIndex: number): number {
  const matches = currentStageMatches();
  const byCode = currentStageStateByCode();
  let offset = 0;
  for (let i = 0; i < matchIndex && i < matches.length; i++) {
    const s = byCode?.get(matches[i]?.code ?? "");
    offset += s?.participants?.length || 0;
  }
  return offset;
}

function stageCoordOf(cell: Element): CellCoord | null {
  const team = Number((cell as HTMLElement).dataset.team);
  const theme = Number((cell as HTMLElement).dataset.theme);
  const answer = Number((cell as HTMLElement).dataset.answer);
  if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) return null;
  const matchCode = (cell as HTMLElement).dataset.matchCode;
  const matches = currentStageMatches();
  const matchIndex = matches.findIndex((m) => m.code === matchCode);
  if (matchIndex < 0) return null;
  const matchState = currentStageStateByCode()?.get(matchCode!);
  if (!matchState) return null;
  const answers = answerCountFor(matchState);
  if (answers <= 0) return null;
  const shootout = (cell as HTMLElement).dataset.shootout === "1";
  const themeOrder = shootout ? regularThemeCountFor(matchState) + theme : theme;
  return {row: stageRowOffset(matchIndex) + team, col: themeOrder * answers + answer};
}

// stageMatchAtRow finds the match a stage-sheet row falls in: the matches
// stack, so row r is team r − (teams above) of the first match whose teams
// reach it.
function stageMatchAtRow(row: number): {code: string; matchState: HostMatchView; team: number} | null {
  let remaining = row;
  const byCode = currentStageStateByCode();
  for (const match of currentStageMatches()) {
    const matchState = byCode?.get(match.code ?? "");
    if (!matchState) continue;
    const teamCount = matchState.participants?.length || 0;
    if (remaining < teamCount) return {code: match.code ?? "", matchState, team: remaining};
    remaining -= teamCount;
  }
  return null;
}

function stageRowCount(): number {
  const byCode = currentStageStateByCode();
  let rows = 0;
  for (const match of currentStageMatches()) rows += byCode?.get(match.code ?? "")?.participants?.length || 0;
  return rows;
}

function columnCount(matchState: HostMatchView | null | undefined): number {
  const answers = answerCountFor(matchState);
  const shootout = matchState?.participants?.[0]?.shootoutThemes?.length || 0;
  return answers * (regularThemeCountFor(matchState) + shootout);
}

function stageCellAtCoord(coord: CellCoord | null): HTMLElement | null {
  if (!coord) return null;
  const at = stageMatchAtRow(coord.row);
  if (!at) return null;
  const answers = answerCountFor(at.matchState);
  if (answers <= 0) return null;
  const regular = regularThemeCountFor(at.matchState);
  const themeOrder = Math.floor(coord.col / answers);
  const answer = coord.col % answers;
  const shootout = themeOrder >= regular;
  const theme = shootout ? themeOrder - regular : themeOrder;
  return stageMatchFrame(at.code)?.querySelector<HTMLElement>(
    `.answer-cell[data-team="${cssEscape(String(at.team))}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(String(theme))}"][data-answer="${cssEscape(String(answer))}"]`,
  ) || null;
}

function stageApplyValues(edits: CellEdit[]): void {
  const byCode = currentStageStateByCode();
  // Group edits by match so each match's cells are applied through a single
  // ekApplyValues call (and thus a single coalesced re-render), rather than one
  // call — and one re-render — per cell.
  const groupsByCode = new Map<string, {matchState: HostMatchView; items: CellEdit[]}>();
  for (const {cell, value} of edits) {
    const matchCode = (cell as HTMLElement).dataset.matchCode;
    if (!matchCode) continue;
    const matchState = byCode?.get(matchCode);
    if (!matchState || matchState.finished) continue;
    if (!groupsByCode.has(matchCode)) groupsByCode.set(matchCode, {matchState, items: []});
    groupsByCode.get(matchCode)!.items.push({cell, value});
  }
  if (groupsByCode.size === 0) return;
  if (!undoApplying) {
    const groups: UndoGroup[] = [];
    for (const [matchCode, {items}] of groupsByCode) {
      const reverse = snapshotMatchEdits(matchCode, items);
      if (reverse.length > 0) groups.push({matchCode, items: reverse});
    }
    if (groups.length > 0) {
      pushUndoEntry({
        kind: "match-edits",
        groups,
        selection: captureSelection(),
      });
    }
  }
  for (const [matchCode, {matchState, items}] of groupsByCode) {
    ekApplyValues(matchCode, matchState, items, {recordUndo: false});
  }
}

// The one sheet cursor for the match view and the stage view alike. In the
// match view the sheet is the focused match: row = team slot, col = theme ×
// answers + answer, shootout themes after the regular ones. In the stage view
// the matches stack into one ragged sheet (stageMatchAtRow), so moving down
// from a match's last team lands on the next match's first — the spill hosts
// navigate by.
const sheet = createSheetCursor({
  root: ekRoot,
  rows: () => (route.mode === "stage" ? stageRowCount() : state?.participants?.length || 0),
  cols: (row) => (route.mode === "stage" ? columnCount(stageMatchAtRow(row)?.matchState) : columnCount(state)),
  readonly: () => viewer || (route.mode === "match" && Boolean(state?.finished)),
  active: () => !viewer && (route.mode === "match" || route.mode === "stage"),
  coordOf: (cell) => (route.mode === "stage" ? stageCoordOf(cell) : state ? ekCoordOf(cell as HTMLElement, state) : null),
  cellAt: (coord) => (route.mode === "stage" ? stageCellAtCoord(coord) : state ? ekCellAtCoord(ekRoot, coord, state) : null),
  values: "marks",
  applyValues: (edits) => {
    if (route.mode === "stage") stageApplyValues(edits);
    else if (state) ekApplyValues(state.code || currentMatchCode(), state, edits);
  },
  rowsOf: teamRowsOf,
  onActive: (cell) => {
    if (!cell) return;
    const team = Number(cell.dataset.team);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) return;
    activeCell = {matchCode: cell.dataset.matchCode || activeCell.matchCode || currentMatchCode(), team, shootout: cell.dataset.shootout === "1", theme, answer};
    shell.presence.publish();
  },
});
sheet.bind();

// A team's rows: both <tr>s of the two-row table (the name row and the
// player row), found by the data-team the cells share.
function teamRowsOf(cell: HTMLElement): Element[] {
  const table = cell.closest(".match-table");
  const team = cell.dataset.team;
  if (!table || team == null) return [];
  const rows = new Set<Element>();
  table.querySelectorAll(`[data-team="${cssEscape(team)}"]`).forEach((node) => {
    const row = node.closest("tr");
    if (row?.parentElement?.tagName === "TBODY") rows.add(row);
  });
  return Array.from(rows);
}

function answerCountFor(matchState: HostMatchView | null | undefined): number {
  return matchState?.questionValues?.length || 0;
}

function regularThemeCountFor(matchState: HostMatchView | null | undefined): number {
  return matchState?.participants?.[0]?.themes?.length || 0;
}

function ekCoordOf(cell: {dataset: DOMStringMap}, matchState: HostMatchView): CellCoord | null {
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

function ekCellAtCoord(table: HTMLElement, coord: CellCoord | null, matchState: HostMatchView): HTMLElement | null {
  if (!coord) return null;
  const answers = answerCountFor(matchState);
  if (answers <= 0) return null;
  const themeOrder = Math.floor(coord.col / answers);
  const answer = coord.col % answers;
  const regular = regularThemeCountFor(matchState);
  const shootout = themeOrder >= regular;
  const theme = shootout ? themeOrder - regular : themeOrder;
  return table.querySelector<HTMLElement>(
    `.answer-cell[data-team="${cssEscape(String(coord.row))}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(String(theme))}"][data-answer="${cssEscape(String(answer))}"]`,
  );
}

function ekApplyValues(matchCode: string, matchState: HostMatchView, edits: CellEdit[], options: {recordUndo?: boolean} = {}): void {
  if (options.recordUndo !== false && !undoApplying) {
    const reverse = snapshotMatchEdits(matchCode, edits);
    if (reverse.length > 0) {
      pushUndoEntry({
        kind: "match-edits",
        groups: [{matchCode, items: reverse}],
        selection: captureSelection(),
      });
    }
  }
  const payloads: EKCellPayload[] = [];
  for (const {cell, value} of edits) {
    const mark = value === "right" ? "right" : value === "wrong" ? "wrong" : "";
    setMarkClass(cell, mark);
    const team = Number((cell as HTMLElement).dataset.team);
    const theme = Number((cell as HTMLElement).dataset.theme);
    const answer = Number((cell as HTMLElement).dataset.answer);
    const shootout = (cell as HTMLElement).dataset.shootout === "1";
    const target = shootout ? shootoutThemesFor(matchState.participants[team])[theme] : matchState.participants[team]?.themes?.[theme];
    if (target?.answers) target.answers[answer] = mark;
    const payload: EKCellPayload = {team, theme, answer, mark};
    if (shootout) payload.shootout = true;
    payloads.push(payload);
  }
  queueEKEdits(matchCode, payloads);
}

function snapshotMatchEdits(matchCode: string, edits: CellEdit[]): UndoEditItem[] {
  const out: UndoEditItem[] = [];
  for (const {cell, value} of edits) {
    const team = Number((cell as HTMLElement).dataset.team);
    const theme = Number((cell as HTMLElement).dataset.theme);
    const answer = Number((cell as HTMLElement).dataset.answer);
    if (!Number.isInteger(team) || !Number.isInteger(theme) || !Number.isInteger(answer)) continue;
    const shootout = (cell as HTMLElement).dataset.shootout === "1";
    const previous = cell.classList.contains("right") ? "right"
      : cell.classList.contains("wrong") ? "wrong" : "";
    const target = value === "right" ? "right" : value === "wrong" ? "wrong" : "";
    if (previous === target) continue;
    out.push({team, theme, answer, shootout, previous});
  }
  return out;
}

function captureSelection(): {anchor: CellCoord; focus: CellCoord} | null {
  const anchor = sheet.anchor;
  const focus = sheet.focus;
  if (!anchor || !focus) return null;
  return {
    anchor: {row: anchor.row, col: anchor.col},
    focus: {row: focus.row, col: focus.col},
  };
}

function currentUndoContext(): UndoContext | null {
  if (route.mode === "match") return {mode: "match", matchCode: route.matchCode || null, stageCode: null};
  if (route.mode === "stage") return {mode: "stage", matchCode: null, stageCode: route.stageCode || null};
  return null;
}

function ensureUndoContext(): UndoContext | null {
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

function pushUndoEntry(entry: UndoEntry): void {
  if (!ensureUndoContext()) return;
  undoStack.push(entry);
  while (undoStack.length > UNDO_LIMIT) undoStack.shift();
}

function performUndo(): boolean {
  if (!ensureUndoContext() || undoStack.length === 0) return false;
  const entry = undoStack.pop();
  if (!entry || entry.kind !== "match-edits") return false;
  undoApplying = true;
  try {
    for (const group of entry.groups) {
      const matchCode = group.matchCode;
      const matchState = matchStateFor(matchCode);
      if (!matchState) continue;
      const edits: CellEdit[] = [];
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

function findAnswerCell(matchCode: string, {team, theme, answer, shootout}: UndoEditItem): HTMLElement | null {
  if (route.mode === "match") {
    const node = indexedNode("answer", {team, theme, answer, shootout: shootout ? "1" : "0"});
    if (node) return node;
  }
  return document.querySelector<HTMLElement>(
    `.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${cssEscape(String(team))}"][data-shootout="${shootout ? "1" : "0"}"][data-theme="${cssEscape(String(theme))}"][data-answer="${cssEscape(String(answer))}"]`,
  );
}

function restoreSelectionFromUndoEntry(entry: UndoEntry): void {
  if (!entry.selection) return;
  sheet.select(entry.selection.anchor, entry.selection.focus, {focus: true});
}

function activeSelectionCoord(): CellCoord | null {
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

function buildTable(options: {compact?: boolean} = {}): HTMLTableElement {
  const matchCode = currentMatchCode();
  const hasShootout = shootoutThemeCount() > 0;
  const showPlaceColumn = true;
  const themes = renderedThemeHeaders();
  const rows = state!.participants.map((team, teamIndex) => {
    const themeCellsList: ScoreTableThemeRowSpec[] = [];
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

  const build = individualGame() ? buildFlatScoreTable : buildTwoRowScoreTable;
  const individual = individualGame() ? " individual-blank" : "";
  // A spectator sees every match in the compact stage skin, focused or not.
  const compact = options.compact || viewer;
  const table = build({
    className: compact ? `match-table compact-score-table ek-stage-table${viewer ? " readonly-table" : ""}${individual}` : `match-table${individual}`,
    attrs: {dataset: {matchCode}},
    rowMarkerColumn: !compact,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
    nameHeader: viewer ? readonlyBattleHeader() : battleHeader(),
    placeColumn: showPlaceColumn,
    themes,
    afterThemeHeaders: trailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
  if (!viewer) table.classList.toggle("match-finished", state!.finished);
  return table;
}

function renderedThemeHeaders(): Array<{
  label: string;
  questionLabels: number[];
  questionClassName?: string;
  labelClassName?: string;
  gapHeaderClassName: string;
  gapClassName: string;
}> {
  const themes = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({
      label: S.ek.theme.column(String(theme + 1)),
      questionLabels: state!.questionValues,
      gapHeaderClassName: isLastRenderedTheme(false, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(false, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: S.ek.shootout.column(String(theme + 1)),
      questionLabels: state!.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
      gapHeaderClassName: isLastRenderedTheme(true, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(true, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  return themes;
}

function trailingHeaders(hasShootout: boolean): Array<HTMLElement | {content: string | number; className: string}> {
  const headers: Array<HTMLElement | {content: string | number; className: string}> = viewer ? [] : [shootoutControlsHeader()];
  if (hasShootout) headers.push({content: S.ek.shootout.letter(), className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function teamNameCell(team: HostParticipantView, teamIndex: number): HTMLElement {
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {rowSpan: seatRowSpan()});
  cell.dataset.team = String(teamIndex);
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

function totalCell(team: HostParticipantView, teamIndex: number): HTMLElement {
  const cell = td(team.total, "sticky sticky-total number total-cell", {rowSpan: seatRowSpan()});
  cell.dataset.team = String(teamIndex);
  return cell;
}

function placeCell(team: HostParticipantView, teamIndex: number, matchCode: string): HTMLElement {
  if (viewer) return td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: seatRowSpan(), dataset: {team: teamIndex}});
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "decimal";
  input.value = formatPlace(team.place);
  input.className = "place-input";
  input.disabled = state!.finished;
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
      const nextTeam = clamp(teamIndex + direction, 0, state!.participants.length - 1);
      focusPlaceInput(nextTeam, {select: true, matchCode});
    }
  });
  const cell = document.createElement("td");
  cell.className = "sticky sticky-place number place-cell";
  cell.rowSpan = seatRowSpan();
  cell.dataset.team = String(teamIndex);
  cell.appendChild(input);
  return cell;
}

function themeCells(team: HostParticipantView, teamIndex: number, theme: HostThemeView, themeIndex: number, isShootout: boolean): ScoreTableThemeRowSpec {
  const matchCode = currentMatchCode();
  // A player seats himself: his match row needs no player cell at all.
  const playerCell = individualGame() ? null
    : viewer ? readonlyPlayerCell(teamIndex, theme, themeIndex, isShootout)
    : buildPlayerSelectCell(team, teamIndex, theme, themeIndex, isShootout, matchCode);
  const scoreCell = td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: seatRowSpan()});
  scoreCell.dataset.team = String(teamIndex);
  scoreCell.dataset.shootout = isShootout ? "1" : "0";
  scoreCell.dataset.theme = String(themeIndex);
  const gapClass = isLastRenderedTheme(isShootout, themeIndex) ? "gap shootout-adjacent-gap" : "gap";

  const answers = theme.answers.map((mark, answerIndex) => {
    const cell = document.createElement("td");
    cell.className = `answer-cell theme-block ${mark}`;
    if (answerIndex === 0) {
      cell.classList.add("theme-block-bottom-left");
    }
    const editable = !viewer && !state!.finished;
    if (editable && isActiveCell(teamIndex, isShootout, themeIndex, answerIndex)) {
      cell.classList.add("active");
    }
    if (!viewer) cell.tabIndex = state!.finished ? -1 : 0;
    cell.dataset.team = String(teamIndex);
    cell.dataset.matchCode = matchCode;
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    cell.title = S.ek.answer.title(String(team.name), isShootout ? S.ek.shootout.column(String(themeIndex + 1)) : S.ek.theme.column(String(themeIndex + 1)), String(state!.questionValues[answerIndex]));
    if (editable) {
      // A click is the sheet cursor's; a focus arriving otherwise (Tab) seats it.
      cell.addEventListener("focus", () => {
        if (sheet.activeCell !== cell) selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {focus: false, matchCode});
      });
    }
    return cell;
  });

  if (!playerCell) return {scoreCell, gapClassName: gapClass, answers};
  return {playerCell, scoreCell, gapClassName: gapClass, answers};
}

// readonlyPlayerCell is the spectator's player: a name, not a <select>. Its
// coordinates let patchScoreTable's playerText sync update it in place, and the
// popover is always there so the sync keeps it in step from and to blank.
function readonlyPlayerCell(teamIndex: number, theme: HostThemeView, themeIndex: number, isShootout: boolean): HTMLElement {
  const playerCell = document.createElement("td");
  playerCell.colSpan = state!.questionValues.length;
  playerCell.className = "readonly-player theme-block theme-block-top-left";
  const playerLabel = theme.player || "";
  const playerWrap = document.createElement("span");
  playerWrap.className = "readonly-player-text-wrap";
  const playerText = document.createElement("span");
  playerText.className = "readonly-player-text";
  playerText.dataset.team = String(teamIndex);
  playerText.dataset.shootout = isShootout ? "1" : "0";
  playerText.dataset.theme = String(themeIndex);
  playerText.textContent = playerLabel;
  playerWrap.appendChild(playerText);
  playerCell.appendChild(playerWrap);
  const playerPopover = document.createElement("span");
  playerPopover.className = "popover popover-inline readonly-player-popover";
  playerPopover.textContent = playerLabel;
  playerCell.appendChild(playerPopover);
  return playerCell;
}

// readonlyBattleHeader names the match for a spectator: letter, title and a
// short venue, with the full title in a popover — and no finish toggle or
// venue button, which are the host's.
function readonlyBattleHeader(): HTMLElement {
  const fullLabel = matchTitle();
  const node = th("", "sticky sticky-name battle readonly-battle-head readonly-battle-with-popover");
  const title = document.createElement("span");
  title.className = "readonly-battle-title";
  title.tabIndex = 0;
  title.setAttribute("aria-label", fullLabel);
  title.title = fullLabel;

  const battle = document.createElement("span");
  battle.className = "readonly-battle-name";
  battle.textContent = letteredBoutTitle(currentMatchCode(), state!.title || "");
  title.appendChild(battle);

  const venueLabel = state!.venue ? formatBattleVenueShort(state!.venue) : "";
  if (venueLabel) {
    const venue = document.createElement("span");
    venue.className = "readonly-battle-venue";
    venue.textContent = venueLabel;
    title.appendChild(venue);
  }

  const popover = document.createElement("span");
  popover.className = "popover readonly-battle-popover";
  popover.textContent = fullLabel;
  title.appendChild(popover);
  node.appendChild(title);
  return node;
}

function buildPlayerSelectCell(team: HostParticipantView, teamIndex: number, theme: HostThemeView, themeIndex: number, isShootout: boolean, matchCode: string): HTMLElement {
  const playerCell = document.createElement("td");
  playerCell.colSpan = state!.questionValues.length;
  playerCell.className = "player-cell theme-block theme-block-top-left";

  const editor = document.createElement("div");
  editor.className = "player-editor";

  const selectWrap = document.createElement("span");
  selectWrap.className = "player-select-wrap";
  const select = document.createElement("select");
  select.dataset.playerSelect = "";
  select.dataset.matchCode = matchCode;
  select.dataset.team = String(teamIndex);
  select.dataset.shootout = isShootout ? "1" : "0";
  select.dataset.theme = String(themeIndex);
  select.appendChild(option("", ""));
  const roster = team.roster || [];
  roster.forEach((player) => select.appendChild(option(player.name, player.name)));
  if (theme.player && !roster.some((player) => player.name === theme.player)) {
    select.appendChild(option(theme.player, theme.player));
  }
  select.value = theme.player;
  select.disabled = state!.finished;
  select.addEventListener("change", () => {
    const payload: EKCellPayload = {team: teamIndex, theme: themeIndex, player: select.value};
    if (isShootout) payload.shootout = true;
    updatePlayerSelectOverflow(selectWrap);
    queueEKEdits(matchCode, [payload]);
    // Drop focus off the dropdown so the global keydown handler (which ignores
    // form controls) takes the arrow keys and moves the active cell, instead of
    // the native <select> cycling its options.
    select.blur();
  });
  selectWrap.appendChild(select);
  const playerPopover = document.createElement("span");
  playerPopover.className = "popover popover-inline player-select-popover";
  playerPopover.textContent = selectedPlayerLabel(select);
  selectWrap.appendChild(playerPopover);
  editor.appendChild(selectWrap);

  playerCell.appendChild(editor);
  return playerCell;
}

function trailingCells(team: HostParticipantView, teamIndex: number, hasShootout: boolean): HTMLElement[] {
  const cells = viewer ? [] : [td("", "shootout-controls-cell", {rowSpan: seatRowSpan()})];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    const tiebreakCell = td(shootoutTotal, "number tiebreak-cell", {rowSpan: seatRowSpan()});
    tiebreakCell.dataset.team = String(teamIndex);
    cells.push(tiebreakCell);
  }
  const plusCell = td(team.plus, "number plus-cell", {rowSpan: seatRowSpan()});
  plusCell.dataset.team = String(teamIndex);
  cells.push(plusCell);
  [0, 1, 2, 3, 4].forEach((idx) => {
    const correctCell = td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {rowSpan: seatRowSpan()});
    correctCell.dataset.team = String(teamIndex);
    correctCell.dataset.valueIndex = String(idx);
    cells.push(correctCell);
  });
  return cells;
}

function battleHeader(): HTMLElement {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "sticky sticky-name battle";

  const layout = document.createElement("span");
  layout.className = "battle-layout";

  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = letteredBoutTitle(matchCode, state!.title || matchTitle());
  layout.appendChild(title);

  if (venues.length > 0) {
    const venueButton = document.createElement("button");
    venueButton.type = "button";
    venueButton.className = "btn btn-xs venue-edit-button";
    venueButton.dataset.matchCode = matchCode;
    venueButton.replaceChildren(icon("pencil"));
    venueButton.title = S.ek.venue.edit();
    venueButton.setAttribute("aria-label", S.ek.venue.edit());
    venueButton.addEventListener("click", () => openVenueDialog(matchCode));
    layout.appendChild(venueButton);
  }

  const label = document.createElement("label");
  label.className = "finish-control";
  label.title = S.ek.bout.finished();
  label.setAttribute("aria-label", S.ek.bout.finished());

  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.dataset.matchCode = matchCode;
  checkbox.checked = Boolean(state!.finished);
  checkbox.addEventListener("change", () => {
    setMatchFinished(matchCode, checkbox.checked);
  });
  label.append(checkbox);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function openVenueDialog(matchCode: string): void {
  const matchState = matchStateFor(matchCode);
  if (!matchState) return;
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog venue-dialog";
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
  actions.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.ek.venue.cancel();
  cancel.addEventListener("click", () => dialog.close());
  const save = document.createElement("button");
  save.type = "submit";
  save.className = "btn";
  save.textContent = S.ek.venue.save();
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

function shootoutControlsHeader(): HTMLElement {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "shootout-controls-head";

  const addShootout = document.createElement("button");
  addShootout.type = "button";
  addShootout.className = "btn btn-xs shootout-add-button";
  addShootout.textContent = S.ek.shootout.addLabel();
  addShootout.title = S.ek.shootout.add();
  addShootout.setAttribute("aria-label", S.ek.shootout.add());
  addShootout.disabled = state!.finished;
  addShootout.addEventListener("click", () => {
    const ms = matchStateFor(matchCode);
    if (!ms) return;
    withMatchState(ms, () => {
      activeCell = {matchCode, team: 0, shootout: true, theme: shootoutThemeCount(), answer: 0};
      void sendStructuralOps(matchCode, shootoutThemeOps(matchCode, shootoutThemeCount(), false));
    });
  });
  node.appendChild(addShootout);

  if (shootoutThemeCount() > 0) {
    const deleteButton = document.createElement("button");
    deleteButton.type = "button";
    deleteButton.className = "btn btn-xs theme-delete-button";
    deleteButton.textContent = S.ek.shootout.removeLabel();
    deleteButton.title = S.ek.shootout.remove();
    deleteButton.setAttribute("aria-label", S.ek.shootout.remove());
    deleteButton.disabled = state!.finished;
    deleteButton.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (!window.confirm(S.ek.shootout.removeConfirm())) return;
      const ms = matchStateFor(matchCode);
      if (!ms) return;
      withMatchState(ms, () => removeLastShootoutTheme(matchCode));
    });
    node.appendChild(deleteButton);
  }

  return node;
}

type ScoreTableThemeRowSpec = {playerCell?: HTMLElement; scoreCell: HTMLElement; gapClassName: string; answers: HTMLElement[]};

function handleGlobalKeydown(event: KeyboardEvent): void {
  if ((route.mode !== "match" && route.mode !== "stage") || isFormControl(event.target)) return;
  // event.code is the physical key (layout-independent), so Cmd/Ctrl-Z fires on a
  // Russian layout too — there the Z key reports event.key "я", which the old
  // key-based check missed, so undo did nothing for Cyrillic-keyboard users.
  const isUndoKey = event.code === "KeyZ" || event.key.toLowerCase() === "z" || event.key === "я" || event.key === "Я";
  if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && isUndoKey) {
    if (performUndo()) event.preventDefault();
  }
}

function selectAnswerCell(team: number, shootout: boolean, theme: number, answer: number, options: {matchCode?: string; focus?: boolean} = {}): void {
  activeCell = {matchCode: options.matchCode || currentMatchCode(), team, shootout, theme, answer};
  seatCursor({focus: options.focus !== false});
}

// seatCursor points the sheet cursor at the active cell — after a rebuild, a
// pane switch, or a page-side move — collapsing any range.
function seatCursor(options: {focus: boolean}): void {
  const coord = activeSelectionCoord();
  if (coord) sheet.select(coord, coord, {focus: options.focus, preventScroll: true});
  else sheet.clear();
}

function isActiveMatchRow(matchCode: string, teamIndex: number): boolean {
  return !state!.finished &&
    activeCell.matchCode === matchCode &&
    activeCell.team === teamIndex;
}

function focusActiveCell(options: FocusOptions = {}): void {
  const cell = findActiveCell();
  if (cell) cell.focus(options);
}

function focusPlaceInput(team: number, options: {select?: boolean; preventScroll?: boolean; matchCode?: string} = {}): void {
  const matchCode = options.matchCode || currentMatchCode();
  const input = (indexedNode("placeInput", {team}) ||
    document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${team}"]`)) as HTMLInputElement | null;
  if (!input) return;
  input.focus({preventScroll: options.preventScroll});
  if (options.select) input.select();
}

// The mobile decimal keypad behind the place inputs has no Return key on iOS,
// so this floating ↑/↓ bar is the only way to step between teams without
// dismissing the keypad. Touch-only (installCellNavBar no-ops on desktop, where
// Enter/Tab/arrows already navigate). Shown while a place input is focused.
let ekNavBar: CellNavBar | null = null;
function ensureEKNavBar(): CellNavBar {
  if (ekNavBar) return ekNavBar;
  ekNavBar = installCellNavBar({
    onPrev: () => advanceActivePlaceInput(-1),
    onNext: () => advanceActivePlaceInput(1),
  });
  return ekNavBar;
}

function advanceActivePlaceInput(direction: number): void {
  const input = document.activeElement;
  if (!(input instanceof HTMLInputElement) || !input.classList.contains("place-input")) return;
  input.dispatchEvent(new Event("change")); // commit current value before moving
  const team = Number(input.dataset.team);
  if (!Number.isInteger(team) || !state) return;
  const nextTeam = clamp(team + direction, 0, state.participants.length - 1);
  focusPlaceInput(nextTeam, {select: true, matchCode: input.dataset.matchCode});
}

document.addEventListener("focusin", (event) => {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.classList.contains("place-input")) {
    ensureEKNavBar().show();
  }
});
document.addEventListener("focusout", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement) || !target.classList.contains("place-input")) return;
  // Advancing briefly drops focus before the next input gains it; defer the
  // hide and only act if focus truly left the place column.
  setTimeout(() => {
    const active = document.activeElement;
    if (!(active instanceof HTMLInputElement) || !active.classList.contains("place-input")) {
      ekNavBar?.hide();
    }
  }, 0);
});

function focusFinishToggle(options: {preventScroll?: boolean} = {}): void {
  const input = document.querySelector<HTMLElement>(".finish-toggle");
  if (input) input.focus({preventScroll: options.preventScroll});
}

function focusedPlaceTeamIndex(): number | null {
  const element = document.activeElement;
  if (!(element instanceof HTMLInputElement) || !element.classList.contains("place-input")) {
    return null;
  }
  const team = Number(element.dataset.team);
  return Number.isInteger(team) ? team : null;
}

function isFinishToggleFocused(): boolean {
  const element = document.activeElement;
  return element instanceof HTMLInputElement && element.classList.contains("finish-toggle");
}

function findActiveCell(): HTMLElement | null {
  const matchCode = currentMatchCode();
  const indexed = indexedNode("answer", {
    team: activeCell.team,
    shootout: activeCell.shootout ? "1" : "0",
    theme: activeCell.theme,
    answer: activeCell.answer,
  });
  if (indexed) return indexed;
  return document.querySelector<HTMLElement>(
    `.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${activeCell.team}"][data-shootout="${activeCell.shootout ? "1" : "0"}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActiveCell(team: number, shootout: boolean, theme: number, answer: number): boolean {
  return activeCell.matchCode === currentMatchCode() &&
    activeCell.team === team &&
    activeCell.shootout === shootout &&
    activeCell.theme === theme &&
    activeCell.answer === answer;
}

function normalizeActiveCell(): void {
  if (!state?.participants?.length || totalThemeCount() === 0) return;
  const team = clamp(activeCell.team, 0, state.participants.length - 1);
  const column = clamp(activeCellColumn(), 0, totalThemeCount() * state.questionValues.length - 1);
  activeCell = cellFromColumn(team, column);
}

function activeCellColumn(): number {
  const themeOffset = activeCell.shootout
    ? regularThemeCount() + activeCell.theme
    : activeCell.theme;
  return themeOffset * state!.questionValues.length + activeCell.answer;
}

function cellFromColumn(team: number, column: number): ActiveCell {
  const themeOffset = Math.floor(column / state!.questionValues.length);
  const answer = column % state!.questionValues.length;
  if (themeOffset < regularThemeCount()) {
    return {matchCode: currentMatchCode(), team, shootout: false, theme: themeOffset, answer};
  }
  return {matchCode: currentMatchCode(), team, shootout: true, theme: themeOffset - regularThemeCount(), answer};
}

function removeLastShootoutTheme(matchCode: string = currentMatchCode()): void {
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
  void sendStructuralOps(matchCode, shootoutThemeOps(matchCode, lastTheme, true));
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

function shootoutThemesFor(team: HostParticipantView): HostThemeView[] {
  return team.shootoutThemes || [];
}

function isLastRenderedTheme(isShootout: boolean, themeIndex: number): boolean {
  if (isShootout) {
    return themeIndex === shootoutThemeCount() - 1;
  }
  return shootoutThemeCount() === 0 && themeIndex === regularThemeCount() - 1;
}

// currentRoute reads the page's URL: /host/fest/… is the host's, /fest/… the
// spectator's, and the sub-route after the game is the same for both.
function currentRoute(): EKRoute {
  const path = window.location.pathname;
  const prefix = path.match(/^(\/host)?\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"} as EKRoute;
  }
  const host = Boolean(prefix[1]);
  const festID = prefix[2];
  const gameID = prefix[3];
  const viewerBase = `/fest/${festID}/game/${gameID}`;
  const at = {
    viewer: !host, festID, gameID, viewerBase,
    base: host ? `/host${viewerBase}` : viewerBase,
    apiBase: `/api/fest/${festID}/games/${gameID}`,
    festApi: `/api/fest/${festID}`,
  };
  // A trailing /static segment forces the static snapshot server-side (see
  // handleFestRouter) but leaves the URL in the bar. Strip it before matching
  // the sub-route, else the injected snapshot is rejected as a "missing" route.
  const rest = path.slice(prefix[0].length).replace(/\/static$/, "").replace(/\/$/, "");
  if (rest === "" || rest === "/") return {mode: "grid", ...at};
  if (rest === "/venues") return {mode: "venues", ...at};
  if (rest === "/roster") return {mode: "roster", ...at};
  if (rest === "/stats") return {mode: "stats", ...at};
  if (rest === "/seed-import" && host) return {mode: "seedImport", ...at};
  const match = rest.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), ...at};
  const stage = rest.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), ...at};
  return {mode: "missing"} as EKRoute;
}

function findStage(data: HostFestView | null, code: string): HostStage | undefined {
  const scheme = parseScheme(data!.schemaJson);
  const stages = (scheme?.stages?.length ? scheme.stages : data!.stages || []) as HostStage[];
  return stages.find((stage) => stage.code === code);
}

function findLiveStage(data: HostFestView | null, code: string): HostStage | undefined {
  return (data?.stages || []).find((stage) => stage.code === code);
}

function mergedStage(data: HostFestView | null, code: string): HostStage {
  const schemeStage = findStage(data, code) || ({} as HostStage);
  const liveStage = findLiveStage(data, code) || ({} as HostStage);
  return {
    ...schemeStage,
    ...liveStage,
    config: liveStage.config || schemeStage.config,
    reseedEntries: liveStage.reseedEntries || schemeStage.reseedEntries || [],
  };
}

function breadcrumbCurrentTitle(gameTitle: string): string {
  if (route.mode === "grid") return "";
  if (route.mode === "venues") return S.ek.crumb.venues();
  if (route.mode === "stats") return S.ek.crumb.stats();
  if (route.mode === "seedImport") return S.ek.crumb.seedImport();
  if (route.mode === "match") return state?.title || route.matchCode || "";
  if (route.mode === "stage") {
    // The displayed tabs first: a synthetic stage (standings, round-robin) exists
    // nowhere server-side, and its code is no title for a crumb.
    return ekSchemeStages().find((stage) => stage.code === route.stageCode)?.title ||
      findStage(fest, route.stageCode!)?.title || route.stageCode || "";
  }
  return gameTitle;
}

function setPageMode(mode: string): void {
  ekRoot.classList.toggle("grid-host", mode === "grid");
  // The roster fits the frame and wraps rather than scrolling sideways like a
  // score board, so the host drops its max-content sizing.
  ekRoot.classList.toggle("fits-frame", mode === "roster");
}

function currentGameTitle(): string {
  const scheme = parseScheme(fest?.schemaJson) as {title?: unknown} | null;
  return String(scheme?.title || "").trim();
}

function matchTitle(matchState: HostMatchView = state!): string {
  const venue = matchState.venue ? ` · ${formatBattleVenue(matchState.venue)}` : "";
  return `${matchState.title}${venue}`;
}

function parsePlace(value: string): number | null {
  const normalized = value.trim().replace(",", ".");
  if (normalized === "") return 0;
  const place = Number(normalized);
  if (!Number.isFinite(place) || place < 0) return null;
  return place;
}

bindSPANavigation();
loadCurrent()
  .then(() => {
    indicator.touch();
    liveEvents.connect();
    shell.presence.connect();
  })
  .catch((error) => {
    indicator.fail();
    console.error(error);
  });
