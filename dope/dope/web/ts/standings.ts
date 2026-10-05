// The server's tables as the pages draw them (ADR-0011): standings columns and
// rows, the results team cell, group standings, and the fest-view stage refs
// they hang off (letters, stage type).

import {formatDisplayText, td, th} from "./cells.js";
import type {CellContent, CellContentItem} from "./cells.js";
import S from "./i18nstrings.js";
import {nameCell} from "./name-cell.js";
import {declarePins, sheetHead} from "./sheet-pins.js";
import type {Pins} from "./sheet-pins.js";

// Decimals a fractional score is printed with, trailing zeros dropped.
const SCORE_DECIMALS = 3;

export interface StageRef {
  code: string;
  title?: string;
  stage_type?: string;
  type?: string;
  // slug is the block's readable URL handle from the scheme, carried by its
  // stages; legacy is the pre-slug `@` spelling a synthetic tab answers to.
  slug?: string;
  legacy?: string;
  kind?: string;
  grain?: {block?: string; wave?: number; group?: string};
  matches?: StageRefMatch[];
  // members names the server stages a displayed stage is assembled from.
  members?: string[];
  // auto marks a reseed the server calculates on its own, a Swiss pool: it
  // is a step inside its Block, not a пересев between two.
  auto?: boolean;
}

export interface StageRefMatch {
  code?: string;
  title?: string;
  letter?: string;
  round?: number;
  group?: string;
}

// One group of the group-stage tab: its title and the rows the sheets'
// Groups view draws — a player, his points, and the split by block round.
export interface GroupStandingsGroup {
  title: string;
  // The id the group's table carries, so a link can land on it.
  anchor?: string;
  blockRoundCount: number;
  rows: Array<{name: string; points: number; blockRounds: number[]; bouts?: string[]}>;
}

export interface GroupStandingsOptions {
  // boutHref links a player's block round to the bout he played it in.
  boutHref?: (code: string) => string;
}

export interface TeamCellOptions {
  className?: string;
  city?: string;
  // flag is the country emoji the screen board decorates a name with, derived
  // from the city. It is NOT a Flag/Division — those are `badges` (ADR-0020).
  flag?: string;
  badges?: readonly string[];
  href?: string;
}

// resultsTeamCell is the name cell of a results table (name-cell.ts): the
// name clips into a fade with the full text on a popover, never an ellipsis. A
// flag is decoration: the label and the popover carry it, the aria-label does
// not. The Division badges sit on the second line beside the city — a fact
// about the team, not part of its name, so the popover and the aria-label
// ignore them.
export function resultsTeamCell(name: string, options: TeamCellOptions = {}): HTMLElement {
  return nameCell(options.flag ? `${options.flag} ${name}` : name, {
    className: options.className ? `results-team ${options.className}` : "results-team",
    ariaLabel: name,
    href: options.href,
    badges: options.badges,
    city: options.city,
  });
}

// A column's kind is its role in the results-table skin: the place, the fading
// name, a number. className is what its head and cells share beyond that.
export interface StandingsColumn {
  label: CellContent;
  kind?: "place" | "name" | "num";
  className?: string;
  // A place or a name column is pinned unless it says `pin: false`. The
  // first name column is the one that pins; a table with a second (EK's
  // stats: the player, then his team) lets the second scroll.
  pin?: boolean;
  // The column's head rides the scroll just past the pinned columns, though
  // its cells scroll (the roster's players).
  trailHead?: boolean;
}

// resultsPins declares a results table's pinned block: the place, the name
// and, on a sheet that shows one, Σ, at the widths the results-table skin
// gives them.
export function resultsPins({place = true, total = false}: {place?: boolean; total?: boolean} = {}): Pins {
  return declarePins([
    ...(place ? [{key: "place", width: "var(--results-place-col)"}] : []),
    {key: "name", width: "var(--results-team-col)"},
    ...(total ? [{key: "total", width: "var(--results-total-col)"}] : []),
  ]);
}

export interface StandingsSpec {
  className?: string;
  columns: StandingsColumn[];
  // A cell is text, or a cell the caller built when it needs more than text.
  rows: CellContentItem[][];
  // sortKey makes the columns sortable by a click on their head, and names
  // the table so a redraw keeps the reader's sort (a statistics tab redraws
  // on every live event).
  sortKey?: string;
}

// SortChoice is the column a reader sorted a table by, and whether they
// turned it round from its natural order.
interface SortChoice {
  column: number;
  turned: boolean;
}

