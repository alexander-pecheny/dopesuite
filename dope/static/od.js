const odRoot = document.getElementById("odTable");
const odTabsRoot = document.getElementById("odTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");

const route = currentRoute();
const viewer = Boolean(route.viewer);
document.body.classList.toggle("viewer-readonly", viewer);
let scheme = null;
let state = null;
let tourLengths = [];
let totalQuestions = 0;

const TABS = viewer
  ? [
      {key: "results", label: "Итог"},
      {key: "detailed", label: "Подробно"},
    ]
  : [
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

function teamTookQuestion(teamIndex, qIndex) {
  if (!state.completed[qIndex]) return false;
  const entries = state.entries[qIndex];
  if (!entries) return false;
  const target = teamIndex + 1;
  const teamCount = state.teams.length;
  for (const v of entries) {
    if (v === target && v >= 1 && v <= teamCount) return true;
  }
  return false;
}

function render() {
  if (!state || !scheme) return;
  setHeading(scheme.title || "ОД");
  document.title = `${viewer ? "Зритель" : "Ведущий"} · ${scheme.title || "ОД"}`;
  if (!TABS.some((t) => t.key === activeTab)) activeTab = TABS[0].key;
  renderTabs();
  if (activeTab === "input") odRoot.replaceChildren(buildInputTable());
  else if (activeTab === "detailed") odRoot.replaceChildren(buildDetailedTable());
  else odRoot.replaceChildren(buildResultsTable());
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

function countValidEntries(qIndex) {
  const list = state.entries[qIndex] || [];
  const teamCount = state.teams.length;
  const seen = new Set();
  let count = 0;
  for (const v of list) {
    if (!Number.isInteger(v) || v < 1 || v > teamCount) continue;
    if (seen.has(v)) continue;
    seen.add(v);
    count++;
  }
  return count;
}

function questionCounts(qIndex) {
  return state.completed[qIndex] ? countValidEntries(qIndex) : 0;
}

function buildInputTable() {
  const n = state.teams.length;
  const table = document.createElement("table");
  table.className = "entry-table" + (viewer ? " entry-readonly" : "");

  const colgroup = document.createElement("colgroup");
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
        tr.appendChild(entryCell(qi, row, tourEnd));
        qi++;
      }
    });
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  return table;
}

function lockCell(qIndex, className) {
  const cell = document.createElement("th");
  cell.className = className;
  const label = document.createElement("label");
  label.className = "entry-lock-label";
  const cb = document.createElement("input");
  cb.type = "checkbox";
  cb.className = "entry-lock-checkbox";
  cb.checked = Boolean(state.completed[qIndex]);
  cb.disabled = viewer;
  cb.addEventListener("change", () => {
    state.completed[qIndex] = cb.checked;
    saveState();
  });
  label.appendChild(cb);
  cell.appendChild(label);
  return cell;
}

function entryCell(qIndex, rowIndex, tourEnd) {
  const td = document.createElement("td");
  td.className = "entry-cell" + (tourEnd ? " entry-tour-end" : "");
  const input = document.createElement("input");
  input.type = "text";
  input.inputMode = "numeric";
  input.className = "entry-input";
  input.dataset.q = String(qIndex);
  input.dataset.row = String(rowIndex);
  input.maxLength = 4;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.disabled = viewer;
  const value = state.entries[qIndex][rowIndex];
  input.value = value ? String(value) : "";
  input.addEventListener("input", () => {
    input.value = input.value.replace(/[^0-9]/g, "");
    state.entries[qIndex][rowIndex] = input.value === "" ? 0 : Number(input.value);
    updateInputValidity();
    saveState();
  });
  input.addEventListener("keydown", (event) => {
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
  });
  input.addEventListener("focus", () => {
    input.select();
  });
  td.appendChild(input);
  markInputValidity(input, qIndex);
  return td;
}

function markInputValidity(input, qIndex) {
  const raw = input.value;
  if (!raw) {
    input.classList.remove("entry-input-bad", "entry-input-dup");
    return;
  }
  const n = Number(raw);
  const inRange = Number.isInteger(n) && n >= 1 && n <= state.teams.length;
  const list = state.entries[qIndex] || [];
  let count = 0;
  for (const v of list) if (v === n) count++;
  const dup = count > 1;
  input.classList.toggle("entry-input-bad", !inRange);
  input.classList.toggle("entry-input-dup", inRange && dup);
}

function updateInputValidity() {
  const inputs = odRoot.querySelectorAll(".entry-input");
  for (const inp of inputs) {
    const qi = Number(inp.dataset.q);
    markInputValidity(inp, qi);
  }
}

