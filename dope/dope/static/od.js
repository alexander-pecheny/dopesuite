const odRoot = document.getElementById("odTable");
const odTabsRoot = document.getElementById("odTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const progressNode = document.getElementById("odProgress");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const {th, td, option} = gameTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);
const teamNameOverflow = gameTable.createTeamNameOverflowController({
  root: odRoot,
  detailed: {
    cellSelector: ".od-detailed-team-cell",
    nameSelector: ".od-detailed-team-name",
    truncatedClass: "od-detailed-team-cell-truncated",
  },
  results: {
    cellSelector: ".results-team",
    nameSelector: ".results-team-name",
    truncatedClass: "results-team-truncated",
    citySelector: ".results-team-city",
    cityTruncatedClass: "results-team-city-truncated",
  },
});
const teamNameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});
const route = gameTable.parseGameRoute();
const viewer = Boolean(route.viewer);
// The URL carries the game slug, but the server broadcasts SSE state under the
// numeric game id (`game-state:<id>`). Default to the slug and upgrade to the
// numeric id from __GAME_INIT__ so the scope matches and remote edits apply.
let scopeGameID = route.gameID;
// staticMode: served as a precomputed snapshot under DDoS lockdown. Skip the SSE
// connection and refresh by reloading on a jitter. Captured before consumeGameInit
// nulls window.__GAME_INIT__.
const staticMode = Boolean(window.__GAME_INIT__?.static);
document.body.classList.toggle("viewer-readonly", viewer);
if (viewer) {
  if (window.__GAME_INIT__?.canEdit) gameTable.mountEditorLink(statusNode);
} else {
  gameTable.mountViewerLink(statusNode);
}
let scheme = null;
let state = null;
let fest = null;
let initialStateSeq = 0; // game-state scope seq at page render; seeds the SSE client's lastSeq
let tourLengths = [];
let totalQuestions = 0;
let renderedTab = null;
let questionStatsCache = null;
let activeEntryEditor = null;
let activeEntryRows = [];
let stateSync = null;
let recorder = null;
let presence = null;
const tabCache = new Map();
const tabScroll = new Map();
const resultsExpandedTours = new Set();
const resultsExpandedShootouts = new Set();
let numberToIndexCache = null;
let entrySuggest = null;
let entrySelection = null;
let entryDragSelection = null;
let entrySuppressClickSelection = false;
const undoStack = [];
const UNDO_LIMIT = 100;

const ENTRY_SELECTION_CLASSES = [
  "entry-selected",
  "entry-selection-anchor",
  "entry-selection-top",
  "entry-selection-bottom",
  "entry-selection-left",
  "entry-selection-right",
];

const TABS = [
  {key: "results", label: "Итог"},
  {key: "detailed", label: "Подробно"},
  {key: "input", label: "Ввод"},
];

function tabFromHash() {
  const key = (window.location.hash || "").replace(/^#/, "");
  return TABS.some((t) => t.key === key) ? key : null;
}
let activeTab = tabFromHash() || (viewer ? "results" : "input");
window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});

