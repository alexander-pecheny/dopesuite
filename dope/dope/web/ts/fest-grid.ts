import { markNameOverflow } from "./widgets.js";
import { festLetters, letteredTitle, standingsTable, type StageRef } from "./match-table.js";
import { blockLabel, groupLabel } from "./game-tabs.js";

export interface FestGridVenueObject {
  number?: unknown;
  Number?: unknown;
  title?: unknown;
  Title?: unknown;
}

export type FestGridVenue = number | string | FestGridVenueObject | null | undefined;

export interface FestGridSlotObject {
  label?: string;
  seed?: { number?: number | string; position?: number | string; basket?: number | string };
  fromMatch?: { match?: string | number; place?: string | number };
  reseed?: { rank?: number | string };
  team?: { name?: string; label?: string; id?: string };
  placeholder?: string;
}

export type FestGridSlot = string | FestGridSlotObject;

export interface FestGridLiveParticipant {
  name?: string;
  source?: string;
  total?: unknown;
  place?: number;
}

export interface FestGridMatch {
  code?: string;
  title?: string;
  status?: string;
  letter?: string;
  venue?: FestGridVenue;
  slots?: FestGridSlot[];
  participants?: FestGridLiveParticipant[];
  participantCount?: number | string;
  // row pins the бой to a row of the Сетка's shared grid (1-based); unset, it
  // flows under the бой before it.
  row?: number;
}

export interface SortRule {
  metric: string;
  dir?: string;
}

export interface ReseedEntry {
  rank?: number;
  participantID?: number;
  name?: string;
  metrics?: Record<string, unknown>;
}

export interface FestGridStage {
  code?: string;
  title?: string;
  stage_type?: string;
  type?: string;
  kind?: string;
  grain?: {block?: string; group?: string; wave?: number};
  standings?: ReseedEntry[];
  // sort is the Ranker's order, from the server: the columns a table shows.
  sort?: SortRule[] | null;
  layout?: { columns?: number };
  matches?: FestGridMatch[];
  reseedEntries?: ReseedEntry[];
  reseedBlockedMessage?: string;
  reseedPendingMatches?: Array<string | number | null | undefined>;
}

export interface FestGridData {
  schemaJson?: unknown;
  stages?: FestGridStage[];
}

export interface FestScheme {
  stages?: FestGridStage[];
}

export interface FestGridOptions {
  basePath?: string;
  viewer?: boolean;
  editable?: boolean;
  canCalculate?: boolean;
  blockedMessage?: string;
  onCalculate?: () => void;
  stageHeaderLink?: boolean;
  matchTitleLink?: boolean;
  // letters is the whole game's буква map — a caller drawing a slice of the
  // scheme passes it, so a slot that names a бой outside the slice still
  // reads «Бой BU».
  letters?: Map<string, string>;
}

// A drawn grid: its root, its Blocks of Groups (re-shaped to the screen on
// every resize) and the frame that update waits on. One entry per grid, so
// two on a page — the брейн Сетка and its pod board — never share state; a
// grid whose root has left the page is forgotten at the next build or resize.
interface Grid {
  root: HTMLElement;
  blocks: Array<{section: HTMLElement; stack: HTMLElement; units: number[]}>;
  frame: number;
}

const grids = new Set<Grid>();
let resizeListenerBound = false;

// Registered lazily on the first buildFestGrid so the module stays importable
// under plain node; before a grid exists the listener would no-op anyway.
function bindFestGridResizeListener(): void {
  if (resizeListenerBound) return;
  resizeListenerBound = true;
  window.addEventListener("resize", () => {
    for (const grid of liveGrids()) scheduleFestGridUpdate(grid);
  });
}

function liveGrids(): Set<Grid> {
  for (const grid of grids) {
    if (!grid.root.isConnected) grids.delete(grid);
  }
  return grids;
}

interface PaintContext {
  letters: Map<string, string> | null;
  options: FestGridOptions;
}

