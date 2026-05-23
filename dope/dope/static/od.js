const odRoot = document.getElementById("odTable");
const odTabsRoot = document.getElementById("odTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const progressNode = document.getElementById("odProgress");

const gameTable = window.DopeTable;
const teamNameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});
const route = currentRoute();
const viewer = Boolean(route.viewer);
document.body.classList.toggle("viewer-readonly", viewer);
let scheme = null;
let state = null;
let tourLengths = [];
let totalQuestions = 0;
let renderedTab = null;
let questionStatsCache = null;
let activeEntryEditor = null;
let activeEntryRows = [];
let stateSync = null;
let presence = null;
const tabCache = new Map();
const tabScroll = new Map();
const resultsExpandedTours = new Set();
const resultsExpandedShootouts = new Set();
let numberToIndexCache = null;
let entrySuggest = null;
let detailedNameOverflowFrame = 0;

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
  if (renderedTab === "detailed") scheduleDetailedNameOverflowUpdate();
  updateResultsScrollState();
});
document.querySelector(".sheet-frame")?.addEventListener("scroll", updateResultsScrollState, {passive: true});

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
  rememberTabScroll(renderedTab);
  setHeading(scheme.title || "ОД");
  document.title = `${viewer ? "Зритель" : "Ведущий"} · ${scheme.title || "ОД"}`;
  if (!TABS.some((t) => t.key === activeTab)) activeTab = TABS[0].key;
  renderTabs();
  updateHeaderProgress();
  const activePane = getTabPane(activeTab);
  for (const pane of tabCache.values()) pane.hidden = pane !== activePane;
  if (!activePane.isConnected) odRoot.appendChild(activePane);
  renderedTab = activeTab;
  restoreTabScroll(activeTab);
  updateResultsScrollState();
  if (activeTab === "detailed") scheduleDetailedNameOverflowUpdate(activePane);
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
}

function toggleResultsTour(tourIndex) {
  if (resultsExpandedTours.has(tourIndex)) resultsExpandedTours.delete(tourIndex);
  else resultsExpandedTours.add(tourIndex);
  invalidateTabCache("results");
  render();
}

function toggleResultsShootout(roundIndex) {
  if (resultsExpandedShootouts.has(roundIndex)) resultsExpandedShootouts.delete(roundIndex);
  else resultsExpandedShootouts.add(roundIndex);
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
    btn.className = "od-tab" + (activeTab === tab.key ? " active" : "");
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
  wrapper.appendChild(allTeamsNumbered() ? buildInputTable() : buildInputGate());
  const shootout = buildInputShootoutTable();
  if (shootout) wrapper.appendChild(shootout);
  return wrapper;
}

function buildInputTable() {
  const n = state.teams.length;
  const showShootoutControls = !viewer && state.shootoutRounds.length === 0;
  const table = document.createElement("table");
  table.className = "entry-table" + (viewer ? " entry-readonly" : "");
  table.addEventListener("click", handleEntryClick);
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
  cb.disabled = viewer;
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
  checkbox.disabled = viewer;
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
  cb.disabled = viewer;
  label.appendChild(cb);
  cell.appendChild(label);
  return cell;
}

function entryCell(qIndex, rowIndex, tourEnd, validationCounts) {
  const td = document.createElement("td");
  td.className = "entry-cell" + (tourEnd ? " entry-tour-end" : "");
  td.dataset.q = String(qIndex);
  td.dataset.row = String(rowIndex);
  if (!viewer) td.tabIndex = 0;
  td.setAttribute("role", "gridcell");
  const value = state.entries[qIndex][rowIndex];
  td.textContent = value ? String(value) : "";
  markEntryCellValidity(td, qIndex, validationCounts);
  return td;
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
  if (isShootoutEntryCell(cell)) {
    cell.querySelector(".shootout-entry-checkbox")?.focus();
    return;
  }
  openEntryEditor(cell);
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
  saveState(["entries", qIndex, rowIndex], parsed.value);
}

