// The Тройка page (ADR-0001): a bracket of head-to-head бои between threes.
// The Протоколы tab draws each бой as two blocks of three chair rows across
// темы of three вопросы, with a «поменялись местами» control between темы; the
// group tabs are crosstable.ts's table with the регламент's рейтинговый балл
// in front of the canon columns. Edits go per бой (PATCH /matches/{code}/state)
// and sync over match: scopes. A self-booting side-effect module bundled by
// pages/troika.ts.

import {cssEscape, td, th} from "./cells.js";
import {festLetters} from "./standings.js";
import type {StageRef} from "./standings.js";
import {buildRosterView, fetchFestRoster} from "./fest-roster.js";
import type {RosterTeam} from "./fest-roster.js";
import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameInitLike} from "./game-page.js";
import {renderTabBar} from "./widgets.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {buildCrosstables, CANON_COLUMNS} from "./crosstable.js";
import type {CrossSlot} from "./crosstable.js";
import {buildFestGrid} from "./fest-grid.js";
import type {FestGridStage} from "./fest-grid.js";
import {gameTabs, groupLabel} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import * as troika from "./troika-protocol.js";
import type {TroikaState} from "./troika-protocol.js";
import {buildTroikaStatsTable, computeTroikaPlayerStats} from "./troika-stats.js";
import type {TroikaBout} from "./troika-stats.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}
const pageWindow = window as Window & PageGlobals;

interface FestInfo {
  title?: string;
  gameName?: string;
  schemaJson?: unknown;
  stages?: FestGridStage[];
  [key: string]: unknown;
}

interface SchemeSlotRef {
  label?: string;
  seed?: {number?: number; position?: number};
  reseed?: {stage?: string; rank?: number};
}

interface SchemeMatch {
  code?: string;
  title?: string;
  slots?: SchemeSlotRef[];
}

interface SchemeStage {
  code?: string;
  title?: string;
  kind?: string;
  stage_type?: string;
  matches?: SchemeMatch[];
  config?: {entrants?: SchemeSlotRef[]};
  grain?: {block?: string; group?: number};
}

interface TroikaScheme {
  title?: string;
  stages?: SchemeStage[];
  seeding?: {source?: string};
}

interface MatchSeat {
  id?: number;
  name?: string;
  roster?: Array<{id?: number; name?: string}>;
}

interface TroikaMatchView {
  code?: string;
  title?: string;
  finished?: boolean;
  seq?: number;
  state?: unknown;
  participants?: MatchSeat[];
}

const root = document.getElementById("troikaTable")!;
const tabsRoot = document.getElementById("troikaTabs");
const statusNode = document.getElementById("status");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const route = parseGameRoute();
const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as TroikaScheme;
const fest = (init?.fest || null) as FestInfo | null;
const shell = mountGamePage({
  app: "troika",
  root,
  statusNode,
  breadcrumbsNode,
  festID: route.festID,
  gameID: route.gameID,
  viewer: Boolean(route.viewer),
  apiBase: route.apiBase,
  init,
  downloads: false,
  chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme.title || "Тройка"}),
  cursorKinds: {
    answer: {selector: ".troika-cell", keys: ["match", "side", "theme", "q", "chair"]},
  },
  activeCursorElement: () => cursor.activeCell,
});
const {viewer, staticMode, scopeGameID, indicator, viewerCounter} = shell;

const matches = new Map<string, TroikaMatchView>();
const states = new Map<string, TroikaState>();
const festStages = new Map<string, FestGridStage>();
for (const stage of fest?.stages || []) {
  if (stage?.code) festStages.set(stage.code, stage);
}
let festRoster: RosterTeam[] = [];
let rosterView: HTMLElement | null = null;
let resyncScheduled = false;

const boutLetters = festLetters(fest?.stages as StageRef[] | undefined);

function tabs(): GameTab[] {
  return gameTabs((scheme.stages || []) as StageRef[],
    {game: "troika", viewer, seeded: Boolean(scheme.seeding?.source)});
}

function tabStages(tab: GameTab): SchemeStage[] {
  return (scheme.stages || []).filter((stage) => tab.stages.includes(stage.code || ""));
}

function stageKind(stage: SchemeStage): string {
  return stage.kind || stage.stage_type || "";
}

function tabFromHash(): string | null {
  const key = (window.location.hash || "").replace(/^#/, "");
  return tabs().some((tab) => tab.key === key) ? key : null;
}

let activeTab = tabFromHash() || "grid";

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});

// === the document ===