export function buildFestGrid(data: FestGridData, options: FestGridOptions = {}): HTMLElement {
  bindFestGridResizeListener();
  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  const liveStages = new Map((data.stages || []).map((stage) => [stage.code, stage]));
  const plan = planGrid(stages, liveStages);
  const ctx: PaintContext = {letters: options.letters || festLetters(data.stages as StageRef[]), options};
  liveGrids();

  const root = document.createElement("div");
  root.className = "fest-grid";
  root.style.setProperty("--grid-unit-rows", String(plan.unitRows));
  const columns = document.createElement("div");
  columns.className = "fest-columns";
  const grid: Grid = {root, blocks: [], frame: 0};
  for (const section of plan.sections) {
    if (section.kind === "block") columns.appendChild(buildBlockColumn(section, grid, ctx));
    else if (section.kind === "standings") columns.appendChild(buildStandingsStage(section, ctx));
    else columns.appendChild(buildMatchesStage(section, ctx));
  }
  root.appendChild(columns);
  grids.add(grid);
  scheduleFestGridUpdate(grid);
  return root;
}

// --- the plan ---------------------------------------------------------------
//
// planGrid is the Сетка's layout without its DOM: what each column is, how
// many rows each box takes of the shared row grid, and how a Block of Groups
// packs into columns. The builders below read it and add nothing.

// GridItem is one box — a бой or a Group's table — its rows (a head and its
// seats), the units of the shared row it spans, and the row it is pinned to.
export interface GridItem {
  rows: number;
  units: number;
  row?: number;
}

// GridTable is a ranked stage drawn as a table: its rows in seating order,
// and the sort whose first key is the table's one number — none until the
// Ranker has written the table.
export interface GridTable {
  stage: FestGridStage;
  live: FestGridStage;
  entries: ReseedEntry[];
  order: string[];
  sort?: SortRule[] | null;
  item: GridItem;
}

// GridBoxes is a stage drawn as бой boxes, one item per бой.
export interface GridBoxes {
  stage: FestGridStage;
  live: FestGridStage;
  boxes: Array<GridItem & {match: FestGridMatch}>;
}

// GridBlock is a Block of Groups: its tables (or a legacy stage's boxes)
// stacked under one head, wrapped into `cols` columns of `rows` units — one
// column until the screen is measured.
export interface GridBlock {
  stages: FestGridStage[];
  entries: Array<GridTable | GridBoxes>;
  rows: number;
  cols: number;
}

export type GridSection =
  | ({kind: "matches"} & GridBoxes)
  | ({kind: "standings"} & GridTable)
  | ({kind: "block"} & GridBlock);

export interface GridPlan {
  unitRows: number;
  sections: GridSection[];
}

// The Сетка's rows are shared across its columns, like the sheet's: a row is
// the grid's tallest box, up to a head and four seats, and anything taller
// spans as many rows as it needs, so what stands beside it stays level. A
// board of two-seat бои gets three-row units; every Сетка with a group table
// gets five.
const MAX_UNIT_ROWS = 5;

export function planGrid(stages: FestGridStage[], liveStages: Map<string | undefined, FestGridStage> = new Map()): GridPlan {
  const liveOf = (stage: FestGridStage) => liveStages.get(stage.code) || stage;
  const items: GridItem[] = [];
  // A Group is a table, not a wall of бои: a группа of nine plays twelve of
  // them, and twelve boxes say less about who is winning than nine rows do.
  // The бои are still there — the detailed tab lists them. A Group the Ranker
  // has not written yet gets placeless rows in seating order, so the map of
  // who sits where is there before a бой is played. Bracket rounds carry no
  // group and keep their boxes, and so does a legacy grouped stage the scheme
  // never graded (kind unknown).
  const table = (stage: FestGridStage): GridTable | null => {
    const live = liveOf(stage);
    const order = blockOf(stage) !== "" ? stageSlotOrder(stage, live) : [];
    const standings = live.standings || stage.standings || [];
    let ranked: Pick<GridTable, "entries" | "sort">;
    if (standings.length) ranked = {entries: standings, sort: live.sort || stage.sort};
    else if (stage.grain?.group && order.length && stage.kind) ranked = {entries: order.map((name) => ({name, metrics: {}}))};
    else return null;
    const item = {rows: 1 + ranked.entries.length, units: 1};
    items.push(item);
    return {stage, live, order, item, ...ranked};
  };
  const boxes = (stage: FestGridStage): GridBoxes => ({
    stage,
    live: liveOf(stage),
    boxes: (stage.matches || []).map((match) => {
      const item = {match, rows: 1 + gridSlotRowCount(match, match.slots || []), units: 1, row: match.row};
      items.push(item);
      return item;
    }),
  });
  const sections: GridSection[] = groupStagesByBlock(stages).map((bucket) => {
    if (bucket.length > 1) {
      return {kind: "block", stages: bucket, entries: bucket.map((stage) => table(stage) || boxes(stage)), rows: 1, cols: 1};
    }
    const ranked = table(bucket[0]);
    return ranked ? {kind: "standings", ...ranked} : {kind: "matches", ...boxes(bucket[0])};
  });
  const unitRows = Math.min(MAX_UNIT_ROWS, Math.max(1, ...items.map(({rows}) => rows)));
  for (const item of items) item.units = Math.max(1, Math.ceil(item.rows / unitRows));
  for (const section of sections) {
    if (section.kind === "block") Object.assign(section, packBlock(blockUnits(section.entries)));
  }
  return {unitRows, sections};
}

