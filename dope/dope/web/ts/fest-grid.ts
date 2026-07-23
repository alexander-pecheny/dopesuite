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

export interface FestGridLiveTeam {
  name?: string;
  source?: string;
  total?: unknown;
  place?: number;
}

export interface FestGridMatch {
  code?: string;
  title?: string;
  status?: string;
  venue?: FestGridVenue;
  slots?: FestGridSlot[];
  teams?: FestGridLiveTeam[];
  participantCount?: number | string;
}

export interface ReseedSortRule {
  metric?: string;
  dir?: string;
}

export interface ReseedEntry {
  rank?: number;
  name?: string;
  metrics?: Record<string, unknown>;
}

export interface FestGridStage {
  code?: string;
  title?: string;
  stage_type?: string;
  type?: string;
  layout?: { columns?: number };
  matches?: FestGridMatch[];
  reseedEntries?: ReseedEntry[];
  reseedBlockedMessage?: string;
  reseedPendingMatches?: Array<string | number | null | undefined>;
  config?: unknown;
  configJson?: unknown;
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
  hiddenVenueMatches?: Set<string | undefined>;
  hideVenue?: boolean;
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
    if (activeFestGridRoot) scheduleFestGridNameOverflowUpdate(activeFestGridRoot);
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
  const liveStages = new Map((data.stages || []).map((stage) => [stage.code, stage]));
  const previousVenueByRow = new Map<number, string>();

  stages.forEach((stage) => {
    const liveStage = liveStages.get(stage.code) || stage;
    if ((stage.stage_type || stage.type) === "reseed") {
      return;
    }
    const hiddenVenueMatches = repeatedVenueMatches(stage, liveStage, previousVenueByRow);
    columns.appendChild(buildMatchesStage(stage, liveStage, {...options, hiddenVenueMatches}));
  });
  root.appendChild(columns);
  activeFestGridRoot = root;
  scheduleFestGridNameOverflowUpdate(root);

  return root;
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
    actions.className = "cluster reseed-actions";
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

  const sortRules = reseedSortRules(stage);
  const hasSourceMatch = entries.some((entry) => entry.metrics?.match);
  const metricColumns = sortRules.length > 0
    ? sortRules
        .map((rule) => rule.metric)
        .filter((metric, index, values): metric is string => Boolean(metric) && values.indexOf(metric) === index)
    : fallbackReseedMetrics(entries);

  const table = document.createElement("table");
  table.className = "results-table reseed-results-table";
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(tableCell("th", "Место", "results-place-head"));
  header.appendChild(tableCell("th", "Команда", "results-team-head reseed-team-head"));
  if (hasSourceMatch) header.appendChild(tableCell("th", "Бой", "results-num reseed-source-head"));
  metricColumns.forEach((metric) => {
    header.appendChild(tableCell("th", reseedMetricHeader(metric, sortRules), "results-num reseed-metric-head"));
  });
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  entries.forEach((entry, index) => {
    const row = document.createElement("tr");
    row.className = "results-row";
    if (index === 0) row.classList.add("results-group-first");
    if (index === entries.length - 1) row.classList.add("results-group-last");
    row.appendChild(tableCell("td", entry.rank || index + 1, "results-place results-num"));
    row.appendChild(reseedTeamCell(entry.name || ""));
    if (hasSourceMatch) row.appendChild(tableCell("td", entry.metrics?.match || "", "results-num reseed-source"));
    metricColumns.forEach((metric) => {
      row.appendChild(tableCell("td", reseedMetricValue(entry.metrics?.[metric]), "results-num reseed-metric"));
    });
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  wrapper.appendChild(table);

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
    matches.appendChild(buildMatchBox(match, liveMatches.get(match.code), {
      ...options,
      hideVenue: options.hiddenVenueMatches?.has(match.code),
    }));
  });
  section.appendChild(matches);
  return section;
}

function buildMatchBox(match: FestGridMatch, liveMatch: FestGridMatch | undefined, options: FestGridOptions = {}): HTMLElement {
  const box = document.createElement("article");
  box.className = `grid-match ${liveMatch?.status || "pending"}`;
  box.dataset.matchCode = match.code || "";

  const venue = firstVenue(liveMatch?.venue, match.venue);
  const grid = document.createElement("div");
  grid.className = "grid-slot-grid";
  grid.appendChild(matchHeadCell(match, venue, options));
  grid.appendChild(gridHeadCell("slot-total-head", "Σ"));
  grid.appendChild(gridHeadCell("slot-place-head", "М"));
  const liveTeams = liveMatch?.teams || [];
  const slots = match.slots || [];
  const rowCount = gridSlotRowCount(match, slots);
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
  rows[0][0].classList.add("grid-slot-top-left");
  rows[0][2].classList.add("grid-slot-top-right");
  const last = rows[rows.length - 1];
  last.forEach((cell) => cell.classList.add("grid-slot-row-last"));
  last[0].classList.add("grid-slot-bottom-left");
  last[2].classList.add("grid-slot-bottom-right");
}

function matchHeadCell(match: FestGridMatch, venue: {number: number; title: string} | null, options: FestGridOptions = {}): HTMLElement {
  const cell = gridCell("grid-slot-head grid-match-head-cell", "");
  const layout = document.createElement("span");
  layout.className = "grid-match-head-layout";
  layout.appendChild(matchTitleNode(match, options));
  const venueLabel = venueText(venue);
  if (venueLabel && !options.hideVenue) {
    layout.appendChild(el("span", "grid-match-venue", venueLabel));
  }
  cell.appendChild(layout);
  return cell;
}

