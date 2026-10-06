// The Brain page (ADR-0001): a round-robin group of head-to-head matches. The
// protocols tab mirrors the reference sheet's match block — question rows of
// № | player | mark | mark | player around a running score, with tiebreak
// rows — and the tables tab is the sheet's group cross-table (score cells,
// points/+/−/± totals, place numbers). Matches come from the rr stage; edits go per match
// (PATCH /matches/{code}/state) and sync over match: scopes, through the bout
// page (bout-page.ts). A self-booting side-effect module bundled by
// pages/brain.ts.

import {formatDisplayText, percentText, td} from "./cells.js";
import {standingsTable} from "./standings.js";
import {sheetHead} from "./sheet-pins.js";
import {buildCrosstables, crossSlot, slotKey, standingsByParticipant} from "./crosstable.js";
import type {StageRef} from "./standings.js";
import {buildGameRosterView, fetchGameRoster} from "./fest-roster.js";
import type {RosterTeam} from "./fest-roster.js";
import {groupAnchorID, mountBoutPage, tabStages, stageBouts} from "./bout-page.js";
import type {BoutPage, BoutView, BoutEntry as BoutEntryOf} from "./bout-page.js";
import type {GameInitLike} from "./game-page.js";
import {nameCell} from "./name-cell.js";
import {SEAT_PICKER_SELECTOR, seatPicker} from "./seat-picker.js";
import {figureData, paintFigures, paintMark, stackedSheet} from "./stacked-sheet.js";
import type {Mark} from "./sheet-cursor.js";
import {computeBrainPlayerStats} from "./brain-stats.js";
import type {StatsBout} from "./brain-stats.js";
import * as brain from "./brain-protocol.js";
import type {BrainMatchState, BrainRow} from "./brain-protocol.js";
import type {FestGridStage} from "./fest-grid.js";
import {canonicalKey, groupLabel} from "./game-tabs.js";
import type {GameTab} from "./game-tabs.js";
import S from "./i18nstrings.js";

interface PageGlobals {
  __GAME_INIT__?: GameInitLike | null;
}

const pageWindow = window as Window & PageGlobals;

interface BrainSlotTeam {
  id?: number;
  name?: string;
}

interface BrainMatchView extends BoutView {
  revision?: number;
  teams?: BrainSlotTeam[];
  participants?: Array<{id?: number; name?: string; place?: number} | null>;
}

interface SchemeSlotRef {
  seed?: {number?: number; position?: number} | null;
  reseed?: {stage?: string; rank?: number} | null;
  fromMatch?: {match?: string; place?: number} | null;
  label?: string;
}

interface BrainSchemeMatch {
  code?: string;
  slots?: SchemeSlotRef[];
}

// BrainStageRules is the rr stage's config vocabulary (the inner object of
// stages.config_json): entrant seeds, the ranking comparator order, the
// points rule and the appended-tiebreak switch.
interface BrainStageRules {
  entrants?: SchemeSlotRef[];
  order?: string[];
  points?: {win?: number; draw?: number; loss?: number} | null;
  tiebreakQuestions?: boolean;
  questions?: number;
}

interface BrainSchemeStage {
  code?: string;
  title?: string;
  kind?: string;
  stage_type?: string;
  grain?: {block?: string; group?: string; wave?: number};
  matches?: BrainSchemeMatch[];
  sources?: string[];
  config?: BrainStageRules | null;
}

interface BrainScheme {
  title?: string;
  questions?: number;
  stages?: BrainSchemeStage[];
  seeding?: {source?: string} | null;
  [key: string]: unknown;
}


interface FestInfo {
  title?: string;
  gameName?: string;
  schemaJson?: unknown;
  stages?: Array<(FestGridStage & {config?: {config?: BrainStageRules} | null}) | null>;
  [key: string]: unknown;
}

const brainRoot = document.getElementById("brainTable")!;

