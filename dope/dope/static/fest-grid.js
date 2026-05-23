function buildFestGrid(data, options = {}) {
  const root = document.createElement("div");
  root.className = "fest-grid";

  const columns = document.createElement("div");
  columns.className = "fest-columns";

  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  const liveStages = new Map((data.stages || []).map((stage) => [stage.code, stage]));

  stages.forEach((stage) => {
    const liveStage = liveStages.get(stage.code) || stage;
    if ((stage.stage_type || stage.type) === "reseed") {
      return;
    }
    columns.appendChild(buildMatchesStage(stage, liveStage, options));
  });
  root.appendChild(columns);

  return root;
}

function buildMatchesStage(stage, liveStage, options = {}) {
  const section = document.createElement("section");
  section.className = "grid-stage";
  section.style.setProperty("--stage-columns", String(stage.layout?.columns || preferredColumns(stage.matches?.length || 1)));

  const header = document.createElement("a");
  header.className = "grid-stage-head grid-stage-link";
  header.href = stageHref(stage, options);
  header.appendChild(el("h2", "", stage.title));
  section.appendChild(header);

  const matches = document.createElement("div");
  matches.className = "grid-matches";
  const liveMatches = new Map((liveStage.matches || []).map((match) => [match.code, match]));
  (stage.matches || []).forEach((match) => {
    matches.appendChild(buildMatchBox(match, liveMatches.get(match.code)));
  });
  section.appendChild(matches);
  return section;
}

function buildMatchBox(match, liveMatch) {
  const box = document.createElement("article");
  box.className = `grid-match ${liveMatch?.status || "pending"}`;

  const head = document.createElement("div");
  head.className = "grid-match-head";
  head.appendChild(el("strong", "grid-match-title", matchLabel(match)));
  const venue = liveMatch?.venue || match.venue;
  head.appendChild(el("span", "grid-match-venue", venueText(venue)));
  box.appendChild(head);

  const table = document.createElement("table");
  table.className = "grid-slot-table";
  const tbody = document.createElement("tbody");
  const liveTeams = liveMatch?.teams || [];
  (match.slots || []).forEach((slot, index) => {
    const live = liveTeams[index] || {};
    const row = document.createElement("tr");
    row.appendChild(el("td", "slot-position", String(index + 1)));
    row.appendChild(el("td", "slot-source", slotLabel(slot, live)));
    row.appendChild(el("td", "slot-total", scoreText(live.total)));
    row.appendChild(el("td", "slot-place", placeText(live.place)));
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  box.appendChild(table);
  return box;
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
  if (!match.title || match.title === defaultTitle) return `бой ${match.code}`;
  return match.title;
}

function slotLabel(slot, live = {}) {
  if (live.name && live.name !== live.source) return live.name;
  if (slot.label) return slot.label;
  if (slot.seed) return `К${slot.seed.basket}-${slot.seed.position}`;
  if (slot.fromMatch) return `${slot.fromMatch.match}${slot.fromMatch.place}`;
  if (slot.reseed) return "";
  if (slot.team) return slot.team.name || slot.team.label || slot.team.id || "";
  if (slot.placeholder) return slot.placeholder;
  return live.source || "";
}

function venueText(venue) {
  if (!venue) return "";
  if (typeof venue === "number") return `пл. ${venue}`;
  return `пл. ${venue.number}: ${venue.title}`;
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
