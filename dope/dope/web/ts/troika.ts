// The Troika page (ADR-0001): a bracket of bouts between threes, two or three
// to a bout, opened by a written qualifier of every troika at once.
// The protocols tab draws each bout as a block of three chair rows per side across
// themes of three questions, with a seating column before every theme a side
// turned round at; the group tabs are crosstable.ts's table with the
// regulations' rating ball in front of the canon columns. Edits go per
// bout (PATCH /matches/{code}/state) and sync over match: scopes. A self-booting side-effect module bundled by
// pages/troika.ts.

import {cssEscape, option, sameArray, td, th} from "./cells.js";
import type {CellContent} from "./cells.js";
import {icon} from "./icons_gen.js";
import {festLetters, standingsTable} from "./standings.js";
import type {StageRef} from "./standings.js";
import {buildRosterView} from "./fest-roster.js";
import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameInitLike} from "./game-page.js";
import {createFloatingPopover, markNameOverflow, renderTabBar} from "./widgets.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {buildCrosstables, CANON_COLUMNS, crossSlot, standingsByParticipant} from "./crosstable.js";
import type {SchemeSlotRef} from "./crosstable.js";
import {buildFestGrid, parseScheme} from "./fest-grid.js";
import type {FestGridStage} from "./fest-grid.js";
import {gameTabs, groupLabel} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import {onNavigate, setHashTab, tabFromHash} from "./url-state.js";
import * as troika from "./troika-protocol.js";
import type {Mark, TroikaState} from "./troika-protocol.js";
import {buildTroikaStatsTable, computeTroikaPlayerStats} from "./troika-stats.js";
import type {TroikaBout} from "./troika-stats.js";
import S from "./i18nstrings.js";

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
  chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme.title || S.troika.title()}),
  cursorKinds: {
    answer: {selector: ".troika-cell", keys: ["match", "side", "theme", "q", "chair"]},
    count: {selector: ".troika-count", keys: ["match", "side", "theme", "q"]},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell || writtenCursor.activeCell,
});
const {viewer, staticMode, scopeGameID, indicator, viewerCounter} = shell;
// Long team names fade at their column and carry a popover, in the group
// tables and the fest grid's boxes alike.
createFloatingPopover({root, specs: [
  {trigger: ".results-team-truncated", popover: ".results-team-name-popover", anchor: ".results-team-name"},
  {trigger: ".grid-slot-team-truncated", popover: ".grid-slot-team-popover", anchor: ".grid-slot-team-name"},
]}).bind();

let nameOverflowFrame = 0;
function scheduleNameOverflow(): void {
  cancelAnimationFrame(nameOverflowFrame);
  nameOverflowFrame = requestAnimationFrame(() => {
    nameOverflowFrame = 0;
    markNameOverflow(root, {cellSelector: ".results-team", nameSelector: ".results-team-name", truncatedClass: "results-team-truncated"});
  });
}
window.addEventListener("resize", scheduleNameOverflow);

const matches = new Map<string, TroikaMatchView>();
const states = new Map<string, TroikaState>();
const festStages = new Map<string, FestGridStage>();
for (const stage of fest?.stages || []) {
  if (stage?.code) festStages.set(stage.code, stage);
}
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

let activeTab = tabFromHash(tabs()) || "grid";

