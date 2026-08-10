// The брейн page (ADR-0001): a round-robin group of head-to-head бои. The
// Протоколы tab mirrors the reference sheet's match block — question rows of
// № | player | mark | mark | player around a running score, with «П» tiebreak
// rows — and the Таблица tab is the sheet's group cross-table (score cells,
// О/+/−/± totals, М places). Matches come from the rr stage; edits go per бой
// (PATCH /matches/{code}/state) and sync over match: scopes. A self-booting
// side-effect module bundled by pages/brain.ts.

import {DopeTable} from "./match-table.js";
import type {CellCoord, CellEdit, CellRangeSelection, GameInitLike, RosterTeam, ScopedEventMessage} from "./match-table.js";
import {computeBrainPlayerStats, rankGroup} from "./brain-rank.js";
import type {RankDuel, RankTeam, StatsBout} from "./brain-rank.js";
import {buildFestGrid, buildReseedStagePanel} from "./fest-grid.js";
import type {FestGridStage} from "./fest-grid.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}

const pageWindow = window as Window & PageGlobals;

interface BrainRow {
  player: string;
  mark: string; // "right" | "wrong" | ""
}

interface BrainMatchState {
  tiebreaks?: number;
  teams?: Array<{rows?: Array<BrainRow | null> | null} | null> | null;
}

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
// points rule and the appended-перестрелка switch.
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

const gameTable = DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);

// Long team names fade at their fixed width and carry a popover — the same
// treatment the ЭК tables give theirs.
const floatingPopover = gameTable.createFloatingPopover({root: brainRoot, specs: [
  {trigger: ".brain-name-head.brain-name-truncated", popover: ".brain-name-popover", anchor: ".brain-name-wrap"},
  {trigger: ".brain-cross-team.brain-name-truncated", popover: ".brain-name-popover", anchor: ".brain-name-wrap"},
]});
floatingPopover.bind();

let brainNameOverflowFrame = 0;
function scheduleBrainNameOverflowUpdate(): void {
  if (brainNameOverflowFrame) cancelAnimationFrame(brainNameOverflowFrame);
  brainNameOverflowFrame = requestAnimationFrame(() => {
    brainNameOverflowFrame = 0;
    gameTable.markNameOverflow(brainRoot, {
      cellSelector: ".brain-name-head",
      nameSelector: ".brain-name",
      truncatedClass: "brain-name-truncated",
    });
    gameTable.markNameOverflow(brainRoot, {
      cellSelector: ".brain-cross-team",
      nameSelector: ".brain-name",
      truncatedClass: "brain-name-truncated",
    });
  });
}
window.addEventListener("resize", scheduleBrainNameOverflowUpdate);

const SEED_TAB = {key: "seed", label: "Посев"};

const route = gameTable.parseGameRoute();
const viewer = Boolean(route.viewer);
const init = pageWindow.__GAME_INIT__ || null;
const staticMode = Boolean(init?.static);
const canEdit = Boolean(init?.canEdit);
const scopeGameID = init?.gameID != null ? String(init.gameID) : route.gameID;
document.body.classList.toggle("viewer-readonly", viewer);
if (viewer) {
  if (canEdit) gameTable.mountEditorLink();
} else {
  gameTable.mountViewerLink();
  if (init?.teamsUnnumbered) gameTable.mountUnnumberedBanner(route.festID);
}

const scheme = (init?.scheme || {}) as BrainScheme;
const fest = (init?.fest || null) as FestInfo | null;
const matches = new Map<string, BrainMatchView>();
let festRoster: RosterTeam[] = [];
let rosterView: HTMLElement | null = null;
let activeTab = tabFromHash() || "grid";
let resyncScheduled = false;

// BlockBucket is one scheme Block's stages, in scheme order: the unit the
// tabs think in — one crosstab tab (when the Block ranks) plus one протоколы
// tab per Block, mirroring the source workbook's tab pairs.
interface BlockBucket {
  block: string;
  label: string;
  stages: BrainSchemeStage[];
  ranks: boolean;
}

function blockBuckets(): BlockBucket[] {
  const buckets: BlockBucket[] = [];
  let current: BlockBucket | null = null;
  for (const stage of scheme.stages || []) {
    if (stageKind(stage) === "reseed") {
      current = null;
      continue;
    }
    // scheme_json written before grain existed unmarshals without one; the
    // -gN code convention still names the Block.
    const block = stage.grain?.block || String(stage.code || "").replace(/-g\d+$/, "") || "";
    if (!current || current.block !== block) {
      current = {block, label: "", stages: [], ranks: false};
      buckets.push(current);
    }
    current.stages.push(stage);
    if (stageKind(stage) === "rr") current.ranks = true;
  }
  for (const bucket of buckets) bucket.label = blockLabel(bucket);
  return buckets;
}

