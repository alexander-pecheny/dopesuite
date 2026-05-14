const siRoot = document.getElementById("siTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

const QUESTION_VALUES = [10, 20, 30, 40, 50];

const route = currentRoute();
let scheme = null;
let state = null;
let participants = [];
let themesCount = 8;
let activeCell = {player: 0, theme: 0, answer: 0};

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
  participants = Array.isArray(scheme.participants) && scheme.participants.length > 0
    ? scheme.participants
    : ["Игрок 1", "Игрок 2", "Игрок 3", "Игрок 4"];
  themesCount = Number(scheme.themes) > 0 ? Number(scheme.themes) : 8;
}

function ensureState() {
  if (!state || typeof state !== "object") state = {};
  if (!Array.isArray(state.participants) || state.participants.length === 0) {
    state.participants = participants.slice();
  }
  if (!Array.isArray(state.themes)) state.themes = [];
  while (state.themes.length < themesCount) state.themes.push({answers: []});
  state.themes = state.themes.slice(0, themesCount).map((theme) => {
    const answers = Array.isArray(theme.answers) ? theme.answers : [];
    const padded = [];
    for (let p = 0; p < state.participants.length; p++) {
      const row = Array.isArray(answers[p]) ? answers[p].slice(0, QUESTION_VALUES.length) : [];
      while (row.length < QUESTION_VALUES.length) row.push("");
      padded.push(row);
    }
    return {answers: padded};
  });
  if (typeof state.finished !== "boolean") state.finished = false;
}

function render() {
  setHeading(scheme.title || "СИ");
  document.title = `Ведущий · ${scheme.title || "СИ"}`;
  siRoot.replaceChildren(buildTable());
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

  for (let t = 0; t < themesCount; t++) {
    for (const value of QUESTION_VALUES) {
      header.appendChild(th(value, "question-head"));
    }
    header.appendChild(th(`Т${t + 1}`, "theme-head"));
    header.appendChild(th("", "gap-head"));
  }
  thead.appendChild(header);
  table.appendChild(thead);

  const totals = state.participants.map((_, p) => playerTotal(p));
  const places = computePlaces(totals);

  const tbody = document.createElement("tbody");
  state.participants.forEach((participant, playerIndex) => {
    const tr = document.createElement("tr");
    tr.appendChild(nameCell(participant, playerIndex));
    tr.appendChild(td(totals[playerIndex], "sticky sticky-total number total-cell"));
    tr.appendChild(td(places[playerIndex] || "", "sticky sticky-place number place-cell"));
    tr.appendChild(td("", "sticky sticky-place-gap place-gap"));

    for (let t = 0; t < themesCount; t++) {
      let themeSum = 0;
      for (let aIndex = 0; aIndex < QUESTION_VALUES.length; aIndex++) {
        const mark = state.themes[t].answers[playerIndex][aIndex];
        const value = QUESTION_VALUES[aIndex];
        if (mark === "right") themeSum += value;
        else if (mark === "wrong") themeSum -= value;
        tr.appendChild(answerCell(playerIndex, t, aIndex, mark));
      }
      tr.appendChild(td(themeSum, "number theme-score theme-block theme-block-score"));
      tr.appendChild(td("", "gap"));
    }
    tbody.appendChild(tr);
    if (playerIndex < state.participants.length - 1) {
      const gapRow = document.createElement("tr");
      gapRow.appendChild(td("", "team-gap", {colSpan: 4 + themesCount * (QUESTION_VALUES.length + 2)}));
      tbody.appendChild(gapRow);
    }
  });
  table.appendChild(tbody);
  return table;
}

function nameCell(name, playerIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "venue-input";
  input.value = name;
  input.placeholder = `Игрок ${playerIndex + 1}`;
  input.disabled = state.finished;
  input.addEventListener("change", () => {
    state.participants[playerIndex] = input.value.trim();
    saveState();
  });
  cell.appendChild(input);
  return cell;
}

