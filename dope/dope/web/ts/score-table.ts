// A match's score table, flat or two-row, with its leading columns pinned; and
// a node index that finds its live cells by their dataset.

import {applyAttrs, cellFromSpec, formatDisplayText, td, th} from "./cells.js";
import type {CellAttrs, CellContent, CellSpec} from "./cells.js";
import {declarePins, sheetHead} from "./sheet-pins.js";
import type {HeadRow, Pins} from "./sheet-pins.js";
import S from "./i18nstrings.js";

export interface ScoreTableTheme {
  label?: CellContent;
  labelClassName?: string;
  questionLabels?: CellContent[];
  questionClassName?: string;
  gapHeaderClassName?: string;
  gapClassName?: string;
}

export interface ScoreTableThemeRow {
  answers?: CellSpec[];
  scoreCell?: CellSpec;
  score?: CellSpec;
  gapCell?: CellSpec;
  gapClassName?: string;
  playerCell?: CellSpec;
  answerGapCell?: CellSpec;
}

export interface ScoreTableRow {
  rowClassName?: string;
  answerRowClassName?: string;
  rowMarkerCell?: CellSpec;
  rowMarkerClassName?: string;
  nameCell?: CellSpec;
  totalCell?: CellSpec;
  total?: CellSpec;
  placeCell?: CellSpec;
  place?: CellSpec;
  placeGapCell?: CellSpec;
  themes?: ScoreTableThemeRow[];
  afterThemeCells?: CellSpec[];
}

export interface ScoreTableOptions {
  className?: string;
  attrs?: CellAttrs | null;
  events?: Record<string, EventListener>;
  themes?: ScoreTableTheme[];
  afterThemeHeaders?: CellSpec[];
  rows?: ScoreTableRow[];
  placeColumn?: boolean;
  rowMarkerColumn?: boolean;
  rowMarkerHeader?: CellSpec;
  rowMarkerHeaderClassName?: string;
  rowMarkerCellClassName?: string;
  nameHeader?: CellSpec;
  totalHeader?: CellSpec;
  placeHeader?: CellSpec;
  placeGapHeader?: CellSpec;
  questionClassName?: string;
  themeHeaderClassName?: string;
  gapHeaderClassName?: string;
  gapClassName?: string;
  answerRowClassName?: string;
  gapRows?: boolean;
  gapRowClassName?: string;
  gapCellClassName?: string;
  gapColSpan?: number;
  // The sheet's pinned block. Left out, it is scoreSheetPins for the columns
  // the table has.
  pins?: Pins;
  // Head rows over the table's own, top first, each with its height: Hamsa's
  // rounds. The table's own head row sticks below them.
  headRowsAbove?: HeadRow[];
}

// scoreSheetPins declares a score table's pinned block: the row marker when
// the sheet has one, the name, Σ, and the place with its gap. A format passes
// what differs: Hamsa's wider total, or `place: false` where the place scrolls
// with the questions (OD's detailed sheet, so two more questions fit on a
// phone). The block starts after the sheet's corner gutter, which the fight
// frame sets to nothing.
export function scoreSheetPins({rowMarker = false, place = true, total = "var(--total-col)"}: {
  rowMarker?: boolean;
  place?: boolean;
  total?: string;
} = {}): Pins {
  return declarePins([
    ...(rowMarker ? [{key: "marker", width: "var(--row-marker-col)"}] : []),
    {key: "name", width: "var(--team-col)"},
    {key: "total", width: total},
    ...(place ? [{key: "place", width: "var(--place-col)"}, {key: "place-gap", width: "var(--place-gap)"}] : []),
  ], {start: "var(--sheet-corner-col)"});
}

// The leading columns every score table has, each with the class that sizes it
// and the class a cell gets when the page passes no class of its own.
const LEADING = {
  marker: {column: "row-marker", head: "row-marker-head", cell: ""},
  name: {column: "col-name", head: "battle", cell: "team-name"},
  total: {column: "col-total", head: "number", cell: "number total-cell"},
  place: {column: "col-place", head: "number", cell: "number place-cell"},
  "place-gap": {column: "", head: "place-gap-head", cell: "place-gap"},
} as const;

type LeadingKey = keyof typeof LEADING;

// leadingCell builds one of those cells from the page's spec and pins it when
// the sheet's declaration says so.
function leadingCell(tag: "th" | "td", key: LeadingKey, spec: CellSpec, pins: Pins, className?: string, rowSpan = 1): Node {
  const own = LEADING[key];
  const node = cellFromSpec(tag, spec, {
    className: className || (tag === "th" ? own.head : own.cell),
    attrs: rowSpan > 1 ? {rowSpan} : undefined,
  });
  if (!(node instanceof Element)) return node;
  if (own.column) node.classList.add(own.column);
  return pins.mark(node, key);
}

