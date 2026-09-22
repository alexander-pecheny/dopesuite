// The Hamsa page (ADR-0001): rounds of four-seat bouts, each bout one wide
// sheet of five game rounds. Two rows per team — the player who sat for a
// theme over the five marks he answered — with the rounds named across the top
// and the team round's bet standing where a theme would. The grid, the block's
// own table and the statistics are the other tabs. Edits go per bout
// (PATCH /matches/{code}/state) and sync over match: scopes. A self-booting
// side-effect module bundled by pages/hamsa.ts.

import {cssEscape, option, questionNumberNode, td, th} from "./cells.js";
import type {CellContent, CellSpec} from "./cells.js";
import {festLetters, standingsTable} from "./standings.js";
import type {StageRef} from "./standings.js";
import {buildRosterView} from "./fest-roster.js";
import {createLiveEvents, createScopedWriter, gameEventsURL, scheduleStaticReload} from "./state-sync.js";
import {mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameInitLike} from "./game-page.js";
import {bindScrollEdges, createFloatingPopover, fitScrollFade, markNameOverflow, renderTabBar} from "./widgets.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {buildTwoRowScoreTable} from "./score-table.js";
import type {ScoreTableThemeRow} from "./score-table.js";
import {buildFestGrid, buildReseedStagePanel} from "./fest-grid.js";
import type {FestGridStage} from "./fest-grid.js";
import {gameTabs} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import {buildEKStatsTable} from "./ek-stats.js";
import * as hamsa from "./hamsa-protocol.js";
import type {HamsaState, Mark} from "./hamsa-protocol.js";
import {computeHamsaPlayerStats} from "./hamsa-stats.js";
import type {HamsaBout} from "./hamsa-stats.js";
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
}

interface SchemeStage {
  code?: string;
  title?: string;
  kind?: string;
  stage_type?: string;
  matches?: SchemeMatch[];
  config?: {shootout?: boolean};
  grain?: {block?: string; group?: string};
}

interface HamsaScheme {
  title?: string;
  stages?: SchemeStage[];
  seeding?: {source?: string};
}

interface MatchSeat {
  id?: number;
  name?: string;
  roster?: Array<{id?: number; name?: string}>;
}

interface HamsaMatchView {
  code?: string;
  title?: string;
  finished?: boolean;
  seq?: number;
  state?: unknown;
  participants?: MatchSeat[];
}

const root = document.getElementById("hamsaTable")!;
const tabsRoot = document.getElementById("hamsaTabs");
const statusNode = document.getElementById("status");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const route = parseGameRoute();
const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as HamsaScheme;
const fest = (init?.fest || null) as FestInfo | null;
const shell = mountGamePage({
  app: "hamsa",
  root,
  statusNode,
  breadcrumbsNode,
  festID: route.festID,
  gameID: route.gameID,
  viewer: Boolean(route.viewer),
  apiBase: route.apiBase,
  init,
  downloads: false,
  chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme.title || S.hamsa.title()}),
  cursorKinds: {
    answer: {selector: ".hamsa-cell", keys: ["match", "seat", "theme", "q", "shootout"]},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
});
const {viewer, staticMode, scopeGameID, indicator, viewerCounter} = shell;
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

const matches = new Map<string, HamsaMatchView>();
const states = new Map<string, HamsaState>();
const festStages = new Map<string, FestGridStage>();
for (const stage of fest?.stages || []) {
  if (stage?.code) festStages.set(stage.code, stage);
}
let rosterView: HTMLElement | null = null;
let resyncScheduled = false;

const boutLetters = festLetters(fest?.stages as StageRef[] | undefined);

function tabs(): GameTab[] {
  return gameTabs((scheme.stages || []) as StageRef[],
    {game: "hamsa", viewer, seeded: Boolean(scheme.seeding?.source)});
}

