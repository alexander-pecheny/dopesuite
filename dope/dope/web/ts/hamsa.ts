// The Hamsa page (ADR-0001): rounds of four-seat bouts, each bout one wide
// sheet of five game rounds. Two rows per team — the player who sat for a
// theme over the five marks he answered — with the rounds named across the top
// and the team round's bet standing where a theme would. The grid, the block's
// own table and the statistics are the other tabs. Edits go per bout
// (PATCH /matches/{code}/state) and sync over match: scopes, through the bout
// page (bout-page.ts). A self-booting side-effect module bundled by
// pages/hamsa.ts.

import {option, questionNumberNode, td, th} from "./cells.js";
import type {CellContent, CellSpec} from "./cells.js";
import {letteredTitle, standingsTable} from "./standings.js";
import {buildGameRosterView} from "./fest-roster.js";
import {nameCell} from "./name-cell.js";
import {seatPicker} from "./seat-picker.js";
import {mountBoutPage, tabStages, stageBouts, seatRoster} from "./bout-page.js";
import type {BoutPage, BoutView, BoutEntry as BoutEntryOf} from "./bout-page.js";
import type {GameInitLike} from "./game-page.js";
import {figureData, paintFigures, paintMark, stackedSheet} from "./stacked-sheet.js";
import {buildTwoRowScoreTable, scoreSheetPins} from "./score-table.js";
import type {ScoreTableThemeRow} from "./score-table.js";
import type {FestGridStage} from "./fest-grid.js";
import type {GameTab} from "./game-tabs.js";
import {buildEKStatsTable} from "./ek-stats.js";
import * as hamsa from "./hamsa-protocol.js";
import type {HamsaState} from "./hamsa-protocol.js";
import {computeHamsaPlayerStats} from "./hamsa-stats.js";
import type {HamsaBout} from "./hamsa-stats.js";
import S from "./i18nstrings.js";

// The sheet's leading columns, which the round header row leaves blank.
const LEADING_COLS = 4;

// A bout sheet pins EK's block, with a total column wide enough for thousands.
const BOUT_PINS = scoreSheetPins({total: "var(--hamsa-total-col)"});

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

interface HamsaMatchView extends BoutView {
  participants?: MatchSeat[];
}

const root = document.getElementById("hamsaTable")!;

const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as HamsaScheme;
const fest = (init?.fest || null) as FestInfo | null;
// The stacked sheet a Round's tab draws: a row is a team of a bout, a column
// one question — the themes, then the bet's single answer, then the shootout's.
const answers = stackedSheet({
  selector: ".hamsa-cell",
  fields: hamsa.SHEET_FIELDS,
  bouts: () => sheetBouts(),
  codeOf: (bout: BoutEntry) => bout.code,
  rowsOf: (bout: BoutEntry) => seatsOf(bout.view).map((_id, seat) => ({seat})),
  columnsOf: (bout: BoutEntry) => hamsa.sheetColumns(stateOf(bout.code), hasShootout(bout)),
  marks: {
    markOf: (cell) => {
      const view = page.view(cell.match);
      const id = seatsOf(view)[cell.seat];
      const section = id ? hamsa.sectionOf(stateOf(cell.match), id) : undefined;
      if (!view || view.finished || !section) return null;
      if (cell.cellKind === "theme" && !section.themes[cell.theme]) return null;
      return hamsa.cellMark(stateOf(cell.match), id, cell);
    },
    setMark: (cell, mark) => {
      const id = seatsOf(page.view(cell.match))[cell.seat];
      const section = hamsa.sectionOf(stateOf(cell.match), id);
      if (!section) return;
      if (cell.cellKind === "bet") section.bet.answer = mark;
      // A block that allows a shootout draws its column from the first mark
      // on, so the theme is written whole the first time somebody uses it.
      else if (cell.cellKind === "shootout") ensureShootout(cell.match, section, id).answers[cell.q] = mark;
      else section.themes[cell.theme].answers[cell.q] = mark;
    },
    pathOf: (cell) => hamsa.markPath(seatsOf(page.view(cell.match))[cell.seat], cell),
    patch: (code, path, value) => page.patch(code, path, value),
    onWritten: (codes) => codes.forEach((code) => page.refresh(code)),
  },
});

