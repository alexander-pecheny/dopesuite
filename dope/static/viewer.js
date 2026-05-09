const viewerRoot = document.getElementById("viewerTable");
const liveDot = document.getElementById("liveDot");

let state = null;

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
    setLive(true);
  });
  events.onerror = () => setLive(false);
}

function setLive(ok) {
  liveDot.classList.toggle("offline", !ok);
  const label = ok ? "Трансляция активна" : "Нет соединения";
  liveDot.setAttribute("aria-label", label);
  liveDot.title = label;
}

function render() {
  if (!state) return;
  viewerRoot.replaceChildren(buildReadonlyTable());
}

function buildReadonlyTable() {
  const table = document.createElement("table");
  table.className = "match-table readonly-table";
  const columnsPerTheme = state.questionValues.length + 2;
  const hasShootout = shootoutThemeCount() > 0;
  const totalColumnSpan = 4 + totalThemeCount() * columnsPerTheme + (hasShootout ? 7 : 6);

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th(state.title, "sticky sticky-name battle"));
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

loadState()
  .then(() => {
    setLive(true);
    connectEvents();
  })
  .catch((error) => {
    setLive(false);
    console.error(error);
  });
