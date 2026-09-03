// EK stats: per-player and per-participant aggregates folded from the
// stages' marks (the sibling of brain-stats and group-stats), and their tables.

import type {MatchView} from "./score-table.js";
import {standingsTable} from "./standings.js";
import S from "./i18nstrings.js";

export interface EKStage {
  code?: string;
  matches?: MatchView[];
}

export interface EKPlayerStatsRow {
  player: string;
  team: string;
  sum: number;
  plus: number;
  battles: number;
  right: number[];
  wrong: number[];
  rightTotal: number;
  share: number;
}

// computeEKPlayerStats aggregates per-player individual stats across every
// battle of an EK game. `stages` is the payload from /stages/matches:
// [{code, matches: [MatchView, ...]}, ...]. Only regular themes are counted —
// shootout themes are a tiebreaker and are excluded, matching
// the Σ+ semantics shown in a battle (ParticipantView.plus ignores shootouts too).
//
// Players are keyed by (team, player) so namesakes on different teams stay
// separate. The team-share column (% of team) is a positive player's
// share among the team's positive contributors (denominator = sum of only the
// positive players' Σ); players with Σ <= 0 are 0 (see the share loop below).
// Returns rows sorted by Σ descending (then Σ+, then name).
export function computeEKPlayerStats(stages: EKStage[] | null | undefined): EKPlayerStatsRow[] {
  const values = [10, 20, 30, 40, 50]; // answer index → nominal value
  const players = new Map<string, EKPlayerStatsRow>();   // key → stat row
  const battleSeen = new Map<string, Set<string>>(); // key → Set of battle ids (for the matches count)
  for (const stage of stages || []) {
    for (const match of stage.matches || []) {
      const battleID = `${stage.code || ""}\x1f${match.code || ""}`;
      for (const team of match.participants || []) {
        const teamName = team.name || "";
        for (const theme of team.themes || []) {
          const playerName = String(theme.player || "").trim();
          if (!playerName) continue;
          const key = `${teamName}\x1f${playerName}`;
          let row = players.get(key);
          let seen = battleSeen.get(key);
          if (!row || !seen) {
            row = {
              player: playerName,
              team: teamName,
              sum: 0,
              plus: 0,
              battles: 0,
              right: [0, 0, 0, 0, 0],
              wrong: [0, 0, 0, 0, 0],
              rightTotal: 0,
              share: 0,
            };
            players.set(key, row);
            seen = new Set();
            battleSeen.set(key, seen);
          }
          if (!seen.has(battleID)) {
            seen.add(battleID);
            row.battles++;
          }
          const statRow = row;
          (theme.answers || []).forEach((mark, i) => {
            const value = values[i] || 0;
            if (mark === "right") {
              statRow.sum += value;
              statRow.plus += value;
              statRow.right[i]++;
              statRow.rightTotal++;
            } else if (mark === "wrong") {
              statRow.sum -= value;
              statRow.wrong[i]++;
            }
          });
        }
      }
    }
  }
  const rows = Array.from(players.values());
  // "% of team": a positive player's share among the team's POSITIVE
  // contributors. A player with Σ <= 0 is 0 (they didn't help), and the
  // denominator is the sum of only the positive players' Σ — so the team's
  // positive players' shares add up to 100%, independent of how negative the
  // rest of the team went.
  const teamPositiveSum = new Map<string, number>();
  for (const row of rows) {
    if (row.sum > 0) teamPositiveSum.set(row.team, (teamPositiveSum.get(row.team) || 0) + row.sum);
  }
  for (const row of rows) {
    const total = teamPositiveSum.get(row.team) || 0;
    row.share = row.sum > 0 && total > 0 ? row.sum / total : 0;
  }
  rows.sort((a, b) =>
    b.sum - a.sum ||
    b.plus - a.plus ||
    a.player.localeCompare(b.player, "ru"));
  return rows;
}

export interface IndividualStatsRow {
  player: string;
  sum: number;
  plus: number;
  battles: number;
  right: number[];
}