export function buildFlatScoreTable(options: ScoreTableOptions): HTMLTableElement {
  const table = document.createElement("table");
  table.className = options.className || "match-table compact-score-table";
  applyAttrs(table, options.attrs);
  for (const [eventName, handler] of Object.entries(options.events || {})) {
    table.addEventListener(eventName, handler);
  }

  const themes = options.themes || [];
  const afterThemeHeaders = options.afterThemeHeaders || [];
  const showPlaceColumn = options.placeColumn !== false;
  const showRowMarker = Boolean(options.rowMarkerColumn);
  const header = document.createElement("tr");
  const pins = options.pins ?? scoreSheetPins({rowMarker: showRowMarker, place: showPlaceColumn});
  if (showRowMarker) {
    header.appendChild(leadingCell("th", "marker", options.rowMarkerHeader ?? "", pins, options.rowMarkerHeaderClassName));
  }
  header.appendChild(leadingCell("th", "name", options.nameHeader, pins));
  header.appendChild(leadingCell("th", "total", options.totalHeader ?? "Σ", pins));
  if (showPlaceColumn) {
    header.appendChild(leadingCell("th", "place", options.placeHeader ?? S.widgets.scoreTable.place(), pins));
    header.appendChild(leadingCell("th", "place-gap", options.placeGapHeader ?? "", pins));
  }

  for (const theme of themes) {
    const questionClass = theme.questionClassName || options.questionClassName || "question-head";
    for (const label of theme.questionLabels || []) {
      header.appendChild(th(label, questionClass));
    }
    header.appendChild(th(theme.label ?? "", theme.labelClassName || options.themeHeaderClassName || "theme-head"));
    header.appendChild(th("", theme.gapHeaderClassName || options.gapHeaderClassName || "gap-head"));
  }
  for (const headerCell of afterThemeHeaders) {
    header.appendChild(cellFromSpec("th", headerCell));
  }
  table.appendChild(sheetHead([...(options.headRowsAbove ?? []), {row: header}]));

  const tbody = document.createElement("tbody");
  const rows = options.rows || [];
  const leadingColumnCount = (showRowMarker ? 1 : 0) + (showPlaceColumn ? 4 : 2);
  const colSpan = options.gapColSpan || leadingColumnCount +
    themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0) +
    afterThemeHeaders.length;
  rows.forEach((rowSpec, rowIndex) => {
    const row = document.createElement("tr");
    if (rowSpec.rowClassName) row.className = rowSpec.rowClassName;
    if (showRowMarker) {
      row.appendChild(leadingCell("td", "marker", rowSpec.rowMarkerCell ?? "", pins, rowSpec.rowMarkerClassName || options.rowMarkerCellClassName));
    }
    row.appendChild(leadingCell("td", "name", rowSpec.nameCell, pins));
    row.appendChild(leadingCell("td", "total", rowSpec.totalCell ?? rowSpec.total, pins));
    if (showPlaceColumn) {
      row.appendChild(leadingCell("td", "place", rowSpec.placeCell ?? rowSpec.place, pins));
      row.appendChild(leadingCell("td", "place-gap", rowSpec.placeGapCell ?? "", pins));
    }

    (rowSpec.themes || []).forEach((themeSpec, themeIndex) => {
      for (const answerCell of themeSpec.answers || []) {
        row.appendChild(cellFromSpec("td", answerCell, {className: "answer-cell theme-block"}));
      }
      row.appendChild(cellFromSpec("td", themeSpec.scoreCell ?? themeSpec.score, {
        className: "number theme-score theme-block theme-block-score",
      }));
      const theme: ScoreTableTheme = themes[themeIndex] || {};
      row.appendChild(cellFromSpec("td", themeSpec.gapCell ?? "", {
        className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
      }));
    });
    for (const extraCell of rowSpec.afterThemeCells || []) {
      row.appendChild(cellFromSpec("td", extraCell));
    }
    tbody.appendChild(row);

    if (options.gapRows !== false && rowIndex < rows.length - 1) {
      const gapRow = document.createElement("tr");
      if (options.gapRowClassName) gapRow.className = options.gapRowClassName;
      gapRow.appendChild(td("", options.gapCellClassName || "team-gap", {colSpan}));
      tbody.appendChild(gapRow);
    }
  });
  table.appendChild(tbody);
  return table;
}