onNavigate(() => {
  const next = tabFromHash(tabs());
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
  states.set(code, troika.parseState(view.state, view.participants?.length || 2));
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
// === the protocol sheet ===

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
  return view.participants?.[side]?.name || S.troika.team.fallback(String(side + 1));
}

// boutRoster is the three (or more) people a side may field. The server sends
// each seat's roster with real player ids (store.SeatsPlayers), which is what
// a chair records — a name matched off the fest registry would not survive
// two players sharing one.
function boutRoster(view: TroikaMatchView, side: number): Array<{id: number; name: string}> {
  return (view.participants?.[side]?.roster || [])
    .filter((player) => player && typeof player.id === "number" && player.id > 0)
    .map((player) => ({id: Number(player.id), name: player.name || ""}));
}

// One bout: a block per side of three chair rows by themes × three questions, with
// a numbered theme head with its value over each block and a running Σ beside it. A seating
// column of chair pickers opens the sheet and stands again before every theme
// where a side turned round.
function buildBout(bout: BoutEntry): HTMLElement {
  const state = stateOf(bout.code);
  if (state.written) return buildWrittenBout(bout);
  const box = document.createElement("section");
  box.className = "troika-bout";
  box.appendChild(boutHead(bout));

  const table = document.createElement("table");
  table.className = "match-table troika-sheet";
  table.classList.toggle("match-finished", Boolean(bout.view.finished));
  const editable = !viewer && !bout.view.finished;
  const seatsAt = seatColumns(bout);

  const thead = document.createElement("thead");
  const themeRow = document.createElement("tr");
  themeRow.appendChild(th(S.troika.protocol.team(), "troika-team-head"));
  state.values.forEach((value, t) => {
    // The gap parts themes BEFORE any seating column, which sits flush
    // against the theme it seats.
    if (t > 0) themeRow.appendChild(th("", "gap-head"));
    if (seatsAt.has(t)) themeRow.appendChild(th(S.troika.protocol.seating(), "player-cell"));
    themeRow.appendChild(th(themeHead(bout, t, value, seatsAt.has(t)),
      troika.isShootoutTheme(state, t) ? "theme-block troika-shootout-head" : "theme-block",
      {colSpan: troika.THEME_QUESTIONS}));
  });
  themeRow.appendChild(th("Σ", "troika-total"));
  themeRow.appendChild(th(finishToggle(bout), "troika-finish-head"));
  thead.appendChild(themeRow);
  table.appendChild(thead);

  const body = document.createElement("tbody");
  const sides = state.sides.length;
  for (let side = 0; side < sides; side++) {
    const roster = boutRoster(bout.view, side);
    for (let chair = 0; chair < troika.CHAIRS; chair++) {
      const tr = document.createElement("tr");
      if (chair === 0) tr.appendChild(td(seatName(bout.view, side), "troika-team", {rowSpan: troika.CHAIRS}));
      state.values.forEach((_value, t) => {
        if (t > 0) tr.appendChild(td("", "gap"));
        if (seatsAt.has(t)) tr.appendChild(td(chairPicker(bout, side, t, chair, roster, editable), "player-cell"));
        for (let q = 0; q < troika.THEME_QUESTIONS; q++) tr.appendChild(markCell(bout.code, side, t, q, chair, state));
      });
      if (chair === 0) {
        tr.appendChild(td(String(troika.sideTotal(state, side)), "number troika-total",
          {rowSpan: troika.CHAIRS, dataset: {total: `${bout.code}-${side}`}}));
        tr.appendChild(td("", "troika-finish-gap", {rowSpan: troika.CHAIRS}));
      }
      body.appendChild(tr);
    }
    if (side < sides - 1) {
      const spacer = document.createElement("tr");
      spacer.className = "troika-side-gap";
      body.appendChild(spacer);
    }
  }
  table.appendChild(body);
  box.appendChild(table);
  return box;
}

// boutHead names the bout by its letter and title. On a host's open bout whose
// sides are level it carries the shootout button: one more theme worth 1,
// and then another until somebody answers first (regulations IV.2.4).
function boutHead(bout: BoutEntry): HTMLElement {
  const head = document.createElement("h3");
  head.className = "troika-bout-head u-row u-gap-sm u-align-center";
  const letter = boutLetters.get(bout.code);
  const title = document.createElement("span");
  title.textContent = [letter, bout.planned.title || bout.view.title || bout.code].filter(Boolean).join(". ");
  head.appendChild(title);
  const state = stateOf(bout.code);
  if (!viewer && !bout.view.finished && !state.written && troika.started(state) && troika.level(state)) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-xs";
    button.replaceChildren(icon("plus"), document.createTextNode(S.troika.shootout.add()));
    button.addEventListener("click", () => addShootoutTheme(bout));
    head.appendChild(button);
  }
  return head;
}