const init = pageWindow.__GAME_INIT__ || null;
const scheme = (init?.scheme || {}) as BrainScheme;
const fest = (init?.fest || null) as FestInfo | null;
// The sheet a Block's protocols tab draws: its bouts side by side, a column a
// side of a bout, a row one question. Only the bouts on that tab are in it.
const answers = stackedSheet({
  selector: ".answer-cell",
  fields: brain.SHEET_FIELDS,
  stack: "columns",
  bouts: () => (onProtocolTab() ? tabBouts(page.tab()) : []),
  codeOf: (bout: BoutEntry) => bout.code,
  rowsOf: (bout: BoutEntry) => brain.sheetRows(page.stateOf(bout.code)),
  columnsOf: () => brain.sheetColumns(),
  marks: {
    markOf: (cell) => {
      const view = page.view(cell.match);
      const row = view ? matchRows(view, cell.side)[cell.q] : undefined;
      return !view || viewer || view.finished || !row ? null : row.mark as Mark;
    },
    setMark: (cell, mark) => {
      const view = page.view(cell.match);
      const row = view ? matchRows(view, cell.side)[cell.q] : undefined;
      if (row) row.mark = mark;
    },
    pathOf: brain.markPath,
    patch: (code, path, value) => page.patch(code, path, value),
    onWritten: (codes) => codes.forEach((code) => page.refresh(code)),
  },
});

const page: BoutPage<BrainMatchView, BrainMatchState> = mountBoutPage({
  app: "brain",
  root: brainRoot,
  tabsRoot: document.getElementById("brainTabs"),
  init,
  scheme,
  fest,
  title: () => S.brain.title(),
  parse: (view) => brain.parseState(view.state, questionsFor(view.code || "")),
  blank: () => brain.parseState(null, schemeQuestions()),
  buildTab,
  buildRoster: (): HTMLElement => buildGameRosterView(page.route.apiBase || "", {editable: !page.viewer}),
  fitsFrame: (tab) => tab?.kind === "roster" || tab?.kind === "entrants",
  shape,
  repaintCells,
  cursorKinds: {
    answer: answers.cursorKind,
    // A player picker carries the address of the mark beside it.
    player: {selector: SEAT_PICKER_SELECTOR, keys: answers.cursorKind.keys},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
  cursors: () => [cursor],
  canonical: canonicalKey,
  onRoster: () => loadTeamRosters(),
});
const {viewer} = page;

let teamRosters: RosterTeam[] = [];

function stageKind(stage: BrainSchemeStage): string {
  return stage.kind || stage.stage_type || "";
}

// Every match of the game carries a letter — the sheets' A..Z, AA.. handle —
// dealt by the compiler and carried on the fest view.
const boutLetters = page.letters;

// protocolStages are the stages whose matches the page draws — everything except
// reseed edges, in scheme order.
function protocolStages(): BrainSchemeStage[] {
  return (scheme.stages || []).filter((s) => stageKind(s) !== "reseed");
}

const stageByMatch = new Map<string, BrainSchemeStage>();
for (const schemeStage of scheme.stages || []) {
  for (const planned of schemeStage.matches || []) {
    if (planned.code) stageByMatch.set(planned.code, schemeStage);
  }
}

// groupRules reads a stage's rules from the fest view — the same
// stages.config_json row the resolver ranks by, so client and server can't
// drift — falling back to the scheme's creation-time copy.
function groupRules(groupStage: BrainSchemeStage): BrainStageRules {
  const stage = fest?.stages?.find((s) => s?.code === groupStage.code);
  return stage?.config?.config || groupStage.config || {};
}

function schemeQuestions(): number {
  const n = Number(scheme.questions);
  return Number.isInteger(n) && n > 0 ? n : 5;
}

// questionsFor is a match's regular question count: its stage's override (the
// DSL's per-block/round questions cascade), else the scheme-wide default.
function questionsFor(code: string): number {
  const stage = stageByMatch.get(code);
  const n = Number(stage ? groupRules(stage).questions : 0);
  return Number.isInteger(n) && n > 0 ? n : schemeQuestions();
}

// onProtocolTab reports a protocols tab in front — the tab keys are
// `protocol:<block>`, one per Block, never the bare word.
function onProtocolTab(): boolean {
  return page.tab()?.kind === "protocol";
}

// loadTeamRosters reads the roster each team plays this game with, which is
// what a bout's player picker offers: the fest roster until the host changes a
// team's roster for this game on the roster tab.
function loadTeamRosters(): void {
  fetchGameRoster(page.route.apiBase || "")
    .then((data) => {
      teamRosters = data.teams;
      if (onProtocolTab()) page.render();
    })
    .catch(() => {});
}

function sendOps(code: string, ops: Array<{path: Array<string | number>; value: unknown}>): void {
  for (const op of ops) page.patch(code, op.path, op.value);
}

function matchRows(view: BrainMatchView, side: number): BrainRow[] {
  return (page.stateOf(view.code || "").teams?.[side]?.rows || []) as BrainRow[];
}

function taken(view: BrainMatchView, side: number): number {
  return matchRows(view, side).filter((row) => row.mark === "right").length;
}

function started(view: BrainMatchView): boolean {
  return [0, 1].some((side) => matchRows(view, side).some((row) => row.mark || row.player));
}

function teamName(view: BrainMatchView, side: number): string {
  return view.participants?.[side]?.name || S.brain.team.fallback(String(side + 1));
}

function rowLabel(index: number, base: number): string {
  if (index < base) return String(index + 1);
  return index === base ? S.brain.row.tiebreak() : S.brain.row.tiebreakN(String(index - base + 1));
}

function rosterFor(name: string): string[] {
  const wanted = name.trim().toLowerCase();
  const team = teamRosters.find((t) => (t.name || "").trim().toLowerCase() === wanted);
  return (team?.players || [])
    .map((p) => (typeof p === "string" ? p : p.name || ""))
    .filter(Boolean);
}

type BoutEntry = BoutEntryOf<BrainMatchView, BrainSchemeStage, BrainSchemeMatch>;

// allBouts flattens every protocol stage's matches in scheme order, for the
// statistics.
function allBouts(): BoutEntry[] {
  return protocolStages().flatMap((stage) => stageBouts(page, stage));
}

// tabBouts are the bouts a protocols tab draws, in the order it draws them.
function tabBouts(tab: GameTab | undefined): BoutEntry[] {
  return tabStages(scheme.stages, tab).flatMap((stage) => stageBouts(page, stage));
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "stats":
    return buildStatsView();
  case "block":
    return buildCrosstable(tabStages(scheme.stages, tab));
  case "pods":
    return buildPodBoard(tabStages(scheme.stages, tab));
  default:
    // A Block's protocols; the module draws the grid and the reseeds.
    return buildProtocols(tabStages(scheme.stages, tab));
  }
}

