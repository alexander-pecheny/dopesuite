const siRoot = document.getElementById("siTable");
const siTabsRoot = document.getElementById("siTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const teamNameOverflow = gameTable.createTeamNameOverflowController({
  root: siRoot,
  detailed: {
    cellSelector: ".ksi-detailed-team-cell",
    nameSelector: ".od-detailed-team-name",
    truncatedClass: "od-detailed-team-cell-truncated",
  },
  results: {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
  },
});
const QUESTION_VALUES = [10, 20, 30, 40, 50];
const RESULT_VALUES = QUESTION_VALUES.slice().reverse();
const KSI_THEMES = 20;
const teamNameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});
const KSI_TABS = [
  {key: "detailed", label: "Подробно"},
  {key: "results", label: "Итог"},
];

const route = gameTable.parseGameRoute();
const viewer = Boolean(route.viewer);
document.body.classList.toggle("viewer-readonly", viewer);
if (viewer) {
  if (window.__GAME_INIT__?.canEdit) gameTable.mountEditorLink(statusNode);
} else {
  gameTable.mountViewerLink(statusNode);
}
let scheme = null;
let state = null;
let fest = null;
let participants = [];
let themesCount = 8;
let activeCell = {player: 0, theme: 0, answer: 0};
let renderedTable = null;
let renderedTab = null;
let activeTab = tabFromHash() || "detailed";
let tableIndex = null;
let scoreCache = null;
let detailedOrderCache = null;
let activeAnswerNode = null;
let activePlayerRows = [];
let stateSync = null;
let presence = null;
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

window.addEventListener("resize", () => {
  if (isTeamMode() && (renderedTab === "detailed" || renderedTab === "results")) {
    teamNameOverflow.schedule();
  }
  updateResultsScrollState();
});
document.querySelector(".sheet-frame")?.addEventListener("scroll", updateResultsScrollState, {passive: true});

async function loadAll() {
  if (consumeGameInit()) {
    revalidateAll().catch((error) => console.error(error));
    return;
  }
  if (hydrateFromCache()) {
    revalidateAll().catch((error) => console.error(error));
    return;
  }
  await fetchAll();
}

// consumeGameInit hydrates scheme/state/fest from window.__GAME_INIT__ so the
// first frame renders without any API round trips. Returns true on success.
function consumeGameInit() {
  const init = window.__GAME_INIT__;
  if (!init || !init.scheme || !init.state) return false;
  window.__GAME_INIT__ = null;
  scheme = init.scheme;
  state = init.state;
  fest = init.fest || null;
  initFromScheme();
  ensureState();
  render();
  writeGameCache();
  return true;
}

function gameCacheKey() {
  return `si:game:${route.festID || ""}:${route.gameID || ""}`;
}

function readGameCache() {
  try {
    const raw = localStorage.getItem(gameCacheKey());
    return raw ? JSON.parse(raw) : null;
  } catch (_err) {
    return null;
  }
}

function writeGameCache() {
  if (!scheme || !state) return;
  try {
    localStorage.setItem(gameCacheKey(), JSON.stringify({scheme, state, fest}));
  } catch (_err) {
    // ignore
  }
}

function hydrateFromCache() {
  const cached = readGameCache();
  if (!cached || !cached.scheme || !cached.state) return false;
  scheme = cached.scheme;
  state = cached.state;
  fest = cached.fest || null;
  initFromScheme();
  ensureState();
  render();
  return true;
}

async function fetchAll() {
  const fresh = await fetchAllRaw();
  scheme = fresh.scheme;
  state = fresh.state;
  fest = fresh.fest;
  initFromScheme();
  ensureState();
  render();
  writeGameCache();
}

