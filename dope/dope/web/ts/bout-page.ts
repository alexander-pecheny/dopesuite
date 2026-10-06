// The bout page: the lifecycle of a Game whose document is many bouts, each
// its own match state (Brain, Hamsa, Troika). mountGameDocument is the same
// thing for a page whose whole document is one blob (OD, KSI); this is its
// counterpart for a page of bouts, built on the same shell.
//
// The page says how to read a bout (parse), how to draw a tab (buildTab) and
// what is its own on the screen. The module owns everything the three pages
// used to write out each, and let drift: the bouts and their parsed states,
// the scopes on the fest stream (fest:, match:<game>:, venues:<fest> and
// game-roster:<game>), the writer with the recorder and the recovery of
// un-acked edits after a reload, the boot order, the render loop with its
// tab bar in the URL, and the host's shared writes: finish, venue and start
// time, the draw and the reseed.
//
// It also owns a bout's address: the one id a bout's box carries
// (boutAnchorID), a group table's (groupAnchorID), the links to both
// (boutHref, groupHref), the scroll and flash when a hash names one, and the
// #@<letter> an old /matches/<letter> address arrives as. The grid tab and
// the reseed tab are drawn here too, the same on every format: a live draw
// panel for a host, and the server's word on whether a reseed can be
// calculated.
//
// And it owns the repaint contract. A page builds each bout's box with
// page.boutBox and says two things about a bout: shape(code), a fingerprint
// of everything the box draws except the marks and the numbers they feed,
// and repaintCells(code), which brings those marks and numbers in line where
// they stand. Whenever a bout's state moves — this host's own edit, the
// server's answer, another host's delta — the module compares the shape with
// the one it drew: the same, and the box is repainted in place; another, and
// the tab is drawn again, keeping each box's size and the view by the box's
// id (steady-redraw.ts).

import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import type {PatchPath, ScopedWriter, SendResult, WriteIntent, WriteRequest} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import type {CursorKind, GameShell} from "./game-shell.js";
import {notifyEmbeddedResize, parseGameRoute} from "./game-page.js";
import type {GameInitLike, GameRoute} from "./game-page.js";
import {fitScrollFade, renderTabBar} from "./widgets.js";
import {gameTabs} from "./game-tabs.js";
import type {GameKind, GameTab} from "./game-tabs.js";
import {RESEED_TAB_CODE} from "./game-tabs.js";
import {hashAnchor, onNavigate, setHashTab, tabFromHash, tabHref} from "./url-state.js";
import {redrawSteady, scrollIntoViewSteady} from "./steady-redraw.js";
import {festLetters} from "./standings.js";
import type {StageRef} from "./standings.js";
import {buildFestGrid, buildReseedStagePanel} from "./fest-grid.js";
import type {FestGridData, FestGridStage} from "./fest-grid.js";
import S from "./i18nstrings.js";
import {boutWhereWhen, buildVenuesTable} from "./venue.js";
import type {Venue} from "./venue.js";
import {createEntrantsTab} from "./entrants.js";
import {createUndo, isUndoKey, valueAt} from "./undo.js";
import type {UndoResult} from "./undo.js";
import type {EntrantsTab} from "./entrants.js";

// A resync or a fest refresh waits this long, so a burst of events costs one
// fetch.
const REFETCH_DEBOUNCE_MS = 250;

// How long the box a link landed on stays marked.
const FLASH_MS = 2500;

// boutAnchorID is the id of a bout's box, on whatever tab draws it: the one
// identity a link, a scroll, a flash and a steady redraw find the bout by.
// Only page.boutBox gives it out.
export function boutAnchorID(code: string): string {
  return `bout-${code}`;
}

// BOUT_BOX_SELECTOR finds every bout's box: page.boutBox marks each with its
// code under data-bout.
export const BOUT_BOX_SELECTOR = "[data-bout]";

// groupAnchorID is the id of a group's table.
export function groupAnchorID(code: string): string {
  return `group-${code}`;
}

// What every bout's view carries, whatever its format: the server's match view
// with the Protocol's document under `state`.
export interface BoutView {
  code?: string;
  title?: string;
  venue?: Venue | null;
  // startsAt: when the bout starts, as the host typed it; absent, no time.
  startsAt?: string;
  finished?: boolean;
  seq?: number;
  state?: unknown;
}

export interface BoutScheme {
  title?: string;
  stages?: unknown[];
}

export interface BoutFest {
  title?: string;
  gameName?: string;
  schemaJson?: unknown;
  stages?: Array<FestGridStage | null>;
}

