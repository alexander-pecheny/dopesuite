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

let festGridNameOverflowFrame = 0;
let activeFestGridRoot: HTMLElement | null = null;
let resizeListenerBound = false;

// Registered lazily on the first buildFestGrid so the module stays importable
// under plain node; before a grid exists the listener would no-op anyway.
function bindFestGridResizeListener(): void {
  if (resizeListenerBound) return;
  resizeListenerBound = true;
  window.addEventListener("resize", () => {
    if (activeFestGridRoot) scheduleFestGridUpdate(activeFestGridRoot);
  });
}

export function buildFestGrid(data: FestGridData, options: FestGridOptions = {}): HTMLElement {
  bindFestGridResizeListener();
  const root = document.createElement("div");
  root.className = "fest-grid";

  const columns = document.createElement("div");
  columns.className = "fest-columns";

  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  boutLetters = options.letters || festLetters(data.stages as StageRef[]);
  placed = [];
  blocks = [];
  const liveStages = new Map((data.stages || []).map((stage) => [stage.code, stage]));

  // A ranking Block is one column: its групп's tables (or a pod Block's бои)
  // stack under one header rather than sprawling a column per группа. Rounds
  // carry no group, so a bracket keeps its column per заход.
  groupStagesByBlock(stages).forEach((bucket) => {
    if (bucket.length > 1) {
      columns.appendChild(buildBlockColumn(bucket, liveStages, options));
      return;
    }
    const stage = bucket[0];
    const liveStage = liveStages.get(stage.code) || stage;
    // A Group is a table, not a wall of бои: a группа of nine plays twelve of
    // them, and twelve boxes say less about who is winning than nine rows do.
    // The бои are still there — the detailed tab lists them. A lone pod ranks
    // itself the same way. Bracket rounds carry no group and keep their boxes,
    // and so does a legacy grouped stage the scheme never graded.
    const order = blockOf(stage) !== "" ? stageSlotOrder(stage, liveStage) : [];
    const standings = liveStage.standings || stage.standings || [];
    if (standings.length) {
      columns.appendChild(buildStandingsStage(stage, liveStage, standings, options, order));
      return;
    }
    const table = ungradedStandings(stage, order);
    if (table) {
      columns.appendChild(buildStandingsStage(stage, liveStage, table, options, order));
      return;
    }
    columns.appendChild(buildMatchesStage(stage, liveStage, options));
  });
  root.appendChild(columns);
  settleRows(root);
  activeFestGridRoot = root;
  scheduleFestGridUpdate(root);

  return root;
}

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

function buildBlockColumn(
  bucket: FestGridStage[],
  liveStages: Map<string | undefined, FestGridStage>,
  options: FestGridOptions = {},
): HTMLElement {
  const first = bucket[0];
  const section = document.createElement("section");
  section.className = "grid-stage grid-stage-block";
  if (first.code) section.classList.add(`grid-stage-${stageClassSuffix(first.code)}`);
  section.dataset.stageCode = first.code || "";
  section.style.setProperty("--stage-columns", "1");

  const header = document.createElement("div");
  header.className = "grid-stage-head";
  header.appendChild(el("h2", "", blockLabel(bucket as StageRef[])));
  section.appendChild(header);

  // One container for the whole stack: .grid-stage lays out via
  // `display: contents`, so loose children would each take a column.
  const stack = document.createElement("div");
  stack.className = "grid-block-stack";
  blocks.push({section, stack});
  bucket.forEach((stage) => {
    const liveStage = liveStages.get(stage.code) || stage;
    const order = stageSlotOrder(stage, liveStage);
    const standings = liveStage.standings || stage.standings || [];
    if (standings.length) {
      stack.appendChild(buildStandingsTable(standings, order, tableHead(stage, liveStage), liveStage.sort || stage.sort));
      return;
    }
    const table = ungradedStandings(stage, order);
    if (table) {
      stack.appendChild(buildStandingsTable(table, order, tableHead(stage, liveStage)));
      return;
    }
    const matches = document.createElement("div");
    matches.className = "grid-matches";
    const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
    (stage.matches || []).forEach((match) => {
      matches.appendChild(buildMatchBox(match, liveMatches.get(match.code), options));
    });
    stack.appendChild(matches);
  });
  section.appendChild(stack);
  return section;
}

// A Block of Groups wraps into as many columns as the screen's height asks
// for: as many rows as fit below the block's head, then the next column — so
// the whole Сетка fits one screen where its бой columns do. The row is the
// unit the CSS sizes; the JS only counts, and evens the columns out.
const MIN_BLOCK_ROWS = 2;

function stackUnits(stack: HTMLElement): number[] {
  return Array.from(stack.children).map((item) => Number((item as HTMLElement).dataset.units) || 1);
}

