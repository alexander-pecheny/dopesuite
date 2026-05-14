const odRoot = document.getElementById("odTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

const route = currentRoute();
let scheme = null;
let state = null;
let tourLengths = [];
let totalQuestions = 0;

async function loadAll() {
  const [schemeResp, stateResp] = await Promise.all([
    fetch(`${route.apiBase}/scheme`),
    fetch(`${route.apiBase}/state`),
  ]);
  if (!schemeResp.ok) throw new Error(await schemeResp.text());
  if (!stateResp.ok) throw new Error(await stateResp.text());
  scheme = await schemeResp.json();
  state = await stateResp.json();
  initFromScheme();
  ensureState();
  render();
}

function initFromScheme() {
  tourLengths = parseTourComp(scheme.tourComp);
  totalQuestions = tourLengths.reduce((acc, n) => acc + n, 0);
}

function parseTourComp(value) {
  if (Array.isArray(value)) return value.map((n) => Number(n) || 0).filter((n) => n > 0);
  if (typeof value === "string") {
    const out = [];
    for (const segment of value.split(",")) {
      const seg = segment.trim();
      if (!seg) continue;
      if (seg.includes("*")) {
        const [before, after] = seg.split("*", 2);
        const count = Number(before.trim()) || 0;
        const repeat = Number(after.trim()) || 0;
        for (let i = 0; i < repeat; i++) out.push(count);
      } else {
        const n = Number(seg);
        if (n > 0) out.push(n);
      }
    }
    return out;
  }
  return [15];
}

function ensureState() {
  if (!state || typeof state !== "object") state = {};
  if (!Array.isArray(state.teams)) {
    state.teams = (scheme.teams || []).map((team) => ({name: team.name || "", city: team.city || ""}));
  }
  const targetCount = scheme.nTeams || state.teams.length || 0;
  while (state.teams.length < targetCount) {
    state.teams.push({name: "", city: ""});
  }
  if (!Array.isArray(state.answers)) state.answers = [];
  while (state.answers.length < state.teams.length) {
    state.answers.push(new Array(totalQuestions).fill(false));
  }
  state.answers = state.answers.map((row) => {
    const arr = Array.isArray(row) ? row.slice(0, totalQuestions) : [];
    while (arr.length < totalQuestions) arr.push(false);
    return arr.map((v) => Boolean(v));
  });
  if (typeof state.finished !== "boolean") state.finished = false;
}

function render() {
  if (!state || !scheme) return;
  setHeading(scheme.title || "ОД");
  document.title = `Ведущий · ${scheme.title || "ОД"}`;
  odRoot.replaceChildren(buildTable());
}

function buildTable() {
  const table = document.createElement("table");
  table.className = "match-table";
  table.classList.toggle("match-finished", state.finished);

  const thead = document.createElement("thead");

  const header = document.createElement("tr");
  header.appendChild(battleHeader());
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));
  header.appendChild(th("", "sticky sticky-place-gap place-gap-head"));

  let questionNumber = 1;
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      header.appendChild(th(questionNumber, "question-head"));
      questionNumber++;
    }
    header.appendChild(th(`Т${tourIndex + 1}`, "theme-head"));
    header.appendChild(th("", "gap-head"));
  });
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const totals = state.teams.map((_, i) => sumRow(state.answers[i]));
  const placeMap = computePlaces(totals);

  state.teams.forEach((team, teamIndex) => {
    const tr = document.createElement("tr");
    const nameTd = nameCell(team, teamIndex);
    tr.appendChild(nameTd);

    const total = totals[teamIndex];
    tr.appendChild(td(total, "sticky sticky-total number total-cell"));
    tr.appendChild(td(placeMap[teamIndex] || "", "sticky sticky-place number place-cell"));
    tr.appendChild(td("", "sticky sticky-place-gap place-gap"));

    let qIndex = 0;
    tourLengths.forEach((tourSize) => {
      let tourSum = 0;
      for (let i = 0; i < tourSize; i++) {
        const answered = Boolean(state.answers[teamIndex][qIndex]);
        if (answered) tourSum += 1;
        const cell = answerCell(teamIndex, qIndex, answered);
        tr.appendChild(cell);
        qIndex++;
      }
      tr.appendChild(td(tourSum, "number theme-score theme-block theme-block-score"));
      tr.appendChild(td("", "gap"));
    });
    tbody.appendChild(tr);
    if (teamIndex < state.teams.length - 1) {
      const gapRow = document.createElement("tr");
      gapRow.appendChild(td("", "team-gap", {colSpan: 4 + totalQuestions + tourLengths.length * 2}));
      tbody.appendChild(gapRow);
    }
  });
  table.appendChild(tbody);
  return table;
}

