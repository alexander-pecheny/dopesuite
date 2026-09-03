// The Brain page (ADR-0001): a round-robin group of head-to-head matches. The
// protocols tab mirrors the reference sheet's match block — question rows of
// № | player | mark | mark | player around a running score, with tiebreak
// rows — and the tables tab is the sheet's group cross-table (score cells,
// points/+/−/± totals, place numbers). Matches come from the rr stage; edits go per match
// (PATCH /matches/{code}/state) and sync over match: scopes. A self-booting
// side-effect module bundled by pages/brain.ts.

import {cssEscape, formatDisplayText, td} from "./cells.js";
import {festLetters, standingsTable} from "./standings.js";
import {buildCrosstables, crossSlot, slotKey, standingsByParticipant} from "./crosstable.js";
import type {StageRef} from "./standings.js";
import {buildRosterView, fetchFestRoster} from "./fest-roster.js";
import type {RosterTeam} from "./fest-roster.js";
import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameInitLike} from "./game-page.js";
import {createFloatingPopover, fitScrollFade, markNameOverflow, renderTabBar} from "./widgets.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {computeBrainPlayerStats} from "./brain-stats.js";
import type {StatsBout} from "./brain-stats.js";
import * as brain from "./brain-protocol.js";
import type {BrainMatchState, BrainRow} from "./brain-protocol.js";
import {buildFestGrid, buildReseedStagePanel} from "./fest-grid.js";
import type {FestGridStage, ReseedEntry} from "./fest-grid.js";
import {gameTabs, canonicalKey, groupLabel} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import S from "./i18nstrings.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}

const pageWindow = window as Window & PageGlobals;

interface BrainSlotTeam {
  id?: number;
  name?: string;
}

interface BrainMatchView {
  code?: string;
  title?: string;
  finished?: boolean;
  revision?: number;
  state?: BrainMatchState | null;
  teams?: BrainSlotTeam[];
  participants?: Array<{id?: number; name?: string; place?: number} | null>;
  seq?: number;
}

interface SchemeSlotRef {
  seed?: {number?: number; position?: number} | null;
  reseed?: {stage?: string; rank?: number} | null;
  fromMatch?: {match?: string; place?: number} | null;
  label?: string;
}

interface BrainSchemeMatch {
  code?: string;
  slots?: SchemeSlotRef[];
}

// BrainStageRules is the rr stage's config vocabulary (the inner object of
// stages.config_json): entrant seeds, the ranking comparator order, the
// points rule and the appended-tiebreak switch.
interface BrainStageRules {
  entrants?: SchemeSlotRef[];
  order?: string[];
  points?: {win?: number; draw?: number; loss?: number} | null;
  tiebreakQuestions?: boolean;
  questions?: number;
}

interface BrainSchemeStage {
  code?: string;
  title?: string;
  kind?: string;
  stage_type?: string;
  grain?: {block?: string; group?: string; wave?: number};
  matches?: BrainSchemeMatch[];
  sources?: string[];
  config?: BrainStageRules | null;
}

interface BrainScheme {
  title?: string;
  questions?: number;
  stages?: BrainSchemeStage[];
  seeding?: {source?: string} | null;
  [key: string]: unknown;
}

interface SeedImportRow {
  sourceRank?: number;
  seedNumber?: number;
  teamID?: number;
  name?: string;
  city?: string;
  declined?: boolean;
  waitlist?: boolean;
}

interface SeedImportData {
  source?: string;
  drawSize?: number;
  activeCount?: number;
  rows?: SeedImportRow[];
}

interface FestInfo {
  title?: string;
  gameName?: string;
  stages?: Array<(FestGridStage & {config?: {config?: BrainStageRules} | null}) | null>;
  [key: string]: unknown;
}

const brainRoot = document.getElementById("brainTable")!;
const brainTabsRoot = document.getElementById("brainTabs");
const statusNode = document.getElementById("status");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");


// Long team names fade at their fixed width and carry a popover — the same
// treatment the EK tables give theirs.
const floatingPopover = createFloatingPopover({root: brainRoot, specs: [
  {trigger: ".brain-name-head.brain-name-truncated", popover: ".brain-name-popover", anchor: ".brain-name-wrap"},
  {trigger: ".results-team-truncated", popover: ".results-team-name-popover", anchor: ".results-team-name"},
]});
floatingPopover.bind();

let brainNameOverflowFrame = 0;
function scheduleBrainNameOverflowUpdate(): void {
  if (brainNameOverflowFrame) cancelAnimationFrame(brainNameOverflowFrame);
  brainNameOverflowFrame = requestAnimationFrame(() => {
    brainNameOverflowFrame = 0;
    markNameOverflow(brainRoot, {
      cellSelector: ".brain-name-head",
      nameSelector: ".brain-name",
      truncatedClass: "brain-name-truncated",
    });
    markNameOverflow(brainRoot, {
      cellSelector: ".results-team",
      nameSelector: ".results-team-name",
      truncatedClass: "results-team-truncated",
    });
  });
}
window.addEventListener("resize", scheduleBrainNameOverflowUpdate);

