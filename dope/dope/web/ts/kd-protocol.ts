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

// KDSeat is what the document holds under a card's key.
export interface KDSeat {
  name: string;
  team?: string;
}

// The players are keyed by card, {"<card>": {name, team}}, so registering one
// is a patch of its own key and two hosts at the desk never overwrite each
// other; taking one off writes null there. A document written before that
// holds a list, [{card, name, team}], and still reads.
export type KDPlayers = Record<string, KDSeat | null> | KDPlayer[];

export interface KDState extends ODState {
  players?: KDPlayers;
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

// maxCard is the highest card n tables tell apart: card c and card c+n² follow
// the same route, so a card past n² would repeat one already dealt.
export function maxCard(n: number): number {
  return n * n;
}

// isJoker says card c never leaves its table: its route moves ⌊(c−1)/n⌋
// tables a tour, which is no move when that is a multiple of n.
export function isJoker(card: number, n: number): boolean {
  return card >= 1 && n >= 1 && Math.floor((card - 1) / n) % n === 0;
}

const CARD_KEY = /^[1-9][0-9]*$/;

// players is the document's player list in card order, cleaned the way the
// server's games.KDPlayers cleans it: an entry whose card is not a whole
// number from 1 (or past maxCard(n), when n is given), a second holder of a
// card and a nameless entry are left out.
export function players(state: KDState, n = 0): KDPlayer[] {
  const raw = state.players;
  const entries: Array<{card: unknown; seat: unknown}> = [];
  if (Array.isArray(raw)) {
    for (const p of raw) entries.push({card: p?.card, seat: p});
  } else if (raw && typeof raw === "object") {
    for (const [key, seat] of Object.entries(raw)) entries.push({card: CARD_KEY.test(key) ? Number(key) : NaN, seat});
  }
  const seen = new Set<number>();
  const out: KDPlayer[] = [];
  for (const {card, seat} of entries) {
    if (!seat || typeof seat !== "object") continue;
    if (typeof card !== "number" || !Number.isInteger(card) || card < 1) continue;
    if (n > 0 && card > maxCard(n)) continue;
    const s = seat as {name?: unknown; team?: unknown};
    const name = typeof s.name === "string" ? s.name.trim() : "";
    if (!name || seen.has(card)) continue;
    seen.add(card);
    out.push({card, name, team: typeof s.team === "string" ? s.team.trim() : ""});
  }
  return out.sort((a, b) => a.card - b.card);
}

// keyedPlayers is the list as the keyed document holds it, for a document
// still in the old list shape: the page writes it once, whole, before it
// patches a single card.
export function keyedPlayers(list: readonly KDPlayer[]): Record<string, KDSeat> {
  const out: Record<string, KDSeat> = {};
  for (const p of list) out[String(p.card)] = {name: p.name, team: p.team || ""};
  return out;
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
  const rows: KDRow[] = players(state, n).map((player) => {
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
