// The brain page's «Индивидуальная статистика»: who buzzed what, folded from
// the бои's marks. This is the one thing the page still computes itself — the
// buzzes are in the бой blobs and nowhere else server-side; every ranking
// comes from the server's tables.

// One бой flattened for the stats fold: the two team names, how many of its
// rows are regular questions, and per row the two sides' (player, mark).
export interface StatsBout {
  teams: [string, string] | string[];
  regular: number;
  rows: Array<Array<{player?: string; mark?: string}>>;
}

export interface BrainPlayerStatsRow {
  player: string;
  team: string;
  attempts: number;
  right: number;
  wrong: number;
}

// computeBrainPlayerStats aggregates who buzzed what across every бой — the
// sheet's «Индивидуальная статистика»: попытки, верно, неверно per (player,
// team). Regular questions only: перестрелки stay out, as the sheet leaves
// them out. Sorted by верно, then попытки, then name.
export function computeBrainPlayerStats(bouts: StatsBout[]): BrainPlayerStatsRow[] {
  const acc = new Map<string, BrainPlayerStatsRow>();
  for (const bout of bouts) {
    bout.rows.slice(0, bout.regular).forEach((row) => {
      row.forEach((cell, side) => {
        const player = (cell?.player || "").trim();
        const mark = cell?.mark || "";
        if (!player || (mark !== "right" && mark !== "wrong")) return;
        const team = bout.teams[side] || "";
        const key = `${player}\x1f${team}`;
        let entry = acc.get(key);
        if (!entry) {
          entry = {player, team, attempts: 0, right: 0, wrong: 0};
          acc.set(key, entry);
        }
        entry.attempts++;
        if (mark === "right") entry.right++;
        else entry.wrong++;
      });
    });
  }
  return Array.from(acc.values()).sort((a, b) =>
    b.right - a.right || b.attempts - a.attempts || a.player.localeCompare(b.player, "ru"));
}
