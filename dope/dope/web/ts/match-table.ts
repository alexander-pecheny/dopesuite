// dope's core shared frontend library (ADR-0001): table builders, cell helpers,
// floating popovers, virtual keypads. Converted from the legacy match-table.js
// IIFE; consumers import DopeTable (or the named exports) instead of reading
// window.DopeTable. The SSE state-sync engine lives in state-sync.ts and is
// re-exported here so existing consumers keep one import.

export * from "./state-sync.js";
import {
  applyDeltaOps,
  createClientRecorder,
  createEpochTracker,
  createLiveEvents,
  createPendingOps,
  createStateSync,
  gameEventsURL,
  installClientRecorder,
  parseScopedEvent,
  scheduleStaticReload,
} from "./state-sync.js";

const MINUS_SIGN = "\u2212";

// Page globals the bundle environment provides (menu.js's dopeMenu, the
// server-inlined __GAME_INIT__). Accessed via a structural cast so this module
// stays importable — and type-checkable — standalone.
export interface DopeMenuJump {
  label: string;
  href: string;
  title?: string;
  external?: boolean;
}

export interface DopeMenuExtra {
  label: string;
  href: string;
  title?: string;
  download?: boolean;
}

interface DopeMenuLike {
  setJump(jump: DopeMenuJump): void;
  setExtras?(items: DopeMenuExtra[]): void;
}

export interface GameInitLike {
  scheme?: unknown;
  state?: unknown;
  fest?: unknown;
  [key: string]: unknown;
}

interface PageGlobals {
  dopeMenu?: DopeMenuLike;
  __GAME_INIT__?: GameInitLike | null;
}

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

export function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
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

export interface GameBreadcrumbsOptions {
  festTitle?: string;
  gameTitle?: string;
  currentTitle?: string;
  festHref?: string;
  gameHref?: string;
}

export function renderGameBreadcrumbs(root: HTMLElement | null | undefined, options: GameBreadcrumbsOptions = {}): void {
  if (!root) return;
  const festTitle = String(options.festTitle || "Фест").trim() || "Фест";
  const gameTitle = String(options.gameTitle || "Игра").trim() || "Игра";
  const currentTitle = String(options.currentTitle || "").trim();
  root.replaceChildren();

  const festLink = document.createElement("a");
  festLink.className = "game-breadcrumbs-fest";
  festLink.href = options.festHref || "/";
  festLink.textContent = festTitle;
  root.appendChild(festLink);

  root.appendChild(breadcrumbSeparator());
  if (options.gameHref && currentTitle && currentTitle !== gameTitle) {
    const gameLink = document.createElement("a");
    gameLink.className = "game-breadcrumbs-game";
    gameLink.href = options.gameHref;
    gameLink.textContent = gameTitle;
    root.appendChild(gameLink);
    root.appendChild(breadcrumbSeparator());
    const current = document.createElement("span");
    current.className = "game-breadcrumbs-current";
    current.textContent = currentTitle;
    root.appendChild(current);
  } else {
    const game = document.createElement("span");
    game.className = "game-breadcrumbs-game";
    game.textContent = currentTitle || gameTitle;
    root.appendChild(game);
  }
}

function breadcrumbSeparator(): HTMLElement {
  const sep = document.createElement("span");
  sep.className = "game-breadcrumbs-sep";
  sep.textContent = "/";
  sep.setAttribute("aria-hidden", "true");
  return sep;
}

export interface HostPresenceOptions {
  root?: HTMLElement;
  eventsURL?: string;
  presenceURL?: string;
  postDelayMs?: number;
  heartbeatMs?: number;
  staleMs?: number;
  cursorFromElement?: (element: Element | EventTarget | null) => unknown;
  getCursor?: () => unknown;
  findTarget?: (cursor: unknown) => Element | null | undefined;
}

export interface HostPresence {
  connect(): void;
  disconnect(): void;
  publish(cursor: unknown): void;
  publishCurrent(): void;
  publishFromElement(element: Element | EventTarget | null): void;
  refresh(): void;
}

interface PresenceMessage {
  userID?: number | string;
  username?: string;
  color?: string;
  active?: boolean;
  cursor?: unknown;
}

interface RemotePresence {
  userID: number | string;
  username: string;
  color: string;
  cursor: unknown;
  seenAt: number;
  node?: HTMLElement;
}