const route = parseGameRoute();
const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as BrainScheme;
const fest = (init?.fest || null) as FestInfo | null;
const shell = mountGamePage({
  app: "brain",
  root: brainRoot,
  statusNode,
  breadcrumbsNode,
  festID: route.festID,
  gameID: route.gameID,
  viewer: Boolean(route.viewer),
  apiBase: route.apiBase,
  init,
  downloads: false,
  chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme.title || S.brain.title()}),
  cursorKinds: {
    answer: {selector: ".answer-cell", keys: ["match", "side", "q"]},
    player: {selector: ".brain-player-select", keys: ["match", "side", "q"]},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
});
const {viewer, staticMode, scopeGameID, indicator, viewerCounter} = shell;
const matches = new Map<string, BrainMatchView>();
let festRoster: RosterTeam[] = [];
let rosterView: HTMLElement | null = null;
let activeTab = tabFromHash() || "grid";
let resyncScheduled = false;

function tabs(): GameTab[] {
  return gameTabs((scheme.stages || []) as StageRef[], {game: "brain", viewer, seeded: Boolean(scheme.seeding?.source)});
}

function tabStages(tab: GameTab): BrainSchemeStage[] {
  return (scheme.stages || []).filter((stage) => tab.stages.includes(stage.code || ""));
}

function stageKind(stage: BrainSchemeStage): string {
  return stage.kind || stage.stage_type || "";
}

// Every match of the game carries a letter — the sheets' A..Z, AA.. handle —
// dealt by the compiler and carried on the fest view.
const boutLetters = festLetters(fest?.stages as StageRef[] | undefined);

// protocolStages are the stages whose matches the page draws — everything except
// reseed edges, in scheme order.
function protocolStages(): BrainSchemeStage[] {
  return (scheme.stages || []).filter((s) => stageKind(s) !== "reseed");
}

const stageByMatch = new Map<string, BrainSchemeStage>();
for (const schemeStage of scheme.stages || []) {
  for (const planned of schemeStage.matches || []) {
    if (planned.code) stageByMatch.set(planned.code, schemeStage);
  }
}

// groupRules reads a stage's rules from the fest view — the same
// stages.config_json row the resolver ranks by, so client and server can't
// drift — falling back to the scheme's creation-time copy.
function groupRules(groupStage: BrainSchemeStage): BrainStageRules {
  const stage = fest?.stages?.find((s) => s?.code === groupStage.code);
  return stage?.config?.config || groupStage.config || {};
}

function schemeQuestions(): number {
  const n = Number(scheme.questions);
  return Number.isInteger(n) && n > 0 ? n : 5;
}

// questionsFor is a match's regular question count: its stage's override (the
// DSL's per-block/round questions cascade), else the scheme-wide default.
function questionsFor(code: string): number {
  const stage = stageByMatch.get(code);
  const n = Number(stage ? groupRules(stage).questions : 0);
  return Number.isInteger(n) && n > 0 ? n : schemeQuestions();
}

// onProtocolTab reports a protocols tab in front — the tab keys are
// `protocol:<block>`, one per Block, never the bare word.
function onProtocolTab(): boolean {
  return tabs().find((t) => t.key === activeTab)?.kind === "protocol";
}

function tabFromHash(): string | null {
  const all = tabs();
  const key = canonicalKey(all, (window.location.hash || "").replace(/^#/, ""));
  return all.some((t) => t.key === key) ? key : null;
}

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});


function normalizeState(view: BrainMatchView): void {
  view.state = brain.parseState(view.state, questionsFor(view.code || ""));
}

// adoptMatchView takes a match's view from wherever it arrives — the fetch, a
// write's response, the stream — with this page's un-acked edits overlaid, so
// a slow write never visibly regresses.
function adoptMatchView(view: BrainMatchView | null | undefined): boolean {
  const code = view?.code;
  if (!view || !code) return false;
  const cached = matches.get(code);
  if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
  view = writer.overlay(matchScope(code), view);
  normalizeState(view);
  matches.set(code, view);
  return true;
}

function matchScope(code: string): string {
  return `match:${scopeGameID}:${code}`;
}

async function fetchMatches(): Promise<void> {
  const response = await fetch(`${route.apiBase}/stages/matches`);
  if (!response.ok) throw new Error(`stages/matches ${response.status}`);
  const stages = await response.json() as Array<{code?: string; matches?: BrainMatchView[]}>;
  for (const stage of stages || []) {
    for (const view of stage.matches || []) adoptMatchView(view);
  }
  render({preserveScroll: true});
}

function scheduleResync(): void {
  if (resyncScheduled) return;
  resyncScheduled = true;
  setTimeout(() => {
    resyncScheduled = false;
    fetchMatches().catch(() => indicator.fail());
  }, 250);
}

function loadFestRoster(): void {
  fetchFestRoster(route.festID)
    .then((teams) => {
      festRoster = teams;
      if (onProtocolTab()) render({preserveScroll: true});
    })
    .catch(() => {});
}

