let festGridNameOverflowFrame = 0;
let activeFestGridRoot = null;

window.addEventListener("resize", () => {
  if (activeFestGridRoot) scheduleFestGridNameOverflowUpdate(activeFestGridRoot);
});

function buildFestGrid(data, options = {}) {
  const root = document.createElement("div");
  root.className = "fest-grid";

  const columns = document.createElement("div");
  columns.className = "fest-columns";

  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  const liveStages = new Map((data.stages || []).map((stage) => [stage.code, stage]));
  const previousVenueByRow = new Map();

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

function buildReseedStagePanel(stage, options = {}) {
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
    ? sortRules.map((rule) => rule.metric).filter((metric, index, values) => metric && values.indexOf(metric) === index)
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

function reseedBlockedMessage(stage, options = {}) {
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

function buildMatchesStage(stage, liveStage, options = {}) {
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

function buildMatchBox(match, liveMatch, options = {}) {
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
  const realRows = [];
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

function gridSlotRowCount(match, slots) {
  const declared = Number(match.participantCount);
  const rowCount = Math.max(slots.length, Number.isFinite(declared) ? declared : 0);
  return rowCount === 3 ? 4 : rowCount;
}

function gridHeadCell(className, text) {
  const cell = gridCell(`grid-slot-head ${className}`, "");
  cell.appendChild(el("span", "grid-head-metric", text));
  return cell;
}

function gridCell(className, text) {
  return el("div", `grid-slot-cell ${className}`, text);
}

function phantomSlotCells() {
  return [
    gridCell("slot-source grid-slot-phantom-cell", ""),
    gridCell("slot-total grid-slot-phantom-cell", ""),
    gridCell("slot-place grid-slot-phantom-cell", ""),
  ].map((cell) => {
    cell.setAttribute("aria-hidden", "true");
    return cell;
  });
}

function decorateGridSlotRows(rows) {
  if (rows.length === 0) return;
  rows[0][0].classList.add("grid-slot-top-left");
  rows[0][2].classList.add("grid-slot-top-right");
  const last = rows[rows.length - 1];
  last.forEach((cell) => cell.classList.add("grid-slot-row-last"));
  last[0].classList.add("grid-slot-bottom-left");
  last[2].classList.add("grid-slot-bottom-right");
}

function matchHeadCell(match, venue, options = {}) {
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

function matchTitleNode(match, options = {}) {
  if (!options.basePath || options.matchTitleLink === false) {
    return el("span", "grid-match-title", matchLabel(match));
  }
  const link = el("a", "grid-match-title grid-match-title-link", matchLabel(match));
  link.href = matchHref(match, options);
  return link;
}

function repeatedVenueMatches(stage, liveStage, previousVenueByRow) {
  const hidden = new Set();
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

function parseScheme(raw) {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch (error) {
    return null;
  }
}

function reseedSortRules(stage) {
  const config = parseObject(stage?.config) || parseObject(stage?.configJson);
  return Array.isArray(config?.sort) ? config.sort.filter((rule) => rule?.metric) : [];
}

function parseObject(value) {
  if (!value) return null;
  if (typeof value === "object") return value;
  if (typeof value !== "string") return null;
  try {
    return JSON.parse(value);
  } catch (error) {
    return null;
  }
}

function fallbackReseedMetrics(entries) {
  const preferred = ["place_sum", "total", "plus", "correct_50", "correct_40", "correct_30", "correct_20", "draw"];
  const present = new Set();
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

function reseedMetricHeader(metric, sortRules) {
  const rule = sortRules.find((item) => item.metric === metric);
  const direction = rule?.dir === "asc" ? "↑" : rule?.dir === "desc" ? "↓" : "";
  return direction ? `${reseedMetricLabel(metric)} ${direction}` : reseedMetricLabel(metric);
}

function reseedMetricLabel(metric) {
  const labels = {
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

function reseedMetricValue(value) {
  if (value === null || value === undefined || value === "") return "";
  const number = Number(value);
  if (Number.isFinite(number) && String(value).trim() !== "") return scoreText(number);
  return String(value);
}

function reseedTeamCell(name) {
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
  popover.className = "results-team-name-popover";
  popover.textContent = name;
  cell.appendChild(popover);
  return cell;
}

function tableCell(tagName, text, className) {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  node.textContent = text == null ? "" : String(text).replace(/^-/, "\u2212");
  return node;
}

function preferredColumns(count) {
  if (count >= 6) return 6;
  if (count >= 4) return 4;
  if (count >= 2) return 2;
  return 1;
}

function stageHref(stage, options = {}) {
  return `${basePath(options)}/stage/${encodeURIComponent(stage.code)}`;
}

function matchHref(match, options = {}) {
  return `${basePath(options)}/matches/${encodeURIComponent(match.code)}`;
}

function basePath(options = {}) {
  return options.basePath || "";
}

function matchLabel(match) {
  const defaultTitle = `Бой ${match.code}`;
  if (!match.title || match.title === defaultTitle) return `Бой ${match.code}`;
  return match.title;
}

function slotTeamCell(label) {
  const cell = gridCell("slot-source grid-slot-team", "");
  const name = document.createElement("span");
  name.className = "grid-slot-team-name";
  name.textContent = label;
  name.tabIndex = 0;
  name.setAttribute("aria-label", label);
  cell.appendChild(name);
  const fullName = document.createElement("span");
  fullName.className = "grid-slot-team-popover";
  fullName.textContent = label;
  cell.appendChild(fullName);
  return cell;
}

function scheduleFestGridNameOverflowUpdate(root) {
  if (festGridNameOverflowFrame) cancelAnimationFrame(festGridNameOverflowFrame);
  festGridNameOverflowFrame = requestAnimationFrame(() => {
    festGridNameOverflowFrame = 0;
    updateFestGridNameOverflow(root);
  });
}

function updateFestGridNameOverflow(root) {
  const cells = root.querySelectorAll(".grid-slot-team");
  const readings = new Array(cells.length);
  for (let i = 0; i < cells.length; i++) {
    const name = cells[i].querySelector(".grid-slot-team-name");
    readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
  }
  for (let i = 0; i < cells.length; i++) {
    cells[i].classList.toggle("grid-slot-team-truncated", readings[i]);
  }
}

function slotLabel(slot, live = {}) {
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

function reseedLabel(reseed) {
  const rank = Number(reseed.rank);
  return Number.isFinite(rank) && rank > 0 ? `Пересев-${rank}` : "Пересев";
}

function venueText(venue) {
  const normalized = normalizeVenue(venue);
  if (!normalized) return "";
  return normalized.title ? `пл. ${normalized.number} (${normalized.title})` : `пл. ${normalized.number}`;
}

function firstVenue(...venues) {
  for (const venue of venues) {
    const normalized = normalizeVenue(venue);
    if (normalized) return normalized;
  }
  return null;
}

function normalizeVenue(venue) {
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

function stageClassSuffix(code) {
  return String(code).replace(/[^a-z0-9_-]/gi, "-");
}

function scoreText(value) {
  if (value === null || value === undefined || value === "") return "";
  const number = Number(value);
  if (!Number.isFinite(number)) return "";
  return String(value).replace(/^-/, "\u2212");
}

function placeText(value) {
  return value > 0 ? String(value) : "";
}

function el(tagName, className, text, attrs = {}) {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  node.textContent = text;
  Object.assign(node, attrs);
  return node;
}
