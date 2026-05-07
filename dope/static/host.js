const hostRoot = document.getElementById("hostTable");
const statusNode = document.getElementById("status");
const titleNode = document.getElementById("matchTitle");

let state = null;
let activeCell = {team: 0, theme: 0, answer: 0};

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

function setStatus(text) {
  statusNode.textContent = text;
  statusNode.dataset.state = text;
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
  titleNode.textContent = state.title;
  hostRoot.replaceChildren(buildTable());
  focusActiveCell({preventScroll: true});
}

function buildTable() {
  const table = document.createElement("table");
  table.className = "match-table";
  const columnsPerTheme = state.questionValues.length + 2;
  const totalColumnSpan = 4 + state.teams[0].themes.length * columnsPerTheme + 7;

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th(state.title, "sticky sticky-name battle"));
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));
  header.appendChild(th("", "sticky sticky-after-place-gap"));

  for (let theme = 0; theme < state.teams[0].themes.length; theme++) {
    for (const value of state.questionValues) {
      header.appendChild(th(value, "question-head"));
    }
    header.appendChild(th(`T${theme + 1}`, "theme-head"));
    header.appendChild(th("", "gap-head"));
  }
  header.appendChild(th("П", "number"));
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
    placeInput.type = "number";
    placeInput.min = "0";
    placeInput.max = String(state.teams.length);
    placeInput.value = team.place || "";
    placeInput.className = "place-input";
    placeInput.addEventListener("change", () => {
      sendUpdate({team: teamIndex, place: Number(placeInput.value || 0)});
    });
    const placeCell = document.createElement("td");
    placeCell.className = "sticky sticky-place number place-cell";
    placeCell.rowSpan = 2;
    placeCell.appendChild(placeInput);
    playerRow.appendChild(placeCell);
    playerRow.appendChild(td("", "sticky sticky-after-place-gap", {rowSpan: 2}));

    team.themes.forEach((theme, themeIndex) => {
      const playerCell = document.createElement("td");
      playerCell.colSpan = 5;
      playerCell.className = "player-cell theme-block theme-block-top-left";
      const select = document.createElement("select");
      select.appendChild(option("", ""));
      team.roster.forEach((player) => select.appendChild(option(player, player)));
      if (theme.player && !team.roster.includes(theme.player)) {
        select.appendChild(option(theme.player, theme.player));
      }
      select.value = theme.player;
      select.addEventListener("change", () => {
        sendUpdate({team: teamIndex, theme: themeIndex, player: select.value});
      });
      playerCell.appendChild(select);
      playerRow.appendChild(playerCell);
      playerRow.appendChild(td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2}));
      playerRow.appendChild(td("", "gap"));

      theme.answers.forEach((mark, answerIndex) => {
        const cell = document.createElement("td");
        cell.className = `answer-cell theme-block ${mark}`;
        if (answerIndex === 0) {
          cell.classList.add("theme-block-bottom-left");
        }
        if (isActiveCell(teamIndex, themeIndex, answerIndex)) {
          cell.classList.add("active");
        }
        cell.tabIndex = 0;
        cell.dataset.team = String(teamIndex);
        cell.dataset.theme = String(themeIndex);
        cell.dataset.answer = String(answerIndex);
        cell.title = `${team.name}, T${themeIndex + 1}, ${state.questionValues[answerIndex]}`;
        cell.addEventListener("click", () => {
          selectAnswerCell(teamIndex, themeIndex, answerIndex);
        });
        cell.addEventListener("focus", () => {
          selectAnswerCell(teamIndex, themeIndex, answerIndex, {focus: false});
        });
        answerRow.appendChild(cell);
      });
      answerRow.appendChild(td("", "gap"));
    });

    const tiebreak = document.createElement("input");
    tiebreak.type = "number";
    tiebreak.value = team.tiebreak;
    tiebreak.className = "tiebreak-input";
    tiebreak.addEventListener("change", () => {
      sendUpdate({team: teamIndex, tiebreak: Number(tiebreak.value || 0)});
    });
    const tiebreakCell = document.createElement("td");
    tiebreakCell.className = "number tiebreak-cell";
    tiebreakCell.rowSpan = 2;
    tiebreakCell.appendChild(tiebreak);
    playerRow.appendChild(tiebreakCell);

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
  return target instanceof HTMLInputElement || target instanceof HTMLSelectElement || target instanceof HTMLTextAreaElement;
}

function selectAnswerCell(team, theme, answer, options = {}) {
  activeCell = {team, theme, answer};
  markActiveCell();
  if (options.focus !== false) {
    focusActiveCell();
  }
}

function moveActiveCell(teamDelta, answerDelta) {
  const maxTeam = state.teams.length - 1;
  const maxColumn = state.teams[0].themes.length * state.questionValues.length - 1;
  const column = activeCell.theme * state.questionValues.length + activeCell.answer;
  const nextTeam = clamp(activeCell.team + teamDelta, 0, maxTeam);
  const nextColumn = clamp(column + answerDelta, 0, maxColumn);
  activeCell = {
    team: nextTeam,
    theme: Math.floor(nextColumn / state.questionValues.length),
    answer: nextColumn % state.questionValues.length,
  };
  markActiveCell();
  focusActiveCell();
}

function setActiveMark(mark) {
  sendUpdate({
    team: activeCell.team,
    theme: activeCell.theme,
    answer: activeCell.answer,
    mark,
  });
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

function findActiveCell() {
  return document.querySelector(
    `.answer-cell[data-team="${activeCell.team}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActiveCell(team, theme, answer) {
  return activeCell.team === team && activeCell.theme === theme && activeCell.answer === answer;
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
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