const live = createLiveEvents({
  eventsURL: () => gameEventsURL(route.festID!, route.gameID),
  gameID: scopeGameID,
  scopes: [{
    // The server broadcasts the whole fest view after every write; the tables
    // in it — every Ranker's standings — are the page's, not recomputed here.
    prefix: "fest:",
    adopt: (_scope, view) => {
      const fresh = view.data as FestInfo | null;
      if (!fresh?.stages) return;
      adoptFestStages(fresh);
      render({preserveScroll: true});
    },
  }, {
    prefix: `match:${scopeGameID}:`,
    base: (scope) => {
      const cached = matches.get(scope.slice(`match:${scopeGameID}:`.length));
      return cached ? {data: cached, seq: Number(cached.seq || 0)} : null;
    },
    adopt: (_scope, view) => {
      const next = view.data as BrainMatchView | null;
      if (!next?.code) {
        scheduleResync();
        return;
      }
      next.seq = view.seq;
      adoptMatchView(next);
      render({preserveScroll: true});
    },
    gap: () => scheduleResync(),
  }],
  indicator,
  onViewers: (count) => viewerCounter.setCount(count),
  onLockdown: scheduleStaticReload,
  reload: fetchMatches,
  staticMode: () => staticMode,
});

const writer = createScopedWriter({
  readonly: viewer,
  urlOf: (scope) => `${route.apiBase}/matches/${encodeURIComponent(scope.slice(`match:${scopeGameID}:`.length))}/state`,
  // Ops address the match's Protocol document, which the view carries as `state`.
  docPath: ["state"],
  adopt: (scope, response) => {
    if (scope.startsWith("stage:")) adoptFestStages(response as FestInfo);
    else adoptMatchView(response as BrainMatchView);
    render({preserveScroll: true});
  },
  indicator,
  onRejected: () => scheduleResync(),
});

function sendOps(code: string, ops: Array<{path: Array<string | number>; value: unknown}>): void {
  for (const op of ops) writer.patch(matchScope(code), op.path, op.value);
}

function sendFinish(code: string, finished: boolean): void {
  void writer.send(matchScope(code), {url: `${route.apiBase}/matches/${encodeURIComponent(code)}/finish`, body: {finished}}, {path: ["finished"], value: finished});
}

function matchRows(view: BrainMatchView, side: number): BrainRow[] {
  return (view.state?.teams?.[side]?.rows || []) as BrainRow[];
}

function taken(view: BrainMatchView, side: number): number {
  return matchRows(view, side).filter((row) => row.mark === "right").length;
}

function started(view: BrainMatchView): boolean {
  return [0, 1].some((side) => matchRows(view, side).some((row) => row.mark || row.player));
}

function teamName(view: BrainMatchView, side: number): string {
  return view.participants?.[side]?.name || S.brain.team.fallback(String(side + 1));
}

function rowLabel(index: number, base: number): string {
  if (index < base) return String(index + 1);
  return index === base ? S.brain.row.tiebreak() : S.brain.row.tiebreakN(String(index - base + 1));
}

function rosterFor(name: string): string[] {
  const wanted = name.trim().toLowerCase();
  const team = festRoster.find((t) => (t.name || "").trim().toLowerCase() === wanted);
  return (team?.players || [])
    .map((p) => (typeof p === "string" ? p : p.name || ""))
    .filter(Boolean);
}

interface BoutEntry {
  code: string;
  view: BrainMatchView;
  planned: BrainSchemeMatch;
  stage: BrainSchemeStage;
}

function stageBouts(stage: BrainSchemeStage): BoutEntry[] {
  const out: BoutEntry[] = [];
  for (const planned of stage.matches || []) {
    const code = planned.code || "";
    const view = matches.get(code);
    if (view) out.push({code, view, planned, stage});
  }
  return out;
}

// allBouts flattens every protocol stage's matches in scheme order — the selection
// grid's column space spans them all.
function allBouts(): BoutEntry[] {
  return protocolStages().flatMap(stageBouts);
}


function render(options: {preserveScroll?: boolean} = {}): void {
  shell.renderChrome();
  renderTabs();
  const frame = brainRoot.closest(".sheet-frame");
  const scrollTop = frame?.scrollTop || 0;
  const node = buildTab(tabs().find((tab) => tab.key === activeTab));
  brainRoot.replaceChildren(node);
  brainRoot.classList.toggle("fits-frame", activeTab === "roster");
  // A grid fits the frame's width like EK's, so its columns measure the same.
  brainRoot.classList.toggle("grid-host", node.matches(".fest-grid") || Boolean(node.querySelector(".fest-grid")));
  scheduleBrainNameOverflowUpdate();
  if (options.preserveScroll && frame) frame.scrollTop = scrollTop;
  restoreSelection();
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "roster":
    return (rosterView ||= buildRosterView(route.festID));
  case "seed":
    return buildSeedView();
  case "stats":
    return buildStatsView();
  case "reseed":
    return buildReseedTab(tabStages(tab));
  case "block":
    return buildCrosstable(tabStages(tab));
  case "pods":
    return buildPodBoard(tabStages(tab));
  case "protocol":
    return buildProtocols(tabStages(tab));
  default:
    return buildGrid();
  }
}

