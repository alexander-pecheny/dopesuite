const odRoot = document.getElementById("odTable");
const odTabsRoot = document.getElementById("odTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

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
let numberToIndexCache = null;

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
  delete state.answers;
  delete state.finished;
}

function invalidateAllCaches() {
  activeEntryEditor = null;
  questionStatsCache = null;
  numberToIndexCache = null;
  for (const pane of tabCache.values()) pane.remove();
  tabCache.clear();
}

function invalidateScoreCaches() {
  questionStatsCache = null;
  invalidateTabCache("detailed", "results");
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
  const activePane = getTabPane(activeTab);
  for (const pane of tabCache.values()) pane.hidden = pane !== activePane;
  if (!activePane.isConnected) odRoot.appendChild(activePane);
  renderedTab = activeTab;
  restoreTabScroll(activeTab);
  refreshPresence();
}

function getTabPane(tab) {
  const cached = tabCache.get(tab);
  if (cached) return cached;
  let node;
  if (tab === "input") node = buildInputTable();
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

function buildInputTable() {
  if (!allTeamsNumbered()) return buildInputGate();
  const n = state.teams.length;
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
  const markerCol = document.createElement("col");
  markerCol.className = "col-entry-marker";
  colgroup.appendChild(markerCol);
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      const c = document.createElement("col");
      c.className = "col-entry-q" + (i === tourSize - 1 && tourIndex < tourLengths.length - 1 ? " col-entry-tour-end" : "");
      colgroup.appendChild(c);
    }
  });
  table.appendChild(colgroup);

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("", "entry-row-marker entry-row-marker-head active-row-marker"));
  let q = 1;
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      const cls = "entry-q-head" + (i === tourSize - 1 && tourIndex < tourLengths.length - 1 ? " entry-tour-end" : "");
      head.appendChild(th(q, cls));
      q++;
    }
  });
  thead.appendChild(head);

  const lockRow = document.createElement("tr");
  lockRow.appendChild(th("", "entry-row-marker entry-row-marker-head active-row-marker"));
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
    tr.appendChild(entryRowMarkerCell(row));
    let qi = 0;
    tourLengths.forEach((tourSize, tourIndex) => {
      for (let i = 0; i < tourSize; i++) {
        const tourEnd = i === tourSize - 1 && tourIndex < tourLengths.length - 1;
        tr.appendChild(entryCell(qi, row, tourEnd, validationCounts[qi]));
        qi++;
      }
    });
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  return table;
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

function entryRowMarkerCell(rowIndex) {
  const cell = document.createElement("td");
  cell.className = "entry-row-marker active-row-marker";
  cell.dataset.row = String(rowIndex);
  return cell;
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
    counts.set(value, (counts.get(value) || 0) + 1);
  }
  return counts;
}

function markEntryCellValidity(cell, qIndex, counts = inputValidationCounts(qIndex)) {
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

function syncActiveEditorValidity(cell) {
  if (!activeEntryEditor || activeEntryEditor.cell !== cell) return;
  activeEntryEditor.input.classList.toggle("entry-input-bad", cell.classList.contains("entry-input-bad"));
  activeEntryEditor.input.classList.toggle("entry-input-dup", cell.classList.contains("entry-input-dup"));
}

function updateInputValidity(qIndex = null) {
  const selector = qIndex === null ? ".entry-cell" : `.entry-cell[data-q="${qIndex}"]`;
  const cells = odRoot.querySelectorAll(selector);
  const counts = qIndex === null ? buildInputValidationCounts() : inputValidationCounts(qIndex);
  for (const cell of cells) {
    const qi = Number(cell.dataset.q);
    markEntryCellValidity(cell, qi, qIndex === null ? counts[qi] : counts);
  }
}

function handleEntryClick(event) {
  if (event.target instanceof HTMLInputElement && event.target.classList.contains("entry-input")) return;
  const cell = event.target.closest?.(".entry-cell");
  if (!cell || viewer) return;
  openEntryEditor(cell);
}

function handleEntryInput(event) {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || !input.classList.contains("entry-input")) return;
  const qIndex = Number(input.dataset.q);
  const rowIndex = Number(input.dataset.row);
  if (!Number.isInteger(qIndex) || !Number.isInteger(rowIndex)) return;
  input.value = input.value.replace(/[^0-9]/g, "");
  const value = input.value === "" ? 0 : Number(input.value);
  state.entries[qIndex][rowIndex] = value;
  invalidateScoreCaches();
  updateInputValidity(qIndex);
  saveState(["entries", qIndex, rowIndex], value);
}

