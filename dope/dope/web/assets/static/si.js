const siRoot = document.getElementById("siTable");
const siTabsRoot = document.getElementById("siTabs");
const statusNode = document.getElementById("status");
const pageHeading = document.querySelector(".host-top h1");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = window.DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);
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
// Sticker type id whose rules match a regular KSI theme; the implicit sticker
// for plain (non-stickers) KSI games and the fallback for unknown ids.
const STICKER_NEUTRAL = "neutral";
// Antu accessories-notes SVG: tick removed, solid fills (no gradient IDs so
// multiple copies on the same page don't collide). CSS hue-rotate() on the SVG
// element shifts all shades together, preserving the note's depth and texture.
// Darker fills so the note is visible against light backgrounds; hue-rotate
// shifts all shades together. Base hue ~44° (amber-yellow).
// No <rect> behind the main face — the face path's dog-ear cutout stays
// transparent, giving the peeled-corner sticker silhouette.
const STICKER_ICON_SVG = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" aria-hidden="true"><g transform="matrix(1.36133 0 0 1.36133-1132.27-679.3)"><path fill-rule="evenodd" fill="#f5c030" d="m831.8 501.31v30.909c0 0 .292 1.723 2.045 1.723h13c11.65-2.057 18.553-8.41 20.08-19.625v-13.376c.084-1.866-1.563-1.952-1.563-1.952l-31.397.11c-2.337-.161-2.167 2.21-2.167 2.21"/><path fill-rule="evenodd" fill="#c47a10" opacity=".75" d="m846.07 534.25c6.656-1.385 10.159-5.085 12.416-9.509 5.994-1.741 7.526-6.272 8.402-11.214.128 12.622-9.624 18.834-20.819 20.723"/></g></svg>';

// Peel-corner darkness: the curl is the sticker colour multiplied this much
// darker than the main face.
const STICKER_PEEL_FACTOR = 0.78;

// Returns the sticker colour a shade darker for the peel corner. The main face
// keeps the exact picked hex, so what an organizer picks is what renders.
function darkenHex(hex, factor) {
  const ch = (i) => Math.max(0, Math.min(255,
    Math.round(parseInt(hex.slice(i, i + 2), 16) * factor)));
  const h = (n) => n.toString(16).padStart(2, "0");
  return "#" + h(ch(1)) + h(ch(3)) + h(ch(5));
}
const teamNameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});
const KSI_TABS = [
  {key: "detailed", label: "Подробно"},
  {key: "results", label: "Итог"},
  {key: "refusals", label: "Отказы"},
];

const route = gameTable.parseGameRoute();
const viewer = Boolean(route.viewer);
// The URL carries the game slug, but the server broadcasts SSE state under the
// numeric game id (`game-state:<id>`). Default to the slug and upgrade to the
// numeric id from __GAME_INIT__ so the scope matches and remote edits apply.
let scopeGameID = route.gameID;
// staticMode: served as a precomputed snapshot under DDoS lockdown. Skip the SSE
// connection and refresh by reloading on a jitter. Captured before the loader
// nulls window.__GAME_INIT__.
const staticMode = Boolean(window.__GAME_INIT__?.static);
const canEdit = Boolean(window.__GAME_INIT__?.canEdit);
document.body.classList.toggle("viewer-readonly", viewer);
if (viewer) {
  if (canEdit) gameTable.mountEditorLink(statusNode);
} else {
  gameTable.mountViewerLink(statusNode);
}
gameTable.mountGameDownloads({apiBase: route.apiBase, canEdit});
let scheme = null;
let state = null;
let fest = null;
let initialStateSeq = 0; // game-state scope seq at page render; seeds the SSE client's lastSeq
let initialStateEpoch = ""; // server epoch at page render; seeds the SSE client's epoch baseline
let participants = [];
let themesCount = 8;
// Sticker configuration for the "KSI with stickers" variant, parsed from
// scheme.stickers. Empty for plain KSI/SI games (stickersEnabled() is false).
let stickerTypes = [];
let stickerById = new Map();
let activeCell = {player: 0, theme: 0, answer: 0};
let renderedTable = null;
let renderedTab = null;
let activeTab = tabFromHash() || "detailed";
let tableIndex = null;
let scoreCache = null;
let detailedOrderCache = null;
// Client-local row order for the «Подробно» sheet: "name" (default) or "number".
// Editors pick whichever identity they read off the floor; never synced.
let detailedSort = "name";
let activeAnswerNode = null;
let activePlayerRows = [];
let stateSync = null;
let recorder = null;
let presence = null;
let cellSelection = null;
const tabScroll = new Map();

// The «Отказы» (refusals) tab is a host-only control surface; its effect — declined
// teams dropping out of the «Итог» ranking — is visible to spectators in that tab, so
// they never need the management list itself.
function visibleTabs() {
  return KSI_TABS.filter((t) => t.key !== "refusals" || !viewer);
}