export function buildTwoRowScoreTable(options: ScoreTableOptions): HTMLTableElement {
  const table = document.createElement("table");
  table.className = options.className || "match-table";
  applyAttrs(table, options.attrs);
  for (const [eventName, handler] of Object.entries(options.events || {})) {
    table.addEventListener(eventName, handler);
  }

  const themes = options.themes || [];
  const afterThemeHeaders = options.afterThemeHeaders || [];
  const showPlaceColumn = options.placeColumn !== false;
  const showRowMarker = Boolean(options.rowMarkerColumn);
  const header = document.createElement("tr");
  const pins = options.pins ?? scoreSheetPins({rowMarker: showRowMarker, place: showPlaceColumn});
  if (showRowMarker) {
    header.appendChild(leadingCell("th", "marker", options.rowMarkerHeader ?? "", pins, options.rowMarkerHeaderClassName));
  }
  header.appendChild(leadingCell("th", "name", options.nameHeader, pins));
  header.appendChild(leadingCell("th", "total", options.totalHeader ?? "Σ", pins));
  if (showPlaceColumn) {
    header.appendChild(leadingCell("th", "place", options.placeHeader ?? S.widgets.scoreTable.place(), pins));
    header.appendChild(leadingCell("th", "place-gap", options.placeGapHeader ?? "", pins));
  }

  for (const theme of themes) {
    const questionClass = theme.questionClassName || options.questionClassName || "question-head";
    for (const label of theme.questionLabels || []) {
      header.appendChild(th(label, questionClass));
    }
    header.appendChild(th(theme.label ?? "", theme.labelClassName || options.themeHeaderClassName || "theme-head"));
    header.appendChild(th("", theme.gapHeaderClassName || options.gapHeaderClassName || "gap-head"));
  }
  for (const headerCell of afterThemeHeaders) {
    header.appendChild(cellFromSpec("th", headerCell));
  }
  table.appendChild(sheetHead([...(options.headRowsAbove ?? []), {row: header}]));

  const tbody = document.createElement("tbody");
  const leadingColumnCount = (showRowMarker ? 1 : 0) + (showPlaceColumn ? 4 : 2);
  const colSpan = options.gapColSpan || leadingColumnCount +
    themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0) +
    afterThemeHeaders.length;
  const rows = options.rows || [];
  rows.forEach((rowSpec, rowIndex) => {
    const topRow = document.createElement("tr");
    const answerRow = document.createElement("tr");
    const rowClassName = rowSpec.rowClassName || "";
    if (rowClassName) topRow.className = rowClassName;
    answerRow.className = [
      rowSpec.answerRowClassName || options.answerRowClassName || "answer-row",
      rowClassName,
    ].filter(Boolean).join(" ");

    if (showRowMarker) {
      topRow.appendChild(leadingCell("td", "marker", rowSpec.rowMarkerCell ?? "", pins, rowSpec.rowMarkerClassName || options.rowMarkerCellClassName, 2));
    }
    topRow.appendChild(leadingCell("td", "name", rowSpec.nameCell, pins, undefined, 2));
    topRow.appendChild(leadingCell("td", "total", rowSpec.totalCell ?? rowSpec.total, pins, undefined, 2));
    if (showPlaceColumn) {
      topRow.appendChild(leadingCell("td", "place", rowSpec.placeCell ?? rowSpec.place, pins, undefined, 2));
      topRow.appendChild(leadingCell("td", "place-gap", rowSpec.placeGapCell ?? "", pins, undefined, 2));
    }

    (rowSpec.themes || []).forEach((themeSpec, themeIndex) => {
      const theme: ScoreTableTheme = themes[themeIndex] || {};
      const questionCount = theme.questionLabels?.length || 0;
      topRow.appendChild(cellFromSpec("td", themeSpec.playerCell ?? "", {
        className: "player-cell theme-block theme-block-top-left",
        attrs: {colSpan: questionCount},
      }));
      topRow.appendChild(cellFromSpec("td", themeSpec.scoreCell ?? themeSpec.score, {
        className: "number theme-score theme-block theme-block-score",
        attrs: {rowSpan: 2},
      }));
      topRow.appendChild(cellFromSpec("td", themeSpec.gapCell ?? "", {
        className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
      }));

      for (const answerCell of themeSpec.answers || []) {
        answerRow.appendChild(cellFromSpec("td", answerCell, {className: "answer-cell theme-block"}));
      }
      answerRow.appendChild(cellFromSpec("td", themeSpec.answerGapCell ?? "", {
        className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
      }));
    });

    for (const extraCell of rowSpec.afterThemeCells || []) {
      topRow.appendChild(cellFromSpec("td", extraCell));
    }

    tbody.appendChild(topRow);
    tbody.appendChild(answerRow);
    if (options.gapRows !== false && rowIndex < rows.length - 1) {
      const gapRow = document.createElement("tr");
      if (options.gapRowClassName) gapRow.className = options.gapRowClassName;
      gapRow.appendChild(td("", options.gapCellClassName || "team-gap", {colSpan}));
      tbody.appendChild(gapRow);
    }
  });
  table.appendChild(tbody);
  return table;
}

