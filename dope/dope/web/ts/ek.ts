// The EK page (ADR-0001), which Erudit-Sextet and individual SI play too:
// rounds of four-seat bouts, each bout a sheet of twelve themes of five
// questions. Two rows per team, the players who sat for a theme over the five
// marks (one row per player in individual SI, who seats himself). A tab holds
// a stage's bouts, or a Block round's across its groups, stacked into one
// sheet the cursor walks; the grid, a Block's group table, the reseed, the
// statistics and the rosters are the other tabs. Edits go per bout to the
// bout's document (PATCH /matches/{code}/state) and sync over match: scopes,
// through the bout page (bout-page.ts), the same as Brain's, Hamsa's and
// Troika's. A self-booting side-effect module bundled by pages/ek.ts.

import {cssEscape, formatPlace, td, th} from "./cells.js";
import type {CellContent} from "./cells.js";
import {buildGroupStandingsView, letteredTitle, standingsTable} from "./standings.js";
import {buildGameRosterView} from "./fest-roster.js";
import {nameCell} from "./name-cell.js";
import {seatPicker} from "./seat-picker.js";
import {seatingLabel} from "./ek-seating.js";
import {boutAnchorID, groupAnchorID, mountBoutPage, tabStages, stageBouts, seatRoster} from "./bout-page.js";
import type {BoutPage, BoutView, BoutEntry as BoutEntryOf} from "./bout-page.js";
import type {GameInitLike} from "./game-page.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {buildFlatScoreTable, buildTwoRowScoreTable, seatingText} from "./score-table.js";
import type {ScoreTableThemeRow} from "./score-table.js";
import {reseedMetricHeader, reseedMetricValue} from "./fest-grid.js";
import type {FestGridStage, SortRule} from "./fest-grid.js";
import {computeGroupBlockRounds} from "./group-stats.js";
import {canonicalKey, groupLabel} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import type {StageRef} from "./standings.js";
import {buildEKStatsTable, buildIndividualStatsTable, computeEKPlayerStats, computeIndividualPlayerStats} from "./ek-stats.js";
import * as ek from "./ek-protocol.js";
import type {EKState, Mark, ThemeKind} from "./ek-protocol.js";
import S from "./i18nstrings.js";

// The question values the trailing counts run over, hardest first.
const QUESTION_VALUES = [10, 20, 30, 40, 50];

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}
const pageWindow = window as Window & PageGlobals;

interface FestInfo {
  title?: string;
  gameName?: string;
  gameType?: string;
  schemaJson?: unknown;
  stages?: FestGridStage[];
  [key: string]: unknown;
}

interface SchemeMatch {
  code?: string;
  title?: string;
  round?: number;
  group?: string;
}

interface SchemeStage {
  code?: string;
  title?: string;
  kind?: string;
  stage_type?: string;
  matches?: SchemeMatch[];
  members?: string[];
  config?: {rules?: {bout?: {points?: string}}; entrants?: Array<{label?: string}>};
  grain?: {block?: string; group?: string};
}

interface EKScheme {
  title?: string;
  stages?: SchemeStage[];
}

interface ThemeView {
  players?: string[];
  answers?: string[];
}

interface MatchSeat {
  id?: number;
  name?: string;
  place?: number;
  roster?: Array<{id?: number; name?: string}>;
  themes?: ThemeView[];
}

interface EKMatchView extends BoutView {
  questionValues?: number[];
  // How many of a team sit on one theme: one in EK, up to three in ES.
  players?: number;
  participants?: MatchSeat[];
}

const root = document.getElementById("ekTable")!;
const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as EKScheme;
const fest = (init?.fest || null) as FestInfo | null;
const gameType = String(fest?.gameType || init?.gameType || "ek");

// Individual SI plays its bracket here: a seat is one player, who has no
// per-theme seating, so a seat takes one row where a team's takes two.
const individual = gameType === "si";
const app: "ek" | "es" | "si" = individual ? "si" : gameType === "es" ? "es" : "ek";

