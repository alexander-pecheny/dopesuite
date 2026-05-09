const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");

let state = null;
let activeCell = {team: 0, shootout: false, theme: 0, answer: 0};

document.addEventListener("keydown", handleGlobalKeydown);

async function loadState() {
  const response = await fetch("/api/state");
  if (!response.ok) throw new Error(await response.text());
  state = await response.json();
  render();
}

function connectEvents() {
  const events = new EventSource("/events");
  events.addEventListener("state", (event) => {
    state = JSON.parse(event.data);
    render();
    setStatus("saved");
  });
  events.onerror = () => setStatus("reconnecting");
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

async function sendUpdate(payload) {
  setStatus("saving");
  try {
    const response = await fetch("/api/update", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload),
    });
    if (!response.ok) throw new Error(await response.text());
    state = await response.json();
    render();
    setStatus("saved");
  } catch (error) {
    setStatus("error");
    console.error(error);
  }
}

function render() {
  if (!state) return;
  normalizeActiveCell();
  const focusedPlaceTeam = focusedPlaceTeamIndex();
  const finishToggleFocused = isFinishToggleFocused();
  hostRoot.replaceChildren(buildTable());
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

function buildTable() {
  const table = document.createElement("table");
  table.className = "match-table";
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
      sendUpdate({team: teamIndex, place});
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
        focusPlaceInput(nextTeam, {select: true});
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
  team.roster.forEach((player) => select.appendChild(option(player, player)));
  if (theme.player && !team.roster.includes(theme.player)) {
    select.appendChild(option(theme.player, theme.player));
  }
  select.value = theme.player;
  select.disabled = state.finished;
  select.addEventListener("change", () => {
    const payload = {team: teamIndex, theme: themeIndex, player: select.value};
    if (isShootout) payload.shootout = true;
    sendUpdate(payload);
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
    cell.dataset.shootout = isShootout ? "1" : "0";
    cell.dataset.theme = String(themeIndex);
    cell.dataset.answer = String(answerIndex);
    cell.title = `${team.name}, ${isShootout ? "П" : "Т"}${themeIndex + 1}, ${state.questionValues[answerIndex]}`;
    if (!state.finished) {
      cell.addEventListener("click", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex);
      });
      cell.addEventListener("focus", () => {
        selectAnswerCell(teamIndex, isShootout, themeIndex, answerIndex, {focus: false});
      });
    }
    answerRow.appendChild(cell);
  });
  answerRow.appendChild(td("", gapClass));
}

function battleHeader() {
  const node = document.createElement("th");
  node.className = "sticky sticky-name battle";

  const layout = document.createElement("span");
  layout.className = "battle-layout";

  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = state.title;
  layout.appendChild(title);

  const label = document.createElement("label");
  label.className = "finish-control";

  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(state.finished);
  checkbox.addEventListener("change", () => {
    sendUpdate({finished: checkbox.checked});
  });

  const text = document.createElement("span");
  text.textContent = "Закончен";

  label.append(checkbox, text);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function shootoutControlsHeader() {
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
    activeCell = {team: 0, shootout: true, theme: shootoutThemeCount(), answer: 0};
    sendUpdate({action: "addShootoutTheme"});
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
      removeLastShootoutTheme();
    });
    node.appendChild(deleteButton);
  }

  return node;
}

function handleGlobalKeydown(event) {
  if (!state || isFormControl(event.target)) return;

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
}

function isFormControl(target) {
  return target instanceof HTMLInputElement ||
    target instanceof HTMLSelectElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLButtonElement;
}

function selectAnswerCell(team, shootout, theme, answer, options = {}) {
  activeCell = {team, shootout, theme, answer};
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
  sendUpdate(payload);
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
  const input = document.querySelector(`.place-input[data-team="${team}"]`);
  if (!input) return;
  input.focus({preventScroll: options.preventScroll});
  if (options.select) input.select();
}

function focusFinishToggle(options = {}) {
  const input = document.querySelector(".finish-toggle");
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

function findActiveCell() {
  return document.querySelector(
    `.answer-cell[data-team="${activeCell.team}"][data-shootout="${activeCell.shootout ? "1" : "0"}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActiveCell(team, shootout, theme, answer) {
  return activeCell.team === team &&
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
    return {team, shootout: false, theme: themeOffset, answer};
  }
  return {team, shootout: true, theme: themeOffset - regularThemeCount(), answer};
}

function removeLastShootoutTheme() {
  const lastTheme = shootoutThemeCount() - 1;
  if (lastTheme < 0) return;
  if (activeCell.shootout && activeCell.theme >= lastTheme) {
    if (lastTheme > 0) {
      activeCell = {...activeCell, theme: lastTheme - 1};
    } else {
      activeCell = {team: activeCell.team, shootout: false, theme: regularThemeCount() - 1, answer: 0};
    }
  }
  sendUpdate({action: "removeShootoutTheme"});
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

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
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

loadState()
  .then(() => {
    setStatus("saved");
    connectEvents();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