async function fetchAllRaw() {
  const [schemeResp, stateResp, festResp] = await Promise.all([
    fetch(`${route.apiBase}/scheme`),
    fetch(`${route.apiBase}/state`),
    route.festID ? fetch(`/api/fest/${route.festID}`) : Promise.resolve(null),
  ]);
  if (!schemeResp.ok) throw new Error(await schemeResp.text());
  if (!stateResp.ok) throw new Error(await stateResp.text());
  if (festResp && !festResp.ok) throw new Error(await festResp.text());
  return {
    scheme: await schemeResp.json(),
    state: await stateResp.json(),
    fest: festResp ? await festResp.json() : null,
  };
}

async function revalidateAll() {
  const prevSchemeJSON = JSON.stringify(scheme);
  const prevStateJSON = JSON.stringify(state);
  const fresh = await fetchAllRaw();
  const freshSchemeJSON = JSON.stringify(fresh.scheme);
  const freshStateJSON = JSON.stringify(fresh.state);
  scheme = fresh.scheme;
  state = fresh.state;
  fest = fresh.fest;
  writeGameCache();
  if (freshSchemeJSON === prevSchemeJSON && freshStateJSON === prevStateJSON) return;
  initFromScheme();
  ensureState();
  render();
}

function initFromScheme() {
  participants = schemeParticipants();
  themesCount = Number(scheme.themes) > 0 ? Number(scheme.themes) : (isTeamMode() ? KSI_THEMES : 8);
}

function schemeParticipants() {
  if (Array.isArray(scheme.participants) && scheme.participants.length > 0) {
    return scheme.participants.slice();
  }
  if (isTeamMode() && Array.isArray(scheme.teams) && scheme.teams.length > 0) {
    return scheme.teams.map((team) => team.name || "");
  }
  if (isTeamMode()) return [];
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
  invalidateScores();
  invalidateDetailedOrder();
}

function render(options = {}) {
  if (!scheme || !state) return;
  const defaultTitle = gameTitleFallback();
  normalizeActiveCell();
  setHeading(scheme.title || defaultTitle);
  document.title = pageTitle();
  if (isTeamMode()) {
    rememberTabScroll(renderedTab);
    if (!KSI_TABS.some((t) => t.key === activeTab)) activeTab = "detailed";
    renderTabs();
    const node = activeTab === "results" ? buildResultsTable() : buildTable();
    renderedTable = activeTab === "detailed" ? node : null;
    if (activeTab !== "detailed") resetTableIndex();
    siRoot.replaceChildren(node);
    renderedTab = activeTab;
    restoreTabScroll(activeTab);
    updateResultsScrollState();
    if (activeTab === "detailed" || activeTab === "results") teamNameOverflow.schedule();
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
    updateResultsScrollState();
  }
  refreshPresence();
}

