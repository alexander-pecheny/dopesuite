// dope's core shared frontend library (ADR-0001): table builders, cell helpers,
// floating popovers, virtual keypads. Converted from the legacy match-table.js
// IIFE; consumers import DopeTable (or the named exports) instead of reading
// window.DopeTable. The SSE state-sync engine lives in state-sync.ts and is
// re-exported here so existing consumers keep one import.

export * from "./state-sync.js";
export * from "./game-page.js";
export * from "./widgets.js";
import {
  applyDeltaOps,
  createClientRecorder,
  createEpochTracker,
  createHostPresence,
  createLiveEvents,
  createPendingOps,
  createStateSync,
  gameEventsURL,
  installClientRecorder,
  parseScopedEvent,
  scheduleStaticReload,
} from "./state-sync.js";
import {
  createGameDataLoader,
  createLocalCache,
  fetchGameData,
  mountEditorLink,
  mountGameDownloads,
  mountUnnumberedBanner,
  mountViewerLink,
  notifyEmbeddedResize,
  parseGameRoute,
  renderGameBreadcrumbs,
} from "./game-page.js";
import {
  clamp,
  createCellRangeSelection,
  createFloatingPopover,
  createStatusReporter,
  createTeamNameOverflowController,
  createViewerCounter,
  installCellNavBar,
  installVirtualKeypad,
  markNameOverflow,
} from "./widgets.js";

const MINUS_SIGN = "\u2212";


export type CellContentItem = Node | string | number | boolean | null | undefined;
export type CellContent = CellContentItem | CellContentItem[];

export interface CellAttrs {
  dataset?: Record<string, string | number | boolean>;
  [key: string]: unknown;
}

export interface CellSpecObject {
  node?: Node;
  content?: CellContent;
  className?: string;
  attrs?: CellAttrs | null;
  dataset?: Record<string, string | number | boolean>;
}

export type CellSpec = CellContent | CellSpecObject;

interface CellDefaults {
  className?: string;
  attrs?: CellAttrs | null;
}

export function th(content: CellContent, className = "", attrs: CellAttrs = {}): HTMLElement {
  return cell("th", content, className, attrs);
}

export function td(content: CellContent, className = "", attrs: CellAttrs = {}): HTMLElement {
  return cell("td", content, className, attrs);
}

function cell(tagName: string, content: CellContent, className = "", attrs: CellAttrs | null = {}): HTMLElement {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  setContent(node, content);
  applyAttrs(node, attrs);
  return node;
}

function cellFromSpec(tagName: string, spec: CellSpec, defaults: CellDefaults = {}): Node {
  if (spec instanceof Node) return spec;
  if (typeof spec === "object" && spec !== null && !Array.isArray(spec) && spec.node instanceof Node) return spec.node;
  if (spec === undefined || spec === null || typeof spec !== "object" || Array.isArray(spec)) {
    return cell(tagName, spec ?? "", defaults.className || "", defaults.attrs || {});
  }
  const node = cell(
    tagName,
    Object.prototype.hasOwnProperty.call(spec, "content") ? spec.content : "",
    spec.className ?? defaults.className ?? "",
    spec.attrs || defaults.attrs || {},
  );
  if (spec.dataset) applyDataset(node, spec.dataset);
  return node;
}

function setContent(node: HTMLElement, content: CellContent): void {
  if (content instanceof Node) {
    node.appendChild(content);
    return;
  }
  if (Array.isArray(content)) {
    for (const item of content) {
      if (item instanceof Node) node.appendChild(item);
      else node.appendChild(document.createTextNode(formatDisplayText(item)));
    }
    return;
  }
  node.textContent = formatDisplayText(content);
}

export function formatDisplayText(value: unknown): string {
  return value == null ? "" : String(value).replace(/^-/, MINUS_SIGN);
}

function applyAttrs(node: HTMLElement, attrs: CellAttrs | null = {}): void {
  if (!attrs) return;
  const {dataset, ...rest} = attrs;
  Object.assign(node, rest);
  if (dataset) applyDataset(node, dataset);
}

function applyDataset(node: HTMLElement, dataset: Record<string, string | number | boolean> = {}): void {
  for (const [key, value] of Object.entries(dataset)) {
    node.dataset[key] = String(value);
  }
}