function nameCell(team, teamIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "venue-input";
  input.value = team.name || "";
  input.placeholder = `Команда ${teamIndex + 1}`;
  input.disabled = state.finished;
  input.addEventListener("change", () => {
    state.teams[teamIndex].name = input.value.trim();
    saveState();
  });
  cell.appendChild(input);
  return cell;
}

function answerCell(teamIndex, qIndex, answered) {
  const cell = document.createElement("td");
  cell.className = `answer-cell theme-block ${answered ? "right" : ""}`;
  cell.tabIndex = state.finished ? -1 : 0;
  cell.title = `Команда ${teamIndex + 1}, вопрос ${qIndex + 1}`;
  if (!state.finished) {
    cell.addEventListener("click", () => {
      state.answers[teamIndex][qIndex] = !state.answers[teamIndex][qIndex];
      saveState();
      render();
    });
    cell.addEventListener("keydown", (event) => {
      if (event.key === " " || event.key === "Enter" || event.key === "q" || event.key === "й" || event.key === "1") {
        event.preventDefault();
        state.answers[teamIndex][qIndex] = !state.answers[teamIndex][qIndex];
        saveState();
        render();
      }
    });
  }
  return cell;
}

function battleHeader() {
  const node = document.createElement("th");
  node.className = "sticky sticky-name battle";
  const layout = document.createElement("span");
  layout.className = "battle-layout";
  const title = document.createElement("span");
  title.className = "battle-title";
  title.textContent = "Команда";
  layout.appendChild(title);

  const label = document.createElement("label");
  label.className = "finish-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(state.finished);
  checkbox.addEventListener("change", () => {
    state.finished = checkbox.checked;
    saveState();
    render();
  });
  const text = document.createElement("span");
  text.textContent = "Закончен";
  label.append(checkbox, text);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function sumRow(row) {
  if (!row) return 0;
  return row.reduce((acc, v) => acc + (v ? 1 : 0), 0);
}

function computePlaces(totals) {
  const sorted = totals.map((total, index) => ({total, index}))
    .sort((a, b) => b.total - a.total);
  const places = new Array(totals.length).fill("");
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total) j++;
    const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) {
      places[sorted[k].index] = sorted[k].total > 0 ? label : "";
    }
    i = j + 1;
  }
  return places;
}

let saveTimer = null;
function saveState() {
  setStatus("saving");
  window.clearTimeout(saveTimer);
  saveTimer = window.setTimeout(async () => {
    try {
      const response = await fetch(`${route.apiBase}/state`, {
        method: "PUT",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(state),
      });
      if (!response.ok) throw new Error(await response.text());
      setStatus("saved");
    } catch (error) {
      console.error(error);
      setStatus("error");
    }
  }, 200);
}

function setStatus(s) {
  const labels = {saved: "Синхронизировано", saving: "Синхронизация", error: "Ошибка"};
  statusNode.dataset.state = s;
  statusNode.setAttribute("aria-label", labels[s] || labels.saving);
  statusNode.title = labels[s] || labels.saving;
}

function setHeading(text) {
  if (pageHeading) pageHeading.textContent = text;
}

function connectEvents() {
  const events = new EventSource("/events");
  const scopeName = `game-state:${route.gameID}`;
  events.addEventListener("state", (event) => {
    let parsed;
    try {
      parsed = JSON.parse(event.data);
    } catch (_e) {
      return;
    }
    if (parsed && parsed.scope === scopeName) {
      state = parsed.data;
      ensureState();
      render();
      setStatus("saved");
    }
  });
  events.onerror = () => setStatus("reconnecting");
}

function currentRoute() {
  const path = window.location.pathname;
  const m = path.match(/^\/host\/tournament\/(\d+)\/game\/(\d+)/);
  if (!m) return {};
  const tournamentID = m[1];
  const gameID = m[2];
  return {
    tournamentID,
    gameID,
    apiBase: `/api/tournaments/${tournamentID}/games/${gameID}`,
  };
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

loadAll()
  .then(() => {
    setStatus("saved");
    connectEvents();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
