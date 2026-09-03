// multi-protocol.ts — the multi games protocol's document as the page reads it
// (ADR-0018): the shape, the minigames a scheme declares, the adapter from the
// server's JSON, and the arithmetic — a minigame's subtotal, total, Σ+ and the
// ranked results with shared places. Pure: every function takes the state and
// the rules it reads.

// A column is one task, and its domain is what its cell may hold. Two or three
// values is a cell you click through; anything wider is one you type into,
// which is what CYCLE_LIMIT draws the line at.
export const CYCLE_LIMIT = 3;

export interface MultiColumn {
  block: number;
  values: number[];
}

export interface MultiMinigame {
  name: string;
  columns: MultiColumn[];
  // A normalised minigame contributes a share of the best, out of a hundred,
  // rather than its own points — so minigames of quite different scales weigh
  // the same in the total.
  normalized: boolean;
}

// What the best result in a normalised minigame is worth.
export const NORMAL_MAX = 100;

export interface MultiScheme {
  gameType?: string;
  title?: string;
  minigames?: Array<{name?: unknown; normalized?: unknown; columns?: Array<{values?: unknown; block?: unknown} | null> | null} | null>;
  sorting?: unknown;
  participants?: string[];
  [key: string]: unknown;
}

export type ParticipantEntry = string | {number?: unknown; name?: unknown} | null | undefined;

export interface MultiState {
  participants: ParticipantEntry[];
  games: Array<{cells: number[][]}>;
  finished: boolean;
  declined: Record<string, boolean>;
  [key: string]: unknown;
}

// MultiRules is what the scheme fixes: which minigames are played, what breaks
// a tie on the total, and whether any task can take points away — the last decides
// whether a Σ+ column is worth drawing at all.
export interface MultiRules {
  minigames: MultiMinigame[];
  sorting: string[];
  signed: boolean;
}

export function rulesOf(scheme: MultiScheme): MultiRules {
  const minigames: MultiMinigame[] = [];
  for (const raw of Array.isArray(scheme.minigames) ? scheme.minigames : []) {
    if (!raw) continue;
    const columns: MultiColumn[] = [];
    for (const column of Array.isArray(raw.columns) ? raw.columns : []) {
      const values = Array.isArray(column?.values)
        ? (column!.values as unknown[]).filter((v): v is number => typeof v === "number" && Number.isFinite(v))
        : [];
      const block = typeof column?.block === "number" && column.block > 0 ? Math.floor(column.block) : 0;
      columns.push({values: values.length ? values : [0], block});
    }
    minigames.push({
      name: typeof raw.name === "string" ? raw.name : "",
      normalized: raw.normalized === true,
      columns,
    });
  }
  const sorting = Array.isArray(scheme.sorting)
    ? (scheme.sorting as unknown[]).filter((s): s is string => typeof s === "string")
    : [];
  const signed = minigames.some((game) => game.columns.some((column) => column.values.some((v) => v < 0)));
  return {minigames, sorting: sorting.length ? sorting : ["total"], signed};
}

export function schemeParticipants(scheme: MultiScheme): string[] {
  return Array.isArray(scheme.participants) ? scheme.participants.slice() : [];
}

// parseState is the adapter from whatever the server stored to a MultiState the
// renderers can trust: one cell grid per minigame, one row per participant,
// each row as wide as the scheme says that minigame is.
export function parseState(raw: unknown, rules: MultiRules, participants: string[]): MultiState {
  const state = (raw && typeof raw === "object" ? raw : {}) as MultiState;
  if (!Array.isArray(state.participants) || state.participants.length === 0) {
    state.participants = participants.slice();
  }
  const grids = Array.isArray(state.games) ? state.games : [];
  state.games = rules.minigames.map((game, index) => {
    const rows = Array.isArray(grids[index]?.cells) ? grids[index]!.cells : [];
    const cells: number[][] = [];
    for (let p = 0; p < state.participants.length; p++) {
      const row: number[] = [];
      for (let c = 0; c < game.columns.length; c++) {
        const value = Array.isArray(rows[p]) ? rows[p][c] : undefined;
        row.push(typeof value === "number" && Number.isFinite(value) ? value : 0);
      }
      cells.push(row);
    }
    return {cells};
  });
  if (typeof state.finished !== "boolean") state.finished = false;
  if (!state.declined || typeof state.declined !== "object" || Array.isArray(state.declined)) {
    state.declined = {};
  }
  return state;
}

// === who is who ===

export function participantName(state: MultiState, index: number): string {
  const entry = state.participants[index];
  if (typeof entry === "string") return entry;
  if (entry && typeof entry === "object" && typeof entry.name === "string") return entry.name;
  return "";
}

