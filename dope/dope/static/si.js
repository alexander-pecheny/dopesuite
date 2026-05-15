const siRoot = document.getElementById("siTable");
const siTabsRoot = document.getElementById("siTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

const gameTable = window.DopeTable;
const QUESTION_VALUES = [10, 20, 30, 40, 50];
const RESULT_VALUES = QUESTION_VALUES.slice().reverse();
const KSI_THEMES = 20;
const teamNameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});
const KSI_TABS = [
  {key: "detailed", label: "Подробно"},
  {key: "results", label: "Итог"},
];

const route = currentRoute();
const viewer = Boolean(route.viewer);
document.body.classList.toggle("viewer-readonly", viewer);
let scheme = null;
let state = null;
let participants = [];
let themesCount = 8;
let activeCell = {player: 0, theme: 0, answer: 0};
let localEchoJSON = "";
let renderedTable = null;
let renderedTab = null;
let activeTab = tabFromHash() || "detailed";
const tabScroll = new Map();

function tabFromHash() {
  const key = (window.location.hash || "").replace(/^#/, "");
  return KSI_TABS.some((t) => t.key === key) ? key : null;
}

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});

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
  themesCount = isTeamMode() ? KSI_THEMES : (Number(scheme.themes) > 0 ? Number(scheme.themes) : 8);
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

function render(options = {}) {
  if (!scheme || !state) return;
  const defaultTitle = gameTitleFallback();
  normalizeActiveCell();
  setHeading(scheme.title || defaultTitle);
  document.title = `${viewer ? "Зритель" : "Ведущий"} · ${scheme.title || defaultTitle}`;
  if (isTeamMode()) {
    rememberTabScroll(renderedTab);
    if (!KSI_TABS.some((t) => t.key === activeTab)) activeTab = "detailed";
    renderTabs();
    const node = activeTab === "results" ? buildResultsTable() : buildTable();
    renderedTable = activeTab === "detailed" ? node : null;
    siRoot.replaceChildren(node);
    renderedTab = activeTab;
    restoreTabScroll(activeTab);
  } else {
    renderTabs();
    const frame = scrollFrame();
    const scrollTop = frame?.scrollTop || 0;
    const scrollLeft = frame?.scrollLeft || 0;
    renderedTable = buildTable();
    siRoot.replaceChildren(renderedTable);
    renderedTab = null;
    if (options.preserveScroll && frame) {
      frame.scrollTop = scrollTop;
      frame.scrollLeft = scrollLeft;
    }
  }
}

function buildTable() {
  const totals = state.participants.map((_, p) => playerTotal(p));
  const places = gameTable.computePlaces(totals);
  const themes = Array.from({length: themesCount}, (_, index) => ({
    label: `Т${index + 1}`,
    questionLabels: QUESTION_VALUES,
  }));
  const rows = detailedPlayerOrder().map((playerIndex) => ({
    nameCell: nameCell(state.participants[playerIndex], playerIndex),
    totalCell: {
      content: totals[playerIndex],
      className: "sticky sticky-total number total-cell",
      dataset: {player: playerIndex},
    },
    placeCell: {
      content: places[playerIndex] || "",
      className: "sticky sticky-place number place-cell",
      dataset: {player: playerIndex},
    },
    themes: themes.map((_, themeIndex) => ({
      answers: QUESTION_VALUES.map((__, answerIndex) => {
        const mark = state.themes[themeIndex].answers[playerIndex][answerIndex];
        return answerCell(playerIndex, themeIndex, answerIndex, mark);
      }),
      scoreCell: {
        content: themeScore(playerIndex, themeIndex),
        className: "number theme-score theme-block theme-block-score",
        dataset: {player: playerIndex, theme: themeIndex},
      },
    })),
  }));

  const table = gameTable.buildFlatScoreTable({
    className: "match-table compact-score-table si-table",
    nameHeader: battleHeader(),
    themes,
    rows,
    events: {
      click: handleTableClick,
      focusin: handleTableFocusIn,
      change: handleTableChange,
    },
  });
  table.classList.toggle("match-finished", state.finished);
  return table;
}

function buildResultsTable() {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  wrapper.appendChild(buildResultsTableInner());
  return wrapper;
}