function handleEntryKeydown(event) {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || !input.classList.contains("entry-input")) return;
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
  if (target instanceof HTMLInputElement && target.classList.contains("entry-input")) {
    markActiveEntryRow(Number(target.dataset.row));
    target.select();
    return;
  }
  const cell = target.closest?.(".entry-cell");
  if (cell && !viewer) {
    markActiveEntryRow(Number(cell.dataset.row));
    openEntryEditor(cell);
  }
}

function handleEntryFocusOut(event) {
  if (!activeEntryEditor || event.target !== activeEntryEditor.input) return;
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
  const qIndex = Number(cell.dataset.q);
  const rowIndex = Number(cell.dataset.row);
  if (!Number.isInteger(qIndex) || !Number.isInteger(rowIndex)) return;
  markActiveEntryRow(rowIndex);
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "numeric";
  input.className = "entry-input";
  input.dataset.q = String(qIndex);
  input.dataset.row = String(rowIndex);
  input.maxLength = 4;
  input.autocomplete = "off";
  input.spellcheck = false;
  const value = state.entries[qIndex][rowIndex];
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
  const {cell, input} = activeEntryEditor;
  const qIndex = Number(cell.dataset.q);
  const rowIndex = Number(cell.dataset.row);
  activeEntryEditor = null;
  input.remove();
  cell.classList.remove("entry-editing");
  const value = state.entries[qIndex]?.[rowIndex] || 0;
  cell.textContent = value ? String(value) : "";
  markEntryCellValidity(cell, qIndex);
}

function handleEntryChange(event) {
  const cb = event.target;
  if (!(cb instanceof HTMLInputElement) || !cb.classList.contains("entry-lock-checkbox")) return;
  const qIndex = Number(cb.dataset.q);
  if (!Number.isInteger(qIndex)) return;
  state.completed[qIndex] = cb.checked;
  invalidateScoreCaches();
  saveState(["completed", qIndex], cb.checked);
}

function focusInput(qIndex, rowIndex) {
  if (qIndex < 0 || qIndex >= totalQuestions) return;
  if (rowIndex < 0 || rowIndex >= state.teams.length) return;
  const sel = `.entry-cell[data-q="${qIndex}"][data-row="${rowIndex}"]`;
  const cell = odRoot.querySelector(sel);
  if (cell) openEntryEditor(cell);
}

function clearActiveEntryRows() {
  if (activeEntryRows.length > 0) {
    activeEntryRows.forEach((row) => row.classList.remove("active-entry-row"));
    activeEntryRows = [];
    return;
  }
  odRoot.querySelectorAll(".active-entry-row").forEach((row) => row.classList.remove("active-entry-row"));
}

function markActiveEntryRow(rowIndex) {
  if (!Number.isInteger(rowIndex) || viewer) return;
  clearActiveEntryRows();
  const row = odRoot.querySelector(`.entry-cell[data-row="${gameTable.cssEscape(rowIndex)}"]`)?.closest("tr");
  if (!row) return;
  row.classList.add("active-entry-row");
  activeEntryRows = [row];
}

// === Подробно ===

function buildDetailedTable() {
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

  const stats = questionStats();
  const totals = state.teams.map((_, i) => sumRow(i, stats));
  const placeMap = computePlaces(totals);
  const rows = detailedTeamOrder().map((teamIndex) => {
    const team = state.teams[teamIndex];
    let qIndex = 0;
    return {
      nameCell: nameCell(team, teamIndex),
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
      }),
    };
  });

  return gameTable.buildFlatScoreTable({
    className: "match-table compact-score-table od-detailed",
    rowMarkerColumn: true,
    rowMarkerHeaderClassName: "sticky row-marker row-marker-head active-row-marker",
    rowMarkerCellClassName: "sticky row-marker active-row-marker",
    nameHeader: "Команда",
    themes,
    rows,
  });
}