const page: BoutPage<EKMatchView, EKState> = mountBoutPage({
  app,
  root,
  tabsRoot: document.getElementById("ekTabs"),
  init,
  scheme,
  fest,
  title: () => (app === "es" ? S.games.es.short() : S.ek.title()),
  parse: (view) => ek.parseState(view.state, seatsOf(view), themeCountOf(view)),
  blank: () => ek.parseState(null, [], 0),
  buildTab,
  buildRoster: (): HTMLElement => buildGameRosterView(page.route.apiBase || "", {editable: !page.viewer}),
  fitsFrame: (tab) => !["grid", "stage", "round"].includes(tab?.kind || "grid"),
  boutSelector: ".ek-bout",
  cursorKinds: {
    answer: {selector: ".ek-cell", keys: ["match", "seat", "theme", "q", "shootout"]},
    place: {selector: ".place-input", keys: ["match", "seat"]},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
  cursors: () => [cursor],
  // An old stage code or a Block's old @-spelling still opens its tab.
  canonical: canonicalKey,
});
const {viewer} = page;
const boutLetters = page.letters;

// === the document ===

// seatsOf is who is sitting at a bout, in slot order; an empty seat keeps its
// place, so a sheet drawn before the draw still has its rows.
function seatsOf(view: EKMatchView | undefined): number[] {
  return (view?.participants || []).map((seat) => Number(seat?.id || 0));
}

// themeCountOf is how many themes the bout plays, as the server laid them out.
function themeCountOf(view: EKMatchView | undefined): number {
  return Math.max(0, ...(view?.participants || []).map((seat) => seat?.themes?.length || 0));
}

function valuesOf(view: EKMatchView | undefined): number[] {
  return view?.questionValues?.length ? view.questionValues : QUESTION_VALUES;
}

function stateOf(code: string): EKState {
  return page.stateOf(code);
}

function sectionOf(code: string, seat: number): ek.EKSection | undefined {
  return stateOf(code).sections.get(seatsOf(page.view(code))[seat]);
}

// shootoutCount is how many shootout themes the bout has: every seat holds
// the same number, since a theme is added and dropped for all of them at once.
function shootoutCount(code: string): number {
  return Math.max(0, ...[...stateOf(code).sections.values()].map((section) => section.shootoutThemes.length));
}

function seatCap(view: EKMatchView | undefined): number {
  return Math.max(1, view?.players || 1);
}

const rowSpan = individual ? 1 : 2;

// === the bout sheet ===

type BoutEntry = BoutEntryOf<EKMatchView, SchemeStage, SchemeMatch>;

interface ThemeColumn {
  kind: ThemeKind;
  theme: number;
}

function themeColumns(code: string): ThemeColumn[] {
  const out: ThemeColumn[] = [];
  for (let theme = 0; theme < themeCountOf(page.view(code)); theme++) out.push({kind: "themes", theme});
  for (let theme = 0; theme < shootoutCount(code); theme++) out.push({kind: "shootoutThemes", theme});
  return out;
}

function seatName(view: EKMatchView, seat: number): string {
  return view.participants?.[seat]?.name || S.ek.seat.fallback(String(seat + 1));
}

// boutTitle is the bout's name as the grid says it, with its group where a
// round tab gathers the bouts of several.
function boutTitle(bout: BoutEntry): string {
  const title = letteredTitle(bout.view.title || bout.code, boutLetters.get(bout.code));
  return bout.planned.group ? `${bout.planned.group}. ${title}` : title;
}

function buildBout(bout: BoutEntry): HTMLElement {
  const {code, view} = bout;
  const state = stateOf(code);
  const seats = seatsOf(view);
  const values = valuesOf(view);
  const columns = themeColumns(code);
  const editable = !viewer && !view.finished;
  const shootouts = shootoutCount(code);

  const box = document.createElement("section");
  box.className = "ek-bout";
  box.id = boutAnchorID(code);

  const build = individual ? buildFlatScoreTable : buildTwoRowScoreTable;
  const table = build({
    className: `match-table compact-score-table ek-stage-table${viewer ? " readonly-table" : ""}${individual ? " individual-blank" : ""}`,
    attrs: {dataset: {match: code}},
    rowMarkerColumn: false,
    nameHeader: boutHeader(bout),
    placeColumn: true,
    themes: columns.map((column) => ({
      label: column.kind === "themes" ? S.ek.theme.column(String(column.theme + 1)) : S.ek.shootout.column(String(column.theme + 1)),
      questionLabels: values,
      questionClassName: column.kind === "shootoutThemes" ? "question-head shootout-head" : undefined,
      labelClassName: column.kind === "shootoutThemes" ? "theme-head shootout-head" : undefined,
    })),
    afterThemeHeaders: trailingHeaders(code, shootouts),
    rows: seats.map((id, seat) => {
      const score = ek.scoreSection(state.sections.get(id), values);
      return {
        nameCell: seatNameCell(seatName(view, seat)),
        totalCell: {content: score.total, className: "number total-cell", attrs: {rowSpan}, dataset: {total: `${code}-${seat}`}},
        placeCell: placeCell(bout, seat),
        themes: columns.map((column) => themeRow(bout, seat, column, editable)),
        afterThemeCells: trailingCells(code, seat, score, shootouts),
      };
    }),
    gapRowClassName: "team-gap-row",
  });
  table.classList.toggle("match-finished", Boolean(view.finished));
  box.appendChild(table);
  return box;
}

// seatNameCell is a seat's name, pinned at the sheet's left edge: on a sheet
// of stacked bouts a long name wraps onto a second line and steps its font
// down before it fades (name-cell.ts shrink).
function seatNameCell(name: string): HTMLElement {
  const cell = nameCell(name, {className: "team-name ek-team-cell", layout: true, shrink: true});
  (cell as HTMLTableCellElement).rowSpan = rowSpan;
  return cell;
}

function trailingHeaders(code: string, shootouts: number): CellContent[] {
  const heads: CellContent[] = viewer ? [] : [shootoutControls(code, shootouts)];
  if (shootouts > 0) heads.push(th(S.ek.shootout.letter(), "number"));
  heads.push(th("Σ+", "number"));
  for (const value of [...QUESTION_VALUES].reverse()) heads.push(th(String(value), "number narrow"));
  return heads;
}

function trailingCells(code: string, seat: number, score: ek.SectionScore, shootouts: number): CellContent[] {
  const cells: CellContent[] = viewer ? [] : [td("", "shootout-controls-cell", {rowSpan})];
  if (shootouts > 0) cells.push(td(score.shootout, "number tiebreak-cell", {rowSpan, dataset: {shootoutTotal: `${code}-${seat}`}}));
  cells.push(td(score.plus, "number plus-cell", {rowSpan, dataset: {plus: `${code}-${seat}`}}));
  for (let q = ek.QUESTIONS - 1; q >= 0; q--) {
    cells.push(td(score.correct[q], "number narrow correct-count-cell", {rowSpan, dataset: {count: `${code}-${seat}-${q}`}}));
  }
  return cells;
}

// placeCell is the seat's place: the server's, or the one the host pinned. A
// host types a place to pin it, and empties the box to hand the place back to
// the scorer. A pin is the host's say over a finished bout, so it stays open
// on one.
function placeCell(bout: BoutEntry, seat: number): HTMLElement {
  const id = seatsOf(bout.view)[seat];
  const pin = sectionOf(bout.code, seat)?.pin ?? null;
  const place = pin ?? bout.view.participants?.[seat]?.place ?? 0;
  if (viewer) return td(formatPlace(place), "number place-cell", {rowSpan, dataset: {place: `${bout.code}-${seat}`}});
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "decimal";
  input.className = "place-input";
  input.value = formatPlace(place);
  input.disabled = !id;
  input.dataset.match = bout.code;
  input.dataset.seat = String(seat);
  input.title = S.ek.place.title(seatName(bout.view, seat));
  input.setAttribute("aria-label", input.title);
  const commit = () => {
    const text = input.value.trim().replace(",", ".");
    const next = text === "" ? null : Number(text);
    if (next !== null && (!Number.isFinite(next) || next < 0)) {
      input.value = formatPlace(place);
      return;
    }
    if ((next || null) === pin) return;
    const section = sectionOf(bout.code, seat);
    if (section) section.pin = next || null;
    page.patch(bout.code, ek.pinPath(id), next || null);
  };
  input.addEventListener("change", commit);
  input.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    input.blur();
  });
  const cell = td("", "number place-cell", {rowSpan, dataset: {place: `${bout.code}-${seat}`}});
  cell.appendChild(input);
  return cell;
}