// A table spans its units; a stage of boxes one.
function blockUnits(entries: Array<GridTable | GridBoxes>): number[] {
  return entries.map((entry) => ("item" in entry ? entry.item.units : 1));
}

// A Block of Groups wraps into as many columns as the screen's height asks
// for: as many rows as fit below the block's head, then the next column — so
// the whole Сетка fits one screen where its бой columns do. The row is the
// unit the CSS sizes; the JS only counts, and evens the columns out. Until
// the screen is measured the stack is one column.
const MIN_BLOCK_ROWS = 2;

export function packBlock(units: number[], viewportRows?: number): {rows: number; cols: number} {
  const total = units.reduce((sum, span) => sum + span, 0);
  if (viewportRows === undefined) return {rows: total || 1, cols: 1};
  const most = Math.max(viewportRows, MIN_BLOCK_ROWS, ...units);
  const cols = columnsFor(units, most);
  // The fewest rows that still pack into that many columns — 12 групп at
  // five to a column read as 4+4+4, not 5+5+2.
  let rows = Math.ceil(total / cols);
  while (rows < most && columnsFor(units, rows) > cols) rows += 1;
  return {rows, cols};
}

// columnsFor packs the stack the way CSS auto-placement will: down a column
// while the next item fits, else the next column.
function columnsFor(units: number[], rows: number): number {
  let cols = 1;
  let filled = 0;
  for (const span of units) {
    if (filled + span > rows) {
      cols += 1;
      filled = 0;
    }
    filled += span;
  }
  return cols;
}

// --- the paint --------------------------------------------------------------

// groupStagesByBlock buckets consecutive group stages of one Block together
// (reseeds dropped — the Пересев tab holds those), leaving every other stage
// in a bucket of its own.
// blockOf names a Group stage's Block, as the grain says; a stage without a
// Group is its own column.
function blockOf(stage: FestGridStage): string {
  return stage.grain?.group ? stage.grain.block || "" : "";
}

function groupStagesByBlock(stages: FestGridStage[]): FestGridStage[][] {
  const buckets: FestGridStage[][] = [];
  let block = "";
  for (const stage of stages) {
    if ((stage.stage_type || stage.type) === "reseed") {
      block = "";
      continue;
    }
    const grouped = blockOf(stage);
    if (grouped && grouped === block) {
      buckets[buckets.length - 1].push(stage);
    } else {
      buckets.push([stage]);
    }
    block = grouped;
  }
  return buckets;
}

// stageColumn is a column of the Сетка: one stage, or a Block's stack.
function stageColumn(stage: FestGridStage, columns: number, className = ""): HTMLElement {
  const column = document.createElement("section");
  column.className = `grid-stage${className ? " " + className : ""}`;
  if (stage.code) column.classList.add(`grid-stage-${stageClassSuffix(stage.code)}`);
  column.dataset.stageCode = stage.code || "";
  column.style.setProperty("--stage-columns", String(columns));
  return column;
}

function buildBlockColumn(section: GridBlock, grid: Grid, ctx: PaintContext): HTMLElement {
  const column = stageColumn(section.stages[0], 1, "grid-stage-block");

  const header = document.createElement("div");
  header.className = "grid-stage-head";
  header.appendChild(el("h2", "", blockLabel(section.stages as StageRef[])));
  column.appendChild(header);

  // One container for the whole stack: .grid-stage lays out via
  // `display: contents`, so loose children would each take a column.
  const stack = document.createElement("div");
  stack.className = "grid-block-stack";
  for (const entry of section.entries) {
    stack.appendChild("item" in entry ? buildStandingsTable(entry) : buildMatchBoxes(entry, ctx));
  }
  setBlockShape(column, section.rows, section.cols);
  grid.blocks.push({section: column, stack, units: blockUnits(section.entries)});
  column.appendChild(stack);
  return column;
}

