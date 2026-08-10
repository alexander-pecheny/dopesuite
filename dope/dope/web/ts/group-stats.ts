// The client mirror of a Block's per-бой scoring rule (ADR-0008), for the
// «Групповой этап» tab: the source sheets show a player's очки split by круг,
// and the split exists nowhere server-side — only the бои do. Kept pure so the
// deno suite pins it.

// evalScoringRule evaluates an arithmetic expression — numbers, named
// variables, + - * / and parens — over a бой's outcome. Anything it cannot
// read is 0: a broken rule must not take the whole tab down.
export function evalScoringRule(expr: string, vars: Record<string, number>): number {
  const tokens = String(expr).match(/\d+(?:\.\d+)?|[a-zа-яё_][a-za-яё0-9_]*|[-+*/()]/gi) || [];
  let pos = 0;
  const peek = () => tokens[pos];
  const take = () => tokens[pos++];
  function primary(): number {
    const token = take();
    if (token === "(") {
      const value = sum();
      if (take() !== ")") throw new Error("скобка");
      return value;
    }
    if (token === "-") return -primary();
    if (token === undefined) throw new Error("обрыв");
    if (/^\d/.test(token)) return Number(token);
    const value = vars[token];
    if (value === undefined) throw new Error(`нет ${token}`);
    return value;
  }
  function product(): number {
    let value = primary();
    while (peek() === "*" || peek() === "/") {
      value = take() === "*" ? value * primary() : value / primary();
    }
    return value;
  }
  function sum(): number {
    let value = product();
    while (peek() === "+" || peek() === "-") {
      value = take() === "+" ? value + product() : value - product();
    }
    return value;
  }
  try {
    const value = sum();
    if (pos !== tokens.length || !Number.isFinite(value)) return 0;
    return value;
  } catch {
    return 0;
  }
}

export interface GroupRoundsMatch {
  round?: number;
  finished?: boolean;
  participants?: Array<{name?: string; place?: number} | null> | null;
}

export interface GroupRoundsRow {
  name: string;
  points: number;
  rounds: number[];
}

// computeGroupRounds folds one группа's бои into очки per круг per player —
// finished бои only, the way standings count them — sorted by очки.
export function computeGroupRounds(opts: {
  matches: GroupRoundsMatch[];
  pointsRule?: string;
  roundCount: number;
}): GroupRoundsRow[] {
  const rule = opts.pointsRule || "seats + 1 - place";
  const rows = new Map<string, GroupRoundsRow>();
  for (const match of opts.matches) {
    const seats = (match.participants || []).length;
    for (const seat of match.participants || []) {
      const name = (seat?.name || "").trim();
      if (!name) continue;
      let row = rows.get(name);
      if (!row) {
        row = {name, points: 0, rounds: new Array<number>(opts.roundCount).fill(0)};
        rows.set(name, row);
      }
      if (!match.finished || !seat?.place) continue;
      const points = evalScoringRule(rule, {seats, place: seat.place});
      row.points += points;
      const round = Number(match.round || 1) - 1;
      if (round >= 0 && round < row.rounds.length) row.rounds[round] += points;
    }
  }
  return Array.from(rows.values()).sort((a, b) =>
    b.points - a.points || a.name.localeCompare(b.name, "ru"));
}