export function createHostPresence(options: HostPresenceOptions): HostPresence {
  const root = options.root || document.body;
  const postDelayMs = typeof options.postDelayMs === "number" && Number.isFinite(options.postDelayMs) ? options.postDelayMs : 80;
  const heartbeatMs = typeof options.heartbeatMs === "number" && Number.isFinite(options.heartbeatMs) ? options.heartbeatMs : 5000;
  const staleMs = typeof options.staleMs === "number" && Number.isFinite(options.staleMs) ? options.staleMs : 16000;
  const remotes = new Map<number | string, RemotePresence>();
  let selfUserID: number | string | null = null;
  let source: EventSource | null = null;
  let layer: HTMLElement | null = null;
  let publishTimer: number | null = null;
  let heartbeatTimer: number | null = null;
  let staleTimer: number | null = null;
  let lastCursor: unknown = null;
  let connected = false;
  let refreshFrame = 0;
  let stickyStyleCache: WeakMap<Element, CSSStyleDeclaration> | null = null;

  function connect(): void {
    if (connected || !options.eventsURL || !options.presenceURL) return;
    connected = true;
    ensureLayer();
    void loadSelf();
    source = new EventSource(options.eventsURL);
    source.addEventListener("presence", (event) => {
      try {
        applyPresence(JSON.parse((event as MessageEvent<string>).data) as PresenceMessage | null);
      } catch (error) {
        console.error(error);
      }
    });
    root.addEventListener("focusin", handleFocusOrClick, true);
    root.addEventListener("click", handleFocusOrClick, true);
    document.addEventListener("keydown", handleKeydown, true);
    document.addEventListener("scroll", scheduleRefresh, {capture: true, passive: true});
    window.addEventListener("scroll", scheduleRefresh, {passive: true});
    window.addEventListener("resize", scheduleRefresh);
    window.addEventListener("beforeunload", sendInactive);
    heartbeatTimer = window.setInterval(() => {
      if (lastCursor) void postPresence(true, lastCursor);
    }, heartbeatMs);
    staleTimer = window.setInterval(pruneStale, 1000);
    publishCurrentSoon();
  }

  async function loadSelf(): Promise<void> {
    try {
      const response = await fetch("/api/auth/me", {headers: {"Accept": "application/json"}});
      if (!response.ok) return;
      const me = await response.json() as {user_id?: number | string; userID?: number | string};
      selfUserID = me.user_id || me.userID || null;
      if (selfUserID && remotes.has(selfUserID)) {
        removeRemote(selfUserID);
      }
    } catch (error) {
      console.error(error);
    }
  }

  function handleFocusOrClick(event: Event): void {
    publishFromElement(event.target);
  }

  function handleKeydown(): void {
    window.requestAnimationFrame(publishCurrent);
  }

  function publishFromElement(element: Element | EventTarget | null): void {
    const cursor = options.cursorFromElement?.(element);
    if (cursor) publish(cursor);
  }

  function publishCurrentSoon(): void {
    window.requestAnimationFrame(publishCurrent);
  }

  function publishCurrent(): void {
    const cursor = options.getCursor?.() || options.cursorFromElement?.(document.activeElement);
    if (cursor) publish(cursor);
  }

  function publish(cursor: unknown): void {
    if (!cursor) return;
    lastCursor = cursor;
    window.clearTimeout(publishTimer ?? undefined);
    publishTimer = window.setTimeout(() => {
      publishTimer = null;
      void postPresence(true, cursor);
    }, postDelayMs);
  }

  async function postPresence(active: boolean, cursor?: unknown): Promise<void> {
    if (!options.presenceURL) return;
    const body = active ? {active: true, cursor} : {active: false};
    try {
      await fetch(options.presenceURL, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(body),
      });
    } catch (error) {
      console.error(error);
    }
  }

  function sendInactive(): void {
    if (!options.presenceURL) return;
    const payload = JSON.stringify({active: false});
    if (navigator.sendBeacon) {
      navigator.sendBeacon(options.presenceURL, new Blob([payload], {type: "application/json"}));
      return;
    }
    void fetch(options.presenceURL, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: payload,
      keepalive: true,
    });
  }

  function applyPresence(message: PresenceMessage | null): void {
    if (!message || !message.userID) return;
    if (selfUserID && message.userID === selfUserID) return;
    if (!message.active || !message.cursor) {
      removeRemote(message.userID);
      return;
    }
    const remote = remotes.get(message.userID) || ({} as RemotePresence);
    remote.userID = message.userID;
    remote.username = message.username || `user-${message.userID}`;
    remote.color = message.color || "var(--blue)";
    remote.cursor = message.cursor;
    remote.seenAt = Date.now();
    remotes.set(message.userID, remote);
    renderRemote(remote);
  }

  function ensureLayer(): HTMLElement {
    if (layer) return layer;
    layer = document.createElement("div");
    layer.className = "collab-cursor-layer";
    document.body.appendChild(layer);
    return layer;
  }

  function renderRemote(remote: RemotePresence): void {
    ensureLayer();
    const target = options.findTarget?.(remote.cursor);
    const node = ensureRemoteNode(remote);
    if (!target || !document.documentElement.contains(target)) {
      node.hidden = true;
      return;
    }
    const rect = target.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) {
      node.hidden = true;
      return;
    }
    if (isHiddenByScrollFrame(target, rect) || isHiddenByStickyLayer(target, rect)) {
      node.hidden = true;
      return;
    }
    node.hidden = false;
    node.style.left = `${Math.round(rect.left)}px`;
    node.style.top = `${Math.round(rect.top)}px`;
    node.style.width = `${Math.round(rect.width)}px`;
    node.style.height = `${Math.round(rect.height)}px`;
    node.style.setProperty("--cursor-color", remote.color);
    const marker = node.querySelector<HTMLElement>(".collab-cursor-marker");
    const label = node.querySelector<HTMLElement>(".collab-cursor-label");
    if (marker) marker.title = remote.username;
    if (label) label.textContent = remote.username;
  }

  function ensureRemoteNode(remote: RemotePresence): HTMLElement {
    if (remote.node) return remote.node;
    const node = document.createElement("div");
    node.className = "collab-cursor";
    const marker = document.createElement("span");
    marker.className = "collab-cursor-marker";
    const label = document.createElement("span");
    label.className = "collab-cursor-label";
    marker.appendChild(label);
    node.appendChild(marker);
    ensureLayer().appendChild(node);
    remote.node = node;
    return node;
  }

  function isHiddenByScrollFrame(target: Element, rect: DOMRect): boolean {
    const frame = target.closest?.(".sheet-frame");
    if (!frame) return false;
    const frameRect = frame.getBoundingClientRect();
    return rect.left < frameRect.left - 1 ||
      rect.right > frameRect.right + 1 ||
      rect.top < frameRect.top - 1 ||
      rect.bottom > frameRect.bottom + 1;
  }

  function isHiddenByStickyLayer(target: Element, rect: DOMRect): boolean {
    const frame = target.closest?.(".sheet-frame");
    if (!frame || target.closest?.(".sticky")) return false;
    const frameRect = frame.getBoundingClientRect();
    let stickyRight = frameRect.left;
    let stickyBottom = frameRect.top;
    const probes = stickyProbes(frame);
    for (const probe of probes) {
      const sticky = probe.node;
      if (sticky === target || sticky.contains(target) || target.contains(sticky)) continue;
      const style = probe.style;
      if (style.position !== "sticky") continue;
      const stickyRect = sticky.getBoundingClientRect();
      if (stickyRect.width <= 0 || stickyRect.height <= 0) continue;
      if (stickyRect.right <= frameRect.left || stickyRect.left >= frameRect.right || stickyRect.bottom <= frameRect.top || stickyRect.top >= frameRect.bottom) continue;

      const overlapsY = stickyRect.top < rect.bottom && stickyRect.bottom > rect.top;
      const isLeftSticky = style.left !== "auto" && stickyRect.left >= frameRect.left - 2 && stickyRect.left < frameRect.right;
      if (overlapsY && isLeftSticky) {
        stickyRight = Math.max(stickyRight, stickyRect.right);
      }

      const overlapsX = stickyRect.left < rect.right && stickyRect.right > rect.left;
      const isTopSticky = style.top !== "auto" && stickyRect.top >= frameRect.top - 2 && stickyRect.top < frameRect.bottom;
      if (overlapsX && isTopSticky) {
        stickyBottom = Math.max(stickyBottom, stickyRect.bottom);
      }
    }
    return rect.left < stickyRight - 1 || rect.top < stickyBottom - 1;
  }

  function scheduleRefresh(): void {
    if (refreshFrame) return;
    refreshFrame = requestAnimationFrame(() => {
      refreshFrame = 0;
      refresh();
    });
  }

  function refresh(): void {
    stickyStyleCache = new WeakMap();
    try {
      for (const remote of remotes.values()) {
        renderRemote(remote);
      }
    } finally {
      stickyStyleCache = null;
    }
  }

  function stickyProbes(frame: Element): Array<{node: Element; style: CSSStyleDeclaration}> {
    const nodes = frame.querySelectorAll(".sticky, thead th");
    const out: Array<{node: Element; style: CSSStyleDeclaration}> = [];
    const cache = stickyStyleCache;
    for (const node of nodes) {
      let style: CSSStyleDeclaration;
      if (cache) {
        const cached = cache.get(node);
        if (cached) {
          style = cached;
        } else {
          style = window.getComputedStyle(node);
          cache.set(node, style);
        }
      } else {
        style = window.getComputedStyle(node);
      }
      out.push({node, style});
    }
    return out;
  }

  function pruneStale(): void {
    const cutoff = Date.now() - staleMs;
    for (const [userID, remote] of remotes.entries()) {
      if (remote.seenAt < cutoff) {
        removeRemote(userID);
      }
    }
  }

  function removeRemote(userID: number | string): void {
    const remote = remotes.get(userID);
    if (remote?.node) remote.node.remove();
    remotes.delete(userID);
  }

  function disconnect(): void {
    if (!connected) return;
    connected = false;
    window.clearTimeout(publishTimer ?? undefined);
    window.clearInterval(heartbeatTimer ?? undefined);
    window.clearInterval(staleTimer ?? undefined);
    if (refreshFrame) {
      cancelAnimationFrame(refreshFrame);
      refreshFrame = 0;
    }
    source?.close();
    source = null;
    sendInactive();
    root.removeEventListener("focusin", handleFocusOrClick, true);
    root.removeEventListener("click", handleFocusOrClick, true);
    document.removeEventListener("keydown", handleKeydown, true);
    document.removeEventListener("scroll", scheduleRefresh, {capture: true});
    window.removeEventListener("scroll", scheduleRefresh);
    window.removeEventListener("resize", scheduleRefresh);
    for (const userID of Array.from(remotes.keys())) removeRemote(userID);
  }

  return {connect, disconnect, publish, publishCurrent, publishFromElement, refresh: scheduleRefresh};
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

