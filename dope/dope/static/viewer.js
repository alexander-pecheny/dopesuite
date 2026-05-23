const viewerRoot = document.getElementById("viewerTable");
const liveDot = document.getElementById("liveDot");
const pageHeading = document.querySelector(".host-top h1");

const gameTable = window.DopeTable;
const route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let fest = null;
let venues = [];
let stageStates = [];
let reloadTimer = null;
let readonlyTableIndex = null;

document.body.classList.toggle("embedded-match", embedded);

async function loadCurrent() {
  if (route.mode === "match") {
    await loadMatch();
  } else if (route.mode === "stage") {
    await loadStage();
  } else if (route.mode === "venues") {
    await loadVenuesPage();
  } else {
    await loadFest();
  }
}

async function loadFest() {
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  fest = await response.json();
  renderFest();
}

async function loadStage() {
  const response = await fetch(route.apiBase);
  if (!response.ok) throw new Error(await response.text());
  fest = await response.json();
  const stage = findStage(fest, route.stageCode);
  const matches = stage?.matches || [];
  stageStates = await Promise.all(matches.map(async (match) => {
    const matchResponse = await fetch(`${route.apiBase}/matches/${encodeURIComponent(match.code)}`);
    if (!matchResponse.ok) throw new Error(await matchResponse.text());
    return matchResponse.json();
  }));
  renderStage();
}

