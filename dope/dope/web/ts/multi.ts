// The multi games page: one sheet per minigame side by side, a subtotal after
// each and the total at the end, plus the ranked table, refusals and the roster. A
// self-booting side-effect module bundled by pages/multi.ts.
//
// The cells hold numbers, so a task whose domain is two or three values is a
// cell you click through and a wider one is a cell you type into; either way
// what may be entered is the scheme's, never the page's.

import {cssEscape, questionNumberNode, td, th} from "./cells.js";
import type {CellContent} from "./cells.js";
import {resultsTeamCell, standingsTable} from "./standings.js";
import {buildRosterView} from "./fest-roster.js";
import {mountGameDocument, mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameDataSnapshot, GameInitLike} from "./game-page.js";
import {bindScrollEdges, createTeamNameOverflowController, fitScrollFade, renderTabBar} from "./widgets.js";
import {icon, iconed} from "./icons_gen.js";
import type {IconName} from "./icons_gen.js";
import type {WriteRequest} from "./state-sync.js";
import {createSheetCursor} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {onNavigate, setHashTab, tabFromHash} from "./url-state.js";
import {ALL_DIVISIONS, divisionChipRow, divisionFromURL, divisionsOf, inDivision, setDivisionInURL} from "./divisions.js";
import * as multi from "./multi-protocol.js";
import {CYCLE_LIMIT} from "./multi-protocol.js";
import S from "./i18nstrings.js";
import type {MultiRules, MultiScheme, MultiState} from "./multi-protocol.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}
const pageWindow = window as Window & PageGlobals;

interface FestInfo {
  title?: string;
  gameName?: string;
  [key: string]: unknown;
}

const root = document.getElementById("multiTable")!;
const tabsRoot = document.getElementById("multiTabs");
const statusNode = document.getElementById("status");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const route = parseGameRoute();
const shell = mountGamePage({
  app: "multi",
  root,
  statusNode,
  breadcrumbsNode,
  festID: route.festID,
  gameID: route.gameID,
  viewer: Boolean(route.viewer),
  apiBase: route.apiBase,
  init: pageWindow.__GAME_INIT__,
  chrome: () => ({festTitle: fest?.title || "", gameTitle: fest?.gameName || scheme?.title || S.multi.title()}),
  cursorKinds: {
    cell: {selector: ".multi-cell", keys: ["participant", "game", "column"]},
  },
  activeCursorElement: () => sheet.activeCell,
  recorderState: () => state,
});
const {viewer} = shell;

fitScrollFade(root.closest(".sheet-frame"));
// Once the sheet is scrolled, the frozen columns' edge shades the content
// sliding under it — the fade every other sheet draws.
const sheetScroll = bindScrollEdges(root.closest(".sheet-frame"), ({left}, frame) => {
  frame.classList.toggle("detailed-scroll-left", activeTab === "detailed" && left);
});

const teamNameOverflow = createTeamNameOverflowController({
  root,
  detailed: {cellSelector: "[data-multi-team-cell]", nameSelector: ".od-detailed-team-name", truncatedClass: "od-detailed-team-cell-truncated"},
  results: {cellSelector: ".results-team", nameSelector: ".results-team-name", truncatedClass: "results-team-truncated"},
});
window.addEventListener("resize", () => teamNameOverflow.schedule());

let scheme: MultiScheme | null = null;
let state: MultiState | null = null;
let fest: FestInfo | null = null;
let rules: MultiRules = {minigames: [], sorting: ["total"], signed: false};
let participants: string[] = [];

const TABS = [
  {key: "detailed", label: S.multi.tabs.detailed()},
  {key: "results", label: S.multi.tabs.results()},
  ...(viewer ? [] : [{key: "refusals", label: S.multi.tabs.refusals()}]),
  {key: "roster", label: S.multi.tabs.roster()},
];
let activeTab = tabFromHash(TABS) || "detailed";

// The Division the viewer is looking at (ADR-0020): «All» until the URL says so.
let activeDivision = ALL_DIVISIONS;