export interface CellNavBarOptions {
  onPrev?: () => void;
  onNext?: () => void;
  prevLabel?: string;
  nextLabel?: string;
}

export interface CellNavBar {
  show(): void;
  hide(): void;
}

// installCellNavBar mounts a floating ↑/↓ bar pinned just above the on-screen
// keyboard for advancing between editable cells. Mobile numeric keypads
// (inputmode=numeric/decimal) have no Return key on iOS, so this is the only
// way to step cell-to-cell without dismissing the keypad. Rendered only on
// coarse-pointer (touch) devices — on desktop, Enter/Tab already do this.
//
// The caller drives visibility with show()/hide(); buttons fire onPrev/onNext
// on `pointerdown` with the default prevented, so the focused input is never
// blurred and the keyboard stays up while we programmatically move focus.
export function installCellNavBar(options: CellNavBarOptions = {}): CellNavBar {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}};

  const {onPrev, onNext, prevLabel = "▲", nextLabel = "▼"} = options;
  const bar = document.createElement("div");
  bar.className = "entry-nav-bar";
  bar.hidden = true;
  const make = (label: string, aria: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };
  bar.append(
    make(prevLabel, "Предыдущая ячейка", onPrev),
    make(nextLabel, "Следующая ячейка", onNext),
  );
  document.body.appendChild(bar);

  let visible = false;
  // Pin to the visual viewport's box (see installVirtualKeypad): iOS resolves
  // fixed + right:0 against the document width when the page scrolls
  // horizontally, overflowing the screen and skewing the arrows.
  const position = () => {
    if (!visible) return;
    const vv = window.visualViewport;
    if (vv) {
      bar.style.left = `${Math.round(vv.offsetLeft)}px`;
      bar.style.right = "auto";
      bar.style.width = `${Math.round(vv.width)}px`;
      bar.style.top = `${Math.round(vv.offsetTop + vv.height - bar.offsetHeight)}px`;
      bar.style.bottom = "auto";
    } else {
      bar.style.left = "0px";
      bar.style.right = "0px";
      bar.style.width = "auto";
      bar.style.top = "auto";
      bar.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      visible = true;
      bar.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      visible = false;
      bar.hidden = true;
    },
  };
}

export interface VirtualKeypadOptions {
  onDigit?: (digit: string) => void;
  onBackspace?: () => void;
  onNav?: (dx: number, dy: number) => void;
}

export interface VirtualKeypad {
  show(): void;
  hide(): void;
  visible(): boolean;
  height(): number;
}

