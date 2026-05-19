const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerLink = document.querySelector(".viewer-link");

const gameTable = window.DopeTable;
const route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let fest = null;
let venues = [];
let stageStates = [];
let renderMatchCode = null;
let activeCell = {matchCode: "", team: 0, shootout: false, theme: 0, answer: 0};
let reloadTimer = null;
const localMatchEchoes = new Set();
let matchTableIndex = null;
let activeAnswerNode = null;
let activeTeamRows = [];
let presence = null;

document.body.classList.toggle("embedded-match", embedded);
document.addEventListener("keydown", handleGlobalKeydown);

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
  const [festResponse, venuesResponse] = await Promise.all([
    fetch(route.apiBase),
    fetch(`${route.festApi}/venues`),
  ]);
  if (!festResponse.ok) throw new Error(await festResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  fest = await festResponse.json();
  venues = await venuesResponse.json();
  const stage = findStage(fest, route.stageCode);
  const matches = stage?.matches || [];
  stageStates = await Promise.all(matches.map(async (match) => {
    const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(match.code)}`);
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  }));
  renderStage();
}

async function loadMatch() {
  const [matchResponse, venuesResponse] = await Promise.all([
    fetch(`${route.apiBase}/matches/${encodeURIComponent(route.matchCode)}`),
    fetch(`${route.festApi}/venues`),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  state = await matchResponse.json();
  venues = await venuesResponse.json();
  render();
}

async function loadVenuesPage() {
  const response = await fetch(`${route.festApi}/venues`);
  if (!response.ok) throw new Error(await response.text());
  venues = await response.json();
  renderVenues();
}

function connectEvents() {
  const events = new EventSource(`/events?fest_id=${encodeURIComponent(route.festID)}`);
  const matchScope = `match:${route.gameID}:${route.matchCode}`;
  const venuesScope = `venues:${route.festID}`;
  events.addEventListener("state", (event) => {
    const message = parseEventData(event.data);
    if (consumeLocalMatchEcho(message)) {
      setStatus("saved");
      return;
    }
    if (route.mode === "match" && message.scope === matchScope) {
      applyUpdatedMatch(message.data, route.matchCode);
      setStatus("saved");
      return;
    }
    if (route.mode === "venues" && message.scope === venuesScope) {
      venues = message.data;
      renderVenues();
      setStatus("saved");
      return;
    }
    if (route.mode === "stage" && message.scope.startsWith("match:")) {
      scheduleReload();
      return;
    }
    scheduleReload();
  });
  events.onerror = () => setStatus("reconnecting");
}

function scheduleReload() {
  window.clearTimeout(reloadTimer);
  reloadTimer = window.setTimeout(() => {
    loadCurrent()
      .then(() => setStatus("saved"))
      .catch((error) => {
        setStatus("error");
        console.error(error);
      });
  }, 120);
}

function parseEventData(raw) {
  return gameTable.parseScopedEvent(raw);
}

function matchScopeFor(matchCode) {
  return `match:${route.gameID}:${matchCode}`;
}

function matchEchoKey(scope, revision) {
  return `${scope}:${revision || 0}`;
}

function rememberLocalMatchEcho(matchCode, updated) {
  if (!updated?.revision) return;
  localMatchEchoes.add(matchEchoKey(matchScopeFor(matchCode), updated.revision));
}

function consumeLocalMatchEcho(message) {
  if (!message?.scope?.startsWith("match:")) return false;
  const key = matchEchoKey(message.scope, message.revision);
  if (!localMatchEchoes.has(key)) return false;
  localMatchEchoes.delete(key);
  return true;
}

function setStatus(state) {
  const labels = {
    saved: "Синхронизировано",
    saving: "Синхронизация",
    reconnecting: "Переподключение",
    error: "Ошибка синхронизации",
  };
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state] || labels.saving);
  statusNode.title = labels[state] || labels.saving;
}

async function sendUpdate(payload, matchCode = currentMatchCode()) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(matchCode)}/update`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
    });
    if (!response.ok) throw new Error(await response.text());
    const updated = await response.json();
    rememberLocalMatchEcho(matchCode, updated);
    applyUpdatedMatch(updated, matchCode);
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

async function sendVenueChange(number, matchCode = currentMatchCode()) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(matchCode)}/venue`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({number}),
    });
    if (!response.ok) throw new Error(await response.text());
    const updated = await response.json();
    rememberLocalMatchEcho(matchCode, updated);
    applyUpdatedMatch(updated, matchCode);
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

async function updateVenueTitle(number, title) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.festApi}/venues/${encodeURIComponent(number)}`, {
      method: "PUT",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({title}),
    });
    if (!response.ok) throw new Error(await response.text());
    venues = await response.json();
    renderVenues();
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