onNavigate(() => {
  const next = tabFromHash(TABS);
  const division = state ? divisionFromURL(divisions()) : activeDivision;
  const tabMoved = Boolean(next && next !== activeTab);
  if (!tabMoved && division === activeDivision) return;
  activeDivision = division;
  if (next && tabMoved) activeTab = next;
  render();
});

// divisions is every Division this game's teams carry, in first-seen order.
function divisions(): string[] {
  if (!state) return [];
  return divisionsOf(state.participants.map((_, index) => multi.participantFlags(state!, index)));
}

// divisionMembers is the rows the chosen Division takes; undefined for «All».
function divisionMembers(): number[] | undefined {
  if (activeDivision === ALL_DIVISIONS) return undefined;
  const members: number[] = [];
  state!.participants.forEach((_, index) => {
    if (inDivision(multi.participantFlags(state!, index), activeDivision)) members.push(index);
  });
  return members;
}

// divisionChips heads the results table when any team carries a Flag.
function divisionChips(): HTMLElement | null {
  const offered = divisions();
  if (!offered.length) return null;
  return divisionChipRow(offered, activeDivision, (division) => {
    if (division === activeDivision) return;
    activeDivision = division;
    setDivisionInURL(division);
    render();
  });
}

const doc = mountGameDocument({
  route,
  cachePrefix: "multi",
  shell,
  adopt: adoptGameSnapshot,
  apply: applyRemoteState,
  current: () => ({scheme, state, fest}),
});

function adoptGameSnapshot({scheme: nextScheme, state: nextState, fest: nextFest}: GameDataSnapshot): void {
  scheme = nextScheme as MultiScheme;
  state = nextState as MultiState;
  fest = (nextFest as FestInfo | null) || null;
  rules = multi.rulesOf(scheme!);
  participants = multi.schemeParticipants(scheme!);
  state = multi.parseState(state, rules, participants);
  render();
}

function applyRemoteState(next: unknown): void {
  state = multi.parseState(next, rules, participants);
  render();
}

// === the sheet ===

// A column block per minigame — its tasks, then its subtotal — and the total last.
// The nominal row prints what each task is worth, which is the top of its
// domain: a host reading the sheet wants the task's price, not its range.
function buildTable(): HTMLElement {
  const table = document.createElement("table");
  // The KSI sheet's compact skin: the same short rows and tight cells.
  table.className = "match-table compact-score-table multi-table";

  const head = document.createElement("thead");
  const gamesRow = document.createElement("tr");
  gamesRow.appendChild(th(teamHead(), "sticky sticky-name", {rowSpan: 2}));
  gamesRow.appendChild(th(S.multi.sheet.total(), "sticky sticky-total number", {rowSpan: 2}));
  if (rules.signed) gamesRow.appendChild(th("Σ+", "sticky sticky-place number", {rowSpan: 2}));
  rules.minigames.forEach((game, g) => {
    // A gap column parts one minigame from the next, as KSI parts its themes.
    if (g > 0) gamesRow.appendChild(th("", "gap-head", {rowSpan: 2}));
    gamesRow.appendChild(th(gameHead(game), "theme-block",
      {colSpan: game.columns.length + gapCount(game) + 1, dataset: {game: g}}));
  });
  head.appendChild(gamesRow);

  const valuesRow = document.createElement("tr");
  rules.minigames.forEach((game) => {
    const uniform = uniformNominal(game);
    game.columns.forEach((column, c) => {
      if (c > 0 && column.block !== game.columns[c - 1].block) valuesRow.appendChild(th("", "gap-head"));
      valuesRow.appendChild(th(questionHead(c + 1, uniform ? null : maxOf(column.values)), "nominal"));
    });
    valuesRow.appendChild(th("Σ", "theme-block-score"));
  });
  head.appendChild(valuesRow);
  table.appendChild(head);

  const sheetRows = multi.scoreSheet(state!, rules);
  const body = document.createElement("tbody");
  rowOrder().forEach((p) => {
    const tr = document.createElement("tr");
    if (multi.participantDeclined(state!, p)) tr.classList.add("declined-row");
    tr.appendChild(teamCell(p));
    tr.appendChild(td(multi.formatScore(sheetRows[p].total), "sticky sticky-total number total-cell",
      {dataset: {total: p}}));
    if (rules.signed) {
      tr.appendChild(td(String(sheetRows[p].plus), "sticky sticky-place number", {dataset: {plus: p}}));
    }
    rules.minigames.forEach((game, g) => {
      if (g > 0) tr.appendChild(td("", "gap"));
      game.columns.forEach((column, c) => {
        if (c > 0 && column.block !== game.columns[c - 1].block) tr.appendChild(td("", "gap"));
        tr.appendChild(cellNode(p, g, c));
      });
      tr.appendChild(td(String(sheetRows[p].raw[g]), "number theme-block-score",
        {dataset: {subtotal: `${p}-${g}`}}));
    });
    body.appendChild(tr);
  });
  table.appendChild(body);
  return table;
}

