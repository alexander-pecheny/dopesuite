// The брейн page (ADR-0001): a round-robin group of head-to-head бои. The
// Протоколы tab mirrors the reference sheet's match block — question rows of
// № | player | mark | mark | player around a running score, with «П» tiebreak
// rows — and the Таблица tab is the sheet's group cross-table (score cells,
// О/+/−/± totals, М places). Matches come from the rr stage; edits go per бой
// (PATCH /matches/{code}/state) and sync over match: scopes. A self-booting
// side-effect module bundled by pages/brain.ts.

import {DopeTable} from "./match-table.js";
import type {CellCoord, CellEdit, CellRangeSelection, GameInitLike, RosterTeam, ScopedEventMessage} from "./match-table.js";
import {rankGroup} from "./brain-rank.js";
import type {RankDuel, RankTeam} from "./brain-rank.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}

const pageWindow = window as Window & PageGlobals;

interface BrainRow {
  player: string;
  mark: string; // "right" | "wrong" | ""
}

interface BrainMatchState {
  tiebreaks?: number;
  teams?: Array<{rows?: Array<BrainRow | null> | null} | null> | null;
}

interface BrainSlotTeam {
  id?: number;
  name?: string;
}

interface BrainMatchView {
  code?: string;
  title?: string;
  finished?: boolean;
  revision?: number;
  state?: BrainMatchState | null;
  teams?: BrainSlotTeam[];
  seq?: number;
}

interface SchemeSlotRef {
  seed?: {number?: number} | null;
  label?: string;
}

interface BrainSchemeMatch {
  code?: string;
  slots?: SchemeSlotRef[];
}

// BrainStageRules is the rr stage's config vocabulary (the inner object of
// stages.config_json): entrant seeds, the ranking comparator order, the
// points rule and the appended-перестрелка switch.
interface BrainStageRules {
  entrants?: SchemeSlotRef[];
  order?: string[];
  points?: {win?: number; draw?: number; loss?: number} | null;
  tiebreakQuestions?: boolean;
}

interface BrainSchemeStage {
  code?: string;
  title?: string;
  matches?: BrainSchemeMatch[];
  config?: BrainStageRules | null;
}

interface BrainScheme {
  title?: string;
  questions?: number;
  stages?: BrainSchemeStage[];
  [key: string]: unknown;
}

interface FestInfo {
  title?: string;
  gameName?: string;
  stages?: Array<{code?: string; config?: {config?: BrainStageRules} | null} | null>;
  [key: string]: unknown;
}

const brainRoot = document.getElementById("brainTable")!;
const brainTabsRoot = document.getElementById("brainTabs");
const statusNode = document.getElementById("status");
const breadcrumbsNode = document.getElementById("gameBreadcrumbs");

const gameTable = DopeTable;
const setStatus = gameTable.createStatusReporter(statusNode);
const viewerCounter = gameTable.createViewerCounter(statusNode);

const BRAIN_TABS = [
  {key: "protocol", label: "Протоколы"},
  {key: "table", label: "Таблица"},
  {key: "roster", label: "Составы"},
];

const route = gameTable.parseGameRoute();
const viewer = Boolean(route.viewer);
const init = pageWindow.__GAME_INIT__ || null;
const staticMode = Boolean(init?.static);
const canEdit = Boolean(init?.canEdit);
const scopeGameID = init?.gameID != null ? String(init.gameID) : route.gameID;
document.body.classList.toggle("viewer-readonly", viewer);
if (viewer) {
  if (canEdit) gameTable.mountEditorLink();
} else {
  gameTable.mountViewerLink();
  if (init?.teamsUnnumbered) gameTable.mountUnnumberedBanner(route.festID);
}

const scheme = (init?.scheme || {}) as BrainScheme;
const fest = (init?.fest || null) as FestInfo | null;
const matches = new Map<string, BrainMatchView>();
let festRoster: RosterTeam[] = [];
let rosterView: HTMLElement | null = null;
let activeTab = tabFromHash() || "protocol";
let resyncScheduled = false;

function groupStage(): BrainSchemeStage {
  return scheme.stages?.[0] || {};
}