function themeRow(bout: BoutEntry, seat: number, column: ThemeColumn, editable: boolean): ScoreTableThemeRow {
  const {code, view} = bout;
  const section = sectionOf(code, seat);
  const theme = column.kind === "themes" ? section?.themes[column.theme] : section?.shootoutThemes[column.theme];
  const values = valuesOf(view);
  const scoreCell = {
    content: theme ? ek.themeScore(theme, values) : 0,
    className: "number theme-score theme-block theme-block-score",
    attrs: {rowSpan},
    dataset: {score: `${code}-${seat}-${column.kind === "themes" ? "t" : "s"}${column.theme}`},
  };
  const answers = values.map((_value, q) => markCell(bout, seat, column, q, theme?.answers[q] || "", editable));
  // A player seats himself in individual SI: his row has no player cell.
  if (individual) return {scoreCell, answers};
  if (viewer) return {playerCell: readonlyPlayerCell(bout, seat, theme?.players || []), scoreCell, answers};
  return {
    playerCell: {
      content: playerCell(bout, seat, column, theme?.players || [], editable),
      className: "player-cell theme-block theme-block-top-left",
      attrs: {colSpan: values.length},
    },
    scoreCell,
    answers,
  };
}

// readonlyPlayerCell is the spectator's seating: the names, not a picker, in
// the cell itself; a long one fades and the popover lists them one per line.
function readonlyPlayerCell(bout: BoutEntry, seat: number, players: number[]): HTMLElement {
  const names = rosterNames(bout, seat);
  const seated = players.map((id) => names.get(id) || "").filter(Boolean);
  const cell = nameCell(seatingText({players: seated}, {players: seatCap(bout.view)}), {
    className: "readonly-player theme-block theme-block-top-left",
    popoverText: seated.join("\n"),
  });
  (cell as HTMLTableCellElement).colSpan = valuesOf(bout.view).length;
  return cell;
}

