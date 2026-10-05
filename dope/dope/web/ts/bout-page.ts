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
// EK keeps its own router and stage cache; it borrows finishControl and
// venueEditor from here.

import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import type {PatchPath, ScopedWriter, SendResult, WriteIntent, WriteRequest} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import type {CursorKind, GameShell} from "./game-shell.js";
import {notifyEmbeddedResize, parseGameRoute} from "./game-page.js";
import type {GameInitLike, GameRoute} from "./game-page.js";
import {fitScrollFade, renderTabBar} from "./widgets.js";
import {gameTabs} from "./game-tabs.js";
import type {GameKind, GameTab} from "./game-tabs.js";
import {onNavigate, setHashTab, tabFromHash} from "./url-state.js";
import {redrawSteady} from "./steady-redraw.js";
import {festLetters} from "./standings.js";
import type {StageRef} from "./standings.js";
import type {FestGridStage} from "./fest-grid.js";
import {boutWhereWhen, buildVenuesTable} from "./venue.js";
import type {Venue} from "./venue.js";
import {createEntrantsTab} from "./entrants.js";
import {createUndo, isUndoKey, valueAt} from "./undo.js";
import type {UndoResult} from "./undo.js";
import type {EntrantsTab} from "./entrants.js";

// A resync or a fest refresh waits this long, so a burst of events costs one
// fetch.
const REFETCH_DEBOUNCE_MS = 250;

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
  // A bout's box on the protocols tab, sized and kept in view across redraws.
  boutSelector: string;
  cursorKinds: Record<string, CursorKind>;
  activeCursorElement?: () => Element | null;
  // The page's sheet cursors: bound at boot, refreshed after every render.
  cursors?: () => Array<{bind(): void; refresh(): void}>;
  // Old hashes a page still answers to (Brain's, game-tabs.ts canonicalKey).
  canonical?: (tabs: GameTab[], key: string) => string;
  // After the module drew a tab (the page's own scroll cues, anchors).
  afterRender?: (tab: GameTab | undefined, node: HTMLElement) => void;
  // A host is finishing a bout: what the page writes first (Troika fills the
  // wrong answers a host left blank), in the same gesture, so one undo takes
  // it back with the finish's own effect.
  beforeFinish?: (code: string) => void;
  // A bout's new view arrived: true when the page repainted it in place, so
  // the tab need not be drawn again.
  repaint?: (code: string) => boolean;
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
  reseed(code: string): Promise<SendResult>;
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
  let activeTab = fromHash() || "grid";
  const currentTab = () => tabs().find((tab) => tab.key === activeTab);

  onNavigate(() => {
    const next = fromHash();
    if (next && next !== activeTab) {
      activeTab = next;
      render();
    }
    spec.onNavigate?.();
  });

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

  function show(code: string): void {
    if (spec.repaint?.(code)) return;
    render();
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
        if (adoptView(next)) show(next.code);
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
        if (adoptView(view) && view?.code) show(view.code);
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
    if (result.codes.length === 1) show(result.codes[0]);
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
    default:
      return spec.buildTab(tab);
    }
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
    const node = buildTab(tab);
    const keepView = drawnTab === activeTab;
    const frame = root.closest(".sheet-frame");
    const scrollTop = frame?.scrollTop || 0;
    // Every mark another host saves redraws the tab; the bouts keep their
    // sizes and the one in view stays put, so the sheet does not jump under
    // the cursor. A tab of no bouts keeps its scroll the same way.
    redrawSteady(root, spec.boutSelector, () => {
      root.replaceChildren(node);
      if (keepView && frame) frame.scrollTop = scrollTop;
    }, keepView);
    drawnTab = activeTab;
    root.classList.toggle("fits-frame", spec.fitsFrame(tab, node));
    // A grid fits the frame's width like EK's, so its columns measure the same.
    root.classList.toggle("grid-host", node.matches(".fest-grid") || Boolean(node.querySelector(".fest-grid")));
    for (const cursor of spec.cursors?.() || []) cursor.refresh();
    shell.presence.refresh();
    spec.afterRender?.(tab, node);
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

  function sendFinish(code: string, finished: boolean): void {
    void writer.send(matchScope(code), {url: matchURL(code, "finish"), body: {finished}}, {path: ["finished"], value: finished});
  }

  async function draw(slot: string, participant: number): Promise<void> {
    // The server holds the choice to the seat's own candidates, so a refusal
    // is a refusal, and the page reloads what it answered either way.
    await writer.send(`draw:${slot}`, {url: `${apiBase}/draw`, method: "PUT", body: {slot, participant}});
    await fetchMatches().catch(() => indicator.fail());
  }

  async function reseed(code: string): Promise<SendResult> {
    const sent = await writer.send(`stage:${code}`, {url: `${apiBase}/stages/${encodeURIComponent(code)}/reseed`});
    if (sent.ok) await fetchMatches().catch(() => indicator.fail());
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
    resync,
    invalidateRoster: () => { rosterView = null; },
    venues,
    festStage: (code) => festStages.get(code),
    gridStages: () => (fest?.stages || []).flatMap((stage) => stage?.code ? [festStages.get(stage.code) || stage] : []),
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
    start,
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