// blockLabel names a Block's tabs: the групп's common prefix («1-й групповой
// этап. Группа 1» → «1-й групповой этап», «DE 1» → «DE»), or for a bracket of
// titled rounds their common prefix, else «Плей-офф».
function blockLabel(bucket: BlockBucket): string {
  const first = String(bucket.stages[0]?.title || "");
  const grouped = bucket.stages.some((stage) => stage.grain?.group);
  if (grouped) {
    const named = first.replace(/\.?\s*Группа\s*\S+$/, "");
    if (named !== first) return named;
    return first.replace(/\s*\d+$/, "") || first;
  }
  if (bucket.stages.length === 1) return first;
  const prefix = first.split(". ")[0];
  if (prefix !== first && bucket.stages.every((stage) => String(stage.title || "").startsWith(prefix + ". "))) {
    return prefix;
  }
  return "Плей-офф";
}

function reseedStages(): BrainSchemeStage[] {
  return (scheme.stages || []).filter((stage) => stageKind(stage) === "reseed");
}

// The tab set mirrors the source workbook: the Сетка first, then per Block its
// crosstabs and its протоколы, the one folded Пересев, the player stats and
// the составы. The Посев tab appears only for the host of a game whose scheme
// declares an [init] seed source.
function visibleTabs(): Array<{key: string; label: string}> {
  const tabs = [{key: "grid", label: "Сетка"}];
  for (const bucket of blockBuckets()) {
    if (bucket.ranks) tabs.push({key: `block:${bucket.block}`, label: bucket.label});
    tabs.push({key: `protocol:${bucket.block}`, label: `${bucket.label} (протоколы)`});
  }
  if (reseedStages().length) tabs.push({key: "reseed", label: "Пересев"});
  tabs.push({key: "stats", label: "Индивидуальная статистика"});
  tabs.push({key: "roster", label: "Составы"});
  if (!viewer && scheme.seeding?.source) tabs.push(SEED_TAB);
  return tabs;
}

function stageKind(stage: BrainSchemeStage): string {
  return stage.kind || stage.stage_type || "";
}

// protocolStages are the stages whose бої the page draws — everything except
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

// questionsFor is a бой's regular question count: its stage's override (the
// DSL's per-block/round questions cascade), else the scheme-wide default.
function questionsFor(code: string): number {
  const stage = stageByMatch.get(code);
  const n = Number(stage ? groupRules(stage).questions : 0);
  return Number.isInteger(n) && n > 0 ? n : schemeQuestions();
}

function tabFromHash(): string | null {
  let key = (window.location.hash || "").replace(/^#/, "");
  // The pre-Block tabs: one crosstable, one протоколы. Old bookmarks land on
  // the first Block's pair rather than silently falling back to the Сетка.
  if (key === "table" || key === "protocol") {
    const first = blockBuckets().find((bucket) => key === "table" ? bucket.ranks : true);
    key = first ? (key === "table" ? `block:${first.block}` : `protocol:${first.block}`) : "grid";
  }
  return visibleTabs().some((t) => t.key === key) ? key : null;
}

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});


function normalizeState(view: BrainMatchView): void {
  const state = (view.state && typeof view.state === "object" ? view.state : {}) as BrainMatchState;
  view.state = state;
  if (!Number.isInteger(state.tiebreaks) || (state.tiebreaks as number) < 0) state.tiebreaks = 0;
  if (!Array.isArray(state.teams)) state.teams = [];
  while (state.teams.length < 2) state.teams.push({rows: []});
  const rowCount = questionsFor(view.code || "") + (state.tiebreaks as number);
  state.teams = state.teams.map((side) => {
    const rows = Array.isArray(side?.rows) ? side!.rows! : [];
    while (rows.length < rowCount) rows.push({player: "", mark: ""});
    return {
      rows: rows.map((row) => ({
        player: typeof row?.player === "string" ? row.player : "",
        mark: row?.mark === "right" || row?.mark === "wrong" ? row.mark : "",
      })),
    };
  });
}