function tabStages(tab: GameTab): SchemeStage[] {
  return (scheme.stages || []).filter((stage) => tab.stages.includes(stage.code || ""));
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

fitScrollFade(root.closest(".sheet-frame"));
// Once a sheet is scrolled sideways, the frozen columns' right edge shades what
// slides under it — the same cue EK's stage sheet draws, from the same class.
const sheetScroll = bindScrollEdges(root.closest(".sheet-frame"), ({left}, frame) => {
  frame.classList.toggle("stage-scroll-left", left && tabs().find((tab) => tab.key === activeTab)?.kind === "protocol");
});

// === the document ===

function matchScope(code: string): string {
  return `match:${scopeGameID}:${code}`;
}

// seatsOf is who is sitting at a bout, in slot order. An empty seat keeps its
// place in the order, so a sheet drawn before the draw still has its rows.
function seatsOf(view: HamsaMatchView | undefined): number[] {
  return (view?.participants || []).map((seat) => Number(seat?.id || 0));
}

function adoptMatchView(view: HamsaMatchView | null | undefined): boolean {
  const code = view?.code;
  if (!view || !code) return false;
  const cached = matches.get(code);
  if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
  view = writer.overlay(matchScope(code), view) as HamsaMatchView;
  matches.set(code, view);
  states.set(code, hamsa.parseState(view.state, seatsOf(view)));
  return true;
}

function stateOf(code: string): HamsaState {
  return states.get(code) || hamsa.parseState(null, []);
}

async function fetchMatches(): Promise<void> {
  const response = await fetch(`${route.apiBase}/stages/matches`);
  if (!response.ok) throw new Error(`stages/matches ${response.status}`);
  const stages = await response.json() as Array<{code?: string; matches?: HamsaMatchView[]}>;
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
      const next = view.data as HamsaMatchView | null;
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
    adoptMatchView(response as HamsaMatchView);
    render();
  },
  indicator,
  onRejected: () => scheduleResync(),
});

function patch(code: string, path: Array<string | number>, value: unknown): void {
  writer.patch(matchScope(code), path, value);
}

// === the bout sheet ===

interface BoutEntry {
  code: string;
  view: HamsaMatchView;
  stage: SchemeStage;
}

function stageBouts(stage: SchemeStage): BoutEntry[] {
  const out: BoutEntry[] = [];
  for (const planned of stage.matches || []) {
    const code = planned.code || "";
    const view = matches.get(code);
    if (view) out.push({code, view, stage});
  }
  return out;
}

function seatName(view: HamsaMatchView, seat: number): string {
  return view.participants?.[seat]?.name || S.hamsa.protocol.seat(String(seat + 1));
}

// boutRoster is the people a team may field. The server sends each seat's
// roster with real player ids (store.SeatsPlayers), which is what a theme
// records — a name matched off the fest registry would not survive two players
// sharing one.
function boutRoster(view: HamsaMatchView, seat: number): Array<{id: number; name: string}> {
  return (view.participants?.[seat]?.roster || [])
    .filter((player) => player && typeof player.id === "number" && player.id > 0)
    .map((player) => ({id: Number(player.id), name: player.name || ""}));
}

// A block allows a shootout where its scheme says so; a bout that already has
// one keeps its column whatever the scheme says today.
function hasShootout(bout: BoutEntry): boolean {
  if (bout.stage.config?.shootout) return true;
  const state = stateOf(bout.code);
  return seatsOf(bout.view).some((id) => (hamsa.sectionOf(state, id)?.shootout.length || 0) > 0);
}

const ROUND_NAMES = [
  S.hamsa.round.light, S.hamsa.round.halfDark, S.hamsa.round.dark,
  S.hamsa.round.personal, S.hamsa.round.team,
];

function roundName(round: number): string {
  return (ROUND_NAMES[round] || ROUND_NAMES[ROUND_NAMES.length - 1])();
}

// roundHead names a game round over its themes: the multiplier is shown where
// the round pays more than the base values, since that is what a reader is
// checking against the sheet in front of them.
function roundHead(state: HamsaState, round: number): string {
  const base = hamsa.baseValues(state)[0] || 1;
  const value = state.rounds[round]?.values[0] || base;
  const multiplier = Math.round(value / base);
  const name = roundName(round);
  return multiplier > 1
    ? S.hamsa.round.headMultiplied(String(round + 1), name, String(multiplier))
    : S.hamsa.round.head(String(round + 1), name);
}

// themeGroups are the sheet's column groups in order: the themes of the first
// four game rounds, the team round's bet, and — where the block allows one —
// the shootout theme. `theme` is the index into the document; `bet` and
// `shootout` say which of the three kinds a group is.
interface ThemeGroup {
  kind: "theme" | "bet" | "shootout";
  theme: number;
  round: number;
  questions: number;
  values: number[];
}