// groupRules reads the stage's rules from the fest view — the same
// stages.config_json row the resolver ranks by, so client and server can't
// drift — falling back to the scheme's creation-time copy.
function groupRules(): BrainStageRules {
  const code = groupStage().code;
  const stage = fest?.stages?.find((s) => s?.code === code);
  return stage?.config?.config || groupStage().config || {};
}

function baseQuestions(): number {
  const n = Number(scheme.questions);
  return Number.isInteger(n) && n > 0 ? n : 5;
}

function tabFromHash(): string | null {
  const key = (window.location.hash || "").replace(/^#/, "");
  return BRAIN_TABS.some((t) => t.key === key) ? key : null;
}

window.addEventListener("hashchange", () => {
  const next = tabFromHash();
  if (next && next !== activeTab) {
    activeTab = next;
    render();
  }
});


function normalizeState(view: BrainMatchView): void {
  const state = (view.state && typeof view.state === "object" ? view.state : {}) as BrainMatchState;
  view.state = state;
  if (!Number.isInteger(state.tiebreaks) || (state.tiebreaks as number) < 0) state.tiebreaks = 0;
  if (!Array.isArray(state.teams)) state.teams = [];
  while (state.teams.length < 2) state.teams.push({rows: []});
  const rowCount = baseQuestions() + (state.tiebreaks as number);
  state.teams = state.teams.map((side) => {
    const rows = Array.isArray(side?.rows) ? side!.rows! : [];
    while (rows.length < rowCount) rows.push({player: "", mark: ""});
    return {
      rows: rows.map((row) => ({
        player: typeof row?.player === "string" ? row.player : "",
        mark: row?.mark === "right" || row?.mark === "wrong" ? row.mark : "",
      })),
    };
  });
}

function adoptMatchView(view: BrainMatchView | null | undefined): boolean {
  if (!view?.code) return false;
  const cached = matches.get(view.code);
  if (cached && Number(view.seq || 0) < Number(cached.seq || 0)) return false;
  normalizeState(view);
  matches.set(view.code, view);
  return true;
}

async function fetchMatches(): Promise<void> {
  const response = await fetch(`${route.apiBase}/stages/matches`);
  if (!response.ok) throw new Error(`stages/matches ${response.status}`);
  const stages = await response.json() as Array<{code?: string; matches?: BrainMatchView[]}>;
  for (const stage of stages || []) {
    for (const view of stage.matches || []) adoptMatchView(view);
  }
  render({preserveScroll: true});
}

function scheduleResync(): void {
  if (resyncScheduled) return;
  resyncScheduled = true;
  setTimeout(() => {
    resyncScheduled = false;
    fetchMatches().catch(() => setStatus("error"));
  }, 250);
}

function loadFestRoster(): void {
  gameTable.fetchFestRoster(route.festID)
    .then((teams) => {
      festRoster = teams;
      if (activeTab === "protocol") render({preserveScroll: true});
    })
    .catch(() => {});
}

function handleScopedMessage(message: ScopedEventMessage): void {
  const prefix = `match:${scopeGameID}:`;
  if (!message.scope?.startsWith(prefix)) return;
  const code = message.scope.slice(prefix.length);
  const cached = matches.get(code);
  const seq = Number(message.seq) || 0;
  if (cached && seq <= Number(cached.seq || 0)) return;
  if (Array.isArray(message.ops)) {
    if (!cached || Number(message.prevSeq) !== Number(cached.seq || 0)) {
      scheduleResync();
      return;
    }
    const next = gameTable.applyDeltaOps(cached, message.ops) as BrainMatchView;
    next.seq = seq;
    adoptMatchView(next);
  } else {
    const view = message.data as BrainMatchView | null;
    if (!view?.code) {
      scheduleResync();
      return;
    }
    view.seq = seq;
    adoptMatchView(view);
  }
  render({preserveScroll: true});
  setStatus("saved");
}

const live = gameTable.createLiveEvents({
  eventsURL: () => gameTable.gameEventsURL(route.festID!, route.gameID),
  onMessage: handleScopedMessage,
  onViewers: (count) => viewerCounter.setCount(count),
  onLockdown: gameTable.scheduleStaticReload,
  reload: fetchMatches,
  staticMode: () => staticMode,
});


async function postMatch(code: string, suffix: string, method: string, body: unknown): Promise<void> {
  setStatus("saving");
  const response = await fetch(`${route.apiBase}/matches/${encodeURIComponent(code)}/${suffix}`, {
    method,
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await response.text());
  const updated = await response.json() as BrainMatchView;
  adoptMatchView(updated);
  render({preserveScroll: true});
  setStatus("saved");
}

function sendOps(code: string, ops: Array<{path: Array<string | number>; value: unknown}>): void {
  postMatch(code, "state", "PATCH", {ops}).catch((error: unknown) => {
    console.error(error);
    setStatus("error");
    scheduleResync();
  });
}

function sendFinish(code: string, finished: boolean): void {
  postMatch(code, "finish", "POST", {finished}).catch((error: unknown) => {
    console.error(error);
    setStatus("error");
    scheduleResync();
  });
}


function matchRows(view: BrainMatchView, side: number): BrainRow[] {
  return (view.state?.teams?.[side]?.rows || []) as BrainRow[];
}

function taken(view: BrainMatchView, side: number): number {
  return matchRows(view, side).filter((row) => row.mark === "right").length;
}

function started(view: BrainMatchView): boolean {
  return [0, 1].some((side) => matchRows(view, side).some((row) => row.mark || row.player));
}

function teamName(view: BrainMatchView, side: number): string {
  return view.teams?.[side]?.name || `Команда ${side + 1}`;
}

function rowLabel(index: number): string {
  const base = baseQuestions();
  if (index < base) return String(index + 1);
  return index === base ? "П" : `П${index - base + 1}`;
}

function rosterFor(name: string): string[] {
  const wanted = name.trim().toLowerCase();
  const team = festRoster.find((t) => (t.name || "").trim().toLowerCase() === wanted);
  return (team?.players || [])
    .map((p) => (typeof p === "string" ? p : p.name || ""))
    .filter(Boolean);
}

function orderedMatches(): Array<{code: string; view: BrainMatchView}> {
  const out: Array<{code: string; view: BrainMatchView}> = [];
  for (const planned of groupStage().matches || []) {
    const code = planned.code || "";
    const view = matches.get(code);
    if (view) out.push({code, view});
  }
  return out;
}


function render(options: {preserveScroll?: boolean} = {}): void {
  const title = fest?.gameName || scheme.title || "Брейн";
  document.title = `${title} · dope`;
  if (breadcrumbsNode && route.festID) {
    gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
      festHref: viewer ? `/fest/${route.festID}` : `/host/fest/${route.festID}`,
      festTitle: fest?.title || "Фест",
      gameTitle: title,
    });
  }
  renderTabs();
  const frame = brainRoot.closest(".sheet-frame");
  const scrollTop = frame?.scrollTop || 0;
  const node = activeTab === "roster"
    ? (rosterView ||= gameTable.buildRosterView(route.festID))
    : activeTab === "table"
      ? buildCrosstable()
      : buildProtocols();
  brainRoot.replaceChildren(node);
  brainRoot.classList.toggle("fits-frame", activeTab === "roster");
  if (options.preserveScroll && frame) frame.scrollTop = scrollTop;
  restoreSelection();
}

