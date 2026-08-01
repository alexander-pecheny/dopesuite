// Group-ranking kernel for the брейн crosstable — the client mirror of
// rr.Standings (domain/structure/rr.go): each key partitions the still-tied
// group, "h2h" (личная встреча) is the points the tied teams took against each
// other, and keys are consumed in order, never re-applied to later sub-ties.
// Kept as a pure module so the deno suite pins it to the Go worked examples.

export interface RankTeam {
  points: number;
  taken: number;
  conceded: number;
}

// One finished бой's point split, by team index.
export interface RankDuel {
  a: number;
  b: number;
  pa: number;
  pb: number;
}

export const CANON_ORDER = ["points", "h2h", "taken", "diff"];

// rankGroup returns the 1-based place per team index, ties sharing a place.
export function rankGroup(teams: RankTeam[], duels: RankDuel[], order: string[] = CANON_ORDER): number[] {
  const seats = teams.map((_, i) => i);
  const tieGroup = new Array<number>(teams.length).fill(0);
  let groupSeq = 0;

  const arrange = (ids: number[], keyIdx: number): number[] => {
    if (ids.length < 2 || keyIdx >= order.length) {
      groupSeq++;
      for (const i of ids) tieGroup[i] = groupSeq;
      return ids;
    }
    const key = order[keyIdx];
    let val = (i: number): number => {
      if (key === "taken") return teams[i].taken;
      if (key === "diff") return teams[i].taken - teams[i].conceded;
      if (key === "points") return teams[i].points;
      return 0;
    };
    if (key === "h2h") {
      const tied = new Set(ids);
      const sub = new Map<number, number>();
      for (const d of duels) {
        if (tied.has(d.a) && tied.has(d.b)) {
          sub.set(d.a, (sub.get(d.a) || 0) + d.pa);
          sub.set(d.b, (sub.get(d.b) || 0) + d.pb);
        }
      }
      val = (i) => sub.get(i) || 0;
    }
    const sorted = ids.map((id, appearance) => ({id, appearance}))
      .sort((x, y) => val(y.id) - val(x.id) || x.appearance - y.appearance)
      .map((e) => e.id);
    const out: number[] = [];
    let start = 0;
    for (let end = 1; end <= sorted.length; end++) {
      if (end === sorted.length || val(sorted[end]) !== val(sorted[start])) {
        out.push(...arrange(sorted.slice(start, end), keyIdx + 1));
        start = end;
      }
    }
    return out;
  };

  const finalOrder = arrange(seats, 0);
  const ranks = new Array<number>(teams.length).fill(0);
  finalOrder.forEach((teamIdx, pos) => {
    const prev = finalOrder[pos - 1];
    ranks[teamIdx] = pos > 0 && tieGroup[teamIdx] === tieGroup[prev] ? ranks[prev] : pos + 1;
  });
  return ranks;
}