function renderTabs(): void {
  if (!brainTabsRoot) return;
  brainTabsRoot.hidden = false;
  renderTabBar(brainTabsRoot, tabs(), activeTab, (key) => {
    activeTab = key;
    if (window.location.hash.replace(/^#/, "") !== key) {
      history.replaceState(null, "", `#${key}`);
    }
    render();
  });
}

// The live fest view feeds the reseed panels (entries, sort rules). The init
// snapshot goes stale, so the calculate button adopts the fresh view it gets back.
const festStages = new Map<string, FestGridStage>();
for (const viewStage of fest?.stages || []) {
  if (viewStage?.code) festStages.set(viewStage.code, viewStage);
}
const reseedError = new Map<string, string>();

function adoptFestStages(fresh: FestInfo | null): void {
  for (const viewStage of fresh?.stages || []) {
    if (viewStage?.code) festStages.set(viewStage.code, viewStage);
  }
}

function reseedPendingBouts(stage: BrainSchemeStage): string[] {
  const sources = new Set(stage.sources || []);
  const pending: string[] = [];
  for (const src of protocolStages()) {
    if (!src.code || !sources.has(src.code)) continue;
    for (const planned of src.matches || []) {
      const code = planned.code || "";
      if (!matches.get(code)?.finished) pending.push(code);
    }
  }
  return pending;
}

async function calculateReseed(code: string): Promise<void> {
  const sent = await writer.send(`stage:${code}`, {url: `${route.apiBase}/stages/${encodeURIComponent(code)}/reseed`});
  if (sent.ok) reseedError.delete(code);
  else reseedError.set(code, sent.error || S.brain.reseed.calculateFailed());
  render({preserveScroll: true});
}

function buildBrainReseedPanel(stage: BrainSchemeStage): HTMLElement {
  const code = stage.code || "";
  const pending = reseedPendingBouts(stage);
  const blocked = pending.length === 1
    ? S.brain.reseed.pendingOne(pending[0])
    : pending.length > 1 ? S.brain.reseed.pendingMany(pending.join(", ")) : "";
  const panel = buildReseedStagePanel({...(festStages.get(code) || {}), code}, {
    letters: boutLetters,
    editable: !viewer,
    canCalculate: pending.length === 0,
    blockedMessage: blocked,
    onCalculate: () => void calculateReseed(code),
  });
  const errorText = reseedError.get(code);
  if (errorText) {
    const note = document.createElement("p");
    note.className = "brain-seed-error";
    note.textContent = errorText;
    panel.appendChild(note);
  }
  return panel;
}

// buildProtocols lays one Block's matches out the way the sheet's protocols tab
// does: a section per stage (group, pod, round), its matches side by side.
function buildProtocols(stages: BrainSchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const multi = stages.length > 1;
  let rendered = 0;
  for (const stage of stages) {
    const bouts = stageBouts(stage);
    if (!bouts.length) continue;
    rendered++;
    if (multi) {
      const head = document.createElement("h2");
      head.className = "brain-stage-head";
      head.textContent = stage.title || stage.code || "";
      wrap.appendChild(head);
    }
    const row = document.createElement("div");
    row.className = "brain-bouts";
    for (const bout of bouts) {
      row.appendChild(buildBout(bout));
    }
    wrap.appendChild(row);
  }
  if (!rendered) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.brain.protocol.empty();
    wrap.appendChild(empty);
  }
  return wrap;
}

// buildGrid is the grid tab: the whole Game at a glance from the same fest data
// the EK pages draw — every Block one column, place-grain, no protocol detail.
function buildGrid(): HTMLElement {
  const stages: FestGridStage[] = [];
  for (const viewStage of fest?.stages || []) {
    if (viewStage?.code) stages.push(festStages.get(viewStage.code) || viewStage);
  }
  return buildFestGrid({schemaJson: fest?.schemaJson, stages},
    {stageHeaderLink: false, matchTitleLink: false, letters: boutLetters});
}

// buildPodBoard is a pod Block's detail tab, the sheet's «Double Elimination»
// view: one column per round, each match a box with its letter, teams and Σ. The
// same boxes the grid once carried — moved where detail belongs. A pod is a
// row band: its matches of every round sit in the band, so a round with one match
// per pod leaves the pod's other slot blank and the columns read across.
function buildPodBoard(pods: BrainSchemeStage[]): HTMLElement {
  type GridMatch = NonNullable<FestGridStage["matches"]>[number];
  const byRound = new Map<number, GridMatch[]>();
  const roundOf = (planned: BrainSchemeMatch) => Number((planned as {round?: number}).round || 1);
  // slots[pod][i] is the match's slot within its pod's round; podRows the widest.
  const slots = pods.map((stage) => {
    const seen = new Map<number, number>();
    return (stage.matches || []).map((planned) => {
      const slot = seen.get(roundOf(planned)) || 0;
      seen.set(roundOf(planned), slot + 1);
      return slot;
    });
  });
  const podRows = Math.max(0, ...slots.flat()) + 1;
  pods.forEach((stage, pod) => {
    const live = new Map((festStages.get(stage.code || "")?.matches || []).map((m) => [m.code, m]));
    (stage.matches || []).forEach((planned, i) => {
      const round = roundOf(planned);
      const merged = {...(planned as GridMatch), ...(live.get(planned.code) || {}), row: pod * podRows + slots[pod][i] + 1};
      const list = byRound.get(round);
      if (list) list.push(merged);
      else byRound.set(round, [merged]);
    });
  });
  const stages = Array.from(byRound.keys()).sort((a, b) => a - b).map((round): FestGridStage => ({
    code: `round-${round}`,
    title: S.brain.pod.round(String(round)),
    stage_type: "matches",
    matches: byRound.get(round),
  }));
  return buildFestGrid({stages}, {stageHeaderLink: false, matchTitleLink: false, letters: boutLetters});
}