window.addEventListener("resize", () => {
  if (renderedTab === "detailed" || renderedTab === "results") teamNameOverflow.schedule();
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

// consumeGameInit hydrates scheme/state/fest from the server-inlined
// window.__GAME_INIT__ payload, skipping the three cold API round trips that
// loadAll would otherwise make. Returns true on success.
function consumeGameInit() {
  const init = window.__GAME_INIT__;
  if (!init || !init.scheme || !init.state) return false;
  window.__GAME_INIT__ = null;
  if (init.gameID != null) scopeGameID = String(init.gameID);
  if (init.seq != null) initialStateSeq = Number(init.seq) || 0;
  scheme = init.scheme;
  state = init.state;
  fest = init.fest || null;
  initFromScheme();
  ensureState();
  invalidateAllCaches();
  render();
  writeGameCache();
  return true;
}

function gameCacheKey() {
  return `od:game:${route.festID || ""}:${route.gameID || ""}`;
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
    // ignore quota / disabled
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
  invalidateAllCaches();
  render();
  return true;
}

async function fetchAll() {
  const data = await fetchAllRaw();
  scheme = data.scheme;
  state = data.state;
  fest = data.fest;
  initFromScheme();
  ensureState();
  invalidateAllCaches();
  render();
  writeGameCache();
}

async function fetchAllRaw() {
  const festURL = route.apiBase || (route.festID ? `/api/fest/${route.festID}` : "");
  const [schemeResp, stateResp, festResp] = await Promise.all([
    fetch(`${route.apiBase}/scheme`),
    fetch(`${route.apiBase}/state`),
    festURL ? fetch(festURL) : Promise.resolve(null),
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
  invalidateAllCaches();
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
  const targetCount = state.teams.length || scheme.nTeams || 0;
  while (state.teams.length < targetCount) {
    state.teams.push({name: "", city: ""});
  }
  const n = state.teams.length;
  if (!Array.isArray(state.entries)) state.entries = [];
  while (state.entries.length < totalQuestions) state.entries.push([]);
  state.entries = state.entries.slice(0, totalQuestions).map((row) => {
    const arr = Array.isArray(row) ? row.slice(0, n) : [];
    while (arr.length < n) arr.push(0);
    return arr.map((v) => {
      const num = Number(v);
      return Number.isInteger(num) && num >= 0 ? num : 0;
    });
  });
  if (!Array.isArray(state.completed)) state.completed = [];
  while (state.completed.length < totalQuestions) state.completed.push(false);
  state.completed = state.completed.slice(0, totalQuestions).map(Boolean);
  if (!Array.isArray(state.shootoutRounds)) state.shootoutRounds = [];
  state.shootoutRounds = state.shootoutRounds
    .map(normalizeShootoutRound)
    .filter((round) => round.teams.length > 0);
  delete state.answers;
  delete state.finished;
}

function normalizeShootoutRound(round) {
  const source = round && typeof round === "object" ? round : {};
  const seen = new Set();
  const teams = [];
  const rawTeams = Array.isArray(source.teams) ? source.teams : [];
  for (const value of rawTeams) {
    const number = Number(value);
    if (!Number.isInteger(number) || number <= 0 || seen.has(number)) continue;
    seen.add(number);
    teams.push(number);
  }

  let rawAnswers = Array.isArray(source.answers) ? source.answers : [];
  if (rawAnswers.length === 0 && Array.isArray(source.questions)) rawAnswers = source.questions;
  const answers = rawAnswers.map((row) => {
    const values = Array.isArray(row) ? row.slice(0, teams.length) : [];
    while (values.length < teams.length) values.push("");
    return values.map(normalizeShootoutMark);
  });
  let rawEntries = Array.isArray(source.entries) ? source.entries : [];
  if (rawEntries.length === 0 && answers.length > 0) {
    rawEntries = answers.map((row) =>
      teams.filter((_, index) => normalizeShootoutMark(row?.[index]) === "right"));
  }
  const questionCount = Math.max(teams.length > 0 ? 1 : 0, answers.length, rawEntries.length);
  while (answers.length < questionCount) {
    answers.push(Array(teams.length).fill(""));
  }
  const entries = [];
  for (let questionIndex = 0; questionIndex < questionCount; questionIndex++) {
    const rawRow = Array.isArray(rawEntries[questionIndex]) ? rawEntries[questionIndex] : [];
    entries.push(normalizeShootoutEntryRowForTeams(rawRow, teams));
  }
  let completed;
  if (Array.isArray(source.completed)) {
    completed = source.completed.slice(0, questionCount).map(Boolean);
    while (completed.length < questionCount) completed.push(false);
  } else {
    completed = answers.map((row) => (row || []).some((mark) => normalizeShootoutMark(mark) === "right"));
    while (completed.length < questionCount) completed.push(false);
  }
  const normalized = {teams, entries, completed, answers};
  for (let questionIndex = 0; questionIndex < entries.length; questionIndex++) {
    syncShootoutAnswersFromEntries(normalized, questionIndex);
  }
  return normalized;
}

function normalizeShootoutMark(value) {
  return value === "right" ? "right" : "";
}

function normalizeShootoutEntryRow(row, length) {
  const values = Array.isArray(row) ? row.slice(0, length) : [];
  while (values.length < length) values.push(0);
  return values.map((value) => {
    const number = Number(value);
    return Number.isInteger(number) && number >= 0 ? number : 0;
  });
}

function normalizeShootoutEntryRowForTeams(row, teams) {
  const out = Array(teams.length).fill(0);
  for (const value of normalizeShootoutEntryRow(row, teams.length)) {
    if (!value) continue;
    const participantIndex = teams.indexOf(value);
    if (participantIndex >= 0) out[participantIndex] = value;
  }
  return out;
}

function syncShootoutAnswersFromEntries(round, questionIndex) {
  if (!round) return;
  const row = normalizeShootoutEntryRowForTeams(round.entries?.[questionIndex], round.teams);
  if (!Array.isArray(round.entries)) round.entries = [];
  round.entries[questionIndex] = row;
  if (!Array.isArray(round.answers)) round.answers = [];
  while (round.answers.length <= questionIndex) round.answers.push(Array(round.teams.length).fill(""));
  const answerRow = Array(round.teams.length).fill("");
  for (const number of row) {
    if (!number) continue;
    const participantIndex = round.teams.indexOf(number);
    if (participantIndex >= 0) answerRow[participantIndex] = "right";
  }
  round.answers[questionIndex] = answerRow;
}

function invalidateAllCaches() {
  rememberTabScroll(activeTab);
  activeEntryEditor = null;
  closeEntrySuggest();
  questionStatsCache = null;
  numberToIndexCache = null;
  for (const pane of tabCache.values()) pane.remove();
  tabCache.clear();
}

function invalidateScoreCaches() {
  questionStatsCache = null;
  invalidateTabCache("detailed", "results");
}

function invalidateShootoutCaches() {
  invalidateTabCache("input", "detailed", "results");
}

function teamNumber(teamIndex) {
  const value = Number(state.teams[teamIndex]?.number);
  return Number.isInteger(value) && value > 0 ? value : 0;
}

function allTeamsNumbered() {
  if (!state.teams.length) return false;
  for (let i = 0; i < state.teams.length; i++) {
    if (!teamNumber(i)) return false;
  }
  return true;
}

function teamIndexByNumber(number) {
  if (!Number.isInteger(number) || number < 1) return -1;
  if (!numberToIndexCache) {
    numberToIndexCache = new Map();
    for (let i = 0; i < state.teams.length; i++) {
      const n = teamNumber(i);
      if (n) numberToIndexCache.set(n, i);
    }
  }
  const found = numberToIndexCache.get(number);
  return found === undefined ? -1 : found;
}

function numbersPageURL() {
  if (!route.festID) return "#";
  return `/host/fest/${route.festID}/numbers`;
}

function invalidateTabCache(...tabs) {
  if (tabs.includes(activeTab)) rememberTabScroll(activeTab);
  for (const tab of tabs) {
    const pane = tabCache.get(tab);
    if (pane) pane.remove();
    tabCache.delete(tab);
  }
}

function questionStats() {
  if (questionStatsCache) return questionStatsCache;
  questionStatsCache = [];
  for (let q = 0; q < totalQuestions; q++) {
    const counts = new Map();
    if (state.completed[q]) {
      const entries = state.entries[q] || [];
      for (const value of entries) {
        const teamIndex = teamIndexByNumber(value);
        if (teamIndex < 0) continue;
        counts.set(teamIndex, (counts.get(teamIndex) || 0) + 1);
      }
    }
    questionStatsCache.push({
      completed: Boolean(state.completed[q]),
      counts,
      validCount: counts.size,
    });
  }
  return questionStatsCache;
}

function teamTookQuestion(teamIndex, qIndex, stats = questionStats()) {
  return Boolean(stats[qIndex]?.counts.has(teamIndex));
}

function render() {
  if (!state || !scheme) return;
  if (renderedTab === "input" && activeTab !== "input") closeEntryEditor();
  const renderedPane = tabCache.get(renderedTab);
  if (renderedPane?.isConnected) rememberTabScroll(renderedTab);
  setHeading(scheme.title || "ОД");
  document.title = pageTitle();
  if (!TABS.some((t) => t.key === activeTab)) activeTab = TABS[0].key;
  renderTabs();
  updateHeaderProgress();
  const activePane = getTabPane(activeTab);
  for (const pane of tabCache.values()) pane.hidden = pane !== activePane;
  if (!activePane.isConnected) odRoot.appendChild(activePane);
  renderedTab = activeTab;
  restoreTabScroll(activeTab);
  updateResultsScrollState();
  if (activeTab === "detailed" || activeTab === "results") teamNameOverflow.schedule(activePane);
  refreshPresence();
}

function getTabPane(tab) {
  const cached = tabCache.get(tab);
  if (cached) return cached;
  let node;
  if (tab === "input") node = buildInputView();
  else if (tab === "detailed") node = buildDetailedTable();
  else node = buildResultsTable();
  const pane = document.createElement("div");
  pane.className = "od-pane";
  pane.dataset.tab = tab;
  pane.appendChild(node);
  tabCache.set(tab, pane);
  return pane;
}

function scrollFrame() {
  return document.querySelector(".sheet-frame");
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
  frame.classList.toggle("results-scroll-left", activeTab === "results" && frame.scrollLeft > 1);
  frame.classList.toggle("detailed-scroll-left", activeTab === "detailed" && frame.scrollLeft > 1);
}

function pageTitle() {
  const gameTitle = String(scheme?.title || "ОД").trim() || "ОД";
  const festTitle = String(fest?.title || "").trim();
  return festTitle ? `${gameTitle} · ${festTitle}` : gameTitle;
}

function toggleResultsTour(tourIndex) {
  if (resultsExpandedTours.has(tourIndex)) resultsExpandedTours.delete(tourIndex);
  else resultsExpandedTours.add(tourIndex);
  rememberTabScroll("results");
  invalidateTabCache("results");
  render();
}

function toggleResultsShootout(roundIndex) {
  if (resultsExpandedShootouts.has(roundIndex)) resultsExpandedShootouts.delete(roundIndex);
  else resultsExpandedShootouts.add(roundIndex);
  rememberTabScroll("results");
  invalidateTabCache("results");
  render();
}

function updateHeaderProgress() {
  if (!progressNode) return;
  const lastQ = lastEnteredQuestion();
  progressNode.textContent = lastQ ? `Введён вопрос ${lastQ}` : "Ни одного вопроса не введено";
}

function renderTabs() {
  odTabsRoot.replaceChildren();
  for (const tab of TABS) {
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
    odTabsRoot.appendChild(btn);
  }
}

// === Ввод ===

function countValidEntries(qIndex, stats = questionStats()) {
  return stats[qIndex]?.validCount || 0;
}

function questionCounts(qIndex) {
  return state.completed[qIndex] ? countValidEntries(qIndex) : 0;
}

function buildInputView() {
  const wrapper = document.createElement("div");
  wrapper.className = "od-input-wrap";
  if (!allTeamsNumbered()) {
    wrapper.appendChild(buildInputGate());
    return wrapper;
  }
  const tables = document.createElement("div");
  tables.className = "od-input-tables";
  tables.appendChild(buildInputTable());
  const shootout = buildInputShootoutTable();
  if (shootout) tables.appendChild(shootout);
  wrapper.appendChild(tables);
  updateEntrySelectionSoon();
  return wrapper;
}

function updateEntrySelectionSoon() {
  window.requestAnimationFrame(updateEntrySelectionUI);
}

function updateEntrySelectionUI() {
  odRoot.querySelectorAll(".entry-cell.entry-selected, .entry-cell.entry-selection-anchor, .entry-cell.entry-selection-top, .entry-cell.entry-selection-bottom, .entry-cell.entry-selection-left, .entry-cell.entry-selection-right").forEach((cell) => {
    cell.classList.remove(...ENTRY_SELECTION_CLASSES);
  });
  const selection = normalizedEntrySelection();
  if (selection) {
    for (let row = selection.rowStart; row <= selection.rowEnd; row++) {
      for (let q = selection.qStart; q <= selection.qEnd; q++) {
        const cell = entryCellNode(q, row);
        if (!cell) continue;
        cell.classList.add("entry-selected");
        if (row === selection.rowStart) cell.classList.add("entry-selection-top");
        if (row === selection.rowEnd) cell.classList.add("entry-selection-bottom");
        if (q === selection.qStart) cell.classList.add("entry-selection-left");
        if (q === selection.qEnd) cell.classList.add("entry-selection-right");
      }
    }
    const anchor = entryCellNode(entrySelection.anchorQ, entrySelection.anchorRow);
    if (anchor) anchor.classList.add("entry-selection-anchor");
  }
}

function normalizedEntrySelection() {
  if (!entrySelection) return null;
  const qStart = Math.max(0, Math.min(entrySelection.anchorQ, entrySelection.focusQ));
  const qEnd = Math.min(totalQuestions - 1, Math.max(entrySelection.anchorQ, entrySelection.focusQ));
  const rowStart = Math.max(0, Math.min(entrySelection.anchorRow, entrySelection.focusRow));
  const rowEnd = Math.min(state.teams.length - 1, Math.max(entrySelection.anchorRow, entrySelection.focusRow));
  if (qStart > qEnd || rowStart > rowEnd) return null;
  return {qStart, qEnd, rowStart, rowEnd};
}

function setEntrySelection(anchorQ, anchorRow, focusQ = anchorQ, focusRow = anchorRow, options = {}) {
  if (viewer) return;
  anchorQ = gameTable.clamp(Number(anchorQ), 0, totalQuestions - 1);
  focusQ = gameTable.clamp(Number(focusQ), 0, totalQuestions - 1);
  anchorRow = gameTable.clamp(Number(anchorRow), 0, state.teams.length - 1);
  focusRow = gameTable.clamp(Number(focusRow), 0, state.teams.length - 1);
  entrySelection = {anchorQ, anchorRow, focusQ, focusRow};
  updateEntrySelectionUI();
  const focusCell = entryCellNode(focusQ, focusRow);
  if (focusCell && options.focus !== false) {
    focusCell.focus({preventScroll: options.preventScroll});
    markActiveEntryRow(focusCell);
  }
}

function entryCellNode(qIndex, rowIndex) {
  return odRoot.querySelector(`.entry-cell[data-q="${gameTable.cssEscape(qIndex)}"][data-row="${gameTable.cssEscape(rowIndex)}"]:not([data-entry-kind="shootout"])`);
}

function entryCellPosition(cell) {
  if (!cell || isShootoutEntryCell(cell)) return null;
  const q = Number(cell.dataset.q);
  const row = Number(cell.dataset.row);
  if (!Number.isInteger(q) || !Number.isInteger(row)) return null;
  return {q, row};
}

function selectedEntryText() {
  const selection = normalizedEntrySelection();
  if (!selection) return "";
  const lines = [];
  for (let row = selection.rowStart; row <= selection.rowEnd; row++) {
    const cols = [];
    for (let q = selection.qStart; q <= selection.qEnd; q++) {
      const value = state.entries[q]?.[row] || 0;
      cols.push(value ? String(value) : "");
    }
    lines.push(cols.join("\t"));
  }
  return lines.join("\n");
}

function fillEntryColumnWithAllTeams(qIndex) {
  if (viewer) return;
  if (!Number.isInteger(qIndex) || qIndex < 0 || qIndex >= totalQuestions) return;
  if (state.completed[qIndex]) return;
  const numbers = state.teams
    .map((_, i) => teamNumber(i))
    .filter((n) => Number.isInteger(n) && n > 0)
    .sort((a, b) => a - b);
  const next = new Array(state.teams.length).fill(0);
  numbers.forEach((n, i) => {
    if (i < next.length) next[i] = n;
  });
  const current = state.entries[qIndex] || [];
  let same = current.length === next.length;
  for (let i = 0; same && i < next.length; i++) {
    if ((current[i] || 0) !== next[i]) same = false;
  }
  if (same) return;
  const hasExistingValues = current.some((v) => Number.isInteger(v) && v > 0);
  if (hasExistingValues && !window.confirm("Заменить значения в колонке номерами всех команд?")) return;
  const previous = current.slice();
  while (previous.length < state.teams.length) previous.push(0);
  closeEntryEditor();
  closeEntrySuggest();
  state.entries[qIndex] = next;
  pushUndoEntry({kind: "entry-column", qIndex, previous});
  invalidateScoreCaches();
  invalidateTabCache("input");
  saveState(["entries", qIndex], next);
  render();
  focusEntrySelection();
}

function pushUndoEntry(entry) {
  undoStack.push(entry);
  while (undoStack.length > UNDO_LIMIT) undoStack.shift();
}

function performUndo() {
  if (viewer || undoStack.length === 0) return false;
  const entry = undoStack.pop();
  if (!entry) return false;
  if (entry.kind === "entry-column") {
    const {qIndex, previous} = entry;
    if (!Number.isInteger(qIndex) || qIndex < 0 || qIndex >= totalQuestions) return false;
    if (state.completed[qIndex]) return false;
    const restored = previous.slice(0, state.teams.length);
    while (restored.length < state.teams.length) restored.push(0);
    closeEntryEditor();
    closeEntrySuggest();
    state.entries[qIndex] = restored;
    invalidateScoreCaches();
    invalidateTabCache("input");
    saveState(["entries", qIndex], restored);
    if (activeTab !== "input") {
      activeTab = "input";
      if (window.location.hash.replace(/^#/, "") !== "input") {
        history.replaceState(null, "", "#input");
      }
    }
    render();
    setEntrySelection(qIndex, 0, qIndex, Math.max(0, state.teams.length - 1), {focus: true});
    return true;
  }
  return false;
}

function clearSelectedEntryCells() {
  const selection = normalizedEntrySelection();
  if (!selection || viewer) return;
  closeEntryEditor();
  let changed = false;
  for (let q = selection.qStart; q <= selection.qEnd; q++) {
    for (let row = selection.rowStart; row <= selection.rowEnd; row++) {
      if (state.entries[q]?.[row]) {
        state.entries[q][row] = 0;
        changed = true;
      }
    }
  }
  if (!changed) return;
  invalidateScoreCaches();
  invalidateTabCache("input");
  saveState(["entries"], state.entries);
  render();
  focusEntrySelection();
}

function focusEntrySelection() {
  if (!entrySelection) return;
  window.requestAnimationFrame(() => {
    const cell = entryCellNode(entrySelection.focusQ, entrySelection.focusRow);
    if (cell) {
      cell.focus({preventScroll: true});
      markActiveEntryRow(cell);
    }
    updateEntrySelectionUI();
  });
}

function parseEntryClipboard(text) {
  const normalized = String(text || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const lines = normalized.split("\n");
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return lines.map((line) => line.split("\t"));
}

function clipboardValueToEntry(raw) {
  const value = String(raw || "").trim();
  if (value === "") return 0;
  if (/^\d+$/.test(value)) return Number(value);
  const lower = value.toLocaleLowerCase("ru");
  for (let teamIndex = 0; teamIndex < state.teams.length; teamIndex++) {
    if (teamLabel(teamIndex).toLocaleLowerCase("ru") === lower) {
      return teamNumber(teamIndex);
    }
  }
  return 0;
}

function pasteEntryClipboard(text) {
  const selection = normalizedEntrySelection();
  if (!selection || viewer) return;
  const rows = parseEntryClipboard(text);
  if (rows.length === 0) return;
  closeEntryEditor();
  const startQ = selection.qStart;
  const startRow = selection.rowStart;
  let changed = false;
  let lastQ = startQ;
  let lastRow = startRow;
  for (let rowOffset = 0; rowOffset < rows.length; rowOffset++) {
    const rowIndex = startRow + rowOffset;
    if (rowIndex >= state.teams.length) break;
    const cols = rows[rowOffset];
    for (let colOffset = 0; colOffset < cols.length; colOffset++) {
      const qIndex = startQ + colOffset;
      if (qIndex >= totalQuestions) break;
      const value = clipboardValueToEntry(cols[colOffset]);
      if (state.entries[qIndex][rowIndex] !== value) {
        state.entries[qIndex][rowIndex] = value;
        changed = true;
      }
      lastQ = qIndex;
      lastRow = rowIndex;
    }
  }
  if (!changed) return;
  setEntrySelection(startQ, startRow, lastQ, lastRow, {focus: false});
  invalidateScoreCaches();
  invalidateTabCache("input");
  saveState(["entries"], state.entries);
  render();
  focusEntrySelection();
}

function startEntryEditWithText(cell, text) {
  const pos = entryCellPosition(cell);
  if (!pos) return;
  openEntryEditor(cell);
  if (!activeEntryEditor?.input) return;
  activeEntryEditor.input.value = text;
  activeEntryEditor.input.dispatchEvent(new Event("input", {bubbles: true}));
  activeEntryEditor.input.focus();
  activeEntryEditor.input.setSelectionRange(activeEntryEditor.input.value.length, activeEntryEditor.input.value.length);
}

function moveEntrySelection(dRow, dQ, extend) {
  if (!entrySelection) {
    setEntrySelection(0, 0);
    return;
  }
  const nextQ = gameTable.clamp(entrySelection.focusQ + dQ, 0, totalQuestions - 1);
  const nextRow = gameTable.clamp(entrySelection.focusRow + dRow, 0, state.teams.length - 1);
  if (extend) setEntrySelection(entrySelection.anchorQ, entrySelection.anchorRow, nextQ, nextRow);
  else setEntrySelection(nextQ, nextRow);
}

function handleEntryMouseDown(event) {
  if (viewer || event.button !== 0) return;
  if (event.target instanceof HTMLInputElement) return;
  const cell = event.target.closest?.(".entry-cell");
  const pos = entryCellPosition(cell);
  if (!pos) return;
  event.preventDefault();
  closeEntryEditor();
  entrySuppressClickSelection = Boolean(event.shiftKey && entrySelection);
  const anchor = event.shiftKey && entrySelection
    ? {q: entrySelection.anchorQ, row: entrySelection.anchorRow}
    : pos;
  setEntrySelection(anchor.q, anchor.row, pos.q, pos.row, {preventScroll: true});
  entryDragSelection = {anchorQ: anchor.q, anchorRow: anchor.row, focusQ: pos.q, focusRow: pos.row, moved: false};
  document.addEventListener("mouseup", handleEntryMouseUp, {once: true});
}

function handleEntryMouseOver(event) {
  if (!entryDragSelection || viewer) return;
  const pos = entryCellPosition(event.target.closest?.(".entry-cell"));
  if (!pos) return;
  if (pos.q !== entryDragSelection.focusQ || pos.row !== entryDragSelection.focusRow) {
    entryDragSelection.moved = true;
    entryDragSelection.focusQ = pos.q;
    entryDragSelection.focusRow = pos.row;
    setEntrySelection(entryDragSelection.anchorQ, entryDragSelection.anchorRow, pos.q, pos.row, {focus: false});
  }
}

function handleEntryMouseUp() {
  entryDragSelection = null;
}

function handleEntryDoubleClick(event) {
  if (viewer) return;
  const cell = event.target.closest?.(".entry-cell");
  if (!entryCellPosition(cell)) return;
  openEntryEditor(cell);
}

function handleEntryCopy(event) {
  if (viewer) return;
  if (event.target instanceof HTMLInputElement) return;
  const text = selectedEntryText();
  if (!text) return;
  event.preventDefault();
  event.clipboardData?.setData("text/plain", text);
}

function handleEntryPaste(event) {
  if (viewer) return;
  if (event.target instanceof HTMLInputElement) return;
  const text = event.clipboardData?.getData("text/plain") || "";
  if (!text) return;
  event.preventDefault();
  pasteEntryClipboard(text);
}

function handleEntryCellKeydown(event, cell) {
  if (viewer) return;
  if (!entryCellPosition(cell)) return;
  if (event.key === "ArrowLeft") {
    event.preventDefault();
    moveEntrySelection(0, -1, event.shiftKey);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    moveEntrySelection(0, 1, event.shiftKey);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    moveEntrySelection(-1, 0, event.shiftKey);
  } else if (event.key === "ArrowDown") {
    event.preventDefault();
    moveEntrySelection(1, 0, event.shiftKey);
  } else if (event.key === "Enter" || event.key === "F2") {
    event.preventDefault();
    openEntryEditor(cell);
  } else if (event.key === "Backspace" || event.key === "Delete" || event.key === " ") {
    event.preventDefault();
    clearSelectedEntryCells();
  } else if (!event.metaKey && !event.ctrlKey && !event.altKey && isFillAllKey(event.key)) {
    const qIndex = Number(cell.dataset.q);
    if (Number.isInteger(qIndex) && !state.completed[qIndex]) {
      event.preventDefault();
      fillEntryColumnWithAllTeams(qIndex);
      return;
    }
    event.preventDefault();
    startEntryEditWithText(cell, event.key);
  } else if (!event.metaKey && !event.ctrlKey && !event.altKey && event.key.length === 1) {
    event.preventDefault();
    startEntryEditWithText(cell, event.key);
  }
}

function isFillAllKey(key) {
  return key === "a" || key === "A" || key === "а" || key === "А";
}

function buildInputTable() {
  const n = state.teams.length;
  const showShootoutControls = !viewer && state.shootoutRounds.length === 0;
  const table = document.createElement("table");
  table.className = "entry-table" + (viewer ? " entry-readonly" : "");
  table.addEventListener("click", handleEntryClick);
  table.addEventListener("dblclick", handleEntryDoubleClick);
  table.addEventListener("mousedown", handleEntryMouseDown);
  table.addEventListener("mouseover", handleEntryMouseOver);
  table.addEventListener("copy", handleEntryCopy);
  table.addEventListener("paste", handleEntryPaste);
  table.addEventListener("input", handleEntryInput);
  table.addEventListener("keydown", handleEntryKeydown);
  table.addEventListener("focusin", handleEntryFocus);
  table.addEventListener("focusout", handleEntryFocusOut);
  table.addEventListener("change", handleEntryChange);
  const validationCounts = buildInputValidationCounts();

  const colgroup = document.createElement("colgroup");
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      const c = document.createElement("col");
      c.className = "col-entry-q" + (i === tourSize - 1 && tourIndex < tourLengths.length - 1 ? " col-entry-tour-end" : "");
      colgroup.appendChild(c);
    }
  });
  if (showShootoutControls) {
    const c = document.createElement("col");
    c.className = "col-entry-shootout-controls";
    colgroup.appendChild(c);
  }
  table.appendChild(colgroup);

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  let q = 1;
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      const cls = "entry-q-head" + (i === tourSize - 1 && tourIndex < tourLengths.length - 1 ? " entry-tour-end" : "");
      head.appendChild(th(q, cls));
      q++;
    }
  });
  if (showShootoutControls) head.appendChild(shootoutControlsHeaderCell({rowSpan: 2}));
  thead.appendChild(head);

  const lockRow = document.createElement("tr");
  let qIdx = 0;
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      const cls = "entry-lock-cell" + (i === tourSize - 1 && tourIndex < tourLengths.length - 1 ? " entry-tour-end" : "");
      lockRow.appendChild(lockCell(qIdx, cls));
      qIdx++;
    }
  });
  thead.appendChild(lockRow);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  for (let row = 0; row < n; row++) {
    const tr = document.createElement("tr");
    let qi = 0;
    tourLengths.forEach((tourSize, tourIndex) => {
      for (let i = 0; i < tourSize; i++) {
        const tourEnd = i === tourSize - 1 && tourIndex < tourLengths.length - 1;
        tr.appendChild(entryCell(qi, row, tourEnd, validationCounts[qi]));
        qi++;
      }
    });
    if (showShootoutControls) tr.appendChild(shootoutControlsBodyCell());
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  return table;
}

