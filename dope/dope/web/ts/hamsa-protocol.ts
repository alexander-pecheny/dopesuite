// hamsa-protocol.ts — the Hamsa protocol's document as the page reads it
// (ADR-0018): the shape, the adapter from the server's JSON, and the
// arithmetic. Pure: every function takes the state it reads.
//
// A bout is five game rounds. The first four play themes of five questions
// each, every round multiplying the base nominal values; the fifth is a single
// question played on a bet the team writes down, added to its score when it
// answers and subtracted when it does not. The document is keyed by
// Participant, so a re-seat can never move one team's marks onto another, and
// it carries what each question was worth in the bout that played it.

export const QUESTIONS = 5;

export type Mark = "right" | "wrong" | "";

export interface GameRound {
  themes: number;
  values: number[];
}

export interface Theme {
  // The player who sat for this theme, by id; 0 is nobody.
  player: number;
  answers: Mark[];
}

export interface Bet {
  // What the team wrote down, or null where it wrote nothing — a team on a
  // zero balance is not admitted to the round.
  amount: number | null;
  answer: Mark;
}

export interface Participant {
  themes: Theme[];
  bet: Bet;
  shootout: Theme[];
  pin: number | null;
}

export interface HamsaState {
  rounds: GameRound[];
  // One section per seated Participant, keyed by its id as a string — the
  // path a patch addresses.
  participants: Record<string, Participant>;
}

const DEFAULT_VALUES = [100, 200, 300, 400, 500];
const DEFAULT_THEMES = [5, 5, 5, 1];
const DEFAULT_MULTIPLIERS = [1, 2, 3, 4];