function buildResultsTableInner() {
  const rows = rankedResultRows();
  const table = document.createElement("table");
  table.className = "results-table ksi-results-table";

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(gameTable.th("Место", "results-place-head"));
  head.appendChild(gameTable.th("Команда", "results-team-head"));
  head.appendChild(gameTable.th("Σ", "results-num-head"));
  head.appendChild(gameTable.th("Σ+", "results-num-head"));
  for (const value of RESULT_VALUES) {
    head.appendChild(gameTable.th(value, "results-num-head"));
  }
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  rows.forEach((row) => {
    const tr = document.createElement("tr");
    tr.appendChild(gameTable.td(row.placeText, "results-place"));
    const nameTd = document.createElement("td");
    nameTd.className = "results-team";
    const nameSpan = document.createElement("span");
    nameSpan.className = "results-team-name";
    nameSpan.textContent = row.name;
    nameTd.appendChild(nameSpan);
    tr.appendChild(nameTd);
    tr.appendChild(gameTable.td(row.metrics.total, "results-num total-cell"));
    tr.appendChild(gameTable.td(row.metrics.plus, "results-num"));
    for (const value of RESULT_VALUES) {
      tr.appendChild(gameTable.td(row.metrics.correct[value] || 0, "results-num"));
    }
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  return table;
}

function rankedResultRows() {
  const rows = state.participants.map((_, index) => ({
    index,
    name: participantLabel(index),
    metrics: resultMetrics(index),
    placeText: "",
  }));
  rows.sort(compareResultRows);
  let i = 0;
  while (i < rows.length) {
    let j = i;
    while (j + 1 < rows.length && sameResultMetrics(rows[i].metrics, rows[j + 1].metrics)) j++;
    const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) rows[k].placeText = label;
    i = j + 1;
  }
  return rows;
}

function compareResultRows(a, b) {
  if (b.metrics.total !== a.metrics.total) return b.metrics.total - a.metrics.total;
  if (b.metrics.plus !== a.metrics.plus) return b.metrics.plus - a.metrics.plus;
  for (const value of RESULT_VALUES) {
    const diff = (b.metrics.correct[value] || 0) - (a.metrics.correct[value] || 0);
    if (diff) return diff;
  }
  return teamNameCollator.compare(a.name, b.name) || a.index - b.index;
}

function sameResultMetrics(a, b) {
  if (a.total !== b.total || a.plus !== b.plus) return false;
  for (const value of RESULT_VALUES) {
    if ((a.correct[value] || 0) !== (b.correct[value] || 0)) return false;
  }
  return true;
}

function resultMetrics(playerIndex) {
  const correct = {};
  for (const value of QUESTION_VALUES) correct[value] = 0;
  let total = 0;
  let plus = 0;
  for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
    const row = state.themes[themeIndex]?.answers?.[playerIndex] || [];
    for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
      const value = QUESTION_VALUES[answerIndex];
      const mark = row[answerIndex];
      if (mark === "right") {
        total += value;
        plus += value;
        correct[value] += 1;
      } else if (mark === "wrong") {
        total -= value;
      }
    }
  }
  return {total, plus, correct};
}

function renderTabs() {
  if (!siTabsRoot) return;
  siTabsRoot.replaceChildren();
  if (!isTeamMode()) {
    siTabsRoot.hidden = true;
    return;
  }
  siTabsRoot.hidden = false;
  for (const tab of KSI_TABS) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "match-tab" + (activeTab === tab.key ? " active" : "");
    btn.textContent = tab.label;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-selected", activeTab === tab.key ? "true" : "false");
    btn.addEventListener("click", () => {
      if (activeTab === tab.key) return;
      activeTab = tab.key;
      if (window.location.hash.replace(/^#/, "") !== tab.key) {
        history.replaceState(null, "", `#${tab.key}`);
      }
      render();
    });
    siTabsRoot.appendChild(btn);
  }
}

function scrollFrame() {
  return siRoot.closest(".sheet-frame");
}

function rememberTabScroll(tab) {
  const frame = scrollFrame();
  if (!tab || !frame) return;
  tabScroll.set(tab, {top: frame.scrollTop, left: frame.scrollLeft});
}

function restoreTabScroll(tab) {
  const frame = scrollFrame();
  if (!frame) return;
  const pos = tabScroll.get(tab) || {top: 0, left: 0};
  frame.scrollTop = pos.top;
  frame.scrollLeft = pos.left;
}

function detailedPlayerOrder() {
  const order = state.participants.map((_, index) => index);
  if (!isTeamMode()) return order;
  return order.sort((a, b) => compareParticipantNames(a, b));
}