export interface ComputePlacesOptions {
  tiebreaks?: readonly unknown[] | null;
  compareTiebreak?: ((a: unknown, b: unknown) => number) | null;
}

// computePlaces ranks teams by descending total, labeling ties with a "lo–hi"
// range (e.g. two teams sharing 2nd both read "2–3"). Pass opts.tiebreaks (a
// parallel array) plus opts.compareTiebreak(a, b) — returning >0 when `a` ranks
// below `b` — to split equal totals, as OD does with its shootout result: two
// teams stay tied only when both total AND tiebreak match. With no comparator
// it degrades to a pure total-based ranking (EK/KSI).
export function computePlaces(totals: readonly number[], opts: ComputePlacesOptions = {}): string[] {
  const {tiebreaks = null, compareTiebreak = null} = opts;
  const tiebreakOf = (index: number) => (tiebreaks ? tiebreaks[index] : null);
  const tied = (a: number, b: number) => !compareTiebreak || compareTiebreak(tiebreakOf(a), tiebreakOf(b)) === 0;
  const sorted = totals
    .map((total, index) => ({total, index}))
    .sort((a, b) => {
      if (b.total !== a.total) return b.total - a.total;
      return compareTiebreak ? compareTiebreak(tiebreakOf(a.index), tiebreakOf(b.index)) : 0;
    });
  const places = new Array<string>(totals.length).fill("");
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total && tied(sorted[j + 1].index, sorted[i].index)) j++;
    const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) places[sorted[k].index] = label;
    i = j + 1;
  }
  return places;
}

// The node index: a built table's live cells, found by the data-* keys they
// carry, so a page updates one cell without searching the table (KSI's
// sheet). Each spec says how to find a kind of cell: its selector and keys.
export interface NodeIndexSpec {
  name: string;
  selector: string;
  keys: string[];
}

export interface NodeIndex {
  get(name: string, values?: Record<string, unknown>): HTMLElement | null;
}

export function createNodeIndex(root: ParentNode, specs: NodeIndexSpec[] | null | undefined): NodeIndex {
  const maps = new Map<string, {keys: string[]; map: Map<string, HTMLElement>}>();
  for (const spec of specs || []) {
    const map = new Map<string, HTMLElement>();
    root.querySelectorAll<HTMLElement>(spec.selector).forEach((node) => {
      map.set(indexKeyFromDataset(node.dataset, spec.keys), node);
    });
    maps.set(spec.name, {keys: spec.keys, map});
  }
  return {
    get(name, values = {}) {
      const entry = maps.get(name);
      if (!entry) return null;
      return entry.map.get(indexKeyFromValues(values, entry.keys)) || null;
    },
  };
}

// createScoreTableIndex indexes a score table's live cells: a mark, a theme's
// score, a row's Σ and place, each keyed by its row (entity: "team" or
// "player") and, inside a row, by its theme and answer.
export function createScoreTableIndex(root: ParentNode, options: {entity?: string} = {}): NodeIndex {
  const rowKeys = [options.entity || "team"];
  const themeKeys = rowKeys.concat(["theme"]);
  return createNodeIndex(root, [
    {name: "answer", selector: ".answer-cell", keys: themeKeys.concat(["answer"])},
    {name: "themeScore", selector: ".theme-score", keys: themeKeys},
    {name: "total", selector: ".total-cell", keys: rowKeys},
    {name: "place", selector: ".place-cell", keys: rowKeys},
  ]);
}

function indexKeyFromDataset(dataset: DOMStringMap, keys: string[]): string {
  const values: Record<string, string | undefined> = {};
  for (const key of keys) values[key] = dataset[key];
  return indexKeyFromValues(values, keys);
}

function indexKeyFromValues(values: Record<string, unknown>, keys: string[]): string {
  return keys.map((key) => String(values[key] ?? "")).join("\u001f");
}

export function setNodeText(node: Element | null | undefined, value: unknown, formatter: (value: unknown) => string = formatDisplayText): void {
  if (!node) return;
  const text = formatter(value);
  if (node.textContent !== text) node.textContent = text;
}

export function setMarkClass(node: Element | null | undefined, mark: string | null | undefined): void {
  if (!node) return;
  node.classList.remove("right", "wrong");
  if (mark) node.classList.add(mark);
}