// installVirtualKeypad mounts a full on-screen numeric keypad pinned to the
// bottom of the visual viewport. It replaces the OS keyboard for digit-only
// cell entry on touch devices: the host <input> sets inputmode="none" so
// iOS/Android suppress their native keypad (which looks out of place and,
// on iOS, lacks a Return key), and these keys drive the input via callbacks.
// Layout: a navigation row (← ↑ ↓ →) above a 3-column digit pad (1–9, then a
// double-width 0 and ⌫). Rendered only on coarse-pointer devices — on desktop
// the physical keyboard and arrow-key navigation already cover this, so it
// returns no-ops. Buttons fire on `pointerdown` with the default prevented so
// the focused input is never blurred and its caret/selection survive editing.
export function installVirtualKeypad(options: VirtualKeypadOptions = {}): VirtualKeypad {
  const coarse = typeof window.matchMedia === "function" &&
    window.matchMedia("(pointer: coarse)").matches;
  if (!coarse) return {show: () => {}, hide: () => {}, visible: () => false, height: () => 0};

  const {onDigit, onBackspace, onNav} = options;
  const pad = document.createElement("div");
  pad.className = "entry-keypad";
  pad.hidden = true;

  const key = (label: string, aria: string, className: string, handler?: () => void) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = label;
    button.setAttribute("aria-label", aria);
    button.addEventListener("pointerdown", (event) => {
      event.preventDefault();
      handler?.();
    });
    return button;
  };

  const navRow = document.createElement("div");
  navRow.className = "entry-keypad-nav";
  navRow.append(
    key("←", "Предыдущая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(-1, 0)),
    key("↑", "Предыдущая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, -1)),
    key("↓", "Следующая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, 1)),
    key("→", "Следующая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(1, 0)),
  );

  const digits = document.createElement("div");
  digits.className = "entry-keypad-digits";
  for (let n = 1; n <= 9; n++) {
    digits.appendChild(key(String(n), String(n), "entry-keypad-key", () => onDigit?.(String(n))));
  }
  digits.appendChild(key("0", "0", "entry-keypad-key entry-keypad-zero", () => onDigit?.("0")));
  digits.appendChild(key("⌫", "Удалить", "entry-keypad-key entry-keypad-back", () => onBackspace?.()));

  pad.append(navRow, digits);
  document.body.appendChild(pad);

  let isVisible = false;
  // Pin to the visual viewport's box explicitly. iOS Safari resolves
  // position:fixed + right:0 against the document width when the page scrolls
  // horizontally (our entry table is wide), which overflows the screen — so we
  // set left/width/top from visualViewport instead of relying on left/right:0.
  const position = () => {
    if (!isVisible) return;
    const vv = window.visualViewport;
    if (vv) {
      pad.style.left = `${Math.round(vv.offsetLeft)}px`;
      pad.style.right = "auto";
      pad.style.width = `${Math.round(vv.width)}px`;
      pad.style.top = `${Math.round(vv.offsetTop + vv.height - pad.offsetHeight)}px`;
      pad.style.bottom = "auto";
    } else {
      pad.style.left = "0px";
      pad.style.right = "0px";
      pad.style.width = "auto";
      pad.style.top = "auto";
      pad.style.bottom = "0px";
    }
  };
  const vv = window.visualViewport;
  if (vv) {
    vv.addEventListener("resize", position);
    vv.addEventListener("scroll", position);
  }
  return {
    show() {
      isVisible = true;
      pad.hidden = false; // unhide before measuring offsetHeight
      position();
    },
    hide() {
      isVisible = false;
      pad.hidden = true;
    },
    visible: () => isVisible,
    height: () => (isVisible ? pad.offsetHeight : 0),
  };
}

export interface FloatingPopoverSpec {
  trigger: string;
  popover: string;
  anchor: string;
}

export interface FloatingPopoverOptions {
  root?: Element | Document | null;
  specs?: FloatingPopoverSpec[];
}

export interface FloatingPopover {
  bind(): void;
  hide(): void;
  position(): void;
}

export function createFloatingPopover(options: FloatingPopoverOptions): FloatingPopover {
  const root = options.root;
  const specs = options.specs || [];
  if (!root || specs.length === 0) {
    return {bind: () => {}, hide: () => {}, position: () => {}};
  }

  let popoverNode: HTMLElement | null = null;
  let active: {trigger: Element; spec: FloatingPopoverSpec} | null = null;

  function triggerFor(target: EventTarget | null): Element | null {
    if (!(target instanceof Element)) return null;
    for (const spec of specs) {
      const trigger = target.closest(spec.trigger);
      if (trigger && root!.contains(trigger)) return trigger;
    }
    return null;
  }

  function specFor(trigger: Element): FloatingPopoverSpec | null {
    return specs.find((spec) => trigger.matches(spec.trigger)) || null;
  }

  function ensureNode(): HTMLElement {
    if (!popoverNode) {
      popoverNode = document.createElement("div");
      popoverNode.className = "popover floating-name-popover";
      document.body.appendChild(popoverNode);
    }
    return popoverNode;
  }

  function show(trigger: Element): void {
    const spec = specFor(trigger);
    const source = spec ? trigger.querySelector(spec.popover) : null;
    const text = source?.textContent?.trim() || "";
    if (!spec || !text) {
      hide();
      return;
    }
    const popover = ensureNode();
    popover.textContent = text;
    popover.classList.add("visible");
    active = {trigger, spec};
    position();
  }

  function hide(): void {
    if (!popoverNode) return;
    popoverNode.classList.remove("visible", "above");
    popoverNode.textContent = "";
    popoverNode.style.removeProperty("top");
    popoverNode.style.removeProperty("left");
    popoverNode.style.removeProperty("max-width");
    active = null;
  }

  function position(): void {
    if (!active || !popoverNode) return;
    const {trigger, spec} = active;
    if (!document.body.contains(trigger) || !trigger.matches(spec.trigger)) {
      hide();
      return;
    }
    const anchor = trigger.querySelector(spec.anchor) || trigger;
    const rect = anchor.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.top > window.innerHeight) {
      hide();
      return;
    }

    const margin = 8;
    const popover = popoverNode;
    popover.style.maxWidth = `${Math.max(80, Math.min(420, window.innerWidth - margin * 2))}px`;
    popover.style.visibility = "hidden";
    popover.classList.add("visible");

    const width = popover.offsetWidth;
    const height = popover.offsetHeight;
    const maxLeft = Math.max(margin, window.innerWidth - width - margin);
    const left = clamp(rect.left, margin, maxLeft);
    const belowTop = rect.bottom - 2;
    const aboveTop = rect.top - height + 2;
    const shouldOpenUp = belowTop + height > window.innerHeight - margin && rect.top > window.innerHeight - rect.bottom;
    const maxTop = Math.max(margin, window.innerHeight - height - margin);
    const top = clamp(shouldOpenUp ? aboveTop : belowTop, margin, maxTop);

    popover.classList.toggle("above", shouldOpenUp);
    popover.style.left = `${Math.round(left)}px`;
    popover.style.top = `${Math.round(top)}px`;
    popover.style.visibility = "";
  }

  function onPointerOver(event: PointerEvent): void {
    // On touch, pointerover fires while swiping across cells; showing here
    // would pop the popover on every swipe. Touch shows via tap (see onTapEnd).
    if (event.pointerType === "touch") return;
    const trigger = triggerFor(event.target);
    if (!trigger || active?.trigger === trigger) return;
    show(trigger);
  }

  let tapStart: {x: number; y: number} | null = null;
  const TAP_MOVE_THRESHOLD = 10;

  function onTapStart(event: PointerEvent): void {
    if (event.pointerType !== "touch") return;
    tapStart = {x: event.clientX, y: event.clientY};
  }

  function onTapEnd(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !tapStart) return;
    const moved = Math.hypot(event.clientX - tapStart.x, event.clientY - tapStart.y);
    tapStart = null;
    if (moved > TAP_MOVE_THRESHOLD) return; // a swipe, not a tap
    const trigger = triggerFor(event.target);
    if (trigger) {
      if (active?.trigger !== trigger) show(trigger);
    } else {
      hide();
    }
  }

  function onPointerOut(event: PointerEvent): void {
    if (event.pointerType === "touch") return;
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    if (event.relatedTarget instanceof Node && trigger.contains(event.relatedTarget)) return;
    if (!trigger.matches(":focus-within")) hide();
  }

  function onFocusIn(event: FocusEvent): void {
    const trigger = triggerFor(event.target);
    if (trigger) show(trigger);
  }

  function onFocusOut(event: FocusEvent): void {
    const trigger = active?.trigger;
    if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
    window.setTimeout(() => {
      if (!trigger.matches(":focus-within") && !trigger.matches(":hover")) hide();
    }, 0);
  }

  let positionFrame = 0;
  function schedulePosition(): void {
    if (positionFrame) return;
    positionFrame = requestAnimationFrame(() => {
      positionFrame = 0;
      position();
    });
  }

  function onPointerDownOutside(event: PointerEvent): void {
    if (!active || event.pointerType !== "touch") return;
    if (event.target instanceof Node && active.trigger.contains(event.target)) return;
    hide();
  }

  function bind(): void {
    document.documentElement.classList.add("floating-popovers-enabled");
    document.addEventListener("pointerover", onPointerOver);
    document.addEventListener("pointerout", onPointerOut);
    document.addEventListener("focusin", onFocusIn);
    document.addEventListener("focusout", onFocusOut);
    document.addEventListener("pointerdown", onPointerDownOutside, true);
    document.addEventListener("pointerdown", onTapStart, true);
    document.addEventListener("pointerup", onTapEnd, true);
    window.addEventListener("scroll", schedulePosition, {capture: true, passive: true});
  }

  return {bind, hide, position};
}

const SYNC_STATUS_LABELS: Record<string, string> = {
  saved: "Синхронизировано",
  saving: "Синхронизация",
  reconnecting: "Переподключение",
  error: "Ошибка",
};

export function createStatusReporter(statusNode: HTMLElement | null | undefined): (state: string) => void {
  const node = statusNode;
  if (!node) return () => {};
  return function setStatus(state: string) {
    node.dataset.state = state;
    const label = SYNC_STATUS_LABELS[state] || SYNC_STATUS_LABELS.saving;
    node.setAttribute("aria-label", label);
    node.title = label;
  };
}

// The standalone ✏️/👀 icons were folded into the ☰ menu (menu.js).
// These now register the menu's context-aware jump item instead of mounting
// an icon; .refresh() re-points it after SPA navigation. statusNode is kept
// for call-site compatibility but unused.
export function mountEditorLink(): {refresh(): void} {
  const set = () => (window as Window & PageGlobals).dopeMenu?.setJump({
    label: "Редактировать",
    href: editorHrefForCurrentLocation(),
    title: "Открыть в режиме редактирования",
  });
  set();
  return {refresh: set};
}

function editorHrefForCurrentLocation(): string {
  return "/host" + window.location.pathname + window.location.search;
}

// mountUnnumberedBanner shows a sticky notice when the fest has teams without
// numbers. Team number is the universal team identity, so the server blocks
// result editing until every team is numbered (409); this points the host at
// the numbers page. Idempotent — re-mounting is a no-op while the banner is up.
export function mountUnnumberedBanner(festID: string | number | null | undefined): HTMLElement | null {
  if (!festID || document.querySelector(".dope-unnumbered-banner")) return null;
  const bar = document.createElement("div");
  bar.className = "dope-unnumbered-banner";
  Object.assign(bar.style, {
    position: "sticky",
    top: "0",
    zIndex: "2147483600",
    background: "var(--amber-bg)",
    color: "var(--amber-text-strong)",
    font: "13px/1.4 system-ui, sans-serif",
    padding: "8px 12px",
    textAlign: "center",
    borderBottom: "1px solid var(--amber-border)",
  });
  bar.append("Командам не присвоены номера — редактирование результатов заблокировано. ");
  const link = document.createElement("a");
  link.href = `/host/fest/${festID}/numbers`;
  link.textContent = "Присвоить номера";
  Object.assign(link.style, {color: "inherit", fontWeight: "600", textDecoration: "underline"});
  bar.appendChild(link);
  document.body.prepend(bar);
  return bar;
}

export function mountViewerLink(): {refresh(): void} {
  const set = () => (window as Window & PageGlobals).dopeMenu?.setJump({
    label: "Страница зрителя",
    href: viewerHrefForCurrentLocation(),
    title: "Открыть зрительскую страницу",
    external: true,
  });
  set();
  return {refresh: set};
}

function viewerHrefForCurrentLocation(): string {
  const path = window.location.pathname.replace(/^\/host(?=\/|$)/, "");
  return (path || "/") + window.location.search;
}

// mountGameDownloads registers the game's archive download links in the ☰ menu:
// "Скачать XLSX" for everyone, and "Скачать .json.gz" (current state + edit
// history) for hosts only. apiBase is the game's /api/fest/.../games/... base.
export function mountGameDownloads(opts: {apiBase?: string; canEdit?: boolean} | null | undefined): void {
  const apiBase = opts && opts.apiBase;
  const menu = (window as Window & PageGlobals).dopeMenu;
  if (!apiBase || !menu?.setExtras) return;
  const items: DopeMenuExtra[] = [{
    label: "Скачать XLSX",
    href: `${apiBase}/export.xlsx`,
    title: "Скачать таблицу игры в формате XLSX",
    download: true,
  }];
  if (opts.canEdit) {
    items.push({
      label: "Скачать .json.gz",
      href: `${apiBase}/export.json.gz`,
      title: "Скачать текущее состояние игры и историю правок",
      download: true,
    });
  }
  menu.setExtras(items);
}

export interface GameRoute {
  viewer?: boolean;
  festID?: string;
  gameID?: string;
  apiBase?: string;
}

export function parseGameRoute(pathname: string = window.location.pathname): GameRoute {
  const host = pathname.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
  if (host) {
    return {
      viewer: false,
      festID: host[1],
      gameID: host[2],
      apiBase: `/api/fest/${host[1]}/games/${host[2]}`,
    };
  }
  const pub = pathname.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (pub) {
    return {
      viewer: true,
      festID: pub[1],
      gameID: pub[2],
      apiBase: `/api/fest/${pub[1]}/games/${pub[2]}`,
    };
  }
  return {};
}

export interface LocalCache {
  key: string;
  read(): unknown;
  write(value: unknown): void;
}

// createLocalCache wraps one localStorage slot in the read/write-with-try/catch
// discipline every page used to copy verbatim: reads degrade to null and writes
// are silently dropped when storage is unavailable (private mode, quota, disabled
// cookies). The page owns the key string and what JSON shape it stores.
export function createLocalCache(key: string): LocalCache {
  return {
    key,
    read() {
      try {
        const raw = window.localStorage.getItem(key);
        return raw ? JSON.parse(raw) : null;
      } catch (_err) {
        return null;
      }
    },
    write(value) {
      if (value == null) return;
      try {
        window.localStorage.setItem(key, JSON.stringify(value));
      } catch (_err) {
        // ignore (quota / disabled / private mode)
      }
    },
  };
}

// notifyEmbeddedResize posts the current document height to the parent frame so
// an embedding host can size its iframe to the content. A no-op outside an
// embed (no parent, or not the ?embed=1 view).
export function notifyEmbeddedResize(embedded: boolean): void {
  if (!embedded || window.parent === window) return;
  window.requestAnimationFrame(() => {
    window.parent.postMessage({
      type: "dope:resize",
      height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
    }, window.location.origin);
  });
}

export interface GameDataSnapshot {
  scheme: unknown;
  state: unknown;
  fest: unknown;
}

// fetchGameData loads the three cold payloads a game page needs — scheme, state,
// and (when the route is fest-scoped) the fest view — in parallel, throwing the
// server's error text on any non-OK response. The fest fetch is skipped when the
// route carries neither an apiBase nor a festID.
export async function fetchGameData(route: GameRoute): Promise<GameDataSnapshot> {
  const festURL = route.apiBase || (route.festID ? `/api/fest/${route.festID}` : "");
  const [schemeResp, stateResp, festResp] = await Promise.all([
    fetch(`${route.apiBase}/scheme`),
    fetch(`${route.apiBase}/state`),
    festURL ? fetch(festURL) : Promise.resolve(null),
  ]);
  if (!schemeResp.ok) throw new Error(await schemeResp.text());
  if (!stateResp.ok) throw new Error(await stateResp.text());
  if (festResp && !festResp.ok) throw new Error(await festResp.text());
  return {
    scheme: await schemeResp.json(),
    state: await stateResp.json(),
    fest: festResp ? await festResp.json() : null,
  };
}

export type GameDataSource = "init" | "cache" | "fetch";

export interface AdoptedGameSnapshot extends GameDataSnapshot {
  init?: GameInitLike;
}

export interface GameDataLoaderOptions {
  route: GameRoute;
  cachePrefix: string;
  adopt: (snapshot: AdoptedGameSnapshot, source: GameDataSource) => void;
  revalidate?: () => unknown;
}

export interface GameDataLoader {
  load(): Promise<void>;
  cache: LocalCache;
  writeSnapshot(snap: GameDataSnapshot | null | undefined): void;
}

// createGameDataLoader centralizes the stale-while-revalidate hydration flow the
// OD and KSI pages share: render instantly from the server-inlined __GAME_INIT__
// payload, else from the localStorage snapshot, else from a cold parallel fetch;
// and in the first two cases kick a background revalidation. The page supplies
// `adopt(snapshot, source)` — which assigns its own scheme/state/fest closures and
// renders — and an optional `revalidate()`. `source` is "init" | "cache" | "fetch";
// on "init" the snapshot also carries the raw `init` payload for seq/epoch/banner
// wiring that only the inlined path has. Returns {load, cache} where `load()`
// mirrors the old loadAll(): it resolves synchronously off init/cache (revalidation
// runs detached) and awaits the network only on a cold start.
export function createGameDataLoader({route, cachePrefix, adopt, revalidate}: GameDataLoaderOptions): GameDataLoader {
  const cache = createLocalCache(`${cachePrefix}:game:${route.festID || ""}:${route.gameID || ""}`);
  function writeSnapshot(snap: GameDataSnapshot | null | undefined): void {
    if (snap && snap.scheme && snap.state) cache.write({scheme: snap.scheme, state: snap.state, fest: snap.fest ?? null});
  }
  function kickRevalidate(): void {
    if (revalidate) Promise.resolve().then(revalidate).catch((error: unknown) => console.error(error));
  }
  async function load(): Promise<void> {
    const init = (window as Window & PageGlobals).__GAME_INIT__;
    if (init && init.scheme && init.state) {
      (window as Window & PageGlobals).__GAME_INIT__ = null;
      const snap = {scheme: init.scheme, state: init.state, fest: init.fest || null};
      adopt({...snap, init}, "init");
      writeSnapshot(snap);
      kickRevalidate();
      return;
    }
    const cached = cache.read() as {scheme?: unknown; state?: unknown; fest?: unknown} | null;
    if (cached && cached.scheme && cached.state) {
      adopt({scheme: cached.scheme, state: cached.state, fest: cached.fest || null}, "cache");
      kickRevalidate();
      return;
    }
    const fresh = await fetchGameData(route);
    adopt(fresh, "fetch");
    writeSnapshot(fresh);
  }
  return {load, cache, writeSnapshot};
}

export interface NameOverflowConfig {
  cellSelector: string;
  nameSelector: string;
  truncatedClass: string;
  citySelector?: string;
  cityTruncatedClass?: string;
}

// markNameOverflow flags every cell under `root` whose inner name (and optional
// city) is clipped, so the page can show a fade + popover. Reads are batched
// ahead of writes so the measure loop never triggers a reflow mid-pass.
export function markNameOverflow(root: ParentNode | null | undefined, cfg: NameOverflowConfig): void {
  if (!root) return;
  const cells = root.querySelectorAll(cfg.cellSelector);
  const readings = new Array<boolean>(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(cfg.nameSelector);
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle(cfg.truncatedClass, readings[i]);
    if (cfg.citySelector && cfg.cityTruncatedClass) {
      const city = cells[i].querySelector(cfg.citySelector);
      city?.classList.toggle(cfg.cityTruncatedClass, city.scrollWidth > city.clientWidth + 1);
    }
  }
}

export interface TeamNameOverflowController {
  schedule(targetRoot?: ParentNode): void;
  updateDetailed(targetRoot?: ParentNode): void;
  updateResults(targetRoot?: ParentNode): void;
}

export function createTeamNameOverflowController({root, detailed, results}: {
  root: ParentNode;
  detailed: NameOverflowConfig;
  results: NameOverflowConfig;
}): TeamNameOverflowController {
  function updateDetailed(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, detailed);
  }
  function updateResults(targetRoot: ParentNode = root): void {
    markNameOverflow(targetRoot, results);
  }
  let frame = 0;
  function schedule(targetRoot: ParentNode = root): void {
    if (frame) cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      frame = 0;
      updateDetailed(targetRoot);
      updateResults(targetRoot);
    });
  }
  return {schedule, updateDetailed, updateResults};
}