function handleEntryKeydown(event) {
  const input = event.target;
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
    openEntryEditor(cell);
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
  const value = shootout
    ? shootoutEntryValue(Number(cell.dataset.round), Number(cell.dataset.question), rowIndex)
    : state.entries[qIndex]?.[rowIndex] || 0;
  cell.textContent = value ? String(value) : "";
  if (shootout) markShootoutEntryCellValidity(cell);
  else markEntryCellValidity(cell, qIndex);
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
    rowMarkerColumn: true,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
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

function scheduleDetailedNameOverflowUpdate(root = odRoot) {
  if (detailedNameOverflowFrame) cancelAnimationFrame(detailedNameOverflowFrame);
  detailedNameOverflowFrame = requestAnimationFrame(() => {
    detailedNameOverflowFrame = 0;
    updateDetailedTeamNameOverflow(root);
  });
}

function updateDetailedTeamNameOverflow(root = odRoot) {
  root.querySelectorAll(".od-detailed-team-cell").forEach((cell) => {
    const name = cell.querySelector(".od-detailed-team-name");
    const truncated = Boolean(name && name.scrollWidth > name.clientWidth + 1);
    cell.classList.toggle("od-detailed-team-cell-truncated", truncated);
  });
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
  const tiebreaks = shootoutRoundTotals.map((roundTotals) =>
    roundTotals.reduce((sum, value) => sum + (value == null ? 0 : value), 0));
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
    if (b.tiebreak !== a.tiebreak) return b.tiebreak - a.tiebreak;
    return a.index - b.index;
  });

  const placeMap = computePlaces(totals);

  const table = document.createElement("table");
  table.className = "results-table od-results-table";

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("Место", "results-place-head"));
  head.appendChild(th("Команда", "results-team-head"));
  head.appendChild(th("Σ", "results-num-head"));
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
      const nameSpan = document.createElement("span");
      nameSpan.className = "results-team-name";
      nameSpan.textContent = team.name || `Команда ${index + 1}`;
      nameTd.appendChild(nameSpan);
      if (team.city) {
        const citySpan = document.createElement("span");
        citySpan.className = "results-team-city";
        citySpan.textContent = team.city;
        nameTd.appendChild(citySpan);
      }
      tr.appendChild(nameTd);
      tr.appendChild(td(total, "results-num total-cell"));
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

function shootoutTotalForTeam(teamIndex) {
  let total = 0;
  for (let roundIndex = 0; roundIndex < state.shootoutRounds.length; roundIndex++) {
    const roundTotal = shootoutRoundTotalForTeam(teamIndex, roundIndex);
    if (roundTotal != null) total += roundTotal;
  }
  return total;
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
  const tiebreaks = state.teams.map((_, index) => shootoutTotalForTeam(index));
  const sorted = totals
    .map((total, index) => ({total, tiebreak: tiebreaks[index], index}))
    .sort((a, b) => {
      if (b.total !== a.total) return b.total - a.total;
      return b.tiebreak - a.tiebreak;
    });
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (
      j + 1 < sorted.length &&
      sorted[j + 1].total === sorted[i].total &&
      sorted[j + 1].tiebreak === sorted[i].tiebreak
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
    return;
  }
  syncState().save();
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
    return;
  }
  invalidateAllCaches();
  render();
}

function currentRoute() {
  const path = window.location.pathname;
  const host = path.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
  if (host) {
    return {
      viewer: false,
      festID: host[1],
      gameID: host[2],
      apiBase: `/api/fest/${host[1]}/games/${host[2]}`,
    };
  }
  const pub = path.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
  if (pub) {
    return {
      viewer: true,
      festID: pub[1],
      gameID: pub[2],
      apiBase: `/api/fest/${pub[1]}/games/${pub[2]}`,
    };
  }
  return {};
}

function th(content, className) {
  return gameTable.th(content, className);
}

function td(content, className, attrs = {}) {
  return gameTable.td(content, className, attrs);
}

function option(value, label) {
  return gameTable.option(value, label);
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