function adoptMatchView(view: BrainMatchView | null | undefined): boolean {
  if (!view?.code) return false;
  const cached = matches.get(view.code);
  if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
  normalizeState(view);
  matches.set(view.code, view);
  return true;
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
    fetchMatches().catch(() => setStatus("error"));
  }, 250);
}

function loadFestRoster(): void {
  gameTable.fetchFestRoster(route.festID)
    .then((teams) => {
      festRoster = teams;
      if (activeTab === "protocol") render({preserveScroll: true});
    })
    .catch(() => {});
}

function handleScopedMessage(message: ScopedEventMessage): void {
  const prefix = `match:${scopeGameID}:`;
  if (!message.scope?.startsWith(prefix)) return;
  const code = message.scope.slice(prefix.length);
  const cached = matches.get(code);
  const seq = Number(message.seq) || 0;
  if (cached && seq <= Number(cached.seq || 0)) return;
  if (Array.isArray(message.ops)) {
    if (!cached || Number(message.prevSeq) !== Number(cached.seq || 0)) {
      scheduleResync();
      return;
    }
    const next = gameTable.applyDeltaOps(cached, message.ops) as BrainMatchView;
    next.seq = seq;
    adoptMatchView(next);
  } else {
    const view = message.data as BrainMatchView | null;
    if (!view?.code) {
      scheduleResync();
      return;
    }
    view.seq = seq;
    adoptMatchView(view);
  }
  render({preserveScroll: true});
  setStatus("saved");
}

const live = gameTable.createLiveEvents({
  eventsURL: () => gameTable.gameEventsURL(route.festID!, route.gameID),
  onMessage: handleScopedMessage,
  onViewers: (count) => viewerCounter.setCount(count),
  onLockdown: gameTable.scheduleStaticReload,
  reload: fetchMatches,
  staticMode: () => staticMode,
});


async function postMatch(code: string, suffix: string, method: string, body: unknown): Promise<void> {
  setStatus("saving");
  const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(code)}/${suffix}`, {
    method,
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await response.text());
  const updated = await response.json() as BrainMatchView;
  adoptMatchView(updated);
  render({preserveScroll: true});
  setStatus("saved");
}

function sendOps(code: string, ops: Array<{path: Array<string | number>; value: unknown}>): void {
  postMatch(code, "state", "PATCH", {ops}).catch((error: unknown) => {
    console.error(error);
    setStatus("error");
    scheduleResync();
  });
}

function sendFinish(code: string, finished: boolean): void {
  postMatch(code, "finish", "POST", {finished}).catch((error: unknown) => {
    console.error(error);
    setStatus("error");
    scheduleResync();
  });
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
  return view.participants?.[side]?.name || `Команда ${side + 1}`;
}

function rowLabel(index: number, base: number): string {
  if (index < base) return String(index + 1);
  return index === base ? "П" : `П${index - base + 1}`;
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

// allBouts flattens every protocol stage's бої in scheme order — the selection
// grid's column space spans them all.
function allBouts(): BoutEntry[] {
  return protocolStages().flatMap(stageBouts);
}


function render(options: {preserveScroll?: boolean} = {}): void {
  const title = fest?.gameName || scheme.title || "Брейн";
  document.title = `${title} · dope`;
  if (breadcrumbsNode && route.festID) {
    gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
      festHref: viewer ? `/fest/${route.festID}` : `/host/fest/${route.festID}`,
      festTitle: fest?.title || "Фест",
      gameTitle: title,
    });
  }
  renderTabs();
  const frame = brainRoot.closest(".sheet-frame");
  const scrollTop = frame?.scrollTop || 0;
  const bucket = blockBuckets().find((b) => activeTab === `block:${b.block}` || activeTab === `protocol:${b.block}`);
  const node = activeTab === "roster"
    ? (rosterView ||= gameTable.buildRosterView(route.festID))
    : activeTab === "seed"
      ? buildSeedView()
      : activeTab === "stats"
        ? buildStatsView()
        : activeTab === "reseed"
          ? buildReseedTab()
          : bucket && activeTab.startsWith("block:")
            ? buildCrosstable(bucket)
            : bucket
              ? buildProtocols(bucket)
              : buildGrid();
  brainRoot.replaceChildren(node);
  brainRoot.classList.toggle("fits-frame", activeTab === "roster");
  scheduleBrainNameOverflowUpdate();
  if (options.preserveScroll && frame) frame.scrollTop = scrollTop;
  restoreSelection();
}

function renderTabs(): void {
  if (!brainTabsRoot) return;
  brainTabsRoot.hidden = false;
  gameTable.renderTabBar(brainTabsRoot, visibleTabs(), activeTab, (key) => {
    activeTab = key;
    if (window.location.hash.replace(/^#/, "") !== key) {
      history.replaceState(null, "", `#${key}`);
    }
    render();
  });
}