// The sticky name cell is EK's: the ek-team-cell family brings the clipped
// name, the fade and the hover popover, so a long team never paints over the
// scores beside it.
function teamCell(p: number): HTMLElement {
  const number = multi.participantNumber(state!, p);
  const name = multi.participantName(state!, p);
  const labelText = `${number > 0 ? number + ". " : ""}${name}`;
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {dataset: {multiTeamCell: ""}});
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout";
  // The number stands in a column of its own, so the rows read down it as
  // they do in KSI's sheet.
  const numberNode = document.createElement("span");
  numberNode.className = "multi-team-number";
  numberNode.textContent = number > 0 ? String(number) : "";
  layout.appendChild(numberNode);
  const nameWrap = document.createElement("span");
  nameWrap.className = "od-detailed-team-name-wrap";
  const label = document.createElement("span");
  label.className = "od-detailed-team-name";
  label.textContent = name;
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

// Points taken wear the green fill and points lost the red one — the
// answer-cell idiom every sheet speaks.
function paintCell(cell: HTMLElement, value: number): void {
  cell.classList.toggle("right", value > 0);
  cell.classList.toggle("wrong", value < 0);
}

function uniformNominal(game: MultiRules["minigames"][number]): boolean {
  const first = maxOf(game.columns[0]?.values || []);
  return game.columns.every((column) => maxOf(column.values) === first);
}

// samePriceEverywhere is whether every task of every minigame pays the same
// top value, as in a song round of two points a question. The price then tells no
// minigame from another, so a head does not repeat it.
function samePriceEverywhere(): boolean {
  const first = maxOf(rules.minigames[0]?.columns[0]?.values || []);
  return rules.minigames.length > 1 &&
    rules.minigames.every((game) => uniformNominal(game) && maxOf(game.columns[0]?.values || []) === first);
}

function gapCount(game: MultiRules["minigames"][number]): number {
  let gaps = 0;
  for (let c = 1; c < game.columns.length; c++) {
    if (game.columns[c].block !== game.columns[c - 1].block) gaps++;
  }
  return gaps;
}

// The minigame's name rides sticky past the frozen columns, so a scrolled
// sheet still says which game these columns are; a uniform price joins it —
// "Not only songs (1 each)" — and the heads keep just the numbers.
function gameHead(game: MultiRules["minigames"][number]): CellContent {
  const span = document.createElement("span");
  span.className = "multi-game-name";
  span.textContent = uniformNominal(game) && !samePriceEverywhere()
    ? S.multi.game.uniformPrice(game.name, String(maxOf(game.columns[0]?.values || [])))
    : game.name;
  span.style.left = "calc(var(--sheet-corner-col) + var(--team-col) + var(--total-col) + var(--space-5)" +
    (rules.signed ? " + var(--place-col)" : "") + ")";
  return span;
}

// A head is the question's number — with its nominal above, muted, where the
// minigame pays unevenly (OD's qhead stack).
function questionHead(num: number, nominal: number | null): CellContent {
  if (nominal === null) return questionNumberNode(num);
  const wrap = document.createElement("span");
  wrap.className = "od-detailed-qhead";
  const price = document.createElement("span");
  price.className = "od-detailed-qcount";
  price.textContent = String(nominal);
  wrap.append(price, questionNumberNode(num));
  return wrap;
}

