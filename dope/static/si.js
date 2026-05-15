const siRoot = document.getElementById("siTable");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

const QUESTION_VALUES = [10, 20, 30, 40, 50];

const route = currentRoute();
const viewer = Boolean(route.viewer);
document.body.classList.toggle("viewer-readonly", viewer);
let scheme = null;
let state = null;
let participants = [];
let themesCount = 8;
let activeCell = {player: 0, theme: 0, answer: 0};
let localEchoJSON = "";

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
  participants = schemeParticipants();
  themesCount = Number(scheme.themes) > 0 ? Number(scheme.themes) : 8;
}

function schemeParticipants() {
  if (Array.isArray(scheme.participants) && scheme.participants.length > 0) {
    return scheme.participants.slice();
  }
  if (isTeamMode() && Array.isArray(scheme.teams) && scheme.teams.length > 0) {
    return scheme.teams.map((team) => team.name || "");
  }
  return ["Игрок 1", "Игрок 2", "Игрок 3", "Игрок 4"];
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
  const defaultTitle = gameTitleFallback();
  setHeading(scheme.title || defaultTitle);
  document.title = `${viewer ? "Зритель" : "Ведущий"} · ${scheme.title || defaultTitle}`;
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
    const totalCell = td(totals[playerIndex], "sticky sticky-total number total-cell");
    totalCell.dataset.player = String(playerIndex);
    tr.appendChild(totalCell);

    const placeCell = td(places[playerIndex] || "", "sticky sticky-place number place-cell");
    placeCell.dataset.player = String(playerIndex);
    tr.appendChild(placeCell);
    tr.appendChild(td("", "sticky sticky-place-gap place-gap"));

    for (let t = 0; t < themesCount; t++) {
      for (let aIndex = 0; aIndex < QUESTION_VALUES.length; aIndex++) {
        const mark = state.themes[t].answers[playerIndex][aIndex];
        tr.appendChild(answerCell(playerIndex, t, aIndex, mark));
      }
      const scoreCell = td(themeScore(playerIndex, t), "number theme-score theme-block theme-block-score");
      scoreCell.dataset.player = String(playerIndex);
      scoreCell.dataset.theme = String(t);
      tr.appendChild(scoreCell);
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
  input.placeholder = participantFallback(playerIndex);
  input.disabled = state.finished || viewer;
  input.addEventListener("change", () => {
    if (viewer) return;
    state.participants[playerIndex] = input.value.trim();
    saveState();
  });
  cell.appendChild(input);
  return cell;
}

function answerCell(playerIndex, themeIndex, answerIndex, mark) {
  const cell = document.createElement("td");
  cell.className = `answer-cell theme-block ${mark}`;
  cell.tabIndex = state.finished || viewer ? -1 : 0;
  cell.dataset.player = String(playerIndex);
  cell.dataset.theme = String(themeIndex);
  cell.dataset.answer = String(answerIndex);
  cell.title = `${state.participants[playerIndex] || participantFallback(playerIndex)}, Т${themeIndex + 1}, ${QUESTION_VALUES[answerIndex]}`;
  if (isActive(playerIndex, themeIndex, answerIndex) && !state.finished && !viewer) {
    cell.classList.add("active");
  }
  if (!state.finished && !viewer) {
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
  checkbox.disabled = viewer;
  checkbox.addEventListener("change", () => {
    if (viewer) return;
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
    total += themeScore(playerIndex, t);
  }
  return total;
}

function themeScore(playerIndex, themeIndex) {
  let total = 0;
  for (let i = 0; i < QUESTION_VALUES.length; i++) {
    const mark = state.themes[themeIndex].answers[playerIndex][i];
    const value = QUESTION_VALUES[i];
    if (mark === "right") total += value;
    else if (mark === "wrong") total -= value;
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
  if (state.finished || viewer) return;
  const row = state.themes[activeCell.theme].answers[activeCell.player];
  if (row[activeCell.answer] === mark) return;
  row[activeCell.answer] = mark;
  updateAnswerCell(activeCell.player, activeCell.theme, activeCell.answer, mark);
  refreshScores();
  saveState();
}

function updateAnswerCell(player, theme, answer, mark) {
  const cell = siRoot.querySelector(`.answer-cell[data-player="${player}"][data-theme="${theme}"][data-answer="${answer}"]`);
  if (!cell) return;
  cell.classList.remove("right", "wrong");
  if (mark) cell.classList.add(mark);
}

function refreshScores() {
  if (!state?.participants) return;
  const totals = state.participants.map((_, p) => playerTotal(p));
  const places = computePlaces(totals);
  state.participants.forEach((_, playerIndex) => {
    const totalCell = siRoot.querySelector(`.total-cell[data-player="${playerIndex}"]`);
    if (totalCell) totalCell.textContent = String(totals[playerIndex]);
    const placeCell = siRoot.querySelector(`.place-cell[data-player="${playerIndex}"]`);
    if (placeCell) placeCell.textContent = places[playerIndex] || "";
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      const scoreCell = siRoot.querySelector(`.theme-score[data-player="${playerIndex}"][data-theme="${themeIndex}"]`);
      if (scoreCell) scoreCell.textContent = String(themeScore(playerIndex, themeIndex));
    }
  });
}

function isTeamMode() {
  return scheme?.gameType === "ksi";
}

function gameTitleFallback() {
  return isTeamMode() ? "КСИ" : "СИ";
}

function participantFallback(index) {
  return `${isTeamMode() ? "Команда" : "Игрок"} ${index + 1}`;
}

function handleKeydown(event) {
  if (viewer) return;
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
  if (viewer) return;
  setStatus("saving");
  window.clearTimeout(saveTimer);
  saveTimer = window.setTimeout(async () => {
    try {
      const raw = JSON.stringify(state);
      localEchoJSON = raw;
      const response = await fetch(`${route.apiBase}/state`, {
        method: "PUT",
        headers: {"Content-Type": "application/json"},
        body: raw,
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
  const labels = {saved: "Синхронизировано", saving: "Синхронизация", reconnecting: "Переподключение", error: "Ошибка"};
  statusNode.dataset.state = s;
  statusNode.setAttribute("aria-label", labels[s] || labels.saving);
  statusNode.title = labels[s] || labels.saving;
}

function setHeading(text) {
  if (pageHeading) pageHeading.textContent = text;
}

function connectEvents() {
  const events = new EventSource(`/events?tournament_id=${encodeURIComponent(route.tournamentID)}`);
  const scopeName = `game-state:${route.gameID}`;
  events.addEventListener("state", (event) => {
    let parsed;
    try {
      parsed = JSON.parse(event.data);
    } catch (_e) {
      return;
    }
    if (parsed && parsed.scope === scopeName) {
      const raw = JSON.stringify(parsed.data);
      state = parsed.data;
      ensureState();
      if (localEchoJSON && raw === localEchoJSON) {
        localEchoJSON = "";
        refreshScores();
        setStatus("saved");
        return;
      }
      render();
      setStatus("saved");
    }
  });
  events.onerror = () => setStatus("reconnecting");
}

function currentRoute() {
  const path = window.location.pathname;
  const host = path.match(/^\/host\/tournament\/(\d+)\/game\/(\d+)/);
  if (host) {
    return {
      viewer: false,
      tournamentID: host[1],
      gameID: host[2],
      apiBase: `/api/tournament/${host[1]}/games/${host[2]}`,
    };
  }
  const pub = path.match(/^\/tournament\/(\d+)\/game\/(\d+)/);
  if (pub) {
    return {
      viewer: true,
      tournamentID: pub[1],
      gameID: pub[2],
      apiBase: `/api/tournament/${pub[1]}/games/${pub[2]}`,
    };
  }
  return {};
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