function numberAt(list: unknown, index: number, fallback: number): number {
  const value = Array.isArray(list) ? list[index] : undefined;
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function markOf(value: unknown): Mark {
  return value === "right" || value === "wrong" ? value : "";
}

function parseTheme(raw: unknown): Theme {
  const doc = (raw && typeof raw === "object" ? raw : {}) as Partial<Theme>;
  const answers: Mark[] = [];
  for (let q = 0; q < QUESTIONS; q++) answers.push(markOf(Array.isArray(doc.answers) ? doc.answers[q] : ""));
  return {player: Math.max(0, Math.trunc(numberAt([doc.player], 0, 0))), answers};
}

// parseState is the adapter from whatever the server stored to a state the
// renderers can trust: the rounds the bout declares, and a section per seat —
// including a seat that has entered nothing, which still has a row and a
// place.
export function parseState(raw: unknown, seats: number[]): HamsaState {
  const doc = (raw && typeof raw === "object" ? raw : {}) as Partial<HamsaState>;
  const rawRounds = Array.isArray(doc.rounds) ? doc.rounds : [];
  const count = rawRounds.length || DEFAULT_THEMES.length;
  const rounds: GameRound[] = [];
  for (let r = 0; r < count; r++) {
    const round = (rawRounds[r] && typeof rawRounds[r] === "object" ? rawRounds[r] : {}) as Partial<GameRound>;
    const themes = Math.max(0, Math.trunc(numberAt([round.themes], 0, DEFAULT_THEMES[r] ?? 0)));
    const values: number[] = [];
    for (let q = 0; q < QUESTIONS; q++) {
      values.push(numberAt(round.values, q, DEFAULT_VALUES[q] * (DEFAULT_MULTIPLIERS[r] ?? r + 1)));
    }
    rounds.push({themes, values});
  }
  const themeCount = rounds.reduce((sum, round) => sum + round.themes, 0);
  const participants: Record<string, Participant> = {};
  const rawParticipants = (doc.participants && typeof doc.participants === "object" ? doc.participants : {}) as Record<string, unknown>;
  for (const id of seats) {
    const key = String(id);
    const section = (rawParticipants[key] && typeof rawParticipants[key] === "object" ? rawParticipants[key] : {}) as Partial<Participant>;
    const themes: Theme[] = [];
    for (let t = 0; t < themeCount; t++) themes.push(parseTheme(Array.isArray(section.themes) ? section.themes[t] : undefined));
    const rawShootout = Array.isArray(section.shootout) ? section.shootout : [];
    const shootout = rawShootout.map(parseTheme);
    const rawBet = (section.bet && typeof section.bet === "object" ? section.bet : {}) as Partial<Bet>;
    const amount = typeof rawBet.amount === "number" && Number.isFinite(rawBet.amount) ? rawBet.amount : null;
    const pin = typeof section.pin === "number" && Number.isFinite(section.pin) ? section.pin : null;
    participants[key] = {themes, bet: {amount, answer: markOf(rawBet.answer)}, shootout, pin};
  }
  return {rounds, participants};
}

export function themeCount(state: HamsaState): number {
  return state.rounds.reduce((sum, round) => sum + round.themes, 0);
}

// roundOfTheme walks the rounds to find the one theme t belongs to; the last
// round answers for a theme past what the document declares.
export function roundOfTheme(state: HamsaState, theme: number): number {
  let seen = 0;
  for (let r = 0; r < state.rounds.length; r++) {
    seen += state.rounds[r].themes;
    if (theme < seen) return r;
  }
  return Math.max(0, state.rounds.length - 1);
}

export function themeValues(state: HamsaState, theme: number): number[] {
  return state.rounds[roundOfTheme(state, theme)]?.values || DEFAULT_VALUES;
}

// The shootout is another personal round, so its questions are worth what the
// last game round paid.
export function shootoutValues(state: HamsaState): number[] {
  return state.rounds[state.rounds.length - 1]?.values || DEFAULT_VALUES;
}

// The base values the per-value counts are named after: the first round's, the
// multiplier there being one.
export function baseValues(state: HamsaState): number[] {
  return state.rounds[0]?.values || DEFAULT_VALUES;
}

export function sectionOf(state: HamsaState, id: number): Participant | undefined {
  return state.participants[String(id)];
}

export function markAt(state: HamsaState, id: number, theme: number, question: number): Mark {
  return sectionOf(state, id)?.themes[theme]?.answers[question] ?? "";
}

export function playerAt(state: HamsaState, id: number, theme: number): number {
  return sectionOf(state, id)?.themes[theme]?.player ?? 0;
}

function scoreTheme(theme: Theme | undefined, values: number[]): number {
  let score = 0;
  theme?.answers.forEach((mark, q) => {
    if (mark === "right") score += values[q] || 0;
    else if (mark === "wrong") score -= values[q] || 0;
  });
  return score;
}

export function themeScore(state: HamsaState, id: number, theme: number): number {
  return scoreTheme(sectionOf(state, id)?.themes[theme], themeValues(state, theme));
}

export function shootoutThemeScore(state: HamsaState, id: number, theme: number): number {
  return scoreTheme(sectionOf(state, id)?.shootout[theme], shootoutValues(state));
}

export function shootoutTotal(state: HamsaState, id: number): number {
  const section = sectionOf(state, id);
  let score = 0;
  for (let t = 0; t < (section?.shootout.length || 0); t++) score += shootoutThemeScore(state, id, t);
  return score;
}

// betScore is what the bet added or took away: nothing until the answer is
// marked, which is what an unplayed fifth round looks like.
export function betScore(state: HamsaState, id: number): number {
  const bet = sectionOf(state, id)?.bet;
  if (!bet || bet.amount === null) return 0;
  if (bet.answer === "right") return bet.amount;
  if (bet.answer === "wrong") return -bet.amount;
  return 0;
}

export function total(state: HamsaState, id: number): number {
  let score = betScore(state, id);
  for (let t = 0; t < themeCount(state); t++) score += themeScore(state, id, t);
  return score;
}

// plus is what the themes took without their penalties. The bet stays out: it
// measures a gamble, not what the team knew.
export function plus(state: HamsaState, id: number): number {
  const section = sectionOf(state, id);
  let score = 0;
  section?.themes.forEach((theme, t) => {
    const values = themeValues(state, t);
    theme.answers.forEach((mark, q) => {
      if (mark === "right") score += values[q] || 0;
    });
  });
  return score;
}

export interface Row {
  id: number;
  total: number;
  plus: number;
  shootout: number;
  place: number;
}

// placesFor ranks the seats by score, then by the shootout alone — teams level
// on the score share the places they cover, and the only tiebreak a bout has
// is the extra personal round. A host's Pin stands instead of the place
// computed, exactly as the server's scorer does it.
export function placesFor(state: HamsaState, seats: number[]): number[] {
  const key = (id: number): [number, number] => [total(state, id), shootoutTotal(state, id)];
  const order = seats.map((_, index) => index);
  order.sort((a, b) => {
    const ka = key(seats[a]);
    const kb = key(seats[b]);
    return kb[0] - ka[0] || kb[1] - ka[1];
  });
  const places = new Array<number>(seats.length).fill(0);
  const same = (a: number, b: number) => {
    const ka = key(seats[a]);
    const kb = key(seats[b]);
    return ka[0] === kb[0] && ka[1] === kb[1];
  };
  for (let start = 0; start < order.length;) {
    let end = start + 1;
    while (end < order.length && same(order[end], order[start])) end++;
    const place = (start + end + 1) / 2;
    for (let i = start; i < end; i++) places[order[i]] = place;
    start = end;
  }
  seats.forEach((id, index) => {
    const pin = sectionOf(state, id)?.pin;
    if (pin !== null && pin !== undefined) places[index] = pin;
  });
  return places;
}

// rows is what the sheet's leading columns show, one per seat in seating order.
export function rows(state: HamsaState, seats: number[]): Row[] {
  const places = placesFor(state, seats);
  return seats.map((id, index) => ({
    id,
    total: total(state, id),
    plus: plus(state, id),
    shootout: shootoutTotal(state, id),
    place: places[index],
  }));
}

export function started(state: HamsaState): boolean {
  for (const section of Object.values(state.participants)) {
    if (section.pin !== null) return true;
    if (section.bet.amount !== null || section.bet.answer !== "") return true;
    for (const theme of [...section.themes, ...section.shootout]) {
      if (theme.player !== 0) return true;
      if (theme.answers.some((mark) => mark !== "")) return true;
    }
  }
  return false;
}