function maxOf(values: number[]): number {
  return values.reduce((best, v) => (v > best ? v : best), values[0] ?? 0);
}

function cellNode(participant: number, game: number, column: number): HTMLElement {
  const value = multi.cellValue(state!, game, participant, column);
  const cell = td(value === 0 ? "" : String(value), "multi-cell answer-cell", {
    dataset: {participant, game, column},
  });
  paintCell(cell, value);
  return cell;
}

// The sheet's row order is the host's own: by name, as the sheet has always
// listed the teams, or by number — the order the answer slips and the
// checkers' table come in, so a column pasted from there lands team by team.
// Local to this page, never synced.
let detailedSort: "name" | "number" = "name";
const nameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});

function rowOrder(): number[] {
  const order = state!.participants.map((_, index) => index);
  const byName = (a: number, b: number) =>
    nameCollator.compare(multi.participantName(state!, a), multi.participantName(state!, b)) || a - b;
  if (detailedSort === "name") return order.sort(byName);
  // A guest team has no number (it is below zero) and a legacy entry none at
  // all: they follow the numbered teams, by name.
  const numberOf = (index: number) => {
    const number = multi.participantNumber(state!, index);
    return number > 0 ? number : Infinity;
  };
  return order.sort((a, b) => numberOf(a) - numberOf(b) || byName(a, b));
}

function setDetailedSort(key: "name" | "number"): void {
  if (detailedSort === key) return;
  detailedSort = key;
  render();
}

// The team column's head: the number and the team, each a button that orders the
// rows by it, as the KSI sheet's head does.
function teamHead(): HTMLElement {
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout multi-team-head-layout";
  const sortButton = (text: string, title: string, key: "name" | "number", className: string) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `${className} multi-sort-head`;
    button.textContent = text;
    button.title = title;
    button.setAttribute("aria-label", title);
    button.classList.toggle("multi-sort-active", detailedSort === key);
    button.addEventListener("click", () => setDetailedSort(key));
    return button;
  };
  layout.append(
    sortButton("№", S.multi.sheet.sortByNumber(), "number", "multi-team-number"),
    sortButton(S.multi.sheet.team(), S.multi.sheet.sortByName(), "name", "multi-team-head-label"),
  );
  return layout;
}

// === editing ===

// domainOf is what this cell may hold. A domain small enough to click through
// cycles; a wider one is typed, and a typed value outside the domain is
// refused rather than silently rounded — the scheme said what a task pays.
function domainOf(game: number, column: number): number[] {
  return rules.minigames[game]?.columns[column]?.values || [0];
}

const sheet = createSheetCursor({
  root,
  cellSelector: ".multi-cell",
  values: "text",
  readonly: () => viewer || activeTab !== "detailed" || Boolean(state?.finished),
  active: () => activeTab === "detailed",
  rows: () => rowOrder().length,
  cols: () => rules.minigames.reduce((n, game) => n + game.columns.length, 0),
  coordOf: (cell) => {
    const node = cell as HTMLElement;
    const participant = Number(node.dataset.participant);
    const game = Number(node.dataset.game);
    const column = Number(node.dataset.column);
    if (!Number.isInteger(participant) || !Number.isInteger(game) || !Number.isInteger(column)) return null;
    const row = rowOrder().indexOf(participant);
    if (row < 0) return null;
    return {row, col: flatColumn(game, column)};
  },
  cellAt: (coord: CellCoord) => {
    const participant = rowOrder()[coord.row];
    const at = unflatColumn(coord.col);
    if (participant === undefined || !at) return null;
    return root.querySelector<HTMLElement>(
      `.multi-cell[data-participant="${cssEscape(String(participant))}"]` +
      `[data-game="${cssEscape(String(at.game))}"][data-column="${cssEscape(String(at.column))}"]`);
  },
  cycle: (cell: Element) => {
    const node = cell as HTMLElement;
    const values = domainOf(Number(node.dataset.game), Number(node.dataset.column));
    if (values.length > CYCLE_LIMIT) return null;
    const current = Number(node.textContent || 0) || 0;
    const at = values.indexOf(current);
    return String(values[(at + 1) % values.length]);
  },
  applyValues: applyCellEdits,
  onEdit: typeIntoCell,
});