function renderTabs(): void {
  if (!brainTabsRoot) return;
  brainTabsRoot.hidden = false;
  gameTable.renderTabBar(brainTabsRoot, BRAIN_TABS, activeTab, (key) => {
    activeTab = key;
    if (window.location.hash.replace(/^#/, "") !== key) {
      history.replaceState(null, "", `#${key}`);
    }
    render();
  });
}

// buildProtocols stacks the group's бой blocks — the sheet's протоколы tab.
function buildProtocols(): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const bouts = orderedMatches();
  if (!bouts.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = "Бои ещё не загружены.";
    wrap.appendChild(empty);
    return wrap;
  }
  for (const {code, view} of bouts) {
    wrap.appendChild(buildBout(code, view));
  }
  return wrap;
}

function buildBout(code: string, view: BrainMatchView): HTMLElement {
  const section = document.createElement("section");
  section.className = "brain-bout";
  const editable = !viewer && !view.finished;

  const table = document.createElement("table");
  table.className = "match-table brain-detailed";
  table.classList.toggle("match-finished", Boolean(view.finished));
  table.dataset.match = code;

  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  const corner = document.createElement("th");
  corner.className = "row-marker brain-bout-corner";
  corner.textContent = code.split("-").pop() || code;
  corner.title = view.title || code;
  head.appendChild(corner);
  head.appendChild(nameHead(view, 0));
  const score = document.createElement("th");
  score.className = "number brain-score-head";
  score.colSpan = 2;
  score.textContent = `${taken(view, 0)} : ${taken(view, 1)}`;
  head.appendChild(score);
  head.appendChild(nameHead(view, 1));
  head.appendChild(finishHead(code, view));
  thead.appendChild(head);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  const rowCount = matchRows(view, 0).length;
  for (let q = 0; q < rowCount; q++) {
    const tr = document.createElement("tr");
    const marker = document.createElement("td");
    marker.className = "row-marker" + (q >= baseQuestions() ? " brain-tiebreak-marker" : "");
    marker.textContent = rowLabel(q);
    tr.appendChild(marker);
    tr.appendChild(playerCell(code, view, 0, q, editable));
    tr.appendChild(markCell(code, view, 0, q, editable));
    tr.appendChild(markCell(code, view, 1, q, editable));
    tr.appendChild(playerCell(code, view, 1, q, editable));
    const gap = document.createElement("td");
    gap.className = "brain-finish-gap";
    tr.appendChild(gap);
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  table.addEventListener("change", handleTableChange);
  section.appendChild(table);
  if (editable && groupRules().tiebreakQuestions) {
    section.appendChild(tiebreakControls(code, view));
  }
  return section;
}

function nameHead(view: BrainMatchView, side: number): HTMLElement {
  const th = document.createElement("th");
  th.className = "brain-name-head";
  const name = document.createElement("span");
  name.className = "brain-name";
  name.textContent = teamName(view, side);
  th.appendChild(name);
  return th;
}

function finishHead(code: string, view: BrainMatchView): HTMLElement {
  const th = document.createElement("th");
  th.className = "brain-finish-head";
  const label = document.createElement("label");
  label.className = "finish-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.className = "finish-toggle";
  checkbox.checked = Boolean(view.finished);
  checkbox.disabled = viewer;
  checkbox.dataset.match = code;
  const text = document.createElement("span");
  text.textContent = "Закончен";
  label.append(checkbox, text);
  th.appendChild(label);
  return th;
}

function playerCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  td.className = "brain-player-cell";
  const select = document.createElement("select");
  select.className = "brain-player-select";
  select.dataset.match = code;
  select.dataset.side = String(side);
  select.dataset.q = String(q);
  select.disabled = !editable;
  const blank = document.createElement("option");
  blank.value = "";
  blank.textContent = "";
  select.appendChild(blank);
  const current = matchRows(view, side)[q]?.player || "";
  const roster = rosterFor(teamName(view, side));
  for (const player of roster) {
    const opt = document.createElement("option");
    opt.value = player;
    opt.textContent = player;
    select.appendChild(opt);
  }
  if (current && !roster.includes(current)) {
    const opt = document.createElement("option");
    opt.value = current;
    opt.textContent = current;
    select.appendChild(opt);
  }
  select.value = current;
  td.appendChild(select);
  return td;
}

function markCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  const mark = matchRows(view, side)[q]?.mark || "";
  td.className = `answer-cell ${mark}`;
  td.tabIndex = editable ? 0 : -1;
  td.dataset.match = code;
  td.dataset.side = String(side);
  td.dataset.q = String(q);
  td.title = `${teamName(view, side)}, вопрос ${rowLabel(q)}`;
  return td;
}

// tiebreakControls adds/removes «П» rows — the tiebreak questions appended
// after the base K when this бой must produce a winner.
function tiebreakControls(code: string, view: BrainMatchView): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "brain-controls";
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn-xs";
  add.textContent = "+ П";
  add.title = "Добавить вопрос перестрелки";
  add.addEventListener("click", () => {
    const state = view.state as BrainMatchState;
    const index = matchRows(view, 0).length;
    sendOps(code, [
      {path: ["tiebreaks"], value: (state.tiebreaks || 0) + 1},
      {path: ["teams", 0, "rows", index], value: {player: "", mark: ""}},
      {path: ["teams", 1, "rows", index], value: {player: "", mark: ""}},
    ]);
  });
  bar.appendChild(add);
  const state = view.state as BrainMatchState;
  const last = matchRows(view, 0).length - 1;
  const lastEmpty = (state.tiebreaks || 0) > 0 &&
    [0, 1].every((side) => {
      const row = matchRows(view, side)[last];
      return !row?.player && !row?.mark;
    });
  if (lastEmpty) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "btn-xs";
    remove.textContent = "− П";
    remove.title = "Убрать пустой вопрос перестрелки";
    remove.addEventListener("click", () => {
      sendOps(code, [
        {path: ["tiebreaks"], value: (state.tiebreaks || 0) - 1},
        {path: ["teams", 0, "rows"], value: matchRows(view, 0).slice(0, last)},
        {path: ["teams", 1, "rows"], value: matchRows(view, 1).slice(0, last)},
      ]);
    });
    bar.appendChild(remove);
  }
  return bar;
}