function detailedTeamOrder() {
  return state.teams
    .map((_, index) => index)
    .sort((a, b) => {
      const byName = teamNameCollator.compare(teamLabel(a), teamLabel(b));
      return byName || a - b;
    });
}

function teamLabel(index) {
  const name = String(state.teams[index]?.name || "").trim();
  return name || `Команда ${index + 1}`;
}

function nameCell(team, teamIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name";
  const num = teamNumber(teamIndex);
  if (num) {
    const numSpan = document.createElement("span");
    numSpan.className = "team-number-badge";
    numSpan.textContent = String(num);
    cell.appendChild(numSpan);
  }
  const name = document.createElement("span");
  name.className = "readonly-team-name";
  name.textContent = team.name || `Команда ${teamIndex + 1}`;
  cell.appendChild(name);
  return cell;
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
  const lastQ = lastEnteredQuestion();
  const meta = document.createElement("div");
  meta.className = "results-meta";
  meta.textContent = lastQ ? `Введён вопрос ${lastQ}` : "Ни одного вопроса не введено";
  wrapper.appendChild(meta);
  wrapper.appendChild(buildResultsTableInner());
  return wrapper;
}

function buildResultsTableInner() {
  const n = state.teams.length;
  const stats = questionStats();
  const totals = state.teams.map((_, i) => sumRow(i, stats));
  const ratings = state.teams.map((_, i) => ratingForTeam(i, stats));
  const tourTotals = state.teams.map((_, i) => tourSumsForTeam(i, stats));

  const sortKeys = state.teams.map((_, i) => ({
    index: i,
    total: totals[i],
    rating: ratings[i],
  }));
  sortKeys.sort((a, b) => {
    if (b.total !== a.total) return b.total - a.total;
    if (b.rating !== a.rating) return b.rating - a.rating;
    return a.index - b.index;
  });

  const placeMap = computePlaces(totals);

  const table = document.createElement("table");
  table.className = "results-table";

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(th("Место", "results-place-head"));
  head.appendChild(th("Команда", "results-team-head"));
  head.appendChild(th("Σ", "results-num-head"));
  head.appendChild(th("R", "results-num-head"));
  for (let t = 0; t < tourLengths.length; t++) {
    head.appendChild(th(`T${t + 1}`, "results-tour-head"));
  }
  thead.appendChild(head);
  table.appendChild(thead);

  const colCount = 4 + tourLengths.length;
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
      tr.appendChild(td(ratings[index], "results-num"));
      for (let t = 0; t < tourLengths.length; t++) {
        tr.appendChild(td(tourTotals[index][t], "results-tour"));
      }
      tbody.appendChild(tr);
    });
  });
  table.appendChild(tbody);
  return table;
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

function anyQuestionCompleted(stats = questionStats()) {
  for (const stat of stats) if (stat.completed) return true;
  return false;
}

function computePlaces(totals) {
  const places = new Array(totals.length).fill("");
  if (!anyQuestionCompleted()) return places;
  const sorted = totals
    .map((total, index) => ({total, index}))
    .sort((a, b) => b.total - a.total);
  let i = 0;
  while (i < sorted.length) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total) j++;
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
  return odPresenceCursorFromElement(document.activeElement);
}

function odPresenceCursorFromElement(element) {
  const entry = element?.closest?.(".entry-input,.entry-cell");
  if (entry && odRoot.contains(entry)) {
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
  if (cursor.kind === "team-name") {
    return odRoot.querySelector(`.venue-input[data-team="${gameTable.cssEscape(cursor.team)}"]`);
  }
  return null;
}

function applyRemoteState(nextState) {
  const typing = document.activeElement && document.activeElement.classList.contains("entry-input");
  state = nextState;
  ensureState();
  if (typing) {
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