// typeIntoCell is the keyboard's way into a cell. Where every value the cell
// may hold is one character (0, 1, 2), a keystroke is the value, written at
// once, and the cursor moves on. Where it is wider, keystrokes in quick
// succession build the number and the cursor stays. Enter steps the value
// the way a tap does. A value outside the cell's domain is refused, as a
// paste's is.
let typed = {cell: null as HTMLElement | null, text: "", at: 0};
const TYPING_PAUSE_MS = 1200;

function typeIntoCell(cell: HTMLElement, text: string | null): void {
  const values = domainOf(Number(cell.dataset.game), Number(cell.dataset.column));
  if (text === null) {
    if (values.length > CYCLE_LIMIT) return;
    const current = Number(cell.textContent || 0) || 0;
    applyCellEdits([{cell, value: String(values[(values.indexOf(current) + 1) % values.length])}]);
    return;
  }
  if (!/^[0-9\-−]$/.test(text)) return;
  const single = values.every((value) => String(value).length === 1);
  if (single) {
    applyCellEdits([{cell, value: text}]);
    if (values.includes(Number(text))) sheet.moveBy(0, 1);
    return;
  }
  const now = Date.now();
  const text2 = typed.cell === cell && now - typed.at < TYPING_PAUSE_MS ? typed.text + text : text;
  typed = {cell, text: text2, at: now};
  applyCellEdits([{cell, value: text2}]);
}

function flatColumn(game: number, column: number): number {
  let base = 0;
  for (let g = 0; g < game; g++) base += rules.minigames[g].columns.length;
  return base + column;
}

function unflatColumn(col: number): {game: number; column: number} | null {
  let base = 0;
  for (let g = 0; g < rules.minigames.length; g++) {
    const width = rules.minigames[g].columns.length;
    if (col < base + width) return {game: g, column: col - base};
    base += width;
  }
  return null;
}

function applyCellEdits(edits: CellEdit[]): void {
  let changed = false;
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const participant = Number(cell.dataset.participant);
    const game = Number(cell.dataset.game);
    const column = Number(cell.dataset.column);
    if (!Number.isInteger(participant) || !Number.isInteger(game) || !Number.isInteger(column)) continue;
    const values = domainOf(game, column);
    const text = String(edit.value ?? "").trim();
    const value = text === "" ? 0 : Number(text.replace(",", ".").replace("−", "-"));
    if (!Number.isFinite(value) || !values.includes(value)) continue;
    const row = state!.games[game].cells[participant];
    if (!row || row[column] === value) continue;
    row[column] = value;
    cell.textContent = value === 0 ? "" : String(value);
    paintCell(cell, value);
    doc.save(["games", game, "cells", participant, column], value);
    changed = true;
  }
  if (changed) refreshTotals();
}

// refreshTotals repaints the numbers the cells feed rather than the sheet, so
// an edit does not move the cursor out from under the host.
function refreshTotals(): void {
  const sheetRows = multi.scoreSheet(state!, rules);
  state!.participants.forEach((_, p) => {
    rules.minigames.forEach((_game, g) => {
      const node = root.querySelector<HTMLElement>(`[data-subtotal="${cssEscape(`${p}-${g}`)}"]`);
      if (node) node.textContent = String(sheetRows[p].raw[g]);
    });
    const total = root.querySelector<HTMLElement>(`[data-total="${cssEscape(String(p))}"]`);
    if (total) total.textContent = multi.formatScore(sheetRows[p].total);
    const plus = root.querySelector<HTMLElement>(`[data-plus="${cssEscape(String(p))}"]`);
    if (plus) plus.textContent = String(sheetRows[p].plus);
  });
}

// === the other tabs ===