interface CrossRow {
  number: number;
  name: string;
  points: number;
  plus: number;
  minus: number;
  rank: number;
}

// buildCrosstable renders the sheet's group table: score cells vs each
// opponent, then О (head-to-head points, finished бои only), + / − / +/−
// (questions taken and conceded across all бои), М (place, ranked by the
// stage's comparator order — КИНСБФ §4.2 by default).
function buildCrosstable(): HTMLElement {
  const stage = groupStage();
  const rules = groupRules();
  const entrants = rules.entrants || [];
  const win = rules.points?.win ?? 2;
  const draw = rules.points?.draw ?? 1;
  const loss = rules.points?.loss ?? 0;
  const rows: CrossRow[] = entrants.map((slot) => ({
    number: slot.seed?.number || 0,
    name: slot.label || "",
    points: 0,
    plus: 0,
    minus: 0,
    rank: 0,
  }));
  const indexByNumber = new Map<number, number>();
  rows.forEach((row, i) => indexByNumber.set(row.number, i));
  const cellText: string[][] = rows.map(() => rows.map(() => ""));
  const cellMuted: boolean[][] = rows.map(() => rows.map(() => false));
  const duels: RankDuel[] = [];

  for (const planned of groupStage().matches || []) {
    const view = matches.get(planned.code || "");
    if (!view) continue;
    const na = planned.slots?.[0]?.seed?.number || 0;
    const nb = planned.slots?.[1]?.seed?.number || 0;
    const a = indexByNumber.get(na);
    const b = indexByNumber.get(nb);
    if (a === undefined || b === undefined) continue;
    rows[a].name = teamName(view, 0);
    rows[b].name = teamName(view, 1);
    const ta = taken(view, 0);
    const tb = taken(view, 1);
    if (view.finished || started(view)) {
      cellText[a][b] = `${ta} : ${tb}`;
      cellText[b][a] = `${tb} : ${ta}`;
      cellMuted[a][b] = cellMuted[b][a] = !view.finished;
      rows[a].plus += ta;
      rows[a].minus += tb;
      rows[b].plus += tb;
      rows[b].minus += ta;
    }
    if (view.finished) {
      const [pa, pb] = ta > tb ? [win, loss] : ta < tb ? [loss, win] : [draw, draw];
      rows[a].points += pa;
      rows[b].points += pb;
      duels.push({a, b, pa, pb});
    }
  }

  const rankTeams: RankTeam[] = rows.map((row) => ({points: row.points, taken: row.plus, conceded: row.minus}));
  const ranks = rankGroup(rankTeams, duels, rules.order || undefined);
  rows.forEach((row, i) => {
    row.rank = ranks[i];
  });

  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const table = document.createElement("table");
  table.className = "match-table brain-crosstable";

  const thead = document.createElement("thead");
  const group = document.createElement("tr");
  const groupHead = document.createElement("th");
  groupHead.colSpan = rows.length + 7;
  groupHead.className = "brain-group-head";
  groupHead.textContent = stage.title || "Группа";
  group.appendChild(groupHead);
  thead.appendChild(group);

  const cols = document.createElement("tr");
  cols.className = "brain-cross-cols";
  const headCell = (text: string, className: string) => {
    const th = document.createElement("th");
    th.className = className;
    th.textContent = text;
    cols.appendChild(th);
  };
  headCell("№", "row-marker");
  headCell("Команда", "brain-cross-team-head");
  rows.forEach((_, i) => headCell(String(i + 1), "brain-cross-num"));
  headCell("О", "brain-cross-num");
  headCell("+", "brain-cross-num");
  headCell("−", "brain-cross-num");
  headCell("+/−", "brain-cross-num");
  headCell("М", "brain-cross-num");
  thead.appendChild(cols);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  rows.forEach((row, i) => {
    const tr = document.createElement("tr");
    const marker = document.createElement("td");
    marker.className = "row-marker";
    marker.textContent = String(i + 1);
    tr.appendChild(marker);
    const name = document.createElement("td");
    name.className = "brain-cross-team";
    name.textContent = row.name;
    tr.appendChild(name);
    rows.forEach((_, j) => {
      const cell = document.createElement("td");
      cell.className = "number brain-cross-cell";
      if (i === j) {
        cell.textContent = "×";
        cell.classList.add("brain-cross-diag");
      } else {
        cell.textContent = cellText[i][j];
        cell.classList.toggle("brain-cross-live", cellMuted[i][j]);
      }
      tr.appendChild(cell);
    });
    const stat = (value: string | number, extra = "") => {
      const cell = document.createElement("td");
      cell.className = "number" + (extra ? ` ${extra}` : "");
      cell.textContent = gameTable.formatDisplayText(value);
      tr.appendChild(cell);
    };
    stat(row.points);
    stat(row.plus);
    stat(row.minus);
    stat(row.plus - row.minus);
    stat(row.rank, "brain-cross-place");
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  wrap.appendChild(table);
  return wrap;
}


function handleTableChange(event: Event): void {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.classList.contains("finish-toggle")) {
    if (viewer) return;
    const code = target.dataset.match || "";
    if (matches.has(code)) sendFinish(code, target.checked);
    return;
  }
  if (target instanceof HTMLSelectElement && target.classList.contains("brain-player-select")) {
    const ctx = cellContext(target);
    if (!ctx || viewer || ctx.view.finished) return;
    matchRows(ctx.view, ctx.side)[ctx.q].player = target.value;
    sendOps(ctx.code, [{path: ["teams", ctx.side, "rows", ctx.q, "player"], value: target.value}]);
  }
}

const MARK_CYCLE: Record<string, string> = {"": "right", right: "wrong", wrong: ""};

function cellContext(el: HTMLElement): {code: string; view: BrainMatchView; side: number; q: number} | null {
  const code = el.dataset.match || "";
  const view = matches.get(code);
  const side = Number(el.dataset.side);
  const q = Number(el.dataset.q);
  if (!view || (side !== 0 && side !== 1) || !Number.isInteger(q)) return null;
  if (q < 0 || q >= matchRows(view, side).length) return null;
  return {code, view, side, q};
}

function cellNode(code: string, side: number, q: number): HTMLElement | null {
  return brainRoot.querySelector<HTMLElement>(
    `.answer-cell[data-match="${gameTable.cssEscape(code)}"][data-side="${side}"][data-q="${q}"]`,
  );
}

// The selection treats the stacked бої as one sheet: row = the question's
// global index down the page, col = the side. The widget (shared with КСИ)
// then gives click/drag/shift ranges, copy/paste and touch tap-cycling.
function globalRow(code: string, q: number): number {
  let row = 0;
  for (const bout of orderedMatches()) {
    if (bout.code === code) return row + q;
    row += matchRows(bout.view, 0).length;
  }
  return -1;
}

function boutAtRow(row: number): {code: string; q: number} | null {
  for (const bout of orderedMatches()) {
    const count = matchRows(bout.view, 0).length;
    if (row < count) return {code: bout.code, q: row};
    row -= count;
  }
  return null;
}

function totalRows(): number {
  return orderedMatches().reduce((sum, bout) => sum + matchRows(bout.view, 0).length, 0);
}

function serializeMark(cell: Element | null | undefined): string {
  const el = cell as HTMLElement | null;
  return el?.classList.contains("right") ? "1" : el?.classList.contains("wrong") ? "0" : "";
}

function parseMarkText(text: string): string {
  const value = String(text || "").trim().toLowerCase();
  if (["1", "+", "q", "й", "right"].includes(value)) return "right";
  if (["0", "-", "\u2212", "w", "ц", "wrong"].includes(value)) return "wrong";
  return "";
}

// applyMarkEdits updates the local views and DOM, then sends one PATCH per бой.
function applyMarkEdits(edits: CellEdit[]): void {
  const opsByCode = new Map<string, Array<{path: Array<string | number>; value: unknown}>>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const ctx = cellContext(cell);
    if (!ctx || viewer || ctx.view.finished) continue;
    const mark = parseMarkText(String(edit.value ?? ""));
    const row = matchRows(ctx.view, ctx.side)[ctx.q];
    if (row.mark === mark) continue;
    row.mark = mark;
    cell.classList.remove("right", "wrong");
    if (mark) cell.classList.add(mark);
    const score = cell.closest("table")?.querySelector(".brain-score-head");
    if (score) score.textContent = `${taken(ctx.view, 0)} : ${taken(ctx.view, 1)}`;
    const ops = opsByCode.get(ctx.code) || [];
    ops.push({path: ["teams", ctx.side, "rows", ctx.q, "mark"], value: mark});
    opsByCode.set(ctx.code, ops);
  }
  for (const [code, ops] of opsByCode) sendOps(code, ops);
}