function answerCell(playerIndex, themeIndex, answerIndex, mark) {
  const cell = document.createElement("td");
  cell.className = `answer-cell theme-block ${mark}`;
  cell.tabIndex = state.finished ? -1 : 0;
  cell.dataset.player = String(playerIndex);
  cell.dataset.theme = String(themeIndex);
  cell.dataset.answer = String(answerIndex);
  cell.title = `${state.participants[playerIndex] || `Игрок ${playerIndex + 1}`}, Т${themeIndex + 1}, ${QUESTION_VALUES[answerIndex]}`;
  if (isActive(playerIndex, themeIndex, answerIndex) && !state.finished) {
    cell.classList.add("active");
  }
  if (!state.finished) {
    cell.addEventListener("click", () => {
      selectCell(playerIndex, themeIndex, answerIndex);
    });
    cell.addEventListener("focus", () => {
      selectCell(playerIndex, themeIndex, answerIndex, {focus: false});
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
  title.textContent = scheme.title || "Бой";
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

function playerTotal(playerIndex) {
  let total = 0;
  for (let t = 0; t < themesCount; t++) {
    for (let i = 0; i < QUESTION_VALUES.length; i++) {
      const mark = state.themes[t].answers[playerIndex][i];
      const value = QUESTION_VALUES[i];
      if (mark === "right") total += value;
      else if (mark === "wrong") total -= value;
    }
  }
  return total;
}

function computePlaces(totals) {
  const sorted = totals.map((total, index) => ({total, index})).sort((a, b) => b.total - a.total);
  const places = new Array(totals.length).fill("");
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total) j++;
    const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) places[sorted[k].index] = label;
    i = j + 1;
  }
  return places;
}

function selectCell(player, theme, answer, options = {}) {
  activeCell = {player, theme, answer};
  markActive();
  if (options.focus !== false) {
    findActive()?.focus();
  }
}

function markActive() {
  document.querySelectorAll(".answer-cell.active").forEach((cell) => cell.classList.remove("active"));
  findActive()?.classList.add("active");
}

function findActive() {
  return document.querySelector(
    `.answer-cell[data-player="${activeCell.player}"][data-theme="${activeCell.theme}"][data-answer="${activeCell.answer}"]`,
  );
}

function isActive(p, t, a) {
  return activeCell.player === p && activeCell.theme === t && activeCell.answer === a;
}

function setMark(mark) {
  if (state.finished) return;
  state.themes[activeCell.theme].answers[activeCell.player][activeCell.answer] = mark;
  saveState();
  render();
}

function handleKeydown(event) {
  if (isFormControl(event.target)) return;
  const key = event.key.toLowerCase();
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    moveCell(0, -1);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    moveCell(0, 1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    moveCell(-1, 0);
  } else if (event.key === "ArrowDown") {
    event.preventDefault();
    moveCell(1, 0);
  } else if (key === "q" || key === "й" || key === "1") {
    event.preventDefault();
    setMark("right");
  } else if (key === "w" || key === "ц" || key === "-") {
    event.preventDefault();
    setMark("wrong");
  } else if (key === "backspace" || key === "delete") {
    event.preventDefault();
    setMark("");
  }
}

function moveCell(dPlayer, dAnswer) {
  const players = state.participants.length;
  const totalCols = themesCount * QUESTION_VALUES.length;
  let column = activeCell.theme * QUESTION_VALUES.length + activeCell.answer;
  column = clamp(column + dAnswer, 0, totalCols - 1);
  const player = clamp(activeCell.player + dPlayer, 0, players - 1);
  selectCell(player, Math.floor(column / QUESTION_VALUES.length), column % QUESTION_VALUES.length);
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function isFormControl(target) {
  return target instanceof HTMLInputElement || target instanceof HTMLSelectElement || target instanceof HTMLTextAreaElement || target instanceof HTMLButtonElement;
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

document.addEventListener("keydown", handleKeydown);

loadAll()
  .then(() => {
    setStatus("saved");
    connectEvents();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