function renderFest() {
  if (!fest) return;
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading(fest.title);
  setViewerLink(route.viewerBase + "/", "Открыть зрительскую сетку");
  document.title = `Ведущий · ${fest.title}`;
  hostRoot.replaceChildren(buildFestGrid(fest, {basePath: route.base}));
  refreshPresence();
}

function renderStage(options = {}) {
  if (!fest) return;
  resetMatchTableIndex();
  const scrollFrame = hostRoot.closest(".sheet-frame");
  const scrollTop = scrollFrame?.scrollTop || 0;
  const scrollLeft = scrollFrame?.scrollLeft || 0;
  const stage = findStage(fest, route.stageCode);
  setHostMode("match");
  setHeading(stage?.title || fest.title);
  setViewerLink(`${route.viewerBase}/stage/${encodeURIComponent(route.stageCode)}`, "Открыть этап для зрителя");
  document.title = `Ведущий · ${stage?.title || fest.title}`;
  hostRoot.replaceChildren(buildStageTables());
  if (options.preserveScroll && scrollFrame) {
    scrollFrame.scrollTop = scrollTop;
    scrollFrame.scrollLeft = scrollLeft;
  }
  refreshPresence();
}

function renderVenues() {
  resetMatchTableIndex();
  setHostMode("grid");
  setHeading("Площадки");
  setViewerLink(`${route.viewerBase}/venues`, "Открыть площадки для зрителя");
  document.title = "Ведущий · Площадки";
  hostRoot.replaceChildren(buildSubnav([{href: route.base + "/", label: "Сетка"}]), buildVenuesTable(true));
  refreshPresence();
}

