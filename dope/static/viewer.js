const viewerRoot = document.getElementById("viewerTable");
const liveDot = document.getElementById("liveDot");
const pageHeading = document.querySelector(".host-top h1");

const route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let tournament = null;
let venues = [];
let stageStates = [];
let reloadTimer = null;

document.body.classList.toggle("embedded-match", embedded);

async function loadCurrent() {
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else {
    await loadTournament();
  }
}

async function loadTournament() {
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  tournament = await response.json();
  renderTournament();
}

async function loadStage() {
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  tournament = await response.json();
  const stage = findStage(tournament, route.stageCode);
  const matches = stage?.matches || [];
  stageStates = await Promise.all(matches.map(async (match) => {
    const matchResponse = await fetch(`${route.apiBase}/matches/${encodeURIComponent(match.code)}`);
    if (!matchResponse.ok) throw new Error(await matchResponse.text());
    return matchResponse.json();
  }));
  renderStage();
}

async function loadMatch() {
  const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode)}`);
  if (!response.ok) throw new Error(await response.text());
  state = await response.json();
  render();
}

async function loadVenuesPage() {
  const response = await fetch(`/api/tournaments/${route.tournamentID}/venues`);
  if (!response.ok) throw new Error(await response.text());
  venues = await response.json();
  renderVenues();
}

function connectEvents() {
  const events = new EventSource("/events");
  const matchScope = `match:${route.gameID}:${route.matchCode}`;
  const venuesScope = `venues:${route.tournamentID}`;
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    if (route.mode === "match" && message.scope === matchScope) {
      state = message.data;
      render();
      setLive(true);
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setLive(true);
      return;
    }
    if (route.mode === "stage" && message.scope.startsWith("match:")) {
      scheduleReload();
      return;
    }
    scheduleReload();
  });
  events.onerror = () => setLive(false);
}

function scheduleReload() {
  window.clearTimeout(reloadTimer);
  reloadTimer = window.setTimeout(() => {
    loadCurrent()
      .then(() => setLive(true))
      .catch((error) => {
        setLive(false);
        console.error(error);
      });
  }, 120);
}

function parseEventData(raw) {
  const parsed = JSON.parse(raw);
  if (parsed && typeof parsed.scope === "string" && Object.prototype.hasOwnProperty.call(parsed, "data")) {
    return parsed;
  }
  return {scope: "unknown", data: parsed};
}

function setLive(ok) {
  liveDot.classList.toggle("offline", !ok);
  const label = ok ? "Трансляция активна" : "Нет соединения";
  liveDot.setAttribute("aria-label", label);
  liveDot.title = label;
}

function renderTournament() {
  if (!tournament) return;
  setViewerMode("grid");
  setHeading(tournament.title);
  document.title = `Зритель · ${tournament.title}`;
  viewerRoot.replaceChildren(buildTournamentGrid(tournament, {viewer: true, basePath: route.base}));
}

function renderStage() {
  if (!tournament) return;
  const stage = findStage(tournament, route.stageCode);
  setViewerMode("match");
  setHeading(stage?.title || tournament.title);
  document.title = `Зритель · ${stage?.title || tournament.title}`;
  viewerRoot.replaceChildren(buildReadonlyStageTables());
}

function renderVenues() {
  setViewerMode("grid");
  setHeading("Площадки");
  document.title = "Зритель · Площадки";
  viewerRoot.replaceChildren(buildSubnav([{href: route.base + "/", label: "Сетка"}]), buildVenuesTable());
}

function render() {
  if (!state) return;
  setViewerMode("match");
  setHeading(state.stageTitle || state.title);
  document.title = `Зритель · ${state.title}`;
  const table = buildReadonlyTable();
  if (embedded) {
    viewerRoot.replaceChildren(table);
    notifyEmbeddedResize();
  } else {
    viewerRoot.replaceChildren(
      buildSubnav([
        {href: route.base + "/", label: "Сетка"},
        {href: route.base + "/venues", label: "Площадки"},
      ]),
      table,
    );
  }
}

function buildTournamentTable(data) {
  const table = document.createElement("table");
  table.className = "tournament-table";
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  ["Этап", "Бой", "Площадка", "Команды", "Σ", "М", "Статус"].forEach((label) => {
    header.appendChild(th(label, ""));
  });
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  data.stages.forEach((stage) => {
    if (!stage.matches || stage.matches.length === 0) {
      const row = document.createElement("tr");
      row.appendChild(td(stage.title, "stage-name"));
      row.appendChild(td(stage.code, "code-cell"));
      row.appendChild(td("", ""));
      row.appendChild(td(stage.stage_type || stage.type || "reseed", "muted-cell", {colSpan: 4}));
      tbody.appendChild(row);
      return;
    }
    stage.matches.forEach((match, matchIndex) => {
      const row = document.createElement("tr");
      row.appendChild(td(matchIndex === 0 ? stage.title : "", "stage-name"));

      const linkCell = document.createElement("td");
      const link = document.createElement("a");
      link.href = `${route.base}/matches/${encodeURIComponent(match.code)}`;
      link.className = "match-link";
      link.textContent = match.code;
      linkCell.appendChild(link);
      row.appendChild(linkCell);

      row.appendChild(td(formatVenue(match.venue), "venue-cell"));
      row.appendChild(teamListCell(match.teams));
      row.appendChild(td(match.teams.map((team) => formatNumber(team.total)).join(" · "), "number-list"));
      row.appendChild(td(match.teams.map((team) => formatPlace(team.place) || "—").join(" · "), "number-list"));
      row.appendChild(td(statusLabel(match.status), `status-cell ${match.status}`));
      tbody.appendChild(row);
    });
  });
  table.appendChild(tbody);
  return table;
}

function buildVenuesTable() {
  const table = document.createElement("table");
  table.className = "tournament-table venues-table";
  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th("№", "number"));
  header.appendChild(th("Название", ""));
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  venues.forEach((venue) => {
    const row = document.createElement("tr");
    row.appendChild(td(venue.number, "number venue-number"));
    row.appendChild(td(venue.title, ""));
    tbody.appendChild(row);
  });
  table.appendChild(tbody);
  return table;
}

function teamListCell(teams) {
  const cell = document.createElement("td");
  cell.className = "teams-cell";
  teams.forEach((team) => {
    const row = document.createElement("span");
    row.textContent = team.name;
    cell.appendChild(row);
  });
  return cell;
}

function buildSubnav(items) {
  const nav = document.createElement("nav");
  nav.className = "subnav";
  items.forEach((item) => {
    const link = document.createElement("a");
    link.href = item.href;
    link.textContent = item.label;
    nav.appendChild(link);
  });
  return nav;
}

function buildReadonlyStageTables() {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack";
  stageStates.forEach((matchState) => {
    wrapper.appendChild(withMatchState(matchState, () => buildReadonlyTable()));
  });
  return wrapper;
}

function withMatchState(matchState, callback) {
  const previousState = state;
  state = matchState;
  try {
    return callback();
  } finally {
    state = previousState;
  }
}

function buildReadonlyTable() {
  const table = document.createElement("table");
  table.className = "match-table readonly-table";
  const columnsPerTheme = state.questionValues.length + 2;
  const hasShootout = shootoutThemeCount() > 0;
  const totalColumnSpan = 4 + totalThemeCount() * columnsPerTheme + (hasShootout ? 7 : 6);

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th(matchTitle(), "sticky sticky-name battle"));
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));
  header.appendChild(th("", "sticky sticky-place-gap place-gap-head"));

  for (let theme = 0; theme < regularThemeCount(); theme++) {
    for (const value of state.questionValues) {
      header.appendChild(th(value, "question-head"));
    }
    header.appendChild(th(`Т${theme + 1}`, "theme-head"));
    header.appendChild(th("", "gap-head"));
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    for (const value of state.questionValues) {
      header.appendChild(th(value, "question-head shootout-head"));
    }
    header.appendChild(th(`П${theme + 1}`, "theme-head shootout-head"));
    header.appendChild(th("", "gap-head"));
  }
  if (hasShootout) {
    header.appendChild(th("П", "number"));
  }
  header.appendChild(th("Σ+", "number"));
  for (const value of [50, 40, 30, 20, 10]) {
    header.appendChild(th(value, "number narrow"));
  }
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  state.teams.forEach((team, teamIndex) => {
    const playerRow = document.createElement("tr");
    const answerRow = document.createElement("tr");
    answerRow.className = "answer-row";

    playerRow.appendChild(td(team.name, "sticky sticky-name team-name", {rowSpan: 2}));
    playerRow.appendChild(td(team.total, "sticky sticky-total number total-cell", {rowSpan: 2}));
    playerRow.appendChild(td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: 2}));
    playerRow.appendChild(td("", "sticky sticky-place-gap place-gap", {rowSpan: 2}));

    team.themes.forEach((theme) => {
      appendReadonlyThemeCells(playerRow, answerRow, theme, false);
    });
    shootoutThemesFor(team).forEach((theme) => {
      appendReadonlyThemeCells(playerRow, answerRow, theme, true);
    });

    if (hasShootout) {
      const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
      playerRow.appendChild(td(shootoutTotal, "number tiebreak-cell", {rowSpan: 2}));
    }
    playerRow.appendChild(td(team.plus, "number plus-cell", {rowSpan: 2}));
    [0, 1, 2, 3, 4].forEach((idx) => {
      playerRow.appendChild(td(team.correctCounts[4 - idx], "number narrow", {rowSpan: 2}));
    });

    tbody.appendChild(playerRow);
    tbody.appendChild(answerRow);
    if (teamIndex < state.teams.length - 1) {
      const gapRow = document.createElement("tr");
      gapRow.className = "team-gap-row";
      gapRow.appendChild(td("", "team-gap", {colSpan: totalColumnSpan}));
      tbody.appendChild(gapRow);
    }
  });
  table.appendChild(tbody);
  return table;
}

function appendReadonlyThemeCells(playerRow, answerRow, theme, isShootout) {
  const playerCell = document.createElement("td");
  playerCell.colSpan = 5;
  playerCell.className = "readonly-player theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }
  playerCell.textContent = theme.player || "";
  playerRow.appendChild(playerCell);
  playerRow.appendChild(td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2}));
  playerRow.appendChild(td("", "gap"));

  theme.answers.forEach((mark, answerIndex) => {
    const className = answerIndex === 0
      ? `answer-cell theme-block theme-block-bottom-left ${mark}`
      : `answer-cell theme-block ${mark}`;
    const cell = td("", className);
    if (isShootout) {
      cell.classList.add("shootout-block");
    }
    answerRow.appendChild(cell);
  });
  answerRow.appendChild(td("", "gap"));
}

function regularThemeCount() {
  return state.teams[0].themes.length;
}

function shootoutThemeCount() {
  return shootoutThemesFor(state.teams[0]).length;
}

function totalThemeCount() {
  return regularThemeCount() + shootoutThemeCount();
}

function shootoutThemesFor(team) {
  return team.shootoutThemes || [];
}

function currentRoute() {
  const path = window.location.pathname;
  const prefix = path.match(/^\/tournaments\/(\d+)\/game\/(\d+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const tournamentID = prefix[1];
  const gameID = prefix[2];
  const base = `/tournaments/${tournamentID}/game/${gameID}`;
  const apiBase = `/api/tournaments/${tournamentID}/games/${gameID}`;
  const rest = path.slice(prefix[0].length);
  const stripped = rest.replace(/\/$/, "");
  if (stripped === "" || stripped === "/") {
    return {mode: "grid", tournamentID, gameID, base, apiBase};
  }
  if (stripped === "/venues") return {mode: "venues", tournamentID, gameID, base, apiBase};
  const match = stripped.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), tournamentID, gameID, base, apiBase};
  const stage = stripped.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), tournamentID, gameID, base, apiBase};
  return {mode: "missing"};
}

function findStage(data, code) {
  const scheme = parseScheme(data.schemaJson);
  const stages = scheme?.stages?.length ? scheme.stages : data.stages || [];
  return stages.find((stage) => stage.code === code);
}

function setHeading(text) {
  if (pageHeading) pageHeading.textContent = text;
}

function setViewerMode(mode) {
  viewerRoot.classList.toggle("grid-host", mode === "grid");
  viewerRoot.classList.toggle("fight-host", mode === "match");
}

function notifyEmbeddedResize() {
  if (!embedded || window.parent === window) return;
  window.requestAnimationFrame(() => {
    window.parent.postMessage({
      type: "dope:resize",
      height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
    }, window.location.origin);
  });
}

function matchTitle() {
  const venue = state.venue ? ` · пл. ${state.venue.number}: ${state.venue.title}` : "";
  return `${state.title}${venue}`;
}

function formatVenue(venue) {
  return venue ? `${venue.number}: ${venue.title}` : "";
}

function statusLabel(status) {
  if (status === "finished") return "закончен";
  if (status === "pending") return "ожидает";
  return "активен";
}

function formatNumber(value) {
  return Number.isFinite(Number(value)) ? String(value) : "";
}

function formatPlace(place) {
  return place > 0 ? place : "";
}

function th(content, className) {
  const node = document.createElement("th");
  node.className = className;
  node.textContent = content;
  return node;
}

function td(content, className, attrs = {}) {
  const node = document.createElement("td");
  node.className = className;
  node.textContent = content;
  Object.assign(node, attrs);
  return node;
}

loadCurrent()
  .then(() => {
    setLive(true);
    connectEvents();
  })
  .catch((error) => {
    setLive(false);
    console.error(error);
  });