// chosenSorts is each sortable table's sort, by its sortKey, for the page's
// lifetime.
const chosenSorts = new Map<string, SortChoice>();

// sortedOrder is the order a column's values sort in, as row indexes, and
// whether the column is numbers. Numbers run from the biggest down and text
// from A, unless the reader turned the column round; a tie keeps the order
// the table came in.
export function sortedOrder(values: ReadonlyArray<number | string>, turned: boolean): {order: number[]; numeric: boolean} {
  const numeric = values.every((value) => typeof value === "number" || value === "");
  const order = values.map((_, index) => index).sort((a, b) => {
    let by = numeric
      ? (Number(values[b]) || 0) - (Number(values[a]) || 0)
      : String(values[a]).localeCompare(String(values[b]), "ru");
    if (turned) by = -by;
    return by || a - b;
  });
  return {order, numeric};
}

// sortRows orders the body's rows by a column (sortedOrder).
function sortRows(table: HTMLTableElement, choice: SortChoice): void {
  const body = table.tBodies[0];
  if (!body) return;
  const rows = [...body.rows];
  const {order, numeric} = sortedOrder(rows.map((row) => sortValue(row.cells[choice.column])), choice.turned);
  order.forEach((index, at) => {
    const row = rows[index];
    row.classList.toggle("results-group-first", at === 0);
    row.classList.toggle("results-group-last", at === rows.length - 1);
    body.appendChild(row);
  });
  const heads = table.tHead?.rows[0]?.cells || [];
  [...heads].forEach((head, index) => {
    if (head.classList.contains("sortable-head")) head.setAttribute("aria-sort", index !== choice.column ? "none" : numeric !== choice.turned ? "descending" : "ascending");
  });
}

// sortValue reads a cell as the sort compares it: a number where the cell is
// one (the typographic minus and a decimal comma included), else its text.
export function sortValue(cell: Pick<Element, "textContent"> | undefined): number | string {
  const text = (cell?.textContent || "").trim();
  const number = Number(text.replace("\u2212", "-").replace(",", "."));
  return text !== "" && Number.isFinite(number) ? number : text.toLocaleLowerCase("ru");
}

// makeSortable lets a reader sort the table by any column with a head, and
// restores the sort they chose last.
function makeSortable(table: HTMLTableElement, key: string): void {
  const heads = [...(table.tHead?.rows[0]?.cells || [])];
  heads.forEach((head, column) => {
    const label = (head.textContent || "").trim();
    if (!label) return;
    head.classList.add("sortable-head");
    head.tabIndex = 0;
    head.title = S.standings.sort.by(label);
    head.setAttribute("aria-label", head.title);
    head.setAttribute("aria-sort", "none");
    const pick = () => {
      const was = chosenSorts.get(key);
      const choice = {column, turned: was?.column === column ? !was.turned : false};
      chosenSorts.set(key, choice);
      sortRows(table, choice);
    };
    head.addEventListener("click", pick);
    head.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      pick();
    });
  });
  const chosen = chosenSorts.get(key);
  if (chosen) sortRows(table, chosen);
}

const STANDINGS_KIND_CLASSES: Record<NonNullable<StandingsColumn["kind"]>, {head: string; cell: string}> = {
  place: {head: "results-place-head", cell: "results-place"},
  name: {head: "results-team-head", cell: "results-team"},
  num: {head: "results-num", cell: "results-num"},
};

// standingsTable is the one builder for every standings-shaped table — a
// place, a name, numbers — so no table restates the results-table skin.
export function standingsTable({className, columns, rows, sortKey}: StandingsSpec): HTMLTableElement {
  const table = document.createElement("table");
  table.className = classNames("results-table", className);
  const pinKeys = columns.map(pinKeyOf);
  const pins = resultsPins({place: pinKeys.includes("place")});
  const head = document.createElement("tr");
  columns.forEach((column, i) => {
    const cell = th(column.label, classNames(column.kind && STANDINGS_KIND_CLASSES[column.kind].head, column.className));
    if (pinKeys[i]) pins.mark(cell, pinKeys[i]);
    else if (column.trailHead) pins.markTrailing(cell);
    head.appendChild(cell);
  });
  table.appendChild(sheetHead([{row: head}]));
  const body = table.appendChild(document.createElement("tbody"));
  rows.forEach((row, index) => {
    const tr = document.createElement("tr");
    tr.className = classNames("results-row", index === 0 && "results-group-first", index === rows.length - 1 && "results-group-last");
    columns.forEach((column, i) => {
      const cell = standingsCell(column, row[i]);
      if (pinKeys[i]) pins.mark(cell, pinKeys[i]);
      tr.appendChild(cell);
    });
    body.appendChild(tr);
  });
  if (sortKey) makeSortable(table, sortKey);
  return table;

  // pinKeyOf is the pinned column a column is, or "" when it scrolls: the
  // first place and the first name, unless the column opts out.
  function pinKeyOf(column: StandingsColumn, index: number): string {
    if (column.pin === false || (column.kind !== "place" && column.kind !== "name")) return "";
    const first = columns.findIndex((other) => other.kind === column.kind && other.pin !== false);
    return first === index ? column.kind : "";
  }
}