function render() {
  if (!state) return;
  setHostMode("match");
  normalizeActiveCell();
  setHeading(state.stageTitle || state.title);
  setViewerLink(`${route.viewerBase}/matches/${encodeURIComponent(state.code || route.matchCode)}`, "Открыть зрительский бой");
  document.title = `Ведущий · ${state.title}`;

  const focusedPlaceTeam = focusedPlaceTeamIndex();
  const finishToggleFocused = isFinishToggleFocused();
  const venueFocused = isVenueSelectFocused();
  const table = buildTable();
  matchTableIndex = gameTable.createScoreTableIndex(table, {entity: "team", shootout: true});
  activeAnswerNode = state.finished ? null : matchTableIndex.get("answer", activeCell);
  markActiveTeamRows(activeAnswerNode);
  if (embedded) {
    hostRoot.replaceChildren(table);
    notifyEmbeddedResize();
  } else {
    hostRoot.replaceChildren(
      buildSubnav([
        {href: route.base + "/", label: "Сетка"},
        {href: route.base + "/venues", label: "Площадки"},
      ]),
      table,
    );
  }
  refreshPresence();
  if (venueFocused) {
    focusVenueSelect({preventScroll: true});
    return;
  }
  if (finishToggleFocused) {
    focusFinishToggle({preventScroll: true});
    return;
  }
  if (!state.finished && focusedPlaceTeam !== null) {
    focusPlaceInput(focusedPlaceTeam, {preventScroll: true});
    return;
  }
  if (state.finished) return;
  focusActiveCell({preventScroll: true});
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

function buildVenuesTable(editable) {
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
    const titleCell = document.createElement("td");
    if (editable) {
      const input = document.createElement("input");
      input.className = "venue-input";
      input.value = venue.title;
      input.dataset.committedTitle = venue.title;
      input.addEventListener("change", () => {
        const title = input.value.trim();
        if (!title) {
          input.value = input.dataset.committedTitle;
          return;
        }
        if (title === input.dataset.committedTitle) return;
        input.dataset.committedTitle = title;
        updateVenueTitle(venue.number, title);
      });
      titleCell.appendChild(input);
    } else {
      titleCell.textContent = venue.title;
    }
    row.appendChild(titleCell);
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

function buildStageTables() {
  const wrapper = document.createElement("div");
  wrapper.className = "stage-table-stack";
  stageStates.forEach((matchState) => {
    wrapper.appendChild(withMatchState(matchState, () => buildTable()));
  });
  return wrapper;
}

function withMatchState(matchState, callback) {
  const previousState = state;
  const previousCode = renderMatchCode;
  state = matchState;
  renderMatchCode = matchState.code || route.matchCode;
  try {
    return callback();
  } finally {
    state = previousState;
    renderMatchCode = previousCode;
  }
}

function currentMatchCode() {
  return renderMatchCode || activeCell.matchCode || route.matchCode;
}

function activeMatchState() {
  if (route.mode === "stage") {
    return stageStates.find((matchState) => matchState.code === activeCell.matchCode) || stageStates[0] || null;
  }
  return state;
}

function replaceStageState(updated) {
  stageStates = stageStates.map((matchState) => matchState.code === updated.code ? updated : matchState);
}

function applyUpdatedMatch(updated, matchCode) {
  if (route.mode === "stage") {
    replaceStageState(updated);
    renderStage({preserveScroll: true});
    return;
  }
  const previous = state;
  state = updated;
  if (canPatchMatchTable(previous, updated)) {
    normalizeActiveCell();
    patchMatchTable(matchCode);
    return;
  }
  render();
}

function canPatchMatchTable(previous, next) {
  if (route.mode !== "match" || !matchTableIndex || !previous || !next) return false;
  if (previous.code !== next.code || previous.title !== next.title || previous.finished !== next.finished) return false;
  if (formatVenue(previous.venue) !== formatVenue(next.venue)) return false;
  if (!sameArray(previous.questionValues, next.questionValues)) return false;
  if ((previous.teams || []).length !== (next.teams || []).length) return false;
  for (let i = 0; i < next.teams.length; i++) {
    const prevTeam = previous.teams[i];
    const nextTeam = next.teams[i];
    if (prevTeam.name !== nextTeam.name) return false;
    if ((prevTeam.themes || []).length !== (nextTeam.themes || []).length) return false;
    if (shootoutThemesFor(prevTeam).length !== shootoutThemesFor(nextTeam).length) return false;
  }
  return true;
}

function patchMatchTable(matchCode) {
  state.teams.forEach((team, teamIndex) => {
    setIndexedText("total", {team: teamIndex}, team.total);
    setIndexedText("plus", {team: teamIndex}, team.plus);
    setIndexedText("tiebreak", {team: teamIndex}, team.shootoutTotal ?? team.tiebreak);
    const placeInput = indexedNode("placeInput", {team: teamIndex}) ||
      document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"]`);
    if (placeInput && document.activeElement !== placeInput) {
      placeInput.value = formatPlace(team.place);
    }
    if (placeInput) {
      placeInput.dataset.committedPlace = String(team.place || 0);
    }
    [0, 1, 2, 3, 4].forEach((idx) => {
      setIndexedText("correctCount", {team: teamIndex, valueIndex: idx}, team.correctCounts[4 - idx]);
    });
    team.themes.forEach((theme, themeIndex) => {
      patchTheme(teamIndex, themeIndex, false, theme, matchCode);
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      patchTheme(teamIndex, themeIndex, true, theme, matchCode);
    });
  });
  markActiveCell();
}

function patchTheme(teamIndex, themeIndex, isShootout, theme, matchCode) {
  const shootoutValue = isShootout ? "1" : "0";
  const select = indexedNode("playerSelect", {team: teamIndex, shootout: shootoutValue, theme: themeIndex}) ||
    document.querySelector(`.player-select[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"][data-shootout="${shootoutValue}"][data-theme="${themeIndex}"]`);
  if (select && document.activeElement !== select) {
    if (theme.player && !Array.from(select.options).some((item) => item.value === theme.player)) {
      select.appendChild(option(theme.player, theme.player));
    }
    select.value = theme.player || "";
  }
  setIndexedText("themeScore", {team: teamIndex, shootout: shootoutValue, theme: themeIndex}, theme.score);
  theme.answers.forEach((mark, answerIndex) => {
    const cell = indexedNode("answer", {team: teamIndex, shootout: shootoutValue, theme: themeIndex, answer: answerIndex}) ||
      document.querySelector(`.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${teamIndex}"][data-shootout="${shootoutValue}"][data-theme="${themeIndex}"][data-answer="${answerIndex}"]`);
    gameTable.setMarkClass(cell, mark);
  });
}

function setIndexedText(name, values, value) {
  const node = indexedNode(name, values);
  if (node) gameTable.setNodeText(node, value, formatNumber);
}

function indexedNode(name, values) {
  if (route.mode !== "match") return null;
  return matchTableIndex?.get(name, values) || null;
}

function resetMatchTableIndex() {
  matchTableIndex = null;
  activeAnswerNode = null;
  clearActiveTeamRows();
}

function buildTable() {
  const matchCode = currentMatchCode();
  const hasShootout = shootoutThemeCount() > 0;
  const showPlaceColumn = false;
  const themes = renderedThemeHeaders();
  const rows = state.teams.map((team, teamIndex) => {
    const themeCellsList = [];
    team.themes.forEach((theme, themeIndex) => {
      themeCellsList.push(themeCells(team, teamIndex, theme, themeIndex, false));
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      themeCellsList.push(themeCells(team, teamIndex, theme, themeIndex, true));
    });
    return {
      rowClassName: isActiveMatchRow(matchCode, teamIndex) ? "active-team-row" : "",
      nameCell: teamNameCell(team, teamIndex),
      totalCell: totalCell(team, teamIndex),
      placeCell: showPlaceColumn ? placeCell(team, teamIndex, matchCode) : null,
      themes: themeCellsList,
      afterThemeCells: trailingCells(team, teamIndex, hasShootout),
    };
  });

  const table = gameTable.buildTwoRowScoreTable({
    className: "match-table",
    attrs: {dataset: {matchCode}},
    rowMarkerColumn: true,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
    nameHeader: battleHeader(),
    placeColumn: showPlaceColumn,
    themes,
    afterThemeHeaders: trailingHeaders(hasShootout),
    rows,
    gapRowClassName: "team-gap-row",
  });
  table.classList.toggle("match-finished", state.finished);
  return table;
}

function renderedThemeHeaders() {
  const themes = [];
  for (let theme = 0; theme < regularThemeCount(); theme++) {
    themes.push({
      label: `Т${theme + 1}`,
      questionLabels: state.questionValues,
      gapHeaderClassName: isLastRenderedTheme(false, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(false, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    themes.push({
      label: `П${theme + 1}`,
      questionLabels: state.questionValues,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
      gapHeaderClassName: isLastRenderedTheme(true, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head",
      gapClassName: isLastRenderedTheme(true, theme) ? "gap shootout-adjacent-gap" : "gap",
    });
  }
  return themes;
}

function trailingHeaders(hasShootout) {
  const headers = [shootoutControlsHeader()];
  if (hasShootout) headers.push({content: "П", className: "number"});
  headers.push({content: "Σ+", className: "number"});
  for (const value of [50, 40, 30, 20, 10]) {
    headers.push({content: value, className: "number narrow"});
  }
  return headers;
}

function teamNameCell(team, teamIndex) {
  const cell = td(team.name, "sticky sticky-name team-name", {rowSpan: 2});
  cell.dataset.team = String(teamIndex);
  return cell;
}

function totalCell(team, teamIndex) {
  const cell = td(team.total, "sticky sticky-total number total-cell", {rowSpan: 2});
  cell.dataset.team = String(teamIndex);
  return cell;
}

function placeCell(team, teamIndex, matchCode) {
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "decimal";
  input.value = formatPlace(team.place);
  input.className = "place-input";
  input.disabled = state.finished;
  input.dataset.matchCode = matchCode;
  input.dataset.team = String(teamIndex);
  input.dataset.committedPlace = String(team.place || 0);
  const commitPlace = () => {
    const place = parsePlace(input.value);
    if (place === null) {
      input.value = formatPlace(team.place);
      return;
    }
    input.value = formatPlace(place);
    if (place === Number(input.dataset.committedPlace)) {
      return true;
    }
    input.dataset.committedPlace = String(place);
    sendUpdate({team: teamIndex, place}, matchCode);
    return true;
  };
  input.addEventListener("change", commitPlace);
  input.addEventListener("keydown", (event) => {
    const isForward = event.key === "ArrowDown" || (event.key === "Tab" && !event.shiftKey);
    const isBackward = event.key === "ArrowUp" || (event.key === "Tab" && event.shiftKey);
    if (event.key !== "Enter" && !isForward && !isBackward) return;

    event.preventDefault();
    if (!commitPlace()) return;
    if (isForward || isBackward) {
      const direction = isForward ? 1 : -1;
      const nextTeam = clamp(teamIndex + direction, 0, state.teams.length - 1);
      focusPlaceInput(nextTeam, {select: true, matchCode});
    }
  });
  const cell = document.createElement("td");
  cell.className = "sticky sticky-place number place-cell";
  cell.rowSpan = 2;
  cell.dataset.team = String(teamIndex);
  cell.appendChild(input);
  return cell;
}

function themeCells(team, teamIndex, theme, themeIndex, isShootout) {
  const matchCode = currentMatchCode();
  const playerCell = document.createElement("td");
  playerCell.colSpan = state.questionValues.length;
  playerCell.className = "player-cell theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }

  const editor = document.createElement("div");
  editor.className = "player-editor";

  const selectWrap = document.createElement("span");
  selectWrap.className = "player-select-wrap";
  const select = document.createElement("select");
  select.className = "player-select";
  select.dataset.matchCode = matchCode;
  select.dataset.team = String(teamIndex);
  select.dataset.shootout = isShootout ? "1" : "0";
  select.dataset.theme = String(themeIndex);
  select.appendChild(option("", ""));
  const roster = team.roster || [];
  roster.forEach((player) => select.appendChild(option(player, player)));
  if (theme.player && !roster.includes(theme.player)) {
    select.appendChild(option(theme.player, theme.player));
  }
  select.value = theme.player;
  select.disabled = state.finished;
  select.addEventListener("change", () => {
    const payload = {team: teamIndex, theme: themeIndex, player: select.value};
    if (isShootout) payload.shootout = true;
    sendUpdate(payload, matchCode);
  });
  selectWrap.appendChild(select);
  editor.appendChild(selectWrap);

  playerCell.appendChild(editor);
  const scoreCell = td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2});
  scoreCell.dataset.team = String(teamIndex);
  scoreCell.dataset.shootout = isShootout ? "1" : "0";
  scoreCell.dataset.theme = String(themeIndex);
  const gapClass = isLastRenderedTheme(isShootout, themeIndex) ? "gap shootout-adjacent-gap" : "gap";

  const answers = theme.answers.map((mark, answerIndex) => {
    const cell = document.createElement("td");
    cell.className = `answer-cell theme-block ${mark}`;
    if (isShootout) {
      cell.classList.add("shootout-block");
    }
    if (answerIndex === 0) {
      cell.classList.add("theme-block-bottom-left");
    }
    if (!state.finished && isActiveCell(teamIndex, isShootout, themeIndex, answerIndex)) {
      cell.classList.add("active");
    }
    cell.tabIndex = state.finished ? -1 : 0;
    cell.dataset.team = String(teamIndex);
    cell.dataset.matchCode = matchCode;
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    cell.title = `${team.name}, ${isShootout ? "П" : "Т"}${themeIndex + 1}, ${state.questionValues[answerIndex]}`;
    if (!state.finished) {
      cell.addEventListener("click", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {matchCode});
      });
      cell.addEventListener("focus", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {focus: false, matchCode});
      });
    }
    return cell;
  });

  return {playerCell, scoreCell, gapClassName: gapClass, answers};
}

function trailingCells(team, teamIndex, hasShootout) {
  const cells = [td("", "shootout-controls-cell", {rowSpan: 2})];
  if (hasShootout) {
    const shootoutTotal = team.shootoutTotal ?? team.tiebreak;
    const tiebreakCell = td(shootoutTotal, "number tiebreak-cell", {rowSpan: 2});
    tiebreakCell.dataset.team = String(teamIndex);
    cells.push(tiebreakCell);
  }
  const plusCell = td(team.plus, "number plus-cell", {rowSpan: 2});
  plusCell.dataset.team = String(teamIndex);
  cells.push(plusCell);
  [0, 1, 2, 3, 4].forEach((idx) => {
    const correctCell = td(team.correctCounts[4 - idx], "number narrow correct-count-cell", {rowSpan: 2});
    correctCell.dataset.team = String(teamIndex);
    correctCell.dataset.valueIndex = String(idx);
    cells.push(correctCell);
  });
  return cells;
}

function battleHeader() {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "sticky sticky-name battle";

  const layout = document.createElement("span");
  layout.className = "battle-layout";

  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = matchTitle();
  layout.appendChild(title);

  if (venues.length > 0) {
    const venueSelect = document.createElement("select");
    venueSelect.className = "venue-select";
    venueSelect.dataset.matchCode = matchCode;
    venues.forEach((venue) => {
      venueSelect.appendChild(option(String(venue.number), `${venue.number}: ${venue.title}`));
    });
    venueSelect.value = state.venue ? String(state.venue.number) : "";
    venueSelect.addEventListener("change", () => {
      sendVenueChange(Number(venueSelect.value), matchCode);
    });
    layout.appendChild(venueSelect);
  }

  const label = document.createElement("label");
  label.className = "finish-control";

  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.dataset.matchCode = matchCode;
  checkbox.checked = Boolean(state.finished);
  checkbox.addEventListener("change", () => {
    sendUpdate({finished: checkbox.checked}, matchCode);
  });

  const text = document.createElement("span");
  text.textContent = "Закончен";

  label.append(checkbox, text);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function shootoutControlsHeader() {
  const matchCode = currentMatchCode();
  const node = document.createElement("th");
  node.className = "shootout-controls-head";

  const addShootout = document.createElement("button");
  addShootout.type = "button";
  addShootout.className = "shootout-add-button";
  addShootout.textContent = "+П";
  addShootout.title = "Добавить тему перестрелки";
  addShootout.setAttribute("aria-label", "Добавить тему перестрелки");
  addShootout.disabled = state.finished;
  addShootout.addEventListener("click", () => {
    activeCell = {matchCode, team: 0, shootout: true, theme: shootoutThemeCount(), answer: 0};
    sendUpdate({action: "addShootoutTheme"}, matchCode);
  });
  node.appendChild(addShootout);

  if (shootoutThemeCount() > 0) {
    const deleteButton = document.createElement("button");
    deleteButton.type = "button";
    deleteButton.className = "theme-delete-button";
    deleteButton.textContent = "−П";
    deleteButton.title = "Удалить тему перестрелки";
    deleteButton.setAttribute("aria-label", "Удалить тему перестрелки");
    deleteButton.disabled = state.finished;
    deleteButton.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (!window.confirm("Удалить тему перестрелки?")) return;
      removeLastShootoutTheme(matchCode);
    });
    node.appendChild(deleteButton);
  }

  return node;
}

