// od-protocol.ts — the OD Protocol's document as the page reads it: the shape
// (ODState), the adapter from the server's JSON to that shape (parseState), and
// the arithmetic over it — who took what, totals per tour, the rating, the
// shootout tiebreak and the places. Pure: every function takes the state it
// reads, so the page's renderers call it with theirs and a test with a fixture.
import {computePlaces} from "./score-table.js";

// A game with no tour composition plays one tour of this many questions.
const DEFAULT_TOUR_QUESTIONS = 15;

export interface ODTeam {
  name: string;
  city: string;
  number?: number;
  // The Divisions the team carries, by short name, propagated with the roster
  // (ADR-0020). The page filters and re-ranks on them; the arithmetic below
  // never looks at them, because what a question is worth is reckoned over
  // everyone who played it.
  flags?: string[];
  // Where a team of a synch fest plays and its number there, under which its
  // venue's sheet records it (ADR-0033).
  venue?: number;
  venueNumber?: number;
  // On a venue's sheet, which numbers the team by venueNumber, the number the
  // document knows it by.
  festNumber?: number;
}

// ODSheet is one venue's part of a synch fest's OD: for each question the
// venue numbers of its teams that took it, and which questions it completed.
export interface ODSheet {
  entries: number[][];
  completed: boolean[];
}

export type ShootoutMark = "right" | "";

export interface ShootoutRound {
  teams: number[];
  entries: number[][];
  completed: boolean[];
  answers: ShootoutMark[][];
}

// A contested answer: a team's disputed answer to a question, and the jury's ruling.
// The server writes the list from its own tables and leaves the text out,
// which only the hosts and the jury may read (ContestedText).
export type ContestedStatus = "pending" | "accepted" | "rejected";

export interface ODContested {
  question: number;
  number: number;
  status: ContestedStatus;
}

// ContestedText is a contested answer as the hosts and the jury read it.
export interface ContestedText extends ODContested {
  answer: string;
}

export interface ODState {
  teams: ODTeam[];
  entries: number[][];
  completed: boolean[];
  shootoutRounds: ShootoutRound[];
  // The sheets of a synch fest's one OD, by venue id; entries and completed
  // then say nothing until flattenVenues folds the sheets into them.
  venues?: Record<string, ODSheet>;
  contested?: ODContested[];
  answers?: unknown;
  finished?: unknown;
}

export interface ODScheme {
  title?: string;
  tourComp?: unknown;
  // A DSL-compiled game carries the Protocol's params on its one stage.
  stages?: Array<{config?: {tourComp?: unknown}}>;
  teams?: Array<{name?: string; city?: string}>;
  nTeams?: number;
}

export interface QuestionStat {
  completed: boolean;
  counts: Map<number, number>;
  validCount: number;
}

export interface RankKey {
  index: number;
  total: number;
  tiebreak: number[];
}

