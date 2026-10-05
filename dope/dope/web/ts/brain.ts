// The Brain page (ADR-0001): a round-robin group of head-to-head matches. The
// protocols tab mirrors the reference sheet's match block — question rows of
// № | player | mark | mark | player around a running score, with tiebreak
// rows — and the tables tab is the sheet's group cross-table (score cells,
// points/+/−/± totals, place numbers). Matches come from the rr stage; edits go per match
// (PATCH /matches/{code}/state) and sync over match: scopes, through the bout
// page (bout-page.ts). A self-booting side-effect module bundled by
// pages/brain.ts.

import {cssEscape, formatDisplayText, percentText, td} from "./cells.js";
import {standingsTable} from "./standings.js";
import {sheetHead} from "./sheet-pins.js";
import {buildCrosstables, crossSlot, slotKey, standingsByParticipant} from "./crosstable.js";
import type {StageRef} from "./standings.js";
import {buildGameRosterView, fetchGameRoster} from "./fest-roster.js";
import type {RosterTeam} from "./fest-roster.js";
import {mountBoutPage} from "./bout-page.js";
import type {BoutPage, BoutView} from "./bout-page.js";
import type {GameInitLike} from "./game-page.js";
import {nameCell} from "./name-cell.js";
import {SEAT_PICKER_SELECTOR, seatPicker} from "./seat-picker.js";
import {createSheetCursor, parseMark} from "./sheet-cursor.js";
import type {CellCoord, CellEdit} from "./sheet-cursor.js";
import {computeBrainPlayerStats} from "./brain-stats.js";
import type {StatsBout} from "./brain-stats.js";
import * as brain from "./brain-protocol.js";
import type {BrainMatchState, BrainRow} from "./brain-protocol.js";
import {buildFestGrid, buildReseedStagePanel} from "./fest-grid.js";
import type {FestGridStage, ReseedEntry} from "./fest-grid.js";
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
  boutSelector: ".brain-bout",
  cursorKinds: {
    answer: {selector: ".answer-cell", keys: ["match", "side", "q"]},
    player: {selector: SEAT_PICKER_SELECTOR, keys: ["match", "side", "q"]},
    finish: {selector: ".finish-toggle", keys: ["match"]},
  },
  activeCursorElement: () => cursor.activeCell,
  cursors: () => [cursor],
  canonical: canonicalKey,
  onRoster: () => loadTeamRosters(),
});
const {viewer} = page;

let teamRosters: RosterTeam[] = [];

function tabStages(tab: GameTab): BrainSchemeStage[] {
  return (scheme.stages || []).filter((stage) => tab.stages.includes(stage.code || ""));
}

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

interface BoutEntry {
  code: string;
  view: BrainMatchView;
  planned: BrainSchemeMatch;
  stage: BrainSchemeStage;
}

function stageBouts(stage: BrainSchemeStage): BoutEntry[] {
  const out: BoutEntry[] = [];
  for (const planned of stage.matches || []) {
    const code = planned.code || "";
    const view = page.view(code);
    if (view) out.push({code, view, planned, stage});
  }
  return out;
}

// allBouts flattens every protocol stage's matches in scheme order — the selection
// grid's column space spans them all.
function allBouts(): BoutEntry[] {
  return protocolStages().flatMap(stageBouts);
}

function buildTab(tab: GameTab | undefined): HTMLElement {
  switch (tab?.kind) {
  case "stats":
    return buildStatsView();
  case "reseed":
    return buildReseedTab(tabStages(tab));
  case "block":
    return buildCrosstable(tabStages(tab));
  case "pods":
    return buildPodBoard(tabStages(tab));
  case "protocol":
    return buildProtocols(tabStages(tab));
  default:
    return buildGrid();
  }
}

// The live fest view feeds the reseed panels (entries, sort rules). The init
// snapshot goes stale, so the calculate button adopts the fresh view it gets back.
const reseedError = new Map<string, string>();

function reseedPendingBouts(stage: BrainSchemeStage): string[] {
  const sources = new Set(stage.sources || []);
  const pending: string[] = [];
  for (const src of protocolStages()) {
    if (!src.code || !sources.has(src.code)) continue;
    for (const planned of src.matches || []) {
      const code = planned.code || "";
      if (!page.view(code)?.finished) pending.push(code);
    }
  }
  return pending;
}

async function calculateReseed(code: string): Promise<void> {
  const sent = await page.reseed(code);
  if (sent.ok) reseedError.delete(code);
  else reseedError.set(code, sent.error || S.brain.reseed.calculateFailed());
  page.render();
}

function buildBrainReseedPanel(stage: BrainSchemeStage): HTMLElement {
  const code = stage.code || "";
  const pending = reseedPendingBouts(stage);
  const blocked = pending.length === 1
    ? S.brain.reseed.pendingOne(pending[0])
    : pending.length > 1 ? S.brain.reseed.pendingMany(pending.join(", ")) : "";
  const panel = buildReseedStagePanel({...(page.festStage(code) || {}), code}, {
    letters: boutLetters,
    editable: !viewer,
    canCalculate: pending.length === 0,
    blockedMessage: blocked,
    onCalculate: () => void calculateReseed(code),
  });
  const errorText = reseedError.get(code);
  if (errorText) {
    const note = document.createElement("p");
    note.className = "brain-seed-error";
    note.textContent = errorText;
    panel.appendChild(note);
  }
  return panel;
}