// buildReseedTab stacks every reseed's panel, each under the name of the
// stage it seats.
function buildReseedTab(reseeds: BrainSchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "reseed-fold";
  const stages = scheme.stages || [];
  for (const stage of reseeds) {
    if (reseeds.length > 1) {
      const index = stages.indexOf(stage);
      const next = stages.slice(index + 1).find((s) => stageKind(s) !== "reseed");
      const head = document.createElement("h3");
      head.className = "reseed-fold-head";
      head.textContent = String(next?.title || "");
      wrap.appendChild(head);
    }
    wrap.appendChild(buildBrainReseedPanel(stage));
  }
  return wrap;
}

// buildStatsView is the sheet's individual statistics: attempts, right,
// wrong per (player, team) over the regular questions of finished matches.
function buildStatsView(): HTMLElement {
  const bouts: StatsBout[] = [];
  for (const {code, view} of allBouts()) {
    const rowCount = matchRows(view, 0).length;
    const rows: StatsBout["rows"] = [];
    for (let q = 0; q < rowCount; q++) {
      rows.push([matchRows(view, 0)[q] || {}, matchRows(view, 1)[q] || {}]);
    }
    bouts.push({teams: [teamName(view, 0), teamName(view, 1)], regular: questionsFor(code), rows});
  }
  const stats = computeBrainPlayerStats(bouts);
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (!stats.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.brain.stats.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  // The same table EK's statistics is — its name columns size to content and
  // its numbers sit tight — with the buzzer's columns in place of the themes'.
  wrapper.appendChild(standingsTable({
    className: "ek-stats-table",
    columns: [
      {label: S.brain.stats.player(), kind: "name", className: "ek-stats-name ek-stats-player"},
      {label: S.brain.stats.team(), kind: "name", className: "ek-stats-name"},
      {label: S.brain.stats.attempts(), kind: "num"},
      {label: S.brain.stats.right(), kind: "num"},
      {label: S.brain.stats.wrong(), kind: "num", className: "ek-stats-wrong"},
      {label: S.brain.stats.share(), kind: "num", className: "ek-stats-share"},
    ],
    rows: stats.map((row) => [
      row.player,
      row.team,
      row.attempts,
      row.right,
      row.wrong,
      row.attempts ? `${Math.round((row.right / row.attempts) * 100)}%` : "",
    ]),
  }));
  return wrapper;
}

function buildBout({code, view, planned}: BoutEntry): HTMLElement {
  const section = document.createElement("section");
  section.className = "brain-bout";
  const editable = !viewer && !view.finished;

  const table = document.createElement("table");
  table.className = "match-table brain-detailed";
  table.classList.toggle("match-finished", Boolean(view.finished));
  table.dataset.match = code;

  // One head row, everything on one line: the match's letter, a team over its
  // player column, the score over the two mark columns, the other team, and
  // the finished tick. A name wider than its column fades to a popover.
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  const corner = document.createElement("th");
  corner.className = "row-marker brain-bout-corner";
  corner.textContent = boutLetters.get(code) || (code.split("-").pop() || code).replace(/^m/, "");
  corner.title = view.title || code;
  head.appendChild(corner);
  head.appendChild(nameHead(view, 0, planned));
  const score = document.createElement("th");
  score.className = "number brain-score-head";
  score.colSpan = 2;
  score.textContent = `${taken(view, 0)} : ${taken(view, 1)}`;
  head.appendChild(score);
  head.appendChild(nameHead(view, 1, planned));
  head.appendChild(finishHead(code, view));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const rowCount = matchRows(view, 0).length;
  const base = questionsFor(code);
  for (let q = 0; q < rowCount; q++) {
    const tr = document.createElement("tr");
    const marker = document.createElement("td");
    marker.className = "row-marker" + (q >= base ? " brain-tiebreak-marker" : "");
    marker.textContent = rowLabel(q, base);
    tr.appendChild(marker);
    tr.appendChild(playerCell(code, view, 0, q, editable));
    tr.appendChild(markCell(code, view, 0, q, editable));
    tr.appendChild(markCell(code, view, 1, q, editable));
    tr.appendChild(playerCell(code, view, 1, q, editable));
    const gap = document.createElement("td");
    gap.className = "brain-finish-gap";
    tr.appendChild(gap);
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  table.addEventListener("change", handleTableChange);
  section.appendChild(table);
  const stage = stageByMatch.get(code);
  if (editable && stage && groupRules(stage).tiebreakQuestions) {
    section.appendChild(tiebreakControls(code, view));
  }
  return section;
}

// nameHead shows the seated team; an unresolved slot shows its source label
// (seed 5, group 1-2 — the server fills it from the slot ref) muted.
function nameHead(view: BrainMatchView, side: number, planned: BrainSchemeMatch): HTMLElement {
  const th = document.createElement("th");
  th.className = "brain-name-head";
  const label = view.participants?.[side]?.name || planned.slots?.[side]?.label || "—";
  const wrap = document.createElement("span");
  wrap.className = "brain-name-wrap";
  const name = document.createElement("span");
  name.className = "brain-name";
  name.textContent = label;
  name.tabIndex = 0;
  name.setAttribute("aria-label", label);
  name.classList.toggle("brain-name-pending", !view.participants?.[side]?.id);
  wrap.appendChild(name);
  th.appendChild(wrap);
  const popover = document.createElement("span");
  popover.className = "popover popover-inline brain-name-popover";
  popover.textContent = label;
  th.appendChild(popover);
  return th;
}

function finishHead(code: string, view: BrainMatchView): HTMLElement {
  const th = document.createElement("th");
  th.className = "brain-finish-head";
  const label = document.createElement("label");
  label.className = "finish-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(view.finished);
  checkbox.disabled = viewer;
  checkbox.dataset.match = code;
  const text = document.createElement("span");
  text.textContent = S.brain.bout.finished();
  label.append(checkbox, text);
  th.appendChild(label);
  return th;
}

function playerCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  td.className = "brain-player-cell";
  const select = document.createElement("select");
  select.className = "brain-player-select";
  select.dataset.match = code;
  select.dataset.side = String(side);
  select.dataset.q = String(q);
  select.disabled = !editable;
  const blank = document.createElement("option");
  blank.value = "";
  blank.textContent = "";
  select.appendChild(blank);
  const current = matchRows(view, side)[q]?.player || "";
  const roster = rosterFor(teamName(view, side));
  for (const player of roster) {
    const opt = document.createElement("option");
    opt.value = player;
    opt.textContent = player;
    select.appendChild(opt);
  }
  if (current && !roster.includes(current)) {
    const opt = document.createElement("option");
    opt.value = current;
    opt.textContent = current;
    select.appendChild(opt);
  }
  select.value = current;
  td.appendChild(select);
  return td;
}

function markCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  const mark = matchRows(view, side)[q]?.mark || "";
  td.className = `answer-cell ${mark}`;
  td.tabIndex = editable ? 0 : -1;
  td.dataset.match = code;
  td.dataset.side = String(side);
  td.dataset.q = String(q);
  td.title = S.brain.mark.title(teamName(view, side), rowLabel(q, questionsFor(code)));
  return td;
}

