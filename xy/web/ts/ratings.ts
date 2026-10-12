// ratings.ts — TrueDL estimated from a tour's tests (pecheny.me/blog/truedl).
// Each Session is a team: its lineup's technical rating in the latest release
// of rating.chgk.gg's "b" model gives the place it would hold among the teams,
// the place gives the band coefficient, and what it took scales to what a team
// from places 251-500 would have taken. The release comes whole from
// /api/ratings and names are matched here, so no tester's name leaves the
// browser.

import type { Tester } from "./sessions.js";

export interface RatingsRelease {
  id: number;
  date: string;
  q: number;
  players: Array<[number, string, string, string, number]>; // id, surname, name, patronymic, rating
  teams: Array<[number, number]>; // id, rating; best first
}

export interface RatedPlayer { id: number; surname: string; name: string; patronymic: string; rating: number }

// The upstream table: the best place of each band and its coefficient.
const BANDS: ReadonlyArray<readonly [number, number]> = [
  [10, 1.61], [25, 1.52], [50, 1.43], [100, 1.32], [250, 1.16],
  [500, 1.0], [1000, 0.81], [2000, 0.6], [3000, 0.43], [5000, 0.31],
];
const LINEUP = 6;
const DL_SCALE = 10;

export function coeffFor(place: number): number | null {
  const band = BANDS.find(([upTo]) => place <= upTo);
  return band && place < BANDS[BANDS.length - 1][0] ? band[1] : null;
}

// The best six, weighted 6/6 … 1/6, as the rating counts a lineup.
export function techRating(ratings: readonly number[], q: number): number {
  const best = [...ratings].sort((a, b) => b - a).slice(0, LINEUP);
  const sum = best.reduce((s, r, k) => s + r * (LINEUP - k) / LINEUP, 0);
  return roundHalfEven(sum) * q;
}

function roundHalfEven(x: number): number {
  const r = Math.round(x);
  return Math.abs(x % 1) === 0.5 && r % 2 !== 0 ? r - 1 : r;
}

// The place a team of this rating would hold: one past every team rated higher.
export function placeFor(rating: number, rel: RatingsRelease): number {
  let lo = 0, hi = rel.teams.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (rel.teams[mid][1] > rating) lo = mid + 1;
    else hi = mid;
  }
  return lo + 1;
}

export function sessionDL(taken: number, played: number, place: number): number | null {
  const c = coeffFor(place);
  if (c == null || played <= 0) return null;
  return DL_SCALE * (1 - Math.min(taken / c, played) / played);
}

const fold = (s: string): string => s.toLowerCase().replace(/\u0451/g, "\u0435").trim();

const indexes = new WeakMap<RatingsRelease, Map<string, RatedPlayer[]>>();

function bySurname(rel: RatingsRelease): Map<string, RatedPlayer[]> {
  let idx = indexes.get(rel);
  if (idx) return idx;
  idx = new Map();
  for (const [id, surname, name, patronymic, rating] of rel.players) {
    const p = { id, surname, name, patronymic, rating };
    const key = fold(surname);
    const list = idx.get(key);
    if (list) list.push(p);
    else idx.set(key, [p]);
  }
  indexes.set(rel, idx);
  return idx;
}

// The rated players a typed name can mean: surname and given name both in it,
// in either order, and nothing in it but those and the patronymic.
export function candidates(rel: RatingsRelease, text: string): RatedPlayer[] {
  const words = fold(text).split(/\s+/).filter(Boolean);
  if (words.length < 2) return [];
  const idx = bySurname(rel);
  const seen = new Set<number>();
  const out: RatedPlayer[] = [];
  for (const w of words) {
    for (const p of idx.get(w) || []) {
      const parts = [fold(p.surname), fold(p.name), fold(p.patronymic)];
      if (seen.has(p.id) || !words.includes(parts[1]) || !words.every((x) => parts.includes(x))) continue;
      seen.add(p.id);
      out.push(p);
    }
  }
  return out.sort((a, b) => b.rating - a.rating);
}

export interface Lineup { rating: number; unknown: Tester[] }

// A team tester with an id stands for the whole Session at its own rating;
// otherwise the players make the lineup. A player is the id it carries, or the
// one rated player the name can mean; anyone else counts as unrated.
export function lineupOf(testers: readonly Tester[], rel: RatingsRelease): Lineup {
  const teamRating = new Map(rel.teams);
  const playerRating = new Map(rel.players.map((p) => [p[0], p[4]]));
  const unknown: Tester[] = [];
  const teams = testers.filter((t) => t.type === "team" && t.id != null && teamRating.has(t.id));
  if (teams.length) return { rating: Math.max(...teams.map((t) => teamRating.get(t.id as number) as number)), unknown };
  const ratings: number[] = [];
  for (const t of testers) {
    if (t.type !== "player") continue;
    if (t.id != null) {
      ratings.push(playerRating.get(t.id) || 0);
      continue;
    }
    const c = candidates(rel, t.text);
    if (c.length === 1) ratings.push(c[0].rating);
    else unknown.push(t);
  }
  return { rating: techRating(ratings, rel.q), unknown };
}

// What one Session did on a tour: how many of its questions it played and took.
export interface SessionResult { taken: number; played: number; testers: Tester[] }
export interface SessionDL extends Lineup { place: number; dl: number | null }

export function sessionDLs(results: readonly SessionResult[], rel: RatingsRelease): SessionDL[] {
  return results.map((r) => {
    const lineup = lineupOf(r.testers, rel);
    const place = placeFor(lineup.rating, rel);
    return { ...lineup, place, dl: sessionDL(r.taken, r.played, place) };
  });
}

// The tour's TrueDL: the mean over the Sessions that could be placed.
export function meanDL(sessions: readonly SessionDL[]): number | null {
  const dls = sessions.map((s) => s.dl).filter((d): d is number => d != null);
  return dls.length ? dls.reduce((a, b) => a + b, 0) / dls.length : null;
}

let loading: Promise<RatingsRelease | null> | null = null;

// The release, fetched once per page; null when the server has none.
export function loadRatings(): Promise<RatingsRelease | null> {
  loading ??= fetch("/api/ratings", { credentials: "same-origin" })
    .then((r) => (r.ok ? (r.json() as Promise<RatingsRelease>) : null))
    .catch(() => null)
    .then((rel) => {
      if (!rel) loading = null;
      return rel;
    });
  return loading;
}