// buildProtocols lays one Block's matches out the way the sheet's protocols tab
// does: a section per stage (group, pod, round), its matches side by side.
function buildProtocols(stages: BrainSchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const multi = stages.length > 1;
  let rendered = 0;
  for (const stage of stages) {
    const bouts = stageBouts(page, stage);
    if (!bouts.length) continue;
    rendered++;
    if (multi) wrap.appendChild(protocolStageHead(stage));
    const row = document.createElement("div");
    row.className = "brain-bouts";
    for (const bout of bouts) {
      row.appendChild(buildBout(bout));
    }
    wrap.appendChild(row);
  }
  if (!rendered) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.brain.protocol.empty();
    wrap.appendChild(empty);
  }
  return wrap;
}

function protocolStageHead(stage: BrainSchemeStage): HTMLElement {
  const head = document.createElement("h2");
  head.className = "brain-stage-head";
  head.textContent = stage.title || stage.code || "";
  return head;
}

// buildPodBoard is a pod Block's detail tab, the sheet's «Double Elimination»
// view: one column per block round, each match a box with its letter, teams and
// Σ. The same boxes the grid once carried — moved where detail belongs. A pod is
// a row band: its matches of every block round sit in the band, so a block round
// with one match per pod leaves the pod's other slot blank and the columns read
// across.
function blockRoundOf(planned: BrainSchemeMatch): number {
  return Number((planned as {round?: number}).round || 1);
}