// buildProtocols lays one Block's matches out the way the sheet's protocols tab
// does: a section per stage (group, pod, round), its matches side by side.
function buildProtocols(stages: BrainSchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "brain-protocol";
  const multi = stages.length > 1;
  let rendered = 0;
  for (const stage of stages) {
    const bouts = stageBouts(stage);
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

// buildGrid is the grid tab: the whole Game at a glance from the same fest data
// the EK pages draw — every Block one column, place-grain, no protocol detail.
function buildGrid(): HTMLElement {
  return buildFestGrid({schemaJson: fest?.schemaJson, stages: page.gridStages()},
    {stageHeaderLink: false, matchTitleLink: false, letters: boutLetters});
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
  return buildFestGrid({stages}, {stageHeaderLink: false, matchTitleLink: false, letters: boutLetters});
}

// buildReseedTab stacks every reseed's panel, each under the name of the
// stage it seats.
function buildReseedTab(reseeds: BrainSchemeStage[]): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "reseed-fold";
  const stages = scheme.stages || [];
  for (const stage of reseeds) {
    if (reseeds.length > 1) {
      const index = stages.indexOf(stage);
      const next = stages.slice(index + 1).find((s) => stageKind(s) !== "reseed");
      const head = document.createElement("h3");
      head.className = "reseed-fold-head";
      head.textContent = String(next?.title || "");
      wrap.appendChild(head);
    }
    wrap.appendChild(buildBrainReseedPanel(stage));
  }
  return wrap;
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
  const section = document.createElement("section");
  section.className = "brain-bout";
  // The bout page keeps a bout's size and the view across redraws by this id.
  section.id = `brain-bout-${code}`;
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
    dataset: {match: code, side: String(side), q: String(q)},
    onChange: (seated) => setPlayer(code, side, q, seated[0] || ""),
  }).element);
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
}

function cellContext(el: HTMLElement): {code: string; view: BrainMatchView; side: number; q: number} | null {
  const code = el.dataset.match || "";
  const view = page.view(code);
  const side = Number(el.dataset.side);
  const q = Number(el.dataset.q);
  if (!view || (side !== 0 && side !== 1) || !Number.isInteger(q)) return null;
  if (q < 0 || q >= matchRows(view, side).length) return null;
  return {code, view, side, q};
}

function cellNode(code: string, side: number, q: number): HTMLElement | null {
  return brainRoot.querySelector<HTMLElement>(
    `.answer-cell[data-match="${cssEscape(code)}"][data-side="${side}"][data-q="${q}"]`,
  );
}

// The selection treats the matches laid side by side as one sheet: row = the
// question, col = match × 2 + side. The widget (shared with KSI) then gives
// click/drag/shift ranges, copy/paste and touch tap-cycling.
function cellCoord(code: string, side: number, q: number): CellCoord | null {
  const idx = allBouts().findIndex((bout) => bout.code === code);
  return idx < 0 ? null : {row: q, col: idx * 2 + side};
}

function boutAtCol(col: number): {code: string; view: BrainMatchView; side: number} | null {
  const bout = allBouts()[Math.floor(col / 2)];
  return bout ? {code: bout.code, view: bout.view, side: col % 2} : null;
}

function totalCols(): number {
  return allBouts().length * 2;
}

// applyMarkEdits updates the local views and DOM, then sends one PATCH per match.
function applyMarkEdits(edits: CellEdit[]): void {
  const opsByCode = new Map<string, Array<{path: Array<string | number>; value: unknown}>>();
  for (const edit of edits) {
    const cell = edit.cell as HTMLElement;
    const ctx = cellContext(cell);
    if (!ctx || viewer || ctx.view.finished) continue;
    const mark = parseMark(edit.value);
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
  const at = boutAtCol(coord.col);
  return at ? cellNode(at.code, at.side, coord.row) : null;
}

// The matches laid side by side are one sheet: row = the question, col = match × 2 +
// side; a match with tiebreak questions is taller than its neighbours.
const cursor = createSheetCursor({
  root: brainRoot,
  rows: (col) => {
    const at = boutAtCol(col);
    return at ? matchRows(at.view, 0).length : 0;
  },
  cols: () => totalCols(),
  readonly: () => viewer,
  active: onProtocolTab,
  coordOf: (cell) => {
    const ctx = cellContext(cell as HTMLElement);
    return ctx ? cellCoord(ctx.code, ctx.side, ctx.q) : null;
  },
  cellAt,
  values: "marks",
  applyValues: applyMarkEdits,
  classes: {row: ""},
});
loadTeamRosters();
page.start();

export {};