function setBlockShape(section: HTMLElement, rows: number, cols: number): void {
  section.style.setProperty("--block-rows", String(rows));
  section.style.setProperty("--block-cols", String(cols));
}

// layoutBlockColumns re-shapes every Block of Groups to the screen: measure
// the rows that fit below its head, re-pack, repaint the shape.
function layoutBlockColumns(grid: Grid): void {
  for (const {section, stack, units} of grid.blocks) {
    const unit = parseFloat(getComputedStyle(stack).gridTemplateRows) || 0;
    if (!unit) continue; // not laid out yet — the plan's one column stands
    const top = stack.getBoundingClientRect().top + window.scrollY;
    const shape = packBlock(units, Math.floor((window.innerHeight - top) / unit));
    setBlockShape(section, shape.rows, shape.cols);
  }
}

export function buildReseedStagePanel(
  stage: FestGridStage | null | undefined,
  options: FestGridOptions = {},
): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper reseed-results-wrapper";

  const entries = Array.isArray(stage?.reseedEntries) ? stage.reseedEntries : [];
  const blockedMessage = reseedBlockedMessage(stage, options);
  if (options.editable) {
    const actions = document.createElement("div");
    actions.className = "cluster";
    const calculateButton = document.createElement("button");
    calculateButton.type = "button";
    calculateButton.className = "btn";
    calculateButton.textContent = entries.length > 0 ? "Пересчитать" : "Рассчитать";
    calculateButton.disabled = !options.canCalculate;
    if (!options.canCalculate) {
      calculateButton.title = blockedMessage || "Исходные бои ещё не закончены";
    }
    calculateButton.addEventListener("click", () => {
      if (calculateButton.disabled) return;
      options.onCalculate?.();
    });
    actions.appendChild(calculateButton);
    wrapper.appendChild(actions);
  }

  // The columns are the Ranker's sort rules, one each, as the server sent them.
  const sortRules = stage?.sort || [];
  const metricColumns = sortRules.map((rule) => rule.metric)
    .filter((metric, index, values) => values.indexOf(metric) === index);
  // The source бои speak in буквы; a column that reads the same in every row
  // — the отбор seats everyone from one бой — says nothing and goes.
  const letters = options.letters;
  const sources = entries.map((entry) => String(entry.metrics?.match || "").split("+").filter(Boolean)
    .map((code) => letters?.get(code) || code).join(", "));
  const hasSourceMatch = sources.some(Boolean) && new Set(sources).size > 1;

  wrapper.appendChild(standingsTable({
    className: "reseed-results-table",
    columns: [
      {label: "Место", kind: "place"},
      {label: "Команда", kind: "name"},
      ...(hasSourceMatch ? [{label: "Бой", kind: "num" as const}] : []),
      ...metricColumns.map((metric) => ({label: reseedMetricHeader(metric, sortRules), kind: "num" as const})),
    ],
    rows: entries.map((entry, index) => [
      entry.rank || index + 1,
      entry.name || "",
      ...(hasSourceMatch ? [sources[index]] : []),
      ...metricColumns.map((metric) => reseedMetricValue(metric, entry.metrics?.[metric])),
    ]),
  }));

  if (options.editable && !options.canCalculate && blockedMessage) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = blockedMessage;
    wrapper.appendChild(empty);
  } else if (entries.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "Пересев пока не рассчитан.";
    wrapper.appendChild(empty);
  }

  return wrapper;
}

function reseedBlockedMessage(stage: FestGridStage | null | undefined, options: FestGridOptions = {}): string {
  const fromOptions = String(options.blockedMessage || "").trim();
  if (fromOptions) return fromOptions;
  const fromStage = String(stage?.reseedBlockedMessage || "").trim();
  if (fromStage) return fromStage;
  const pending = Array.isArray(stage?.reseedPendingMatches)
    ? stage.reseedPendingMatches.map((code) => String(code || "").trim()).filter(Boolean)
    : [];
  if (pending.length === 1) return `Бой ${pending[0]} не закончен`;
  if (pending.length > 1) return `Бои ${pending.join(", ")} не закончены`;
  return "";
}