export function option(value: string | number, label: unknown): HTMLOptionElement {
  const node = document.createElement("option");
  node.value = String(value);
  node.textContent = formatDisplayText(label);
  return node;
}

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
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  if (showRowMarker) {
    header.appendChild(cellFromSpec("th", options.rowMarkerHeader ?? "", {
      className: options.rowMarkerHeaderClassName || "sticky row-marker row-marker-head",
    }));
  }
  header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
  header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
  if (showPlaceColumn) {
    header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
    header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));
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
  thead.appendChild(header);
  table.appendChild(thead);

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
      row.appendChild(cellFromSpec("td", rowSpec.rowMarkerCell ?? "", {
        className: rowSpec.rowMarkerClassName || options.rowMarkerCellClassName || "sticky row-marker",
      }));
    }
    row.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name"}));
    row.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell"}));
    if (showPlaceColumn) {
      row.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell"}));
      row.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap"}));
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
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  if (showRowMarker) {
    header.appendChild(cellFromSpec("th", options.rowMarkerHeader ?? "", {
      className: options.rowMarkerHeaderClassName || "sticky row-marker row-marker-head",
    }));
  }
  header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
  header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
  if (showPlaceColumn) {
    header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
    header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));
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
  thead.appendChild(header);
  table.appendChild(thead);

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
      topRow.appendChild(cellFromSpec("td", rowSpec.rowMarkerCell ?? "", {
        className: rowSpec.rowMarkerClassName || options.rowMarkerCellClassName || "sticky row-marker",
        attrs: {rowSpan: 2},
      }));
    }
    topRow.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name", attrs: {rowSpan: 2}}));
    topRow.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell", attrs: {rowSpan: 2}}));
    if (showPlaceColumn) {
      topRow.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell", attrs: {rowSpan: 2}}));
      topRow.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap", attrs: {rowSpan: 2}}));
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

export function setText(root: ParentNode, selector: string, value: unknown, formatter: (value: unknown) => string = formatDisplayText): void {
  const node = root.querySelector(selector);
  if (node) node.textContent = formatter(value);
}

export function isFormControl(target: unknown): boolean {
  return target instanceof HTMLInputElement ||
    target instanceof HTMLSelectElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLButtonElement;
}

export function sameArray(a: unknown, b: unknown): boolean {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export function cssEscape(value: string): string {
  // Cast because lib.dom types CSS.escape as always present; the runtime check
  // guards older browsers where it isn't.
  const escape = (window.CSS as {escape?: (value: string) => string} | undefined)?.escape;
  return escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&");
}

export interface ThemeView {
  score?: number | string | null;
  player?: string | null;
  answers?: Array<string | null | undefined>;
}

export interface TeamView {
  name?: string;
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
  teams?: TeamView[];
}

export interface PatchScoreTableOptions {
  formatNumber?: (value: unknown) => string;
  onPlayerSelectSynced?: (node: HTMLSelectElement) => void;
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

// scoreTeamOf / scoreThemeOf resolve the MatchView team / theme a built cell
// refers to, straight from the cell's own data-* coordinates — so a sync needs
// nothing but the node and the new state.
function scoreTeamOf(node: HTMLElement, matchState: MatchView): TeamView | null {
  return (matchState.teams || [])[Number(node.dataset.team)] || null;
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
    {name: "playerText", selector: ".readonly-player-text", keys: themeKeys,
      sync: (node, ms) => {
        const theme = scoreThemeOf(node, ms);
        if (!theme) return;
        setNodeText(node, theme.player);
        const popover = node.closest(".readonly-player")?.querySelector(".readonly-player-popover");
        if (popover) setNodeText(popover, theme.player);
      }},
    {name: "playerSelect", selector: ".player-select", keys: themeKeys,
      sync: (node, ms, o) => {
        const select = node as HTMLSelectElement;
        const theme = scoreThemeOf(select, ms);
        if (!theme || document.activeElement === select) return; // don't clobber an open select
        const value = theme.player || "";
        if (value && !Array.from(select.options).some((opt) => opt.value === value)) {
          select.appendChild(new Option(value, value));
        }
        if (select.value !== value) select.value = value;
        o.onPlayerSelectSynced?.(select);
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
  const prevTeams = previous.teams || [];
  const nextTeams = next.teams || [];
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
// their tables contain get patched. opts.formatNumber formats numeric text;
// opts.onPlayerSelectSynced lets the host refresh its select's overflow chrome.
export function patchScoreTable(index: NodeIndex | null | undefined, matchState: MatchView | null | undefined, opts: PatchScoreTableOptions = {}): void {
  if (!index || !matchState) return;
  const state = matchState;
  for (const spec of index.specs || []) {
    const sync = spec.sync;
    if (!sync) continue;
    index.eachNode(spec.name, (node) => sync(node, state, opts));
  }
}



export type VenueLike = number | string | {number?: unknown; Number?: unknown; title?: unknown; Title?: unknown} | null | undefined;

export interface Venue {
  number: number;
  title: string;
}

export function normalizeVenue(venue: VenueLike): Venue | null {
  if (!venue) return null;
  if (typeof venue === "number" || typeof venue === "string") {
    const number = Number(venue);
    return Number.isFinite(number) && number > 0 ? {number, title: ""} : null;
  }
  const number = Number(venue.number ?? venue.Number);
  if (!Number.isFinite(number) || number <= 0) return null;
  const title = String(venue.title ?? venue.Title ?? "").trim();
  return {number, title};
}

export function formatVenue(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `${normalized.number}: ${normalized.title}` : String(normalized.number);
}

export function formatBattleVenue(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `пл. ${normalized.number} (${normalized.title})` : `пл. ${normalized.number}`;
}

export function formatBattleVenueShort(venue: VenueLike): string {
  const normalized = normalizeVenue(venue);
  return normalized ? `пл. ${normalized.number}` : "";
}

export function statusLabel(status: string | null | undefined): string {
  if (status === "finished") return "закончен";
  if (status === "pending") return "ожидает";
  return "активен";
}

export function formatNumber(value: unknown): string {
  return Number.isFinite(Number(value)) ? formatDisplayText(value) : "";
}

export function formatPlace(place: number | null | undefined): string {
  return place != null && place > 0 ? String(place) : "";
}

export interface StageRef {
  code: string;
  title?: string;
  stage_type?: string;
  type?: string;
}

export function stageType(stage: {stage_type?: string; type?: string} | null | undefined): string {
  return stage?.stage_type || stage?.type || "";
}

export function stageTabLabel(stage: StageRef): string {
  if (stageType(stage) === "reseed") return "Пересев";
  switch (stage.code) {
  case "r16_run1":
    return "1/16-1";
  case "r16_run2":
    return "1/16-2";
  case "r8":
    return "1/8";
  case "r4":
    return "1/4";
  case "r2":
    return "1/2";
  case "final":
    return "Финал";
  default:
    return stage.title || stage.code;
  }
}

export function teamListCell(teams: Array<{name: string}> | null | undefined): HTMLTableCellElement {
  const cell = document.createElement("td");
  cell.className = "teams-cell";
  (teams || []).forEach((team) => {
    const row = document.createElement("span");
    row.textContent = team.name;
    cell.appendChild(row);
  });
  return cell;
}

export interface VenuesTableOptions {
  editable?: boolean;
  onTitleChange?: (number: number, title: string) => void;
}

export function buildVenuesTable(venues: Venue[] | null | undefined, options: VenuesTableOptions = {}): HTMLElement {
  const editable = Boolean(options.editable);
  const onTitleChange = typeof options.onTitleChange === "function" ? options.onTitleChange : null;
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper venues-results-wrapper";

  const table = document.createElement("table");
  table.className = "results-table venues-results-table";
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th("№", "results-place-head"));
  header.appendChild(th("Название", "results-team-head venues-title-head"));
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const list = venues || [];
  list.forEach((venue, index) => {
    const row = document.createElement("tr");
    row.className = "results-row";
    if (index === 0) row.classList.add("results-group-first");
    if (index === list.length - 1) row.classList.add("results-group-last");
    row.appendChild(td(venue.number, "results-place venues-number"));
    const titleCell = document.createElement("td");
    titleCell.className = "results-team venues-title-cell";
    if (editable && onTitleChange) {
      const input = document.createElement("input");
      input.className = "venue-input";
      input.value = venue.title;
      input.dataset.committedTitle = venue.title;
      input.addEventListener("change", () => {
        const title = input.value.trim();
        if (!title) {
          input.value = input.dataset.committedTitle ?? "";
          return;
        }
        if (title === input.dataset.committedTitle) return;
        input.dataset.committedTitle = title;
        onTitleChange(venue.number, title);
      });
      titleCell.appendChild(input);
    } else {
      titleCell.textContent = venue.title;
    }
    row.appendChild(titleCell);
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  wrapper.appendChild(table);
  return wrapper;
}

export interface RosterPlayer {
  name?: string;
  ratingID?: number;
}

export interface RosterTeam {
  name?: string;
  city?: string;
  number?: number;
  ratingID?: number;
  players?: Array<RosterPlayer | string>;
}

// Roster ("Составы") — the fest-level team→players list, shared by every game
// page (EK/OD/KSI, host and viewer). The data is the same for all games in a
// fest, so it is fetched once per festID and cached for the page's lifetime.
const rosterCache = new Map<string | number, Promise<RosterTeam[]>>();

export function fetchFestRoster(festID: string | number | null | undefined): Promise<RosterTeam[]> {
  if (!festID) return Promise.resolve([]);
  const cached = rosterCache.get(festID);
  if (cached) return cached;
  const promise = fetch(`/api/fest/${encodeURIComponent(festID)}/roster`)
    .then((response) => {
      if (!response.ok) throw new Error(`roster ${response.status}`);
      return response.json();
    })
    .then((data: unknown) => {
      const parsed = data as {teams?: unknown} | null;
      return parsed && Array.isArray(parsed.teams) ? (parsed.teams as RosterTeam[]) : [];
    })
    .catch((err: unknown) => {
      // Don't cache a failure — let a later render retry the fetch.
      rosterCache.delete(festID);
      throw err;
    });
  rosterCache.set(festID, promise);
  return promise;
}

// rating.chgk.info deep links: team/player names in the Составы view link to
// their rating pages when a rating id is known.
const RATING_TEAM_URL = "https://rating.chgk.info/teams/";
const RATING_PLAYER_URL = "https://rating.chgk.info/players/";

// rosterNameNode returns a link to `href` (an external rating page) when one is
// given, otherwise a plain span — both carrying `className` so styling is the
// same whether or not a rating id was available.
function rosterNameNode(text: string, href: string, className: string): HTMLElement {
  if (href) {
    const a = document.createElement("a");
    a.className = `${className} quiet-link`;
    a.href = href;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.textContent = text;
    return a;
  }
  const span = document.createElement("span");
  span.className = className;
  span.textContent = text;
  return span;
}

// buildRosterTable renders the team→players table using the shared results-table
// design-system styling. One row per team: number, name (+ city), player list.
// Team and player names become rating.chgk.info links when a rating id exists.
export function buildRosterTable(teams: RosterTeam[] | null | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper roster-results-wrapper";
  const list = teams || [];
  if (list.length === 0) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = "Составы пока не заданы.";
    wrapper.appendChild(empty);
    return wrapper;
  }

  const hasNumbers = list.some((team) => Number(team.number) > 0);
  const table = document.createElement("table");
  table.className = "results-table roster-results-table";

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  if (hasNumbers) header.appendChild(th("№", "results-place-head"));
  header.appendChild(th("Команда", "results-team-head"));
  header.appendChild(th("Игроки", "roster-players-head"));
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  list.forEach((team, index) => {
    const row = document.createElement("tr");
    row.className = "results-row";
    if (index === 0) row.classList.add("results-group-first");
    if (index === list.length - 1) row.classList.add("results-group-last");

    if (hasNumbers) {
      row.appendChild(td(Number(team.number) > 0 ? team.number : "", "results-place"));
    }

    const teamCell = document.createElement("td");
    teamCell.className = "results-team roster-team-cell";
    const teamHref = Number(team.ratingID) > 0 ? `${RATING_TEAM_URL}${team.ratingID}` : "";
    // Same DOM shape as buildEKStatsTable's name cell, so the shared
    // fade-on-overflow + hover/focus popover (results-team styling) applies to
    // clipped team names — markNameOverflow toggles .results-team-truncated.
    const nameWrap = document.createElement("span");
    nameWrap.className = "results-team-name-wrap";
    const nameEl = rosterNameNode(team.name || "", teamHref, "results-team-name roster-team-name");
    nameEl.tabIndex = 0;
    nameEl.setAttribute("aria-label", team.name || "");
    nameWrap.appendChild(nameEl);
    teamCell.appendChild(nameWrap);
    const namePopover = document.createElement("span");
    namePopover.className = "popover results-team-name-popover";
    namePopover.textContent = team.name || "";
    teamCell.appendChild(namePopover);
    if (team.city) {
      const city = document.createElement("span");
      city.className = "roster-team-city";
      city.textContent = team.city;
      teamCell.appendChild(city);
    }
    row.appendChild(teamCell);

    const playersCell = document.createElement("td");
    playersCell.className = "roster-players-cell";
    const players = Array.isArray(team.players) ? team.players : [];
    if (players.length === 0) {
      playersCell.classList.add("empty");
      playersCell.textContent = "—";
    } else {
      players.forEach((player) => {
        // Tolerate both the current {name, ratingID} shape and a bare string.
        const info: RosterPlayer = typeof player === "string" ? {name: player} : (player || {});
        const chip = document.createElement("span");
        chip.className = "roster-player";
        const href = Number(info.ratingID) > 0 ? `${RATING_PLAYER_URL}${info.ratingID}` : "";
        chip.appendChild(rosterNameNode(info.name || "", href, "roster-player-name"));
        playersCell.appendChild(chip);
      });
    }
    row.appendChild(playersCell);
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  wrapper.appendChild(table);
  return wrapper;
}

// buildRosterView returns a container node for the "Составы" tab that fills
// itself asynchronously: it shows a loading line, fetches the fest roster, then
// swaps in the table (or an error line on failure). Safe to drop straight into
// a tab pane by any page — no roster data needs to be threaded through.
export function buildRosterView(festID: string | number | null | undefined): HTMLElement {
  const container = document.createElement("div");
  container.className = "roster-view";
  const loading = document.createElement("p");
  loading.className = "roster-empty";
  loading.textContent = "Загрузка составов…";
  container.appendChild(loading);

  fetchFestRoster(festID)
    .then((teams) => {
      container.replaceChildren(buildRosterTable(teams));
      // Flag clipped team names so the shared fade + popover kick in, and
      // re-check whenever the container's width changes (tab switch, resize).
      // The popover itself is already handled: the CSS-only variant on OD/KSI,
      // and the page-bound floating popover on the EK host/viewer roots.
      const remeasure = () => markNameOverflow(container, {
        cellSelector: ".roster-team-cell",
        nameSelector: ".results-team-name",
        truncatedClass: "results-team-truncated",
      });
      requestAnimationFrame(remeasure);
      if (typeof ResizeObserver === "function") {
        new ResizeObserver(remeasure).observe(container);
      }
    })
    .catch(() => {
      const error = document.createElement("p");
      error.className = "roster-empty";
      error.textContent = "Не удалось загрузить составы.";
      container.replaceChildren(error);
    });
  return container;
}

export function fitEKStageTeamName(cell: HTMLElement | null | undefined, nameNode: HTMLElement | null | undefined): boolean {
  if (!cell || !nameNode) return false;
  const name = nameNode;
  const baseSize = parseFloat(getComputedStyle(name).fontSize) || 13;
  const minSize = 9;
  const vertOverflows = () => name.scrollHeight > name.clientHeight + 1;
  const horizOverflows = () => name.scrollWidth > name.clientWidth + 1;
  name.style.fontSize = "";
  if (vertOverflows()) {
    let size = Math.floor(baseSize) - 1;
    while (size >= minSize) {
      name.style.fontSize = `${size}px`;
      if (!vertOverflows()) break;
      size -= 1;
    }
    if (size < minSize) name.style.fontSize = `${minSize}px`;
  }
  return vertOverflows() || horizOverflows();
}

export interface EKStage {
  code?: string;
  matches?: MatchView[];
}

export interface EKPlayerStatsRow {
  player: string;
  team: string;
  sum: number;
  plus: number;
  battles: number;
  right: number[];
  wrong: number[];
  rightTotal: number;
  share: number;
}

// computeEKPlayerStats aggregates per-player individual stats across every
// battle of an EK game. `stages` is the payload from /stages/matches:
// [{code, matches: [MatchView, ...]}, ...]. Only regular themes are counted —
// shootout ("перестрелка") themes are a tiebreaker and are excluded, matching
// the Σ+ semantics shown in a battle (TeamView.plus ignores shootouts too).
//
// Players are keyed by (team, player) so namesakes on different teams stay
// separate. The team-share column ("% от команды") is a positive player's
// share among the team's positive contributors (denominator = sum of only the
// positive players' Σ); players with Σ <= 0 are 0 (see the share loop below).
// Returns rows sorted by Σ descending (then Σ+, then name).
export function computeEKPlayerStats(stages: EKStage[] | null | undefined): EKPlayerStatsRow[] {
  const values = [10, 20, 30, 40, 50]; // answer index → nominal value
  const players = new Map<string, EKPlayerStatsRow>();   // key → stat row
  const battleSeen = new Map<string, Set<string>>(); // key → Set of battle ids (for the Бои count)
  for (const stage of stages || []) {
    for (const match of stage.matches || []) {
      const battleID = `${stage.code || ""}\x1f${match.code || ""}`;
      for (const team of match.teams || []) {
        const teamName = team.name || "";
        for (const theme of team.themes || []) {
          const playerName = String(theme.player || "").trim();
          if (!playerName) continue;
          const key = `${teamName}\x1f${playerName}`;
          let row = players.get(key);
          let seen = battleSeen.get(key);
          if (!row || !seen) {
            row = {
              player: playerName,
              team: teamName,
              sum: 0,
              plus: 0,
              battles: 0,
              right: [0, 0, 0, 0, 0],
              wrong: [0, 0, 0, 0, 0],
              rightTotal: 0,
              share: 0,
            };
            players.set(key, row);
            seen = new Set();
            battleSeen.set(key, seen);
          }
          if (!seen.has(battleID)) {
            seen.add(battleID);
            row.battles++;
          }
          const statRow = row;
          (theme.answers || []).forEach((mark, i) => {
            const value = values[i] || 0;
            if (mark === "right") {
              statRow.sum += value;
              statRow.plus += value;
              statRow.right[i]++;
              statRow.rightTotal++;
            } else if (mark === "wrong") {
              statRow.sum -= value;
              statRow.wrong[i]++;
            }
          });
        }
      }
    }
  }
  const rows = Array.from(players.values());
  // "% от команды": a positive player's share among the team's POSITIVE
  // contributors. A player with Σ <= 0 is 0 (they didn't help), and the
  // denominator is the sum of only the positive players' Σ — so the team's
  // positive players' shares add up to 100%, independent of how negative the
  // rest of the team went.
  const teamPositiveSum = new Map<string, number>();
  for (const row of rows) {
    if (row.sum > 0) teamPositiveSum.set(row.team, (teamPositiveSum.get(row.team) || 0) + row.sum);
  }
  for (const row of rows) {
    const total = teamPositiveSum.get(row.team) || 0;
    row.share = row.sum > 0 && total > 0 ? row.sum / total : 0;
  }
  rows.sort((a, b) =>
    b.sum - a.sum ||
    b.plus - a.plus ||
    a.player.localeCompare(b.player, "ru"));
  return rows;
}

// buildEKStatsTable renders the rows from computeEKPlayerStats into the
// "Статистика" table. Columns: Игрок, Команда, Σ, Σ+, Бои, 50/40/30/20/10
// (correct counts, descending nominal), −50…−10 (wrong counts, shown as a
// plain positive count), and the team-share percentage. Counts are always
// shown (including 0). Name cells reuse the results-team truncate+fade+popover
// structure so long names behave like everywhere else. Shared host/viewer.
export function buildEKStatsTable(rows: EKPlayerStatsRow[] | null | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (!rows || rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "Пока нет данных: ни одного ответа не отмечено.";
    wrapper.appendChild(empty);
    return wrapper;
  }

  const table = document.createElement("table");
  table.className = "results-table ek-stats-table";
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("Игрок", "results-team-head ek-stats-name-head ek-stats-player-head"));
  head.appendChild(th("Команда", "results-team-head ek-stats-name-head ek-stats-team-head"));
  head.appendChild(th("Σ", "number ek-stats-sum-head"));
  head.appendChild(th("Σ+", "number"));
  head.appendChild(th("Бои", "number"));
  for (const value of [50, 40, 30, 20, 10]) {
    head.appendChild(th(value, "number narrow"));
  }
  for (const value of [50, 40, 30, 20, 10]) {
    head.appendChild(th(`-${value}`, "number narrow ek-stats-wrong-head"));
  }
  head.appendChild(th("% от команды", "number ek-stats-share-head"));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const nameCell = (text: string, className: string) => {
    const cell = document.createElement("td");
    cell.className = `results-team ek-stats-name ${className}`;
    const wrap = document.createElement("span");
    wrap.className = "results-team-name-wrap";
    const name = document.createElement("span");
    name.className = "results-team-name";
    name.textContent = text;
    name.tabIndex = 0;
    name.setAttribute("aria-label", text);
    wrap.appendChild(name);
    cell.appendChild(wrap);
    const popover = document.createElement("span");
    popover.className = "popover results-team-name-popover";
    popover.textContent = text;
    cell.appendChild(popover);
    return cell;
  };
  rows.forEach((row, index) => {
    const tr = document.createElement("tr");
    tr.className = "results-row";
    if (index === 0) tr.classList.add("results-group-first");
    if (index === rows.length - 1) tr.classList.add("results-group-last");
    tr.appendChild(nameCell(row.player, "ek-stats-player"));
    tr.appendChild(nameCell(row.team, "ek-stats-team"));
    tr.appendChild(td(row.sum, "number ek-stats-sum"));
    tr.appendChild(td(row.plus, "number"));
    tr.appendChild(td(row.battles, "number"));
    for (let i = 4; i >= 0; i--) {
      tr.appendChild(td(row.right[i] || 0, "number narrow"));
    }
    for (let i = 4; i >= 0; i--) {
      tr.appendChild(td(row.wrong[i] || 0, "number narrow ek-stats-wrong"));
    }
    tr.appendChild(td(`${Math.round(row.share * 100)}%`, "number ek-stats-share"));
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  wrapper.appendChild(table);
  return wrapper;
}

export const DopeTable = {
  th,
  td,
  option,
  applyDeltaOps,
  createClientRecorder,
  installClientRecorder,
  createViewerCounter,
  buildFlatScoreTable,
  buildTwoRowScoreTable,
  computePlaces,
  setText,
  formatDisplayText,
  isFormControl,
  clamp,
  sameArray,
  cssEscape,
  createNodeIndex,
  createScoreTableIndex,
  scoreCellSpecs,
  setNodeText,
  setMarkClass,
  canPatchScoreShape,
  patchScoreTable,
  renderGameBreadcrumbs,
  parseScopedEvent,
  createStateSync,
  createLiveEvents,
  createPendingOps,
  createHostPresence,
  normalizeVenue,
  formatVenue,
  formatBattleVenue,
  formatBattleVenueShort,
  statusLabel,
  formatNumber,
  formatPlace,
  stageType,
  stageTabLabel,
  teamListCell,
  buildVenuesTable,
  fetchFestRoster,
  buildRosterTable,
  buildRosterView,
  createFloatingPopover,
  installCellNavBar,
  installVirtualKeypad,
  createStatusReporter,
  mountEditorLink,
  mountViewerLink,
  mountUnnumberedBanner,
  mountGameDownloads,
  parseGameRoute,
  markNameOverflow,
  createLocalCache,
  gameEventsURL,
  createEpochTracker,
  notifyEmbeddedResize,
  scheduleStaticReload,
  fetchGameData,
  createGameDataLoader,
  createTeamNameOverflowController,
  fitEKStageTeamName,
  createCellRangeSelection,
  computeEKPlayerStats,
  buildEKStatsTable,
};