function themeGroups(bout: BoutEntry): ThemeGroup[] {
  const state = stateOf(bout.code);
  const groups: ThemeGroup[] = [];
  const count = hamsa.themeCount(state);
  for (let t = 0; t < count; t++) {
    groups.push({kind: "theme", theme: t, round: hamsa.roundOfTheme(state, t), questions: hamsa.QUESTIONS, values: hamsa.themeValues(state, t)});
  }
  groups.push({kind: "bet", theme: 0, round: state.rounds.length, questions: 1, values: []});
  if (hasShootout(bout)) {
    groups.push({kind: "shootout", theme: 0, round: state.rounds.length + 1, questions: hamsa.QUESTIONS, values: hamsa.shootoutValues(state)});
  }
  return groups;
}

// A group's own head is narrow — it stands over the theme's score, one column
// wide — so it names the theme and nothing else; what the questions are worth
// is written across their own headers. The team round's score is a theme head
// too, numbered after the sixteen: its column holds a number like the others,
// and the bet column is headed by the word for a stake, where the host types it.
function groupLabelOf(group: ThemeGroup, themes: number): string {
  switch (group.kind) {
  case "bet":
    return S.hamsa.protocol.theme(String(themes + 1));
  case "shootout":
    return S.hamsa.round.shootout();
  default:
    return S.hamsa.protocol.theme(String(group.theme + 1));
  }
}

// columnGroup is the sheet's geometry, stated once. A table whose first row
// spans columns — and the round names do — cannot take its widths from that
// row (fixed layout divides a spanning width over the columns it covers), so
// the sheet declares them instead, and every width is a token.
function columnGroup(groups: ThemeGroup[]): HTMLElement {
  const cols = document.createElement("colgroup");
  const col = (className: string) => {
    const node = document.createElement("col");
    node.className = className;
    cols.appendChild(node);
  };
  col("hamsa-col-name");
  col("hamsa-col-total");
  col("hamsa-col-place");
  col("hamsa-col-place-gap");
  for (const group of groups) {
    for (let q = 0; q < group.questions; q++) col(group.kind === "bet" ? "hamsa-col-bet" : "hamsa-col-q");
    col("hamsa-col-score");
    col("hamsa-col-gap");
  }
  col("hamsa-col-total");
  for (let q = 0; q < hamsa.QUESTIONS; q++) col("hamsa-col-narrow");
  return cols;
}

// The trailing columns are EK's: Σ+, then one narrow count per question of a
// theme, hardest first. A Hamsa question is worth a different nominal in every
// round, so they are named by position — Q5 is the fifth question of a theme,
// counted over all sixteen.
function trailingHeaders(): CellSpec[] {
  const heads: CellSpec[] = [{content: S.hamsa.protocol.plus(), className: "number plus-head"}];
  for (let q = hamsa.QUESTIONS; q > 0; q--) {
    heads.push({content: S.hamsa.protocol.questionCount(String(q)), className: "number narrow"});
  }
  return heads;
}

// buildBout is one bout: the wide sheet, two rows per team.
function buildBout(bout: BoutEntry): HTMLElement {
  const state = stateOf(bout.code);
  const seats = seatsOf(bout.view);
  const groups = themeGroups(bout);
  const editable = !viewer && !bout.view.finished;
  const rows = hamsa.rows(state, seats);

  const box = document.createElement("section");
  box.className = "hamsa-bout u-col u-gap-sm";

  const table = buildTwoRowScoreTable({
    className: "match-table hamsa-sheet",
    attrs: {dataset: {match: bout.code}},
    nameHeader: boutHeader(bout),
    themes: groups.map((group) => ({
      label: groupLabelOf(group, hamsa.themeCount(state)),
      questionLabels: group.kind === "bet"
        ? [S.hamsa.protocol.bet()]
        : group.values.map((value) => questionNumberNode(value)),
      // The bet's head is a word over a column the width of a score, so it is
      // written a step smaller, the way a three-figure question number is.
      questionClassName: group.kind === "bet" ? "question-head hamsa-bet-head" : undefined,
    })),
    afterThemeHeaders: trailingHeaders(),
    rows: seats.map((id, seat) => ({
      nameCell: {content: seatName(bout.view, seat), className: "sticky sticky-name team-name"},
      totalCell: {content: rows[seat].total, className: "sticky sticky-total number total-cell", dataset: {total: `${bout.code}-${seat}`}},
      placeCell: {content: placeText(rows[seat].place), className: "sticky sticky-place number place-cell", dataset: {place: `${bout.code}-${seat}`}},
      themes: groups.map((group) => themeRow(bout, id, seat, group, editable)),
      afterThemeCells: [
        {content: rows[seat].plus, className: "number plus-cell", attrs: {rowSpan: 2}, dataset: {plus: `${bout.code}-${seat}`}},
        ...rows[seat].correct.slice().reverse().map((count, index) => ({
          content: count,
          className: "number narrow correct-count-cell",
          attrs: {rowSpan: 2},
          dataset: {count: `${bout.code}-${seat}-${hamsa.QUESTIONS - 1 - index}`},
        })),
      ],
    })),
  });
  table.classList.toggle("match-finished", Boolean(bout.view.finished));
  table.insertBefore(columnGroup(groups), table.firstChild);
  table.tHead?.insertBefore(roundHeaderRow(bout, groups), table.tHead.firstChild);

  box.appendChild(table);
  return box;
}