// addShootoutTheme appends a shootout theme to every side, seated as each
// side sat for the last theme.
function addShootoutTheme(bout: BoutEntry): void {
  const state = stateOf(bout.code);
  state.values.push(troika.SHOOTOUT_VALUE);
  state.shootout++;
  for (const side of state.sides) {
    const last = side.themes[side.themes.length - 1];
    side.themes.push({
      order: last ? last.order.slice() : new Array(troika.CHAIRS).fill(0),
      answers: Array.from({length: troika.THEME_QUESTIONS}, () => new Array<Mark>(troika.CHAIRS).fill("")),
    });
  }
  saveThemes(bout.code, state);
}

// dropShootoutTheme takes the last shootout theme back off, which the head
// offers only while nothing is entered in it.
function dropShootoutTheme(bout: BoutEntry): void {
  const state = stateOf(bout.code);
  if (state.shootout <= 0) return;
  state.values.pop();
  state.shootout--;
  for (const side of state.sides) side.themes.pop();
  saveThemes(bout.code, state);
}

// saveThemes writes a change to the bout's shape: the values, the count of
// shootout themes and every side's themes, each whole.
function saveThemes(code: string, state: TroikaState): void {
  patch(code, ["values"], state.values.slice());
  patch(code, ["shootout"], state.shootout);
  state.sides.forEach((side, s) => patch(code, ["sides", s, "themes"], side.themes.map((theme) => ({
    order: theme.order.slice(),
    answers: theme.answers.map((row) => row.slice()),
  }))));
  render();
}

function shootoutThemeEmpty(state: TroikaState, t: number): boolean {
  return state.sides.every((side) => (side.themes[t]?.answers || []).every((row) => row.every((mark) => mark === "")));
}

// The finished tick: a finished bout's sheet is read-only until the host
// unticks it — the server rejects edits to a finished bout.
function finishToggle(bout: BoutEntry): CellContent {
  const label = document.createElement("label");
  label.className = "finish-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(bout.view.finished);
  checkbox.disabled = viewer;
  checkbox.dataset.match = bout.code;
  checkbox.addEventListener("change", () => {
    void writer.send(matchScope(bout.code),
      {url: `${route.apiBase}/matches/${encodeURIComponent(bout.code)}/finish`, body: {finished: checkbox.checked}},
      {path: ["finished"], value: checkbox.checked});
  });
  const text = document.createElement("span");
  text.textContent = S.troika.bout.finished();
  label.append(checkbox, text);
  return label;
}

// seatOpen is the themes a host has opened a seating column at without yet
// changing the seating there; once it differs, the column stands on its own.
const seatOpen = new Map<string, Set<number>>();

function seatColumns(bout: BoutEntry): Set<number> {
  const state = stateOf(bout.code);
  const at = new Set(seatOpen.get(bout.code));
  at.add(0);
  for (let t = 1; t < state.values.length; t++) if (troika.turnedAt(state, t)) at.add(t);
  return at;
}

// The theme's head with, for a host, the seating control before the words: a
// ⇅ opens a seating column before this theme, and while one stands there
// the same spot is the × that undoes the change. No quiet toggling — a column
// is exactly where the seating changes, and removing one is an edit.
function themeHead(bout: BoutEntry, t: number, value: number, has: boolean): CellContent {
  const state = stateOf(bout.code);
  if (troika.isShootoutTheme(state, t)) {
    const number = t - (state.values.length - state.shootout) + 1;
    const label = S.troika.shootout.head(String(number));
    const last = t === state.values.length - 1;
    if (viewer || bout.view.finished || !last || !shootoutThemeEmpty(state, t)) return label;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-xs troika-seat-button";
    button.title = S.troika.shootout.drop();
    button.setAttribute("aria-label", button.title);
    button.replaceChildren(icon("x"));
    button.addEventListener("click", () => dropShootoutTheme(bout));
    return [button, label];
  }
  const label = S.troika.theme.head(String(t + 1), String(value));
  if (viewer || bout.view.finished || t === 0) return label;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "btn btn-xs troika-seat-button";
  if (has) {
    button.title = S.troika.theme.unseat(String(t + 1));
    button.replaceChildren(icon("x"));
    button.addEventListener("click", () => deleteTurn(bout, t));
  } else {
    button.title = S.troika.theme.seat(String(t + 1));
    button.replaceChildren(icon("arrow-up-down"));
    button.addEventListener("click", () => {
      const opened = seatOpen.get(bout.code) || new Set<number>();
      opened.add(t);
      seatOpen.set(bout.code, opened);
      render();
    });
  }
  button.setAttribute("aria-label", button.title);
  return [button, label];
}