function matchScope(code: string): string {
  return `match:${scopeGameID}:${code}`;
}

function adoptMatchView(view: TroikaMatchView | null | undefined): boolean {
  const code = view?.code;
  if (!view || !code) return false;
  const cached = matches.get(code);
  if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
  view = writer.overlay(matchScope(code), view) as TroikaMatchView;
  matches.set(code, view);
  states.set(code, troika.parseState(view.state));
  return true;
}

function stateOf(code: string): TroikaState {
  return states.get(code) || troika.parseState(null);
}

async function fetchMatches(): Promise<void> {
  const response = await fetch(`${route.apiBase}/stages/matches`);
  if (!response.ok) throw new Error(`stages/matches ${response.status}`);
  const stages = await response.json() as Array<{code?: string; matches?: TroikaMatchView[]}>;
  for (const stage of stages || []) {
    for (const view of stage.matches || []) adoptMatchView(view);
  }
  render();
}

function scheduleResync(): void {
  if (resyncScheduled) return;
  resyncScheduled = true;
  setTimeout(() => {
    resyncScheduled = false;
    fetchMatches().catch(() => indicator.fail());
  }, 250);
}

const live = createLiveEvents({
  eventsURL: () => gameEventsURL(route.festID!, route.gameID),
  gameID: scopeGameID,
  scopes: [{
    prefix: "fest:",
    adopt: (_scope, view) => {
      const fresh = view.data as FestInfo | null;
      if (!fresh?.stages) return;
      for (const stage of fresh.stages) if (stage?.code) festStages.set(stage.code, stage);
      render();
    },
  }, {
    prefix: `match:${scopeGameID}:`,
    base: (scope) => {
      const cached = matches.get(scope.slice(`match:${scopeGameID}:`.length));
      return cached ? {data: cached, seq: Number(cached.seq || 0)} : null;
    },
    adopt: (_scope, view) => {
      const next = view.data as TroikaMatchView | null;
      if (!next?.code) {
        scheduleResync();
        return;
      }
      next.seq = view.seq;
      adoptMatchView(next);
      render();
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
  docPath: ["state"],
  adopt: (_scope, response) => {
    adoptMatchView(response as TroikaMatchView);
    render();
  },
  indicator,
  onRejected: () => scheduleResync(),
});

function patch(code: string, path: Array<string | number>, value: unknown): void {
  writer.patch(matchScope(code), path, value);
}

// === the протокол sheet ===

interface BoutEntry {
  code: string;
  view: TroikaMatchView;
  planned: SchemeMatch;
  stage: SchemeStage;
}

function protocolStages(): SchemeStage[] {
  return (scheme.stages || []).filter((stage) => (stage.matches || []).length > 0);
}

function stageBouts(stage: SchemeStage): BoutEntry[] {
  const out: BoutEntry[] = [];
  for (const planned of stage.matches || []) {
    const code = planned.code || "";
    const view = matches.get(code);
    if (view) out.push({code, view, planned, stage});
  }
  return out;
}

function seatName(view: TroikaMatchView, side: number): string {
  return view.participants?.[side]?.name || `Команда ${side + 1}`;
}

// boutRoster is the three (or more) people a side may field, by id. A бой's
// own seat roster is what the server sent; the фест registry fills in for a
// seat whose players it did not carry.
function boutRoster(view: TroikaMatchView, side: number): Array<{id: number; name: string}> {
  const seat = view.participants?.[side];
  const carried = (seat?.roster || [])
    .filter((player) => player && typeof player.id === "number")
    .map((player) => ({id: Number(player.id), name: player.name || ""}));
  if (carried.length) return carried;
  const team = festRoster.find((entry) => entry.name === seat?.name);
  // A фест player carries no бой id, so the roster's own position stands in —
  // negative, so it can never collide with a real one.
  return (team?.players || []).map((player, index) => ({
    id: -(index + 1),
    name: typeof player === "string" ? player : player?.name || "",
  }));
}

// One бой: a block per side of three chair rows by темы × three вопросы, with
// the theme's нарицательная over each block and a running Σ beside it.
function buildBout(bout: BoutEntry): HTMLElement {
  const state = stateOf(bout.code);
  const box = document.createElement("section");
  box.className = "troika-bout";

  const head = document.createElement("h3");
  head.className = "troika-bout-head";
  const letter = boutLetters.get(bout.code);
  head.textContent = [letter, bout.planned.title || bout.view.title || bout.code].filter(Boolean).join(". ");
  box.appendChild(head);

  const table = document.createElement("table");
  table.className = "match-table troika-sheet";

  const thead = document.createElement("thead");
  const themeRow = document.createElement("tr");
  themeRow.appendChild(th("", "team-col"));
  themeRow.appendChild(th("", "num-col"));
  state.values.forEach((value, t) => {
    themeRow.appendChild(th(`Тема ${t + 1}${value === 1 ? "" : ` · ${value}`}`, "theme-block",
      {colspan: String(troika.THEME_QUESTIONS)}));
  });
  themeRow.appendChild(th("Σ", "total-col"));
  thead.appendChild(themeRow);
  table.appendChild(thead);

  const body = document.createElement("tbody");
  for (let side = 0; side < 2; side++) {
    const roster = boutRoster(bout.view, side);
    const total = troika.sideTotal(state, side);
    for (let chair = 0; chair < troika.CHAIRS; chair++) {
      const tr = document.createElement("tr");
      if (chair === 0) {
        const teamCell = td(seatName(bout.view, side), "team-col troika-team",
          {rowspan: String(troika.CHAIRS)});
        tr.appendChild(teamCell);
      }
      tr.appendChild(td(chairPicker(bout, side, chair, roster), "num-col troika-chair"));
      state.values.forEach((_value, t) => {
        for (let q = 0; q < troika.THEME_QUESTIONS; q++) {
          tr.appendChild(markCell(bout.code, side, t, q, chair, state));
        }
      });
      if (chair === 0) {
        tr.appendChild(td(String(total), "number total-col troika-total",
          {rowspan: String(troika.CHAIRS), "data-total": `${bout.code}-${side}`}));
      }
      body.appendChild(tr);
    }
    if (side === 0) {
      const spacer = document.createElement("tr");
      spacer.className = "troika-side-gap";
      body.appendChild(spacer);
    }
  }
  table.appendChild(body);
  box.appendChild(table);
  return box;
}

// The chair cell names who is sitting there for the тема the sheet opens on,
// and lets the host reseat from any тема onward — «здесь поменялись местами».
// Seats are a fact per тема, so a swap rewrites the темы after it and leaves
// the ones already played exactly as they were.
function chairPicker(bout: BoutEntry, side: number, chair: number,
  roster: Array<{id: number; name: string}>): HTMLElement {
  const state = stateOf(bout.code);
  const select = document.createElement("select");
  select.className = "troika-chair-select";
  select.disabled = viewer || Boolean(bout.view.finished);
  select.title = chair === troika.CHAIRS - 1 ? "Коренной" : `Пристяжной ${chair + 1}`;
  const current = troika.chairAt(state, side, swapFrom, chair);
  const blank = document.createElement("option");
  blank.value = "0";
  blank.textContent = "—";
  select.appendChild(blank);
  for (const player of roster) {
    const option = document.createElement("option");
    option.value = String(player.id);
    option.textContent = player.name;
    if (player.id === current) option.selected = true;
    select.appendChild(option);
  }
  select.addEventListener("change", () => {
    const order: number[] = [];
    for (let c = 0; c < troika.CHAIRS; c++) {
      order.push(c === chair ? Number(select.value) || 0 : troika.chairAt(state, side, swapFrom, c));
    }
    troika.swapFrom(state, side, swapFrom, order);
    for (let t = swapFrom; t < state.values.length; t++) {
      patch(bout.code, ["sides", side, "themes", t, "order"], order);
    }
    render();
  });
  return select;
}

// swapFrom is the тема the chair pickers write from — «начиная с темы N».
// Zero is the бой's opening lineup, which is what a host sets first.
let swapFrom = 0;

function markCell(code: string, side: number, theme: number, q: number, chair: number,
  state: TroikaState): HTMLElement {
  const mark = troika.markAt(state, side, theme, q, chair);
  const cell = td(mark === "right" ? "+" : mark === "wrong" ? "−" : "", "troika-cell answer-cell", {
    "data-match": code,
    "data-side": String(side),
    "data-theme": String(theme),
    "data-q": String(q),
    "data-chair": String(chair),
    "data-mark": mark,
  });
  return cell;
}

// === the cursor ===

// The sheet the cursor walks is every бой of the tab stacked: a row is one
// chair of one side of one бой, a column one вопрос. The columns are uniform
// within a бой and may differ between them, which is what the ragged geometry
// is for.
function sheetBouts(): BoutEntry[] {
  const tab = tabs().find((entry) => entry.key === activeTab);
  if (!tab || tab.kind !== "protocol") return [];
  return tabStages(tab).flatMap(stageBouts);
}

function sheetRows(): Array<{code: string; side: number; chair: number}> {
  const rows: Array<{code: string; side: number; chair: number}> = [];
  for (const bout of sheetBouts()) {
    for (let side = 0; side < 2; side++) {
      for (let chair = 0; chair < troika.CHAIRS; chair++) rows.push({code: bout.code, side, chair});
    }
  }
  return rows;
}

const cursor = createSheetCursor({
  root,
  cellSelector: ".troika-cell",
  values: "marks",
  readonly: () => viewer,
  active: () => tabs().find((tab) => tab.key === activeTab)?.kind === "protocol",
  rows: () => sheetRows().length,
  cols: (row: number) => {
    const at = sheetRows()[row];
    return at ? stateOf(at.code).values.length * troika.THEME_QUESTIONS : 0;
  },
  coordOf: (cell) => {
    const node = cell as HTMLElement;
    const code = node.dataset.match || "";
    const side = Number(node.dataset.side);
    const chair = Number(node.dataset.chair);
    const theme = Number(node.dataset.theme);
    const q = Number(node.dataset.q);
    const row = sheetRows().findIndex((entry) => entry.code === code && entry.side === side && entry.chair === chair);
    if (row < 0 || !Number.isInteger(theme) || !Number.isInteger(q)) return null;
    return {row, col: theme * troika.THEME_QUESTIONS + q};
  },
  cellAt: (coord: CellCoord) => {
    const at = sheetRows()[coord.row];
    if (!at) return null;
    const theme = Math.floor(coord.col / troika.THEME_QUESTIONS);
    const q = coord.col % troika.THEME_QUESTIONS;
    return root.querySelector<HTMLElement>(
      `.troika-cell[data-match="${cssEscape(at.code)}"][data-side="${cssEscape(String(at.side))}"]` +
      `[data-chair="${cssEscape(String(at.chair))}"][data-theme="${cssEscape(String(theme))}"]` +
      `[data-q="${cssEscape(String(q))}"]`);
  },
  applyValues: applyMarks,
});

function applyMarks(edits: CellEdit[]): void {
  const touched = new Set<string>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const code = cell.dataset.match || "";
    const side = Number(cell.dataset.side);
    const theme = Number(cell.dataset.theme);
    const q = Number(cell.dataset.q);
    const chair = Number(cell.dataset.chair);
    const state = states.get(code);
    if (!state) continue;
    const mark = parseMark(edit.value);
    const row = state.sides[side]?.themes[theme]?.answers[q];
    if (!row || row[chair] === mark) continue;
    row[chair] = mark;
    cell.dataset.mark = mark;
    cell.textContent = mark === "right" ? "+" : mark === "wrong" ? "−" : "";
    patch(code, ["sides", side, "themes", theme, "answers", q, chair], mark);
    touched.add(code);
  }
  for (const code of touched) refreshTotals(code);
}

// refreshTotals repaints the Σ a бой's cells feed rather than the sheet, so an
// edit does not move the cursor out from under the host.
function refreshTotals(code: string): void {
  const state = stateOf(code);
  for (let side = 0; side < 2; side++) {
    const node = root.querySelector<HTMLElement>(`[data-total="${cssEscape(`${code}-${side}`)}"]`);
    if (node) node.textContent = String(troika.sideTotal(state, side));
  }
}

// === the tabs ===

function buildProtocols(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "troika-protocol";
  const many = stages.length > 1;
  wrap.appendChild(swapControl());
  for (const stage of stages) {
    const bouts = stageBouts(stage);
    if (!bouts.length) continue;
    if (many) {
      const head = document.createElement("h2");
      head.className = "troika-stage-head";
      head.textContent = stage.title || stage.code || "";
      wrap.appendChild(head);
    }
    const row = document.createElement("div");
    row.className = "troika-bouts";
    for (const bout of bouts) row.appendChild(buildBout(bout));
    wrap.appendChild(row);
  }
  return wrap;
}

// The регламент turns the пристяжные round at the половина and teams swap
// oftener, so the host says which тема a lineup takes effect from and the
// chair pickers write from there.
function swapControl(): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "troika-swap-bar";
  const label = document.createElement("label");
  label.textContent = "Рассадка начиная с темы ";
  const select = document.createElement("select");
  select.className = "troika-swap-select";
  const themes = sheetBouts().reduce((most, bout) => Math.max(most, stateOf(bout.code).values.length), 0);
  for (let t = 0; t < themes; t++) {
    const option = document.createElement("option");
    option.value = String(t);
    option.textContent = String(t + 1);
    if (t === swapFrom) option.selected = true;
    select.appendChild(option);
  }
  select.disabled = viewer;
  select.addEventListener("change", () => {
    swapFrom = Number(select.value) || 0;
    render();
  });
  label.appendChild(select);
  bar.appendChild(label);
  return bar;
}

// Троечка's регламент ranks on the рейтинговый балл first, then личная
// встреча, забитые and разница — so its table shows the балл in front of the
// canon columns the crosstable already draws.
function buildGroups(stages: SchemeStage[]): HTMLElement {
  return buildCrosstables({
    className: "troika-groups",
    columns: [{label: "Р", metric: "rating"}, ...CANON_COLUMNS],
    groups: stages.filter((stage) => stageKind(stage) === "rr").map((stage) => ({
      title: groupLabel(stage as StageRef),
      entrants: (stage.config?.entrants || []).map((slot) => ({key: slotKey(slot), label: slot.label || ""})),
      bouts: (stage.matches || []).flatMap((planned) => {
        const view = matches.get(planned.code || "");
        if (!view) return [];
        const state = stateOf(planned.code || "");
        return [{
          slots: [
            {key: slotKey(planned.slots?.[0]), label: planned.slots?.[0]?.label || ""},
            {key: slotKey(planned.slots?.[1]), label: planned.slots?.[1]?.label || ""},
          ] as [CrossSlot, CrossSlot],
          sides: [0, 1].map((side) => ({
            name: view.participants?.[side]?.name || "",
            id: Number(view.participants?.[side]?.id || 0),
            score: troika.sideTotal(state, side),
          })),
          finished: Boolean(view.finished),
          started: troika.started(state),
        }];
      }),
      standings: standingsOf(stage.code || ""),
    })),
  });
}

function standingsOf(stageCode: string): Map<number, Record<string, unknown>> {
  const out = new Map<number, Record<string, unknown>>();
  for (const entry of festStages.get(stageCode)?.standings || []) {
    if (entry.participantID) out.set(Number(entry.participantID), entry.metrics || {});
  }
  return out;
}

function slotKey(slot: SchemeSlotRef | null | undefined): string {
  if (!slot) return "";
  if (slot.seed?.number) return `s${slot.seed.number}`;
  if (slot.seed?.position) return `p${slot.seed.position}`;
  if (slot.reseed) return `r${slot.reseed.stage || ""}:${slot.reseed.rank || 0}`;
  return slot.label || "";
}

function buildStats(): HTMLElement {
  const bouts: TroikaBout[] = [];
  for (const stage of protocolStages()) {
    for (const entry of stageBouts(stage)) {
      bouts.push({
        state: stateOf(entry.code),
        sides: [0, 1].map((side) => ({
          team: seatName(entry.view, side),
          players: new Map(boutRoster(entry.view, side).map((player) => [player.id, player.name])),
        })),
      });
    }
  }
  return buildTroikaStatsTable(computeTroikaPlayerStats(bouts));
}

function buildGrid(): HTMLElement {
  const stages: FestGridStage[] = [];
  for (const stage of fest?.stages || []) {
    if (stage?.code) stages.push(festStages.get(stage.code) || stage);
  }
  return buildFestGrid({schemaJson: fest?.schemaJson, stages},
    {stageHeaderLink: false, matchTitleLink: false, letters: boutLetters});
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "roster":
    return (rosterView ||= buildRosterView(route.festID));
  case "stats":
    return buildStats();
  case "block":
  case "pods":
    return buildGroups(tabStages(tab));
  case "protocol":
    return buildProtocols(tabStages(tab));
  default:
    return buildGrid();
  }
}

function render(): void {
  shell.renderChrome();
  if (tabsRoot) {
    tabsRoot.hidden = false;
    renderTabBar(tabsRoot, tabs(), activeTab, (key) => {
      activeTab = key;
      if (window.location.hash.replace(/^#/, "") !== key) history.replaceState(null, "", `#${key}`);
      render();
    });
  }
  const node = buildTab(tabs().find((tab) => tab.key === activeTab));
  root.replaceChildren(node);
  root.classList.toggle("fits-frame", activeTab === "roster");
  root.classList.toggle("grid-host", Boolean(node.querySelector(".fest-grid")) || node.matches(".fest-grid"));
  cursor.refresh();
}

cursor.bind();
live.connect();
fetchFestRoster(route.festID).then((teams) => {
  festRoster = teams;
  render();
}).catch(() => {});
fetchMatches().catch(() => indicator.fail());