function matchTitleNode(match: FestGridMatch, options: FestGridOptions = {}): HTMLElement {
  if (!options.basePath || options.matchTitleLink === false) {
    return el("span", "grid-match-title", matchLabel(match));
  }
  const link = el("a", "grid-match-title grid-match-title-link", matchLabel(match));
  link.href = matchHref(match, options);
  return link;
}

function repeatedVenueMatches(
  stage: FestGridStage,
  liveStage: FestGridStage,
  previousVenueByRow: Map<number, string>,
): Set<string | undefined> {
  const hidden = new Set<string | undefined>();
  const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
  (stage.matches || []).forEach((match, index) => {
    const liveMatch = liveMatches.get(match.code);
    const label = venueText(firstVenue(liveMatch?.venue, match.venue));
    if (!label) return;
    if (previousVenueByRow.get(index) === label) {
      hidden.add(match.code);
    }
    previousVenueByRow.set(index, label);
  });
  return hidden;
}

export function parseScheme(raw: unknown): FestScheme | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw as string) as FestScheme;
  } catch (error) {
    return null;
  }
}

function reseedSortRules(stage: FestGridStage | null | undefined): ReseedSortRule[] {
  const config = parseObject(stage?.config) || parseObject(stage?.configJson);
  const sort = config?.sort;
  return Array.isArray(sort)
    ? (sort as ReseedSortRule[]).filter((rule) => rule?.metric)
    : [];
}

function parseObject(value: unknown): Record<string, unknown> | null {
  if (!value) return null;
  if (typeof value === "object") return value as Record<string, unknown>;
  if (typeof value !== "string") return null;
  try {
    return JSON.parse(value) as Record<string, unknown>;
  } catch (error) {
    return null;
  }
}

function fallbackReseedMetrics(entries: ReseedEntry[]): string[] {
  const preferred = ["place_sum", "total", "plus", "correct_50", "correct_40", "correct_30", "correct_20", "draw"];
  const present = new Set<string>();
  entries.forEach((entry) => {
    Object.keys(entry.metrics || {}).forEach((metric) => {
      if (metric !== "match") present.add(metric);
    });
  });
  const ordered = preferred.filter((metric) => present.has(metric));
  Array.from(present).sort().forEach((metric) => {
    if (!ordered.includes(metric)) ordered.push(metric);
  });
  return ordered;
}

function reseedMetricHeader(metric: string, sortRules: ReseedSortRule[]): string {
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
    wrong_50: "−50",
    wrong_40: "−40",
    wrong_30: "−30",
    wrong_20: "−20",
    wrong_10: "−10",
    draw: "Жребий",
  };
  return labels[metric] || metric;
}

function reseedMetricValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "";
  const number = Number(value);
  if (Number.isFinite(number) && String(value).trim() !== "") return scoreText(number);
  return String(value);
}

function reseedTeamCell(name: string): HTMLElement {
  const cell = tableCell("td", "", "results-team reseed-team");
  const wrap = document.createElement("span");
  wrap.className = "results-team-name-wrap";
  const label = document.createElement("span");
  label.className = "results-team-name";
  label.textContent = name;
  label.tabIndex = 0;
  label.setAttribute("aria-label", name);
  wrap.appendChild(label);
  cell.appendChild(wrap);
  const popover = document.createElement("span");
  popover.className = "popover results-team-name-popover";
  popover.textContent = name;
  cell.appendChild(popover);
  return cell;
}

function tableCell(tagName: "td" | "th", text: unknown, className?: string): HTMLTableCellElement {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  node.textContent = text == null ? "" : String(text).replace(/^-/, "−");
  return node;
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
  return `${basePath(options)}/matches/${encodeURIComponent(String(match.code))}`;
}

function basePath(options: FestGridOptions = {}): string {
  return options.basePath || "";
}

function matchLabel(match: FestGridMatch): string {
  const defaultTitle = `Бой ${match.code}`;
  if (!match.title || match.title === defaultTitle) return `Бой ${match.code}`;
  return match.title;
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
  fullName.className = "popover grid-slot-team-popover";
  fullName.textContent = label;
  cell.appendChild(fullName);
  return cell;
}

function scheduleFestGridNameOverflowUpdate(root: HTMLElement): void {
  if (festGridNameOverflowFrame) cancelAnimationFrame(festGridNameOverflowFrame);
  festGridNameOverflowFrame = requestAnimationFrame(() => {
    festGridNameOverflowFrame = 0;
    updateFestGridNameOverflow(root);
  });
}

function updateFestGridNameOverflow(root: HTMLElement): void {
  const cells = root.querySelectorAll(".grid-slot-team");
  const readings = new Array<boolean>(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(".grid-slot-team-name");
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle("grid-slot-team-truncated", readings[i]);
  }
}

function slotLabel(slot: FestGridSlot, live: FestGridLiveTeam = {}): string {
  if (typeof slot === "string") return slot;
  if (live.name && live.name !== live.source) return live.name;
  if (slot.label) return slot.label;
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

function venueText(venue: FestGridVenue | {number: number; title: string} | null): string {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `пл. ${normalized.number} (${normalized.title})` : `пл. ${normalized.number}`;
}

function firstVenue(...venues: FestGridVenue[]): {number: number; title: string} | null {
  for (const venue of venues) {
    const normalized = normalizeVenue(venue);
    if (normalized) return normalized;
  }
  return null;
}

function normalizeVenue(venue: FestGridVenue): {number: number; title: string} | null {
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
