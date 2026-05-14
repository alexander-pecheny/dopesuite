const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const viewerLink = document.querySelector(".viewer-link");

const route = currentRoute();
const embedded = new URLSearchParams(window.location.search).get("embed") === "1";
let state = null;
let tournament = null;
let venues = [];
let stageStates = [];
let renderMatchCode = null;
let activeCell = {matchCode: "", team: 0, shootout: false, theme: 0, answer: 0};
let reloadTimer = null;

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
  const [tournamentResponse, venuesResponse] = await Promise.all([
    fetch(route.apiBase),
    fetch(`${route.tournamentApi}/venues`),
  ]);
  if (!tournamentResponse.ok) throw new Error(await tournamentResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  tournament = await tournamentResponse.json();
  venues = await venuesResponse.json();
  const stage = findStage(tournament, route.stageCode);
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
    fetch(`${route.tournamentApi}/venues`),
  ]);
  if (!matchResponse.ok) throw new Error(await matchResponse.text());
  if (!venuesResponse.ok) throw new Error(await venuesResponse.text());
  state = await matchResponse.json();
  venues = await venuesResponse.json();
  render();
}

async function loadVenuesPage() {
  const response = await fetch(`${route.tournamentApi}/venues`);
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
  const parsed = JSON.parse(raw);
  if (parsed && typeof parsed.scope === "string" && Object.prototype.hasOwnProperty.call(parsed, "data")) {
    return parsed;
  }
  return {scope: "unknown", data: parsed};
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
    if (route.mode === "stage") {
      replaceStageState(updated);
      renderStage({preserveScroll: true});
    } else {
      state = updated;
      render();
    }
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
    if (route.mode === "stage") {
      replaceStageState(updated);
      renderStage({preserveScroll: true});
    } else {
      state = updated;
      render();
    }
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

async function updateVenueTitle(number, title) {
  setStatus("saving");
  try {
    const response = await fetch(`${route.tournamentApi}/venues/${encodeURIComponent(number)}`, {
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

function renderTournament() {
  if (!tournament) return;
  setHostMode("grid");
  setHeading(tournament.title);
  setViewerLink(route.viewerBase + "/", "Открыть зрительскую сетку");
  document.title = `Ведущий · ${tournament.title}`;
  hostRoot.replaceChildren(buildTournamentGrid(tournament, {basePath: route.base}));
}

function renderStage(options = {}) {
  if (!tournament) return;
  const scrollFrame = hostRoot.closest(".sheet-frame");
  const scrollTop = scrollFrame?.scrollTop || 0;
  const scrollLeft = scrollFrame?.scrollLeft || 0;
  const stage = findStage(tournament, route.stageCode);
  setHostMode("match");
  setHeading(stage?.title || tournament.title);
  setViewerLink(`${route.viewerBase}/stage/${encodeURIComponent(route.stageCode)}`, "Открыть этап для зрителя");
  document.title = `Ведущий · ${stage?.title || tournament.title}`;
  hostRoot.replaceChildren(buildStageTables());
  if (options.preserveScroll && scrollFrame) {
    scrollFrame.scrollTop = scrollTop;
    scrollFrame.scrollLeft = scrollLeft;
  }
}

function renderVenues() {
  setHostMode("grid");
  setHeading("Площадки");
  setViewerLink(`${route.viewerBase}/venues`, "Открыть площадки для зрителя");
  document.title = "Ведущий · Площадки";
  hostRoot.replaceChildren(buildSubnav([{href: route.base + "/", label: "Сетка"}]), buildVenuesTable(true));
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

function buildVenuesTable(editable) {
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

function buildTable() {
  const matchCode = currentMatchCode();
  const table = document.createElement("table");
  table.className = "match-table";
  table.dataset.matchCode = matchCode;
  table.classList.toggle("match-finished", state.finished);
  const columnsPerTheme = state.questionValues.length + 2;
  const hasShootout = shootoutThemeCount() > 0;
  const totalColumnSpan = 5 + totalThemeCount() * columnsPerTheme + (hasShootout ? 7 : 6);

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(battleHeader());
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));
  header.appendChild(th("", "sticky sticky-place-gap place-gap-head"));

  for (let theme = 0; theme < regularThemeCount(); theme++) {
    for (const value of state.questionValues) {
      header.appendChild(th(value, "question-head"));
    }
    header.appendChild(th(`Т${theme + 1}`, "theme-head"));
    const gapClass = isLastRenderedTheme(false, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head";
    header.appendChild(th("", gapClass));
  }
  for (let theme = 0; theme < shootoutThemeCount(); theme++) {
    for (const value of state.questionValues) {
      header.appendChild(th(value, "question-head shootout-head"));
    }
    header.appendChild(th(`П${theme + 1}`, "theme-head shootout-head"));
    const gapClass = isLastRenderedTheme(true, theme) ? "gap-head shootout-adjacent-gap-head" : "gap-head";
    header.appendChild(th("", gapClass));
  }
  header.appendChild(shootoutControlsHeader());
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

    const placeInput = document.createElement("input");
    placeInput.type = "text";
    placeInput.inputMode = "decimal";
    placeInput.value = formatPlace(team.place);
    placeInput.className = "place-input";
    placeInput.disabled = state.finished;
    placeInput.dataset.matchCode = matchCode;
    placeInput.dataset.team = String(teamIndex);
    placeInput.dataset.committedPlace = String(team.place || 0);
    const commitPlace = () => {
      const place = parsePlace(placeInput.value);
      if (place === null) {
        placeInput.value = formatPlace(team.place);
        return;
      }
      placeInput.value = formatPlace(place);
      if (place === Number(placeInput.dataset.committedPlace)) {
        return true;
      }
      placeInput.dataset.committedPlace = String(place);
      sendUpdate({team: teamIndex, place}, matchCode);
      return true;
    };
    placeInput.addEventListener("change", commitPlace);
    placeInput.addEventListener("keydown", (event) => {
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
    const placeCell = document.createElement("td");
    placeCell.className = "sticky sticky-place number place-cell";
    placeCell.rowSpan = 2;
    placeCell.appendChild(placeInput);
    playerRow.appendChild(placeCell);
    playerRow.appendChild(td("", "sticky sticky-place-gap place-gap", {rowSpan: 2}));

    team.themes.forEach((theme, themeIndex) => {
      appendThemeCells(playerRow, answerRow, team, teamIndex, theme, themeIndex, false);
    });
    shootoutThemesFor(team).forEach((theme, themeIndex) => {
      appendThemeCells(playerRow, answerRow, team, teamIndex, theme, themeIndex, true);
    });
    playerRow.appendChild(td("", "shootout-controls-cell", {rowSpan: 2}));

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

function appendThemeCells(playerRow, answerRow, team, teamIndex, theme, themeIndex, isShootout) {
  const matchCode = currentMatchCode();
  const playerCell = document.createElement("td");
  playerCell.colSpan = 5;
  playerCell.className = "player-cell theme-block theme-block-top-left";
  if (isShootout) {
    playerCell.classList.add("shootout-block");
  }

  const editor = document.createElement("div");
  editor.className = "player-editor";

  const selectWrap = document.createElement("span");
  selectWrap.className = "player-select-wrap";
  const select = document.createElement("select");
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
  playerRow.appendChild(playerCell);
  playerRow.appendChild(td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2}));
  const gapClass = isLastRenderedTheme(isShootout, themeIndex) ? "gap shootout-adjacent-gap" : "gap";
  playerRow.appendChild(td("", gapClass));

  theme.answers.forEach((mark, answerIndex) => {
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
    answerRow.appendChild(cell);
  });
  answerRow.appendChild(td("", gapClass));
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
  return target instanceof HTMLInputElement ||
    target instanceof HTMLSelectElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLButtonElement;
}

function selectAnswerCell(team, shootout, theme, answer, options = {}) {
  activeCell = {matchCode: options.matchCode || currentMatchCode(), team, shootout, theme, answer};
  markActiveCell();
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
  document.querySelectorAll(".answer-cell.active").forEach((cell) => cell.classList.remove("active"));
  const cell = findActiveCell();
  if (cell) cell.classList.add("active");
}

function focusActiveCell(options = {}) {
  const cell = findActiveCell();
  if (cell) cell.focus(options);
}

function focusPlaceInput(team, options = {}) {
  const matchCode = options.matchCode || currentMatchCode();
  const input = document.querySelector(`.place-input[data-match-code="${cssEscape(matchCode)}"][data-team="${team}"]`);
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
  const prefix = path.match(/^\/host\/tournament\/(\d+)\/game\/(\d+)/);
  if (!prefix) {
    return {mode: "missing"};
  }
  const tournamentID = prefix[1];
  const gameID = prefix[2];
  const base = `/host/tournament/${tournamentID}/game/${gameID}`;
  const viewerBase = `/tournament/${tournamentID}/game/${gameID}`;
  const apiBase = `/api/tournament/${tournamentID}/games/${gameID}`;
  const tournamentApi = `/api/tournament/${tournamentID}`;
  const rest = path.slice(prefix[0].length).replace(/\/$/, "");
  if (rest === "" || rest === "/") {
    return {mode: "grid", tournamentID, gameID, base, viewerBase, apiBase, tournamentApi};
  }
  if (rest === "/venues") return {mode: "venues", tournamentID, gameID, base, viewerBase, apiBase, tournamentApi};
  const match = rest.match(/^\/matches\/([^/]+)$/);
  if (match) return {mode: "match", matchCode: decodeURIComponent(match[1]), tournamentID, gameID, base, viewerBase, apiBase, tournamentApi};
  const stage = rest.match(/^\/stage\/([^/]+)$/);
  if (stage) return {mode: "stage", stageCode: decodeURIComponent(stage[1]), tournamentID, gameID, base, viewerBase, apiBase, tournamentApi};
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

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function cssEscape(value) {
  return window.CSS?.escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&");
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

function option(value, label) {
  const node = document.createElement("option");
  node.value = value;
  node.textContent = label;
  return node;
}

loadCurrent()
  .then(() => {
    setStatus("saved");
    connectEvents();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