function rosterNames(bout: BoutEntry, seat: number): Map<number, string> {
  return new Map(seatRoster(bout.view, seat).map((player) => [player.id, player.name]));
}

// playerCell names who sat for the theme. The document keeps player ids; the
// roster the server sent with the seat is where their names and the host's
// choices come from. Erudit-Sextet seats up to three on a theme.
function playerCell(bout: BoutEntry, seat: number, column: ThemeColumn, players: number[], editable: boolean): HTMLElement {
  const names = rosterNames(bout, seat);
  const id = seatsOf(bout.view)[seat];
  const cap = seatCap(bout.view);
  return seatPicker({
    roster: seatRoster(bout.view, seat).map((player) => ({id: String(player.id), name: player.name})),
    seated: players.filter((player) => names.has(player)).map(String),
    cap,
    disabled: !editable || !id,
    title: cap > 1 ? S.ek.seats.label() : undefined,
    nobody: S.seat.nobody(),
    line: seatingLabel,
    dataset: {match: bout.code, seat: String(seat), theme: String(column.theme), shootout: column.kind === "themes" ? "0" : "1"},
    onChange: (chosen) => {
      const ids = chosen.map(Number).filter((player) => player > 0);
      const theme = column.kind === "themes" ? sectionOf(bout.code, seat)?.themes[column.theme] : sectionOf(bout.code, seat)?.shootoutThemes[column.theme];
      if (theme) theme.players = ids;
      page.patch(bout.code, ek.playersPath(id, column.kind, column.theme), ids);
    },
  }).element;
}

function markCell(bout: BoutEntry, seat: number, column: ThemeColumn, q: number, mark: Mark, editable: boolean): HTMLElement {
  const {code, view} = bout;
  const cell = td("", "ek-cell answer-cell theme-block", {dataset: {
    match: code, seat, theme: column.theme, q, shootout: column.kind === "themes" ? "0" : "1",
  }});
  if (q === 0) cell.classList.add("theme-block-bottom-left");
  if (!viewer) cell.tabIndex = editable ? 0 : -1;
  const label = column.kind === "themes" ? S.ek.theme.column(String(column.theme + 1)) : S.ek.shootout.column(String(column.theme + 1));
  cell.title = S.ek.answer.title(seatName(view, seat), label, String(valuesOf(view)[q] || 0));
  paintMark(cell, mark);
  return cell;
}