function setBlockShape(section: HTMLElement, rows: number, cols: number): void {
  section.style.setProperty("--block-rows", String(rows));
  section.style.setProperty("--block-cols", String(cols));
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

function layoutBlockColumns(root: HTMLElement): void {
  for (const {section, stack} of blocks) {
    const unit = parseFloat(getComputedStyle(stack).gridTemplateRows) || 0;
    if (!unit) continue; // not laid out yet — settleRows' one column stands
    const units = stackUnits(stack);
    const top = stack.getBoundingClientRect().top + window.scrollY;
    const fit = Math.floor((window.innerHeight - top) / unit);
    const most = Math.max(fit, MIN_BLOCK_ROWS, ...units);
    const cols = columnsFor(units, most);
    // The fewest rows that still pack into that many columns — 12 групп at
    // five to a column read as 4+4+4, not 5+5+2.
    let rows = Math.ceil(units.reduce((sum, span) => sum + span, 0) / cols);
    while (rows < most && columnsFor(units, rows) > cols) rows += 1;
    setBlockShape(section, rows, cols);
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
  const letters = options.letters || boutLetters;
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
function buildStandingsStage(
  stage: FestGridStage,
  liveStage: FestGridStage,
  standings: ReseedEntry[],
  options: FestGridOptions,
  order: string[],
): HTMLElement {
  const section = document.createElement("section");
  section.className = "grid-stage";
  if (stage.code) section.classList.add(`grid-stage-${stageClassSuffix(stage.code)}`);
  section.dataset.stageCode = stage.code || "";
  section.style.setProperty("--stage-columns", "1");

  const header = document.createElement(options.stageHeaderLink === false ? "div" : "a");
  header.className = "grid-stage-head";
  if (header instanceof HTMLAnchorElement) {
    header.href = stageHref(stage, options);
    header.classList.add("grid-stage-link");
  }
  header.appendChild(el("h2", "", stage.grain?.group ? blockLabel([stage as StageRef]) : stage.title));
  section.appendChild(header);
  const body = document.createElement("div");
  body.className = "grid-matches";
  body.appendChild(buildStandingsTable(standings, order, tableHead(stage, liveStage), liveStage.sort || stage.sort));
  section.appendChild(body);
  return section;
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

// ungradedStandings is the compact table for a Group whose Ranker has not
// written a table yet — placeless rows in seating order, so the map of who
// sits where is there before a бой is played. A legacy stage the scheme never
// graded (kind unknown, blockOf by code convention alone) gets nothing — its
// бои stay.
function ungradedStandings(stage: FestGridStage, order: string[]): ReseedEntry[] | null {
  if (!stage.grain?.group || !order.length || !stage.kind) return null;
  return order.map((name) => ({name, metrics: {}}));
}

// A Group's table is a бой box: the same article and cells the бой boxes
// beside it wear, so one skin covers both by construction. The rows sit in
// seating order; the columns are the name, М, and the one number the Block
// ranks by first — a second number costs forty pixels the names need, and
// everything else belongs on the stage's own page.
function buildStandingsTable(standings: ReseedEntry[], order: string[], head: TableHead, sort?: SortRule[] | null): HTMLElement {
  const metric = sort?.[0]?.metric;
  const box = el("article", `grid-box grid-standings${metric ? "" : " grid-standings-bare"}`, "");
  const grid = el("div", "grid-slot-grid", "");
  const title = gridCell("grid-slot-head grid-match-head-cell", "");
  title.appendChild(headLayout(el("span", "grid-match-title", head.title), head.venue));
  grid.appendChild(title);
  if (metric) grid.appendChild(gridHeadCell("slot-total-head", standingsMetricLabel(metric)));
  grid.appendChild(gridHeadCell("slot-place-head", "М"));
  const rows = order.length
    ? standings.slice().sort((a, b) => slotIndex(order, a) - slotIndex(order, b))
    : standings;
  placeOnRows(box, 1 + rows.length);
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

function buildMatchesStage(stage: FestGridStage, liveStage: FestGridStage, options: FestGridOptions = {}): HTMLElement {
  const section = document.createElement("section");
  section.className = "grid-stage";
  if (stage.code) section.classList.add(`grid-stage-${stageClassSuffix(stage.code)}`);
  section.dataset.stageCode = stage.code || "";
  section.style.setProperty("--stage-columns", String(stage.layout?.columns || preferredColumns(stage.matches?.length || 1)));

  const header = document.createElement(options.stageHeaderLink === false ? "div" : "a");
  header.className = "grid-stage-head";
  if (header instanceof HTMLAnchorElement) {
    header.href = stageHref(stage, options);
    header.classList.add("grid-stage-link");
  }
  header.appendChild(el("h2", "", stage.title));
  section.appendChild(header);

  const matches = document.createElement("div");
  matches.className = "grid-matches";
  const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
  (stage.matches || []).forEach((match) => {
    matches.appendChild(buildMatchBox(match, liveMatches.get(match.code), options));
  });
  section.appendChild(matches);
  return section;
}

// The Сетка's rows are shared across its columns, like the sheet's: a row is
// the grid's tallest box, up to a head and four seats, and anything taller
// spans as many rows as it needs, so what stands beside it stays level. A
// board of two-seat бои gets three-row units; every Сетка with a group table
// gets five.
const MAX_UNIT_ROWS = 5;
let placed: Array<{item: HTMLElement; rows: number; row?: number}> = [];
let blocks: Array<{section: HTMLElement; stack: HTMLElement}> = [];

function placeOnRows(item: HTMLElement, rows: number, row?: number): void {
  placed.push({item, rows, row});
}

// settleRows sizes the row once every box is built, then places them: each
// item its span, each Block of Groups one column until layoutBlockColumns
// has measured the screen.
function settleRows(root: HTMLElement): void {
  const unitRows = Math.min(MAX_UNIT_ROWS, Math.max(1, ...placed.map(({rows}) => rows)));
  root.style.setProperty("--grid-unit-rows", String(unitRows));
  for (const {item, rows, row} of placed) {
    const units = Math.max(1, Math.ceil(rows / unitRows));
    item.dataset.units = String(units);
    item.style.setProperty("grid-row", row ? `${row} / span ${units}` : `span ${units}`);
  }
  for (const {section, stack} of blocks) {
    setBlockShape(section, stackUnits(stack).reduce((sum, units) => sum + units, 0) || 1, 1);
  }
}

function buildMatchBox(match: FestGridMatch, liveMatch: FestGridMatch | undefined, options: FestGridOptions = {}): HTMLElement {
  const box = document.createElement("article");
  box.className = `grid-box grid-match ${liveMatch?.status || "pending"}`;
  box.dataset.matchCode = match.code || "";

  const venue = firstVenue(liveMatch?.venue, match.venue);
  const grid = document.createElement("div");
  grid.className = "grid-slot-grid";
  grid.appendChild(matchHeadCell(match, venue, options));
  grid.appendChild(gridHeadCell("slot-total-head", "Σ"));
  grid.appendChild(gridHeadCell("slot-place-head", "М"));
  const liveTeams = liveMatch?.participants || [];
  const slots = match.slots || [];
  const rowCount = gridSlotRowCount(match, slots);
  placeOnRows(box, 1 + rowCount, match.row);
  const realRows: HTMLElement[][] = [];
  for (let index = 0; index < rowCount; index += 1) {
    const slot = slots[index];
    if (!slot) {
      phantomSlotCells().forEach((cell) => grid.appendChild(cell));
      continue;
    }
    const live = liveTeams[index] || {};
    const cells = [
      slotTeamCell(slotLabel(slot, live)),
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

function matchHeadCell(match: FestGridMatch, venue: Venue | null, options: FestGridOptions = {}): HTMLElement {
  const cell = gridCell("grid-slot-head grid-match-head-cell", "");
  cell.appendChild(headLayout(matchTitleNode(match, options), venue));
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

function matchTitleNode(match: FestGridMatch, options: FestGridOptions = {}): HTMLElement {
  if (!options.basePath || options.matchTitleLink === false) {
    return el("span", "grid-match-title", matchLabel(match));
  }
  const link = el("a", "grid-match-title grid-match-title-link", matchLabel(match));
  link.href = matchHref(match, options);
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

function matchHref(match: FestGridMatch, options: FestGridOptions = {}): string {
  const code = String(match.code || "");
  return `${basePath(options)}/matches/${encodeURIComponent(boutLetters?.get(code) || code)}`;
}

function basePath(options: FestGridOptions = {}): string {
  return options.basePath || "";
}

// The бои wear their буква — the sheets' A..Z, AA.. handle — as the compiler
// dealt them and the fest view carries them; a URL says the буква too.
let boutLetters: Map<string, string> | null = null;

function matchLabel(match: FestGridMatch): string {
  const letter = boutLetters?.get(match.code || "");
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

function scheduleFestGridUpdate(root: HTMLElement): void {
  if (festGridNameOverflowFrame) cancelAnimationFrame(festGridNameOverflowFrame);
  festGridNameOverflowFrame = requestAnimationFrame(() => {
    festGridNameOverflowFrame = 0;
    layoutBlockColumns(root);
    updateFestGridNameOverflow(root);
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

function slotLabel(slot: FestGridSlot, live: FestGridLiveParticipant = {}): string {
  if (typeof slot === "string") return slot;
  if (live.name && live.name !== live.source) return live.name;
  if (slot.label) {
    const letter = slot.fromMatch ? boutLetters?.get(String(slot.fromMatch.match || "")) : "";
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