export interface CellCoord {
  row: number;
  col: number;
}

export interface CellRect {
  rowStart: number;
  rowEnd: number;
  colStart: number;
  colEnd: number;
}

export interface CellEdit {
  cell: Element;
  value: unknown;
}

export interface CellRangeClasses {
  selected: string;
  anchor: string;
  top: string;
  bottom: string;
  left: string;
  right: string;
}

export interface CellRangeSelectionOptions {
  root: HTMLElement;
  cellSelector?: string;
  readonly?: boolean | (() => boolean);
  coordOf: (cell: Element) => CellCoord | null | undefined;
  cellAtCoord: (coord: CellCoord | null) => HTMLElement | null | undefined;
  serialize?: (cell: Element | null | undefined) => string;
  parse?: (text: string) => unknown;
  applyValues?: (edits: CellEdit[]) => void;
  onSelectionChange?: (selection: {anchor: CellCoord | null; focus: CellCoord | null; rect: CellRect | null} | null) => void;
  onActiveChange?: (cell: HTMLElement, coord: CellCoord | null) => void;
  // cycle(cell) -> next value (in applyValues' value space) for a touch tap.
  // When provided, tapping a cell on a touch device advances it through its
  // states (e.g. empty → right → wrong → empty), the only way to enter data
  // on mobile where there is no physical +/- keyboard.
  cycle?: ((cell: Element) => unknown) | null;
  classes?: Partial<CellRangeClasses>;
}