// buildStandingsStage draws a ranking Kind as its own table — место, team, and
// whatever the block ranks by. It is what the source sheets show for a группа,
// and it fits a column where a dozen бой boxes do not.
function buildStandingsStage(section: GridTable, ctx: PaintContext): HTMLElement {
  const {stage} = section;
  const column = stageColumn(stage, 1);
  column.appendChild(stageHead(stage, stage.grain?.group ? blockLabel([stage as StageRef]) : stage.title, ctx));
  const body = document.createElement("div");
  body.className = "grid-matches";
  body.appendChild(buildStandingsTable(section));
  column.appendChild(body);
  return column;
}

// stageHead is a column's title, a link to the stage's page unless the caller
// draws without one.
function stageHead(stage: FestGridStage, title: string | undefined, ctx: PaintContext): HTMLElement {
  const header = document.createElement(ctx.options.stageHeaderLink === false ? "div" : "a");
  header.className = "grid-stage-head";
  if (header instanceof HTMLAnchorElement) {
    header.href = stageHref(stage, ctx.options);
    header.classList.add("grid-stage-link");
  }
  header.appendChild(el("h2", "", title));
  return header;
}

// A table's head row names what the table is — the группа, or the Block when
// it is the Block's only table — and the table it plays at, the way a бой box
// says «Бой A · пл. 1». A Group holds one table, so the venue is its own.
interface TableHead {
  title: string;
  venue: Venue | null;
}

function tableHead(stage: FestGridStage, liveStage: FestGridStage): TableHead {
  return {
    title: stage.grain?.group ? groupLabel(stage as StageRef) : String(stage.title || ""),
    venue: stageVenue(stage, liveStage),
  };
}

// stageVenue is the one table every бой of the stage sits at, or null when
// they spread — a pod's бои share a table like a группа's do.
function stageVenue(stage: FestGridStage, liveStage: FestGridStage): Venue | null {
  const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
  let found: Venue | null = null;
  for (const match of stage.matches || []) {
    const venue = firstVenue(liveMatches.get(match.code)?.venue, match.venue);
    if (!venue) return null;
    if (found && venueText(found) !== venueText(venue)) return null;
    found = venue;
  }
  return found;
}

// stageSlotOrder is the seating order of a stage's participants — first
// appearance across its бои, which is how the schedule dealt them. The Сетка's
// rows sit in this order, not place order, so a live группа never reshuffles
// under the reader. An unseated slot contributes its label («Пересев-3»), so
// the map of who proceeds where survives until the reseed fills the names.
function stageSlotOrder(stage: FestGridStage, liveStage: FestGridStage): string[] {
  const order: string[] = [];
  const seen = new Set<string>();
  const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
  (stage.matches || []).forEach((match) => {
    const live = liveMatches.get(match.code);
    const liveTeams = live?.participants || match.participants || [];
    const slots = match.slots || [];
    const count = Math.max(liveTeams.length, slots.length);
    for (let index = 0; index < count; index += 1) {
      const name = String(liveTeams[index]?.name || "") ||
        (slots[index] !== undefined ? slotLabel(slots[index], liveTeams[index] || {}) : "");
      if (!name || seen.has(name)) continue;
      seen.add(name);
      order.push(name);
    }
  });
  return order;
}

// A Group's table is a бой box: the same article and cells the бой boxes
// beside it wear, so one skin covers both by construction. The rows sit in
// seating order; the columns are the name, М, and the one number the Block
// ranks by first — a second number costs forty pixels the names need, and
// everything else belongs on the stage's own page.
function buildStandingsTable({stage, live, entries, order, sort, item}: GridTable): HTMLElement {
  const metric = sort?.[0]?.metric;
  const head = tableHead(stage, live);
  const box = el("article", `grid-box grid-standings${metric ? "" : " grid-standings-bare"}`, "");
  const grid = el("div", "grid-slot-grid", "");
  const title = gridCell("grid-slot-head grid-match-head-cell", "");
  title.appendChild(headLayout(el("span", "grid-match-title", head.title), head.venue));
  grid.appendChild(title);
  if (metric) grid.appendChild(gridHeadCell("slot-total-head", standingsMetricLabel(metric)));
  grid.appendChild(gridHeadCell("slot-place-head", "М"));
  const rows = order.length
    ? entries.slice().sort((a, b) => slotIndex(order, a) - slotIndex(order, b))
    : entries;
  spanRows(box, item);
  const realRows = rows.map((entry) => {
    const cells = [slotTeamCell(String(entry.name || ""))];
    if (metric) cells.push(gridCell("slot-total", reseedMetricValue(metric, entry.metrics?.[metric])));
    cells.push(gridCell("slot-place", placeText(Number(entry.metrics?.place ?? entry.rank) || null)));
    cells.forEach((cell) => grid.appendChild(cell));
    return cells;
  });
  decorateGridSlotRows(realRows);
  box.appendChild(grid);
  return box;
}