// tiebreakControls adds/removes tiebreak rows — the tiebreak questions appended
// after the base K when this match must produce a winner.
function tiebreakControls(code: string, view: BrainMatchView): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "brain-controls";
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn-xs";
  add.textContent = S.brain.tiebreak.add();
  add.title = S.brain.tiebreak.addHint();
  add.addEventListener("click", () => {
    const state = view.state as BrainMatchState;
    const index = matchRows(view, 0).length;
    sendOps(code, [
      {path: ["tiebreaks"], value: (state.tiebreaks || 0) + 1},
      {path: ["teams", 0, "rows", index], value: {player: "", mark: ""}},
      {path: ["teams", 1, "rows", index], value: {player: "", mark: ""}},
    ]);
  });
  bar.appendChild(add);
  const state = view.state as BrainMatchState;
  const last = matchRows(view, 0).length - 1;
  const lastEmpty = (state.tiebreaks || 0) > 0 &&
    [0, 1].every((side) => {
      const row = matchRows(view, side)[last];
      return !row?.player && !row?.mark;
    });
  if (lastEmpty) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "btn-xs";
    remove.textContent = S.brain.tiebreak.remove();
    remove.title = S.brain.tiebreak.removeHint();
    remove.addEventListener("click", () => {
      sendOps(code, [
        {path: ["tiebreaks"], value: (state.tiebreaks || 0) - 1},
        {path: ["teams", 0, "rows"], value: matchRows(view, 0).slice(0, last)},
        {path: ["teams", 1, "rows"], value: matchRows(view, 1).slice(0, last)},
      ]);
    });
    bar.appendChild(remove);
  }
  return bar;
}


