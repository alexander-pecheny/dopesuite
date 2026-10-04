// A match's score table — built once (flat or two-row) and then patched in place
// from a MatchView through a node index keyed by the cells' dataset.

import {applyAttrs, cellFromSpec, formatDisplayText, formatPlace, sameArray, td, th} from "./cells.js";
import type {CellAttrs, CellContent, CellSpec} from "./cells.js";
import {seatedNames, seatingLabel} from "./ek-seating.js";
import {SEAT_PICKER_SELECTOR, seatPickerOf} from "./seat-picker.js";
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

export interface ThemeView {
  score?: number | string | null;
  // Whoever the team sent to the theme — one player in EK, up to three in
  // Erudit-Sextet. The order carries no meaning.
  players?: Array<string | null | undefined> | null;
  answers?: Array<string | null | undefined>;
}

export interface ParticipantView {
  name?: string;
  // The team's roster, which is what a seating is cut against.
  roster?: Array<{name?: string}>;
  total?: number | string | null;
  plus?: number | string | null;
  tiebreak?: number | string | null;
  shootoutTotal?: number | string | null;
  place?: number;
  correctCounts?: number[];
  themes?: ThemeView[];
  shootoutThemes?: ThemeView[];
}

export interface MatchView {
  code?: string;
  finished?: boolean;
  questionValues?: unknown[];
  // How many players a team may seat on one theme — one in EK, up to three in
  // Erudit-Sextet. Absent means one.
  players?: number;
  participants?: ParticipantView[];
}

export interface PatchScoreTableOptions {
  formatNumber?: (value: unknown) => string;
}

export interface NodeIndexSpec {
  name: string;
  selector: string;
  keys: string[];
  sync?: (node: HTMLElement, matchState: MatchView, opts: PatchScoreTableOptions) => void;
}

export interface NodeIndex {
  specs: NodeIndexSpec[];
  get(name: string, values?: Record<string, unknown>): HTMLElement | null;
  eachNode(name: string, cb: (node: HTMLElement) => void): void;
}

export function createNodeIndex(root: ParentNode, specs: NodeIndexSpec[] | null | undefined): NodeIndex {
  const list = specs || [];
  const maps = new Map<string, {keys: string[]; map: Map<string, HTMLElement>}>();
  for (const spec of list) {
    const map = new Map<string, HTMLElement>();
    root.querySelectorAll<HTMLElement>(spec.selector).forEach((node) => {
      map.set(indexKeyFromDataset(node.dataset, spec.keys), node);
    });
    maps.set(spec.name, {keys: spec.keys, map});
  }
  return {
    // specs is retained so patchScoreTable can drive the sync from the same
    // single source of truth used to build the index.
    specs: list,
    get(name, values = {}) {
      const entry = maps.get(name);
      if (!entry) return null;
      return entry.map.get(indexKeyFromValues(values, entry.keys)) || null;
    },
    eachNode(name, cb) {
      const entry = maps.get(name);
      if (!entry) return;
      entry.map.forEach((node) => cb(node));
    },
  };
}

export interface ScoreCellSpecsOptions {
  entity?: string;
  matchScoped?: boolean;
  shootout?: boolean;
}

export interface ScoreTableIndexOptions extends ScoreCellSpecsOptions {
  extraSpecs?: NodeIndexSpec[];
}

export function createScoreTableIndex(root: ParentNode, options: ScoreTableIndexOptions = {}): NodeIndex {
  return createNodeIndex(root, scoreCellSpecs(options).concat(options.extraSpecs || []));
}

// scoreTeamOf / scoreThemeOf resolve the MatchView participant / theme a built cell
// refers to, straight from the cell's own data-* coordinates — so a sync needs
// nothing but the node and the new state.
function scoreTeamOf(node: HTMLElement, matchState: MatchView): ParticipantView | null {
  return (matchState.participants || [])[Number(node.dataset.team)] || null;
}

// seatingText is a theme's seating as a cell prints it: the one player's whole
// name where a theme seats one (EK, and personal SI on its borrowed page), and
// the surnames where it seats more — three full names never fit five columns.
export function seatingText(theme: ThemeView, matchState: MatchView): string {
  const seated = seatedNames(theme.players);
  if ((matchState.players || 1) <= 1) return seated[0] || "";
  return seatingLabel(seated);
}

function scoreThemeOf(node: HTMLElement, matchState: MatchView): ThemeView | null {
  const team = scoreTeamOf(node, matchState);
  if (!team) return null;
  const themes = node.dataset.shootout === "1" ? team.shootoutThemes : team.themes;
  return (themes || [])[Number(node.dataset.theme)] || null;
}

