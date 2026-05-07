const viewerRoot = document.getElementById("viewerTable");
const titleNode = document.getElementById("viewerTitle");
const statusNode = document.getElementById("viewerStatus");
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
  statusNode.textContent = ok ? "live" : "reconnect";
}

function render() {
  if (!state) return;
  titleNode.textContent = state.title;
  viewerRoot.replaceChildren(buildReadonlyTable());
}

function buildReadonlyTable() {
  const table = document.createElement("table");
  table.className = "match-table readonly-table";
  const columnsPerTheme = state.questionValues.length + 2;
  const totalColumnSpan = 3 + state.teams[0].themes.length * columnsPerTheme + 7;

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th(state.title, "sticky sticky-name battle"));
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));

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
    playerRow.appendChild(td(formatPlace(team.place), "sticky sticky-place number place-cell", {rowSpan: 2}));

    team.themes.forEach((theme) => {
      const playerCell = document.createElement("td");
      playerCell.colSpan = 5;
      playerCell.className = "readonly-player theme-block theme-block-top-left";
      playerCell.textContent = theme.player || "";
      playerRow.appendChild(playerCell);
      playerRow.appendChild(td(theme.score, "number theme-score theme-block theme-block-score", {rowSpan: 2}));
      playerRow.appendChild(td("", "gap"));

      theme.answers.forEach((mark, answerIndex) => {
        const className = answerIndex === 0
          ? `answer-cell theme-block theme-block-bottom-left ${mark}`
          : `answer-cell theme-block ${mark}`;
        answerRow.appendChild(td("", className));
      });
      answerRow.appendChild(td("", "gap"));
    });

    playerRow.appendChild(td(team.tiebreak || "", "number tiebreak-cell", {rowSpan: 2}));
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