function buildTable() {
  const scores = getScoreCache();
  const showPlaceColumn = false;
  const themes = Array.from({length: themesCount}, (_, index) => ({
    label: `Т${index + 1}`,
    questionLabels: QUESTION_VALUES,
  }));
  const rows = detailedPlayerOrder().map((playerIndex) => ({
    rowClassName: isActivePlayerRow(playerIndex) ? "active-team-row" : "",
    nameCell: nameCell(state.participants[playerIndex], playerIndex),
    totalCell: indexedCell(scores.totals[playerIndex], "sticky sticky-total number total-cell", {player: playerIndex}),
    placeCell: showPlaceColumn
      ? indexedCell(scores.places[playerIndex] || "", "sticky sticky-place number place-cell", {player: playerIndex})
      : null,
    themes: themes.map((_, themeIndex) => ({
      answers: QUESTION_VALUES.map((__, answerIndex) => {
        const mark = state.themes[themeIndex].answers[playerIndex][answerIndex];
        return answerCell(playerIndex, themeIndex, answerIndex, mark);
      }),
      scoreCell: indexedCell(
        scores.themeScores[playerIndex][themeIndex],
        "number theme-score theme-block theme-block-score",
        {player: playerIndex, theme: themeIndex},
      ),
    })),
  }));

  const table = gameTable.buildFlatScoreTable({
    className: "match-table compact-score-table si-table od-detailed ksi-detailed",
    rowMarkerColumn: true,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
    nameHeader: battleHeader(),
    placeColumn: showPlaceColumn,
    themes,
    rows,
    events: {
      click: handleTableClick,
      focusin: handleTableFocusIn,
      change: handleTableChange,
    },
  });
  table.classList.toggle("match-finished", state.finished);
  tableIndex = gameTable.createScoreTableIndex(table, {entity: "player"});
  activeAnswerNode = state.finished || viewer ? null : tableIndex.get("answer", activeCell);
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
  head.appendChild(gameTable.th("Σ", "results-num-head results-total-head"));
  head.appendChild(gameTable.th("Σ+", "results-num-head"));
  for (const value of RESULT_VALUES) {
    head.appendChild(gameTable.th(value, "results-num-head"));
  }
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  rows.forEach((row, rowIdx) => {
    const tr = document.createElement("tr");
    const classes = ["results-row"];
    if (rowIdx === 0) classes.push("results-group-first");
    if (rowIdx === rows.length - 1) classes.push("results-group-last");
    tr.className = classes.join(" ");
    tr.appendChild(gameTable.td(row.placeText, "results-place"));
    const nameTd = document.createElement("td");
    nameTd.className = "results-team";
    const nameWrap = document.createElement("span");
    nameWrap.className = "results-team-name-wrap";
    const nameSpan = document.createElement("span");
    nameSpan.className = "results-team-name";
    nameSpan.textContent = row.name;
    nameSpan.tabIndex = 0;
    nameSpan.setAttribute("aria-label", row.name);
    nameWrap.appendChild(nameSpan);
    nameTd.appendChild(nameWrap);
    const fullName = document.createElement("span");
    fullName.className = "results-team-name-popover";
    fullName.textContent = row.name;
    nameTd.appendChild(fullName);
    tr.appendChild(nameTd);
    tr.appendChild(gameTable.td(row.metrics.total, "results-num total-cell results-total"));
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

function updateResultsScrollState() {
  const frame = scrollFrame();
  if (!frame) return;
  frame.classList.toggle("results-scroll-left", isTeamMode() && activeTab === "results" && frame.scrollLeft > 1);
  frame.classList.toggle("detailed-scroll-left", isTeamMode() && activeTab === "detailed" && frame.scrollLeft > 1);
}

function detailedPlayerOrder() {
  if (detailedOrderCache) return detailedOrderCache;
  const order = state.participants.map((_, index) => index);
  if (isTeamMode()) order.sort((a, b) => compareParticipantNames(a, b));
  detailedOrderCache = order;
  return detailedOrderCache;
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
  if (isTeamMode()) {
    cell.className = "sticky sticky-name team-name od-detailed-team-cell ksi-detailed-team-cell";
    const labelText = name || participantFallback(playerIndex);
    const layout = document.createElement("span");
    layout.className = "od-detailed-team-layout";

    const nameWrap = document.createElement("span");
    nameWrap.className = "od-detailed-team-name-wrap";
    const label = document.createElement("span");
    label.className = "readonly-team-name od-detailed-team-name";
    label.textContent = labelText;
    label.tabIndex = 0;
    label.setAttribute("aria-label", labelText);
    nameWrap.appendChild(label);
    layout.appendChild(nameWrap);
    cell.appendChild(layout);

    const fullName = document.createElement("span");
    fullName.className = "od-detailed-team-name-popover";
    fullName.textContent = labelText;
    cell.appendChild(fullName);
    return cell;
  }
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

function indexedCell(content, className, dataset = {}) {
  const cell = document.createElement("td");
  cell.className = className;
  cell.textContent = gameTable.formatDisplayText(content);
  for (const [key, value] of Object.entries(dataset)) {
    cell.dataset[key] = String(value);
  }
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
  if (isTeamMode()) {
    const node = document.createElement("th");
    node.className = "sticky sticky-name battle od-detailed-team-head";
    node.appendChild(detailedNameHeader());
    return node;
  }
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

function detailedNameHeader() {
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout od-detailed-team-head-layout";
  const label = document.createElement("span");
  label.className = "od-detailed-team-head-label";
  label.textContent = "Команда";
  layout.appendChild(label);
  return layout;
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
    saveState(["finished"], target.checked);
    render({preserveScroll: true});
    return;
  }
  if (target instanceof HTMLInputElement && target.classList.contains("venue-input")) {
    if (viewer || isTeamMode()) return;
    const playerIndex = Number(target.dataset.player);
    if (!Number.isInteger(playerIndex) || playerIndex < 0 || playerIndex >= state.participants.length) return;
    const name = target.value.trim();
    state.participants[playerIndex] = name;
    invalidateDetailedOrder();
    saveState(["participants", playerIndex], name);
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

function getScoreCache() {
  if (scoreCache) return scoreCache;
  const themeScores = state.participants.map(() => Array(themesCount).fill(0));
  const totals = state.participants.map(() => 0);
  for (let playerIndex = 0; playerIndex < state.participants.length; playerIndex++) {
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      const row = state.themes[themeIndex]?.answers?.[playerIndex] || [];
      let score = 0;
      for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
        score += scoreContribution(row[answerIndex], answerIndex);
      }
      themeScores[playerIndex][themeIndex] = score;
      totals[playerIndex] += score;
    }
  }
  scoreCache = {themeScores, totals, places: gameTable.computePlaces(totals)};
  return scoreCache;
}

function invalidateScores() {
  scoreCache = null;
}

function invalidateDetailedOrder() {
  detailedOrderCache = null;
}

function resetTableIndex() {
  tableIndex = null;
  activeAnswerNode = null;
  clearActivePlayerRows();
}

function scoreContribution(mark, answerIndex) {
  const value = QUESTION_VALUES[answerIndex];
  if (mark === "right") return value;
  if (mark === "wrong") return -value;
  return 0;
}

function applyScoreDelta(player, theme, answer, previousMark, nextMark) {
  const scores = getScoreCache();
  const delta = scoreContribution(nextMark, answer) - scoreContribution(previousMark, answer);
  if (!delta) return;
  scores.themeScores[player][theme] += delta;
  scores.totals[player] += delta;
  scores.places = gameTable.computePlaces(scores.totals);
}

function selectCell(player, theme, answer, options = {}) {
  activeCell = {player, theme, answer};
  markActive();
  if (options.focus !== false) {
    findActive()?.focus();
  }
}

function markActive() {
  clearActivePlayerRows();
  if (activeAnswerNode) {
    activeAnswerNode.classList.remove("active");
    activeAnswerNode = null;
  }
  if (state.finished || viewer || !isDetailedTabActive()) return;
  activeAnswerNode = findActive();
  if (activeAnswerNode) {
    activeAnswerNode.classList.add("active");
    markActivePlayerRows(activeAnswerNode);
  }
}

function isActivePlayerRow(playerIndex) {
  return !state.finished &&
    !viewer &&
    isDetailedTabActive() &&
    activeCell.player === playerIndex;
}

function clearActivePlayerRows() {
  if (activePlayerRows.length > 0) {
    activePlayerRows.forEach((row) => row.classList.remove("active-team-row"));
    activePlayerRows = [];
    return;
  }
  siRoot.querySelectorAll(".active-team-row").forEach((row) => row.classList.remove("active-team-row"));
}

function markActivePlayerRows(cell) {
  const row = cell?.closest?.("tr");
  if (!row) return;
  row.classList.add("active-team-row");
  activePlayerRows = [row];
}

function findActive() {
  const indexed = tableIndex?.get("answer", activeCell);
  if (indexed) return indexed;
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
  const previousMark = row[activeCell.answer];
  if (previousMark === mark) return;
  getScoreCache();
  row[activeCell.answer] = mark;
  applyScoreDelta(activeCell.player, activeCell.theme, activeCell.answer, previousMark, mark);
  updateAnswerCell(activeCell.player, activeCell.theme, activeCell.answer, mark);
  refreshChangedScores(activeCell.player, activeCell.theme);
  saveState(["themes", activeCell.theme, "answers", activeCell.player, activeCell.answer], mark);
}

function updateAnswerCell(player, theme, answer, mark) {
  const cell = tableIndex?.get("answer", {player, theme, answer}) ||
    siRoot.querySelector(`.answer-cell[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"][data-answer="${gameTable.cssEscape(answer)}"]`);
  if (!cell) return;
  gameTable.setMarkClass(cell, mark);
  cell.title = answerTitle(player, theme, answer);
}

function refreshScores() {
  if (!state?.participants) return;
  const scores = getScoreCache();
  state.participants.forEach((_, playerIndex) => {
    gameTable.setNodeText(scoreNode("total", {player: playerIndex}), scores.totals[playerIndex]);
    gameTable.setNodeText(scoreNode("place", {player: playerIndex}), scores.places[playerIndex] || "");
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      gameTable.setNodeText(
        scoreNode("themeScore", {player: playerIndex, theme: themeIndex}),
        scores.themeScores[playerIndex][themeIndex],
      );
    }
  });
}

function refreshChangedScores(player, theme) {
  const scores = getScoreCache();
  gameTable.setNodeText(scoreNode("total", {player}), scores.totals[player]);
  gameTable.setNodeText(scoreNode("themeScore", {player, theme}), scores.themeScores[player][theme]);
  refreshPlaces(scores.places);
}

function refreshChangedScoreSet(changedThemes) {
  if (!changedThemes || changedThemes.size === 0) return;
  const scores = getScoreCache();
  for (const [player, themes] of changedThemes.entries()) {
    gameTable.setNodeText(scoreNode("total", {player}), scores.totals[player]);
    for (const theme of themes) {
      gameTable.setNodeText(scoreNode("themeScore", {player, theme}), scores.themeScores[player][theme]);
    }
  }
  refreshPlaces(scores.places);
}

function refreshPlaces(places) {
  state.participants.forEach((_, playerIndex) => {
    gameTable.setNodeText(scoreNode("place", {player: playerIndex}), places[playerIndex] || "");
  });
}

function scoreNode(name, values) {
  return tableIndex?.get(name, values);
}

function patchTable(previous = null) {
  if (!renderedTable || !tableIndex) return false;
  const participantNamesChanged = previous?.participants && !gameTable.sameArray(previous.participants, state.participants);
  const changedThemes = new Map();
  state.participants.forEach((participant, playerIndex) => {
    const input = tableIndex.get("input", {player: playerIndex});
    if (input) {
      if (document.activeElement !== input) input.value = participant || "";
      input.placeholder = participantFallback(playerIndex);
    }
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
        const mark = state.themes[themeIndex].answers[playerIndex][answerIndex];
        const previousMark = previous?.themes?.[themeIndex]?.answers?.[playerIndex]?.[answerIndex];
        if (!previous || participantNamesChanged || previousMark !== mark) {
          updateAnswerCell(playerIndex, themeIndex, answerIndex, mark);
        }
        if (!previous || previousMark !== mark) {
          rememberChangedTheme(changedThemes, playerIndex, themeIndex);
        }
      }
    }
  });
  if (previous) refreshChangedScoreSet(changedThemes);
  else refreshScores();
  markActive();
  return true;
}

function rememberChangedTheme(changedThemes, player, theme) {
  let themes = changedThemes.get(player);
  if (!themes) {
    themes = new Set();
    changedThemes.set(player, themes);
  }
  themes.add(theme);
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

function pageTitle() {
  const gameTitle = String(scheme?.title || gameTitleFallback()).trim() || gameTitleFallback();
  const festTitle = String(fest?.title || "").trim();
  return festTitle ? `${gameTitle} · ${festTitle}` : gameTitle;
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
  } else if (key === "w" || key === "ц" || key === "-" || key === "2") {
    event.preventDefault();
    setMark("wrong");
  } else if (key === "backspace" || key === "delete" || event.key === " ") {
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

function saveState(path, value) {
  if (Array.isArray(path)) {
    syncState().patch(path, value);
    return;
  }
  syncState().save();
}

function setHeading(text) {
  if (pageHeading) pageHeading.textContent = text;
  renderGameBreadcrumbs(text);
}

function renderGameBreadcrumbs(gameTitle) {
  if (!breadcrumbsNode || !route.festID) return;
  gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
    festHref: viewer ? `/fest/${route.festID}` : `/host/fest/${route.festID}`,
    festTitle: fest?.title || "Фест",
    gameTitle: gameTitle || scheme?.title || gameTitleFallback(),
  });
}

function connectEvents() {
  syncState().connect();
}

function syncState() {
  if (stateSync) return stateSync;
  stateSync = gameTable.createStateSync({
    readonly: viewer,
    stateURL: `${route.apiBase}/state`,
    eventsURL: `/events?fest_id=${encodeURIComponent(route.festID)}`,
    scope: `game-state:${route.gameID}`,
    getState: () => state,
    setStatus,
    onRemoteState: applyRemoteState,
  });
  return stateSync;
}

function connectPresence() {
  if (viewer || presence || !route.festID) return;
  presence = gameTable.createHostPresence({
    root: siRoot,
    eventsURL: `/host-events?fest_id=${encodeURIComponent(route.festID)}`,
    presenceURL: `/api/fest/${route.festID}/presence`,
    cursorFromElement: siPresenceCursorFromElement,
    getCursor: currentSIPresenceCursor,
    findTarget: findSIPresenceTarget,
  });
  presence.connect();
}

function refreshPresence() {
  presence?.refresh();
}

function currentSIPresenceCursor() {
  const focused = siPresenceCursorFromElement(document.activeElement);
  if (focused) return focused;
  if (!isDetailedTabActive()) return null;
  return {
    app: "si",
    kind: "answer",
    gameID: route.gameID,
    player: activeCell.player,
    theme: activeCell.theme,
    answer: activeCell.answer,
  };
}

function siPresenceCursorFromElement(element) {
  const target = element?.closest?.(".answer-cell,.venue-input,.finish-toggle");
  if (!target || !siRoot.contains(target)) return null;
  if (target.classList.contains("answer-cell")) {
    return {
      app: "si",
      kind: "answer",
      gameID: route.gameID,
      player: Number(target.dataset.player),
      theme: Number(target.dataset.theme),
      answer: Number(target.dataset.answer),
    };
  }
  if (target.classList.contains("venue-input")) {
    return {app: "si", kind: "participant", gameID: route.gameID, player: Number(target.dataset.player)};
  }
  if (target.classList.contains("finish-toggle")) {
    return {app: "si", kind: "finish", gameID: route.gameID};
  }
  return null;
}

function findSIPresenceTarget(cursor) {
  if (!cursor || cursor.app !== "si" || String(cursor.gameID) !== String(route.gameID)) return null;
  if (cursor.kind === "answer") {
    return siRoot.querySelector(
      `.answer-cell[data-player="${gameTable.cssEscape(cursor.player)}"][data-theme="${gameTable.cssEscape(cursor.theme)}"][data-answer="${gameTable.cssEscape(cursor.answer)}"]`,
    );
  }
  if (cursor.kind === "participant") {
    return siRoot.querySelector(`.venue-input[data-player="${gameTable.cssEscape(cursor.player)}"]`);
  }
  if (cursor.kind === "finish") {
    return siRoot.querySelector(".finish-toggle");
  }
  return null;
}

function applyRemoteState(nextState) {
  const previous = state;
  state = nextState;
  ensureState();
  if (canPatchState(previous, state) && patchTable(previous)) return;
  render({preserveScroll: true});
}

document.addEventListener("keydown", handleKeydown);

loadAll()
  .then(() => {
    setStatus("saved");
    connectEvents();
    connectPresence();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