function orderAt(state: TroikaState, side: number, t: number): number[] {
  const order: number[] = [];
  for (let c = 0; c < troika.CHAIRS; c++) order.push(troika.chairAt(state, side, t, c));
  return order;
}

// deleteTurn undoes the seating change before theme t: every theme from t that
// still holds the order set there goes back to the theme before's, so a later
// change is left exactly as it was played.
function deleteTurn(bout: BoutEntry, t: number): void {
  const state = stateOf(bout.code);
  for (let side = 0; side < state.sides.length; side++) {
    const prev = orderAt(state, side, t - 1);
    const cur = orderAt(state, side, t);
    if (sameArray(prev, cur)) continue;
    for (let tt = t; tt < state.values.length && sameArray(orderAt(state, side, tt), cur); tt++) {
      state.sides[side].themes[tt].order = prev.slice();
      patch(bout.code, ["sides", side, "themes", tt, "order"], prev);
    }
  }
  seatOpen.get(bout.code)?.delete(t);
  render();
}

// The chair cell names who is sitting there from the theme its column stands
// before. Seats are a fact per theme, so setting one rewrites that theme and
// every one after it, leaving the themes already played exactly as they were.
function chairPicker(bout: BoutEntry, side: number, from: number, chair: number,
  roster: Array<{id: number; name: string}>, editable: boolean): HTMLElement {
  const state = stateOf(bout.code);
  const select = document.createElement("select");
  select.className = "troika-chair-select";
  select.disabled = !editable;
  select.title = chair === troika.CHAIRS - 1 ? S.troika.chair.lead() : S.troika.chair.outrider(String(chair + 1));
  const current = troika.chairAt(state, side, from, chair);
  select.appendChild(option(0, "—"));
  for (const player of roster) {
    const node = option(player.id, player.name);
    node.selected = player.id === current;
    select.appendChild(node);
  }
  select.addEventListener("change", () => {
    const order: number[] = [];
    for (let c = 0; c < troika.CHAIRS; c++) {
      order.push(c === chair ? Number(select.value) || 0 : troika.chairAt(state, side, from, c));
    }
    troika.swapFrom(state, side, from, order);
    for (let t = from; t < state.values.length; t++) {
      patch(bout.code, ["sides", side, "themes", t, "order"], order);
    }
    render();
  });
  return select;
}

function markCell(code: string, side: number, theme: number, q: number, chair: number,
  state: TroikaState): HTMLElement {
  const cell = td("", "troika-cell answer-cell", {dataset: {match: code, side, theme, q, chair}});
  paintMark(cell, troika.markAt(state, side, theme, q, chair));
  return cell;
}

// A cell has three faces: paper, red for a wrong answer, green for a right
// one. The cursor reads the mark off the class.
function paintMark(cell: HTMLElement, mark: Mark): void {
  cell.classList.toggle("right", mark === "right");
  cell.classList.toggle("wrong", mark === "wrong");
}

// === the cursor ===

// The sheet the cursor walks is every bout of the tab stacked: a row is one
// chair of one side of one bout, a column one question. The columns are uniform
// within a bout and may differ between them, which is what the ragged geometry
// is for.
function sheetBouts(): BoutEntry[] {
  const tab = tabs().find((entry) => entry.key === activeTab);
  if (!tab || tab.kind !== "protocol") return [];
  return tabStages(tab).flatMap(stageBouts).filter((bout) => !stateOf(bout.code).written);
}