const page: BoutPage<HamsaMatchView, HamsaState> = mountBoutPage({
  app: "hamsa",
  root,
  tabsRoot: document.getElementById("hamsaTabs"),
  init,
  scheme,
  fest,
  title: () => S.hamsa.title(),
  parse: (view) => hamsa.parseState(view.state, seatsOf(view)),
  blank: () => hamsa.parseState(null, []),
  buildTab,
  buildRoster: (): HTMLElement => buildGameRosterView(page.route.apiBase || "", {editable: !page.viewer}),
  fitsFrame: (tab) => tab?.kind !== "grid" && tab?.kind !== "protocol",
  shape,
  repaintCells,
  cursorKinds: {
    answer: answers.cursorKind,
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
  cursors: () => [cursor],
});
const {viewer} = page;

const boutLetters = page.letters;

// === the document ===

// seatsOf is who is sitting at a bout, in slot order. An empty seat keeps its
// place in the order, so a sheet drawn before the draw still has its rows.
function seatsOf(view: HamsaMatchView | undefined): number[] {
  return (view?.participants || []).map((seat) => Number(seat?.id || 0));
}

function stateOf(code: string): HamsaState {
  return page.stateOf(code);
}

function patch(code: string, path: Array<string | number>, value: unknown): void {
  page.patch(code, path, value);
}

// === the bout sheet ===

type BoutEntry = BoutEntryOf<HamsaMatchView, SchemeStage, SchemeMatch>;

function seatName(view: HamsaMatchView, seat: number): string {
  return view.participants?.[seat]?.name || S.hamsa.protocol.seat(String(seat + 1));
}

// seatNameCell is a team's name on the bout sheet, the EK two-row cell: a long
// name stays on one line and fades at the column's edge, whole in the popover,
// rather than wrapping the row taller than its neighbours.
function seatNameCell(name: string): HTMLElement {
  const cell = nameCell(name, {className: "team-name ek-team-cell", layout: true});
  (cell as HTMLTableCellElement).rowSpan = 2;
  return cell;
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

// themeGroups are the sheet's column groups in order (hamsa.sheetGroups), each
// with the game round it stands under and what its questions are worth.
interface ThemeGroup extends hamsa.SheetGroup {
  round: number;
  values: number[];
}

function themeGroups(bout: BoutEntry): ThemeGroup[] {
  const state = stateOf(bout.code);
  return hamsa.sheetGroups(state, hasShootout(bout)).map((group) => {
    switch (group.kind) {
    case "bet":
      return {...group, round: state.rounds.length, values: []};
    case "shootout":
      return {...group, round: state.rounds.length + 1, values: hamsa.shootoutValues(state)};
    default:
      return {...group, round: hamsa.roundOfTheme(state, group.theme), values: hamsa.themeValues(state, group.theme)};
    }
  });
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
    return S.hamsa.protocol.shootoutTheme();
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

  const box = page.boutBox(bout.code, "hamsa-bout u-col u-gap-sm");

  const table = buildTwoRowScoreTable({
    className: "match-table hamsa-sheet",
    pins: BOUT_PINS,
    headRowsAbove: [{row: roundHeaderRow(bout, groups), height: "var(--head-row)"}],
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
      nameCell: seatNameCell(seatName(bout.view, seat)),
      totalCell: {content: rows[seat].total, className: "number total-cell", dataset: figureData("total", {seat})},
      placeCell: {content: placeContent(bout, seat, rows[seat]), className: "number place-cell", dataset: figureData("place", {seat})},
      themes: groups.map((group) => themeRow(bout, id, seat, group, editable)),
      afterThemeCells: [
        {content: rows[seat].plus, className: "number plus-cell", attrs: {rowSpan: 2}, dataset: figureData("plus", {seat})},
        ...rows[seat].correct.slice().reverse().map((count, index) => ({
          content: count,
          className: "number narrow correct-count-cell",
          attrs: {rowSpan: 2},
          dataset: figureData("count", {seat, q: hamsa.QUESTIONS - 1 - index}),
        })),
      ],
    })),
  });
  table.classList.toggle("match-finished", Boolean(bout.view.finished));
  table.insertBefore(columnGroup(groups), table.firstChild);

  box.appendChild(table);
  return box;
}