function buildResultsTable(): HTMLElement {
  const rows = multi.rankedResultRows(state!, rules, (index) => multi.participantName(state!, index), divisionMembers());
  // A fest that ranks on the sum of places reads each minigame's place beside
  // its score, and the sum in a column of its own.
  const byPlaces = rules.sorting.includes(multi.PLACE_SUM);
  return standingsTable({
    columns: [
      {label: S.multi.results.place(), kind: "place"},
      {label: S.multi.results.team(), kind: "name"},
      ...rules.minigames.map((game) => ({label: game.name, kind: "num" as const})),
      ...(byPlaces ? [{label: S.multi.results.placeSum(), kind: "num" as const, className: "total-col"}] : []),
      {label: S.multi.results.total(), kind: "num" as const, className: "total-col"},
      ...(rules.signed ? [{label: "Σ+", kind: "num" as const, className: "total-col"}] : []),
    ],
    rows: rows.map((row) => [
      row.placeText,
      resultsTeamCell(row.name),
      ...row.games.map((score, g) => byPlaces
        ? S.multi.results.scoreAndPlace(multi.formatScore(score), formatPlace(row.places[g]))
        : multi.formatScore(score)),
      ...(byPlaces ? [formatPlace(row.placeSum)] : []),
      multi.formatScore(row.total),
      ...(rules.signed ? [String(row.plus)] : []),
    ]),
  });
}

// formatPlace prints a place or a sum of places, which a shared place makes
// fractional: 3.5, not 3.50.
function formatPlace(value: number): string {
  return String(Math.round(value * 100) / 100);
}

// The teams tab is the host's. A team that refused to play keeps its row on
// the sheet and leaves the ranking, so the numbers of the rest do not shift.
// Below the fest's teams come the game's guest teams, which the host adds here
// by name, renames, and removes while nothing is entered for them.
function buildTeamsPanel(): HTMLElement {
  const panel = document.createElement("div");
  panel.className = "u-col u-gap-md";
  panel.appendChild(buildTeamsTable());
  if (guestNotice) {
    const notice = document.createElement("p");
    notice.className = "hint hint-danger";
    notice.textContent = guestNotice;
    panel.appendChild(notice);
  }
  panel.appendChild(buildGuestAddForm());
  const hint = document.createElement("p");
  hint.className = "hint";
  hint.textContent = S.multi.guests.hint();
  panel.appendChild(hint);
  return panel;
}

function buildTeamsTable(): HTMLElement {
  const table = document.createElement("table");
  table.className = "match-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  headRow.appendChild(th("№"));
  headRow.appendChild(th(S.multi.refusals.team(), "results-team-head"));
  headRow.appendChild(th(S.multi.refusals.declined()));
  headRow.appendChild(th(""));
  head.appendChild(headRow);
  table.appendChild(head);
  const body = document.createElement("tbody");
  state!.participants.forEach((_, index) => {
    const guest = multi.participantGuest(state!, index);
    const number = multi.participantNumber(state!, index);
    const tr = document.createElement("tr");
    tr.appendChild(td(number > 0 ? String(number) : ""));
    tr.appendChild(guest && renaming === number
      ? td(guestRenameField(number, multi.participantName(state!, index)))
      : td(multi.participantName(state!, index), "results-team"));
    const box = document.createElement("input");
    box.type = "checkbox";
    box.checked = multi.participantDeclined(state!, index);
    box.disabled = viewer;
    box.addEventListener("change", () => {
      const key = multi.declinedKey(state!, index);
      if (!key) return;
      state!.declined[key] = box.checked;
      doc.save(["declined", key], box.checked);
      render();
    });
    tr.appendChild(td(box));
    tr.appendChild(td(guest ? guestActions(number, multi.participantName(state!, index)) : ""));
    body.appendChild(tr);
  });
  table.appendChild(body);
  return table;
}

// === guest teams ===

// renaming is the guest team whose name is an input just now (0: none);
// guestNotice is what the last guest write said when it failed.
let renaming = 0;
let guestNotice = "";