function buildInputShootoutTable() {
  if (!allTeamsNumbered() || state.shootoutRounds.length === 0) return null;
  const wrap = document.createElement("div");
  wrap.className = "od-shootout-entry-wrap";
  const questions = shootoutInputQuestions();
  const teamNumbers = shootoutInputTeamNumbers();
  if (questions.length === 0 || teamNumbers.length === 0) return null;

  const table = document.createElement("table");
  table.className = "entry-table od-shootout-entry-table" + (viewer ? " entry-readonly" : "");
  table.addEventListener("click", handleEntryClick);
  table.addEventListener("input", handleEntryInput);
  table.addEventListener("keydown", handleEntryKeydown);
  table.addEventListener("focusin", handleEntryFocus);
  table.addEventListener("focusout", handleEntryFocusOut);
  table.addEventListener("change", handleEntryChange);

  const colgroup = document.createElement("colgroup");
  const teamCol = document.createElement("col");
  teamCol.className = "col-entry-shootout-team";
  colgroup.appendChild(teamCol);
  for (const question of questions) {
    const c = document.createElement("col");
    c.className = "col-entry-shootout-check" + (question.lastInRound ? " col-entry-tour-end" : "");
    colgroup.appendChild(c);
  }
  if (!viewer) {
    const c = document.createElement("col");
    c.className = "col-entry-shootout-controls";
    colgroup.appendChild(c);
  }
  table.appendChild(colgroup);

  const thead = document.createElement("thead");
  const roundHead = document.createElement("tr");
  roundHead.appendChild(th("", "od-shootout-meta-round-cell"));
  for (const question of questions) {
    const cls = "entry-q-head od-shootout-round-head" + (question.lastInRound ? " entry-tour-end" : "");
    roundHead.appendChild(th(question.firstInRound ? `П${question.roundIndex + 1}` : "", cls));
  }
  if (!viewer) {
    roundHead.appendChild(shootoutControlsHeaderCell({rowSpan: 3}));
  }
  thead.appendChild(roundHead);

  const numberHead = document.createElement("tr");
  numberHead.appendChild(shootoutMetaCell());
  for (const question of questions) {
    const cls = "entry-q-head" + (question.lastInRound ? " entry-tour-end" : "");
    numberHead.appendChild(th(question.number, cls));
  }
  thead.appendChild(numberHead);

  const lockRow = document.createElement("tr");
  lockRow.appendChild(th("", "od-shootout-meta-lock-cell"));
  for (const question of questions) {
    const cls = "entry-lock-cell" + (question.lastInRound ? " entry-tour-end" : "");
    lockRow.appendChild(shootoutLockCell(question.roundIndex, question.questionIndex, cls));
  }
  thead.appendChild(lockRow);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const validationCounts = state.shootoutRounds.map((_, roundIndex) => buildShootoutInputValidationCounts(roundIndex));
  for (const number of teamNumbers) {
    const tr = document.createElement("tr");
    tr.appendChild(shootoutTeamCell(number));
    for (const question of questions) {
      const rowIndex = question.round.teams.indexOf(number);
      if (rowIndex < 0) {
        tr.appendChild(shootoutExcludedCell(question.lastInRound));
      } else {
        tr.appendChild(shootoutEntryCell(
          question.roundIndex,
          question.questionIndex,
          rowIndex,
          validationCounts[question.roundIndex]?.[question.questionIndex],
          question.lastInRound,
        ));
      }
    }
    if (!viewer) tr.appendChild(shootoutControlsBodyCell());
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}

function shootoutInputQuestions() {
  const questions = [];
  let number = 1;
  state.shootoutRounds.forEach((round, roundIndex) => {
    const count = round.entries?.length || 0;
    for (let questionIndex = 0; questionIndex < count; questionIndex++) {
      questions.push({
        round,
        roundIndex,
        questionIndex,
        number,
        firstInRound: questionIndex === 0,
        lastInRound: questionIndex === count - 1 && roundIndex < state.shootoutRounds.length - 1,
      });
      number++;
    }
  });
  return questions;
}

function shootoutInputTeamNumbers() {
  const seen = new Set();
  const numbers = [];
  for (const round of state.shootoutRounds) {
    for (const number of round.teams || []) {
      if (!number || seen.has(number)) continue;
      seen.add(number);
      numbers.push(number);
    }
  }
  return numbers;
}

function shootoutMetaCell() {
  const cell = document.createElement("th");
  cell.className = "od-shootout-meta-cell";
  const inner = document.createElement("div");
  inner.className = "od-shootout-meta-inner";
  const title = document.createElement("div");
  title.className = "od-shootout-entry-title";
  title.textContent = "Перестрелка";
  inner.append(title);
  cell.appendChild(inner);
  return cell;
}

function shootoutThemeHeaders() {
  let questionNumber = 1;
  return state.shootoutRounds.map((round, roundIndex) => {
    const questionLabels = round.answers.map(() => questionNumber++);
    return {
      label: `П${roundIndex + 1}`,
      questionLabels,
      questionClassName: "question-head shootout-head",
      labelClassName: "theme-head shootout-head",
    };
  });
}

function shootoutControlsHeaderCell(attrs = {}, options = {}) {
  const cell = document.createElement("th");
  cell.className = "od-shootout-controls-head";
  if (attrs.rowSpan) cell.rowSpan = attrs.rowSpan;

  const panel = document.createElement("div");
  panel.className = "od-shootout-controls-panel";

  const includeAddRound = options.includeAddRound ?? true;
  if (includeAddRound) {
    const addRound = document.createElement("button");
    addRound.type = "button";
    addRound.className = "btn od-add-shootout-round";
    addRound.textContent = "Добавить раунд перестрелки";
    addRound.disabled = !allTeamsNumbered() || state.teams.length < 2;
    addRound.title = addRound.disabled ? "Сначала заполните номера команд" : "Добавить раунд перестрелки";
    addRound.addEventListener("click", openShootoutRoundDialog);
    panel.appendChild(addRound);
  }

  const roundIndexes = Number.isInteger(options.roundIndex)
    ? [options.roundIndex]
    : state.shootoutRounds.map((_, index) => index);
  roundIndexes.forEach((roundIndex) => {
    const round = state.shootoutRounds[roundIndex];
    if (!round) return;
    const group = document.createElement("span");
    group.className = "od-shootout-round-controls";

    const label = document.createElement("span");
    label.className = "od-shootout-round-label";
    label.textContent = `П${roundIndex + 1}`;
    group.appendChild(label);

    const addQuestion = document.createElement("button");
    addQuestion.type = "button";
    addQuestion.className = "btn";
    addQuestion.textContent = "Добавить вопрос";
    addQuestion.addEventListener("click", () => addShootoutQuestion(roundIndex));
    group.appendChild(addQuestion);

    const removeQuestion = document.createElement("button");
    removeQuestion.type = "button";
    removeQuestion.className = "btn danger";
    removeQuestion.textContent = "Удалить вопрос";
    removeQuestion.addEventListener("click", () => removeShootoutQuestion(roundIndex));
    group.appendChild(removeQuestion);

    panel.appendChild(group);
  });

  cell.appendChild(panel);
  return cell;
}

function shootoutControlsBodyCell() {
  return td("", "od-shootout-controls-cell");
}

function shootoutTeamCell(number) {
  const cell = document.createElement("td");
  cell.className = "od-shootout-team-cell";
  const teamIndex = teamIndexByNumber(number);
  const name = document.createElement("span");
  name.className = "readonly-team-name";
  name.textContent = teamIndex >= 0 ? teamLabel(teamIndex) : String(number || "");
  cell.appendChild(name);
  return cell;
}

function shootoutExcludedCell(tourEnd) {
  return td("", "od-shootout-excluded" + (tourEnd ? " entry-tour-end" : ""));
}

function shootoutLockCell(roundIndex, questionIndex, className) {
  const cell = document.createElement("th");
  cell.className = className;
  const label = document.createElement("label");
  label.className = "entry-lock-label";
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.className = "entry-lock-checkbox";
  cb.dataset.entryKind = "shootout";
  cb.dataset.round = String(roundIndex);
  cb.dataset.question = String(questionIndex);
  cb.checked = shootoutQuestionCompleted(roundIndex, questionIndex);
  makeViewerCheckboxReadonly(cb);
  label.appendChild(cb);
  cell.appendChild(label);
  return cell;
}

function shootoutEntryCell(roundIndex, questionIndex, rowIndex, validationCounts, tourEnd = false) {
  const cell = document.createElement("td");
  cell.className = "entry-cell od-shootout-check-cell" + (tourEnd ? " entry-tour-end" : "");
  cell.dataset.entryKind = "shootout";
  cell.dataset.round = String(roundIndex);
  cell.dataset.question = String(questionIndex);
  cell.dataset.row = String(rowIndex);
  cell.setAttribute("role", "gridcell");
  const round = state.shootoutRounds[roundIndex];
  const value = shootoutEntryValue(roundIndex, questionIndex, rowIndex);
  const label = document.createElement("label");
  label.className = "shootout-entry-check-label";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "shootout-entry-checkbox";
  checkbox.dataset.entryKind = "shootout";
  checkbox.dataset.round = String(roundIndex);
  checkbox.dataset.question = String(questionIndex);
  checkbox.dataset.row = String(rowIndex);
  checkbox.checked = Boolean(value && value === round?.teams?.[rowIndex]);
  makeViewerCheckboxReadonly(checkbox);
  label.appendChild(checkbox);
  cell.appendChild(label);
  markShootoutEntryCellValidity(cell, validationCounts);
  return cell;
}

function buildInputGate() {
  const wrap = document.createElement("div");
  wrap.className = "od-input-gate";
  const msg = document.createElement("p");
  msg.appendChild(document.createTextNode("Чтобы ввод работал, надо заполнить "));
  if (viewer || !route.festID) {
    msg.appendChild(document.createTextNode("номера команд"));
  } else {
    const link = document.createElement("a");
    link.href = numbersPageURL();
    link.textContent = "номера команд";
    msg.appendChild(link);
  }
  msg.appendChild(document.createTextNode("."));
  wrap.appendChild(msg);
  return wrap;
}

function lockCell(qIndex, className) {
  const cell = document.createElement("th");
  cell.className = className;
  const label = document.createElement("label");
  label.className = "entry-lock-label";
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.className = "entry-lock-checkbox";
  cb.dataset.q = String(qIndex);
  cb.checked = Boolean(state.completed[qIndex]);
  makeViewerCheckboxReadonly(cb);
  label.appendChild(cb);
  cell.appendChild(label);
  return cell;
}

function makeViewerCheckboxReadonly(checkbox) {
  if (!viewer) return;
  checkbox.setAttribute("aria-disabled", "true");
  checkbox.addEventListener("click", preventViewerCheckboxToggle);
  checkbox.addEventListener("keydown", preventViewerCheckboxKeydown);
}

function preventViewerCheckboxToggle(event) {
  event.preventDefault();
  event.stopPropagation();
}

function preventViewerCheckboxKeydown(event) {
  if (event.key !== " " && event.key !== "Enter") return;
  event.preventDefault();
  event.stopPropagation();
}

function entryCell(qIndex, rowIndex, tourEnd, validationCounts) {
  const td = document.createElement("td");
  td.className = "entry-cell" + (tourEnd ? " entry-tour-end" : "");
  td.dataset.q = String(qIndex);
  td.dataset.row = String(rowIndex);
  if (!viewer) td.tabIndex = 0;
  td.setAttribute("role", "gridcell");
  applyEntryCellDisplay(td, qIndex, rowIndex);
  markEntryCellValidity(td, qIndex, validationCounts);
  return td;
}

function entryCellShowsCoffin(qIndex, rowIndex) {
  return rowIndex === 0 && Boolean(state.completed[qIndex]) && countValidEntries(qIndex) === 0;
}

function applyEntryCellDisplay(td, qIndex, rowIndex) {
  if (entryCellShowsCoffin(qIndex, rowIndex)) {
    td.textContent = "⚰️";
    td.classList.add("entry-coffin");
    return;
  }
  td.classList.remove("entry-coffin");
  const value = state.entries[qIndex]?.[rowIndex] || 0;
  td.textContent = value ? String(value) : "";
}

function refreshEntryColumnCoffin(qIndex) {
  if (!Number.isInteger(qIndex)) return;
  if (activeEntryEditor) {
    const editorCell = activeEntryEditor.cell;
    if (Number(editorCell?.dataset.q) === qIndex && Number(editorCell?.dataset.row) === 0) return;
  }
  const cell = entryCellNode(qIndex, 0);
  if (cell) applyEntryCellDisplay(cell, qIndex, 0);
}

function buildInputValidationCounts() {
  const counts = [];
  for (let q = 0; q < totalQuestions; q++) counts.push(inputValidationCounts(q));
  return counts;
}

function inputValidationCounts(qIndex) {
  const counts = new Map();
  const list = state.entries[qIndex] || [];
  for (const value of list) {
    if (!value) continue;
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  return counts;
}

function markEntryCellValidity(cell, qIndex, counts = inputValidationCounts(qIndex)) {
  if (isShootoutEntryCell(cell)) {
    markShootoutEntryCellValidity(cell);
    return;
  }
  const rowIndex = Number(cell.dataset.row);
  const value = state.entries[qIndex]?.[rowIndex] || 0;
  const raw = value ? String(value) : "";
  if (!raw) {
    cell.classList.remove("entry-input-bad", "entry-input-dup");
    syncActiveEditorValidity(cell);
    return;
  }
  const n = Number(raw);
  const known = teamIndexByNumber(n) >= 0;
  const dup = (counts.get(n) || 0) > 1;
  cell.classList.toggle("entry-input-bad", !known);
  cell.classList.toggle("entry-input-dup", known && dup);
  syncActiveEditorValidity(cell);
}

function buildShootoutInputValidationCounts(roundIndex) {
  const round = state.shootoutRounds[roundIndex];
  if (!round) return [];
  return round.entries.map((_, questionIndex) => shootoutInputValidationCounts(roundIndex, questionIndex));
}

function shootoutInputValidationCounts(roundIndex, questionIndex) {
  const counts = new Map();
  const round = state.shootoutRounds[roundIndex];
  const list = round?.entries?.[questionIndex] || [];
  for (const value of list) {
    if (!value) continue;
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  return counts;
}

function markShootoutEntryCellValidity(cell, counts = null) {
  const roundIndex = Number(cell.dataset.round);
  const questionIndex = Number(cell.dataset.question);
  const rowIndex = Number(cell.dataset.row);
  const round = state.shootoutRounds[roundIndex];
  const value = round?.entries?.[questionIndex]?.[rowIndex] || 0;
  const raw = value ? String(value) : "";
  if (!raw) {
    cell.classList.remove("entry-input-bad", "entry-input-dup");
    syncActiveEditorValidity(cell);
    syncShootoutCheckValidity(cell);
    return;
  }
  const n = Number(raw);
  const currentCounts = counts || shootoutInputValidationCounts(roundIndex, questionIndex);
  const known = Boolean(round?.teams?.includes(n));
  const dup = (currentCounts.get(n) || 0) > 1;
  cell.classList.toggle("entry-input-bad", !known);
  cell.classList.toggle("entry-input-dup", known && dup);
  syncActiveEditorValidity(cell);
  syncShootoutCheckValidity(cell);
}

function syncActiveEditorValidity(cell) {
  if (!activeEntryEditor || activeEntryEditor.cell !== cell) return;
  activeEntryEditor.input.classList.toggle("entry-input-bad", cell.classList.contains("entry-input-bad"));
  activeEntryEditor.input.classList.toggle("entry-input-dup", cell.classList.contains("entry-input-dup"));
}

function syncShootoutCheckValidity(cell) {
  const checkbox = cell.querySelector(".shootout-entry-checkbox");
  if (!checkbox) return;
  checkbox.classList.toggle("entry-input-bad", cell.classList.contains("entry-input-bad"));
  checkbox.classList.toggle("entry-input-dup", cell.classList.contains("entry-input-dup"));
}

function updateInputValidity(qIndex = null) {
  const selector = qIndex === null ? ".entry-cell" : `.entry-cell[data-q="${qIndex}"]`;
  const cells = odRoot.querySelectorAll(selector);
  const counts = qIndex === null ? buildInputValidationCounts() : inputValidationCounts(qIndex);
  for (const cell of cells) {
    if (isShootoutEntryCell(cell)) {
      markShootoutEntryCellValidity(cell);
    } else {
      const qi = Number(cell.dataset.q);
      markEntryCellValidity(cell, qi, qIndex === null ? counts[qi] : counts);
    }
  }
}

function updateShootoutInputValidity(roundIndex, questionIndex) {
  const selector = `.entry-cell[data-entry-kind="shootout"][data-round="${gameTable.cssEscape(roundIndex)}"][data-question="${gameTable.cssEscape(questionIndex)}"]`;
  const counts = shootoutInputValidationCounts(roundIndex, questionIndex);
  for (const cell of odRoot.querySelectorAll(selector)) {
    markShootoutEntryCellValidity(cell, counts);
  }
}

function isShootoutEntryCell(cell) {
  return cell?.classList?.contains("od-shootout-check-cell");
}

function shootoutEntryValue(roundIndex, questionIndex, rowIndex) {
  return state.shootoutRounds[roundIndex]?.entries?.[questionIndex]?.[rowIndex] || 0;
}

function setShootoutEntryValue(roundIndex, questionIndex, rowIndex, value) {
  const round = state.shootoutRounds[roundIndex];
  if (!round?.entries?.[questionIndex]) return false;
  round.entries[questionIndex][rowIndex] = value;
  syncShootoutAnswersFromEntries(round, questionIndex);
  return true;
}

function shootoutQuestionCompleted(roundIndex, questionIndex) {
  return Boolean(state.shootoutRounds[roundIndex]?.completed?.[questionIndex]);
}

function handleEntryClick(event) {
  if (event.target instanceof HTMLInputElement && event.target.classList.contains("entry-input")) return;
  if (event.target instanceof HTMLInputElement && event.target.classList.contains("shootout-entry-checkbox")) return;
  const cell = event.target.closest?.(".entry-cell");
  if (!cell || viewer) return;
  if (entrySuppressClickSelection) {
    entrySuppressClickSelection = false;
    return;
  }
  if (isShootoutEntryCell(cell)) {
    cell.querySelector(".shootout-entry-checkbox")?.focus();
    return;
  }
  const pos = entryCellPosition(cell);
  if (pos) setEntrySelection(pos.q, pos.row, pos.q, pos.row, {preventScroll: true});
}

function handleEntryInput(event) {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || !input.classList.contains("entry-input")) return;
  const parsed = parseEntryInput(input);
  input.value = parsed.display;
  if (input.dataset.entryKind === "shootout") {
    const roundIndex = Number(input.dataset.round);
    const questionIndex = Number(input.dataset.question);
    const rowIndex = Number(input.dataset.row);
    if (!Number.isInteger(roundIndex) || !Number.isInteger(questionIndex) || !Number.isInteger(rowIndex)) return;
    if (parsed.pending) {
      updateEntrySuggest(input);
      return;
    }
    if (!setShootoutEntryValue(roundIndex, questionIndex, rowIndex, parsed.value)) return;
    closeEntrySuggest();
    invalidateTabCache("detailed", "results");
    updateShootoutInputValidity(roundIndex, questionIndex);
    saveState(["shootoutRounds"], state.shootoutRounds);
    return;
  }
  const qIndex = Number(input.dataset.q);
  const rowIndex = Number(input.dataset.row);
  if (!Number.isInteger(qIndex) || !Number.isInteger(rowIndex)) return;
  if (parsed.pending) {
    updateEntrySuggest(input);
    return;
  }
  state.entries[qIndex][rowIndex] = parsed.value;
  closeEntrySuggest();
  invalidateScoreCaches();
  updateInputValidity(qIndex);
  refreshEntryColumnCoffin(qIndex);
  saveState(["entries", qIndex, rowIndex], parsed.value);
}

function handleEntryKeydown(event) {
  const input = event.target;
  const cell = event.target.closest?.(".entry-cell");
  if (!(input instanceof HTMLInputElement) && cell) {
    handleEntryCellKeydown(event, cell);
    return;
  }
  if (!(input instanceof HTMLInputElement) || !input.classList.contains("entry-input")) return;
  if (handleEntrySuggestKeydown(event, input)) return;
  if (input.dataset.entryKind === "shootout") {
    const roundIndex = Number(input.dataset.round);
    const questionIndex = Number(input.dataset.question);
    const rowIndex = Number(input.dataset.row);
    if (event.key === "Enter" || event.key === "ArrowDown") {
      event.preventDefault();
      focusShootoutInput(roundIndex, questionIndex, rowIndex + 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      focusShootoutInput(roundIndex, questionIndex, rowIndex - 1);
    } else if (event.key === "ArrowLeft" && input.selectionStart === 0 && input.selectionEnd === 0) {
      event.preventDefault();
      focusShootoutInput(roundIndex, questionIndex - 1, rowIndex);
    } else if (event.key === "ArrowRight" && input.selectionStart === input.value.length && input.selectionEnd === input.value.length) {
      event.preventDefault();
      focusShootoutInput(roundIndex, questionIndex + 1, rowIndex);
    }
    return;
  }
  const qIndex = Number(input.dataset.q);
  const rowIndex = Number(input.dataset.row);
  if (event.key === "Enter" || event.key === "ArrowDown") {
    event.preventDefault();
    focusInput(qIndex, rowIndex + 1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    focusInput(qIndex, rowIndex - 1);
  } else if (event.key === "ArrowLeft" && input.selectionStart === 0 && input.selectionEnd === 0) {
    event.preventDefault();
    focusInput(qIndex - 1, rowIndex);
  } else if (event.key === "ArrowRight" && input.selectionStart === input.value.length && input.selectionEnd === input.value.length) {
    event.preventDefault();
    focusInput(qIndex + 1, rowIndex);
  }
}

function handleEntryDocumentKeydown(event) {
  if (viewer || event.defaultPrevented || activeTab !== "input" || !entrySelection) return;
  if (event.metaKey || event.ctrlKey || event.altKey) return;
  if (event.key !== "Backspace" && event.key !== "Delete" && event.key !== " ") return;
  const target = event.target;
  const editable = target instanceof HTMLInputElement
    || target instanceof HTMLTextAreaElement
    || target instanceof HTMLSelectElement
    || Boolean(target?.isContentEditable);
  if (editable) return;
  if (target !== document.body && target !== document.documentElement && !odRoot.contains(target)) return;
  event.preventDefault();
  clearSelectedEntryCells();
}

document.addEventListener("keydown", handleEntryDocumentKeydown);
document.addEventListener("keydown", handleUndoKeydown);

function handleUndoKeydown(event) {
  if (viewer || event.defaultPrevented) return;
  if (!event.metaKey && !event.ctrlKey) return;
  if (event.shiftKey || event.altKey) return;
  if (event.key.toLowerCase() !== "z") return;
  const target = event.target;
  const editable = target instanceof HTMLInputElement
    || target instanceof HTMLTextAreaElement
    || target instanceof HTMLSelectElement
    || Boolean(target?.isContentEditable);
  if (editable) return;
  if (performUndo()) event.preventDefault();
}

function handleEntryFocus(event) {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.classList.contains("shootout-entry-checkbox")) {
    markActiveEntryRow(target.closest("td"));
    return;
  }
  if (target instanceof HTMLInputElement && target.classList.contains("entry-input")) {
    markActiveEntryRow(target.closest("td"));
    target.select();
    return;
  }
  const cell = target.closest?.(".entry-cell");
  if (cell && !viewer) {
    markActiveEntryRow(cell);
    const pos = entryCellPosition(cell);
    if (pos && !entrySelection) setEntrySelection(pos.q, pos.row, pos.q, pos.row, {focus: false});
  }
}

function handleEntryFocusOut(event) {
  if (!activeEntryEditor || event.target !== activeEntryEditor.input) return;
  if (entrySuggest?.list?.contains(event.relatedTarget)) return;
  if (activeEntryEditor.cell.contains(event.relatedTarget)) return;
  closeEntryEditor();
}

function openEntryEditor(cell) {
  if (viewer) return;
  if (activeEntryEditor?.cell === cell) {
    activeEntryEditor.input.focus();
    activeEntryEditor.input.select();
    return;
  }
  closeEntryEditor();
  const shootout = isShootoutEntryCell(cell);
  const qIndex = Number(cell.dataset.q);
  const rowIndex = Number(cell.dataset.row);
  if (!Number.isInteger(rowIndex) || (!shootout && !Number.isInteger(qIndex))) return;
  markActiveEntryRow(cell);
  if (!shootout) setEntrySelection(qIndex, rowIndex, qIndex, rowIndex, {focus: false});
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "text";
  input.className = "entry-input";
  if (shootout) {
    input.dataset.entryKind = "shootout";
    input.dataset.round = cell.dataset.round;
    input.dataset.question = cell.dataset.question;
  } else {
    input.dataset.q = String(qIndex);
  }
  input.dataset.row = String(rowIndex);
  input.maxLength = 80;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.setAttribute("aria-autocomplete", "list");
  const value = shootout
    ? shootoutEntryValue(Number(cell.dataset.round), Number(cell.dataset.question), rowIndex)
    : state.entries[qIndex][rowIndex];
  input.value = value ? String(value) : "";
  cell.textContent = "";
  cell.classList.add("entry-editing");
  cell.appendChild(input);
  activeEntryEditor = {cell, input};
  syncActiveEditorValidity(cell);
  input.focus();
  input.select();
}

function closeEntryEditor() {
  if (!activeEntryEditor) return;
  closeEntrySuggest();
  const {cell, input} = activeEntryEditor;
  const shootout = isShootoutEntryCell(cell);
  const qIndex = Number(cell.dataset.q);
  const rowIndex = Number(cell.dataset.row);
  activeEntryEditor = null;
  input.remove();
  cell.classList.remove("entry-editing");
  if (shootout) {
    const value = shootoutEntryValue(Number(cell.dataset.round), Number(cell.dataset.question), rowIndex);
    cell.textContent = value ? String(value) : "";
    markShootoutEntryCellValidity(cell);
  } else {
    applyEntryCellDisplay(cell, qIndex, rowIndex);
    markEntryCellValidity(cell, qIndex);
  }
}

function parseEntryInput(input) {
  const raw = input.value.trim();
  if (/^\d*$/.test(raw)) {
    return {display: raw, value: raw === "" ? 0 : Number(raw), pending: false};
  }
  return {display: raw, value: 0, pending: true};
}

function entrySuggestOptions(input) {
  const shootout = input.dataset.entryKind === "shootout";
  if (shootout) {
    const round = state.shootoutRounds[Number(input.dataset.round)];
    return (round?.teams || []).map(entrySuggestOptionForNumber).filter(Boolean);
  }
  return state.teams
    .map((_, teamIndex) => entrySuggestOptionForNumber(teamNumber(teamIndex)))
    .filter(Boolean);
}

function entrySuggestOptionForNumber(number) {
  const teamIndex = teamIndexByNumber(number);
  if (teamIndex < 0) return null;
  return {number, label: teamLabel(teamIndex)};
}

function updateEntrySuggest(input) {
  const query = input.value.trim().toLocaleLowerCase("ru");
  if (!query || /^\d+$/.test(query)) {
    closeEntrySuggest();
    return;
  }
  const matches = entrySuggestOptions(input)
    .filter((option) => option.label.toLocaleLowerCase("ru").includes(query))
    .sort((a, b) => teamNameCollator.compare(a.label, b.label) || a.number - b.number)
    .slice(0, 8);
  if (matches.length === 0) {
    closeEntrySuggest();
    return;
  }
  if (!entrySuggest) {
    const list = document.createElement("div");
    list.className = "entry-suggest";
    list.tabIndex = -1;
    document.body.appendChild(list);
    entrySuggest = {input, list, items: [], active: 0};
  }
  entrySuggest.input = input;
  entrySuggest.items = matches;
  entrySuggest.active = 0;
  renderEntrySuggest();
}

function renderEntrySuggest() {
  if (!entrySuggest) return;
  const {input, list, items, active} = entrySuggest;
  list.replaceChildren();
  items.forEach((option, index) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "entry-suggest-option" + (index === active ? " active" : "");
    button.dataset.index = String(index);
    button.innerHTML = "";
    const badge = document.createElement("span");
    badge.className = "team-number-badge";
    badge.textContent = String(option.number);
    const name = document.createElement("span");
    name.textContent = option.label;
    button.append(badge, name);
    button.addEventListener("mousedown", (event) => {
      event.preventDefault();
      chooseEntrySuggest(index);
    });
    list.appendChild(button);
  });
  const rect = input.getBoundingClientRect();
  list.style.left = `${Math.round(rect.left)}px`;
  list.style.top = `${Math.round(rect.bottom + 2)}px`;
  list.style.width = `${Math.max(220, Math.round(rect.width))}px`;
}

function closeEntrySuggest() {
  if (!entrySuggest) return;
  entrySuggest.list.remove();
  entrySuggest = null;
}

function handleEntrySuggestKeydown(event, input) {
  if (!entrySuggest || entrySuggest.input !== input) return false;
  if (event.key === "ArrowDown") {
    event.preventDefault();
    entrySuggest.active = Math.min(entrySuggest.active + 1, entrySuggest.items.length - 1);
    renderEntrySuggest();
    return true;
  }
  if (event.key === "ArrowUp") {
    event.preventDefault();
    entrySuggest.active = Math.max(entrySuggest.active - 1, 0);
    renderEntrySuggest();
    return true;
  }
  if (event.key === "Enter" || event.key === "Tab") {
    event.preventDefault();
    chooseEntrySuggest(entrySuggest.active);
    return true;
  }
  if (event.key === "Escape") {
    event.preventDefault();
    closeEntrySuggest();
    return true;
  }
  return false;
}

function chooseEntrySuggest(index) {
  if (!entrySuggest) return;
  const option = entrySuggest.items[index];
  const input = entrySuggest.input;
  if (!option || !input) return;
  input.value = String(option.number);
  closeEntrySuggest();
  input.dispatchEvent(new Event("input", {bubbles: true}));
  input.focus();
  input.select();
}

function handleEntryChange(event) {
  const shootoutCheckbox = event.target;
  if (shootoutCheckbox instanceof HTMLInputElement && shootoutCheckbox.classList.contains("shootout-entry-checkbox")) {
    const roundIndex = Number(shootoutCheckbox.dataset.round);
    const questionIndex = Number(shootoutCheckbox.dataset.question);
    const rowIndex = Number(shootoutCheckbox.dataset.row);
    const round = state.shootoutRounds[roundIndex];
    const value = shootoutCheckbox.checked ? round?.teams?.[rowIndex] || 0 : 0;
    if (!setShootoutEntryValue(roundIndex, questionIndex, rowIndex, value)) return;
    invalidateTabCache("detailed", "results");
    updateShootoutInputValidity(roundIndex, questionIndex);
    saveState(["shootoutRounds"], state.shootoutRounds);
    return;
  }
  const cb = event.target;
  if (!(cb instanceof HTMLInputElement) || !cb.classList.contains("entry-lock-checkbox")) return;
  if (cb.dataset.entryKind === "shootout") {
    const roundIndex = Number(cb.dataset.round);
    const questionIndex = Number(cb.dataset.question);
    const round = state.shootoutRounds[roundIndex];
    if (!round?.completed || !Number.isInteger(questionIndex)) return;
    round.completed[questionIndex] = cb.checked;
    invalidateTabCache("detailed", "results");
    saveState(["shootoutRounds"], state.shootoutRounds);
    return;
  }
  const qIndex = Number(cb.dataset.q);
  if (!Number.isInteger(qIndex)) return;
  state.completed[qIndex] = cb.checked;
  invalidateScoreCaches();
  updateHeaderProgress();
  refreshEntryColumnCoffin(qIndex);
  saveState(["completed", qIndex], cb.checked);
}

function focusInput(qIndex, rowIndex) {
  if (qIndex === totalQuestions && state.shootoutRounds.length > 0) {
    focusShootoutInput(0, 0, rowIndex);
    return;
  }
  if (qIndex < 0 || qIndex >= totalQuestions) return;
  if (rowIndex < 0 || rowIndex >= state.teams.length) return;
  const sel = `.entry-cell[data-q="${qIndex}"][data-row="${rowIndex}"]`;
  const cell = odRoot.querySelector(sel);
  if (cell) openEntryEditor(cell);
}

function focusShootoutInput(roundIndex, questionIndex, rowIndex) {
  const round = state.shootoutRounds[roundIndex];
  if (!round) return;
  if (questionIndex < 0 && roundIndex > 0) {
    const previous = state.shootoutRounds[roundIndex - 1];
    focusShootoutInput(roundIndex - 1, previous.entries.length - 1, rowIndex);
    return;
  }
  if (questionIndex >= round.entries.length && roundIndex < state.shootoutRounds.length - 1) {
    focusShootoutInput(roundIndex + 1, 0, rowIndex);
    return;
  }
  if (questionIndex < 0 || questionIndex >= round.entries.length) return;
  if (rowIndex < 0 || rowIndex >= round.teams.length) return;
  const sel = `.entry-cell[data-entry-kind="shootout"][data-round="${gameTable.cssEscape(roundIndex)}"][data-question="${gameTable.cssEscape(questionIndex)}"][data-row="${gameTable.cssEscape(rowIndex)}"]`;
  const cell = odRoot.querySelector(sel);
  const checkbox = cell?.querySelector(".shootout-entry-checkbox");
  if (checkbox && !viewer) checkbox.focus();
}

function clearActiveEntryRows() {
  if (activeEntryRows.length > 0) {
    activeEntryRows.forEach((row) => row.classList.remove("active-entry-row"));
    activeEntryRows = [];
    return;
  }
  odRoot.querySelectorAll(".active-entry-row").forEach((row) => row.classList.remove("active-entry-row"));
}

function markActiveEntryRow(cell) {
  if (!cell || viewer) return;
  clearActiveEntryRows();
  const row = cell.closest("tr");
  if (!row) return;
  row.classList.add("active-entry-row");
  activeEntryRows = [row];
}

// === Подробно ===

function buildDetailedTable() {
  const wrapper = document.createElement("div");
  wrapper.className = "od-detailed-wrap";
  wrapper.appendChild(buildDetailedScoreTable());
  return wrapper;
}

function buildDetailedScoreTable() {
  const themes = [];
  let qNum = 1;
  tourLengths.forEach((tourSize, tourIndex) => {
    const questionLabels = [];
    for (let i = 0; i < tourSize; i++) {
      questionLabels.push(qNum);
      qNum++;
    }
    themes.push({label: `Т${tourIndex + 1}`, questionLabels});
  });
  themes.push(...shootoutThemeHeaders());

  const stats = questionStats();
  const totals = state.teams.map((_, i) => sumRow(i, stats));
  const placeMap = computePlaces(totals);
  const rows = detailedTeamOrder().map((teamIndex) => {
    const team = state.teams[teamIndex];
    let qIndex = 0;
    return {
      nameCell: nameCell(teamIndex),
      totalCell: {
        content: totals[teamIndex],
        className: "sticky sticky-total number total-cell",
      },
      placeCell: {
        content: placeMap[teamIndex] || "",
        className: "sticky sticky-place number place-cell",
      },
      themes: tourLengths.map((tourSize) => {
        let tourSum = 0;
        const answers = [];
        for (let i = 0; i < tourSize; i++) {
          const answered = teamTookQuestion(teamIndex, qIndex, stats);
          if (answered) tourSum += 1;
          const classes = ["answer-cell", "theme-block", "readonly"];
          if (answered) classes.push("right");
          if (i === 0) classes.push("theme-block-top-left", "theme-block-bottom-left");
          answers.push({
            content: answered ? String(qIndex + 1) : "",
            className: classes.join(" "),
          });
          qIndex++;
        }
        return {
          answers,
          scoreCell: {
            content: tourSum,
            className: "number theme-score theme-block theme-block-score",
          },
        };
      }).concat(shootoutThemeCells(teamIndex, {editable: false})),
    };
  });

  return gameTable.buildFlatScoreTable({
    className: "match-table compact-score-table od-detailed",
    nameHeader: {
      content: detailedNameHeader(),
      className: "sticky sticky-name battle od-detailed-team-head",
    },
    themes,
    rows,
  });
}

function shootoutThemeCells(teamIndex, options = {}) {
  void options;
  const number = teamNumber(teamIndex);
  return state.shootoutRounds.map((round, roundIndex) => {
    const participantIndex = round.teams.indexOf(number);
    let score = 0;
    const answers = round.answers.map((_, questionIndex) => {
      const completed = shootoutQuestionCompleted(roundIndex, questionIndex);
      const mark = participantIndex >= 0 && completed
        ? normalizeShootoutMark(round.answers[questionIndex]?.[participantIndex])
        : "";
      if (mark === "right") score++;
      return shootoutAnswerCell(teamIndex, roundIndex, questionIndex, mark, {
        participating: participantIndex >= 0,
      });
    });
    return {
      answers,
      scoreCell: {
        content: participantIndex >= 0 ? score : "",
        className: "number theme-score theme-block theme-block-score od-shootout-score" +
          (participantIndex >= 0 ? "" : " od-shootout-excluded"),
      },
    };
  });
}

function shootoutAnswerCell(teamIndex, roundIndex, questionIndex, mark, options = {}) {
  const participating = Boolean(options.participating);
  const cell = document.createElement("td");
  const classes = ["answer-cell", "theme-block", "od-shootout-cell", "readonly"];
  if (!participating) classes.push("od-shootout-excluded");
  if (mark) classes.push(mark);
  if (questionIndex === 0) classes.push("theme-block-top-left", "theme-block-bottom-left");
  cell.className = classes.join(" ");
  cell.tabIndex = -1;
  cell.dataset.round = String(roundIndex);
  cell.dataset.question = String(questionIndex);
  cell.dataset.teamNumber = String(teamNumber(teamIndex));
  if (participating) {
    cell.title = `${teamLabel(teamIndex)}, П${roundIndex + 1}, вопрос ${questionIndex + 1}`;
  }
  return cell;
}

function detailedTeamOrder() {
  return state.teams
    .map((_, index) => index)
    .sort((a, b) => {
      const aNumber = teamNumber(a);
      const bNumber = teamNumber(b);
      if (aNumber && bNumber && aNumber !== bNumber) return aNumber - bNumber;
      if (aNumber !== bNumber) return aNumber ? -1 : 1;
      const byName = teamNameCollator.compare(teamLabel(a), teamLabel(b));
      return byName || a - b;
    });
}

function teamLabel(index) {
  const name = String(state.teams[index]?.name || "").trim();
  return name || `Команда ${index + 1}`;
}

function nameCell(teamIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name od-detailed-team-cell";
  const label = teamLabel(teamIndex);
  const num = teamNumber(teamIndex);
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout";

  const numSpan = document.createElement("span");
  numSpan.className = "od-detailed-team-number";
  numSpan.textContent = num ? String(num) : "";
  layout.appendChild(numSpan);

  const nameWrap = document.createElement("span");
  nameWrap.className = "od-detailed-team-name-wrap";
  const name = document.createElement("span");
  name.className = "readonly-team-name od-detailed-team-name";
  name.textContent = label;
  name.tabIndex = 0;
  name.setAttribute("aria-label", label);
  nameWrap.appendChild(name);
  layout.appendChild(nameWrap);
  cell.appendChild(layout);

  const fullName = document.createElement("span");
  fullName.className = "od-detailed-team-name-popover";
  fullName.textContent = label;
  cell.appendChild(fullName);

  return cell;
}

function detailedNameHeader() {
  const layout = document.createElement("span");
  layout.className = "od-detailed-team-layout od-detailed-team-head-layout";
  const numberSpace = document.createElement("span");
  numberSpace.className = "od-detailed-team-number";
  const label = document.createElement("span");
  label.className = "od-detailed-team-head-label";
  label.textContent = "Команда";
  layout.append(numberSpace, label);
  return layout;
}

function openShootoutRoundDialog() {
  if (viewer || !allTeamsNumbered() || state.teams.length < 2) return;
  const dialog = document.createElement("dialog");
  dialog.className = "od-shootout-dialog";

  const form = document.createElement("form");
  form.method = "dialog";
  form.className = "od-shootout-dialog-form";

  const title = document.createElement("h2");
  title.textContent = "Раунд перестрелки";
  form.appendChild(title);

  const list = document.createElement("div");
  list.className = "od-shootout-team-list";
  const stats = questionStats();
  const totals = state.teams.map((_, teamIndex) => sumRow(teamIndex, stats));
  const order = state.teams
    .map((_, teamIndex) => teamIndex)
    .sort((a, b) => {
      if (totals[b] !== totals[a]) return totals[b] - totals[a];
      const byName = teamNameCollator.compare(teamLabel(a), teamLabel(b));
      return byName || a - b;
    });
  for (const teamIndex of order) {
    const number = teamNumber(teamIndex);
    if (!number) continue;
    const label = document.createElement("label");
    label.className = "od-shootout-team-option";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.value = String(number);
    const name = document.createElement("span");
    name.textContent = `${teamLabel(teamIndex)} (${totals[teamIndex]})`;
    const badge = document.createElement("span");
    badge.className = "team-number-badge";
    badge.textContent = String(number);
    label.append(checkbox, badge, name);
    list.appendChild(label);
  }
  form.appendChild(list);

  const actions = document.createElement("div");
  actions.className = "od-shootout-dialog-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = "Отмена";
  cancel.addEventListener("click", () => dialog.close());
  const submit = document.createElement("button");
  submit.type = "button";
  submit.className = "btn";
  submit.textContent = "Создать";
  submit.disabled = true;
  actions.append(cancel, submit);
  form.appendChild(actions);

  const selectedNumbers = () => Array.from(list.querySelectorAll("input:checked"))
    .map((input) => Number(input.value))
    .filter((number) => Number.isInteger(number) && number > 0);
  const syncSubmit = () => {
    submit.disabled = selectedNumbers().length < 2;
  };
  list.addEventListener("change", syncSubmit);
  submit.addEventListener("click", () => {
    const numbers = selectedNumbers();
    if (numbers.length < 2) return;
    dialog.close();
    createShootoutRound(numbers);
  });

  dialog.appendChild(form);
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener("close", () => dialog.remove(), {once: true});
  document.body.appendChild(dialog);
  if (typeof dialog.showModal === "function") {
    dialog.showModal();
  } else {
    dialog.setAttribute("open", "");
  }
}

function createShootoutRound(numbers) {
  const seen = new Set();
  const teams = numbers.filter((number) => {
    if (!Number.isInteger(number) || number <= 0 || seen.has(number)) return false;
    if (teamIndexByNumber(number) < 0) return false;
    seen.add(number);
    return true;
  });
  if (teams.length < 2) return;
  const round = {
    teams,
    entries: [Array(teams.length).fill(0)],
    completed: [false],
    answers: [Array(teams.length).fill("")],
  };
  rememberTabScroll(activeTab);
  state.shootoutRounds.push(round);
  invalidateShootoutCaches();
  saveState(["shootoutRounds"], state.shootoutRounds);
  activeTab = "input";
  if (window.location.hash.replace(/^#/, "") !== "input") {
    history.replaceState(null, "", "#input");
  }
  render();
  focusShootoutInput(state.shootoutRounds.length - 1, 0, 0);
}

function addShootoutQuestion(roundIndex) {
  if (viewer) return;
  const round = state.shootoutRounds[roundIndex];
  if (!round) return;
  if (!Array.isArray(round.entries)) round.entries = [];
  if (!Array.isArray(round.completed)) round.completed = [];
  round.answers.push(Array(round.teams.length).fill(""));
  round.entries.push(Array(round.teams.length).fill(0));
  round.completed.push(false);
  rememberTabScroll(activeTab);
  invalidateShootoutCaches();
  saveState(["shootoutRounds"], state.shootoutRounds);
  render();
  focusShootoutInput(roundIndex, round.entries.length - 1, 0);
}

function removeShootoutQuestion(roundIndex) {
  if (viewer) return;
  const round = state.shootoutRounds[roundIndex];
  if (!round || round.answers.length === 0) return;
  const lastQuestion = round.answers.length <= 1;
  const message = lastQuestion ? "Удалить раунд перестрелки?" : "Удалить последний вопрос перестрелки?";
  if (!window.confirm(message)) return;
  if (lastQuestion) {
    state.shootoutRounds.splice(roundIndex, 1);
  } else {
    round.answers.pop();
    round.entries.pop();
    round.completed.pop();
  }
  rememberTabScroll(activeTab);
  invalidateShootoutCaches();
  saveState(["shootoutRounds"], state.shootoutRounds);
  render();
  if (!lastQuestion) focusShootoutInput(roundIndex, round.entries.length - 1, 0);
}

// === Итог ===

function lastEnteredQuestion() {
  for (let q = state.completed.length - 1; q >= 0; q--) {
    if (state.completed[q]) return q + 1;
  }
  return 0;
}

function buildResultsTable() {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  wrapper.appendChild(buildResultsTableInner());
  return wrapper;
}

function buildResultsTableInner() {
  const stats = questionStats();
  const totals = state.teams.map((_, i) => sumRow(i, stats));
  const shootoutRoundTotals = state.teams.map((_, teamIndex) =>
    state.shootoutRounds.map((__, roundIndex) => shootoutRoundTotalForTeam(teamIndex, roundIndex)));
  const tiebreaks = state.teams.map((_, i) => shootoutTiebreakForTeam(i));
  const ratings = state.teams.map((_, i) => ratingForTeam(i, stats));
  const tourTotals = state.teams.map((_, i) => tourSumsForTeam(i, stats));
  const tourStarts = tourStartIndexes();
  const tourStarted = tourLengths.map((_, tourIndex) => tourHasStarted(tourIndex));
  const shootoutRoundCount = state.shootoutRounds.length;

  const sortKeys = state.teams.map((_, i) => ({
    index: i,
    total: totals[i],
    tiebreak: tiebreaks[i],
  }));
  sortKeys.sort((a, b) => {
    if (b.total !== a.total) return b.total - a.total;
    const cmp = compareShootoutTiebreaks(a.tiebreak, b.tiebreak);
    if (cmp !== 0) return cmp;
    return a.index - b.index;
  });

  const placeMap = computePlaces(totals);

  const table = document.createElement("table");
  table.className = "results-table od-results-table";

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("Место", "results-place-head"));
  head.appendChild(th("Команда", "results-team-head"));
  head.appendChild(th("Σ", "results-num-head results-total-head"));
  for (let t = 0; t < tourLengths.length; t++) {
    head.appendChild(resultsTourHeader(t));
    if (resultsExpandedTours.has(t)) {
      for (let q = 0; q < tourLengths[t]; q++) {
        head.appendChild(th(tourStarts[t] + q + 1, "results-answer-head"));
      }
    }
  }
  for (let roundIndex = 0; roundIndex < shootoutRoundCount; roundIndex++) {
    head.appendChild(resultsShootoutHeader(roundIndex));
    if (resultsExpandedShootouts.has(roundIndex)) {
      const round = state.shootoutRounds[roundIndex];
      for (let q = 0; q < (round?.answers || []).length; q++) {
        head.appendChild(th(shootoutQuestionNumber(roundIndex, q), "results-answer-head results-shootout-answer-head"));
      }
    }
  }
  head.appendChild(th("R", "results-num-head"));
  thead.appendChild(head);
  table.appendChild(thead);

  const colCount = 4 + tourLengths.length + shootoutRoundCount +
    expandedResultsQuestionCount() + expandedResultsShootoutQuestionCount();
  const groups = [];
  sortKeys.forEach((row) => {
    const placeText = placeMap[row.index] || "—";
    const last = groups[groups.length - 1];
    if (last && last.placeText === placeText) last.rows.push(row);
    else groups.push({placeText, rows: [row]});
  });

  const tbody = document.createElement("tbody");
  groups.forEach((group, groupIdx) => {
    if (groupIdx > 0) {
      const gap = document.createElement("tr");
      gap.className = "results-group-gap";
      gap.appendChild(td("", "results-group-gap-cell", {colSpan: colCount}));
      tbody.appendChild(gap);
    }
    group.rows.forEach(({index, total}, rowIdx) => {
      const tr = document.createElement("tr");
      const classes = ["results-row"];
      if (rowIdx === 0) classes.push("results-group-first");
      if (rowIdx === group.rows.length - 1) classes.push("results-group-last");
      tr.className = classes.join(" ");
      const team = state.teams[index];
      tr.appendChild(td(group.placeText, "results-place"));
      const nameTd = document.createElement("td");
      nameTd.className = "results-team";
      const teamLabelText = team.name || `Команда ${index + 1}`;
      const nameWrap = document.createElement("span");
      nameWrap.className = "results-team-name-wrap";
      const nameSpan = document.createElement("span");
      nameSpan.className = "results-team-name";
      nameSpan.textContent = teamLabelText;
      nameSpan.tabIndex = 0;
      nameSpan.setAttribute("aria-label", teamLabelText);
      nameWrap.appendChild(nameSpan);
      if (team.city) {
        const citySpan = document.createElement("span");
        citySpan.className = "results-team-city";
        citySpan.textContent = team.city;
        nameWrap.appendChild(citySpan);
      }
      nameTd.appendChild(nameWrap);
      const fullName = document.createElement("span");
      fullName.className = "results-team-name-popover";
      fullName.textContent = teamLabelText;
      nameTd.appendChild(fullName);
      tr.appendChild(nameTd);
      tr.appendChild(td(total, "results-num total-cell results-total"));
      for (let t = 0; t < tourLengths.length; t++) {
        if (tourStarted[t]) tr.appendChild(td(tourTotals[index][t], "results-tour"));
        else tr.appendChild(td("·", "results-tour results-tour-pending"));
        if (resultsExpandedTours.has(t)) {
          for (let q = 0; q < tourLengths[t]; q++) {
            tr.appendChild(resultsAnswerCell(index, tourStarts[t] + q, stats, q, tourLengths[t]));
          }
        }
      }
      for (let roundIndex = 0; roundIndex < shootoutRoundCount; roundIndex++) {
        const value = shootoutRoundTotals[index][roundIndex];
        tr.appendChild(td(value == null ? "" : value, "results-num"));
        if (resultsExpandedShootouts.has(roundIndex)) {
          const round = state.shootoutRounds[roundIndex];
          for (let q = 0; q < (round?.answers || []).length; q++) {
            tr.appendChild(resultsShootoutAnswerCell(index, roundIndex, q));
          }
        }
      }
      tr.appendChild(td(ratings[index], "results-num"));
      tbody.appendChild(tr);
    });
  });
  table.appendChild(tbody);
  return table;
}

function resultsTourHeader(tourIndex) {
  const expanded = resultsExpandedTours.has(tourIndex);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "results-tour-toggle";
  button.textContent = `T${tourIndex + 1}`;
  button.setAttribute("aria-expanded", expanded ? "true" : "false");
  button.title = expanded ? "Свернуть тур" : "Показать вопросы тура";
  button.addEventListener("click", () => toggleResultsTour(tourIndex));
  return th(button, "results-tour-head results-tour-toggle-head" + (expanded ? " expanded" : ""));
}

function resultsShootoutHeader(roundIndex) {
  const expanded = resultsExpandedShootouts.has(roundIndex);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "results-tour-toggle";
  button.textContent = `П${roundIndex + 1}`;
  button.setAttribute("aria-expanded", expanded ? "true" : "false");
  button.title = expanded ? "Свернуть перестрелку" : "Показать вопросы перестрелки";
  button.addEventListener("click", () => toggleResultsShootout(roundIndex));
  return th(button, "results-num-head results-tour-toggle-head results-shootout-toggle-head" + (expanded ? " expanded" : ""));
}

function resultsAnswerCell(teamIndex, qIndex, stats, tourQuestionIndex, tourSize) {
  const answered = teamTookQuestion(teamIndex, qIndex, stats);
  const classes = ["results-answer"];
  if (tourQuestionIndex === 0) classes.push("results-answer-left");
  if (tourQuestionIndex === tourSize - 1) classes.push("results-answer-right");
  const cell = td("", classes.join(" "));
  const mark = document.createElement("span");
  mark.className = "results-answer-mark";
  mark.textContent = answered ? String(qIndex + 1) : "";
  cell.appendChild(mark);
  if (answered) cell.classList.add("right");
  return cell;
}

function resultsShootoutAnswerCell(teamIndex, roundIndex, questionIndex) {
  const round = state.shootoutRounds[roundIndex];
  const questionCount = (round?.answers || []).length;
  const number = teamNumber(teamIndex);
  const participantIndex = round?.teams?.indexOf(number) ?? -1;
  const participating = participantIndex >= 0;
  const completed = shootoutQuestionCompleted(roundIndex, questionIndex);
  const mark = participating && completed
    ? normalizeShootoutMark(round.answers[questionIndex]?.[participantIndex])
    : "";
  const classes = ["results-answer", "results-shootout-answer"];
  if (questionIndex === 0) classes.push("results-answer-left");
  if (questionIndex === questionCount - 1) classes.push("results-answer-right");
  if (!participating) classes.push("results-answer-excluded");
  const cell = td("", classes.join(" "));
  if (!participating) return cell;
  const markNode = document.createElement("span");
  markNode.className = "results-answer-mark";
  cell.appendChild(markNode);
  if (mark === "right") cell.classList.add("right");
  return cell;
}

function tourStartIndexes() {
  const starts = [];
  let qIndex = 0;
  for (const size of tourLengths) {
    starts.push(qIndex);
    qIndex += size;
  }
  return starts;
}

function tourHasStarted(tourIndex) {
  const start = tourStartIndexes()[tourIndex] || 0;
  const end = start + (tourLengths[tourIndex] || 0);
  for (let q = start; q < end; q++) {
    if (state.completed[q]) return true;
  }
  return false;
}

function expandedResultsQuestionCount() {
  let count = 0;
  for (const tourIndex of resultsExpandedTours) {
    count += tourLengths[tourIndex] || 0;
  }
  return count;
}

function expandedResultsShootoutQuestionCount() {
  let count = 0;
  for (const roundIndex of resultsExpandedShootouts) {
    count += (state.shootoutRounds[roundIndex]?.answers || []).length;
  }
  return count;
}

function shootoutQuestionNumber(roundIndex, questionIndex) {
  let number = questionIndex + 1;
  for (let i = 0; i < roundIndex; i++) {
    number += (state.shootoutRounds[i]?.answers || []).length;
  }
  return number;
}

// === scoring helpers ===

function sumRow(teamIndex, stats = questionStats()) {
  let s = 0;
  for (let q = 0; q < totalQuestions; q++) {
    if (teamTookQuestion(teamIndex, q, stats)) s++;
  }
  return s;
}

function tourSumsForTeam(teamIndex, stats = questionStats()) {
  const out = [];
  let qi = 0;
  for (const size of tourLengths) {
    let s = 0;
    for (let i = 0; i < size; i++) {
      if (teamTookQuestion(teamIndex, qi, stats)) s++;
      qi++;
    }
    out.push(s);
  }
  return out;
}

function ratingForTeam(teamIndex, stats = questionStats()) {
  const teamCount = state.teams.length;
  let r = 0;
  for (let q = 0; q < totalQuestions; q++) {
    if (!teamTookQuestion(teamIndex, q, stats)) continue;
    const took = countValidEntries(q, stats);
    r += teamCount - took;
  }
  return r;
}

function shootoutTiebreakForTeam(teamIndex) {
  // Per-round scores for lexicographic comparison; -1 marks rounds the team didn't play,
  // so an early-exit team isn't overtaken by teams who continued accumulating points.
  const result = [];
  for (let roundIndex = 0; roundIndex < state.shootoutRounds.length; roundIndex++) {
    const roundTotal = shootoutRoundTotalForTeam(teamIndex, roundIndex);
    result.push(roundTotal != null ? roundTotal : -1);
  }
  return result;
}

function compareShootoutTiebreaks(a, b) {
  const len = Math.max(a.length, b.length);
  for (let i = 0; i < len; i++) {
    const av = a[i] ?? -1;
    const bv = b[i] ?? -1;
    if (av !== bv) return bv - av;
  }
  return 0;
}

function shootoutRoundTotalForTeam(teamIndex, roundIndex) {
  const number = teamNumber(teamIndex);
  if (!number) return null;
  const round = state.shootoutRounds[roundIndex];
  if (!round) return null;
  const participantIndex = round.teams.indexOf(number);
  if (participantIndex < 0) return null;
  let total = 0;
  for (let questionIndex = 0; questionIndex < (round.answers || []).length; questionIndex++) {
    if (!shootoutQuestionCompleted(roundIndex, questionIndex)) continue;
    if (normalizeShootoutMark(round.answers[questionIndex]?.[participantIndex]) === "right") total++;
  }
  return total;
}

function teamHasShootoutRound(teamIndex) {
  const number = teamNumber(teamIndex);
  if (!number) return false;
  return (state.shootoutRounds || []).some((round) => round.teams.includes(number));
}

function anyShootoutMarked() {
  for (const round of state.shootoutRounds || []) {
    for (let questionIndex = 0; questionIndex < (round.answers || []).length; questionIndex++) {
      if (!round.completed?.[questionIndex]) continue;
      for (const mark of round.answers[questionIndex] || []) {
        if (normalizeShootoutMark(mark)) return true;
      }
    }
  }
  return false;
}

function anyQuestionCompleted(stats = questionStats()) {
  for (const stat of stats) if (stat.completed) return true;
  return false;
}

function computePlaces(totals) {
  const places = new Array(totals.length).fill("");
  if (!anyQuestionCompleted() && !anyShootoutMarked()) return places;
  const tiebreaks = state.teams.map((_, index) => shootoutTiebreakForTeam(index));
  const sorted = totals
    .map((total, index) => ({total, tiebreak: tiebreaks[index], index}))
    .sort((a, b) => {
      if (b.total !== a.total) return b.total - a.total;
      return compareShootoutTiebreaks(a.tiebreak, b.tiebreak);
    });
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (
      j + 1 < sorted.length &&
      sorted[j + 1].total === sorted[i].total &&
      compareShootoutTiebreaks(sorted[j + 1].tiebreak, sorted[i].tiebreak) === 0
    ) j++;
    const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) {
      places[sorted[k].index] = label;
    }
    i = j + 1;
  }
  return places;
}