function podSlots(pods: BrainSchemeStage[]): number[][] {
  return pods.map((stage) => {
    const seen = new Map<number, number>();
    return (stage.matches || []).map((planned) => {
      const slot = seen.get(blockRoundOf(planned)) || 0;
      seen.set(blockRoundOf(planned), slot + 1);
      return slot;
    });
  });
}

function buildPodBoard(pods: BrainSchemeStage[]): HTMLElement {
  type GridMatch = NonNullable<FestGridStage["matches"]>[number];
  const byBlockRound = new Map<number, GridMatch[]>();
  // slots[pod][i] is the match's slot within its pod's block round; podRows is
  // the widest.
  const slots = podSlots(pods);
  const podRows = Math.max(0, ...slots.flat()) + 1;
  pods.forEach((stage, pod) => {
    const live = new Map((page.festStage(stage.code || "")?.matches || []).map((m) => [m.code, m]));
    (stage.matches || []).forEach((planned, i) => {
      const blockRound = blockRoundOf(planned);
      const merged = {...(planned as GridMatch), ...(live.get(planned.code) || {}), row: pod * podRows + slots[pod][i] + 1};
      const list = byBlockRound.get(blockRound);
      if (list) list.push(merged);
      else byBlockRound.set(blockRound, [merged]);
    });
  });
  const stages = Array.from(byBlockRound.keys()).sort((a, b) => a - b).map((blockRound): FestGridStage => ({
    code: `round-${blockRound}`,
    title: S.brain.pod.blockRound(String(blockRound)),
    stage_type: "matches",
    matches: byBlockRound.get(blockRound),
  }));
  return page.grid({stages});
}

// buildStatsView is the sheet's individual statistics: attempts, right,
// wrong per (player, team) over the regular questions of finished matches.
function buildStatsView(): HTMLElement {
  const bouts: StatsBout[] = [];
  for (const {code, view} of allBouts()) {
    const rowCount = matchRows(view, 0).length;
    const rows: StatsBout["rows"] = [];
    for (let q = 0; q < rowCount; q++) {
      rows.push([matchRows(view, 0)[q] || {}, matchRows(view, 1)[q] || {}]);
    }
    bouts.push({teams: [teamName(view, 0), teamName(view, 1)], regular: questionsFor(code), rows});
  }
  const stats = computeBrainPlayerStats(bouts);
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (!stats.length) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.brain.stats.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  // The same table EK's statistics is — its name columns size to content and
  // its numbers sit tight — with the buzzer's columns in place of the themes'.
  wrapper.appendChild(standingsTable({
    className: "ek-stats-table",
    sortKey: "brain-stats",
    columns: [
      {label: S.brain.stats.player(), kind: "name", className: "ek-stats-name ek-stats-player"},
      {label: S.brain.stats.team(), kind: "name", className: "ek-stats-name"},
      {label: S.brain.stats.attempts(), kind: "num"},
      {label: S.brain.stats.right(), kind: "num"},
      {label: S.brain.stats.wrong(), kind: "num", className: "ek-stats-wrong"},
      {label: S.brain.stats.share(), kind: "num", className: "ek-stats-share"},
    ],
    rows: stats.map((row) => [
      row.player,
      row.team,
      row.attempts,
      row.right,
      row.wrong,
      row.attempts ? percentText(row.right / row.attempts) : "",
    ]),
  }));
  return wrapper;
}