function tabFromHash() {
  const key = (window.location.hash || "").replace(/^#/, "");
  return visibleTabs().some((t) => t.key === key) ? key : null;
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

const gameLoader = gameTable.createGameDataLoader({
  route,
  cachePrefix: "si",
  adopt: adoptGameSnapshot,
  revalidate: revalidateAll,
});

// adoptGameSnapshot assigns the page's scheme/state/fest from a loader snapshot
// and renders the first frame. On the "init" path the snapshot also carries the
// raw __GAME_INIT__ payload, the only source with the SSE seq/epoch baseline and
// the unnumbered-teams flag.
function adoptGameSnapshot({scheme: nextScheme, state: nextState, fest: nextFest, init}) {
  if (init) {
    if (init.gameID != null) scopeGameID = String(init.gameID);
    if (init.seq != null) initialStateSeq = Number(init.seq) || 0;
    if (init.epoch != null) initialStateEpoch = String(init.epoch);
    if (init.teamsUnnumbered && !viewer) gameTable.mountUnnumberedBanner(route.festID);
  }
  scheme = nextScheme;
  state = nextState;
  fest = nextFest || null;
  initFromScheme();
  ensureState();
  render();
}

async function revalidateAll() {
  const prevSchemeJSON = JSON.stringify(scheme);
  const prevStateJSON = JSON.stringify(state);
  const prevFestJSON = JSON.stringify(fest);
  const fresh = await gameTable.fetchGameData(route);
  const freshSchemeJSON = JSON.stringify(fresh.scheme);
  const freshStateJSON = JSON.stringify(fresh.state);
  const freshFestJSON = JSON.stringify(fresh.fest);
  scheme = fresh.scheme;
  state = fresh.state;
  fest = fresh.fest;
  gameLoader.writeSnapshot(fresh);
  if (freshSchemeJSON === prevSchemeJSON && freshStateJSON === prevStateJSON && freshFestJSON === prevFestJSON) return;
  initFromScheme();
  ensureState();
  render();
}

function initFromScheme() {
  participants = schemeParticipants();
  themesCount = Number(scheme.themes) > 0 ? Number(scheme.themes) : (isTeamMode() ? KSI_THEMES : 8);
  initStickers();
}

function initStickers() {
  stickerTypes = [];
  stickerById = new Map();
  const types = scheme?.stickers && Array.isArray(scheme.stickers.types) ? scheme.stickers.types : [];
  for (const raw of types) {
    if (!raw || typeof raw.id !== "string" || !raw.id) continue;
    const type = {
      id: raw.id,
      label: typeof raw.label === "string" && raw.label ? raw.label : raw.id,
      color: typeof raw.color === "string" ? raw.color : "",
      // Max count a team may use; null = unlimited (the neutral sticker).
      max: typeof raw.max === "number" && Number.isFinite(raw.max) ? raw.max : null,
    };
    stickerTypes.push(type);
    stickerById.set(type.id, type);
  }
}

// stickersEnabled gates the whole sticker UI/scoring path: only KSI team games
// that actually carry a sticker configuration.
function stickersEnabled() {
  return isTeamMode() && stickerTypes.length > 0;
}

function stickerValue(player, theme) {
  const id = state.stickers?.[theme]?.[player];
  return typeof id === "string" ? id : "";
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
  // Refused-to-play flags live in a dedicated top-level map keyed by team identity
  // (`n<number>`, or `s<name>` for the legacy number-less case) rather than on the
  // participant objects, which are an immutable rating roster and get fully rebuilt
  // on every roster re-import — keys here survive that.
  if (!state.declined || typeof state.declined !== "object" || Array.isArray(state.declined)) {
    state.declined = {};
  }
  ensureStickerGrid();
  invalidateScores();
  invalidateDetailedOrder();
}

// ensureStickerGrid normalises state.stickers to a themesCount × participants
// grid of sticker ids (""=unset). Only meaningful for stickers games; left
// untouched otherwise.
function ensureStickerGrid() {
  if (!stickersEnabled()) return;
  const grid = Array.isArray(state.stickers) ? state.stickers : [];
  const next = [];
  for (let t = 0; t < themesCount; t++) {
    const row = Array.isArray(grid[t]) ? grid[t] : [];
    const padded = [];
    for (let p = 0; p < state.participants.length; p++) {
      const id = row[p];
      padded.push(typeof id === "string" && stickerById.has(id) ? id : "");
    }
    next.push(padded);
  }
  state.stickers = next;
}

function render(options = {}) {
  if (!scheme || !state) return;
  const defaultTitle = gameTitleFallback();
  normalizeActiveCell();
  setHeading(scheme.title || defaultTitle);
  document.title = pageTitle();
  if (isTeamMode()) {
    rememberTabScroll(renderedTab);
    if (!visibleTabs().some((t) => t.key === activeTab)) activeTab = "detailed";
    renderTabs();
    const node = activeTab === "results"
      ? buildResultsTable()
      : activeTab === "refusals"
        ? buildRefusalsTable()
        : buildTable();
    renderedTable = activeTab === "detailed" ? node : null;
    if (activeTab !== "detailed") resetTableIndex();
    siRoot.replaceChildren(node);
    renderedTab = activeTab;
    restoreTabScroll(activeTab);
    updateResultsScrollState();
    if (activeTab === "detailed" || activeTab === "results") teamNameOverflow.schedule();
    if (activeTab === "detailed") refreshAllStickerLimits();
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
    nameCell: nameCell(participantName(playerIndex), playerIndex),
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
        themeScoreDisplay(scores, playerIndex, themeIndex),
        "number theme-score theme-block theme-block-score",
        {player: playerIndex, theme: themeIndex},
      ),
      // In a stickers game the per-theme spacer carries the team's sticker
      // dropdown for that theme; plain KSI leaves it as the usual empty gap.
      gapCell: stickersEnabled() ? stickerSelectCell(playerIndex, themeIndex) : undefined,
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
  attachKSISelection(table);
  return table;
}

function attachKSISelection(table) {
  if (cellSelection) {
    cellSelection.unbind();
    cellSelection = null;
  }
  if (viewer) return;
  cellSelection = gameTable.createCellRangeSelection({
    root: table,
    cellSelector: ".answer-cell",
    readonly: () => Boolean(state?.finished),
    coordOf: ksiCoordOf,
    cellAtCoord: ksiCellAtCoord,
    serialize: ksiSerializeMark,
    parse: parseKSIMarkText,
    cycle: ksiCycleMark,
    applyValues: applyKSIEdits,
    onActiveChange: (cell) => {
      if (!cell) return;
      const player = Number(cell.dataset.player);
      const theme = Number(cell.dataset.theme);
      const answer = Number(cell.dataset.answer);
      activeCell = {player, theme, answer};
      markActive();
    },
  });
  cellSelection.bind();
}

function ksiCoordOf(cell) {
  const player = Number(cell.dataset.player);
  const theme = Number(cell.dataset.theme);
  const answer = Number(cell.dataset.answer);
  if (!Number.isInteger(player) || !Number.isInteger(theme) || !Number.isInteger(answer)) return null;
  const order = detailedPlayerOrder();
  const row = order.indexOf(player);
  if (row < 0) return null;
  return {row, col: theme * QUESTION_VALUES.length + answer};
}

function ksiCellAtCoord(coord) {
  if (!coord) return null;
  const order = detailedPlayerOrder();
  const player = order[coord.row];
  if (player === undefined) return null;
  const answers = QUESTION_VALUES.length;
  const theme = Math.floor(coord.col / answers);
  const answer = coord.col % answers;
  return tableIndex?.get("answer", {player, theme, answer})
    || siRoot.querySelector(
      `.answer-cell[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"][data-answer="${gameTable.cssEscape(answer)}"]`,
    );
}

function ksiSerializeMark(cell) {
  if (cell.classList.contains("right")) return "+";
  if (cell.classList.contains("wrong")) return "-";
  return "";
}

// Touch tap cycle: empty → right → wrong → empty.
function ksiCycleMark(cell) {
  if (cell.classList.contains("right")) return "wrong";
  if (cell.classList.contains("wrong")) return "";
  return "right";
}

function parseKSIMarkText(text) {
  const value = String(text || "").trim().toLowerCase();
  if (value === "") return "";
  if (["+", "1", "right", "y", "yes", "✓", "v", "да"].includes(value)) return "right";
  if (["-", "−", "0", "wrong", "n", "no", "x", "✗", "нет"].includes(value)) return "wrong";
  return "";
}

function applyKSIEdits(edits) {
  if (state?.finished || viewer) return;
  for (const {cell, value} of edits) {
    const player = Number(cell.dataset.player);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    if (!Number.isInteger(player) || !Number.isInteger(theme) || !Number.isInteger(answer)) continue;
    const mark = value === "right" ? "right" : value === "wrong" ? "wrong" : "";
    const row = state.themes[theme]?.answers?.[player];
    if (!row) continue;
    const previousMark = row[answer];
    if (previousMark === mark) continue;
    getScoreCache();
    row[answer] = mark;
    recomputeTheme(player, theme);
    updateAnswerCell(player, theme, answer, mark);
    refreshChangedScores(player, theme);
    saveState(["themes", theme, "answers", player, answer], mark);
  }
}

function buildResultsTable() {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";
  wrapper.appendChild(buildResultsTableInner());
  return wrapper;
}

// The «Отказы» tab: every team with a checkbox to mark it as having refused to play.
// Mirrors the EK seed-import decline list; a checked team drops out of the «Итог»
// ranking. Reuses the seed-import table styling for a consistent look.
function buildRefusalsTable() {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper";

  const order = state.participants.map((_, index) => index).sort(compareParticipantNumbers);

  const table = document.createElement("table");
  table.className = "results-table seed-import-table ksi-refusals-table";

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  head.appendChild(gameTable.th("№", "results-place-head seed-number-head"));
  head.appendChild(gameTable.th("Команда", "results-team-head seed-team-head"));
  head.appendChild(gameTable.th("Отказалась", "seed-declined-head"));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  order.forEach((index, rowIdx) => {
    const declined = participantDeclined(index);
    const tr = document.createElement("tr");
    const classes = ["results-row"];
    if (rowIdx === 0) classes.push("results-group-first");
    if (rowIdx === order.length - 1) classes.push("results-group-last");
    if (declined) classes.push("seed-declined-row");
    tr.className = classes.join(" ");

    const number = participantNumber(index);
    tr.appendChild(gameTable.td(number > 0 ? number : "", "results-place seed-number-cell"));

    const teamCell = document.createElement("td");
    teamCell.className = "results-team seed-team-cell";
    const nameWrap = document.createElement("span");
    nameWrap.className = "results-team-name-wrap";
    const label = participantLabel(index);
    const name = document.createElement("span");
    name.className = "results-team-name";
    name.textContent = label;
    name.tabIndex = 0;
    name.setAttribute("aria-label", label);
    nameWrap.appendChild(name);
    teamCell.appendChild(nameWrap);
    const fullName = document.createElement("span");
    fullName.className = "results-team-name-popover";
    fullName.textContent = label;
    teamCell.appendChild(fullName);
    tr.appendChild(teamCell);

    const declinedCell = document.createElement("td");
    declinedCell.className = "results-num seed-declined-cell";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = declined;
    checkbox.disabled = viewer;
    checkbox.setAttribute("aria-label", `Отказалась: ${label}`);
    checkbox.addEventListener("change", () => setParticipantDeclined(index, checkbox.checked));
    declinedCell.appendChild(checkbox);
    tr.appendChild(declinedCell);

    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  wrapper.appendChild(table);
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
  // Teams that refused to play are excluded from the ranking entirely — they take no
  // place and don't shift anyone else, mirroring how declined teams are skipped in EK
  // seeding. They appear only in the «Отказы» tab.
  const rows = state.participants
    .map((_, index) => ({
      index,
      name: participantLabel(index),
      metrics: resultMetrics(index),
      placeText: "",
    }))
    .filter((row) => !participantDeclined(row.index));
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
    let stickerId = STICKER_NEUTRAL;
    if (stickersEnabled()) {
      stickerId = stickerValue(playerIndex, themeIndex);
      if (!stickerId) continue; // unscored theme excluded from the ranking
    }
    const row = state.themes[themeIndex]?.answers?.[playerIndex] || [];
    for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
      const value = QUESTION_VALUES[answerIndex];
      const mark = row[answerIndex];
      const contribution = markContribution(stickerId, mark, answerIndex);
      total += contribution;
      if (contribution > 0) plus += contribution;
      if (mark === "right") correct[value] += 1;
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
  for (const tab of visibleTabs()) {
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
  let order = state.participants.map((_, index) => index);
  if (isTeamMode()) {
    // Teams that refused to play are dropped from the «Подробно» sheet entirely.
    order = order.filter((index) => !participantDeclined(index));
    order.sort(detailedSort === "number" ? compareParticipantNumbers : compareParticipantNames);
  }
  detailedOrderCache = order;
  return detailedOrderCache;
}

function compareParticipantNames(a, b) {
  const byName = teamNameCollator.compare(participantLabel(a), participantLabel(b));
  return byName || a - b;
}

function compareParticipantNumbers(a, b) {
  const na = participantNumber(a);
  const nb = participantNumber(b);
  // Numbered teams ascending; unnumbered fall to the bottom, then by name so the
  // order is stable regardless of participant index.
  if (na > 0 && nb > 0 && na !== nb) return na - nb;
  if (na > 0 !== nb > 0) return na > 0 ? -1 : 1;
  return compareParticipantNames(a, b);
}

// setDetailedSort changes the local row order of the «Подробно» sheet and
// re-renders. Purely a view concern — no state write, no broadcast — so it is
// available to viewers too.
function setDetailedSort(key) {
  if (key !== "number" && key !== "name") return;
  if (detailedSort === key) return;
  detailedSort = key;
  invalidateDetailedOrder();
  render();
}

// Participants are stored as {number, name} objects in team mode — number is the
// universal team identity — but as bare name strings in player mode / legacy
// states. These read either shape.
function participantName(index) {
  const p = state.participants?.[index];
  if (typeof p === "string") return p;
  return p && typeof p === "object" ? String(p.name ?? "") : "";
}

function participantNumber(index) {
  const p = state.participants?.[index];
  return p && typeof p === "object" ? Number(p.number) || 0 : 0;
}

// Identity key for the refused-to-play map: the team number when known (the stable
// identity that survives roster reorders/renames), else a name fallback for legacy
// number-less states. Returns "" when there's nothing to key on.
function declinedKey(index) {
  const number = participantNumber(index);
  if (number > 0) return `n${number}`;
  const name = participantName(index).trim().toLowerCase();
  return name ? `s${name}` : "";
}

function participantDeclined(index) {
  const key = declinedKey(index);
  return key ? Boolean(state.declined?.[key]) : false;
}

function setParticipantDeclined(index, declined) {
  if (viewer) return;
  const key = declinedKey(index);
  if (!key) return;
  if (!state.declined || typeof state.declined !== "object") state.declined = {};
  state.declined[key] = declined;
  invalidateDetailedOrder();
  render();
  saveState(["declined", key], declined);
}

// participantsEqual compares two participant arrays by identity (number + name),
// tolerating both shapes, so in-place patching isn't defeated by fresh object
// references arriving on every delta.
function participantsEqual(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  const key = (p) => (typeof p === "string" ? `n:${p}` : `${p?.number || 0}:${p?.name ?? ""}`);
  for (let i = 0; i < a.length; i++) {
    if (key(a[i]) !== key(b[i])) return false;
  }
  return true;
}

function participantLabel(index) {
  const name = participantName(index).trim();
  return name || participantFallback(index);
}

function nameCell(name, playerIndex) {
  const cell = document.createElement("td");
  cell.className = "sticky sticky-name team-name";
  if (isTeamMode()) {
    cell.className = "sticky sticky-name team-name od-detailed-team-cell ksi-detailed-team-cell";
    const number = participantNumber(playerIndex);
    const baseName = name || participantFallback(playerIndex);
    const layout = document.createElement("span");
    layout.className = "od-detailed-team-layout";

    const numSpan = document.createElement("span");
    numSpan.className = "od-detailed-team-number";
    numSpan.textContent = number > 0 ? String(number) : "";
    layout.appendChild(numSpan);

    const nameWrap = document.createElement("span");
    nameWrap.className = "od-detailed-team-name-wrap";
    const label = document.createElement("span");
    label.className = "readonly-team-name od-detailed-team-name";
    label.textContent = baseName;
    label.tabIndex = 0;
    label.setAttribute("aria-label", baseName);
    nameWrap.appendChild(label);
    layout.appendChild(nameWrap);
    cell.appendChild(layout);

    const fullName = document.createElement("span");
    fullName.className = "od-detailed-team-name-popover";
    fullName.textContent = baseName;
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

// stickerSelectCell builds the per-(team, theme) sticker picker that sits in
// the theme's spacer column. Unselected: plain chevron. Selected: SVG icon in
// sticker colour. A transparent <select> overlay handles interaction.
function stickerSelectCell(playerIndex, themeIndex) {
  const cell = document.createElement("td");
  cell.className = "gap ksi-sticker-cell";
  cell.dataset.player = String(playerIndex);
  cell.dataset.theme = String(themeIndex);

  const wrap = document.createElement("div");
  wrap.className = "ksi-sticker-wrap";

  const icon = document.createElement("span");
  icon.className = "ksi-sticker-icon";
  icon.innerHTML = STICKER_ICON_SVG;
  icon.hidden = true;

  const select = document.createElement("select");
  select.className = "ksi-sticker-select";
  select.dataset.player = String(playerIndex);
  select.dataset.theme = String(themeIndex);
  select.disabled = state.finished || viewer;
  select.title = `${participantLabel(playerIndex)}, Т${themeIndex + 1}: стикер`;

  const blank = document.createElement("option");
  blank.value = "";
  blank.textContent = "—";
  select.appendChild(blank);
  for (const type of stickerTypes) {
    const opt = document.createElement("option");
    opt.value = type.id;
    opt.textContent = type.label;
    select.appendChild(opt);
  }

  const current = stickerValue(playerIndex, themeIndex);
  select.value = current;
  wrap.appendChild(icon);
  wrap.appendChild(select);
  cell.appendChild(wrap);
  applyStickerColor(select, current);
  return cell;
}

function applyStickerColor(select, stickerId) {
  const type = stickerId ? stickerById.get(stickerId) : null;
  const wrap = select.closest(".ksi-sticker-wrap");
  const icon = wrap?.querySelector(".ksi-sticker-icon");
  const svg = icon?.querySelector("svg");
  if (type && type.color) {
    if (svg) {
      // Main face = exact picked colour; peel corner = a shade darker.
      const paths = svg.querySelectorAll("path");
      if (paths[0]) paths[0].setAttribute("fill", type.color);
      if (paths[1]) {
        paths[1].setAttribute("fill", darkenHex(type.color, STICKER_PEEL_FACTOR));
        paths[1].setAttribute("opacity", "1");
      }
    }
    if (icon) icon.hidden = false;
    wrap?.classList.add("has-sticker");
  } else {
    if (icon) icon.hidden = true;
    wrap?.classList.remove("has-sticker");
  }
}

function setSticker(player, theme, rawId) {
  if (!stickersEnabled() || state.finished || viewer) return;
  const id = stickerById.has(rawId) ? rawId : "";
  if (!Array.isArray(state.stickers[theme])) return;
  if (state.stickers[theme][player] === id) return;
  state.stickers[theme][player] = id;
  recomputeTheme(player, theme);
  const scores = getScoreCache();
  gameTable.setNodeText(scoreNode("themeScore", {player, theme}), themeScoreDisplay(scores, player, theme));
  gameTable.setNodeText(scoreNode("total", {player}), scores.totals[player]);
  refreshPlaces(scores.places);
  const select = stickerSelectNode(player, theme);
  if (select) {
    select.value = id;
    applyStickerColor(select, id);
  }
  refreshStickerLimits(player);
  saveState(["stickers", theme, player], id);
}

function stickerCellNode(player, theme) {
  return siRoot.querySelector(
    `.ksi-sticker-cell[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"]`,
  );
}

function stickerSelectNode(player, theme) {
  return siRoot.querySelector(
    `.ksi-sticker-select[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"]`,
  );
}

// refreshStickerLimits flags a team's themes whose sticker type is used more
// than the configured max, so organizers can spot a miscounted sheet.
function refreshStickerLimits(player) {
  if (!stickersEnabled()) return;
  const counts = {};
  for (let theme = 0; theme < themesCount; theme++) {
    const id = stickerValue(player, theme);
    if (id) counts[id] = (counts[id] || 0) + 1;
  }
  for (let theme = 0; theme < themesCount; theme++) {
    const id = stickerValue(player, theme);
    const type = id ? stickerById.get(id) : null;
    const over = Boolean(type && type.max != null && counts[id] > type.max);
    stickerCellNode(player, theme)?.classList.toggle("ksi-sticker-over", over);
  }
}

function refreshAllStickerLimits() {
  if (!stickersEnabled()) return;
  state.participants.forEach((_, player) => refreshStickerLimits(player));
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

  const numberHead = document.createElement("button");
  numberHead.type = "button";
  numberHead.className = "od-detailed-team-number ksi-sort-head";
  numberHead.textContent = "№";
  numberHead.title = "Сортировать по номеру";
  numberHead.setAttribute("aria-label", "Сортировать по номеру");
  numberHead.classList.toggle("ksi-sort-active", detailedSort === "number");
  numberHead.addEventListener("click", () => setDetailedSort("number"));

  const label = document.createElement("button");
  label.type = "button";
  label.className = "od-detailed-team-head-label ksi-sort-head";
  label.textContent = "Команда";
  label.title = "Сортировать по названию";
  label.setAttribute("aria-label", "Сортировать по названию");
  label.classList.toggle("ksi-sort-active", detailedSort === "name");
  label.addEventListener("click", () => setDetailedSort("name"));

  layout.append(numberHead, label);
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
  if (target instanceof HTMLSelectElement && target.classList.contains("ksi-sticker-select")) {
    if (viewer || state.finished) return;
    const player = Number(target.dataset.player);
    const theme = Number(target.dataset.theme);
    if (!Number.isInteger(player) || !Number.isInteger(theme)) return;
    setSticker(player, theme, target.value);
    return;
  }
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
  // themeScored tracks whether a theme counts yet: in a stickers game a theme is
  // not scored until its (team, theme) sticker is chosen, so it stays blank and
  // is excluded from the total.
  const themeScored = state.participants.map(() => Array(themesCount).fill(true));
  const totals = state.participants.map(() => 0);
  for (let playerIndex = 0; playerIndex < state.participants.length; playerIndex++) {
    for (let themeIndex = 0; themeIndex < themesCount; themeIndex++) {
      const {value, scored} = computeThemeValue(playerIndex, themeIndex);
      themeScores[playerIndex][themeIndex] = value;
      themeScored[playerIndex][themeIndex] = scored;
      if (scored) totals[playerIndex] += value;
    }
  }
  scoreCache = {themeScores, themeScored, totals, places: gameTable.computePlaces(totals)};
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

// markContribution is the signed value of one answer mark under a sticker. It
// mirrors games.KSIStickerMarkValue on the server so client and server scoring
// can't drift.
function markContribution(stickerId, mark, answerIndex) {
  const value = QUESTION_VALUES[answerIndex];
  switch (stickerId) {
    case "x2":
      return mark === "right" ? 2 * value : mark === "wrong" ? -2 * value : 0;
    case "nowrong":
      return mark === "right" ? value : 0;
    case "emptywrong":
      return mark === "right" ? value : -value; // wrong or empty → -value
    default: // neutral, and any unknown id
      return mark === "right" ? value : mark === "wrong" ? -value : 0;
  }
}

// computeThemeValue returns one team's value for one theme and whether it is
// scored. Plain KSI scores every theme under neutral rules; a stickers game
// leaves a theme unscored (scored=false) until its sticker is selected.
function computeThemeValue(player, theme) {
  const row = state.themes[theme]?.answers?.[player] || [];
  let stickerId = STICKER_NEUTRAL;
  if (stickersEnabled()) {
    stickerId = stickerValue(player, theme);
    if (!stickerId) return {value: 0, scored: false};
  }
  let value = 0;
  for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
    value += markContribution(stickerId, row[answerIndex], answerIndex);
  }
  return {value, scored: true};
}

// recomputeTheme recomputes a single (player, theme) value in place and adjusts
// that player's total/places — used after a mark or sticker change. Sticker
// scoring isn't a simple per-cell delta (e.g. "empty = wrong"), so the whole
// theme is recomputed rather than diffed.
function recomputeTheme(player, theme) {
  const scores = getScoreCache();
  const prevContribution = scores.themeScored[player][theme] ? scores.themeScores[player][theme] : 0;
  const {value, scored} = computeThemeValue(player, theme);
  scores.themeScores[player][theme] = value;
  scores.themeScored[player][theme] = scored;
  const contribution = scored ? value : 0;
  if (contribution !== prevContribution) {
    scores.totals[player] += contribution - prevContribution;
    scores.places = gameTable.computePlaces(scores.totals);
  }
}

// themeScoreDisplay is the text shown in a theme's score cell: the value, or
// blank for an unscored (sticker not yet chosen) theme.
function themeScoreDisplay(scores, player, theme) {
  return scores.themeScored[player][theme] ? scores.themeScores[player][theme] : "";
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
  recomputeTheme(activeCell.player, activeCell.theme);
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
        themeScoreDisplay(scores, playerIndex, themeIndex),
      );
    }
  });
}

function refreshChangedScores(player, theme) {
  const scores = getScoreCache();
  gameTable.setNodeText(scoreNode("total", {player}), scores.totals[player]);
  gameTable.setNodeText(scoreNode("themeScore", {player, theme}), themeScoreDisplay(scores, player, theme));
  refreshPlaces(scores.places);
}

function refreshChangedScoreSet(changedThemes) {
  if (!changedThemes || changedThemes.size === 0) return;
  const scores = getScoreCache();
  for (const [player, themes] of changedThemes.entries()) {
    gameTable.setNodeText(scoreNode("total", {player}), scores.totals[player]);
    for (const theme of themes) {
      gameTable.setNodeText(scoreNode("themeScore", {player, theme}), themeScoreDisplay(scores, player, theme));
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
  const participantNamesChanged = previous?.participants && !participantsEqual(previous.participants, state.participants);
  const changedThemes = new Map();
  state.participants.forEach((_, playerIndex) => {
    const input = tableIndex.get("input", {player: playerIndex});
    if (input) {
      if (document.activeElement !== input) input.value = participantName(playerIndex);
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
  // A sticker change re-scores whole themes and re-selects dropdowns; rebuild
  // the sheet rather than try to patch it cell by cell.
  if (stickersEnabled() && JSON.stringify(previous.stickers || []) !== JSON.stringify(next.stickers || [])) return false;
  // A refusal toggle adds/removes a team from the «Подробно» sheet and «Итог» ranking
  // but leaves participants/themes untouched, so force a full re-render to reflect it.
  if (JSON.stringify(previous.declined || {}) !== JSON.stringify(next.declined || {})) return false;
  if (!Array.isArray(previous.participants) || !Array.isArray(next.participants)) return false;
  if (previous.participants.length !== next.participants.length) return false;
  if (isTeamMode() && !participantsEqual(previous.participants, next.participants)) return false;
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
  return `${participantLabel(playerIndex)}, Т${themeIndex + 1}, ${QUESTION_VALUES[answerIndex]}`;
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
    moveCell(0, -1, event.shiftKey);
  } else if (event.key === "ArrowRight") {
    event.preventDefault();
    moveCell(0, 1, event.shiftKey);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    moveCell(-1, 0, event.shiftKey);
  } else if (event.key === "ArrowDown") {
    event.preventDefault();
    moveCell(1, 0, event.shiftKey);
  } else if (key === "q" || key === "й" || key === "+" || key === "1" || event.code === "NumpadAdd") {
    event.preventDefault();
    setMarkForSelection("right");
  } else if (key === "w" || key === "ц" || key === "-" || key === "2" || event.code === "NumpadSubtract") {
    event.preventDefault();
    setMarkForSelection("wrong");
  } else if (key === "backspace" || key === "delete" || event.key === " ") {
    event.preventDefault();
    setMarkForSelection("");
  }
}

function setMarkForSelection(mark) {
  if (state?.finished || viewer || !isDetailedTabActive()) return;
  const cells = cellSelection?.selectedCells() || [];
  if (cells.length > 1) {
    applyKSIEdits(cells.map((cell) => ({cell, value: mark})));
    return;
  }
  setMark(mark);
}

function moveCell(dPlayer, dAnswer, extend = false) {
  const playerOrder = detailedPlayerOrder();
  const players = playerOrder.length;
  const totalCols = themesCount * QUESTION_VALUES.length;
  let column = activeCell.theme * QUESTION_VALUES.length + activeCell.answer;
  column = gameTable.clamp(column + dAnswer, 0, totalCols - 1);
  const currentPosition = Math.max(0, playerOrder.indexOf(activeCell.player));
  const nextPosition = gameTable.clamp(currentPosition + dPlayer, 0, players - 1);
  const player = playerOrder[nextPosition];
  const nextTheme = Math.floor(column / QUESTION_VALUES.length);
  const nextAnswer = column % QUESTION_VALUES.length;
  if (extend && cellSelection) {
    const anchor = cellSelection.anchor || ksiCoordForActive();
    const focusCoord = {row: nextPosition, col: column};
    cellSelection.setSelection(anchor, focusCoord, {focus: true});
    activeCell = {player, theme: nextTheme, answer: nextAnswer};
    markActive();
    return;
  }
  selectCell(player, nextTheme, nextAnswer);
  if (cellSelection) cellSelection.setSelection({row: nextPosition, col: column}, {row: nextPosition, col: column}, {focus: false});
}

function ksiCoordForActive() {
  const order = detailedPlayerOrder();
  const row = order.indexOf(activeCell.player);
  return {row: row < 0 ? 0 : row, col: activeCell.theme * QUESTION_VALUES.length + activeCell.answer};
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
    // Mark the just-edited answer cell as pending right away; it clears when the
    // server confirms the edit (refreshPendingMarkers, driven from
    // applyRemoteState on the PATCH ack / any remote update).
    if (path.length === 5 && path[0] === "themes" && path[2] === "answers") {
      answerCellNode(path[3], path[1], path[4])?.classList.add("pending");
    }
    return;
  }
  syncState().save();
}

function answerCellNode(player, theme, answer) {
  return tableIndex?.get("answer", {player, theme, answer}) ||
    siRoot.querySelector(`.answer-cell[data-player="${gameTable.cssEscape(player)}"][data-theme="${gameTable.cssEscape(theme)}"][data-answer="${gameTable.cssEscape(answer)}"]`);
}

// refreshPendingMarkers reconciles the per-cell "pending" highlight with the
// sync controller's un-acked edits: a cell stays marked until its own PATCH is
// confirmed, then clears. Called after any remote update / ack and after a full
// re-render (which rebuilds cells without the class).
function refreshPendingMarkers() {
  if (viewer || !stateSync?.isPending) return;
  siRoot.querySelectorAll(".answer-cell").forEach((cell) => {
    const player = Number(cell.dataset.player);
    const theme = Number(cell.dataset.theme);
    const answer = Number(cell.dataset.answer);
    cell.classList.toggle("pending", stateSync.isPending(["themes", theme, "answers", player, answer]));
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
    gameTitle: fest?.gameName || gameTitle || scheme?.title || gameTitleFallback(),
  });
}

function connectEvents() {
  if (staticMode) {
    gameTable.scheduleStaticReload();
    return;
  }
  syncState().connect();
}

function syncState() {
  if (stateSync) return stateSync;
  recorder = gameTable.installClientRecorder({
    scope: `game-state:${scopeGameID}`,
    getState: () => state,
    // Editors always get the download button; spectators only when they add
    // ?log to the URL, so the diagnostic UI stays off the public view.
    showButton: !viewer || /[?&]log\b/.test(location.search),
  });
  stateSync = gameTable.createStateSync({
    readonly: viewer,
    stateURL: `${route.apiBase}/state`,
    eventsURL: gameTable.gameEventsURL(route.festID, route.gameID),
    scope: `game-state:${scopeGameID}`,
    getState: () => state,
    getInitialSeq: () => initialStateSeq,
    getInitialEpoch: () => initialStateEpoch,
    setStatus,
    onRemoteState: applyRemoteState,
    onViewers: (count) => viewerCounter.setCount(count),
    onLockdown: gameTable.scheduleStaticReload,
    recorder,
    onWriteError: (info) => recorder?.event("write-rejected", info),
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
  if (canPatchState(previous, state) && patchTable(previous)) {
    refreshPendingMarkers();
    return;
  }
  render({preserveScroll: true});
  refreshPendingMarkers();
}

document.addEventListener("keydown", handleKeydown);

gameLoader.load()
  .then(() => {
    setStatus("saved");
    connectEvents();
    connectPresence();
  })
  .catch((error) => {
    setStatus("error");
    console.error(error);
  });