// === persistence ===

function saveState(path, value) {
  if (Array.isArray(path)) {
    syncState().patch(path, value);
    refreshPendingMarkers();
    return;
  }
  syncState().save();
}

// refreshPendingMarkers reconciles the per-cell "pending" spinner with the sync
// controller's un-acked edits: an entry cell stays marked until the edit
// covering it is confirmed (isPending is ancestor-aware, so a whole-column or
// whole-grid patch marks the cells under it too). Driven from saveState (edit)
// and applyRemoteState (ack / any remote update, incl. after a full rebuild).
function refreshPendingMarkers() {
  if (viewer || !stateSync || !stateSync.isPending) return;
  odRoot.querySelectorAll(".entry-cell").forEach((cell) => {
    const q = Number(cell.dataset.q);
    const row = Number(cell.dataset.row);
    const pending = Number.isInteger(q) && Number.isInteger(row) &&
      stateSync.isPending(["entries", q, row]);
    cell.classList.toggle("pending", Boolean(pending));
  });
  const shootoutPending = stateSync.isPending(["shootoutRounds"]);
  odRoot.querySelectorAll(".od-shootout-cell").forEach((cell) => {
    cell.classList.toggle("pending", shootoutPending);
  });
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
    gameTitle: fest?.gameName || gameTitle || scheme?.title || "ОД",
  });
}