// slotIndex finds an entry's seat in the slot order; a name the schedule never
// seated sinks below everyone it did.
function slotIndex(order: string[], entry: ReseedEntry): number {
  const index = order.indexOf(String(entry.name || ""));
  return index < 0 ? order.length + Number(entry.rank || 0) : index;
}

// The Сетка's columns are a glance wide, and it already writes М and Σ rather
// than «место» and «сумма». The few metrics whose names do not fit get the same
// treatment; everything else keeps the word the пересев panel uses.
function standingsMetricLabel(metric: string): string {
  const short: Record<string, string> = {points: "О", taken: "В", bouts: "Б"};
  return short[metric] || reseedMetricLabel(metric);
}

function buildMatchesStage(section: GridBoxes, ctx: PaintContext): HTMLElement {
  const {stage} = section;
  const column = stageColumn(stage, stage.layout?.columns || preferredColumns(stage.matches?.length || 1));
  column.appendChild(stageHead(stage, stage.title, ctx));
  column.appendChild(buildMatchBoxes(section, ctx));
  return column;
}

function buildMatchBoxes({live, boxes}: GridBoxes, ctx: PaintContext): HTMLElement {
  const matches = document.createElement("div");
  matches.className = "grid-matches";
  const liveMatches = new Map((live.matches || []).map((match) => [match.code, match]));
  for (const item of boxes) matches.appendChild(buildMatchBox(item.match, liveMatches.get(item.match.code), item, ctx));
  return matches;
}

// spanRows sets a box on the shared row grid: its span, at the row it is
// pinned to or flowing under the box before it.
function spanRows(box: HTMLElement, item: GridItem): void {
  box.dataset.units = String(item.units);
  box.style.setProperty("grid-row", item.row ? `${item.row} / span ${item.units}` : `span ${item.units}`);
}

function buildMatchBox(match: FestGridMatch, liveMatch: FestGridMatch | undefined, item: GridItem, ctx: PaintContext): HTMLElement {
  const box = document.createElement("article");
  box.className = `grid-box grid-match ${liveMatch?.status || "pending"}`;
  box.dataset.matchCode = match.code || "";

  const venue = firstVenue(liveMatch?.venue, match.venue);
  const grid = document.createElement("div");
  grid.className = "grid-slot-grid";
  grid.appendChild(matchHeadCell(match, venue, ctx));
  grid.appendChild(gridHeadCell("slot-total-head", "Σ"));
  grid.appendChild(gridHeadCell("slot-place-head", "М"));
  const liveTeams = liveMatch?.participants || [];
  const slots = match.slots || [];
  spanRows(box, item);
  const realRows: HTMLElement[][] = [];
  for (let index = 0; index < item.rows - 1; index += 1) {
    const slot = slots[index];
    if (!slot) {
      phantomSlotCells().forEach((cell) => grid.appendChild(cell));
      continue;
    }
    const live = liveTeams[index] || {};
    const cells = [
      slotTeamCell(slotLabel(slot, live, ctx.letters)),
      gridCell("slot-total", scoreText(live.total)),
      gridCell("slot-place", placeText(live.place)),
    ];
    realRows.push(cells);
    cells.forEach((cell) => grid.appendChild(cell));
  }
  decorateGridSlotRows(realRows);
  box.appendChild(grid);
  return box;
}

function gridSlotRowCount(match: FestGridMatch, slots: FestGridSlot[]): number {
  const declared = Number(match.participantCount);
  const rowCount = Math.max(slots.length, Number.isFinite(declared) ? declared : 0);
  return rowCount === 3 ? 4 : rowCount;
}