export interface BoutPageSpec<V extends BoutView, S> {
  app: "brain" | "hamsa" | "troika" | "ek" | "es" | "si";
  // The sheet the tabs draw into, and the strip the tab bar goes in.
  root: HTMLElement;
  tabsRoot: HTMLElement | null;
  init: GameInitLike | null;
  scheme: BoutScheme;
  fest: BoutFest | null;
  // The game's name when neither the fest nor the scheme gives one.
  title: () => string;
  // A bout's document as the page trusts it, read from its view (already
  // overlaid with this page's un-acked edits); blank is a bout not yet seen.
  parse: (view: V) => S;
  blank: () => S;
  // The page's own tabs; the entrants, roster and venues tabs are the
  // module's. The roster tab is the page's to build (each format links it
  // differently) and the module keeps it until the roster changes.
  buildTab: (tab: GameTab | undefined) => HTMLElement;
  buildRoster: () => HTMLElement;
  // Whether a drawn tab fits the frame's width rather than scrolling sideways.
  fitsFrame: (tab: GameTab | undefined, node: HTMLElement) => boolean;
  // The repaint contract. shape is a fingerprint of everything a bout's box
  // draws except its marks and the numbers they feed (seats, themes, the
  // finished tick, the heads' buttons); repaintCells brings the marks and the
  // numbers of a drawn box in line with the state. Left out, every change
  // draws the tab again.
  shape?: (code: string) => string;
  repaintCells?: (code: string) => void;
  cursorKinds: Record<string, CursorKind>;
  activeCursorElement?: () => Element | null;
  // The page's sheet cursors: bound at boot, refreshed after every render.
  cursors?: () => Array<{bind(): void; refresh(): void}>;
  // Old hashes a page still answers to (Brain's, game-tabs.ts canonicalKey).
  canonical?: (tabs: GameTab[], key: string) => string;
  // A host is finishing a bout: what the page writes first (Troika fills the
  // wrong answers a host left blank), in the same gesture, so one undo takes
  // it back with the finish's own effect.
  beforeFinish?: (code: string) => void;
  // The URL moved under the page (back, forward, a typed hash).
  onNavigate?: () => void;
  // The fest view arrived, after the module adopted its stages.
  onFest?: () => void;
  // A team's roster in this Game changed (the roster tab, a player override).
  onRoster?: () => void;
  // Test seams: the shell, the route, the stream.
  shell?: GameShell;
  route?: GameRoute;
  newEventSource?: (url: string) => EventSource;
}

export interface BoutPage<V extends BoutView, S> {
  readonly route: GameRoute;
  readonly viewer: boolean;
  readonly shell: GameShell;
  // Every bout's letter, as the compiler dealt it.
  readonly letters: Map<string, string>;
  tabs(): GameTab[];
  tab(): GameTab | undefined;
  view(code: string): V | undefined;
  stateOf(code: string): S;
  codes(): string[];
  // One cell edit of a bout's document, coalesced and retried by the writer,
  // and remembered for this host's undo.
  patch(code: string, path: PatchPath, value: unknown): void;
  // Take back this host's last edit (undo.ts): only what this page wrote, and
  // no cell another host has changed since. Ctrl+Z (⌘Z) calls it.
  undo(): UndoResult | null;
  // Whether an edit of this page at the path still waits for the server.
  isPending(code: string, path: PatchPath): boolean;
  // A structural write on a bout's scope.
  send(code: string, request: WriteRequest, intent?: WriteIntent): Promise<SendResult>;
  render(): void;
  // A bout's state moved: repaint its box in place when its shape is the one
  // drawn, else draw the tab again. A bout the tab in front does not draw
  // leaves a tab of boxes alone.
  refresh(code: string): void;
  // A bout's box, the one way a page makes one: the element with the bout's
  // id (boutAnchorID) and its code under data-bout. Drawing it tells the
  // module the bout is on the tab in front.
  boutBox(code: string, className: string): HTMLElement;
  // The bout's box as drawn now, or null.
  boxOf(code: string): HTMLElement | null;
  // Fetch every bout again, soon; a burst of reasons fetches once.
  resync(): void;
  // Drop the roster tab, so the next drawing builds it afresh.
  invalidateRoster(): void;
  // The fest's venues, one array kept in place, so a dialog opened from an
  // older drawing still lists the current ones.
  readonly venues: Venue[];
  festStage(code: string): FestGridStage | undefined;
  // The grid's stages: the fest view's, as fresh as the page has them.
  gridStages(): FestGridStage[];
  // Where and when a bout is played, with the host's pencil.
  whereWhen(code: string, options: {title: string; venueAlways: boolean; className: string}): HTMLElement[];
  // The finished tick for a bout: with the word beside it, or the word as its
  // tooltip where the column is narrow.
  finishToggle(code: string, options: {text?: string; title?: string}): HTMLLabelElement;
  finish(code: string, finished: boolean): void;
  // Seat a drawn Slot; the bouts are read again whatever the server answered.
  draw(slot: string, participant: number): Promise<void>;
  // Calculate a reseed; the fest view and the bouts it seats are read again.
  // A refusal is kept and shown on the reseed's panel until the next try.
  reseed(code: string): Promise<SendResult>;
  // Where a link to a bout leads: the tab that holds it, at the bout's box,
  // by its letter. "" when no tab holds it.
  boutHref(code: string): string;
  // Where a link to a group leads: the tab with the group's table, at it.
  groupHref(code: string): string;
  // A grid with the module's links and draw panel: the whole Game's, or the
  // slice a tab draws (a Swiss Block's rounds, Brain's pod board).
  grid(data?: FestGridData): HTMLElement;
  // Bind the cursors, connect, fetch the bouts, then recover and show presence.
  start(): void;
}