function paintMark(cell: HTMLElement, mark: Mark): void {
  cell.classList.toggle("right", mark === "right");
  cell.classList.toggle("wrong", mark === "wrong");
}

// boutHeader is the sheet's name column head: the bout's lettered name, where
// and when it is played, and for a host the finished tick.
function boutHeader(bout: BoutEntry): HTMLElement {
  const node = th("", "battle");
  const layout = document.createElement("span");
  layout.className = "battle-layout";
  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = boutTitle(bout);
  layout.appendChild(title);
  layout.append(...page.whereWhen(bout.code, {title: title.textContent || "", venueAlways: !viewer, className: "battle-venue"}));
  if (!viewer) layout.appendChild(page.finishToggle(bout.code, {title: S.ek.bout.finished()}));
  node.appendChild(layout);
  return node;
}

// shootoutControls adds a shootout theme to every seat of the bout at once, or
// drops the last one, after the host confirms.
function shootoutControls(code: string, shootouts: number): HTMLElement {
  const node = document.createElement("th");
  node.className = "shootout-controls-head";
  const finished = Boolean(page.view(code)?.finished);
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn btn-xs shootout-add-button";
  add.textContent = S.ek.shootout.addLabel();
  add.title = S.ek.shootout.add();
  add.setAttribute("aria-label", add.title);
  add.disabled = finished;
  add.addEventListener("click", () => sendShootoutTheme(code, shootouts, false));
  node.appendChild(add);
  if (shootouts > 0) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "btn btn-xs theme-delete-button";
    remove.textContent = S.ek.shootout.removeLabel();
    remove.title = S.ek.shootout.remove();
    remove.setAttribute("aria-label", remove.title);
    remove.disabled = finished;
    remove.addEventListener("click", (event) => {
      event.preventDefault();
      if (!window.confirm(S.ek.shootout.removeConfirm())) return;
      sendShootoutTheme(code, shootouts - 1, true);
    });
    node.appendChild(remove);
  }
  return node;
}

function sendShootoutTheme(code: string, theme: number, remove: boolean): void {
  const ops = seatsOf(page.view(code)).filter(Boolean).map((id) => {
    const path = ["participants", String(id), "shootoutThemes", theme];
    return remove ? {op: "remove", path} : {path, value: {answers: ["", "", "", "", ""]}};
  });
  if (!ops.length) return;
  const url = `${page.route.apiBase}/matches/${encodeURIComponent(code)}/state`;
  void page.send(code, {url, method: "PATCH", body: {ops}});
}

// === the cursor ===

function tabBouts(tab: GameTab | undefined): BoutEntry[] {
  if (tab?.kind === "round") {
    // A Block round gathers one round of every group, in the order the
    // groups were written.
    const out: BoutEntry[] = [];
    for (const planned of (tab.stage?.matches || []) as SchemeMatch[]) {
      const code = planned.code || "";
      const view = page.view(code);
      const stage = (scheme.stages || []).find((entry) => (entry.matches || []).some((match) => match.code === code));
      if (view && stage) out.push({code, view, planned, stage});
    }
    return out;
  }
  if (tab?.kind === "stage") return tabStages(scheme.stages, tab).flatMap((stage) => stageBouts(page, stage));
  return [];
}

function sheetBouts(): BoutEntry[] {
  return tabBouts(page.tab());
}

interface SheetRow {
  code: string;
  seat: number;
}

function sheetRows(): SheetRow[] {
  return sheetBouts().flatMap((bout) => seatsOf(bout.view).map((_id, seat) => ({code: bout.code, seat})));
}

function columnsOf(code: string): Array<{kind: ThemeKind; theme: number; q: number}> {
  return themeColumns(code).flatMap((column) => Array.from({length: ek.QUESTIONS}, (_, q) => ({...column, q})));
}