// scheduleStaticReload reloads the page after ~5s (jittered 4-7s) so a fleet of
// static viewers spreads its reloads across the window instead of stampeding.
function scheduleStaticReload() {
  window.setTimeout(() => window.location.reload(), 4000 + Math.floor(Math.random() * 3000));
}

function connectEvents() {
  if (staticMode) {
    scheduleStaticReload();
    return;
  }
  syncState().connect();
}

function syncState() {
  if (stateSync) return stateSync;
  recorder = gameTable.installClientRecorder({
    scope: `game-state:${scopeGameID}`,
    getState: () => state,
    showButton: !viewer || /[?&]log\b/.test(location.search),
  });
  stateSync = gameTable.createStateSync({
    readonly: viewer,
    stateURL: `${route.apiBase}/state`,
    eventsURL: `/events?fest_id=${encodeURIComponent(route.festID)}`,
    scope: `game-state:${scopeGameID}`,
    getState: () => state,
    getInitialSeq: () => initialStateSeq,
    setStatus,
    onRemoteState: applyRemoteState,
    onViewers: (count) => viewerCounter.setCount(count),
    onLockdown: scheduleStaticReload,
    recorder,
    onWriteError: (info) => recorder?.event("write-rejected", info),
  });
  return stateSync;
}

function connectPresence() {
  if (viewer || presence || !route.festID) return;
  presence = gameTable.createHostPresence({
    root: odRoot,
    eventsURL: `/host-events?fest_id=${encodeURIComponent(route.festID)}`,
    presenceURL: `/api/fest/${route.festID}/presence`,
    cursorFromElement: odPresenceCursorFromElement,
    getCursor: currentODPresenceCursor,
    findTarget: findODPresenceTarget,
  });
  presence.connect();
}