// The group table is crosstable.ts's, so Brain and Troika draw one table.
// This says only what a Brain match is: two sides, taken marks as the score.
function buildCrosstable(stages: BrainSchemeStage[]): HTMLElement {
  return buildCrosstables({
    className: "brain-groups",
    groups: stages.filter((stage) => stageKind(stage) === "rr").map((stage) => ({
      title: groupLabel(stage as StageRef),
      entrants: (groupRules(stage).entrants || []).map(crossSlot),
      bouts: (stage.matches || []).flatMap((planned) => {
        const view = matches.get(planned.code || "");
        if (!view) return [];
        return [{
          slots: [crossSlot(planned.slots?.[0]), crossSlot(planned.slots?.[1])],
          sides: [0, 1].map((side) => ({
            name: view.participants?.[side]?.name || "",
            id: Number(view.participants?.[side]?.id || 0),
            score: taken(view, side),
          })),
          finished: Boolean(view.finished),
          started: started(view),
        }];
      }),
      standings: standingsByParticipant(festStages.get(stage.code || "")),
    })),
  });
}

let seedImport: SeedImportData | null = null;
let seedError = "";
let seedLoaded = false;
let seedBusy = false;

async function seedAction(run: () => Promise<Response>): Promise<void> {
  if (seedBusy) return;
  seedBusy = true;
  render({preserveScroll: true});
  try {
    const response = await run();
    if (!response.ok) throw new Error(await response.text());
    seedImport = await response.json() as SeedImportData;
    seedError = "";
    await fetchMatches();
  } catch (error) {
    seedError = error instanceof Error ? error.message.trim() : String(error);
  }
  seedBusy = false;
  render({preserveScroll: true});
}

function loadSeedView(): void {
  if (seedLoaded) return;
  seedLoaded = true;
  fetch(`${route.apiBase}/seed-import`)
    .then(async (response) => {
      if (!response.ok) throw new Error(await response.text());
      const data = await response.json() as SeedImportData;
      // An action's response may have landed while this GET was in flight —
      // the freshly imported ladder must not be clobbered by the stale read.
      if (!seedImport) {
        seedImport = data;
        render({preserveScroll: true});
      }
    })
    .catch(async (error: unknown) => {
      seedError = error instanceof Error ? error.message.trim() : String(error);
      render({preserveScroll: true});
    });
}

// buildSeedView is the seeding tab: the declared source, the one import button
// (or the xlsx upload), and the ladder — active seeds, declines, waitlist.
function buildSeedView(): HTMLElement {
  loadSeedView();
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const source = scheme.seeding?.source || "";

  const bar = document.createElement("div");
  bar.className = "brain-seed-bar";
  if (source === "xlsx") {
    const file = document.createElement("input");
    file.type = "file";
    file.accept = ".xlsx";
    file.className = "brain-seed-file";
    const upload = document.createElement("button");
    upload.type = "button";
    upload.className = "btn";
    upload.textContent = S.brain.seed.upload();
    upload.disabled = seedBusy;
    upload.addEventListener("click", () => {
      const chosen = file.files?.[0];
      if (!chosen) {
        seedError = S.brain.seed.noFile();
        render({preserveScroll: true});
        return;
      }
      const body = new FormData();
      body.append("file", chosen);
      void seedAction(() => fetch(`${route.apiBase}/seed-import/xlsx`, {method: "POST", body}));
    });
    bar.append(file, upload);
  } else {
    const importButton = document.createElement("button");
    importButton.type = "button";
    importButton.className = "btn";
    importButton.textContent = source === "random" ? S.brain.seed.draw() : S.brain.seed.importFrom(source);
    importButton.disabled = seedBusy;
    importButton.addEventListener("click", () => {
      void seedAction(() => fetch(`${route.apiBase}/seed-import/run`, {method: "POST"}));
    });
    bar.appendChild(importButton);
  }
  wrap.appendChild(bar);

  if (seedError) {
    const error = document.createElement("p");
    error.className = "brain-seed-error";
    error.textContent = seedError;
    wrap.appendChild(error);
  }

  const rows = seedImport?.rows || [];
  if (!rows.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.brain.seed.empty();
    wrap.appendChild(empty);
    return wrap;
  }

  const table = document.createElement("table");
  table.className = "match-table brain-seed-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  for (const text of [S.brain.seedHead.seed(), S.brain.seedHead.team(), S.brain.seedHead.city(), S.brain.seedHead.rank(), S.brain.seedHead.declined()]) {
    const th = document.createElement("th");
    th.textContent = text;
    head.appendChild(th);
  }
  thead.appendChild(head);
  table.appendChild(thead);
  const tbody = document.createElement("tbody");
  for (const row of rows) {
    const tr = document.createElement("tr");
    tr.classList.toggle("brain-seed-waitlist", Boolean(row.waitlist));
    tr.classList.toggle("brain-seed-declined", Boolean(row.declined));
    const seed = document.createElement("td");
    seed.className = "number";
    seed.textContent = row.declined ? "—" : row.waitlist ? S.brain.seed.waitlist() : String(row.seedNumber || "");
    const name = document.createElement("td");
    name.className = "brain-seed-name";
    name.textContent = row.name || "";
    const city = document.createElement("td");
    city.textContent = row.city || "";
    const rank = document.createElement("td");
    rank.className = "number";
    rank.textContent = String(row.sourceRank || "");
    const decline = document.createElement("td");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = Boolean(row.declined);
    checkbox.disabled = seedBusy;
    checkbox.addEventListener("change", () => {
      void seedAction(() => fetch(`${route.apiBase}/seed-import/decline`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({teamID: row.teamID, declined: checkbox.checked}),
      }));
    });
    decline.appendChild(checkbox);
    tr.append(seed, name, city, rank, decline);
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}