function guestActions(number: number, name: string): HTMLElement {
  const actions = document.createElement("span");
  actions.className = "multi-guest-actions u-row u-gap-xs";
  const rename = iconButton("pencil", S.multi.guests.renameLabel());
  rename.addEventListener("click", () => {
    renaming = number;
    guestNotice = "";
    render();
    root.querySelector<HTMLInputElement>("[data-guest-rename]")?.focus();
  });
  const remove = iconButton("trash-2", S.multi.guests.removeLabel());
  remove.addEventListener("click", () => {
    if (!window.confirm(S.multi.guests.removeConfirm(name))) return;
    void guestWrite({url: `${route.apiBase}/guests/${number}`, method: "DELETE"});
  });
  actions.append(rename, remove);
  return actions;
}

function iconButton(name: IconName, label: string): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "action-icon";
  button.title = label;
  button.setAttribute("aria-label", label);
  button.appendChild(icon(name));
  return button;
}

// guestRenameField is a guest team's name as an input: Enter or leaving the
// field saves it, Escape keeps the old one.
function guestRenameField(number: number, name: string): HTMLElement {
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.value = name;
  input.dataset.guestRename = "";
  input.setAttribute("aria-label", S.multi.guests.renameLabel());
  let done = false;
  const finish = (save: boolean) => {
    if (done) return;
    done = true;
    renaming = 0;
    const next = input.value.trim();
    if (save && next && next !== name) {
      void guestWrite({url: `${route.apiBase}/guests/${number}`, method: "PUT", body: {name: next}});
    } else {
      render();
    }
  };
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") finish(true);
    else if (event.key === "Escape") finish(false);
  });
  input.addEventListener("blur", () => finish(true));
  return input;
}

function buildGuestAddForm(): HTMLElement {
  const form = document.createElement("form");
  form.className = "u-row u-wrap u-gap-sm u-align-center";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "input";
  input.placeholder = S.multi.guests.namePlaceholder();
  input.dataset.guestAdd = "";
  input.size = 32;
  input.setAttribute("aria-label", S.multi.guests.namePlaceholder());
  const add = document.createElement("button");
  add.type = "submit";
  add.className = "btn";
  add.append(...iconed("plus", S.multi.guests.add()));
  form.append(input, add);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const name = input.value.trim();
    if (!name) {
      input.focus();
      return;
    }
    void guestWrite({url: `${route.apiBase}/guests`, method: "POST", body: {name}}).then((ok) => {
      if (ok) root.querySelector<HTMLInputElement>("[data-guest-add]")?.focus();
    });
  });
  return form;
}

// guestWrite sends one guest-team edit. The server answers with the new state,
// which the writer adopts like any broadcast; a refusal is shown under the table.
async function guestWrite(request: WriteRequest): Promise<boolean> {
  guestNotice = "";
  const sent = await doc.sync().writer.send(doc.scope, request);
  if (!sent.ok) guestNotice = S.multi.guests.failed(sent.error || "");
  render();
  return sent.ok;
}

// === render ===

function render(): void {
  if (!scheme || !state) return;
  shell.renderChrome();
  if (!TABS.some((t) => t.key === activeTab)) activeTab = "detailed";
  // The strip is served hidden until the page has a document to switch
  // between, as on the KSI and Troika pages; nothing here ever showed it.
  if (tabsRoot) tabsRoot.hidden = false;
  if (tabsRoot) renderTabBar(tabsRoot, TABS, activeTab, (key) => {
    activeTab = key;
    setHashTab(key);
    render();
  });
  // A roster change can add a Division or take the chosen one away.
  activeDivision = divisionFromURL(divisions());
  const node = activeTab === "results"
    ? buildResultsTable()
    : activeTab === "refusals"
      ? buildTeamsPanel()
      : activeTab === "roster"
        ? rosterView()
        : buildTable();
  const chips = activeTab === "results" ? divisionChips() : null;
  root.replaceChildren(...(chips ? [chips, node] : [node]));
  root.classList.toggle("fits-frame", activeTab === "roster" || activeTab === "refusals");
  teamNameOverflow.schedule();
  sheetScroll.refresh();
  if (activeTab === "detailed") sheet.refresh();
}

let rosterCache: HTMLElement | null = null;
function rosterView(): HTMLElement {
  if (!rosterCache) rosterCache = buildRosterView(route.festID);
  return rosterCache;
}

sheet.bind();
doc.load().catch((error: unknown) => {
  console.error(error);
});