export function mountBoutPage<V extends BoutView, S>(spec: BoutPageSpec<V, S>): BoutPage<V, S> {
  const {root, scheme, fest} = spec;
  const route = spec.route || parseGameRoute();
  const viewer = Boolean(route.viewer);
  // An embedded view (?embed=1, a bout in another site's frame) is the sheet
  // alone: no tabs, no menu items, no presence; it tells its frame its height.
  const embedded = new URLSearchParams(window.location?.search || "").get("embed") === "1";
  if (embedded) document.body?.classList.add("embedded-match");
  const shell = spec.shell || mountGamePage({
    embedded,
    // Erudit-Sextet and individual SI are EK's page to the shell.
    app: spec.app === "es" || spec.app === "si" ? "ek" : spec.app,
    root,
    statusNode: document.getElementById("status"),
    breadcrumbsNode: document.getElementById("gameBreadcrumbs"),
    festID: route.festID,
    gameID: route.gameID,
    viewer,
    apiBase: route.apiBase,
    init: spec.init,
    // Every bout format exports its sheets (games.Definition.Sheets).
    chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme.title || spec.title()}),
    cursorKinds: spec.cursorKinds,
    activeCursorElement: spec.activeCursorElement,
  });
  const {scopeGameID, indicator} = shell;
  const apiBase = route.apiBase || "";
  const festAPI = `/api/fest/${encodeURIComponent(String(route.festID || ""))}`;
  const matchPrefix = `match:${scopeGameID}:`;
  const venuesScope = `venues:${route.festID}`;
  const letters = festLetters(fest?.stages as StageRef[] | undefined);

  const views = new Map<string, V>();
  const states = new Map<string, S>();
  const festStages = new Map<string, FestGridStage>();
  adoptFest(fest);
  const venues: Venue[] = [];
  let rosterView: HTMLElement | null = null;
  // The entrants tab is built the first time it is drawn; after a change the
  // bouts are fetched again, since seats moved, and a rebuilt Structure reloads.
  let entrants: EntrantsTab | null = null;
  const entrantsTab = () => (entrants ||= createEntrantsTab({
    apiBase,
    onChanged: () => resync(),
    onRebuilt: () => window.location.reload(),
  }));

  const matchScope = (code: string) => matchPrefix + code;
  const matchURL = (code: string, suffix: string) => `${apiBase}/matches/${encodeURIComponent(code)}/${suffix}`;

  // ---- tabs, in the URL's hash ----

  const tabs = () => gameTabs((scheme.stages || []) as StageRef[], {game: spec.app as GameKind, viewer});
  const fromHash = () => tabFromHash(tabs(), {canonical: spec.canonical});
  openLandingTab();
  let activeTab = fromHash() || "grid";
  const currentTab = () => tabs().find((tab) => tab.key === activeTab);

  onNavigate(() => {
    openLandingTab();
    const next = fromHash();
    if (next && next !== activeTab) {
      activeTab = next;
      render();
    }
    showAnchor();
    spec.onNavigate?.();
  });

  // ---- a bout's address ----

  function schemeStages(): StageRef[] {
    return (scheme.stages || []) as StageRef[];
  }

  // boutCode reads a bout's letter, or its code, as the bout's code; "" when
  // it names no bout.
  function boutCode(named: string): string {
    for (const [code, letter] of letters) if (letter === named || code === named) return code;
    for (const stage of schemeStages()) {
      if ((stage.matches || []).some((match) => match.code === named)) return named;
    }
    return "";
  }

  // boutTab is the tab that draws a bout's box: a stage's tab or a Block's
  // protocols, or a Block round gathered across its groups.
  function boutTab(code: string): GameTab | undefined {
    const stage = schemeStages().find((entry) => (entry.matches || []).some((match) => match.code === code));
    return tabs().find((tab) => {
      if (tab.kind === "round") return (tab.stage?.matches || []).some((match) => match.code === code);
      return (tab.kind === "stage" || tab.kind === "protocol") && Boolean(stage) && tab.stages.includes(stage!.code);
    });
  }

  // groupTab is the tab with a group's table.
  function groupTab(code: string): GameTab | undefined {
    return tabs().find((tab) => (tab.kind === "block" || tab.kind === "pods") && tab.stages.includes(code));
  }

  // stageTab is the tab a grid column's head leads to: the first that draws
  // the stage.
  function stageTab(code: string): GameTab | undefined {
    return tabs().find((tab) => tab.kind !== "grid" && tab.stages.includes(code));
  }

  function boutHref(code: string): string {
    const tab = boutTab(code);
    return tab ? tabHref(tab.key, letters.get(code) || code) : "";
  }

  function groupHref(code: string): string {
    const tab = groupTab(code);
    return tab ? tabHref(tab.key, groupAnchorID(code)) : "";
  }

  // openLandingTab puts in the hash the tab of what a hash with no tab names
  // (#@A: what an old /matches/<letter> address now arrives as), so the page
  // opens that tab and scrolls there.
  function openLandingTab(): void {
    const anchor = hashAnchor();
    if (!anchor || !(window.location.hash || "").startsWith("#@")) return;
    const group = anchor.startsWith("group-") ? anchor.slice("group-".length) : "";
    const tab = group ? groupTab(group) : boutTab(boutCode(anchor));
    if (tab) window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${tabHref(tab.key, anchor)}`);
  }

  // anchorNode is the box the hash's anchor names: a bout by its letter or
  // code, or a group's table. A Troika link used to name a group by its bare
  // code (#block:s1@s1-g3); that still finds it.
  function anchorNode(anchor: string): HTMLElement | null {
    const byID = (id: string) => document.getElementById?.(id) || null;
    if (anchor.startsWith("group-")) return byID(anchor);
    const code = boutCode(anchor);
    return (code ? byID(boutAnchorID(code)) : null) || byID(groupAnchorID(anchor));
  }

  // showAnchor scrolls to what the hash names, once per tab and anchor: a
  // later redraw (another host's mark) must not pull the page back to it.
  let shownAnchor = "";
  const flash = createFlash();
  function showAnchor(): void {
    const anchor = hashAnchor();
    const key = `${activeTab}@${anchor}`;
    if (!anchor || key === shownAnchor) return;
    const node = anchorNode(anchor);
    if (!node) return;
    shownAnchor = key;
    scrollIntoViewSteady(node);
    flash.mark(node);
  }

  // ---- the bouts ----

  function adoptFest(view: BoutFest | null | undefined): void {
    for (const stage of view?.stages || []) if (stage?.code) festStages.set(stage.code, stage);
  }

  // adoptView takes a bout's view from wherever it arrives — the fetch, a
  // write's answer, the stream — with this page's un-acked edits overlaid, so a
  // slow write never visibly regresses. An older view than the one held is
  // dropped.
  function adoptView(view: V | null | undefined): boolean {
    const code = view?.code;
    if (!view || !code) return false;
    const cached = views.get(code);
    if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
    view = writer.overlay(matchScope(code), view);
    // The grid's heads carry each bout's venue and time from the fest view,
    // which a move of the bout leaves stale.
    if (cached && ((cached.venue?.number || 0) !== (view.venue?.number || 0) || (cached.startsAt || "") !== (view.startsAt || ""))) refreshFest();
    views.set(code, view);
    states.set(code, spec.parse(view));
    return true;
  }

  function gridStages(): FestGridStage[] {
    return (fest?.stages || []).flatMap((stage) => stage?.code ? [festStages.get(stage.code) || stage] : []);
  }

  // ---- the repaint contract ----

  // drawing collects the bouts whose boxes the tab being built draws; drawn
  // is each one's shape as the page last drew it. A repaint is safe only
  // against what is on screen: this host's own marks are in the state before
  // the server answers them, so the state just before an answer can match the
  // answer while the box on screen is older than both.
  let drawing = new Set<string>();
  const drawn = new Map<string, string>();

  function boutBox(code: string, className: string): HTMLElement {
    const box = document.createElement("section");
    box.className = className;
    box.id = boutAnchorID(code);
    box.dataset.bout = code;
    drawing.add(code);
    return box;
  }

  function boxOf(code: string): HTMLElement | null {
    return document.getElementById?.(boutAnchorID(code)) || null;
  }

  function refresh(code: string): void {
    const before = drawn.get(code);
    if (before === undefined) {
      // A tab of boxes draws nothing of a bout it holds no box for, unless
      // the bout belongs there and only now arrived.
      if (drawn.size === 0 || boutTab(code)?.key === activeTab) render();
      return;
    }
    if (!spec.shape || !spec.repaintCells || spec.shape(code) !== before) {
      render();
      return;
    }
    spec.repaintCells(code);
    for (const cursor of spec.cursors?.() || []) cursor.refresh();
  }

  async function fetchMatches(): Promise<void> {
    const response = await fetch(`${apiBase}/stages/matches`);
    if (!response.ok) throw new Error(`stages/matches ${response.status}`);
    const stages = await response.json() as Array<{code?: string; matches?: V[]}>;
    for (const stage of stages || []) {
      for (const view of stage.matches || []) adoptView(view);
    }
    render();
  }

  let resyncScheduled = false;
  function resync(): void {
    if (resyncScheduled) return;
    resyncScheduled = true;
    window.setTimeout(() => {
      resyncScheduled = false;
      fetchMatches().catch(() => indicator.fail());
    }, REFETCH_DEBOUNCE_MS);
  }

  // refreshFest reads the fest view again: the grid's heads carry each bout's
  // venue from it, and a venue moved or renamed leaves them stale.
  let festRefresh = 0;
  function refreshFest(): void {
    window.clearTimeout(festRefresh);
    festRefresh = window.setTimeout(async () => {
      try {
        const response = await fetch(apiBase);
        if (!response.ok) return;
        adoptFest(await response.json() as BoutFest);
        render();
      } catch (error) {
        console.error(error);
      }
    }, REFETCH_DEBOUNCE_MS);
  }

  function adoptVenues(list: unknown): boolean {
    if (!Array.isArray(list)) return false;
    venues.splice(0, venues.length, ...(list as Venue[]));
    return true;
  }

  // Only a host edits a bout's venue, and only a Game with a venues tab lists
  // them to a spectator.
  async function fetchVenues(): Promise<void> {
    if (viewer && !tabs().some((tab) => tab.kind === "venues")) return;
    const response = await fetch(`${festAPI}/venues`);
    if (!response.ok) throw new Error(`venues ${response.status}`);
    adoptVenues(await response.json());
    render();
  }

  // ---- the stream and the writer ----

  const live = createLiveEvents({
    eventsURL: () => gameEventsURL(route.festID!, route.gameID),
    gameID: scopeGameID,
    scopes: [{
      // The server broadcasts the whole fest view after every write; the
      // tables in it — every Ranker's standings — are the page's to draw.
      prefix: "fest:",
      adopt: (_scope, view) => {
        const fresh = view.data as BoutFest | null;
        if (!fresh?.stages) return;
        adoptFest(fresh);
        entrants?.refresh();
        spec.onFest?.();
        render();
      },
    }, {
      // A host renamed, added or removed a venue: the venues tab and the
      // bouts' heads say so, and an added venue may have seated bouts.
      prefix: venuesScope,
      adopt: (_scope, view) => {
        if (!adoptVenues(view.data)) return;
        resync();
        refreshFest();
        render();
      },
    }, {
      prefix: matchPrefix,
      base: (scope) => {
        const cached = views.get(scope.slice(matchPrefix.length));
        return cached ? {data: cached, seq: Number(cached.seq || 0)} : null;
      },
      adopt: (_scope, view) => {
        const next = view.data as V | null;
        if (!next?.code) {
          resync();
          return;
        }
        next.seq = view.seq;
        if (adoptView(next)) refresh(next.code);
      },
      gap: () => resync(),
    }, {
      // A team's roster in this Game changed: the bouts carry the seat
      // rosters, so they are fetched again, and the roster tab with them.
      prefix: `game-roster:${scopeGameID}`,
      adopt: (scope) => {
        if (scope !== `game-roster:${scopeGameID}`) return;
        rosterView = null;
        resync();
        spec.onRoster?.();
        render();
      },
    }],
    indicator,
    onViewers: (count) => shell.viewerCounter.setCount(count),
    onLockdown: scheduleStaticReload,
    reload: fetchMatches,
    onRecoverError: (error) => {
      indicator.fail();
      console.error(error);
    },
    staticMode: () => shell.staticMode,
    recorder: () => shell.recorder,
    newEventSource: spec.newEventSource,
  });

  const writer: ScopedWriter = createScopedWriter({
    readonly: viewer,
    urlOf: (scope) => matchURL(scope.slice(matchPrefix.length), "state"),
    // Ops address the bout's Protocol document, which the view carries as `state`.
    docPath: ["state"],
    adopt: (scope, response) => {
      if (scope.startsWith(matchPrefix)) {
        const view = response as V | null;
        if (adoptView(view) && view?.code) refresh(view.code);
        return;
      }
      if (scope === venuesScope) return; // venueEditor's callback draws it
      // A reseed or a draw answers with the fest view.
      adoptFest(response as BoutFest);
      render();
    },
    indicator,
    recorder: () => shell.recorder,
    onRejected: (info) => {
      shell.recorder?.event("write-rejected", info);
      resync();
    },
  });

  // ---- this host's undo ----

  const currentAt = (code: string, path: PatchPath) => {
    const view = views.get(code);
    return view ? valueAt((writer.overlay(matchScope(code), view) as {state?: unknown}).state, path) : undefined;
  };
  const undoStack = createUndo({
    current: currentAt,
    apply: (code, path, value) => writer.patch(matchScope(code), path, value ?? null),
  });

  function patch(code: string, path: PatchPath, value: unknown): void {
    if (!viewer) undoStack.record(code, path, currentAt(code, path), value);
    writer.patch(matchScope(code), path, value);
  }

  function undo(): UndoResult | null {
    const result = undoStack.undo();
    if (!result) return null;
    for (const code of result.codes) {
      const view = views.get(code);
      if (view) states.set(code, spec.parse(writer.overlay(matchScope(code), view)));
    }
    if (result.skipped) shell.recorder?.event("undo-skipped", {skipped: result.skipped});
    if (result.codes.length === 1) refresh(result.codes[0]);
    else if (result.codes.length) render();
    return result;
  }

  function onUndoKey(event: KeyboardEvent): void {
    if (!isUndoKey(event)) return;
    // A text box keeps its own undo.
    const target = event.target as HTMLElement | null;
    if (target?.closest("input, textarea, [contenteditable]")) return;
    event.preventDefault();
    undo();
  }

  const venueEdits = venueEditor({
    writer,
    festID: String(route.festID || ""),
    festAPI,
    adopt: (list, change) => {
      adoptVenues(list);
      // The server seats at a new venue the bouts the scheme puts there.
      if (change === "add") resync();
      refreshFest();
      render();
    },
  });

  // ---- the render loop ----

  function buildTab(tab: GameTab | undefined): HTMLElement {
    switch (tab?.kind) {
    case "entrants":
      return entrantsTab().element();
    case "roster":
      return (rosterView ||= spec.buildRoster());
    case "venues":
      return buildVenuesTable(venues, {
        editable: !viewer,
        onTitleChange: (number, title) => void venueEdits.rename(number, title),
        onAdd: venueEdits.add,
        onDelete: venueEdits.remove,
      });
    case "reseed":
      return reseedTab(tab);
    case "grid":
    case undefined:
      return grid();
    default:
      return spec.buildTab(tab);
    }
  }

  function grid(data?: FestGridData): HTMLElement {
    return buildFestGrid(data || {schemaJson: fest?.schemaJson, stages: gridStages()}, {
      letters,
      editable: !viewer,
      onDraw: (slot, participant) => void draw(slot, participant),
      stageHref: (stage) => {
        const tab = stageTab(stage.code || "");
        return tab ? tabHref(tab.key) : "";
      },
      matchHref: boutHref,
      groupHref: (stage) => groupHref(stage.code || ""),
    });
  }

  // reseedTab stacks a tab's reseeds. Where a tab holds several, each goes
  // under the name of the stage it seats; a lone reseed keeps its panel bare.
  function reseedTab(tab: GameTab): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "reseed-fold";
    const headed = tab.key === `stage:${RESEED_TAB_CODE}` || tab.stages.length > 1;
    for (const code of tab.stages) {
      if (headed) {
        const head = document.createElement("h3");
        head.className = "reseed-fold-head";
        head.textContent = seatedStageTitle(code);
        wrap.appendChild(head);
      }
      const live = festStages.get(code);
      wrap.appendChild(buildReseedStagePanel(live || {code}, {
        editable: !viewer,
        canCalculate: Boolean(live?.reseedReady),
        blockedMessage: reseedWaits(live),
        letters,
        error: reseedErrors.get(code) || "",
        onCalculate: () => void reseed(code),
      }));
    }
    return wrap;
  }

  // reseedWaits says what a reseed the server holds back still waits for:
  // the unfinished bouts by their letters, or, once every bout is done, the
  // stages whose places are not settled yet.
  function reseedWaits(live: FestGridStage | undefined): string {
    const pending = (live?.reseedPendingMatches || []).map((code) => String(code || "")).filter(Boolean);
    const bouts = pending.filter((code) => boutCode(code)).map((code) => letters.get(code) || code);
    if (bouts.length === 1) return S.fest.reseed.blockedOne(bouts[0]);
    if (bouts.length > 1) return S.fest.reseed.blockedMany(bouts.join(", "));
    const stages = pending.map((code) => schemeStages().find((stage) => stage.code === code)?.title || code);
    return stages.length ? S.fest.reseed.blockedStages(stages.join(", ")) : "";
  }

  // seatedStageTitle is the name of the stage a reseed seats: the next one
  // after it that is not a reseed itself.
  function seatedStageTitle(code: string): string {
    const stages = schemeStages();
    const index = stages.findIndex((stage) => stage.code === code);
    const isReseed = (stage: StageRef) => [stage.stage_type, stage.type, stage.kind].includes("reseed");
    return String(stages.slice(index + 1).find((stage) => !isReseed(stage))?.title || "");
  }

  // drawnTab is the tab the page last drew: a redraw of it keeps the view.
  let drawnTab = "";

  function render(): void {
    shell.renderChrome();
    if (spec.tabsRoot && !embedded) {
      spec.tabsRoot.hidden = false;
      renderTabBar(spec.tabsRoot, tabs(), activeTab, (key) => {
        activeTab = key;
        setHashTab(key);
        render();
      });
    }
    const tab = currentTab();
    drawing = new Set();
    const node = buildTab(tab);
    drawn.clear();
    for (const code of drawing) drawn.set(code, spec.shape?.(code) ?? "");
    const keepView = drawnTab === activeTab;
    const frame = root.closest(".sheet-frame");
    const scrollTop = frame?.scrollTop || 0;
    // Every mark another host saves redraws the tab; the bouts keep their
    // sizes and the one in view stays put, so the sheet does not jump under
    // the cursor. A tab of no bouts keeps its scroll the same way.
    redrawSteady(root, BOUT_BOX_SELECTOR, () => {
      root.replaceChildren(node);
      if (keepView && frame) frame.scrollTop = scrollTop;
    }, keepView);
    drawnTab = activeTab;
    root.classList.toggle("fits-frame", spec.fitsFrame(tab, node));
    // A grid fits the frame's width like EK's, so its columns measure the same.
    root.classList.toggle("grid-host", node.matches(".fest-grid") || Boolean(node.querySelector(".fest-grid")));
    for (const cursor of spec.cursors?.() || []) cursor.refresh();
    shell.presence.refresh();
    flash.redraw();
    showAnchor();
    notifyEmbeddedResize(embedded);
  }

  // ---- the host's writes ----

  function finish(code: string, finished: boolean): void {
    if (finished && !viewer && spec.beforeFinish) {
      // What the page writes first must land before the bout is finished,
      // since a finished bout takes no more marks.
      spec.beforeFinish(code);
      void writer.flush().then(() => sendFinish(code, finished));
      return;
    }
    sendFinish(code, finished);
  }

  // sendFinish overlays the finished flag on the bout's view at once, so the
  // sheet reads it from the tick: a bout the host just unticked takes marks
  // before the server has answered.
  function sendFinish(code: string, finished: boolean): void {
    const sent = writer.send(matchScope(code), {url: matchURL(code, "finish"), body: {finished}}, {path: ["finished"], value: finished});
    const view = views.get(code);
    if (view) {
      const overlaid = writer.overlay(matchScope(code), view);
      views.set(code, overlaid);
      states.set(code, spec.parse(overlaid));
      refresh(code);
    }
    void sent;
  }

  async function draw(slot: string, participant: number): Promise<void> {
    // The server holds the choice to the seat's own candidates, so a refusal
    // is a refusal, and the page reloads what it answered either way.
    await writer.send(`draw:${slot}`, {url: `${apiBase}/draw`, method: "PUT", body: {slot, participant}});
    await fetchMatches().catch(() => indicator.fail());
  }

  // reseedErrors is why the server refused each reseed's last calculation.
  const reseedErrors = new Map<string, string>();

  async function reseed(code: string): Promise<SendResult> {
    const sent = await writer.send(`stage:${code}`, {url: `${apiBase}/stages/${encodeURIComponent(code)}/reseed`});
    if (sent.ok) {
      reseedErrors.delete(code);
      await fetchMatches().catch(() => indicator.fail());
    } else {
      reseedErrors.set(code, sent.error || S.fest.reseed.calculateFailed());
      render();
    }
    return sent;
  }

  // ---- boot ----

  // recover re-sends the un-acked edits a previous load persisted (a refresh
  // mid-sync), once their bouts are here to show them on, overlaid.
  function recover(): void {
    let recovered = false;
    for (const [code, view] of views) {
      if (writer.recover(matchScope(code)) === 0) continue;
      recovered = true;
      states.set(code, spec.parse(writer.overlay(matchScope(code), view)));
    }
    if (recovered) render();
  }

  function start(): void {
    for (const cursor of spec.cursors?.() || []) cursor.bind();
    if (!viewer) document.addEventListener("keydown", onUndoKey);
    render();
    fitScrollFade(root.closest(".sheet-frame"));
    live.connect();
    fetchMatches()
      .then(() => {
        recover();
        shell.presence.connect();
        indicator.touch();
      })
      .catch((error: unknown) => {
        indicator.fail();
        console.error(error);
      });
    fetchVenues().catch(() => indicator.fail());
  }

  return {
    route,
    viewer,
    shell,
    letters,
    tabs,
    tab: currentTab,
    view: (code) => views.get(code),
    stateOf: (code) => states.get(code) || spec.blank(),
    codes: () => [...views.keys()],
    patch,
    undo,
    isPending: (code, path) => writer.isPending(matchScope(code), path),
    send: (code, request, intent) => writer.send(matchScope(code), request, intent),
    render,
    refresh,
    boutBox,
    boxOf,
    resync,
    invalidateRoster: () => { rosterView = null; },
    venues,
    festStage: (code) => festStages.get(code),
    gridStages,
    whereWhen: (code, options) => {
      const view = views.get(code);
      return boutWhereWhen({
        ...options,
        venue: view?.venue,
        startsAt: view?.startsAt,
        host: viewer ? undefined : {
          venues,
          pickVenue: (number) => void writer.send(matchScope(code), {url: matchURL(code, "venue"), body: {number}}),
          saveStartsAt: (time, wave) => void writer.send(matchScope(code), {url: matchURL(code, "starts-at"), body: {time, wave}}),
        },
      });
    },
    finishToggle: (code, options) => finishControl({
      ...options,
      code,
      finished: Boolean(views.get(code)?.finished),
      disabled: viewer,
      onChange: (finished) => finish(code, finished),
    }),
    finish,
    draw,
    reseed,
    boutHref,
    groupHref,
    grid,
    start,
  };
}

// createFlash marks the box a link landed on for a moment: the mark fades out
// by itself and goes at the host's first click, so it never stays on. A redraw
// in the meantime (the bouts arriving, another host's mark) builds the box
// anew, so redraw puts the mark on the new one, as far into its fade as the
// old one was.
function createFlash(): {mark(node: HTMLElement): void; redraw(): void} {
  let id = "";
  let since = 0;
  let timer = 0;
  const clear = () => {
    document.getElementById?.(id)?.classList.remove("bout-target");
    id = "";
    window.clearTimeout(timer);
    document.removeEventListener("pointerdown", clear, true);
  };
  const paint = () => {
    const node = id ? document.getElementById?.(id) : null;
    if (!node) return;
    node.style.animationDelay = `${since - Date.now()}ms`;
    node.classList.add("bout-target");
  };
  return {
    mark(node) {
      clear();
      id = node.id;
      since = Date.now();
      paint();
      timer = window.setTimeout(clear, FLASH_MS);
      document.addEventListener("pointerdown", clear, true);
    },
    redraw: paint,
  };
}

// ---- the helpers EK shares ----

export interface FinishControlOptions {
  code: string;
  finished: boolean;
  // The word beside the tick; or, where the name column is too narrow for it,
  // the label's tooltip.
  text?: string;
  title?: string;
  disabled?: boolean;
  // The data-* key the tick carries the bout's code under; presence finds it
  // by that key. "match" unless the page names its cells otherwise.
  dataKey?: string;
  onChange: (finished: boolean) => void;
}

// tabStages is a tab's stages, in the scheme's order.
export function tabStages<T extends {code?: string}>(stages: readonly T[] | undefined, tab: GameTab | undefined): T[] {
  return (stages || []).filter((stage) => (tab?.stages || []).includes(stage.code || ""));
}

// BoutEntry is one bout of a stage that the page has a view of: its code, its
// view, the scheme's match it was planned as, and its stage.
export interface BoutEntry<V, St, M> {
  code: string;
  view: V;
  planned: M;
  stage: St;
}

// stageBouts is a stage's bouts that the page has a view of, in the scheme's
// order.
export function stageBouts<V extends BoutView, S, M extends {code?: string}, St extends {matches?: M[]}>(
  page: BoutPage<V, S>, stage: St,
): Array<BoutEntry<V, St, M>> {
  const out: Array<BoutEntry<V, St, M>> = [];
  for (const planned of stage.matches || []) {
    const code = planned.code || "";
    const view = page.view(code);
    if (view) out.push({code, view, planned, stage});
  }
  return out;
}

// seatRoster is the people a seat may field. The server sends each seat's
// roster with real player ids (store.SeatsPlayers), which is what a theme or a
// chair records: a name matched off the fest registry would not survive two
// players sharing one.
export function seatRoster(
  view: {participants?: Array<{roster?: Array<{id?: number; name?: string}>} | null | undefined>}, seat: number,
): Array<{id: number; name: string}> {
  return (view.participants?.[seat]?.roster || [])
    .filter((player) => player && typeof player.id === "number" && player.id > 0)
    .map((player) => ({id: Number(player.id), name: player.name || ""}));
}

// finishControl is a bout's finished tick: a finished bout's sheet is
// read-only until the host unticks it, and the server rejects edits to it.
export function finishControl(options: FinishControlOptions): HTMLLabelElement {
  const label = document.createElement("label");
  label.className = "finish-control";
  if (options.title) {
    label.title = options.title;
    label.setAttribute("aria-label", options.title);
  }
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = options.finished;
  checkbox.disabled = Boolean(options.disabled);
  checkbox.dataset[options.dataKey || "match"] = options.code;
  checkbox.addEventListener("change", () => options.onChange(checkbox.checked));
  label.append(checkbox);
  if (options.text) {
    const text = document.createElement("span");
    text.textContent = options.text;
    label.append(text);
  }
  return label;
}

export type VenueChange = "rename" | "add" | "delete";

export interface VenueEditor {
  // Each resolves to why the server refused, or to an empty string.
  rename(number: number, title: string): Promise<string>;
  add(title: string, number: number): Promise<string>;
  remove(number: number): Promise<string>;
}

// venueEditor is the host's edits to the fest's venues, which every Game of
// the fest shares. Each goes out on the venues:<fest> scope and answers with
// the whole list, which adopt is handed.
export function venueEditor(options: {
  writer: ScopedWriter;
  festID: string;
  festAPI: string;
  adopt: (venues: Venue[], change: VenueChange) => void;
}): VenueEditor {
  const scope = `venues:${options.festID}`;
  const at = (number: number) => `${options.festAPI}/venues/${encodeURIComponent(number)}`;
  async function run(request: WriteRequest, change: VenueChange): Promise<string> {
    const sent = await options.writer.send(scope, request);
    if (!sent.ok) return sent.error || "";
    if (Array.isArray(sent.response)) options.adopt(sent.response as Venue[], change);
    return "";
  }
  return {
    rename: (number, title) => run({url: at(number), method: "PUT", body: {title}}, "rename"),
    add: (title, number) => run({url: `${options.festAPI}/venues`, method: "POST", body: number > 0 ? {title, number} : {title}}, "add"),
    remove: (number) => run({url: at(number), method: "DELETE"}, "delete"),
  };
}