// computeIndividualPlayerStats aggregates a personal game per participant —
// the participant is the player, so there is no per-theme player to read.
// Σ, Σ+ (positive points), matches, and the taken counts per value; regular themes
// only, finished matches only — a seeded but unplayed match is not a match played —
// sorted by Σ.
export function computeIndividualPlayerStats(stages: EKStage[] | null | undefined): IndividualStatsRow[] {
  const values = [10, 20, 30, 40, 50];
  const players = new Map<string, IndividualStatsRow>();
  for (const stage of stages || []) {
    for (const match of stage.matches || []) {
      if (!match.finished) continue;
      for (const seat of match.participants || []) {
        const name = (seat.name || "").trim();
        if (!name) continue;
        let row = players.get(name);
        if (!row) {
          row = {player: name, sum: 0, plus: 0, battles: 0, right: [0, 0, 0, 0, 0]};
          players.set(name, row);
        }
        row.battles++;
        for (const theme of seat.themes || []) {
          (theme.answers || []).forEach((mark, i) => {
            if (mark === "right") {
              row.sum += values[i] || 0;
              row.plus += values[i] || 0;
              row.right[i]++;
            } else if (mark === "wrong") {
              row.sum -= values[i] || 0;
            }
          });
        }
      }
    }
  }
  const rows = Array.from(players.values());
  rows.sort((a, b) => b.sum - a.sum || b.plus - a.plus || a.player.localeCompare(b.player, "ru"));
  return rows;
}

// The EK nominals, high to low — the order the stats tables list them; the
// stat rows count them by answer position, 10 first.
const EK_VALUES = [50, 40, 30, 20, 10];
const byNominal = (counts: number[]) => EK_VALUES.map((value) => counts[value / 10 - 1] || 0);

// buildIndividualStatsTable renders computeIndividualPlayerStats rows — the
// source's stats columns: player, score, Σ+, matches, takens per value.
export function buildIndividualStatsTable(rows: IndividualStatsRow[] | null | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (!rows || rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "roster-empty";
    empty.textContent = S.ek.stats.individualEmpty();
    wrapper.appendChild(empty);
    return wrapper;
  }
  wrapper.appendChild(standingsTable({
    className: "ek-stats-table",
    columns: [
      {label: S.ek.stats.player(), kind: "name", className: "ek-stats-name ek-stats-player"},
      {label: "Σ", kind: "num", className: "ek-stats-sum"},
      {label: "Σ+", kind: "num"},
      {label: S.ek.stats.battles(), kind: "num"},
      ...EK_VALUES.map((value) => ({label: `+${value}`, kind: "num" as const, className: "narrow"})),
    ],
    rows: rows.map((row) => [row.player, row.sum, row.plus, row.battles, ...byNominal(row.right)]),
  }));
  return wrapper;
}

// buildEKStatsTable renders the rows from computeEKPlayerStats into the
// stats table. Columns: player, team, Σ, Σ+, matches, 50/40/30/20/10
// (correct counts, descending nominal), −50…−10 (wrong counts, shown as a
// plain positive count), and the team-share percentage. Counts are always
// shown (including 0). Name cells reuse the results-team truncate+fade+popover
// structure so long names behave like everywhere else. Shared host/viewer.
export function buildEKStatsTable(rows: EKPlayerStatsRow[] | null | undefined): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "results-wrapper ek-stats-wrapper";
  if (!rows || rows.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = S.ek.stats.empty();
    wrapper.appendChild(empty);
    return wrapper;
  }

  wrapper.appendChild(standingsTable({
    className: "ek-stats-table",
    columns: [
      {label: S.ek.stats.player(), kind: "name", className: "ek-stats-name ek-stats-player"},
      {label: S.ek.stats.team(), kind: "name", className: "ek-stats-name"},
      {label: "Σ", kind: "num", className: "ek-stats-sum"},
      {label: "Σ+", kind: "num"},
      {label: S.ek.stats.battles(), kind: "num"},
      ...EK_VALUES.map((value) => ({label: value, kind: "num" as const, className: "narrow"})),
      ...EK_VALUES.map((value) => ({label: `-${value}`, kind: "num" as const, className: "narrow ek-stats-wrong"})),
      {label: S.ek.stats.share(), kind: "num", className: "ek-stats-share"},
    ],
    rows: rows.map((row) => [
      row.player,
      row.team,
      row.sum,
      row.plus,
      row.battles,
      ...byNominal(row.right),
      ...byNominal(row.wrong),
      `${Math.round(row.share * 100)}%`,
    ]),
  }));
  return wrapper;
}