function buildBout({code, view, planned}: BoutEntry): HTMLElement {
  // The bout page keeps a bout's size and the view across redraws by its box,
  // and a link lands on it.
  const section = page.boutBox(code, "brain-bout");
  const editable = !viewer && !view.finished;

  const table = document.createElement("table");
  table.className = "match-table brain-detailed";
  table.classList.toggle("match-finished", Boolean(view.finished));
  table.dataset.match = code;

  // When the bout starts, once the host gave it a time, with its venue, and
  // the host's pencil that sets both.
  const whereWhen = page.whereWhen(code, {title: view.title || code, venueAlways: false, className: "battle-venue"});
  // The time goes over the table; the pencil sits by the finished tick, so a
  // bout with no time keeps the one head row it always had.
  const pencil = whereWhen.find((node) => node.classList.contains("venue-edit-button"));
  const shown = whereWhen.filter((node) => node !== pencil);
  if (shown.length) {
    const caption = document.createElement("caption");
    const line = document.createElement("span");
    line.className = "u-row u-gap-sm u-align-center";
    line.append(...shown);
    caption.appendChild(line);
    table.appendChild(caption);
  }

  // One head row, everything on one line: the match's letter, a team over its
  // player column, the score over the two mark columns, the other team, and
  // the finished tick. A name wider than its column fades to a popover.
  const head = document.createElement("tr");
  const corner = document.createElement("th");
  corner.className = "row-marker brain-bout-corner";
  corner.textContent = boutLetters.get(code) || (code.split("-").pop() || code).replace(/^m/, "");
  corner.title = view.title || code;
  head.appendChild(corner);
  head.appendChild(nameHead(view, 0, planned));
  const score = document.createElement("th");
  score.className = "number brain-score-head";
  score.colSpan = 2;
  Object.assign(score.dataset, figureData("score"));
  score.textContent = `${taken(view, 0)} : ${taken(view, 1)}`;
  head.appendChild(score);
  head.appendChild(nameHead(view, 1, planned));
  const finish = document.createElement("th");
  finish.className = "brain-finish-head";
  finish.appendChild(page.finishToggle(code, {text: S.brain.bout.finished()}));
  if (pencil) finish.prepend(pencil);
  head.appendChild(finish);
  table.appendChild(sheetHead([{row: head}]));

  const tbody = document.createElement("tbody");
  const rowCount = matchRows(view, 0).length;
  const base = questionsFor(code);
  for (let q = 0; q < rowCount; q++) {
    const tr = document.createElement("tr");
    const marker = document.createElement("td");
    marker.className = "row-marker" + (q >= base ? " brain-tiebreak-marker" : "");
    marker.textContent = rowLabel(q, base);
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
  section.appendChild(table);
  const stage = stageByMatch.get(code);
  if (editable && stage && groupRules(stage).tiebreakQuestions) {
    section.appendChild(tiebreakControls(code, view));
  }
  return section;
}

// nameHead shows the seated team; an unresolved slot shows its source label
// (seed 5, group 1-2 — the server fills it from the slot ref) muted.
function nameHead(view: BrainMatchView, side: number, planned: BrainSchemeMatch): HTMLElement {
  const label = view.participants?.[side]?.name || planned.slots?.[side]?.label || "—";
  return nameCell(label, {
    tag: "th",
    className: "brain-name-head",
    textClassName: view.participants?.[side]?.id ? undefined : "brain-name-pending",
  });
}

// playerCell is who answered question q for one side. The document keeps the
// player's name, "" for nobody, so a name is the picker's id.
function playerCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  td.className = "brain-player-cell";
  const current = matchRows(view, side)[q]?.player || "";
  td.appendChild(seatPicker({
    roster: rosterFor(teamName(view, side)).map((player) => ({id: player, name: player})),
    seated: current ? [current] : [],
    disabled: !editable,
    dataset: answers.dataset({match: code, side, q}),
    onChange: (seated) => setPlayer(code, side, q, seated[0] || ""),
  }).element);
  return td;
}

function markCell(code: string, view: BrainMatchView, side: number, q: number, editable: boolean): HTMLElement {
  const td = document.createElement("td");
  td.className = "answer-cell";
  paintMark(td, (matchRows(view, side)[q]?.mark || "") as Mark);
  td.tabIndex = editable ? 0 : -1;
  Object.assign(td.dataset, answers.dataset({match: code, side, q}));
  td.title = S.brain.mark.title(teamName(view, side), rowLabel(q, questionsFor(code)));
  return td;
}