// roundHeaderRow stands over the theme headers and names the five game rounds,
// each spanning its own themes. The leading columns — the bout, Σ and the
// place — are the sheet's own and stay blank here.
function roundHeaderRow(bout: BoutEntry, groups: ThemeGroup[]): HTMLElement {
  const state = stateOf(bout.code);
  const row = document.createElement("tr");
  row.className = "hamsa-round-row";
  row.appendChild(th("", "sticky sticky-name hamsa-round-lead", {colSpan: 4}));
  let index = 0;
  while (index < groups.length) {
    const round = groups[index].round;
    let span = 0;
    let cells = 0;
    while (index + span < groups.length && groups[index + span].round === round) {
      // Each group costs its questions, its own label and a gap.
      cells += groups[index + span].questions + 2;
      span++;
    }
    const label = groups[index].kind === "shootout" ? S.hamsa.round.shootout() : roundHead(state, round);
    row.appendChild(th(label, "hamsa-round-head", {colSpan: cells}));
    index += span;
  }
  row.appendChild(th("", "hamsa-round-tail", {colSpan: 1 + hamsa.QUESTIONS}));
  return row;
}

function placeText(place: number): string {
  if (!place) return "";
  return Number.isInteger(place) ? String(place) : place.toFixed(1);
}

function themeRow(bout: BoutEntry, id: number, seat: number, group: ThemeGroup, editable: boolean): ScoreTableThemeRow {
  const state = stateOf(bout.code);
  if (group.kind === "bet") {
    return {
      // Not a player-cell: that one is sized to hold a name, and a bet is a
      // number in a column the width of a score.
      playerCell: {content: betInput(bout, id, seat, editable), className: "hamsa-bet-cell theme-block theme-block-top-left"},
      scoreCell: {content: hamsa.betScore(state, id), className: "number theme-score theme-block theme-block-score",
        attrs: {rowSpan: 2}, dataset: {bet: `${bout.code}-${seat}`}},
      answers: [markCell(bout, id, seat, 0, 0, "bet")],
    };
  }
  const shootout = group.kind === "shootout";
  const score = shootout ? hamsa.shootoutThemeScore(state, id, 0) : hamsa.themeScore(state, id, group.theme);
  return {
    playerCell: {
      content: playerSelect(bout, id, seat, group, editable),
      className: "player-cell theme-block theme-block-top-left",
      attrs: {colSpan: group.questions},
    },
    scoreCell: {content: score, className: "number theme-score theme-block theme-block-score",
      attrs: {rowSpan: 2}, dataset: {score: `${bout.code}-${seat}-${shootout ? "s" : "t"}${group.theme}`}},
    answers: group.values.map((_value, q) => markCell(bout, id, seat, group.theme, q, shootout ? "shootout" : "theme")),
  };
}