export function participantNumber(state: MultiState, index: number): number {
  const entry = state.participants[index];
  if (entry && typeof entry === "object" && typeof entry.number === "number") return entry.number;
  return 0;
}

// declinedKey mirrors games.KSIDeclinedKey: by number when numbered, else by
// lowercased name, so a refusal survives a roster re-import.
export function declinedKey(state: MultiState, index: number): string {
  const number = participantNumber(state, index);
  if (number > 0) return `n${number}`;
  const name = participantName(state, index).trim().toLowerCase();
  return name ? `s${name}` : "";
}

export function participantDeclined(state: MultiState, index: number): boolean {
  const key = declinedKey(state, index);
  return Boolean(key && state.declined[key]);
}

// === the arithmetic ===

export function cellValue(state: MultiState, game: number, participant: number, column: number): number {
  const row = state.games[game]?.cells[participant];
  const value = row ? row[column] : 0;
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export interface ScoreRow {
  // What each minigame contributed to the total: its points, or its share of
  // the best where the minigame is normalised.
  games: number[];
  // What was actually scored in each minigame, before any normalising — the
  // number the cells add up to and the sheet prints under the block.
  raw: number[];
  total: number;
  plus: number;
}

// scoreSheet is every participant's subtotals, total and Σ+ in one pass — what
// the detailed sheet prints and what the ranking reads.
//
// A normalised minigame is scored against the best result in it among the
// teams in the standings: a team that refused to play cannot set the scale for
// everyone else. Below nought is nought — a team that finished a minigame on
// minus scores nothing for it rather than dragging its total down.
export function scoreSheet(state: MultiState, rules: MultiRules): ScoreRow[] {
  const rows = state.participants.map((_, p) => {
    const row: ScoreRow = {games: [], raw: [], total: 0, plus: 0};
    rules.minigames.forEach((game, g) => {
      let subtotal = 0;
      for (let c = 0; c < game.columns.length; c++) {
        const value = cellValue(state, g, p, c);
        subtotal += value;
        if (value > 0) row.plus += value;
      }
      row.raw.push(subtotal);
    });
    return row;
  });
  const best = rules.minigames.map((_, g) => {
    let top = 0;
    rows.forEach((row, p) => {
      if (!participantDeclined(state, p)) top = Math.max(top, row.raw[g]);
    });
    return top;
  });
  for (const row of rows) {
    rules.minigames.forEach((game, g) => {
      const raw = row.raw[g];
      const value = !game.normalized ? raw
        : best[g] > 0 && raw > 0 ? (NORMAL_MAX * raw) / best[g]
        : 0;
      row.games.push(value);
      row.total += value;
    });
  }
  return rows;
}

// formatScore prints a total that may be fractional the way the sheets do: two
// decimals where normalising made it fractional, nothing where it did not.
export function formatScore(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

export interface ResultRow {
  index: number;
  placeText: string;
  name: string;
  games: number[];
  raw: number[];
  total: number;
  plus: number;
}

// metricOf reads one of the names a scheme may rank on: total, plus, or
// game1..gameN by the order the minigames are played.
export function metricOf(row: {games: number[]; total: number; plus: number}, name: string): number {
  if (name === "total") return row.total;
  if (name === "plus") return row.plus;
  const index = Number(name.replace(/^game/, ""));
  if (name.startsWith("game") && Number.isInteger(index) && index >= 1 && index <= row.games.length) {
    return row.games[index - 1];
  }
  return 0;
}

// rankedResultRows is the results table: every team that did not refuse to
// play, ranked by the scheme's comparators — the total alone unless a fest named
// more — with a shared place label ("2–3") where every one of them ties. A
// declined team takes no place and shifts nobody.
export function rankedResultRows(
  state: MultiState,
  rules: MultiRules,
  label: (index: number) => string,
): ResultRow[] {
  const sheet = scoreSheet(state, rules);
  const rows: ResultRow[] = [];
  state.participants.forEach((_, index) => {
    if (participantDeclined(state, index)) return;
    rows.push({index, placeText: "", name: label(index), ...sheet[index]});
  });
  const level = (a: ResultRow, b: ResultRow) => rules.sorting.every((name) => metricOf(a, name) === metricOf(b, name));
  rows.sort((a, b) => {
    for (const name of rules.sorting) {
      const diff = metricOf(b, name) - metricOf(a, name);
      if (diff !== 0) return diff;
    }
    return a.index - b.index;
  });
  let i = 0;
  while (i < rows.length) {
    let j = i;
    while (j + 1 < rows.length && level(rows[i], rows[j + 1])) j++;
    const place = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) rows[k].placeText = place;
    i = j + 1;
  }
  return rows;
}