function refreshPresence() {
  presence?.refresh();
}

function currentODPresenceCursor() {
  const focused = odPresenceCursorFromElement(document.activeElement);
  if (focused) return focused;
  return null;
}

function odPresenceCursorFromElement(element) {
  const entry = element?.closest?.(".entry-input,.entry-cell,.shootout-entry-checkbox");
  if (entry && odRoot.contains(entry)) {
    if (entry.dataset.entryKind === "shootout") {
      return {
        app: "od",
        kind: "shootout-entry",
        gameID: route.gameID,
        round: Number(entry.dataset.round),
        question: Number(entry.dataset.question),
        row: Number(entry.dataset.row),
      };
    }
    return {
      app: "od",
      kind: "entry",
      gameID: route.gameID,
      q: Number(entry.dataset.q),
      row: Number(entry.dataset.row),
    };
  }
  const teamName = element?.closest?.(".venue-input");
  if (teamName && odRoot.contains(teamName)) {
    return {app: "od", kind: "team-name", gameID: route.gameID, team: Number(teamName.dataset.team)};
  }
  return null;
}

function findODPresenceTarget(cursor) {
  if (!cursor || cursor.app !== "od" || String(cursor.gameID) !== String(route.gameID)) return null;
  if (cursor.kind === "entry") {
    return odRoot.querySelector(`.entry-cell[data-q="${gameTable.cssEscape(cursor.q)}"][data-row="${gameTable.cssEscape(cursor.row)}"]`);
  }
  if (cursor.kind === "shootout-entry") {
    return odRoot.querySelector(
      `.entry-cell[data-entry-kind="shootout"][data-round="${gameTable.cssEscape(cursor.round)}"][data-question="${gameTable.cssEscape(cursor.question)}"][data-row="${gameTable.cssEscape(cursor.row)}"]`,
    );
  }
  if (cursor.kind === "team-name") {
    return odRoot.querySelector(`.venue-input[data-team="${gameTable.cssEscape(cursor.team)}"]`);
  }
  return null;
}

function applyRemoteState(nextState) {
  const active = document.activeElement;
  const editingInput = active && active.classList.contains("entry-input");
  const editingShootout = active && (
    active.classList.contains("shootout-entry-checkbox") ||
    active.classList.contains("entry-lock-checkbox") && active.dataset.entryKind === "shootout"
  );
  state = nextState;
  ensureState();
  updateHeaderProgress();
  if (editingInput || editingShootout) {
    questionStatsCache = null;
    numberToIndexCache = null;
    invalidateTabCache("detailed", "results");
    refreshPendingMarkers();
    return;
  }
  invalidateAllCaches();
  render();
  refreshPendingMarkers();
}

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