// scoreCellSpecs is the SINGLE source of truth for the score table's live
// cells. Each entry says how to find the cell (selector + dataset keys, used to
// build the index) AND how to keep it in step with a MatchView (sync, used by
// patchScoreTable). Adding a new live cell means adding one entry here —
// indexing and the in-place patch both pick it up, so no cell can be rendered
// but silently left un-synced (the bug this replaced). A spec without a sync is
// index-only: its value change is handled by a full rebuild (place medals) or
// it is host-managed out of band (venue input).
export function scoreCellSpecs(options: ScoreCellSpecsOptions = {}): NodeIndexSpec[] {
  const entity = options.entity || "team";
  const prefix = options.matchScoped ? ["matchCode"] : [];
  const teamKeys = prefix.concat([entity]);
  const themeKeys = teamKeys.concat(options.shootout ? ["shootout"] : [], ["theme"]);
  return [
    {name: "answer", selector: ".answer-cell", keys: themeKeys.concat(["answer"]),
      sync: (node, ms) => {
        const theme = scoreThemeOf(node, ms);
        if (theme) setMarkClass(node, (theme.answers || [])[Number(node.dataset.answer)]);
      }},
    {name: "themeScore", selector: ".theme-score", keys: themeKeys,
      sync: (node, ms, o) => {
        const theme = scoreThemeOf(node, ms);
        if (theme) setNodeText(node, theme.score, o.formatNumber);
      }},
    // The per-round player shows as read-only text on the viewer and as an
    // editable <select> on the host; each surface has its own spec so both stay
    // live. (Before, only the host's select was patched — the viewer's text was
    // forgotten, so player changes never reached spectators.)
    {name: "playerText", selector: ".readonly-player .name-cell-text", keys: themeKeys,
      sync: (node, ms) => {
        const theme = scoreThemeOf(node, ms);
        if (!theme) return;
        const text = seatingText(theme, ms);
        setNodeText(node, text);
        node.setAttribute("aria-label", text);
        const popover = node.closest(".readonly-player")?.querySelector(".popover-inline");
        if (popover) setNodeText(popover, seatedNames(theme.players).join("\n"));
      }},
    // The host's seat picker, one seat or several (Erudit-Sextet): the
    // picker shows the seating it is told and keeps its own line and popover.
    {name: "seatPicker", selector: SEAT_PICKER_SELECTOR, keys: themeKeys,
      sync: (node, ms) => {
        const theme = scoreThemeOf(node, ms);
        if (theme) seatPickerOf(node)?.update(seatedNames(theme.players));
      }},
    {name: "total", selector: ".total-cell", keys: teamKeys,
      sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.total, o.formatNumber); }},
    {name: "plus", selector: ".plus-cell", keys: teamKeys,
      sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.plus, o.formatNumber); }},
    {name: "tiebreak", selector: ".tiebreak-cell", keys: teamKeys,
      sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.shootoutTotal ?? t.tiebreak, o.formatNumber); }},
    {name: "correctCount", selector: ".correct-count-cell", keys: teamKeys.concat(["valueIndex"]),
      sync: (node, ms, o) => {
        const t = scoreTeamOf(node, ms);
        // Columns render reversed: cell valueIndex i shows correctCounts[4 - i].
        if (t) setNodeText(node, (t.correctCounts || [])[4 - Number(node.dataset.valueIndex)], o.formatNumber);
      }},
    {name: "placeInput", selector: ".place-input", keys: teamKeys,
      sync: (node, ms) => {
        const input = node as HTMLInputElement;
        const t = scoreTeamOf(input, ms);
        if (!t) return;
        if (document.activeElement !== input) input.value = formatPlace(t.place);
        input.dataset.committedPlace = String(t.place || 0);
      }},
    // Index-only (no sync): place restyles medal classes and the viewer renders
    // it as text, so a place change forces a rebuild; venue input is host-managed.
    {name: "place", selector: ".place-cell", keys: teamKeys},
    {name: "input", selector: ".venue-input", keys: teamKeys},
  ];
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

// canPatchScoreShape reports whether `next` can be patched into a table built
// for `previous` without a rebuild — i.e. the table SHAPE (team/theme counts,
// team names, finished flag, question values) is unchanged and only cell
// VALUES (scores, marks, players, places) differ. Callers add their own extra
// gates (title, venue, place) for fields their table renders structurally.
// Shared by the host (editable) and viewer (read-only) so a live edit patches
// in place instead of tearing down and rebuilding the whole battle.
export function canPatchScoreShape(previous: MatchView | null | undefined, next: MatchView | null | undefined): boolean {
  if (!previous || !next) return false;
  if (previous.code !== next.code || previous.finished !== next.finished) return false;
  if (!sameArray(previous.questionValues, next.questionValues)) return false;
  const prevTeams = previous.participants || [];
  const nextTeams = next.participants || [];
  if (prevTeams.length !== nextTeams.length) return false;
  for (let i = 0; i < nextTeams.length; i++) {
    if (prevTeams[i].name !== nextTeams[i].name) return false;
    if ((prevTeams[i].themes || []).length !== (nextTeams[i].themes || []).length) return false;
    if ((prevTeams[i].shootoutThemes || []).length !== (nextTeams[i].shootoutThemes || []).length) return false;
  }
  return true;
}

// patchScoreTable updates a built score table in place from a MatchView. It is
// data-driven: for every spec that declares a `sync` (see scoreCellSpecs), it
// runs that sync over each indexed cell of that type, each cell reading its own
// data-* coordinates. Shared verbatim by the host and viewer — whatever cells
// their tables contain get patched. opts.formatNumber formats numeric text.
export function patchScoreTable(index: NodeIndex | null | undefined, matchState: MatchView | null | undefined, opts: PatchScoreTableOptions = {}): void {
  if (!index || !matchState) return;
  const state = matchState;
  for (const spec of index.specs || []) {
    const sync = spec.sync;
    if (!sync) continue;
    index.eachNode(spec.name, (node) => sync(node, state, opts));
  }
}