function gridHeadCell(className: string, text: string): HTMLElement {
  const cell = gridCell(`grid-slot-head ${className}`, "");
  cell.appendChild(el("span", "grid-head-metric", text));
  return cell;
}

function gridCell(className: string, text: string): HTMLElement {
  return el("div", `grid-slot-cell ${className}`, text);
}

function phantomSlotCells(): HTMLElement[] {
  return [
    gridCell("slot-source grid-slot-phantom-cell", ""),
    gridCell("slot-total grid-slot-phantom-cell", ""),
    gridCell("slot-place grid-slot-phantom-cell", ""),
  ].map((cell) => {
    cell.setAttribute("aria-hidden", "true");
    return cell;
  });
}

function decorateGridSlotRows(rows: HTMLElement[][]): void {
  if (rows.length === 0) return;
  const first = rows[0];
  const last = rows[rows.length - 1];
  first[0].classList.add("grid-slot-top-left");
  first[first.length - 1].classList.add("grid-slot-top-right");
  last.forEach((cell) => cell.classList.add("grid-slot-row-last"));
  last[0].classList.add("grid-slot-bottom-left");
  last[last.length - 1].classList.add("grid-slot-bottom-right");
}

function matchHeadCell(match: FestGridMatch, venue: Venue | null, ctx: PaintContext): HTMLElement {
  const cell = gridCell("grid-slot-head grid-match-head-cell", "");
  cell.appendChild(headLayout(matchTitleNode(match, ctx), venue));
  return cell;
}

function headLayout(title: HTMLElement, venue: Venue | null): HTMLElement {
  const layout = document.createElement("span");
  layout.className = "grid-match-head-layout";
  layout.appendChild(title);
  const venueLabel = venueText(venue);
  if (venueLabel) layout.appendChild(el("span", "grid-match-venue", venueLabel));
  return layout;
}

function matchTitleNode(match: FestGridMatch, ctx: PaintContext): HTMLElement {
  const label = matchLabel(match, ctx.letters);
  if (!ctx.options.basePath || ctx.options.matchTitleLink === false) {
    return el("span", "grid-match-title", label);
  }
  const link = el("a", "grid-match-title grid-match-title-link", label);
  link.href = matchHref(match, ctx);
  return link;
}

export function parseScheme(raw: unknown): FestScheme | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw as string) as FestScheme;
  } catch (error) {
    return null;
  }
}

function reseedMetricHeader(metric: string, sortRules: SortRule[]): string {
  const rule = sortRules.find((item) => item.metric === metric);
  const direction = rule?.dir === "asc" ? "↑" : rule?.dir === "desc" ? "↓" : "";
  return direction ? `${reseedMetricLabel(metric)} ${direction}` : reseedMetricLabel(metric);
}

function reseedMetricLabel(metric: string): string {
  const labels: Record<string, string> = {
    place_sum: "Σ мест",
    total: "Σ",
    plus: "Σ+",
    tiebreak: "П",
    correct_50: "+50",
    correct_40: "+40",
    correct_30: "+30",
    correct_20: "+20",
    correct_10: "+10",
    taken50: "+50",
    taken40: "+40",
    taken30: "+30",
    taken20: "+20",
    taken10: "+10",
    wrong_50: "−50",
    wrong_40: "−40",
    wrong_30: "−30",
    wrong_20: "−20",
    wrong_10: "−10",
    points_share: "% очков",
    taken_share: "% взятых",
    diff: "+/−",
    taken_base: "Взятые б/п",
    points: "Очки",
    taken: "Взятые",
    bouts: "Боёв",
    draw: "Жребий",
  };
  return labels[metric] || metric;
}

function reseedMetricValue(metric: string, value: unknown): string {
  if (value === null || value === undefined || value === "") return "";
  const number = Number(value);
  if (!Number.isFinite(number) || String(value).trim() === "") return String(value);
  if (metric.endsWith("_share")) return `${scoreText(Math.round(number * 1000) / 10)}%`;
  return scoreText(number);
}

function preferredColumns(count: number): number {
  if (count >= 6) return 6;
  if (count >= 4) return 4;
  if (count >= 2) return 2;
  return 1;
}

function stageHref(stage: FestGridStage, options: FestGridOptions = {}): string {
  return `${basePath(options)}/stage/${encodeURIComponent(String(stage.code))}`;
}