// playerSelect names who sat for this theme. The document keeps a player id,
// so the roster the server sent with the seat is what the options come from.
function playerSelect(bout: BoutEntry, id: number, seat: number, group: ThemeGroup, editable: boolean): HTMLElement {
  const state = stateOf(bout.code);
  const current = group.kind === "shootout"
    ? hamsa.sectionOf(state, id)?.shootout[0]?.player || 0
    : hamsa.playerAt(state, id, group.theme);
  const select = document.createElement("select");
  select.className = "hamsa-player-select";
  select.disabled = !editable || !id;
  select.appendChild(option(0, S.hamsa.draw.none()));
  for (const player of boutRoster(bout.view, seat)) {
    const node = option(player.id, player.name);
    node.selected = player.id === current;
    select.appendChild(node);
  }
  select.value = String(current);
  select.addEventListener("change", () => {
    const value = Number(select.value) || 0;
    const path = group.kind === "shootout"
      ? ["participants", String(id), "shootout", 0, "player"]
      : ["participants", String(id), "themes", group.theme, "player"];
    const section = hamsa.sectionOf(state, id);
    const theme = section && group.kind === "shootout"
      ? ensureShootout(bout.code, section, id)
      : section?.themes[group.theme];
    if (theme) theme.player = value;
    patch(bout.code, path, value);
    select.blur();
  });
  return select;
}

// betInput is the team round: a number the host types, as the captain
// wrote it. The regulations cap it at the team's balance after four rounds;
// dope records what it is told and does not police the limit.
function betInput(bout: BoutEntry, id: number, seat: number, editable: boolean): HTMLElement {
  const state = stateOf(bout.code);
  const input = document.createElement("input");
  input.type = "number";
  input.className = "hamsa-bet-input";
  input.min = "0";
  input.step = "1";
  input.disabled = !editable || !id;
  input.dataset.bet = `${bout.code}-${seat}`;
  const amount = hamsa.sectionOf(state, id)?.bet.amount;
  input.value = amount === null || amount === undefined ? "" : String(amount);
  input.title = S.hamsa.protocol.betTitle(seatName(bout.view, seat));
  input.setAttribute("aria-label", input.title);
  input.addEventListener("change", () => {
    const text = input.value.trim();
    const value = text === "" ? null : Math.trunc(Number(text));
    if (text !== "" && !Number.isFinite(value)) return;
    const bet = hamsa.sectionOf(state, id)?.bet;
    if (bet) bet.amount = value;
    patch(bout.code, ["participants", String(id), "bet", "amount"], value);
    refreshTotals(bout.code);
  });
  return input;
}

function markCell(bout: BoutEntry, id: number, seat: number, theme: number, q: number, kind: "theme" | "bet" | "shootout"): HTMLElement {
  const state = stateOf(bout.code);
  const mark = kind === "bet"
    ? hamsa.sectionOf(state, id)?.bet.answer || ""
    : kind === "shootout"
      ? hamsa.sectionOf(state, id)?.shootout[0]?.answers[q] || ""
      : hamsa.markAt(state, id, theme, q);
  const cell = td("", "hamsa-cell answer-cell theme-block", {dataset: {
    match: bout.code, seat, theme, q, shootout: kind === "shootout" ? "1" : "0", bet: kind === "bet" ? "1" : "0",
  }});
  if (q === 0) cell.classList.add("theme-block-bottom-left");
  if (!viewer) cell.tabIndex = bout.view.finished ? -1 : 0;
  cell.title = kind === "bet"
    ? S.hamsa.protocol.betTitle(seatName(bout.view, seat))
    : S.hamsa.protocol.answerTitle(seatName(bout.view, seat), String(theme + 1), String(hamsa.themeValues(state, theme)[q] || 0));
  paintMark(cell, mark);
  return cell;
}

// ensureShootout is the shootout theme a seat plays, written into the document
// the first time it is touched: a bout starts without one, because most blocks
// never play one at all.
function ensureShootout(code: string, section: hamsa.Participant, id: number): hamsa.Theme {
  const existing = section.shootout[0];
  if (existing) return existing;
  const row: hamsa.Theme = {player: 0, answers: ["", "", "", "", ""]};
  section.shootout[0] = row;
  patch(code, ["participants", String(id), "shootout", 0], {player: 0, answers: ["", "", "", "", ""]});
  return row;
}

function paintMark(cell: HTMLElement, mark: Mark): void {
  cell.classList.toggle("right", mark === "right");
  cell.classList.toggle("wrong", mark === "wrong");
}