// parseTourComp reads the tour composition: a list of sizes, or the compact
// string form "15,15,15" / "12*3".
export function parseTourComp(value: unknown): number[] {
  if (Array.isArray(value)) return value.map((n) => Number(n) || 0).filter((n) => n > 0);
  if (typeof value === "string") {
    const out: number[] = [];
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
  return [DEFAULT_TOUR_QUESTIONS];
}

// tourLengthsOf is the scheme's tour composition, from the game or its one stage.
export function tourLengthsOf(scheme: ODScheme): number[] {
  return parseTourComp(scheme.tourComp ?? scheme.stages?.[0]?.config?.tourComp);
}

// parseState is the adapter from whatever the server stored to an ODState the
// renderers can trust: the team list (from the scheme when the document has
// none), an entries grid of exactly totalQuestions rows × teams, a completed
// flag per question, normalised shootout rounds; retired fields dropped.
export function parseState(raw: unknown, scheme: ODScheme, totalQuestions: number): ODState {
  const state = (raw && typeof raw === "object" ? raw : {}) as ODState;
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
    const arr: unknown[] = Array.isArray(row) ? row.slice(0, n) : [];
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
  if (state.venues && typeof state.venues === "object") parseVenueSheets(state, totalQuestions);
  state.contested = parseContested(state.contested);
  delete state.answers;
  delete state.finished;
  return state;
}

// parseVenueSheets gives each venue's sheet a column per question with a slot
// per team of the venue, and a completion per question.
function parseVenueSheets(state: ODState, totalQuestions: number): void {
  const counts = new Map<string, number>();
  for (const team of state.teams) {
    if (team.venue) counts.set(String(team.venue), (counts.get(String(team.venue)) || 0) + 1);
  }
  const venues = state.venues as Record<string, Partial<ODSheet>>;
  for (const [id, sheet] of Object.entries(venues)) {
    const n = counts.get(id) || 0;
    const entries = Array.isArray(sheet.entries) ? sheet.entries : [];
    const completed = Array.isArray(sheet.completed) ? sheet.completed : [];
    venues[id] = {
      entries: Array.from({length: totalQuestions}, (_, q) => {
        const column = (Array.isArray(entries[q]) ? entries[q] : []).slice(0, n).map((v) => Math.max(0, Math.trunc(Number(v)) || 0));
        while (column.length < n) column.push(0);
        return column;
      }),
      completed: Array.from({length: totalQuestions}, (_, q) => Boolean(completed[q])),
    };
  }
}

const CONTESTED_STATUSES: readonly ContestedStatus[] = ["pending", "accepted", "rejected"];

export function parseContested(raw: unknown): ODContested[] {
  if (!Array.isArray(raw)) return [];
  const out: ODContested[] = [];
  for (const item of raw) {
    const row = (item && typeof item === "object" ? item : {}) as Partial<ODContested>;
    const question = Number(row.question);
    const number = Number(row.number);
    if (!Number.isInteger(question) || question < 0 || !Number.isInteger(number) || number <= 0) continue;
    const status = CONTESTED_STATUSES.includes(row.status as ContestedStatus) ? row.status as ContestedStatus : "pending";
    out.push({question, number, status});
  }
  return out;
}

// isSynch says the document keeps a sheet per venue.
export function isSynch(state: ODState): boolean {
  return Boolean(state.venues && Object.keys(state.venues).length > 0);
}

// flattenVenues is the document as one sheet keyed by the teams' fest
// numbers, for the standings: a team took a question its venue completed with
// its number entered there, and a question is completed once any venue
// completed it. The server's FlattenVenues reckons the same.
export function flattenVenues(state: ODState): ODState {
  const venues = state.venues || {};
  const total = state.completed.length;
  const n = state.teams.length;
  const entries: number[][] = Array.from({length: total}, () => []);
  const completed = Array.from({length: total}, (_, q) => Object.values(venues).some((sheet) => sheet.completed[q]));
  for (const team of state.teams) {
    const sheet = venues[String(team.venue || 0)];
    if (!sheet || !team.venueNumber) continue;
    for (let q = 0; q < total; q++) {
      if (sheet.completed[q] && sheet.entries[q]?.includes(team.venueNumber)) entries[q].push(team.number || 0);
    }
  }
  for (const column of entries) {
    while (column.length < n) column.push(0);
  }
  return {...state, entries, completed};
}

// venueSheet is one venue as the input tab enters it: its teams under their
// numbers there, and its sheet, whose arrays it shares so an edit lands in the
// document.
export function venueSheet(state: ODState, venueID: number): ODState {
  const sheet = state.venues?.[String(venueID)] || {entries: [], completed: []};
  const here = state.teams.filter((team) => team.venue === venueID);
  const teams = here.map((team) => ({...team, number: team.venueNumber || 0, festNumber: team.number}));
  const venueNumber = new Map(here.map((team) => [team.number || 0, team.venueNumber || 0]));
  const contested = (state.contested || [])
    .filter((item) => venueNumber.get(item.number))
    .map((item) => ({...item, number: venueNumber.get(item.number) || 0}));
  return {teams, entries: sheet.entries, completed: sheet.completed, shootoutRounds: [], contested};
}

export function normalizeShootoutRound(round: unknown): ShootoutRound {
  const source = (round && typeof round === "object" ? round : {}) as {
    teams?: unknown;
    answers?: unknown;
    questions?: unknown;
    entries?: unknown;
    completed?: unknown;
  };
  const seen = new Set<number>();
  const teams: number[] = [];
  const rawTeams: unknown[] = Array.isArray(source.teams) ? source.teams : [];
  for (const value of rawTeams) {
    const number = Number(value);
    if (!Number.isInteger(number) || number <= 0 || seen.has(number)) continue;
    seen.add(number);
    teams.push(number);
  }

  let rawAnswers: unknown[] = Array.isArray(source.answers) ? source.answers : [];
  if (rawAnswers.length === 0 && Array.isArray(source.questions)) rawAnswers = source.questions;
  const answers = rawAnswers.map((row) => {
    const values: unknown[] = Array.isArray(row) ? row.slice(0, teams.length) : [];
    while (values.length < teams.length) values.push("");
    return values.map(normalizeShootoutMark);
  });
  let rawEntries: unknown[] = Array.isArray(source.entries) ? source.entries : [];
  if (rawEntries.length === 0 && answers.length > 0) {
    rawEntries = answers.map((row) =>
      teams.filter((_, index) => normalizeShootoutMark(row?.[index]) === "right"));
  }
  const questionCount = Math.max(teams.length > 0 ? 1 : 0, answers.length, rawEntries.length);
  while (answers.length < questionCount) {
    answers.push(Array<ShootoutMark>(teams.length).fill(""));
  }
  const entries: number[][] = [];
  for (let questionIndex = 0; questionIndex < questionCount; questionIndex++) {
    const rawRow: unknown[] = Array.isArray(rawEntries[questionIndex]) ? rawEntries[questionIndex] as unknown[] : [];
    entries.push(normalizeShootoutEntryRowForTeams(rawRow, teams));
  }
  let completed: boolean[];
  if (Array.isArray(source.completed)) {
    completed = source.completed.slice(0, questionCount).map(Boolean);
    while (completed.length < questionCount) completed.push(false);
  } else {
    completed = answers.map((row) => (row || []).some((mark) => normalizeShootoutMark(mark) === "right"));
    while (completed.length < questionCount) completed.push(false);
  }
  const normalized: ShootoutRound = {teams, entries, completed, answers};
  for (let questionIndex = 0; questionIndex < entries.length; questionIndex++) {
    syncShootoutAnswersFromEntries(normalized, questionIndex);
  }
  return normalized;
}

export function normalizeShootoutMark(value: unknown): ShootoutMark {
  return value === "right" ? "right" : "";
}

export function normalizeShootoutEntryRow(row: unknown, length: number): number[] {
  const values: unknown[] = Array.isArray(row) ? row.slice(0, length) : [];
  while (values.length < length) values.push(0);
  return values.map((value) => {
    const number = Number(value);
    return Number.isInteger(number) && number >= 0 ? number : 0;
  });
}

export function normalizeShootoutEntryRowForTeams(row: unknown, teams: number[]): number[] {
  const out = Array<number>(teams.length).fill(0);
  for (const value of normalizeShootoutEntryRow(row, teams.length)) {
    if (!value) continue;
    const participantIndex = teams.indexOf(value);
    if (participantIndex >= 0) out[participantIndex] = value;
  }
  return out;
}

// syncShootoutAnswersFromEntries derives the marks row of a shootout question
// from its entries row — entries are what the host types, marks what the
// table shows.
export function syncShootoutAnswersFromEntries(round: ShootoutRound | null | undefined, questionIndex: number): void {
  if (!round) return;
  const row = normalizeShootoutEntryRowForTeams(round.entries?.[questionIndex], round.teams);
  if (!Array.isArray(round.entries)) round.entries = [];
  round.entries[questionIndex] = row;
  if (!Array.isArray(round.answers)) round.answers = [];
  while (round.answers.length <= questionIndex) round.answers.push(Array<ShootoutMark>(round.teams.length).fill(""));
  const answerRow = Array<ShootoutMark>(round.teams.length).fill("");
  for (const number of row) {
    if (!number) continue;
    const participantIndex = round.teams.indexOf(number);
    if (participantIndex >= 0) answerRow[participantIndex] = "right";
  }
  round.answers[questionIndex] = answerRow;
}

// === who is who ===

export function teamNumber(state: ODState, teamIndex: number): number {
  const value = Number(state.teams[teamIndex]?.number);
  return Number.isInteger(value) && value > 0 ? value : 0;
}

export function allTeamsNumbered(state: ODState): boolean {
  if (!state.teams.length) return false;
  for (let i = 0; i < state.teams.length; i++) {
    if (!teamNumber(state, i)) return false;
  }
  return true;
}

// numberIndex maps a team Number to its row; the entries grid stores Numbers.
export function numberIndex(state: ODState): Map<number, number> {
  const index = new Map<number, number>();
  for (let i = 0; i < state.teams.length; i++) {
    const n = teamNumber(state, i);
    if (n) index.set(n, i);
  }
  return index;
}

// === scoring ===

// questionStats folds the entries grid once: per question, who took it (by
// team row) and how many did — what every total and rating reads.
// A contested answer the jury accepted counts as taken; the server's Scored reckons
// the same.
export function questionStats(state: ODState, totalQuestions: number, index: Map<number, number> = numberIndex(state)): QuestionStat[] {
  const stats: QuestionStat[] = [];
  for (let q = 0; q < totalQuestions; q++) {
    const counts = new Map<number, number>();
    if (state.completed[q]) {
      for (const value of state.entries[q] || []) {
        const teamIndex = index.get(value);
        if (teamIndex === undefined) continue;
        counts.set(teamIndex, (counts.get(teamIndex) || 0) + 1);
      }
    }
    stats.push({completed: Boolean(state.completed[q]), counts, validCount: counts.size});
  }
  for (const item of state.contested || []) {
    const stat = stats[item.question];
    const teamIndex = index.get(item.number);
    if (item.status !== "accepted" || !stat?.completed || teamIndex === undefined || stat.counts.has(teamIndex)) continue;
    stat.counts.set(teamIndex, 1);
    stat.validCount = stat.counts.size;
  }
  return stats;
}

// === contested answers ===

export function contestedAt(state: ODState, teamIndex: number, qIndex: number): ODContested | undefined {
  const number = teamNumber(state, teamIndex);
  return number ? (state.contested || []).find((item) => item.question === qIndex && item.number === number) : undefined;
}

// pendingCount is how many of the team's contested answers on questions [from, to) wait
// for the jury, on questions it has not taken anyway.
export function pendingCount(state: ODState, teamIndex: number, from = 0, to = Infinity): number {
  const number = teamNumber(state, teamIndex);
  if (!number) return 0;
  return (state.contested || []).filter((item) => item.number === number && item.status === "pending" &&
    item.question >= from && item.question < to && !state.entries[item.question]?.includes(number)).length;
}

// withPending is a score as it reads while contested answers wait on the jury: "10+2".
export function withPending(value: number | string, pending: number): string {
  return pending > 0 ? `${value}+${pending}` : String(value);
}

// ContestedGroup is one answer to one question, as the jury rules on it: every
// team that gave it and the ruling they share.
export interface ContestedGroup {
  question: number;
  answer: string;
  numbers: number[];
  status: ContestedStatus;
}

// contestedKey is what makes two answers one for a ruling: case and spacing
// aside. The server's contested.Key folds them the same way.
export function contestedKey(answer: string): string {
  return answer.trim().split(/\s+/).join(" ").toLowerCase();
}

export function groupContested(list: readonly ContestedText[]): ContestedGroup[] {
  const groups = new Map<string, ContestedGroup>();
  for (const item of list) {
    const key = `${item.question}\u0000${contestedKey(item.answer)}`;
    const group = groups.get(key);
    if (group) group.numbers.push(item.number);
    else groups.set(key, {question: item.question, answer: item.answer, numbers: [item.number], status: item.status});
  }
  return [...groups.values()].sort((a, b) => a.question - b.question || a.answer.localeCompare(b.answer, "ru"));
}

export function teamTookQuestion(stats: QuestionStat[], teamIndex: number, qIndex: number): boolean {
  return Boolean(stats[qIndex]?.counts.has(teamIndex));
}

export function countValidEntries(stats: QuestionStat[], qIndex: number): number {
  return stats[qIndex]?.validCount || 0;
}

export function sumRow(stats: QuestionStat[], teamIndex: number): number {
  let s = 0;
  for (let q = 0; q < stats.length; q++) {
    if (teamTookQuestion(stats, teamIndex, q)) s++;
  }
  return s;
}

export function tourSumsForTeam(stats: QuestionStat[], teamIndex: number, tourLengths: number[]): number[] {
  const out: number[] = [];
  let qi = 0;
  for (const size of tourLengths) {
    let s = 0;
    for (let i = 0; i < size; i++) {
      if (teamTookQuestion(stats, teamIndex, qi)) s++;
      qi++;
    }
    out.push(s);
  }
  return out;
}

// ratingForTeam is the ChGK rating: a question taken by k of n teams is worth
// n − k + 1 to each of them.
export function ratingForTeam(state: ODState, stats: QuestionStat[], teamIndex: number): number {
  const teamCount = state.teams.length;
  let r = 0;
  for (let q = 0; q < stats.length; q++) {
    if (!teamTookQuestion(stats, teamIndex, q)) continue;
    r += teamCount - countValidEntries(stats, q) + 1;
  }
  return r;
}

export function shootoutQuestionCompleted(state: ODState, roundIndex: number, questionIndex: number): boolean {
  return Boolean(state.shootoutRounds[roundIndex]?.completed?.[questionIndex]);
}

export function shootoutRoundTotalForTeam(state: ODState, teamIndex: number, roundIndex: number): number | null {
  const number = teamNumber(state, teamIndex);
  if (!number) return null;
  const round = state.shootoutRounds[roundIndex];
  if (!round) return null;
  const participantIndex = round.teams.indexOf(number);
  if (participantIndex < 0) return null;
  let total = 0;
  for (let questionIndex = 0; questionIndex < (round.answers || []).length; questionIndex++) {
    if (!shootoutQuestionCompleted(state, roundIndex, questionIndex)) continue;
    if (normalizeShootoutMark(round.answers[questionIndex]?.[participantIndex]) === "right") total++;
  }
  return total;
}

// shootoutTiebreakForTeam is the per-round shootout score, for lexicographic
// comparison; −1 marks a round the team did not play, so a team that left
// early is not overtaken by one that kept accumulating.
export function shootoutTiebreakForTeam(state: ODState, teamIndex: number): number[] {
  const result: number[] = [];
  for (let roundIndex = 0; roundIndex < state.shootoutRounds.length; roundIndex++) {
    const roundTotal = shootoutRoundTotalForTeam(state, teamIndex, roundIndex);
    result.push(roundTotal != null ? roundTotal : -1);
  }
  return result;
}

export function compareShootoutTiebreaks(a: number[], b: number[]): number {
  const len = Math.max(a.length, b.length);
  for (let i = 0; i < len; i++) {
    const av = a[i] ?? -1;
    const bv = b[i] ?? -1;
    if (av !== bv) return bv - av;
  }
  return 0;
}

// rankedTeamOrder sorts team rows into standings order: by game total, then
// the shootout tiebreak, then the row as a stable fallback. The results sheet and
// screen board share it so their rows agree; the place labels come from
// placesFor. members narrows it to a Division's rows (ADR-0020); undefined is
// the whole field.
export function rankedTeamOrder(state: ODState, totals: number[], tiebreaks: number[][], members?: readonly number[]): RankKey[] {
  return (members ? members.slice() : state.teams.map((_, index) => index))
    .map((index) => ({index, total: totals[index], tiebreak: tiebreaks[index]}))
    .sort((a, b) => {
      if (b.total !== a.total) return b.total - a.total;
      const cmp = compareShootoutTiebreaks(a.tiebreak, b.tiebreak);
      if (cmp !== 0) return cmp;
      return a.index - b.index;
    });
}

export function anyShootoutMarked(state: ODState): boolean {
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

export function anyQuestionCompleted(stats: QuestionStat[]): boolean {
  for (const stat of stats) if (stat.completed) return true;
  return false;
}

// placesFor ranks on game total, breaking ties on the shootout. Before any
// question or shootout is marked there is nothing to rank and the places stay
// blank. Given a Division's members it deals places afresh among those rows
// alone and leaves every other row blank — a Division is ranked within itself
// (ADR-0020).
export function placesFor(state: ODState, stats: QuestionStat[], totals: number[], members?: readonly number[]): string[] {
  const places = new Array<string>(totals.length).fill("");
  if (!anyQuestionCompleted(stats) && !anyShootoutMarked(state)) return places;
  const rows = members ? members.slice() : state.teams.map((_, index) => index);
  const dealt = computePlaces(rows.map((index) => totals[index]), {
    tiebreaks: rows.map((index) => shootoutTiebreakForTeam(state, index)),
    compareTiebreak: (a, b) => compareShootoutTiebreaks(a as number[], b as number[]),
  });
  rows.forEach((index, rank) => {
    places[index] = dealt[rank];
  });
  return places;
}

// rows is the whole standings sheet from one document: totals, tour sums,
// ratings, places and the row order — narrowed to a Division's members when
// given. The rating stays reckoned over the whole field: everyone played the
// question, whatever Division they are in.
export interface ODRow {
  index: number;
  total: number;
  tourSums: number[];
  rating: number;
  place: string;
}

export function rows(state: ODState, tourLengths: number[], members?: readonly number[]): ODRow[] {
  const total = tourLengths.reduce((acc, n) => acc + n, 0);
  const stats = questionStats(state, total);
  const totals = state.teams.map((_, i) => sumRow(stats, i));
  const tiebreaks = state.teams.map((_, i) => shootoutTiebreakForTeam(state, i));
  const places = placesFor(state, stats, totals, members);
  return rankedTeamOrder(state, totals, tiebreaks, members).map(({index}) => ({
    index,
    total: totals[index],
    tourSums: tourSumsForTeam(stats, index, tourLengths),
    rating: ratingForTeam(state, stats, index),
    place: places[index],
  }));
}