// The live fest view feeds the reseed panels (entries, sort rules). The init
// snapshot goes stale, so «Рассчитать» adopts the fresh view it gets back.
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
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/stages/${encodeURIComponent(code)}/reseed`, {method: "POST"});
    if (!response.ok) throw new Error((await response.text()).trim() || "Не удалось рассчитать пересев");
    adoptFestStages(await response.json() as FestInfo);
    reseedError.delete(code);
    setStatus("saved");
  } catch (error) {
    reseedError.set(code, error instanceof Error ? error.message : String(error));
    setStatus("error");
  }
  render({preserveScroll: true});
}

function buildBrainReseedPanel(stage: BrainSchemeStage): HTMLElement {
  const code = stage.code || "";
  const pending = reseedPendingBouts(stage);
  const blocked = pending.length === 1
    ? `Бой ${pending[0]} не закончен`
    : pending.length > 1 ? `Бои ${pending.join(", ")} не закончены` : "";
  const panel = buildReseedStagePanel({...(festStages.get(code) || {}), code}, {
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

// buildProtocols lays one Block's бої out the way the sheet's протоколы tab
// does: a section per stage (группа, pod, round), its бої side by side.
function buildProtocols(bucket: BlockBucket): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const multi = bucket.stages.length > 1;
  let rendered = 0;
  for (const stage of bucket.stages) {
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
    empty.textContent = "Бои ещё не загружены.";
    wrap.appendChild(empty);
  }
  return wrap;
}

// buildGrid is the Сетка: the whole Game at a glance from the same fest data
// the ЭК pages draw — every Block one column, М-grain, no protocol detail.
function buildGrid(): HTMLElement {
  const stages: FestGridStage[] = [];
  for (const viewStage of fest?.stages || []) {
    if (viewStage?.code) stages.push(festStages.get(viewStage.code) || viewStage);
  }
  return buildFestGrid({schemaJson: fest?.schemaJson, stages},
    {stageHeaderLink: false, matchTitleLink: false});
}

// buildReseedTab stacks every reseed's panel, each under the name of the
// stage it seats.
function buildReseedTab(): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "reseed-fold";
  const stages = scheme.stages || [];
  const reseeds = reseedStages();
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

// buildStatsView is the sheet's «Индивидуальная статистика»: попытки, верно,
// неверно per (player, team) over the regular questions of finished бои.
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
  wrapper.className = "results-wrapper brain-stats-wrapper";
  if (!stats.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = "Пока никто не жал на кнопку.";
    wrapper.appendChild(empty);
    return wrapper;
  }
  const table = document.createElement("table");
  table.className = "results-table brain-stats-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  const th = (text: string, className = "number") => {
    const cell = document.createElement("th");
    cell.className = className;
    cell.textContent = text;
    head.appendChild(cell);
  };
  th("Игрок", "results-team-head brain-stats-name-head");
  th("Команда", "results-team-head brain-stats-name-head");
  th("Попытки");
  th("Верно");
  th("Неверно");
  th("% верных");
  thead.appendChild(head);
  table.appendChild(thead);
  const tbody = document.createElement("tbody");
  for (const row of stats) {
    const tr = document.createElement("tr");
    const td = (text: string, className = "number") => {
      const cell = document.createElement("td");
      cell.className = className;
      cell.textContent = text;
      tr.appendChild(cell);
    };
    td(row.player, "results-team brain-stats-name");
    td(row.team, "results-team brain-stats-name");
    td(String(row.attempts));
    td(String(row.right));
    td(String(row.wrong));
    td(row.attempts ? `${Math.round((row.right / row.attempts) * 100)}%` : "");
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  wrapper.appendChild(table);
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

  // Team names take the header row at double width — each spans its player and
  // mark columns, the way the sheet merges them — and the score gets a row of
  // its own beneath. Double width fits most names; the rest fade to a popover.
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  const corner = document.createElement("th");
  corner.className = "row-marker brain-bout-corner";
  corner.rowSpan = 2;
  corner.textContent = (code.split("-").pop() || code).replace(/^m/, "");
  corner.title = view.title || code;
  head.appendChild(corner);
  head.appendChild(nameHead(view, 0, planned));
  head.appendChild(nameHead(view, 1, planned));
  head.appendChild(finishHead(code, view));
  thead.appendChild(head);
  const scoreRow = document.createElement("tr");
  const score = document.createElement("th");
  score.className = "number brain-score-head";
  score.colSpan = 4;
  score.textContent = `${taken(view, 0)} : ${taken(view, 1)}`;
  scoreRow.appendChild(score);
  const scoreGap = document.createElement("th");
  scoreGap.className = "brain-finish-gap";
  scoreRow.appendChild(scoreGap);
  thead.appendChild(scoreRow);
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
// (Посев 5, Гр. 1-2 — the server fills it from the slot ref) muted.
function nameHead(view: BrainMatchView, side: number, planned: BrainSchemeMatch): HTMLElement {
  const th = document.createElement("th");
  th.className = "brain-name-head";
  th.colSpan = 2;
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
  text.textContent = "Закончен";
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
  td.title = `${teamName(view, side)}, вопрос ${rowLabel(q, questionsFor(code))}`;
  return td;
}

// tiebreakControls adds/removes «П» rows — the tiebreak questions appended
// after the base K when this бой must produce a winner.
function tiebreakControls(code: string, view: BrainMatchView): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "brain-controls";
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn-xs";
  add.textContent = "+ П";
  add.title = "Добавить вопрос перестрелки";
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
    remove.textContent = "− П";
    remove.title = "Убрать пустой вопрос перестрелки";
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


interface CrossRow {
  key: string;
  name: string;
  points: number;
  plus: number;
  minus: number;
  rank: number;
}

// slotKey is a stable identity for an entrant ref, whatever grain it is:
// seeds by number/position, rank refs by stage+rank.
function slotKey(slot: SchemeSlotRef | null | undefined): string {
  if (!slot) return "";
  if (slot.seed?.number) return `s${slot.seed.number}`;
  if (slot.seed?.position) return `p${slot.seed.position}`;
  if (slot.reseed) return `r${slot.reseed.stage || ""}:${slot.reseed.rank || 0}`;
  return slot.label || "";
}

// buildCrosstable stacks one group table per rr stage: score cells vs each
// opponent, then О (head-to-head points, finished бои only), + / − / +/−
// (questions taken and conceded across all бои), М (place, ranked by the
// stage's comparator order — КИНСБФ §4.2 by default).
function buildCrosstable(bucket: BlockBucket): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const groups = bucket.stages.filter((stage) => stageKind(stage) === "rr");
  if (!groups.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = "В этой схеме нет групповых таблиц.";
    wrap.appendChild(empty);
    return wrap;
  }
  for (const stage of groups) {
    wrap.appendChild(buildGroupTable(stage));
  }
  return wrap;
}

function buildGroupTable(stage: BrainSchemeStage): HTMLElement {
  const rules = groupRules(stage);
  const entrants = rules.entrants || [];
  const win = rules.points?.win ?? 2;
  const draw = rules.points?.draw ?? 1;
  const loss = rules.points?.loss ?? 0;
  const rows: CrossRow[] = entrants.map((slot) => ({
    key: slotKey(slot),
    name: slot.label || "",
    points: 0,
    plus: 0,
    minus: 0,
    rank: 0,
  }));
  const indexByKey = new Map<string, number>();
  rows.forEach((row, i) => indexByKey.set(row.key, i));
  const cellText: string[][] = rows.map(() => rows.map(() => ""));
  const cellMuted: boolean[][] = rows.map(() => rows.map(() => false));
  const duels: RankDuel[] = [];

  for (const planned of stage.matches || []) {
    const view = matches.get(planned.code || "");
    if (!view) continue;
    const a = indexByKey.get(slotKey(planned.slots?.[0]));
    const b = indexByKey.get(slotKey(planned.slots?.[1]));
    if (a === undefined || b === undefined) continue;
    if (view.participants?.[0]?.name) rows[a].name = view.participants[0].name;
    if (view.participants?.[1]?.name) rows[b].name = view.participants[1].name;
    const ta = taken(view, 0);
    const tb = taken(view, 1);
    if (view.finished || started(view)) {
      cellText[a][b] = `${ta} : ${tb}`;
      cellText[b][a] = `${tb} : ${ta}`;
      cellMuted[a][b] = cellMuted[b][a] = !view.finished;
      rows[a].plus += ta;
      rows[a].minus += tb;
      rows[b].plus += tb;
      rows[b].minus += ta;
    }
    if (view.finished) {
      const [pa, pb] = ta > tb ? [win, loss] : ta < tb ? [loss, win] : [draw, draw];
      rows[a].points += pa;
      rows[b].points += pb;
      duels.push({a, b, pa, pb});
    }
  }

  const rankTeams: RankTeam[] = rows.map((row) => ({points: row.points, taken: row.plus, conceded: row.minus}));
  const ranks = rankGroup(rankTeams, duels, rules.order || undefined);
  rows.forEach((row, i) => {
    row.rank = ranks[i];
  });

  const table = document.createElement("table");
  table.className = "match-table brain-crosstable";

  const thead = document.createElement("thead");
  const group = document.createElement("tr");
  const groupHead = document.createElement("th");
  groupHead.colSpan = rows.length + 7;
  groupHead.className = "brain-group-head";
  groupHead.textContent = stage.title || "Группа";
  group.appendChild(groupHead);
  thead.appendChild(group);

  const cols = document.createElement("tr");
  cols.className = "brain-cross-cols";
  const headCell = (text: string, className: string) => {
    const th = document.createElement("th");
    th.className = className;
    th.textContent = text;
    cols.appendChild(th);
  };
  headCell("№", "row-marker");
  headCell("Команда", "brain-cross-team-head");
  rows.forEach((_, i) => headCell(String(i + 1), "brain-cross-num"));
  headCell("О", "brain-cross-num");
  headCell("+", "brain-cross-num");
  headCell("−", "brain-cross-num");
  headCell("+/−", "brain-cross-num");
  headCell("М", "brain-cross-num");
  thead.appendChild(cols);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  rows.forEach((row, i) => {
    const tr = document.createElement("tr");
    const marker = document.createElement("td");
    marker.className = "row-marker";
    marker.textContent = String(i + 1);
    tr.appendChild(marker);
    const name = document.createElement("td");
    name.className = "brain-cross-team";
    const wrap = document.createElement("span");
    wrap.className = "brain-name-wrap";
    const label = document.createElement("span");
    label.className = "brain-name";
    label.textContent = row.name;
    label.tabIndex = 0;
    label.setAttribute("aria-label", row.name);
    wrap.appendChild(label);
    name.appendChild(wrap);
    const popover = document.createElement("span");
    popover.className = "popover popover-inline brain-name-popover";
    popover.textContent = row.name;
    name.appendChild(popover);
    tr.appendChild(name);
    rows.forEach((_, j) => {
      const cell = document.createElement("td");
      cell.className = "number brain-cross-cell";
      if (i === j) {
        cell.textContent = "×";
        cell.classList.add("brain-cross-diag");
      } else {
        cell.textContent = cellText[i][j];
        cell.classList.toggle("brain-cross-live", cellMuted[i][j]);
      }
      tr.appendChild(cell);
    });
    const stat = (value: string | number, extra = "") => {
      const cell = document.createElement("td");
      cell.className = "number" + (extra ? ` ${extra}` : "");
      cell.textContent = gameTable.formatDisplayText(value);
      tr.appendChild(cell);
    };
    stat(row.points);
    stat(row.plus);
    stat(row.minus);
    stat(row.plus - row.minus);
    stat(row.rank, "brain-cross-place");
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  return table;
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

// buildSeedView is the Посев tab: the declared source, the one import button
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
    upload.textContent = "Загрузить посев из xlsx";
    upload.disabled = seedBusy;
    upload.addEventListener("click", () => {
      const chosen = file.files?.[0];
      if (!chosen) {
        seedError = "Выберите файл";
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
    importButton.textContent = source === "random" ? "Провести жребий" : `Импортировать посев из ${source}`;
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
    empty.textContent = "Посев ещё не импортирован.";
    wrap.appendChild(empty);
    return wrap;
  }

  const table = document.createElement("table");
  table.className = "match-table brain-seed-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.className = "brain-cross-cols";
  for (const text of ["Посев", "Команда", "Город", "Место в источнике", "Отказ"]) {
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
    seed.textContent = row.declined ? "—" : row.waitlist ? "запас" : String(row.seedNumber || "");
    const name = document.createElement("td");
    name.className = "brain-cross-team";
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

const MARK_CYCLE: Record<string, string> = {"": "right", right: "wrong", wrong: ""};

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
    `.answer-cell[data-match="${gameTable.cssEscape(code)}"][data-side="${side}"][data-q="${q}"]`,
  );
}

// The selection treats the бої laid side by side as one sheet: row = the
// question, col = бой × 2 + side. The widget (shared with КСИ) then gives
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

function serializeMark(cell: Element | null | undefined): string {
  const el = cell as HTMLElement | null;
  return el?.classList.contains("right") ? "1" : el?.classList.contains("wrong") ? "0" : "";
}

function parseMarkText(text: string): string {
  const value = String(text || "").trim().toLowerCase();
  if (["1", "+", "q", "й", "right"].includes(value)) return "right";
  if (["0", "-", "\u2212", "w", "ц", "wrong"].includes(value)) return "wrong";
  return "";
}

// applyMarkEdits updates the local views and DOM, then sends one PATCH per бой.
function applyMarkEdits(edits: CellEdit[]): void {
  const opsByCode = new Map<string, Array<{path: Array<string | number>; value: unknown}>>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const ctx = cellContext(cell);
    if (!ctx || viewer || ctx.view.finished) continue;
    const mark = parseMarkText(String(edit.value ?? ""));
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

const cellSelection: CellRangeSelection = gameTable.createCellRangeSelection({
  root: brainRoot,
  cellSelector: ".answer-cell",
  readonly: () => viewer,
  coordOf: (cell) => {
    const ctx = cellContext(cell as HTMLElement);
    return ctx ? cellCoord(ctx.code, ctx.side, ctx.q) : null;
  },
  cellAtCoord: cellAt,
  serialize: serializeMark,
  parse: parseMarkText,
  cycle: (cell) => MARK_CYCLE[parseMarkText(serializeMark(cell))] ?? "right",
  applyValues: applyMarkEdits,
  onActiveChange: (cell) => {
    brainRoot.querySelector(".answer-cell.active")?.classList.remove("active");
    cell?.classList.add("active");
  },
});
cellSelection.bind();

// restoreSelection re-applies the cursor after a re-render rebuilt the cells.
function restoreSelection(): void {
  if (activeTab !== "protocol" || !cellSelection.anchor) return;
  cellSelection.setSelection(cellSelection.anchor, cellSelection.focus, {focus: false});
}

function moveActive(dRow: number, dCol: number, extend: boolean): void {
  const from = cellSelection.focus;
  if (!from) return;
  const col = gameTable.clamp(from.col + dCol, 0, totalCols() - 1);
  const rows = matchRows(boutAtCol(col)!.view, 0).length;
  const next = {row: gameTable.clamp(from.row + dRow, 0, rows - 1), col};
  cellSelection.setSelection(extend ? cellSelection.anchor || from : next, next);
}

function setMarkForSelection(mark: string): void {
  const cells = cellSelection.selectedCells();
  const active = cellAt(cellSelection.focus);
  const targets = cells.length > 1 ? cells : active ? [active] : [];
  applyMarkEdits(targets.map((cell) => ({cell, value: mark})));
}

function handleKeydown(event: KeyboardEvent): void {
  if (viewer || activeTab !== "protocol" || !cellSelection.focus) return;
  const target = event.target as HTMLElement | null;
  if (target && (target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  const key = event.key.toLowerCase();
  if (event.key === "ArrowUp") moveActive(-1, 0, event.shiftKey);
  else if (event.key === "ArrowDown") moveActive(1, 0, event.shiftKey);
  else if (event.key === "ArrowLeft") moveActive(0, -1, event.shiftKey);
  else if (event.key === "ArrowRight") moveActive(0, 1, event.shiftKey);
  else if (key === "q" || key === "й" || key === "+" || key === "1" || event.code === "NumpadAdd") setMarkForSelection("right");
  else if (key === "w" || key === "ц" || key === "-" || key === "0" || event.code === "NumpadSubtract") setMarkForSelection("wrong");
  else if (key === "backspace" || key === "delete" || event.key === " ") setMarkForSelection("");
  else return;
  event.preventDefault();
}

document.addEventListener("keydown", handleKeydown);


render();
gameTable.fitScrollFade(brainRoot.closest(".sheet-frame"));
loadFestRoster();
fetchMatches()
  .then(() => {
    setStatus("saved");
    live.connect();
  })
  .catch((error: unknown) => {
    setStatus("error");
    console.error(error);
  });

export {};