// boutHeader is the sheet's name column head, and it is EK's: the bout's name
// beside the finished tick, in the one cell that stays frozen while the themes
// scroll under it. A title above the sheet left that cell empty, and the
// question headers showed through it.
// A finished bout is read-only until the host unticks it.
function boutHeader(bout: BoutEntry): CellContent {
  const node = th("", "sticky sticky-name battle");
  const layout = document.createElement("span");
  layout.className = "battle-layout";
  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = [boutLetters.get(bout.code), bout.view.title || bout.code].filter(Boolean).join(". ");
  layout.appendChild(title);

  // A spectator gets the name alone, as on EK: the tick is the host's control.
  if (viewer) {
    node.appendChild(layout);
    return node;
  }

  const label = document.createElement("label");
  label.className = "finish-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(bout.view.finished);
  checkbox.dataset.match = bout.code;
  checkbox.addEventListener("change", () => {
    void writer.send(matchScope(bout.code),
      {url: `${route.apiBase}/matches/${encodeURIComponent(bout.code)}/finish`, body: {finished: checkbox.checked}},
      {path: ["finished"], value: checkbox.checked});
  });
  // The tick alone, with the word in the tooltip: EK's, because the name column
  // is 90px on a phone and the bout's name has to fit beside it.
  label.title = S.hamsa.protocol.finished();
  label.setAttribute("aria-label", S.hamsa.protocol.finished());
  label.append(checkbox);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

// === the cursor ===

function sheetBouts(): BoutEntry[] {
  const tab = tabs().find((entry) => entry.key === activeTab);
  if (!tab || tab.kind !== "protocol") return [];
  return tabStages(tab).flatMap(stageBouts);
}

interface SheetRow {
  code: string;
  seat: number;
  id: number;
}

function sheetRows(): SheetRow[] {
  const out: SheetRow[] = [];
  for (const bout of sheetBouts()) {
    seatsOf(bout.view).forEach((id, seat) => out.push({code: bout.code, seat, id}));
  }
  return out;
}

// The sheet the cursor walks is every bout of the tab stacked: a row is one
// team of one bout, a column one question — the themes, then the bet's single
// answer, then the shootout's.
function columnsOf(bout: BoutEntry): Array<{theme: number; q: number; kind: "theme" | "bet" | "shootout"}> {
  const out: Array<{theme: number; q: number; kind: "theme" | "bet" | "shootout"}> = [];
  for (const group of themeGroups(bout)) {
    for (let q = 0; q < group.questions; q++) out.push({theme: group.theme, q, kind: group.kind});
  }
  return out;
}

function boutOf(code: string): BoutEntry | undefined {
  return sheetBouts().find((bout) => bout.code === code);
}

const cursor = createSheetCursor({
  root,
  cellSelector: ".hamsa-cell",
  values: "marks",
  readonly: () => viewer,
  active: () => tabs().find((tab) => tab.key === activeTab)?.kind === "protocol",
  rows: () => sheetRows().length,
  cols: (row: number) => {
    const at = sheetRows()[row];
    const bout = at && boutOf(at.code);
    return bout ? columnsOf(bout).length : 0;
  },
  coordOf: (cell) => {
    const node = cell as HTMLElement;
    const code = node.dataset.match || "";
    const seat = Number(node.dataset.seat);
    const bout = boutOf(code);
    if (!bout) return null;
    const theme = Number(node.dataset.theme);
    const q = Number(node.dataset.q);
    const kind = node.dataset.bet === "1" ? "bet" : node.dataset.shootout === "1" ? "shootout" : "theme";
    const row = sheetRows().findIndex((entry) => entry.code === code && entry.seat === seat);
    const col = columnsOf(bout).findIndex((column) => column.kind === kind && column.theme === theme && column.q === q);
    if (row < 0 || col < 0) return null;
    return {row, col};
  },
  cellAt: (coord: CellCoord) => {
    const at = sheetRows()[coord.row];
    const bout = at && boutOf(at.code);
    if (!bout) return null;
    const column = columnsOf(bout)[coord.col];
    if (!column) return null;
    return root.querySelector<HTMLElement>(
      `.hamsa-cell[data-match="${cssEscape(at.code)}"][data-seat="${cssEscape(String(at.seat))}"]` +
      `[data-theme="${cssEscape(String(column.theme))}"][data-q="${cssEscape(String(column.q))}"]` +
      `[data-shootout="${column.kind === "shootout" ? "1" : "0"}"][data-bet="${column.kind === "bet" ? "1" : "0"}"]`);
  },
  applyValues: applyMarks,
});

function applyMarks(edits: CellEdit[]): void {
  const touched = new Set<string>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const code = cell.dataset.match || "";
    const seat = Number(cell.dataset.seat);
    const theme = Number(cell.dataset.theme);
    const q = Number(cell.dataset.q);
    const state = states.get(code);
    const view = matches.get(code);
    if (!state || view?.finished) continue;
    const id = seatsOf(view)[seat];
    if (!id) continue;
    const section = hamsa.sectionOf(state, id);
    if (!section) continue;
    const mark = parseMark(edit.value) as Mark;
    if (cell.dataset.bet === "1") {
      if (section.bet.answer === mark) continue;
      section.bet.answer = mark;
      patch(code, ["participants", String(id), "bet", "answer"], mark);
    } else if (cell.dataset.shootout === "1") {
      // A block that allows a shootout draws its column from the first mark
      // on, so the theme is written whole the first time somebody uses it.
      const row = ensureShootout(code, section, id);
      if (row.answers[q] === mark) continue;
      row.answers[q] = mark;
      patch(code, ["participants", String(id), "shootout", 0, "answers", q], mark);
    } else {
      const row = section.themes[theme];
      if (!row || row.answers[q] === mark) continue;
      row.answers[q] = mark;
      patch(code, ["participants", String(id), "themes", theme, "answers", q], mark);
    }
    paintMark(cell, mark);
    touched.add(code);
  }
  for (const code of touched) refreshTotals(code);
}