function standingsCell(column: StandingsColumn, value: CellContentItem): HTMLElement {
  const own = column.kind ? STANDINGS_KIND_CLASSES[column.kind].cell : "";
  if (value instanceof Object) {
    const cell = value as HTMLElement;
    cell.className = classNames(cell.className, own, column.className);
    return cell;
  }
  if (column.kind === "name") return resultsTeamCell(formatDisplayText(value), {className: column.className});
  return td(value, classNames(own, column.className));
}

function classNames(...names: Array<string | false | null | undefined>): string {
  return names.filter(Boolean).join(" ");
}

// buildGroupStandingsView is the sheets' Groups view: all groups on one tab,
// each a table of Player | Points | Round 1..N, two abreast where the screen
// fits them.
export function buildGroupStandingsView(groups: GroupStandingsGroup[], options: GroupStandingsOptions = {}): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "group-standings";
  // Up to three decimals, trailing zeros dropped: a rule like Octobearfest's
  // (4 - place) + sum/1000 decides ties in the third place, and toFixed(1)
  // showed 14.96 and 15 alike as «15.0».
  const score = (value: number) => (Number.isInteger(value) ? String(value) : String(Number(value.toFixed(SCORE_DECIMALS))));
  const blockRounds = (group: GroupStandingsGroup) => Array.from({length: group.blockRoundCount}, (_, blockRound) => blockRound);
  for (const group of groups) {
    const item = document.createElement("section");
    item.className = "group-standings-item";
    if (group.anchor) item.id = group.anchor;
    const head = document.createElement("h3");
    head.className = "group-standings-head";
    head.textContent = group.title;
    item.appendChild(head);
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper";
    wrapper.appendChild(standingsTable({
      className: "group-standings-table",
      columns: [
        {label: S.standings.columns.place(), kind: "place"},
        {label: S.standings.columns.player(), kind: "name"},
        {label: S.standings.columns.points(), kind: "num"},
        ...blockRounds(group).map((blockRound) => ({label: S.standings.columns.blockRound(String(blockRound + 1)), kind: "num" as const})),
      ],
      rows: group.rows.map((row, index) => [
        index + 1,
        row.name,
        score(row.points),
        ...blockRounds(group).map((blockRound) => roundCell(score(row.blockRounds[blockRound] || 0), row.bouts?.[blockRound] || "", options)),
      ]),
    }));
    item.appendChild(wrapper);
    wrap.appendChild(item);
  }
  return wrap;
}

// roundCell is a player's points in one block round, a link to the bout he
// played it in where the page has one.
function roundCell(text: string, code: string, options: GroupStandingsOptions): CellContentItem {
  const href = code ? options.boutHref?.(code) || "" : "";
  if (!href) return text;
  const link = document.createElement("a");
  link.className = "group-round-link";
  link.href = href;
  link.textContent = text;
  return td(link);
}

// festLetters is every match's letter by code, read off the fest view: the
// compiler dealt them (A..Z, AA.. in schedule order, none for a block that
// declined) and the store carries them, so a page never counts.
export function festLetters(stages: ReadonlyArray<StageRef | null | undefined> | null | undefined): Map<string, string> {
  const letters = new Map<string, string>();
  for (const stage of stages || []) {
    for (const match of stage?.matches || []) {
      if (match.code && match.letter) letters.set(match.code, match.letter);
    }
  }
  return letters;
}

// letteredTitle swaps a title's bout number for the match's letter; a title
// the bout regex never matches (the written qualifier) is left alone.
export function letteredTitle(title: string, letter: string | undefined): string {
  if (!letter) return title;
  return title.replace(/Бой\s+\d+/, `Бой ${letter}`);
}

export function stageType(stage: {stage_type?: string; type?: string} | null | undefined): string {
  return stage?.stage_type || stage?.type || "";
}