function handleGlobalKeydown(event) {
  if ((route.mode !== "match" && route.mode !== "stage") || isFormControl(event.target)) return;
  const matchState = activeMatchState();
  if (!matchState) return;

  withMatchState(matchState, () => {
    const key = event.key.toLowerCase();
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      moveActiveCell(0, -1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      moveActiveCell(0, 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      moveActiveCell(-1, 0);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      moveActiveCell(1, 0);
    } else if (key === "q" || key === "й" || key === "1") {
      event.preventDefault();
      setActiveMark("right");
    } else if (key === "w" || key === "ц" || key === "-") {
      event.preventDefault();
      setActiveMark("wrong");
    } else if (key === "backspace" || key === "delete") {
      event.preventDefault();
      setActiveMark("");
    }
  });
}

function isFormControl(target) {
  return gameTable.isFormControl(target);
}

function selectAnswerCell(team, shootout, theme, answer, options = {}) {
  activeCell = {matchCode: options.matchCode || currentMatchCode(), team, shootout, theme, answer};
  markActiveCell();
  publishPresence();
  if (options.focus !== false) {
    focusActiveCell();
  }
}

function moveActiveCell(teamDelta, answerDelta) {
  const maxTeam = state.teams.length - 1;
  const maxColumn = totalThemeCount() * state.questionValues.length - 1;
  const column = activeCellColumn();
  const nextTeam = clamp(activeCell.team + teamDelta, 0, maxTeam);
  const nextColumn = clamp(column + answerDelta, 0, maxColumn);
  activeCell = cellFromColumn(nextTeam, nextColumn);
  markActiveCell();
  focusActiveCell();
}

function setActiveMark(mark) {
  if (state.finished) return;
  const payload = {
    team: activeCell.team,
    theme: activeCell.theme,
    answer: activeCell.answer,
    mark,
  };
  if (activeCell.shootout) payload.shootout = true;
  sendUpdate(payload, currentMatchCode());
}

function markActiveCell() {
  clearActiveTeamRows();
  if (route.mode === "match" && activeAnswerNode) {
    activeAnswerNode.classList.remove("active");
    activeAnswerNode = null;
  } else {
    document.querySelectorAll(".answer-cell.active").forEach((cell) => cell.classList.remove("active"));
  }
  const cell = findActiveCell();
  if (cell) {
    cell.classList.add("active");
    markActiveTeamRows(cell);
    if (route.mode === "match") activeAnswerNode = cell;
  }
}

function isActiveMatchRow(matchCode, teamIndex) {
  return !state.finished &&
    activeCell.matchCode === matchCode &&
    activeCell.team === teamIndex;
}

function clearActiveTeamRows() {
  if (activeTeamRows.length > 0) {
    activeTeamRows.forEach((row) => row.classList.remove("active-team-row"));
    activeTeamRows = [];
    return;
  }
  hostRoot.querySelectorAll(".active-team-row").forEach((row) => row.classList.remove("active-team-row"));
}

function markActiveTeamRows(cell) {
  clearActiveTeamRows();
  if (!cell) return;
  const table = cell.closest(".match-table");
  const team = cell.dataset.team;
  if (!table || team == null) return;
  const rows = new Set();
  table.querySelectorAll(`[data-team="${cssEscape(team)}"]`).forEach((node) => {
    const row = node.closest("tr");
    if (row?.parentElement?.tagName === "TBODY") rows.add(row);
  });
  activeTeamRows = Array.from(rows);
  activeTeamRows.forEach((row) => row.classList.add("active-team-row"));
}

function focusActiveCell(options = {}) {
  const cell = findActiveCell();
  if (cell) cell.focus(options);
}

function focusPlaceInput(team, options = {}) {
  const matchCode = options.matchCode || currentMatchCode();
  const input = indexedNode("placeInput", {team}) ||
    document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${team}"]`);
  if (!input) return;
  input.focus({preventScroll: options.preventScroll});
  if (options.select) input.select();
}

function focusFinishToggle(options = {}) {
  const input = document.querySelector(".finish-toggle");
  if (input) input.focus({preventScroll: options.preventScroll});
}

function focusVenueSelect(options = {}) {
  const input = document.querySelector(".venue-select");
  if (input) input.focus({preventScroll: options.preventScroll});
}

function focusedPlaceTeamIndex() {
  const element = document.activeElement;
  if (!(element instanceof HTMLInputElement) || !element.classList.contains("place-input")) {
    return null;
  }
  const team = Number(element.dataset.team);
  return Number.isInteger(team) ? team : null;
}

function isFinishToggleFocused() {
  const element = document.activeElement;
  return element instanceof HTMLInputElement && element.classList.contains("finish-toggle");
}

function isVenueSelectFocused() {
  const element = document.activeElement;
  return element instanceof HTMLSelectElement && element.classList.contains("venue-select");
}

function findActiveCell() {
  const matchCode = currentMatchCode();
  const indexed = indexedNode("answer", {
    team: activeCell.team,
    shootout: activeCell.shootout ? "1" : "0",
    theme: activeCell.theme,
    answer: activeCell.answer,
  });
  if (indexed) return indexed;
  return document.querySelector(
    `.answer-cell[data-match-code="${cssEscape(matchCode)}"][data-team="${activeCell.team}"][data-shootout="${activeCell.shootout ? "1" : "0"}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActiveCell(team, shootout, theme, answer) {
  return activeCell.matchCode === currentMatchCode() &&
    activeCell.team === team &&
    activeCell.shootout === shootout &&
    activeCell.theme === theme &&
    activeCell.answer === answer;
}

function normalizeActiveCell() {
  if (!state?.teams?.length || totalThemeCount() === 0) return;
  const team = clamp(activeCell.team, 0, state.teams.length - 1);
  const column = clamp(activeCellColumn(), 0, totalThemeCount() * state.questionValues.length - 1);
  activeCell = cellFromColumn(team, column);
}

function activeCellColumn() {
  const themeOffset = activeCell.shootout
    ? regularThemeCount() + activeCell.theme
    : activeCell.theme;
  return themeOffset * state.questionValues.length + activeCell.answer;
}

function cellFromColumn(team, column) {
  const themeOffset = Math.floor(column / state.questionValues.length);
  const answer = column % state.questionValues.length;
  if (themeOffset < regularThemeCount()) {
    return {matchCode: currentMatchCode(), team, shootout: false, theme: themeOffset, answer};
  }
  return {matchCode: currentMatchCode(), team, shootout: true, theme: themeOffset - regularThemeCount(), answer};
}

function removeLastShootoutTheme(matchCode = currentMatchCode()) {
  const lastTheme = shootoutThemeCount() - 1;
  if (lastTheme < 0) return;
  activeCell = {...activeCell, matchCode};
  if (activeCell.shootout && activeCell.theme >= lastTheme) {
    if (lastTheme > 0) {
      activeCell = {...activeCell, theme: lastTheme - 1};
    } else {
      activeCell = {matchCode: currentMatchCode(), team: activeCell.team, shootout: false, theme: regularThemeCount() - 1, answer: 0};
    }
  }
  sendUpdate({action: "removeShootoutTheme"}, matchCode);
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

function isLastRenderedTheme(isShootout, themeIndex) {
  if (isShootout) {
    return themeIndex === shootoutThemeCount() - 1;
  }
  return shootoutThemeCount() === 0 && themeIndex === regularThemeCount() - 1;
}

function currentRoute() {
  const path = window.location.pathname;
  const prefix = path.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const festID = prefix[1];
  const gameID = prefix[2];
  const base = `/host/fest/${festID}/game/${gameID}`;
  const viewerBase = `/fest/${festID}/game/${gameID}`;
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  const festApi = `/api/fest/${festID}`;
  const rest = path.slice(prefix[0].length).replace(/\/$/, "");
  if (rest === "" || rest === "/") {
    return {mode: "grid", festID, gameID, base, viewerBase, apiBase, festApi};
  }
  if (rest === "/venues") return {mode: "venues", festID, gameID, base, viewerBase, apiBase, festApi};
  const match = rest.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), festID, gameID, base, viewerBase, apiBase, festApi};
  const stage = rest.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), festID, gameID, base, viewerBase, apiBase, festApi};
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

function setViewerLink(href, title) {
  if (!viewerLink) return;
  viewerLink.href = href;
  viewerLink.title = title;
  viewerLink.setAttribute("aria-label", title);
}

function setHostMode(mode) {
  hostRoot.classList.toggle("grid-host", mode === "grid");
  hostRoot.classList.toggle("fight-host", mode === "match");
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

function sameArray(a, b) {
  return gameTable.sameArray(a, b);
}

function clamp(value, min, max) {
  return gameTable.clamp(value, min, max);
}

function cssEscape(value) {
  return gameTable.cssEscape(value);
}

function parsePlace(value) {
  const normalized = value.trim().replace(",", ".");
  if (normalized === "") return 0;
  const place = Number(normalized);
  if (!Number.isFinite(place) || place < 0) return null;
  return place;
}

function formatPlace(place) {
  return place > 0 ? String(place) : "";
}

function th(content, className) {
  return gameTable.th(content, className);
}

function td(content, className, attrs = {}) {
  return gameTable.td(content, className, attrs);
}

function connectPresence() {
  if (presence || embedded || !route.festID) return;
  presence = gameTable.createHostPresence({
    root: hostRoot,
    eventsURL: `/host-events?fest_id=${encodeURIComponent(route.festID)}`,
    presenceURL: `${route.festApi}/presence`,
    cursorFromElement: hostPresenceCursorFromElement,
    getCursor: currentHostPresenceCursor,
    findTarget: findHostPresenceTarget,
  });
  presence.connect();
}

function refreshPresence() {
  presence?.refresh();
}

function publishPresence() {
  presence?.publishCurrent();
}

function currentHostPresenceCursor() {
  const focused = hostPresenceCursorFromElement(document.activeElement);
  if (focused) return focused;
  if (route.mode !== "match" && route.mode !== "stage") return null;
  return {
    app: "ek",
    kind: "answer",
    gameID: route.gameID,
    matchCode: activeCell.matchCode || currentMatchCode(),
    team: activeCell.team,
    shootout: Boolean(activeCell.shootout),
    theme: activeCell.theme,
    answer: activeCell.answer,
  };
}

function hostPresenceCursorFromElement(element) {
  const target = element?.closest?.(".answer-cell,.player-select,.place-input,.finish-toggle,.venue-select");
  if (!target || !hostRoot.contains(target)) return null;
  const matchCode = target.dataset.matchCode || currentMatchCode();
  if (target.classList.contains("answer-cell")) {
    return {
      app: "ek",
      kind: "answer",
      gameID: route.gameID,
      matchCode,
      team: Number(target.dataset.team),
      shootout: target.dataset.shootout === "1",
      theme: Number(target.dataset.theme),
      answer: Number(target.dataset.answer),
    };
  }
  if (target.classList.contains("player-select")) {
    return {
      app: "ek",
      kind: "player",
      gameID: route.gameID,
      matchCode,
      team: Number(target.dataset.team),
      shootout: target.dataset.shootout === "1",
      theme: Number(target.dataset.theme),
    };
  }
  if (target.classList.contains("place-input")) {
    return {app: "ek", kind: "place", gameID: route.gameID, matchCode, team: Number(target.dataset.team)};
  }
  if (target.classList.contains("finish-toggle")) {
    return {app: "ek", kind: "finish", gameID: route.gameID, matchCode};
  }
  if (target.classList.contains("venue-select")) {
    return {app: "ek", kind: "venue", gameID: route.gameID, matchCode};
  }
  return null;
}

function findHostPresenceTarget(cursor) {
  if (!cursor || cursor.app !== "ek" || String(cursor.gameID) !== String(route.gameID)) return null;
  const matchCode = cssEscape(cursor.matchCode || route.matchCode || "");
  switch (cursor.kind) {
  case "answer":
    return hostRoot.querySelector(
      `.answer-cell[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"][data-shootout="${cursor.shootout ? "1" : "0"}"][data-theme="${cssEscape(cursor.theme)}"][data-answer="${cssEscape(cursor.answer)}"]`,
    );
  case "player":
    return hostRoot.querySelector(
      `.player-select[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"][data-shootout="${cursor.shootout ? "1" : "0"}"][data-theme="${cssEscape(cursor.theme)}"]`,
    );
  case "place":
    return hostRoot.querySelector(`.place-input[data-match-code="${matchCode}"][data-team="${cssEscape(cursor.team)}"]`);
  case "finish":
    return hostRoot.querySelector(`.finish-toggle[data-match-code="${matchCode}"]`);
  case "venue":
    return hostRoot.querySelector(`.venue-select[data-match-code="${matchCode}"]`);
  default:
    return null;
  }
}

function option(value, label) {
  return gameTable.option(value, label);
}

loadCurrent()
  .then(() => {
    setStatus("saved");
    connectEvents();
    connectPresence();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