function matchHref(match: FestGridMatch, ctx: PaintContext): string {
  const code = String(match.code || "");
  return `${basePath(ctx.options)}/matches/${encodeURIComponent(ctx.letters?.get(code) || code)}`;
}

function basePath(options: FestGridOptions = {}): string {
  return options.basePath || "";
}

// The бои wear their буква — the sheets' A..Z, AA.. handle — as the compiler
// dealt them and the fest view carries them; a URL says the буква too.
function matchLabel(match: FestGridMatch, letters: Map<string, string> | null): string {
  const letter = letters?.get(match.code || "");
  if (!match.title || match.title === `Бой ${match.code}`) return `Бой ${letter || match.code}`;
  return letteredTitle(match.title, letter);
}

function slotTeamCell(label: string): HTMLElement {
  const cell = gridCell("slot-source grid-slot-team", "");
  const name = document.createElement("span");
  name.className = "grid-slot-team-name";
  name.textContent = label;
  name.tabIndex = 0;
  name.setAttribute("aria-label", label);
  cell.appendChild(name);
  const fullName = document.createElement("span");
  fullName.className = "popover popover-inline grid-slot-team-popover";
  fullName.textContent = label;
  cell.appendChild(fullName);
  return cell;
}

function scheduleFestGridUpdate(grid: Grid): void {
  if (grid.frame) cancelAnimationFrame(grid.frame);
  grid.frame = requestAnimationFrame(() => {
    grid.frame = 0;
    layoutBlockColumns(grid);
    updateFestGridNameOverflow(grid.root);
  });
}

function updateFestGridNameOverflow(root: HTMLElement): void {
  markNameOverflow(root, {
    cellSelector: ".grid-slot-team",
    nameSelector: ".grid-slot-team-name",
    truncatedClass: "grid-slot-team-truncated",
  });
  markNameOverflow(root, {
    cellSelector: ".grid-match-head-layout",
    nameSelector: ".grid-match-title",
    truncatedClass: "grid-head-truncated",
  });
}

function slotLabel(slot: FestGridSlot, live: FestGridLiveParticipant = {}, letters: Map<string, string> | null = null): string {
  if (typeof slot === "string") return slot;
  if (live.name && live.name !== live.source) return live.name;
  if (slot.label) {
    const letter = slot.fromMatch ? letters?.get(String(slot.fromMatch.match || "")) : "";
    return letteredTitle(slot.label, letter || undefined);
  }
  if (slot.seed) {
    const number = slot.seed.number || slot.seed.position;
    if (slot.seed.basket) return `К${slot.seed.basket}-${number}`;
    return number ? `seed-${number}` : "seed";
  }
  if (slot.fromMatch) return `${slot.fromMatch.match}${slot.fromMatch.place}`;
  if (slot.reseed) return reseedLabel(slot.reseed);
  if (slot.team) return slot.team.name || slot.team.label || slot.team.id || "";
  if (slot.placeholder) return slot.placeholder;
  return live.source || "";
}

function reseedLabel(reseed: { rank?: number | string }): string {
  const rank = Number(reseed.rank);
  return Number.isFinite(rank) && rank > 0 ? `Пересев-${rank}` : "Пересев";
}

type Venue = {number: number; title: string};

function venueText(venue: FestGridVenue | Venue | null): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `пл. ${normalized.number} (${normalized.title})` : `пл. ${normalized.number}`;
}

function firstVenue(...venues: FestGridVenue[]): Venue | null {
  for (const venue of venues) {
    const normalized = normalizeVenue(venue);
    if (normalized) return normalized;
  }
  return null;
}

function normalizeVenue(venue: FestGridVenue): Venue | null {
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

function stageClassSuffix(code: string): string {
  return String(code).replace(/[^a-z0-9_-]/gi, "-");
}

function scoreText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "";
  const number = Number(value);
  if (!Number.isFinite(number)) return "";
  return String(value).replace(/^-/, "−");
}

function placeText(value: number | null | undefined): string {
  return value != null && value > 0 ? String(value) : "";
}

function el<K extends keyof HTMLElementTagNameMap>(
  tagName: K,
  className: string,
  text: string | null | undefined,
  attrs: Record<string, unknown> = {},
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  node.textContent = text ?? null;
  Object.assign(node, attrs);
  return node;
}