function sheetRows(): Array<{code: string; side: number; chair: number}> {
  const rows: Array<{code: string; side: number; chair: number}> = [];
  for (const bout of sheetBouts()) {
    for (let side = 0; side < stateOf(bout.code).sides.length; side++) {
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
  active: () => sheetBouts().length > 0,
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
    if (!state || matches.get(code)?.finished) continue;
    const mark = parseMark(edit.value);
    const row = state.sides[side]?.themes[theme]?.answers[q];
    if (!row || row[chair] === mark) continue;
    row[chair] = mark;
    paintMark(cell, mark);
    patch(code, ["sides", side, "themes", theme, "answers", q, chair], mark);
    touched.add(code);
  }
  for (const code of touched) refreshTotals(code);
}

// refreshTotals repaints the Σ a bout's cells feed rather than the sheet, so an
// edit does not move the cursor out from under the host.
function refreshTotals(code: string): void {
  const state = stateOf(code);
  for (let side = 0; side < state.sides.length; side++) {
    const node = root.querySelector<HTMLElement>(`[data-total="${cssEscape(`${code}-${side}`)}"]`);
    if (node) node.textContent = String(troika.sideTotal(state, side));
  }
}

// === the written qualifier ===

// The written bout is the qualifier: every troika at once, on paper. A row per
// troika, a column per question under its theme, and in each cell how many of
// the troika's three answers were right. Σ pays each of them at the theme's
// value; «3» and «2» count the questions answered three and two times right,
// which is what separates troikas level on Σ (regulations IV.2.3). The place is
// the Block's own, lot and all, as the server ranked it.
function buildWrittenBout(bout: BoutEntry): HTMLElement {
  const state = stateOf(bout.code);
  const box = document.createElement("section");
  box.className = "troika-bout";
  box.appendChild(boutHead(bout));

  const table = document.createElement("table");
  table.className = "match-table troika-sheet troika-written-sheet";
  table.classList.toggle("match-finished", Boolean(bout.view.finished));
  const thead = document.createElement("thead");
  const themeRow = document.createElement("tr");
  themeRow.appendChild(th(S.troika.protocol.team(), "troika-team-head"));
  state.values.forEach((value, t) => {
    if (t > 0) themeRow.appendChild(th("", "gap-head"));
    themeRow.appendChild(th(S.troika.theme.head(String(t + 1), String(value)), "theme-block",
      {colSpan: troika.THEME_QUESTIONS}));
  });
  themeRow.appendChild(th("Σ", "troika-total"));
  themeRow.appendChild(th(S.troika.written.threes(), "troika-total", {title: S.troika.written.threesHint()}));
  themeRow.appendChild(th(S.troika.written.twos(), "troika-total", {title: S.troika.written.twosHint()}));
  themeRow.appendChild(th(S.troika.written.place(), "troika-total"));
  themeRow.appendChild(th(finishToggle(bout), "troika-finish-head"));
  thead.appendChild(themeRow);
  table.appendChild(thead);

  const ranks = new Map<number, number>();
  for (const entry of festStages.get(bout.stage.code || "")?.standings || []) {
    if (entry.participantID && entry.rank) ranks.set(Number(entry.participantID), Number(entry.rank));
  }
  const body = document.createElement("tbody");
  state.sides.forEach((_side, side) => {
    const tr = document.createElement("tr");
    tr.appendChild(td(seatName(bout.view, side), "troika-team"));
    state.values.forEach((_value, t) => {
      if (t > 0) tr.appendChild(td("", "gap"));
      for (let q = 0; q < troika.THEME_QUESTIONS; q++) {
        const count = troika.countAt(state, side, t, q);
        tr.appendChild(td(count ? String(count) : "", "troika-count answer-cell",
          {dataset: {match: bout.code, side, theme: t, q}}));
      }
    });
    const totals = writtenTotals(state, side);
    tr.appendChild(td(String(totals.total), "number troika-total", {dataset: {total: `${bout.code}-${side}`}}));
    tr.appendChild(td(String(totals.threes), "number troika-total", {dataset: {threes: `${bout.code}-${side}`}}));
    tr.appendChild(td(String(totals.twos), "number troika-total", {dataset: {twos: `${bout.code}-${side}`}}));
    const id = Number(bout.view.participants?.[side]?.id || 0);
    tr.appendChild(td(ranks.has(id) ? String(ranks.get(id)) : "", "number troika-total"));
    tr.appendChild(td("", "troika-finish-gap"));
    body.appendChild(tr);
  });
  table.appendChild(body);
  box.appendChild(table);
  return box;
}

function writtenTotals(state: TroikaState, side: number): {total: number; threes: number; twos: number} {
  let threes = 0;
  let twos = 0;
  for (const row of state.sides[side]?.counts || []) {
    for (const count of row) {
      if (count === 3) threes++;
      if (count === 2) twos++;
    }
  }
  return {total: troika.sideTotal(state, side), threes, twos};
}

function writtenBout(): BoutEntry | null {
  const tab = tabs().find((entry) => entry.key === activeTab);
  if (!tab || tab.kind !== "protocol") return null;
  return tabStages(tab).flatMap(stageBouts).find((bout) => stateOf(bout.code).written) || null;
}

const writtenCursor = createSheetCursor({
  root,
  cellSelector: ".troika-count",
  values: "text",
  readonly: () => viewer || Boolean(writtenBout()?.view.finished),
  active: () => writtenBout() !== null,
  rows: () => {
    const bout = writtenBout();
    return bout ? stateOf(bout.code).sides.length : 0;
  },
  cols: () => {
    const bout = writtenBout();
    return bout ? stateOf(bout.code).values.length * troika.THEME_QUESTIONS : 0;
  },
  coordOf: (cell) => {
    const node = cell as HTMLElement;
    const side = Number(node.dataset.side);
    const theme = Number(node.dataset.theme);
    const q = Number(node.dataset.q);
    if (!Number.isInteger(side) || !Number.isInteger(theme) || !Number.isInteger(q)) return null;
    return {row: side, col: theme * troika.THEME_QUESTIONS + q};
  },
  cellAt: (coord: CellCoord) => {
    const bout = writtenBout();
    if (!bout) return null;
    const theme = Math.floor(coord.col / troika.THEME_QUESTIONS);
    const q = coord.col % troika.THEME_QUESTIONS;
    return root.querySelector<HTMLElement>(
      `.troika-count[data-match="${cssEscape(bout.code)}"][data-side="${cssEscape(String(coord.row))}"]` +
      `[data-theme="${cssEscape(String(theme))}"][data-q="${cssEscape(String(q))}"]`);
  },
  // A click steps 0 → 1 → 2 → 3 → 0; a digit is typed straight in.
  cycle: (cell: Element) => String(((Number(cell.textContent || 0) || 0) + 1) % (troika.CHAIRS + 1)),
  applyValues: applyCounts,
});

function applyCounts(edits: CellEdit[]): void {
  const touched = new Set<string>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const code = cell.dataset.match || "";
    const side = Number(cell.dataset.side);
    const theme = Number(cell.dataset.theme);
    const q = Number(cell.dataset.q);
    const state = states.get(code);
    if (!state || matches.get(code)?.finished) continue;
    const text = String(edit.value ?? "").trim();
    const count = text === "" ? 0 : Number(text);
    if (!Number.isInteger(count) || count < 0 || count > troika.CHAIRS) continue;
    const row = state.sides[side]?.counts[theme];
    if (!row || row[q] === count) continue;
    row[q] = count;
    cell.textContent = count ? String(count) : "";
    patch(code, ["sides", side, "counts", theme, q], count);
    touched.add(code);
  }
  for (const code of touched) {
    const state = stateOf(code);
    state.sides.forEach((_side, side) => {
      const totals = writtenTotals(state, side);
      for (const [key, value] of Object.entries(totals)) {
        const node = root.querySelector<HTMLElement>(`[data-${key}="${cssEscape(`${code}-${side}`)}"]`);
        if (node) node.textContent = String(value);
      }
    });
  }
}

