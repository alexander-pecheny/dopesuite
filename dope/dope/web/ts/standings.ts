// The server's tables as the pages draw them (ADR-0011): standings columns and
// rows, the results team cell, group standings, and the fest-view stage refs
// they hang off (letters, stage type).

import {formatDisplayText, nameNode, td, th} from "./cells.js";
import type {CellContent, CellContentItem} from "./cells.js";
import S from "./i18nstrings.js";

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
}

export interface StageRefMatch {
  code?: string;
  title?: string;
  letter?: string;
  round?: number;
  group?: string;
}

// One group of the group-stage tab: its title and the rows the sheets'
// Groups view draws — a player, his points, and the split by round.
export interface GroupStandingsGroup {
  title: string;
  roundCount: number;
  rows: Array<{name: string; points: number; rounds: number[]}>;
}

export interface TeamCellOptions {
  className?: string;
  city?: string;
  flag?: string;
  href?: string;
}

// resultsTeamCell is the one name cell of a results table: the name clips into
// a fade with the full text on a popover, never an ellipsis. A flag is
// decoration: the label and the popover carry it, the aria-label does not.
export function resultsTeamCell(name: string, options: TeamCellOptions = {}): HTMLElement {
  const cell = td("", classNames("results-team", options.className));
  const label = options.flag ? `${options.flag} ${name}` : name;
  const wrap = document.createElement("span");
  wrap.className = "results-team-name-wrap";
  const node = nameNode(label, options.href || "", "results-team-name");
  node.tabIndex = 0;
  node.setAttribute("aria-label", name);
  wrap.appendChild(node);
  if (options.city) {
    const city = document.createElement("span");
    city.className = "results-team-city";
    city.textContent = options.city;
    wrap.appendChild(city);
  }
  cell.appendChild(wrap);
  const popover = document.createElement("span");
  popover.className = "popover popover-inline results-team-name-popover";
  popover.textContent = label;
  cell.appendChild(popover);
  return cell;
}

// A column's kind is its role in the results-table skin: the place, the fading
// name, a number. className is what its head and cells share beyond that.
export interface StandingsColumn {
  label: CellContent;
  kind?: "place" | "name" | "num";
  className?: string;
}

export interface StandingsSpec {
  className?: string;
  columns: StandingsColumn[];
  // A cell is text, or a cell the caller built when it needs more than text.
  rows: CellContentItem[][];
}

const STANDINGS_KIND_CLASSES: Record<NonNullable<StandingsColumn["kind"]>, {head: string; cell: string}> = {
  place: {head: "results-place-head", cell: "results-place"},
  name: {head: "results-team-head", cell: "results-team"},
  num: {head: "results-num", cell: "results-num"},
};

// standingsTable is the one builder for every standings-shaped table — a
// place, a name, numbers — so no table restates the results-table skin.
export function standingsTable({className, columns, rows}: StandingsSpec): HTMLTableElement {
  const table = document.createElement("table");
  table.className = classNames("results-table", className);
  const head = document.createElement("tr");
  for (const column of columns) {
    head.appendChild(th(column.label, classNames(column.kind && STANDINGS_KIND_CLASSES[column.kind].head, column.className)));
  }
  table.appendChild(document.createElement("thead")).appendChild(head);
  const body = table.appendChild(document.createElement("tbody"));
  rows.forEach((row, index) => {
    const tr = document.createElement("tr");
    tr.className = classNames("results-row", index === 0 && "results-group-first", index === rows.length - 1 && "results-group-last");
    columns.forEach((column, i) => tr.appendChild(standingsCell(column, row[i])));
    body.appendChild(tr);
  });
  return table;
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
export function buildGroupStandingsView(groups: GroupStandingsGroup[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "group-standings";
  const score = (value: number) => (Number.isInteger(value) ? String(value) : value.toFixed(1));
  const rounds = (group: GroupStandingsGroup) => Array.from({length: group.roundCount}, (_, round) => round);
  for (const group of groups) {
    const item = document.createElement("section");
    item.className = "group-standings-item";
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
        ...rounds(group).map((round) => ({label: S.standings.columns.round(String(round + 1)), kind: "num" as const})),
      ],
      rows: group.rows.map((row, index) => [
        index + 1,
        row.name,
        score(row.points),
        ...rounds(group).map((round) => score(row.rounds[round] || 0)),
      ]),
    }));
    item.appendChild(wrapper);
    wrap.appendChild(item);
  }
  return wrap;
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