// refreshTotals repaints what an edit feeds rather than the sheet, so the
// cursor does not move out from under the host.
function refreshTotals(code: string): void {
  const view = matches.get(code);
  const state = stateOf(code);
  const seats = seatsOf(view);
  const bout = boutOf(code);
  hamsa.rows(state, seats).forEach((row, seat) => {
    setCell(`[data-total="${cssEscape(`${code}-${seat}`)}"]`, String(row.total));
    setCell(`[data-plus="${cssEscape(`${code}-${seat}`)}"]`, String(row.plus));
    row.correct.forEach((count, q) => setCell(`[data-count="${cssEscape(`${code}-${seat}-${q}`)}"]`, String(count)));
    setCell(`[data-place="${cssEscape(`${code}-${seat}`)}"]`, placeText(row.place));
    setCell(`[data-bet="${cssEscape(`${code}-${seat}`)}"]:not(input)`, String(hamsa.betScore(state, row.id)));
    if (!bout) return;
    for (const group of themeGroups(bout)) {
      if (group.kind === "bet") continue;
      const shootout = group.kind === "shootout";
      const score = shootout ? hamsa.shootoutThemeScore(state, row.id, 0) : hamsa.themeScore(state, row.id, group.theme);
      setCell(`[data-score="${cssEscape(`${code}-${seat}-${shootout ? "s" : "t"}${group.theme}`)}"]`, String(score));
    }
  });
}

function setCell(selector: string, text: string): void {
  const node = root.querySelector<HTMLElement>(selector);
  if (node) node.textContent = text;
}

// === the tabs ===

function buildProtocols(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "u-col u-gap-lg";
  for (const stage of stages) {
    const bouts = stageBouts(stage);
    if (!bouts.length) {
      const empty = document.createElement("p");
      empty.className = "empty";
      empty.textContent = S.hamsa.protocol.unseated();
      wrap.appendChild(empty);
      continue;
    }
    for (const bout of bouts) wrap.appendChild(buildBout(bout));
  }
  return wrap;
}

// The block's own table: the sum of places over its rounds, with the columns
// the scheme ranked by beside it.
const TABLE_COLUMNS: Array<{metric: string; label: () => string}> = [
  {metric: "place_sum", label: S.hamsa.table.placeSum},
  {metric: "total", label: S.hamsa.table.total},
  {metric: "first", label: S.hamsa.table.first},
  {metric: "bouts", label: S.hamsa.table.bouts},
  {metric: "seed", label: S.hamsa.table.seed},
];