function compareParticipantNames(a, b) {
  const byName = teamNameCollator.compare(participantLabel(a), participantLabel(b));
  return byName || a - b;
}

function participantLabel(index) {
  const name = String(state.participants[index] || "").trim();
  return name || participantFallback(index);
}

function nameCell(name, playerIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "venue-input";
  input.dataset.player = String(playerIndex);
  input.value = name;
  input.placeholder = participantFallback(playerIndex);
  input.disabled = state.finished || viewer;
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
  cell.title = answerTitle(playerIndex, themeIndex, answerIndex);
  if (isActive(playerIndex, themeIndex, answerIndex) && !state.finished && !viewer) {
    cell.classList.add("active");
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
  const text = document.createElement("span");
  text.textContent = "Закончен";
  label.append(checkbox, text);
  layout.appendChild(label);
  node.appendChild(layout);
  return node;
}

function handleTableClick(event) {
  const cell = event.target.closest?.(".answer-cell");
  if (!cell || state.finished || viewer) return;
  selectCellFromNode(cell);
}

function handleTableFocusIn(event) {
  const cell = event.target.closest?.(".answer-cell");
  if (!cell || state.finished || viewer) return;
  selectCellFromNode(cell, {focus: false});
}

function handleTableChange(event) {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.classList.contains("finish-toggle")) {
    if (viewer) return;
    state.finished = target.checked;
    saveState();
    render({preserveScroll: true});
    return;
  }
  if (target instanceof HTMLInputElement && target.classList.contains("venue-input")) {
    if (viewer) return;
    const playerIndex = Number(target.dataset.player);
    if (!Number.isInteger(playerIndex) || playerIndex < 0 || playerIndex >= state.participants.length) return;
    state.participants[playerIndex] = target.value.trim();
    saveState();
    if (isTeamMode()) render({preserveScroll: true});
  }
}

function selectCellFromNode(cell, options = {}) {
  const player = Number(cell.dataset.player);
  const theme = Number(cell.dataset.theme);
  const answer = Number(cell.dataset.answer);
  if (!Number.isInteger(player) || !Number.isInteger(theme) || !Number.isInteger(answer)) return;
  selectCell(player, theme, answer, options);
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

function selectCell(player, theme, answer, options = {}) {
  activeCell = {player, theme, answer};
  markActive();
  if (options.focus !== false) {
    findActive()?.focus();
  }
}

function markActive() {
  siRoot.querySelectorAll(".answer-cell.active").forEach((cell) => cell.classList.remove("active"));
  if (state.finished || viewer || !isDetailedTabActive()) return;
  findActive()?.classList.add("active");
}

function findActive() {
  return siRoot.querySelector(
    `.answer-cell[data-player="${gameTable.cssEscape(activeCell.player)}"][data-theme="${gameTable.cssEscape(activeCell.theme)}"][data-answer="${gameTable.cssEscape(activeCell.answer)}"]`,
  );
}

function isActive(p, t, a) {
  return activeCell.player === p && activeCell.theme === t && activeCell.answer === a;
}

function setMark(mark) {
  if (state.finished || viewer || !isDetailedTabActive()) return;
  const row = state.themes[activeCell.theme].answers[activeCell.player];
  if (row[activeCell.answer] === mark) return;
  row[activeCell.answer] = mark;
  updateAnswerCell(activeCell.player, activeCell.theme, activeCell.answer, mark);
  refreshScores();
  saveState();
}

function updateAnswerCell(player, theme, answer, mark) {
  const cell = siRoot.querySelector(`.answer-cell[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"][data-answer="${gameTable.cssEscape(answer)}"]`);
  if (!cell) return;
  cell.classList.remove("right", "wrong");
  if (mark) cell.classList.add(mark);
  cell.title = answerTitle(player, theme, answer);
}

function refreshScores() {
  if (!state?.participants) return;
  const totals = state.participants.map((_, p) => playerTotal(p));
  const places = gameTable.computePlaces(totals);
  state.participants.forEach((_, playerIndex) => {
    const totalCell = siRoot.querySelector(`.total-cell[data-player="${gameTable.cssEscape(playerIndex)}"]`);
    if (totalCell) totalCell.textContent = String(totals[playerIndex]);
    const placeCell = siRoot.querySelector(`.place-cell[data-player="${gameTable.cssEscape(playerIndex)}"]`);
    if (placeCell) placeCell.textContent = places[playerIndex] || "";
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      const scoreCell = siRoot.querySelector(`.theme-score[data-player="${gameTable.cssEscape(playerIndex)}"][data-theme="${gameTable.cssEscape(themeIndex)}"]`);
      if (scoreCell) scoreCell.textContent = String(themeScore(playerIndex, themeIndex));
    }
  });
}

