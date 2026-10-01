import S from "./i18nstrings.js";

// The client mirror of a Block's per-match scoring rule (ADR-0008), for the
// group-stage tab: the source sheets show a player's points split by block
// round, and the split exists nowhere server-side — only the matches do. Kept
// pure so deno suite pins it.

// evalScoringRule evaluates an arithmetic expression — numbers, named
// variables, + - * / and parens — over a match's outcome. Anything it cannot
// read is 0: a broken rule must not take the whole tab down.
export function evalScoringRule(expr: string, vars: Record<string, number>): number {
  const tokens = String(expr).match(/\d+(?:\.\d+)?|[a-zа-яё_][a-zа-яё0-9_]*|[-+*/()]/gi) || [];
  let pos = 0;
  const peek = () => tokens[pos];
  const take = () => tokens[pos++];
  function primary(): number {
    const token = take();
    if (token === "(") {
      const value = sum();
      if (take() !== ")") throw new Error(S.widgets.groupStats.paren());
      return value;
    }
    if (token === "-") return -primary();
    if (token === undefined) throw new Error(S.widgets.groupStats.truncated());
    if (/^\d/.test(token)) return Number(token);
    const value = vars[token];
    if (value === undefined) throw new Error(S.widgets.groupStats.noToken(token));
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

export interface GroupBlockRoundsSeat {
  // id is the seat's Participant; two players may share a name.
  id?: number;
  name?: string;
  place?: number;
  // The seat's Protocol metrics as the match view carries them. A rule may
  // read them the way the server's does (ADR-0008) — the rules of
  // Octobearfest's personal SI pay (4 - place) + sum/1000.
  total?: number | string | null;
  plus?: number | string | null;
  shootoutTotal?: number | string | null;
  correctCounts?: number[];
}

export interface GroupBlockRoundsMatch {
  // code is the bout's, for a link to it from the player's round.
  code?: string;
  blockRound?: number;
  finished?: boolean;
  questionValues?: unknown[];
  participants?: Array<GroupBlockRoundsSeat | null> | null;
}

// seatMetrics is one seat's Protocol metrics under the names the server gives
// them: total, plus, shootoutTotal, and takenN — the correct answers on the
// questions worth N.
function seatMetrics(seat: GroupBlockRoundsSeat, questionValues: unknown[] | undefined): Record<string, number> {
  const out: Record<string, number> = {};
  for (const key of ["total", "plus", "shootoutTotal"] as const) {
    const value = Number(seat[key]);
    if (seat[key] !== undefined && seat[key] !== null && seat[key] !== "" && Number.isFinite(value)) out[key] = value;
  }
  (seat.correctCounts || []).forEach((count, k) => {
    const value = Number(questionValues?.[k]);
    if (Number.isFinite(value) && value > 0) out[`taken${value}`] = Number(count) || 0;
  });
  return out;
}

// boutScope is the client mirror of the server's (structure/rules.go): the
// seat's place, the table's size, whether it tied, its own metrics, and the
// other seats' — oppN_, and the opp_, opp_max_ and opp_min_ aggregates. A rule
// that reads a name this mirror lacks evaluates to 0, as before.
export function boutScope(match: GroupBlockRoundsMatch, seatIndex: number): Record<string, number> {
  const seats = (match.participants || []);
  const mine = seats[seatIndex] || {};
  const place = Number(mine.place || 0);
  const scope: Record<string, number> = {
    place,
    seats: seats.length,
    finished: match.finished ? 1 : 0,
    tied: seats.filter((other, i) => i !== seatIndex && other && Number(other.place || 0) === place).length,
    ...seatMetrics(mine, match.questionValues),
  };
  const sums: Record<string, number> = {}, maxes: Record<string, number> = {}, mins: Record<string, number> = {};
  let index = 0;
  seats.forEach((other, i) => {
    if (i === seatIndex || !other) return;
    index++;
    scope[`opp${index}_place`] = Number(other.place || 0);
    for (const [key, value] of Object.entries(seatMetrics(other, match.questionValues))) {
      scope[`opp${index}_${key}`] = value;
      sums[key] = (sums[key] || 0) + value;
      maxes[key] = key in maxes ? Math.max(maxes[key], value) : value;
      mins[key] = key in mins ? Math.min(mins[key], value) : value;
    }
  });
  for (const key of Object.keys(sums)) {
    scope[`opp_${key}`] = sums[key];
    scope[`opp_max_${key}`] = maxes[key];
    scope[`opp_min_${key}`] = mins[key];
  }
  return scope;
}

export interface GroupBlockRoundsRow {
  // id is the Participant when the seats named one, 0 otherwise.
  id: number;
  name: string;
  points: number;
  blockRounds: number[];
  // bouts is the bout the player sits in each block round, by its code: ""
  // where the block round seats him nowhere.
  bouts: string[];
}

// computeGroupBlockRounds folds one group's matches into points per block round
// per player — finished matches only, the way standings count them — sorted by
// points.
export function computeGroupBlockRounds(opts: {
  matches: GroupBlockRoundsMatch[];
  pointsRule?: string;
  blockRoundCount: number;
}): GroupBlockRoundsRow[] {
  const rule = opts.pointsRule || "seats + 1 - place";
  const rows = new Map<string, GroupBlockRoundsRow>();
  for (const match of opts.matches) {
    for (const [seatIndex, seat] of (match.participants || []).entries()) {
      const name = (seat?.name || "").trim();
      if (!name) continue;
      // A row is a Participant, keyed by id where the seat has one: two
      // players of one name are two rows.
      const id = Number(seat?.id) || 0;
      const key = id ? `id:${id}` : `name:${name}`;
      let row = rows.get(key);
      if (!row) {
        row = {id, name, points: 0, blockRounds: new Array<number>(opts.blockRoundCount).fill(0), bouts: new Array<string>(opts.blockRoundCount).fill("")};
        rows.set(key, row);
      }
      const seated = Number(match.blockRound || 1) - 1;
      if (match.code && seated >= 0 && seated < row.bouts.length) row.bouts[seated] = match.code;
      if (!match.finished || !seat?.place) continue;
      const points = evalScoringRule(rule, boutScope(match, seatIndex));
      row.points += points;
      const blockRound = Number(match.blockRound || 1) - 1;
      if (blockRound >= 0 && blockRound < row.blockRounds.length) row.blockRounds[blockRound] += points;
    }
  }
  return Array.from(rows.values()).sort((a, b) =>
    b.points - a.points || a.name.localeCompare(b.name, "ru"));
}