function cellAt(coord: CellCoord | null): HTMLElement | null {
  if (!coord) return null;
  const at = boutAtRow(coord.row);
  return at ? cellNode(at.code, coord.col, at.q) : null;
}

const cellSelection: CellRangeSelection = gameTable.createCellRangeSelection({
  root: brainRoot,
  cellSelector: ".answer-cell",
  readonly: () => viewer,
  coordOf: (cell) => {
    const ctx = cellContext(cell as HTMLElement);
    if (!ctx) return null;
    const row = globalRow(ctx.code, ctx.q);
    return row < 0 ? null : {row, col: ctx.side};
  },
  cellAtCoord: cellAt,
  serialize: serializeMark,
  parse: parseMarkText,
  cycle: (cell) => MARK_CYCLE[parseMarkText(serializeMark(cell))] ?? "right",
  applyValues: applyMarkEdits,
  onActiveChange: (cell) => {
    brainRoot.querySelector(".answer-cell.active")?.classList.remove("active");
    cell?.classList.add("active");
  },
});
cellSelection.bind();

// restoreSelection re-applies the cursor after a re-render rebuilt the cells.
function restoreSelection(): void {
  if (activeTab !== "protocol" || !cellSelection.anchor) return;
  cellSelection.setSelection(cellSelection.anchor, cellSelection.focus, {focus: false});
}

