// kd-protocol.ts — the friendship cup's arithmetic (CONTEXT.md, Kubok
// Druzhby; ADR-0026): which table a route card sends its holder to in a tour,
// and the personal standings a player earns from the tables on his card. Pure
// (ADR-0018): the page and a test call it with the document they hold. The
// server ranks the same way in games.ComputeKDResults.
import {computePlaces} from "./score-table.js";
import {questionStats, tourSumsForTeam, numberIndex} from "./od-protocol.js";
import type {ODState} from "./od-protocol.js";

export interface KDPlayer {
  card: number;
  name: string;
  team?: string;
}

export interface KDState extends ODState {
  players?: KDPlayer[];
}

export interface KDRow {
  player: KDPlayer;
  place: string;
  total: number;
  // tables[t] is the table the card seats him at in tour t, tours[t] what
  // that table took there.
  tables: number[];
  tours: number[];
  // best[k] counts his tours where the table took all but k questions.
  best: number[];
}

// TIEBREAKS is how many "all but k questions" counts break a tie on the sum:
// a full tour, one short, two short (regulations: 4, 3, 2 of 4).
export const TIEBREAKS = 3;

// kdTable is the table card c sends its holder to in tour t (both from 1)
// when n tables play. Cards 1…n stay at their table all game.
export function kdTable(card: number, tour: number, n: number): number {
  if (!(card >= 1) || !(tour >= 1) || !(n >= 1)) return 0;
  const i = card - 1;
  return ((i % n) + Math.floor(i / n) * (tour - 1)) % n + 1;
}

export function isPrime(n: number): boolean {
  if (!Number.isInteger(n) || n < 2) return false;
  for (let d = 2; d * d <= n; d++) if (n % d === 0) return false;
  return true;
}

// toursStarted marks the tours with at least one completed question: the
// standings show a dot for the rest rather than a zero nobody earned yet.
export function toursStarted(state: KDState, tourLengths: number[]): boolean[] {
  let q = 0;
  return tourLengths.map((size) => {
    let any = false;
    for (let i = 0; i < size; i++, q++) any ||= Boolean(state.completed?.[q]);
    return any;
  });
}

// players is the document's player list, cleaned: cards are positive whole
// numbers, names trimmed.
export function players(state: KDState): KDPlayer[] {
  return (Array.isArray(state.players) ? state.players : [])
    .map((p) => ({card: Math.trunc(Number(p?.card) || 0), name: String(p?.name || "").trim(), team: String(p?.team || "").trim()}))
    .filter((p) => p.card > 0);
}

// nextFreeCard is the lowest card number nobody holds — what the next player
// to register draws.
export function nextFreeCard(list: readonly KDPlayer[]): number {
  const held = new Set(list.map((p) => p.card));
  let card = 1;
  while (held.has(card)) card++;
  return card;
}

function compareBest(a: number[], b: number[]): number {
  for (let k = 0; k < TIEBREAKS; k++) {
    const d = (b[k] || 0) - (a[k] || 0);
    if (d !== 0) return d;
  }
  return 0;
}

// standings ranks the players by the sum of their tables' tours, then by the
// tours taken in full, one short, two short; equal on all of it they share a
// place. Before any question is completed the places stay blank.
export function standings(state: KDState, tourLengths: number[], n: number): KDRow[] {
  const total = tourLengths.reduce((acc, size) => acc + size, 0);
  const index = numberIndex(state);
  const stats = questionStats(state, total, index);
  const byTable = new Map<number, number[]>();
  for (const [number, row] of index) byTable.set(number, tourSumsForTeam(stats, row, tourLengths));
  const started = stats.some((stat) => stat.completed);
  const rows: KDRow[] = players(state).map((player) => {
    const tables = tourLengths.map((_, t) => kdTable(player.card, t + 1, n));
    const tours = tables.map((table, t) => byTable.get(table)?.[t] || 0);
    const best = new Array<number>(TIEBREAKS).fill(0);
    tours.forEach((took, t) => {
      const short = tourLengths[t] - took;
      if (short >= 0 && short < TIEBREAKS) best[short]++;
    });
    return {player, place: "", total: tours.reduce((acc, v) => acc + v, 0), tables, tours, best};
  });
  const places = computePlaces(rows.map((row) => row.total), {
    tiebreaks: rows.map((row) => row.best),
    compareTiebreak: (a, b) => compareBest(a as number[], b as number[]),
  });
  rows.forEach((row, i) => {
    row.place = started ? places[i] : "";
  });
  return rows
    .map((row, i) => ({row, i}))
    .sort((a, b) => b.row.total - a.row.total || compareBest(a.row.best, b.row.best) || a.row.player.card - b.row.player.card || a.i - b.i)
    .map(({row}) => row);
}
