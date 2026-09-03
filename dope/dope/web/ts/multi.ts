// The multi games page: one sheet per minigame side by side, a subtotal after
// each and the total at the end, plus the ranked table, refusals and the roster. A
// self-booting side-effect module bundled by pages/multi.ts.
//
// The cells hold numbers, so a task whose domain is two or three values is a
// cell you click through and a wider one is a cell you type into; either way
// what may be entered is the scheme's, never the page's.

import {cssEscape, td, th} from "./cells.js";
import type {CellContent} from "./cells.js";
import {resultsTeamCell, standingsTable} from "./standings.js";
import {buildRosterView} from "./fest-roster.js";
import {mountGameDocument, mountGamePage} from "./game-shell.js";
import {parseGameRoute} from "./game-page.js";
import type {GameDataSnapshot, GameInitLike} from "./game-page.js";
import {bindScrollEdges, createTeamNameOverflowController, fitScrollFade, renderTabBar} from "./widgets.js";
import {createSheetCursor} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import * as multi from "./multi-protocol.js";
import {CYCLE_LIMIT} from "./multi-protocol.js";
import S from "./i18nstrings_ru_gen.js";
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
let activeTab = tabFromHash() || "detailed";

function tabFromHash(): string | null {
  const key = (window.location.hash || "").replace(/^#/, "");
  return TABS.some((t) => t.key === key) ? key : null;
}

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});

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
  gamesRow.appendChild(th(S.multi.sheet.team(), "sticky sticky-name", {rowSpan: 2}));
  gamesRow.appendChild(th(S.multi.sheet.total(), "sticky sticky-total number", {rowSpan: 2}));
  if (rules.signed) gamesRow.appendChild(th("Σ+", "sticky sticky-place number", {rowSpan: 2}));
  rules.minigames.forEach((game, g) => {
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
  const labelText = `${number ? number + ". " : ""}${multi.participantName(state!, p)}`;
  const cell = td("", "sticky sticky-name team-name ek-team-cell", {dataset: {multiTeamCell: ""}});
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout";
  const nameWrap = document.createElement("span");
  nameWrap.className = "od-detailed-team-name-wrap";
  const label = document.createElement("span");
  label.className = "od-detailed-team-name";
  label.textContent = labelText;
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
  span.textContent = uniformNominal(game)
    ? S.multi.game.uniformPrice(game.name, String(maxOf(game.columns[0]?.values || [])))
    : game.name;
  span.style.left = "calc(var(--sheet-corner-col) + var(--team-col) + var(--total-col) + var(--space-5)" +
    (rules.signed ? " + var(--place-col)" : "") + " + var(--space-2))";
  return span;
}

// A head is the question's number — with its nominal above, muted, where the
// minigame pays unevenly (OD's qhead stack).
function questionHead(num: number, nominal: number | null): CellContent {
  if (nominal === null) return String(num);
  const wrap = document.createElement("span");
  wrap.className = "od-detailed-qhead";
  const price = document.createElement("span");
  price.className = "od-detailed-qcount";
  price.textContent = String(nominal);
  const number = document.createElement("span");
  number.textContent = String(num);
  wrap.append(price, number);
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

function rowOrder(): number[] {
  return state!.participants.map((_, index) => index);
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
});

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
  const rows = multi.rankedResultRows(state!, rules, (index) => multi.participantName(state!, index));
  return standingsTable({
    columns: [
      {label: S.multi.results.place(), kind: "place"},
      {label: S.multi.results.team(), kind: "name"},
      ...rules.minigames.map((game) => ({label: game.name, kind: "num" as const})),
      {label: S.multi.results.total(), kind: "num" as const, className: "total-col"},
      ...(rules.signed ? [{label: "Σ+", kind: "num" as const, className: "total-col"}] : []),
    ],
    rows: rows.map((row) => [
      row.placeText,
      resultsTeamCell(row.name),
      ...row.games.map(multi.formatScore),
      multi.formatScore(row.total),
      ...(rules.signed ? [String(row.plus)] : []),
    ]),
  });
}

// The refusals tab is the host's: a team that refused to play keeps its row on
// the sheet and leaves the ranking, so the numbers of the rest do not shift.
function buildRefusalsTable(): HTMLElement {
  const table = document.createElement("table");
  table.className = "match-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  headRow.appendChild(th("№"));
  headRow.appendChild(th(S.multi.refusals.team(), "results-team-head"));
  headRow.appendChild(th(S.multi.refusals.declined()));
  head.appendChild(headRow);
  table.appendChild(head);
  const body = document.createElement("tbody");
  state!.participants.forEach((_, index) => {
    const tr = document.createElement("tr");
    tr.appendChild(td(String(multi.participantNumber(state!, index) || "")));
    tr.appendChild(td(multi.participantName(state!, index), "results-team"));
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
    body.appendChild(tr);
  });
  table.appendChild(body);
  return table;
}

// === render ===

function render(): void {
  if (!scheme || !state) return;
  shell.renderChrome();
  if (!TABS.some((t) => t.key === activeTab)) activeTab = "detailed";
  if (tabsRoot) renderTabBar(tabsRoot, TABS, activeTab, (key) => {
    activeTab = key;
    window.location.hash = key;
    render();
  });
  const node = activeTab === "results"
    ? buildResultsTable()
    : activeTab === "refusals"
      ? buildRefusalsTable()
      : activeTab === "roster"
        ? rosterView()
        : buildTable();
  root.replaceChildren(node);
  root.classList.toggle("fits-frame", activeTab === "roster");
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