// roundHeaderRow stands over the theme headers and names the five game rounds,
// each spanning its own themes. The leading columns — the bout, Σ and the
// place — are the sheet's own and stay blank here.
function roundHeaderRow(bout: BoutEntry, groups: ThemeGroup[]): HTMLTableRowElement {
  const state = stateOf(bout.code);
  const row = document.createElement("tr");
  row.className = "hamsa-round-row";
  row.appendChild(BOUT_PINS.markSpan(th("", "hamsa-round-lead", {colSpan: LEADING_COLS})));
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

// placeContent is the place cell: the place, and for a host, under a place
// the team shares, the lot that says which of them goes forward first. The
// shared place stays what the sum of places counts; the lot only seats the
// next game. It stays open on a finished bout, because a tie is only known once
// the bout is over, and the server takes it there.
function placeContent(bout: BoutEntry, seat: number, row: hamsa.Row): CellContent {
  const text = placeText(row.place);
  const id = seatsOf(bout.view)[seat];
  if (viewer || !id || row.tie < 2) return text;
  const select = document.createElement("select");
  select.className = "hamsa-lot-select";
  select.addEventListener("change", () => {
    const value = Number(select.value) || null;
    const section = hamsa.sectionOf(stateOf(bout.code), id);
    if (section) section.lot = value;
    patch(bout.code, ["participants", String(id), "lot"], value);
    page.refresh(bout.code);
  });
  const label = document.createElement("span");
  const wrap = document.createElement("span");
  wrap.className = "u-col u-align-center";
  wrap.append(label, select);
  fillPlace(wrap, bout, seat, row);
  return wrap;
}

// fillPlace brings a place with its lot in line with the row: the place, the
// lots the tie allows and the one chosen. It keeps the select, so a host who
// is on it stays on it when another host's mark repaints the bout.
function fillPlace(wrap: HTMLElement, bout: BoutEntry, seat: number, row: hamsa.Row): void {
  const label = wrap.firstElementChild;
  const select = wrap.querySelector<HTMLSelectElement>(".hamsa-lot-select");
  if (!label || !select) return;
  const text = placeText(row.place);
  if (label.textContent !== text) label.textContent = text;
  const missing = Boolean(bout.view.finished) && row.lot === null;
  select.classList.toggle("needs-lot", missing);
  select.title = missing ? S.hamsa.protocol.lotMissing(seatName(bout.view, seat)) : S.hamsa.protocol.lotTitle(seatName(bout.view, seat));
  select.setAttribute("aria-label", select.title);
  if (select.options.length !== row.tie + 1) {
    select.replaceChildren(option(0, S.hamsa.draw.none()));
    for (let lot = 1; lot <= row.tie; lot++) select.appendChild(option(lot, lot));
  }
  const value = String(row.lot ?? 0);
  if (select.value !== value) select.value = value;
}

function themeRow(bout: BoutEntry, id: number, seat: number, group: ThemeGroup, editable: boolean): ScoreTableThemeRow {
  const state = stateOf(bout.code);
  if (group.kind === "bet") {
    return {
      // Not a player-cell: that one is sized to hold a name, and a bet is a
      // number in a column the width of a score.
      playerCell: {content: betInput(bout, id, seat, editable), className: "hamsa-bet-cell theme-block theme-block-top-left"},
      scoreCell: {content: hamsa.betScore(state, id), className: "number theme-score theme-block theme-block-score",
        attrs: {rowSpan: 2}, dataset: figureData("bet", {seat})},
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
      attrs: {rowSpan: 2}, dataset: figureData("score", {seat, cellKind: group.kind, theme: group.theme})},
    answers: group.values.map((_value, q) => markCell(bout, id, seat, group.theme, q, shootout ? "shootout" : "theme")),
  };
}

// playerSelect names who sat for this theme. The document keeps a player id,
// 0 for nobody, so the roster the server sent with the seat is what the
// choices come from, and the picker is given the ids as text.
function playerSelect(bout: BoutEntry, id: number, seat: number, group: ThemeGroup, editable: boolean): HTMLElement {
  const state = stateOf(bout.code);
  const current = group.kind === "shootout"
    ? hamsa.sectionOf(state, id)?.shootout[0]?.player || 0
    : hamsa.playerAt(state, id, group.theme);
  const roster = seatRoster(bout.view, seat);
  return seatPicker({
    roster: roster.map((player) => ({id: String(player.id), name: player.name})),
    seated: roster.some((player) => player.id === current) ? [String(current)] : [],
    disabled: !editable || !id,
    nobody: S.seat.nobody(),
    onChange: (seated) => {
      const value = Number(seated[0]) || 0;
      const path = group.kind === "shootout"
        ? ["participants", String(id), "shootout", 0, "player"]
        : ["participants", String(id), "themes", group.theme, "player"];
      const section = hamsa.sectionOf(stateOf(bout.code), id);
      const theme = section && group.kind === "shootout"
        ? ensureShootout(bout.code, section, id)
        : section?.themes[group.theme];
      if (theme) theme.player = value;
      patch(bout.code, path, value);
      page.refresh(bout.code);
    },
  }).element;
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
  input.dataset.seat = String(seat);
  const amount = hamsa.sectionOf(state, id)?.bet.amount;
  input.value = amount === null || amount === undefined ? "" : String(amount);
  input.title = S.hamsa.protocol.betTitle(seatName(bout.view, seat));
  input.setAttribute("aria-label", input.title);
  input.addEventListener("change", () => {
    const text = input.value.trim();
    const value = text === "" ? null : Math.trunc(Number(text));
    if (text !== "" && !Number.isFinite(value)) return;
    // The state the box was drawn from may have been read again since: a
    // repaint keeps the box, so the handler reads the state it writes now.
    const bet = hamsa.sectionOf(stateOf(bout.code), id)?.bet;
    if (bet) bet.amount = value;
    patch(bout.code, ["participants", String(id), "bet", "amount"], value);
    page.refresh(bout.code);
  });
  return input;
}

function markCell(bout: BoutEntry, id: number, seat: number, theme: number, q: number, kind: hamsa.CellKind): HTMLElement {
  const state = stateOf(bout.code);
  const mark = hamsa.cellMark(state, id, {cellKind: kind, theme, q});
  const cell = td("", "hamsa-cell answer-cell theme-block", {dataset: answers.dataset({match: bout.code, seat, cellKind: kind, theme, q})});
  if (q === 0) cell.classList.add("theme-block-bottom-left");
  if (!viewer) cell.tabIndex = bout.view.finished ? -1 : 0;
  cell.title = kind === "bet"
    ? S.hamsa.protocol.betTitle(seatName(bout.view, seat))
    : kind === "shootout"
      ? S.hamsa.protocol.shootoutAnswerTitle(seatName(bout.view, seat), String(hamsa.shootoutValues(state)[q] || 0))
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

// boutHeader is the sheet's name column head, and it is EK's: the bout's name
// beside the finished tick, in the one cell that stays frozen while the themes
// scroll under it. A title above the sheet left that cell empty, and the
// question headers showed through it.
// A finished bout is read-only until the host unticks it.
function boutHeader(bout: BoutEntry): CellContent {
  const node = th("", "battle");
  const layout = document.createElement("span");
  layout.className = "battle-layout";
  const title = document.createElement("span");
  title.className = "battle-title";
  // Named as the grid names it — "Match A", the letter in place of the number —
  // so a host reading the sheet and a player reading the grid say the same.
  title.textContent = letteredTitle(bout.view.title || bout.code, boutLetters.get(bout.code));
  layout.appendChild(title);
  // When the bout starts, once the host gave it a time, with its venue.
  layout.append(...page.whereWhen(bout.code, {title: title.textContent || "", venueAlways: false, className: "battle-venue"}));

  // A spectator gets the name alone, as on EK: the tick is the host's control.
  if (viewer) {
    node.appendChild(layout);
    return node;
  }

  // The tick alone, with the word in the tooltip: EK's, because the name column
  // is 90px on a phone and the bout's name has to fit beside it.
  layout.appendChild(page.finishToggle(bout.code, {title: S.hamsa.protocol.finished()}));
  node.appendChild(layout);
  return node;
}

// === the cursor ===

function sheetBouts(): BoutEntry[] {
  const tab = page.tab();
  if (!tab || tab.kind !== "protocol") return [];
  return tabStages(scheme.stages, tab).flatMap((stage) => stageBouts(page, stage));
}

const cursor = answers.cursor(root, {
  values: "marks",
  readonly: () => viewer,
  active: () => page.tab()?.kind === "protocol",
});

// === the repaint contract ===

// shape is everything of a bout its box draws except the marks and the
// numbers they feed: who sits where, the rounds, the themes and their values,
// the shootout's column, the head. The bet, the places and the lots are
// repainted in place.
function shape(code: string): string {
  const view = page.view(code);
  const bout = answers.boutOf(code);
  if (!view || !bout) return "";
  const state = stateOf(code);
  return JSON.stringify({
    finished: Boolean(view.finished), title: view.title || "",
    venue: view.venue ? [view.venue.number, view.venue.title] : null, startsAt: view.startsAt || "",
    seats: (view.participants || []).map((seat) => [seat?.id, seat?.name, (seat?.roster || []).map((player) => player.id)]),
    rounds: state.rounds.map((round) => round.values),
    groups: themeGroups(bout).map((group) => [group.kind, group.theme, group.questions, group.values]),
    players: seatsOf(view).map((id) => {
      const section = hamsa.sectionOf(state, id);
      return section ? [section.themes.map((theme) => theme.player), section.shootout[0]?.player || 0] : null;
    }),
  });
}

// repaintCells brings a drawn bout's marks, its figures, its places with their
// lots and its bets in line with the state, where they stand.
function repaintCells(code: string): void {
  const box = page.boxOf(code);
  const bout = answers.boutOf(code);
  const state = stateOf(code);
  const seats = seatsOf(page.view(code));
  const rows = hamsa.rows(state, seats);
  answers.paint(box, code, (cell) => hamsa.cellMark(state, seats[cell.seat], cell));
  paintFigures(box, (figure, at, cell) => {
    const seat = Number(at.seat);
    const row = rows[seat];
    if (!row) return undefined;
    switch (figure) {
    case "total": return row.total;
    case "plus": return row.plus;
    case "count": return row.correct[Number(at.q)];
    case "bet": return hamsa.betScore(state, row.id);
    case "place": {
      if (!bout) return undefined;
      // A lot select already there is filled in place, so it keeps focus.
      // The seat's id is part of the shape, so the select's handler still
      // writes the right seat.
      const drawn = cell.querySelector<HTMLElement>(".hamsa-lot-select")?.parentElement;
      if (drawn && !viewer && row.tie >= 2) {
        fillPlace(drawn, bout, seat, row);
        return undefined;
      }
      const content = placeContent(bout, seat, row);
      return content instanceof Node ? content : String(content ?? "");
    }
    case "score": return at.cellKind === "shootout"
      ? hamsa.shootoutThemeScore(state, row.id, 0)
      : hamsa.themeScore(state, row.id, Number(at.theme));
    default: return undefined;
    }
  });
  for (const input of box?.querySelectorAll<HTMLInputElement>(".hamsa-bet-input") || []) {
    if (input === document.activeElement) continue;
    const amount = hamsa.sectionOf(state, seats[Number(input.dataset.seat)])?.bet.amount;
    input.value = amount === null || amount === undefined ? "" : String(amount);
  }
}

// === the tabs ===

function buildProtocols(stages: SchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "u-col u-gap-lg";
  for (const stage of stages) {
    const bouts = stageBouts(page, stage);
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
  const entries = stages.flatMap((stage) => page.festStage(stage.code || "")?.standings || []);
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

function buildStats(): HTMLElement {
  const bouts: HamsaBout[] = [];
  let values: number[] = [];
  for (const stage of scheme.stages || []) {
    for (const entry of stageBouts(page, stage)) {
      const state = stateOf(entry.code);
      if (!values.length) values = hamsa.baseValues(state);
      bouts.push({
        state,
        seats: seatsOf(entry.view).map((id, seat) => ({
          id,
          team: seatName(entry.view, seat),
          players: new Map(seatRoster(entry.view, seat).map((player) => [player.id, player.name])),
        })),
      });
    }
  }
  return buildEKStatsTable(computeHamsaPlayerStats(bouts), values.length ? values : undefined);
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "stats":
    return buildStats();
  case "block":
    return buildBlockTable(tabStages(scheme.stages, tab));
  default:
    // A Round's bouts; the module draws the grid and the reseed.
    return buildProtocols(tabStages(scheme.stages, tab));
  }
}

page.start();
