// Hamsa's statistics tab: the same folds EK's tab shows — a player's score,
// the plus column, the bouts he sat and how many questions he took and lost at
// each value — over Hamsa's own document.
//
// It reads the document rather than a projection: Hamsa's state is keyed by
// Participant and names its players by id, so the bout's rosters resolve them.
// The counts are named after the base values, the round's multiplier being a
// fact about the bout and not about the player.

import * as hamsa from "./hamsa-protocol.js";
import type {HamsaState} from "./hamsa-protocol.js";
import type {EKPlayerStatsRow} from "./ek-stats.js";

// HamsaBout is one bout as the statistics read it: its document, and each
// seat's team and roster.
export interface HamsaBout {
  state: HamsaState;
  seats: Array<{id: number; team: string; players: Map<number, string>}>;
}

// computeHamsaPlayerStats folds every bout into a row per (team, player), so
// namesakes on different teams stay apart. A theme nobody was seated for is
// skipped: it belongs to no player.
export function computeHamsaPlayerStats(bouts: HamsaBout[]): EKPlayerStatsRow[] {
  const players = new Map<string, EKPlayerStatsRow>();
  const seen = new Map<string, Set<number>>();
  bouts.forEach((bout, boutIndex) => {
    const values = hamsa.baseValues(bout.state);
    for (const seat of bout.seats) {
      for (let t = 0; t < hamsa.themeCount(bout.state); t++) {
        const playerID = hamsa.playerAt(bout.state, seat.id, t);
        const name = (seat.players.get(playerID) || "").trim();
        if (!name) continue;
        const key = `${seat.team}\x1f${name}`;
        let row = players.get(key);
        let bouts = seen.get(key);
        if (!row || !bouts) {
          row = {
            player: name, team: seat.team, sum: 0, plus: 0, battles: 0,
            right: values.map(() => 0), wrong: values.map(() => 0), rightTotal: 0, share: 0,
          };
          players.set(key, row);
          bouts = new Set<number>();
          seen.set(key, bouts);
        }
        if (!bouts.has(boutIndex)) {
          bouts.add(boutIndex);
          row.battles++;
        }
        const themeValues = hamsa.themeValues(bout.state, t);
        const counted = row;
        hamsa.sectionOf(bout.state, seat.id)?.themes[t]?.answers.forEach((mark, q) => {
          const value = themeValues[q] || 0;
          if (mark === "right") {
            counted.sum += value;
            counted.plus += value;
            counted.right[q]++;
            counted.rightTotal++;
          } else if (mark === "wrong") {
            counted.sum -= value;
            counted.wrong[q]++;
          }
        });
      }
    }
  });
  const rows = Array.from(players.values());
  // The share column is EK's: what a positive player brought of his team's
  // positive points, so a team's positive players add up to a hundred.
  const teamPositive = new Map<string, number>();
  for (const row of rows) {
    if (row.sum > 0) teamPositive.set(row.team, (teamPositive.get(row.team) || 0) + row.sum);
  }
  for (const row of rows) {
    const total = teamPositive.get(row.team) || 0;
    row.share = row.sum > 0 && total > 0 ? row.sum / total : 0;
  }
  rows.sort((a, b) => b.sum - a.sum || b.plus - a.plus || a.player.localeCompare(b.player, "ru"));
  return rows;
}