export interface CellRangeSelection {
  bind(): void;
  unbind(): void;
  setSelection(anchor: CellCoord | null, focus?: CellCoord | null, opts?: {focus?: boolean; preventScroll?: boolean}): void;
  clearSelection(): void;
  deleteSelected(): boolean;
  selectedCells(): HTMLElement[];
  refresh(): void;
  readonly anchor: CellCoord | null;
  readonly focus: CellCoord | null;
  readonly rect: CellRect | null;
}

export function createCellRangeSelection(options: CellRangeSelectionOptions): CellRangeSelection {
  const {
    root,
    cellSelector = ".answer-cell",
    readonly = () => false,
    coordOf,
    cellAtCoord,
    serialize = (cell) => (cell?.textContent || "").trim(),
    parse = (text) => String(text || "").trim(),
    applyValues,
    onSelectionChange,
    onActiveChange,
    cycle = null,
    classes = {},
  } = options;
  const cls: CellRangeClasses = {
    selected: "cell-selected",
    anchor: "cell-selection-anchor",
    top: "cell-selection-top",
    bottom: "cell-selection-bottom",
    left: "cell-selection-left",
    right: "cell-selection-right",
    ...classes,
  };
  const allClasses = [cls.selected, cls.anchor, cls.top, cls.bottom, cls.left, cls.right];
  const isReadonly = typeof readonly === "function" ? readonly : () => Boolean(readonly);

  let anchor: CellCoord | null = null;
  let focusCoord: CellCoord | null = null;
  let dragState: {anchor: CellCoord; focus: CellCoord} | null = null;
  let suppressNextClick = false;
  let tapStart: {cell: Element; x: number; y: number} | null = null;

  function rect(): CellRect | null {
    if (!anchor || !focusCoord) return null;
    return {
      rowStart: Math.min(anchor.row, focusCoord.row),
      rowEnd: Math.max(anchor.row, focusCoord.row),
      colStart: Math.min(anchor.col, focusCoord.col),
      colEnd: Math.max(anchor.col, focusCoord.col),
    };
  }

  function clearClasses(): void {
    root.querySelectorAll(`${cellSelector}.${cls.selected}, ${cellSelector}.${cls.anchor}`).forEach((cell) => {
      cell.classList.remove(...allClasses);
    });
  }

  function renderClasses(): void {
    clearClasses();
    const r = rect();
    if (!r) return;
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        if (!cell) continue;
        cell.classList.add(cls.selected);
        if (row === r.rowStart) cell.classList.add(cls.top);
        if (row === r.rowEnd) cell.classList.add(cls.bottom);
        if (col === r.colStart) cell.classList.add(cls.left);
        if (col === r.colEnd) cell.classList.add(cls.right);
      }
    }
    const anchorCell = cellAtCoord(anchor);
    if (anchorCell) anchorCell.classList.add(cls.anchor);
  }

  function selectedCells(): HTMLElement[] {
    const out: HTMLElement[] = [];
    const r = rect();
    if (!r) return out;
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        if (cell) out.push(cell);
      }
    }
    return out;
  }

  function setSelection(newAnchor: CellCoord | null, newFocus: CellCoord | null = newAnchor, opts: {focus?: boolean; preventScroll?: boolean} = {}): void {
    anchor = newAnchor ? {row: newAnchor.row, col: newAnchor.col} : null;
    focusCoord = newFocus ? {row: newFocus.row, col: newFocus.col} : null;
    renderClasses();
    onSelectionChange?.({anchor, focus: focusCoord, rect: rect()});
    const focusCell = cellAtCoord(focusCoord);
    if (focusCell) {
      if (opts.focus !== false) focusCell.focus({preventScroll: opts.preventScroll});
      onActiveChange?.(focusCell, focusCoord);
    }
  }

  function clearSelection(): void {
    anchor = null;
    focusCoord = null;
    clearClasses();
    onSelectionChange?.(null);
  }

  function deleteSelected(): boolean {
    if (isReadonly()) return false;
    const cells = selectedCells();
    if (cells.length === 0) return false;
    const empty = parse("");
    const edits: CellEdit[] = [];
    for (const cell of cells) edits.push({cell, value: empty});
    applyValues?.(edits);
    return true;
  }

  function copySelection(event: ClipboardEvent): boolean {
    const r = rect();
    if (!r) return false;
    const lines: string[] = [];
    for (let row = r.rowStart; row <= r.rowEnd; row++) {
      const cols: string[] = [];
      for (let col = r.colStart; col <= r.colEnd; col++) {
        const cell = cellAtCoord({row, col});
        cols.push(cell ? serialize(cell) : "");
      }
      lines.push(cols.join("\t"));
    }
    event.clipboardData?.setData("text/plain", lines.join("\n"));
    event.preventDefault();
    return true;
  }

  function pasteSelection(event: ClipboardEvent): boolean {
    if (isReadonly() || !anchor) return false;
    const text = event.clipboardData?.getData("text/plain") || "";
    if (!text) return false;
    event.preventDefault();
    const normalized = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    const lines = normalized.split("\n");
    if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
    const grid = lines.map((line) => line.split("\t"));
    if (grid.length === 0) return false;
    const r = rect();
    if (!r) return false;
    const startRow = r.rowStart;
    const startCol = r.colStart;
    const edits: CellEdit[] = [];
    let lastRow = startRow;
    let lastCol = startCol;
    for (let rOff = 0; rOff < grid.length; rOff++) {
      const cols = grid[rOff];
      for (let cOff = 0; cOff < cols.length; cOff++) {
        const cell = cellAtCoord({row: startRow + rOff, col: startCol + cOff});
        if (!cell) continue;
        edits.push({cell, value: parse(cols[cOff])});
        lastRow = startRow + rOff;
        lastCol = startCol + cOff;
      }
    }
    if (edits.length > 0) applyValues?.(edits);
    setSelection({row: startRow, col: startCol}, {row: lastRow, col: lastCol}, {focus: true});
    return true;
  }

  function handleMouseDown(event: MouseEvent): void {
    if (event.button !== 0 || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell)) return;
    if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return;
    const coord = coordOf(cell);
    if (!coord) return;
    event.preventDefault();
    suppressNextClick = Boolean(event.shiftKey && anchor);
    const nextAnchor = event.shiftKey && anchor ? anchor : coord;
    setSelection(nextAnchor, coord, {preventScroll: true});
    dragState = {anchor: nextAnchor, focus: coord};
    document.addEventListener("mouseup", handleMouseUp, {once: true});
  }

  function handleMouseUp(): void {
    dragState = null;
  }

  function handleMouseOver(event: MouseEvent): void {
    if (!dragState || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell)) return;
    const coord = coordOf(cell);
    if (!coord) return;
    if (coord.row === dragState.focus.row && coord.col === dragState.focus.col) return;
    dragState.focus = coord;
    setSelection(dragState.anchor, coord, {focus: false});
  }

  function handleClickCapture(event: MouseEvent): void {
    if (suppressNextClick) {
      suppressNextClick = false;
      event.stopPropagation();
    }
  }

  function isEditableTarget(target: EventTarget | null): boolean {
    return target instanceof HTMLInputElement
      || target instanceof HTMLTextAreaElement
      || target instanceof HTMLSelectElement
      || (target instanceof HTMLElement && target.isContentEditable);
  }

  function handleCopy(event: ClipboardEvent): void {
    const target = event.target;
    if (isEditableTarget(target)) return;
    if (!(target instanceof Node && root.contains(target)) && !root.contains(document.activeElement)) return;
    copySelection(event);
  }

  function handlePaste(event: ClipboardEvent): void {
    const target = event.target;
    if (isEditableTarget(target)) return;
    if (!(target instanceof Node && root.contains(target)) && !root.contains(document.activeElement)) return;
    pasteSelection(event);
  }

  // Touch taps cycle a cell's value via `cycle` (see option docs). We track
  // the touch from pointerdown so we can tell a tap from a scroll: if the
  // finger moves more than a few px or lifts off a different cell, it was a
  // scroll and we leave the value alone. Gated on pointerType === "touch", so
  // mouse clicks (desktop select/drag) are unaffected.
  const TAP_MOVE_TOLERANCE = 10;

  function handlePointerDown(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !cycle || isReadonly()) {
      tapStart = null;
      return;
    }
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || !root.contains(cell) || isEditableTarget(target)) {
      tapStart = null;
      return;
    }
    tapStart = {cell, x: event.clientX, y: event.clientY};
  }

  function handlePointerUp(event: PointerEvent): void {
    if (event.pointerType !== "touch" || !cycle) return;
    const start = tapStart;
    tapStart = null;
    if (!start || isReadonly()) return;
    const target = event.target;
    const cell = target instanceof Element ? target.closest(cellSelector) : null;
    if (!cell || cell !== start.cell) return;
    if (Math.abs(event.clientX - start.x) > TAP_MOVE_TOLERANCE
      || Math.abs(event.clientY - start.y) > TAP_MOVE_TOLERANCE) return;
    const value = cycle(cell);
    if (value === undefined || value === null) return;
    applyValues?.([{cell, value}]);
    const coord = coordOf(cell);
    if (coord) setSelection(coord, coord, {focus: false});
  }

  function handlePointerCancel(): void {
    tapStart = null;
  }

  function bind(): void {
    root.addEventListener("mousedown", handleMouseDown);
    root.addEventListener("mouseover", handleMouseOver);
    root.addEventListener("click", handleClickCapture, true);
    root.addEventListener("pointerdown", handlePointerDown);
    root.addEventListener("pointerup", handlePointerUp);
    root.addEventListener("pointercancel", handlePointerCancel);
    document.addEventListener("copy", handleCopy);
    document.addEventListener("paste", handlePaste);
  }

  function unbind(): void {
    root.removeEventListener("mousedown", handleMouseDown);
    root.removeEventListener("mouseover", handleMouseOver);
    root.removeEventListener("click", handleClickCapture, true);
    root.removeEventListener("pointerdown", handlePointerDown);
    root.removeEventListener("pointerup", handlePointerUp);
    root.removeEventListener("pointercancel", handlePointerCancel);
    document.removeEventListener("copy", handleCopy);
    document.removeEventListener("paste", handlePaste);
  }

  return {
    bind,
    unbind,
    setSelection,
    clearSelection,
    deleteSelected,
    selectedCells,
    refresh: renderClasses,
    get anchor() { return anchor; },
    get focus() { return focusCoord; },
    get rect() { return rect(); },
  };
}