function focusInput(qIndex, rowIndex) {
  if (qIndex < 0 || qIndex >= totalQuestions) return;
  if (rowIndex < 0 || rowIndex >= state.teams.length) return;
  const sel = `.entry-input[data-q="${qIndex}"][data-row="${rowIndex}"]`;
  const node = odRoot.querySelector(sel);
  if (node) {
    node.focus();
    node.select();
  }
}

// === Подробно ===

function buildDetailedTable() {
  const table = document.createElement("table");
  table.className = "match-table od-detailed";

  const thead = document.createElement("thead");
  const header = document.createElement("tr");
  header.appendChild(th("Команда", "sticky sticky-name battle"));
  header.appendChild(th("Σ", "sticky sticky-total number"));
  header.appendChild(th("М", "sticky sticky-place number"));
  header.appendChild(th("", "sticky sticky-place-gap place-gap-head"));

  let qNum = 1;
  tourLengths.forEach((tourSize, tourIndex) => {
    for (let i = 0; i < tourSize; i++) {
      header.appendChild(th(qNum, "question-head"));
      qNum++;
    }
    header.appendChild(th(`Т${tourIndex + 1}`, "theme-head"));
    header.appendChild(th("", "gap-head"));
  });
  thead.appendChild(header);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const totals = state.teams.map((_, i) => sumRow(i));
  const placeMap = computePlaces(totals);

  state.teams.forEach((team, teamIndex) => {
    const tr = document.createElement("tr");
    tr.appendChild(nameCell(team, teamIndex));

    tr.appendChild(td(totals[teamIndex], "sticky sticky-total number total-cell"));
    tr.appendChild(td(placeMap[teamIndex] || "", "sticky sticky-place number place-cell"));
    tr.appendChild(td("", "sticky sticky-place-gap place-gap"));

    let qIndex = 0;
    tourLengths.forEach((tourSize) => {
      let tourSum = 0;
      for (let i = 0; i < tourSize; i++) {
        const answered = teamTookQuestion(teamIndex, qIndex);
        if (answered) tourSum += 1;
        const cell = document.createElement("td");
        const classes = ["answer-cell", "theme-block", "readonly"];
        if (answered) classes.push("right");
        if (i === 0) classes.push("theme-block-top-left", "theme-block-bottom-left");
        cell.className = classes.join(" ");
        if (answered) cell.textContent = String(qIndex + 1);
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
  input.disabled = viewer;
  input.addEventListener("change", () => {
    state.teams[teamIndex].name = input.value.trim();
    saveState();
  });
  cell.appendChild(input);
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
  const totals = state.teams.map((_, i) => sumRow(i));
  const ratings = state.teams.map((_, i) => ratingForTeam(i));
  const tourTotals = state.teams.map((_, i) => tourSumsForTeam(i));

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

function sumRow(teamIndex) {
  let s = 0;
  for (let q = 0; q < totalQuestions; q++) {
    if (teamTookQuestion(teamIndex, q)) s++;
  }
  return s;
}

function tourSumsForTeam(teamIndex) {
  const out = [];
  let qi = 0;
  for (const size of tourLengths) {
    let s = 0;
    for (let i = 0; i < size; i++) {
      if (teamTookQuestion(teamIndex, qi)) s++;
      qi++;
    }
    out.push(s);
  }
  return out;
}

function ratingForTeam(teamIndex) {
  const teamCount = state.teams.length;
  let r = 0;
  for (let q = 0; q < totalQuestions; q++) {
    if (!teamTookQuestion(teamIndex, q)) continue;
    const took = countValidEntries(q);
    r += teamCount - took;
  }
  return r;
}

function anyQuestionCompleted() {
  for (const c of state.completed) if (c) return true;
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

let saveTimer = null;
function saveState() {
  if (viewer) return;
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
      const typing = document.activeElement && document.activeElement.classList.contains("entry-input");
      state = parsed.data;
      ensureState();
      if (typing) {
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
      apiBase: `/api/tournaments/${host[1]}/games/${host[2]}`,
    };
  }
  const pub = path.match(/^\/tournaments\/(\d+)\/game\/(\d+)/);
  if (pub) {
    return {
      viewer: true,
      tournamentID: pub[1],
      gameID: pub[2],
      apiBase: `/api/tournaments/${pub[1]}/games/${pub[2]}`,
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

loadAll()
  .then(() => {
    setStatus("saved");
    connectEvents();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