function patchTable() {
  if (!renderedTable) return false;
  state.participants.forEach((participant, playerIndex) => {
    const input = siRoot.querySelector(`.venue-input[data-player="${gameTable.cssEscape(playerIndex)}"]`);
    if (input) {
      if (document.activeElement !== input) input.value = participant || "";
      input.placeholder = participantFallback(playerIndex);
    }
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
        updateAnswerCell(playerIndex, themeIndex, answerIndex, state.themes[themeIndex].answers[playerIndex][answerIndex]);
      }
    }
  });
  refreshScores();
  markActive();
  return true;
}

function canPatchState(previous, next) {
  if (!renderedTable || !previous || !next) return false;
  if (previous.finished !== next.finished) return false;
  if (!Array.isArray(previous.participants) || !Array.isArray(next.participants)) return false;
  if (previous.participants.length !== next.participants.length) return false;
  if (isTeamMode() && !gameTable.sameArray(previous.participants, next.participants)) return false;
  if (!Array.isArray(previous.themes) || !Array.isArray(next.themes)) return false;
  if (previous.themes.length !== next.themes.length) return false;
  for (let themeIndex = 0; themeIndex < next.themes.length; themeIndex++) {
    const prevAnswers = previous.themes[themeIndex]?.answers || [];
    const nextAnswers = next.themes[themeIndex]?.answers || [];
    if (prevAnswers.length !== nextAnswers.length) return false;
    for (let playerIndex = 0; playerIndex < nextAnswers.length; playerIndex++) {
      if ((prevAnswers[playerIndex] || []).length !== (nextAnswers[playerIndex] || []).length) return false;
    }
  }
  return true;
}

function answerTitle(playerIndex, themeIndex, answerIndex) {
  return `${state.participants[playerIndex] || participantFallback(playerIndex)}, Т${themeIndex + 1}, ${QUESTION_VALUES[answerIndex]}`;
}

function isTeamMode() {
  return scheme?.gameType === "ksi";
}

function isDetailedTabActive() {
  return !isTeamMode() || activeTab === "detailed";
}

function gameTitleFallback() {
  return isTeamMode() ? "КСИ" : "СИ";
}

function participantFallback(index) {
  return `${isTeamMode() ? "Команда" : "Игрок"} ${index + 1}`;
}

function handleKeydown(event) {
  if (viewer) return;
  if (!isDetailedTabActive()) return;
  if (gameTable.isFormControl(event.target)) return;
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
  const playerOrder = detailedPlayerOrder();
  const players = playerOrder.length;
  const totalCols = themesCount * QUESTION_VALUES.length;
  let column = activeCell.theme * QUESTION_VALUES.length + activeCell.answer;
  column = gameTable.clamp(column + dAnswer, 0, totalCols - 1);
  const currentPosition = Math.max(0, playerOrder.indexOf(activeCell.player));
  const nextPosition = gameTable.clamp(currentPosition + dPlayer, 0, players - 1);
  const player = playerOrder[nextPosition];
  selectCell(player, Math.floor(column / QUESTION_VALUES.length), column % QUESTION_VALUES.length);
}

function normalizeActiveCell() {
  if (!state?.participants?.length || themesCount <= 0) return;
  const maxColumn = themesCount * QUESTION_VALUES.length - 1;
  const column = gameTable.clamp(activeCell.theme * QUESTION_VALUES.length + activeCell.answer, 0, maxColumn);
  activeCell = {
    player: gameTable.clamp(activeCell.player, 0, state.participants.length - 1),
    theme: Math.floor(column / QUESTION_VALUES.length),
    answer: column % QUESTION_VALUES.length,
  };
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
      const previous = state;
      state = parsed.data;
      ensureState();
      if (localEchoJSON && raw === localEchoJSON) {
        localEchoJSON = "";
        if (!patchTable()) render({preserveScroll: true});
        setStatus("saved");
        return;
      }
      if (canPatchState(previous, state) && patchTable()) {
        setStatus("saved");
        return;
      }
      render({preserveScroll: true});
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