function moveActive(dRow: number, dCol: number, extend: boolean): void {
  const from = cellSelection.focus;
  if (!from) return;
  const next = {
    row: gameTable.clamp(from.row + dRow, 0, totalRows() - 1),
    col: gameTable.clamp(from.col + dCol, 0, 1),
  };
  cellSelection.setSelection(extend ? cellSelection.anchor || from : next, next);
}

function setMarkForSelection(mark: string): void {
  const cells = cellSelection.selectedCells();
  const active = cellAt(cellSelection.focus);
  const targets = cells.length > 1 ? cells : active ? [active] : [];
  applyMarkEdits(targets.map((cell) => ({cell, value: mark})));
}

function handleKeydown(event: KeyboardEvent): void {
  if (viewer || activeTab !== "protocol" || !cellSelection.focus) return;
  const target = event.target as HTMLElement | null;
  if (target && (target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  const key = event.key.toLowerCase();
  if (event.key === "ArrowUp") moveActive(-1, 0, event.shiftKey);
  else if (event.key === "ArrowDown") moveActive(1, 0, event.shiftKey);
  else if (event.key === "ArrowLeft") moveActive(0, -1, event.shiftKey);
  else if (event.key === "ArrowRight") moveActive(0, 1, event.shiftKey);
  else if (key === "q" || key === "й" || key === "+" || key === "1" || event.code === "NumpadAdd") setMarkForSelection("right");
  else if (key === "w" || key === "ц" || key === "-" || key === "0" || event.code === "NumpadSubtract") setMarkForSelection("wrong");
  else if (key === "backspace" || key === "delete" || event.key === " ") setMarkForSelection("");
  else return;
  event.preventDefault();
}

document.addEventListener("keydown", handleKeydown);


render();
gameTable.fitScrollFade(brainRoot.closest(".sheet-frame"));
loadFestRoster();
fetchMatches()
  .then(() => {
    setStatus("saved");
    live.connect();
  })
  .catch((error: unknown) => {
    setStatus("error");
    console.error(error);
  });

export {};