function buildBlockTable(stages: SchemeStage[]): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  const entries = stages.flatMap((stage) => festStages.get(stage.code || "")?.standings || []);
  if (!entries.length) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.hamsa.table.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  const shown = TABLE_COLUMNS.filter((column) => entries.some((entry) => entry.metrics?.[column.metric] !== undefined));
  wrapper.appendChild(standingsTable({
    className: "hamsa-table",
    columns: [
      {label: S.hamsa.table.place(), kind: "place"},
      {label: S.hamsa.table.team(), kind: "name"},
      ...shown.map((column) => ({label: column.label(), kind: "num" as const})),
    ],
    rows: entries.map((entry, index) => [
      entry.rank || index + 1,
      entry.name || "",
      ...shown.map((column) => metricText(entry.metrics?.[column.metric])),
    ]),
  }));
  return wrapper;
}

function metricText(value: unknown): string {
  const number = Number(value);
  if (!Number.isFinite(number)) return "";
  return Number.isInteger(number) ? String(number) : number.toFixed(1);
}

function buildReseeds(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "u-col u-gap-lg";
  for (const stage of stages) {
    const live = festStages.get(stage.code || "");
    wrap.appendChild(buildReseedStagePanel(live, {
      editable: !viewer,
      canCalculate: Boolean(live?.reseedReady),
      letters: boutLetters,
      onCalculate: () => void calculateReseed(stage.code || ""),
    }));
  }
  return wrap;
}

async function calculateReseed(code: string): Promise<void> {
  const response = await fetch(`${route.apiBase}/stages/${encodeURIComponent(code)}/reseed`, {
    method: "POST", headers: {"Content-Type": "application/json"},
  });
  if (!response.ok) {
    indicator.fail();
    return;
  }
  const view = await response.json() as FestInfo;
  for (const stage of view.stages || []) if (stage?.code) festStages.set(stage.code, stage);
  await fetchMatches();
}

function buildStats(): HTMLElement {
  const bouts: HamsaBout[] = [];
  let values: number[] = [];
  for (const stage of scheme.stages || []) {
    for (const entry of stageBouts(stage)) {
      const state = stateOf(entry.code);
      if (!values.length) values = hamsa.baseValues(state);
      bouts.push({
        state,
        seats: seatsOf(entry.view).map((id, seat) => ({
          id,
          team: seatName(entry.view, seat),
          players: new Map(boutRoster(entry.view, seat).map((player) => [player.id, player.name])),
        })),
      });
    }
  }
  return buildEKStatsTable(computeHamsaPlayerStats(bouts), values.length ? values : undefined);
}

function buildGrid(): HTMLElement {
  const stages: FestGridStage[] = [];
  for (const stage of fest?.stages || []) {
    if (stage?.code) stages.push(festStages.get(stage.code) || stage);
  }
  return buildFestGrid({schemaJson: fest?.schemaJson, stages}, {
    stageHeaderLink: false,
    matchTitleLink: false,
    letters: boutLetters,
    editable: !viewer,
    onDraw: (slot, participant) => void applyDraw(slot, participant),
  });
}

// applyDraw seats a Draw Slot. The server holds the choice to the seat's own
// candidates, so a refusal is a refusal and the page simply reloads what it
// answered.
async function applyDraw(slot: string, participant: number): Promise<void> {
  const response = await fetch(`${route.apiBase}/draw`, {
    method: "PUT",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({slot, participant}),
  });
  if (!response.ok) {
    indicator.fail();
    return;
  }
  const view = await response.json() as FestInfo;
  for (const stage of view.stages || []) if (stage?.code) festStages.set(stage.code, stage);
  await fetchMatches();
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "roster":
    return (rosterView ||= buildRosterView(route.festID));
  case "stats":
    return buildStats();
  case "block":
    return buildBlockTable(tabStages(tab));
  case "reseed":
    return buildReseeds(tabStages(tab));
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
  const tab = tabs().find((entry) => entry.key === activeTab);
  const node = buildTab(tab);
  root.replaceChildren(node);
  root.classList.toggle("fits-frame", tab?.kind !== "grid" && tab?.kind !== "protocol");
  root.classList.toggle("grid-host", Boolean(node.querySelector(".fest-grid")) || node.matches(".fest-grid"));
  scheduleNameOverflow();
  sheetScroll.refresh();
  cursor.refresh();
}

cursor.bind();
live.connect();
fetchMatches().catch(() => indicator.fail());