// tiebreakControls adds/removes tiebreak rows — the tiebreak questions appended
// after the base K when this match must produce a winner.
function tiebreakControls(code: string, view: BrainMatchView): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "brain-controls";
  const add = document.createElement("button");
  add.type = "button";
  add.className = "btn-xs";
  add.textContent = S.brain.tiebreak.add();
  add.title = S.brain.tiebreak.addHint();
  add.addEventListener("click", () => {
    const state = page.stateOf(code);
    const index = matchRows(view, 0).length;
    sendOps(code, [
      {path: ["tiebreaks"], value: (state.tiebreaks || 0) + 1},
      {path: ["teams", 0, "rows", index], value: {player: "", mark: ""}},
      {path: ["teams", 1, "rows", index], value: {player: "", mark: ""}},
    ]);
  });
  bar.appendChild(add);
  const state = page.stateOf(code);
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
    remove.textContent = S.brain.tiebreak.remove();
    remove.title = S.brain.tiebreak.removeHint();
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


// The group table is crosstable.ts's, so Brain and Troika draw one table.
// This says only what a Brain match is: two sides, taken marks as the score.
function buildCrosstable(stages: BrainSchemeStage[]): HTMLElement {
  return buildCrosstables({
    className: "brain-groups",
    groups: stages.filter((stage) => stageKind(stage) === "rr").map((stage) => ({
      title: groupLabel(stage as StageRef),
      anchor: groupAnchorID(stage.code || ""),
      entrants: (groupRules(stage).entrants || []).map(crossSlot),
      bouts: (stage.matches || []).flatMap((planned) => {
        const view = page.view(planned.code || "");
        if (!view) return [];
        return [{
          slots: [crossSlot(planned.slots?.[0]), crossSlot(planned.slots?.[1])],
          sides: [0, 1].map((side) => ({
            name: view.participants?.[side]?.name || "",
            id: Number(view.participants?.[side]?.id || 0),
            score: taken(view, side),
          })),
          finished: Boolean(view.finished),
          started: started(view),
          href: page.boutHref(planned.code || ""),
        }];
      }),
      standings: standingsByParticipant(page.festStage(stage.code || "")),
    })),
  });
}

// setPlayer writes who answered a question, as the host picked it.
function setPlayer(code: string, side: number, q: number, player: string): void {
  const view = page.view(code);
  if (!view || viewer || view.finished) return;
  const rows = matchRows(view, side);
  if (q < 0 || q >= rows.length) return;
  rows[q].player = player;
  sendOps(code, [{path: ["teams", side, "rows", q, "player"], value: player}]);
  page.refresh(code);
}

// === the repaint contract ===

// shape is everything of a bout its box draws except the marks and the score
// they feed: the sides, who answered each question, the tiebreak rows and
// whether the last one may be dropped, the head.
function shape(code: string): string {
  const view = page.view(code);
  if (!view) return "";
  const state = page.stateOf(code);
  const last = matchRows(view, 0).length - 1;
  return JSON.stringify({
    finished: Boolean(view.finished), title: view.title || "",
    venue: view.venue ? [view.venue.number, view.venue.title] : null, startsAt: view.startsAt || "",
    sides: (view.participants || []).map((side) => [side?.id, side?.name]),
    players: [0, 1].map((side) => matchRows(view, side).map((row) => row.player || "")),
    tiebreaks: state.tiebreaks || 0,
    lastEmpty: [0, 1].every((side) => !matchRows(view, side)[last]?.mark),
  });
}

// repaintCells brings a drawn bout's marks and its score in line with the
// state, where they stand.
function repaintCells(code: string): void {
  const view = page.view(code);
  if (!view) return;
  const box = page.boxOf(code);
  answers.paint(box, code, (cell) => (matchRows(view, cell.side)[cell.q]?.mark || "") as Mark);
  paintFigures(box, (figure) => (figure === "score" ? `${taken(view, 0)} : ${taken(view, 1)}` : undefined));
}

const cursor = answers.cursor(brainRoot, {
  readonly: () => viewer,
  active: onProtocolTab,
  values: "marks",
  classes: {row: ""},
});
loadTeamRosters();
page.start();

export {};