// createViewerCounter renders a live "NN👀" concurrent-viewer tally
// immediately to the left of the sync-status tick. The span is created and
// inserted dynamically (no markup change needed) and stays hidden until a
// positive count arrives. setCount is driven by "viewers" SSE events.
export function createViewerCounter(statusNode: HTMLElement | null | undefined): {setCount(count: unknown): void} {
  if (!statusNode || !statusNode.parentElement) {
    return {setCount: () => {}};
  }
  const node = document.createElement("span");
  node.className = "viewers-count";
  node.hidden = true;
  node.setAttribute("aria-label", "Зрителей онлайн");
  // Number and eyes are separate children so the flex `gap` spaces them — a
  // single "N👀" text node would render them touching.
  const num = document.createElement("span");
  num.className = "viewers-count-num";
  const eyes = document.createElement("span");
  eyes.className = "viewers-count-eyes";
  eyes.textContent = "\u{1F440}";
  eyes.setAttribute("aria-hidden", "true");
  node.append(num, eyes);
  statusNode.parentElement.insertBefore(node, statusNode);
  return {
    setCount(count) {
      const n = Number(count);
      if (!Number.isFinite(n) || n <= 0) {
        node.hidden = true;
        num.textContent = "";
        return;
      }
      num.textContent = String(n);
      node.title = `Зрителей онлайн: ${n}`;
      node.hidden = false;
    },
  };
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