async function loadMatch() {
  const [matchResponse, festResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode)}`),
    fetch(route.apiBase),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  state = await matchResponse.json();
  fest = await festResponse.json();
  render();
}

async function loadVenuesPage() {
  const [venuesResponse, festResponse] = await Promise.all([
    fetch(`/api/fest/${route.festID}/venues`),
    fetch(route.apiBase),
  ]);
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  if (!festResponse.ok) throw new Error(await festResponse.text());
  venues = await venuesResponse.json();
  fest = await festResponse.json();
  renderVenues();
}

function connectEvents() {
  const events = new EventSource(`/events?fest_id=${encodeURIComponent(route.festID)}`);
  const matchScope = `match:${route.gameID}:${route.matchCode}`;
  const venuesScope = `venues:${route.festID}`;
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    if (route.mode === "match" && message.scope === matchScope) {
      applyUpdatedMatch(message.data);
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
  return gameTable.parseScopedEvent(raw);
}

function setLive(ok) {
  liveDot.classList.toggle("offline", !ok);
  const label = ok ? "Трансляция активна" : "Нет соединения";
  liveDot.setAttribute("aria-label", label);
  liveDot.title = label;
}

function renderFest() {
  if (!fest) return;
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading(fest.title);
  document.title = pageTitle();
  viewerRoot.replaceChildren(buildFestGrid(fest, {viewer: true, basePath: route.base}));
}

function renderStage() {
  if (!fest) return;
  resetReadonlyTableIndex();
  const stage = findStage(fest, route.stageCode);
  setViewerMode("match");
  setHeading(stage?.title || fest.title);
  document.title = pageTitle();
  viewerRoot.replaceChildren(buildReadonlyStageTables());
}

function renderVenues() {
  resetReadonlyTableIndex();
  setViewerMode("grid");
  setHeading("Площадки");
  document.title = pageTitle("Площадки");
  viewerRoot.replaceChildren(buildSubnav([{href: route.base + "/", label: "Сетка"}]), buildVenuesTable());
}

function render() {
  if (!state) return;
  setViewerMode("match");
  setHeading(state.stageTitle || state.title);
  document.title = pageTitle();
  const table = buildReadonlyTable();
  readonlyTableIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
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

function applyUpdatedMatch(updated) {
  const previous = state;
  state = updated;
  if (canPatchReadonlyMatchTable(previous, updated)) {
    patchReadonlyMatchTable();
    return;
  }
  render();
}

function canPatchReadonlyMatchTable(previous, next) {
  if (route.mode !== "match" || !readonlyTableIndex || !previous || !next) return false;
  if (previous.code !== next.code || previous.title !== next.title || previous.finished !== next.finished) return false;
  if (matchTitleFor(previous) !== matchTitleFor(next)) return false;
  if (!gameTable.sameArray(previous.questionValues, next.questionValues)) return false;
  if ((previous.teams || []).length !== (next.teams || []).length) return false;
  for (let i = 0; i < next.teams.length; i++) {
    const prevTeam = previous.teams[i];
    const nextTeam = next.teams[i];
    if (prevTeam.name !== nextTeam.name || formatPlace(prevTeam.place) !== formatPlace(nextTeam.place)) return false;
    if ((prevTeam.themes || []).length !== (nextTeam.themes || []).length) return false;
    if (shootoutThemesFor(prevTeam).length !== shootoutThemesFor(nextTeam).length) return false;
  }
  return true;
}

function patchReadonlyMatchTable() {
  state.teams.forEach((team, teamIndex) => {
    setIndexedText("total", {team: teamIndex}, team.total);
    setIndexedText("plus", {team: teamIndex}, team.plus);
    setIndexedText("tiebreak", {team: teamIndex}, team.shootoutTotal ?? team.tiebreak);
    [0, 1, 2, 3, 4].forEach((idx) => {
      setIndexedText("correctCount", {team: teamIndex, valueIndex: idx}, team.correctCounts[4 - idx]);
    });
    team.themes.forEach((theme, themeIndex) => {
      patchReadonlyTheme(teamIndex, themeIndex, false, theme);
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      patchReadonlyTheme(teamIndex, themeIndex, true, theme);
    });
  });
}

function patchReadonlyTheme(teamIndex, themeIndex, isShootout, theme) {
  const shootout = isShootout ? "1" : "0";
  setIndexedText("themeScore", {team: teamIndex, shootout, theme: themeIndex}, theme.score);
  theme.answers.forEach((mark, answerIndex) => {
    const cell = readonlyTableIndex?.get("answer", {team: teamIndex, shootout, theme: themeIndex, answer: answerIndex});
    gameTable.setMarkClass(cell, mark);
  });
}

function setIndexedText(name, values, value) {
  const node = readonlyTableIndex?.get(name, values);
  if (node) gameTable.setNodeText(node, value, formatNumber);
}

function resetReadonlyTableIndex() {
  readonlyTableIndex = null;
}

function buildFestTable(data) {
  const table = document.createElement("table");
  table.className = "fest-table";
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
  table.className = "fest-table venues-table";
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
    link.className = "action-link";
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
  const hasShootout = shootoutThemeCount() > 0;
  const themes = readonlyThemeHeaders();
  const rows = state.teams.map((team, teamIndex) => {
    const themeCells = [];
    team.themes.forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, false));
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      themeCells.push(readonlyThemeCells(teamIndex, theme, themeIndex, true));
    });
    return {
      nameCell: td(team.name, "sticky sticky-name team-name", {rowSpan: 2, dataset: {team: teamIndex}}),
      totalCell: td(team.total, "sticky sticky-total number total-cell", {rowSpan: 2, dataset: {team: teamIndex}}),
      placeCell: td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: 2, dataset: {team: teamIndex}}),
      themes: themeCells,
      afterThemeCells: readonlyTrailingCells(team, teamIndex, hasShootout),
    };
  });

  return gameTable.buildTwoRowScoreTable({
    className: "match-table readonly-table",
    nameHeader: {content: matchTitle(), className: "sticky sticky-name battle"},
    themes,
    afterThemeHeaders: readonlyTrailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
}

function readonlyThemeHeaders() {
  const themes = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({label: `Т${theme + 1}`, questionLabels: state.questionValues});
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: `П${theme + 1}`,
      questionLabels: state.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
    });
  }
  return themes;
}

function readonlyTrailingHeaders(hasShootout) {
  const headers = [];
  if (hasShootout) headers.push({content: "П", className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function readonlyThemeCells(teamIndex, theme, themeIndex, isShootout) {
  const playerCell = document.createElement("td");
  playerCell.colSpan = state.questionValues.length;
  playerCell.className = "readonly-player theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }
  playerCell.textContent = theme.player || "";
  const answers = theme.answers.map((mark, answerIndex) => {
    const className = answerIndex === 0
      ? `answer-cell theme-block theme-block-bottom-left ${mark}`
      : `answer-cell theme-block ${mark}`;
    const cell = td("", className);
    cell.dataset.team = String(teamIndex);
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    if (isShootout) {
      cell.classList.add("shootout-block");
    }
    return cell;
  });
  return {
    playerCell,
    scoreCell: td(theme.score, "number theme-score theme-block theme-block-score", {
      rowSpan: 2,
      dataset: {team: teamIndex, shootout: isShootout ? "1" : "0", theme: themeIndex},
    }),
    answers,
  };
}

function readonlyTrailingCells(team, teamIndex, hasShootout) {
  const cells = [];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    cells.push(td(shootoutTotal, "number tiebreak-cell", {rowSpan: 2, dataset: {team: teamIndex}}));
  }
  cells.push(td(team.plus, "number plus-cell", {rowSpan: 2, dataset: {team: teamIndex}}));
  [0, 1, 2, 3, 4].forEach((idx) => {
    cells.push(td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {
      rowSpan: 2,
      dataset: {team: teamIndex, valueIndex: idx},
    }));
  });
  return cells;
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
  const prefix = path.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const festID = prefix[1];
  const gameID = prefix[2];
  const base = `/fest/${festID}/game/${gameID}`;
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  const rest = path.slice(prefix[0].length);
  const stripped = rest.replace(/\/$/, "");
  if (stripped === "" || stripped === "/") {
    return {mode: "grid", festID, gameID, base, apiBase};
  }
  if (stripped === "/venues") return {mode: "venues", festID, gameID, base, apiBase};
  const match = stripped.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), festID, gameID, base, apiBase};
  const stage = stripped.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), festID, gameID, base, apiBase};
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

function pageTitle(primary = "") {
  const main = String(primary || currentGameTitle() || state?.title || "").trim();
  const festTitle = String(fest?.title || "").trim();
  if (main && festTitle) return `${main} · ${festTitle}`;
  return main || festTitle || "Фест";
}

function currentGameTitle() {
  const scheme = parseScheme(fest?.schemaJson);
  return String(scheme?.title || "").trim();
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
  return matchTitleFor(state);
}

function matchTitleFor(matchState) {
  const venue = matchState?.venue ? ` · пл. ${matchState.venue.number}: ${matchState.venue.title}` : "";
  return `${matchState?.title || ""}${venue}`;
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
  return gameTable.th(content, className);
}

function td(content, className, attrs = {}) {
  return gameTable.td(content, className, attrs);
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