function handleTableChange(event: Event): void {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.classList.contains("finish-toggle")) {
    if (viewer) return;
    const code = target.dataset.match || "";
    if (matches.has(code)) sendFinish(code, target.checked);
    return;
  }
  if (target instanceof HTMLSelectElement && target.classList.contains("brain-player-select")) {
    const ctx = cellContext(target);
    if (!ctx || viewer || ctx.view.finished) return;
    matchRows(ctx.view, ctx.side)[ctx.q].player = target.value;
    sendOps(ctx.code, [{path: ["teams", ctx.side, "rows", ctx.q, "player"], value: target.value}]);
  }
}

function cellContext(el: HTMLElement): {code: string; view: BrainMatchView; side: number; q: number} | null {
  const code = el.dataset.match || "";
  const view = matches.get(code);
  const side = Number(el.dataset.side);
  const q = Number(el.dataset.q);
  if (!view || (side !== 0 && side !== 1) || !Number.isInteger(q)) return null;
  if (q < 0 || q >= matchRows(view, side).length) return null;
  return {code, view, side, q};
}

function cellNode(code: string, side: number, q: number): HTMLElement | null {
  return brainRoot.querySelector<HTMLElement>(
    `.answer-cell[data-match="${cssEscape(code)}"][data-side="${side}"][data-q="${q}"]`,
  );
}

// The selection treats the matches laid side by side as one sheet: row = the
// question, col = match × 2 + side. The widget (shared with KSI) then gives
// click/drag/shift ranges, copy/paste and touch tap-cycling.
function cellCoord(code: string, side: number, q: number): CellCoord | null {
  const idx = allBouts().findIndex((bout) => bout.code === code);
  return idx < 0 ? null : {row: q, col: idx * 2 + side};
}

function boutAtCol(col: number): {code: string; view: BrainMatchView; side: number} | null {
  const bout = allBouts()[Math.floor(col / 2)];
  return bout ? {code: bout.code, view: bout.view, side: col % 2} : null;
}

function totalCols(): number {
  return allBouts().length * 2;
}

// applyMarkEdits updates the local views and DOM, then sends one PATCH per match.
function applyMarkEdits(edits: CellEdit[]): void {
  const opsByCode = new Map<string, Array<{path: Array<string | number>; value: unknown}>>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const ctx = cellContext(cell);
    if (!ctx || viewer || ctx.view.finished) continue;
    const mark = parseMark(edit.value);
    const row = matchRows(ctx.view, ctx.side)[ctx.q];
    if (row.mark === mark) continue;
    row.mark = mark;
    cell.classList.remove("right", "wrong");
    if (mark) cell.classList.add(mark);
    const score = cell.closest("table")?.querySelector(".brain-score-head");
    if (score) score.textContent = `${taken(ctx.view, 0)} : ${taken(ctx.view, 1)}`;
    const ops = opsByCode.get(ctx.code) || [];
    ops.push({path: ["teams", ctx.side, "rows", ctx.q, "mark"], value: mark});
    opsByCode.set(ctx.code, ops);
  }
  for (const [code, ops] of opsByCode) sendOps(code, ops);
}

function cellAt(coord: CellCoord | null): HTMLElement | null {
  if (!coord) return null;
  const at = boutAtCol(coord.col);
  return at ? cellNode(at.code, at.side, coord.row) : null;
}

// The matches laid side by side are one sheet: row = the question, col = match × 2 +
// side; a match with tiebreak questions is taller than its neighbours.
const cursor = createSheetCursor({
  root: brainRoot,
  rows: (col) => {
    const at = boutAtCol(col);
    return at ? matchRows(at.view, 0).length : 0;
  },
  cols: () => totalCols(),
  readonly: () => viewer,
  active: onProtocolTab,
  coordOf: (cell) => {
    const ctx = cellContext(cell as HTMLElement);
    return ctx ? cellCoord(ctx.code, ctx.side, ctx.q) : null;
  },
  cellAt,
  values: "marks",
  applyValues: applyMarkEdits,
  classes: {row: ""},
});
cursor.bind();

// restoreSelection re-applies the cursor after a re-render rebuilt the cells.
function restoreSelection(): void {
  if (!onProtocolTab() || !cursor.anchor) return;
  cursor.select(cursor.anchor, cursor.focus, {focus: false});
}



render();
fitScrollFade(brainRoot.closest(".sheet-frame"));
loadFestRoster();
fetchMatches()
  .then(() => {
    indicator.touch();
    live.connect();
    shell.presence.connect();
  })
  .catch((error: unknown) => {
    indicator.fail();
    console.error(error);
  });

export {};
