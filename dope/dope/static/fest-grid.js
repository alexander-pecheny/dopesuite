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

  const venue = liveMatch?.venue || match.venue;
  const table = document.createElement("table");
  table.className = "grid-slot-table";
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.className = "grid-match-head-row";
  header.appendChild(matchHeadCell(match, venue, options));
  header.appendChild(el("th", "slot-total-head", "Σ"));
  header.appendChild(el("th", "slot-place-head", "М"));
  thead.appendChild(header);
  table.appendChild(thead);
  const tbody = document.createElement("tbody");
  const liveTeams = liveMatch?.teams || [];
  (match.slots || []).forEach((slot, index) => {
    const live = liveTeams[index] || {};
    const row = document.createElement("tr");
    row.appendChild(slotTeamCell(slotLabel(slot, live)));
    row.appendChild(el("td", "slot-total", scoreText(live.total)));
    row.appendChild(el("td", "slot-place", placeText(live.place)));
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  box.appendChild(table);
  return box;
}

function matchHeadCell(match, venue, options = {}) {
  const cell = document.createElement("th");
  cell.className = "grid-match-head-cell";
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
    const label = venueText(liveMatch?.venue || match.venue);
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
  const cell = document.createElement("td");
  cell.className = "slot-source grid-slot-team";
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
  root.querySelectorAll(".grid-slot-team").forEach((cell) => {
    const name = cell.querySelector(".grid-slot-team-name");
    const truncated = Boolean(name && name.scrollWidth > name.clientWidth + 1);
    cell.classList.toggle("grid-slot-team-truncated", truncated);
  });
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
  if (slot.reseed) return "";
  if (slot.team) return slot.team.name || slot.team.label || slot.team.id || "";
  if (slot.placeholder) return slot.placeholder;
  return live.source || "";
}

function venueText(venue) {
  if (!venue) return "";
  if (typeof venue === "number") return `пл. ${venue}`;
  return venue.title ? `пл. ${venue.number} (${venue.title})` : `пл. ${venue.number}`;
}

function stageClassSuffix(code) {
  return String(code).replace(/[^a-z0-9_-]/gi, "-");
}

function scoreText(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number === 0) return "";
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