// === the tabs ===

function buildProtocols(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "troika-protocol";
  const many = stages.length > 1;
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

// Troika's regulations rank on the rating ball first, then the head-to-head
// comparator, taken and the difference — so its table shows the ball in front of the
// canon columns the crosstable already draws.
function buildGroups(stages: SchemeStage[]): HTMLElement {
  const swiss = stages.filter((stage) => stageKind(stage) === "swiss");
  if (swiss.length) {
    const wrap = buildSwissTables(swiss);
    wrap.appendChild(buildGridOf(stages.filter((stage) => stageKind(stage) !== "swiss")));
    return wrap;
  }
  return buildCrosstables({
    className: "troika-groups",
    columns: [{label: S.troika.groups.rating(), metric: "rating"}, ...CANON_COLUMNS],
    groups: stages.filter((stage) => stageKind(stage) === "rr").map((stage) => ({
      title: groupLabel(stage as StageRef),
      entrants: (stage.config?.entrants || []).map(crossSlot),
      bouts: (stage.matches || []).flatMap((planned) => {
        const view = matches.get(planned.code || "");
        if (!view) return [];
        const state = stateOf(planned.code || "");
        return [{
          slots: [crossSlot(planned.slots?.[0]), crossSlot(planned.slots?.[1])],
          sides: [0, 1].map((side) => ({
            name: view.participants?.[side]?.name || "",
            id: Number(view.participants?.[side]?.id || 0),
            score: troika.sideTotal(state, side),
          })),
          finished: Boolean(view.finished),
          started: troika.started(state),
        }];
      }),
      standings: standingsByParticipant(festStages.get(stage.code || "")),
    })),
  });
}

// A Swiss Block's table: who went on (the wins reached) and who is out (the
// losses reached), by record, then by the seed the qualifier gave them — the
// order the play-off seats by.
function buildSwissTables(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "troika-protocol";
  for (const stage of stages) {
    const entries = festStages.get(stage.code || "")?.standings || [];
    const head = document.createElement("h2");
    head.className = "troika-stage-head";
    head.textContent = stage.title || stage.code || "";
    wrap.appendChild(head);
    const number = (value: unknown) => (typeof value === "number" && Number.isFinite(value) ? String(value) : "");
    wrap.appendChild(standingsTable({
      columns: [
        {label: S.troika.swiss.place(), kind: "place"},
        {label: S.troika.protocol.team(), kind: "name"},
        {label: S.troika.swiss.wins(), kind: "num"},
        {label: S.troika.swiss.losses(), kind: "num"},
        {label: S.troika.swiss.seed(), kind: "num"},
      ],
      rows: entries.map((entry) => [
        number(entry.rank),
        entry.name || "",
        number(entry.metrics?.wins),
        number(entry.metrics?.losses),
        number(entry.metrics?.seed),
      ]),
    }));
  }
  return wrap;
}

function buildStats(): HTMLElement {
  const bouts: TroikaBout[] = [];
  for (const stage of protocolStages()) {
    for (const entry of stageBouts(stage)) {
      const state = stateOf(entry.code);
      if (state.written) continue;
      bouts.push({
        state,
        sides: state.sides.map((_, side) => ({
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

// buildGridOf is the grid cut down to some stages: a Block's rounds, each a
// column of its bouts with who sat there, their Σ and place — the pairings
// at a glance, the marks left to the protocols tab.
function buildGridOf(only: SchemeStage[]): HTMLElement {
  const codes = new Set(only.map((stage) => stage.code || ""));
  const scheme = parseScheme(fest?.schemaJson);
  const schemeStages = (scheme?.stages || []).filter((stage) => codes.has(stage.code || ""));
  const live: FestGridStage[] = [];
  for (const stage of fest?.stages || []) {
    if (stage?.code && codes.has(stage.code)) live.push(festStages.get(stage.code) || stage);
  }
  return buildFestGrid({schemaJson: JSON.stringify({stages: schemeStages}), stages: live},
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
      setHashTab(key);
      render();
    });
  }
  const tab = tabs().find((entry) => entry.key === activeTab);
  const node = buildTab(tab);
  root.replaceChildren(node);
  // Groups and bouts wrap into the frame's width rather than pushing the page sideways.
  root.classList.toggle("fits-frame", tab?.kind !== "grid");
  root.classList.toggle("grid-host", Boolean(node.querySelector(".fest-grid")) || node.matches(".fest-grid"));
  scheduleNameOverflow();
  cursor.refresh();
  writtenCursor.refresh();
}

cursor.bind();
writtenCursor.bind();
live.connect();
fetchMatches().catch(() => indicator.fail());