const cursor = createSheetCursor({
  root,
  cellSelector: ".ek-cell",
  values: "marks",
  readonly: () => viewer,
  active: () => ["stage", "round"].includes(page.tab()?.kind || ""),
  rows: () => sheetRows().length,
  cols: (row: number) => {
    const at = sheetRows()[row];
    return at ? columnsOf(at.code).length : 0;
  },
  coordOf: (cell) => {
    const node = cell as HTMLElement;
    const code = node.dataset.match || "";
    const seat = Number(node.dataset.seat);
    const kind: ThemeKind = node.dataset.shootout === "1" ? "shootoutThemes" : "themes";
    const theme = Number(node.dataset.theme);
    const q = Number(node.dataset.q);
    const row = sheetRows().findIndex((entry) => entry.code === code && entry.seat === seat);
    const col = columnsOf(code).findIndex((column) => column.kind === kind && column.theme === theme && column.q === q);
    return row < 0 || col < 0 ? null : {row, col};
  },
  cellAt: (coord: CellCoord) => {
    const at = sheetRows()[coord.row];
    const column = at && columnsOf(at.code)[coord.col];
    if (!column) return null;
    return root.querySelector<HTMLElement>(
      `.ek-cell[data-match="${cssEscape(at.code)}"][data-seat="${cssEscape(String(at.seat))}"]` +
      `[data-theme="${cssEscape(String(column.theme))}"][data-q="${cssEscape(String(column.q))}"]` +
      `[data-shootout="${column.kind === "themes" ? "0" : "1"}"]`);
  },
  applyValues: applyMarks,
});

function applyMarks(edits: CellEdit[]): void {
  const touched = new Set<string>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const code = cell.dataset.match || "";
    const seat = Number(cell.dataset.seat);
    const kind: ThemeKind = cell.dataset.shootout === "1" ? "shootoutThemes" : "themes";
    const theme = Number(cell.dataset.theme);
    const q = Number(cell.dataset.q);
    const view = page.view(code);
    const id = seatsOf(view)[seat];
    if (!view || view.finished || !id) continue;
    const row = kind === "themes" ? sectionOf(code, seat)?.themes[theme] : sectionOf(code, seat)?.shootoutThemes[theme];
    if (!row) continue;
    const mark = parseMark(edit.value) as Mark;
    if (row.answers[q] === mark) continue;
    row.answers[q] = mark;
    page.patch(code, ek.answerPath(id, kind, theme, q), mark);
    paintMark(cell, mark);
    touched.add(code);
  }
  for (const code of touched) refreshTotals(code);
}

// refreshTotals repaints what an edit feeds rather than the sheet, so the
// cursor does not move out from under the host.
function refreshTotals(code: string): void {
  const view = page.view(code);
  const values = valuesOf(view);
  const state = stateOf(code);
  seatsOf(view).forEach((id, seat) => {
    const section = state.sections.get(id);
    const score = ek.scoreSection(section, values);
    setCell(`[data-total="${cssEscape(`${code}-${seat}`)}"]`, String(score.total));
    setCell(`[data-plus="${cssEscape(`${code}-${seat}`)}"]`, String(score.plus));
    setCell(`[data-shootout-total="${cssEscape(`${code}-${seat}`)}"]`, String(score.shootout));
    score.correct.forEach((count, q) => setCell(`[data-count="${cssEscape(`${code}-${seat}-${q}`)}"]`, String(count)));
    for (const column of themeColumns(code)) {
      const theme = column.kind === "themes" ? section?.themes[column.theme] : section?.shootoutThemes[column.theme];
      const key = `${code}-${seat}-${column.kind === "themes" ? "t" : "s"}${column.theme}`;
      setCell(`[data-score="${cssEscape(key)}"]`, String(theme ? ek.themeScore(theme, values) : 0));
    }
  });
}

function setCell(selector: string, text: string): void {
  const node = root.querySelector<HTMLElement>(selector);
  if (node) node.textContent = text;
}

// === the tabs ===

function buildBouts(tab: GameTab | undefined): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "u-col u-gap-lg";
  const bouts = tabBouts(tab);
  // A Block's own table with no bouts of its own — Erudit-Sextet's group stage
  // ranks both its games together — is drawn as that table.
  if (!bouts.length) {
    const live = page.festStage(tab?.stages[0] || "");
    if (live?.standings?.length) return buildRankedStageTable(live);
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.ek.stage.empty();
    wrap.appendChild(empty);
    return wrap;
  }
  for (const bout of bouts) wrap.appendChild(buildBout(bout));
  return wrap;
}

// buildGroupTable is a Block's groups on one tab: a player, his points, and
// the split by block round. The order and the points are the server's
// standings (head-to-head and all); the split by round is reckoned here from
// the bouts, since the server has no column for it.
function buildGroupTable(tab: GameTab | undefined): HTMLElement {
  const groups = (tab?.stages || []).map((code) => {
    const stage = (scheme.stages || []).find((entry) => entry.code === code);
    const planned = stage?.matches || [];
    const blockRoundCount = Math.max(1, ...planned.map((match) => Number(match.round || 1)));
    const matches = planned.map((match) => {
      const view = page.view(match.code || "");
      return {code: match.code, blockRound: match.round, finished: Boolean(view?.finished), questionValues: view?.questionValues, participants: view?.participants};
    });
    const rows = computeGroupBlockRounds({matches, pointsRule: stage?.config?.rules?.bout?.points, blockRoundCount});
    const standings = page.festStage(code)?.standings || [];
    if (standings.length) {
      const byID = new Map(standings.filter((entry) => entry.participantID).map((entry) => [Number(entry.participantID), entry]));
      const byName = new Map(standings.map((entry) => [String(entry.name || "").trim(), entry]));
      const entryOf = (row: {id: number; name: string}) => (row.id ? byID.get(row.id) : byName.get(row.name));
      for (const row of rows) {
        const points = Number(entryOf(row)?.metrics?.points);
        if (Number.isFinite(points)) row.points = points;
      }
      const rank = (row: {id: number; name: string}) => Number(entryOf(row)?.rank) || Number.MAX_SAFE_INTEGER;
      rows.sort((a, b) => rank(a) - rank(b) || b.points - a.points || a.name.localeCompare(b.name, "ru"));
    }
    if (!rows.length) {
      for (const entrant of stage?.config?.entrants || []) {
        if (entrant.label) rows.push({id: 0, name: entrant.label, points: 0, blockRounds: new Array<number>(blockRoundCount).fill(0), bouts: []});
      }
    }
    const groupBouts = planned.map((match) => {
      const view = page.view(match.code || "");
      const state = stateOf(match.code || "");
      const values = valuesOf(view);
      const sides = !view ? [] : seatsOf(view).map((id, seat) => ({name: seatName(view, seat), score: ek.scoreSection(state.sections.get(id), values).total}));
      return {
        label: boutLetters.get(match.code || "") || match.code || "",
        href: page.boutHref(match.code || ""),
        blockRound: Number(match.round) || undefined,
        sides,
        started: Boolean(view?.finished) || sides.some((side) => Number(side.score)),
      };
    });
    return {title: stage ? groupLabel(stage as StageRef) : code, anchor: groupAnchorID(code), blockRoundCount, rows, groupBouts};
  });
  return buildGroupStandingsView(groups, {boutHref: page.boutHref});
}

// buildRankedStageTable draws a ranked stage's table: place, team, and the
// metrics the server ranked by, in its order.
function buildRankedStageTable(stage: FestGridStage): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  const metrics = ((stage.sort || []) as SortRule[]).map((rule) => rule.metric).filter((metric) => metric !== "draw");
  wrapper.appendChild(standingsTable({
    className: "stage-standings-table",
    columns: [
      {label: S.ek.table.place(), kind: "place"},
      {label: S.ek.table.team(), kind: "name"},
      ...metrics.map((metric) => ({label: reseedMetricHeader(metric, []), kind: "num" as const})),
    ],
    rows: (stage.standings || []).map((entry, index) => [
      String(entry.rank || index + 1),
      entry.name || "",
      ...metrics.map((metric) => reseedMetricValue(metric, entry.metrics?.[metric])),
    ]),
  }));
  return wrapper;
}

function buildStats(): HTMLElement {
  const stages = (scheme.stages || []).map((stage) => ({
    code: stage.code,
    matches: stageBouts(page, stage).map((bout) => bout.view),
  }));
  // A personal game has no per-theme players: the participant is the player.
  return individual
    ? buildIndividualStatsTable(computeIndividualPlayerStats(stages as never))
    : buildEKStatsTable(computeEKPlayerStats(stages as never));
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "stats":
    return buildStats();
  case "block":
    return buildGroupTable(tab);
  default:
    // A stage's tab, or a Block round's: the module draws the grid and the
    // reseeds.
    return buildBouts(tab);
  }
}

page.start();
