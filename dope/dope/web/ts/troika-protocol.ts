// troika-protocol.ts — the Troika protocol's document as the page reads it
// (ADR-0018): the shape, the adapter from the server's JSON, and the
// arithmetic. Pure: every function takes the state it reads.
//
// A bout is two or three sides over themes of three questions. Three players sit at each
// table in the order the host asks them — chair 0 first — and all three
// answer every question their team plays. Every correct answer pays that theme's
// value on its own, so one question yields nought to three times its
// value; the sheet's familiar 1/2/3 is a count of correct answers, not a rank.

export const CHAIRS = 3;
export const THEME_QUESTIONS = 3;
export const DEFAULT_THEME_VALUE = 1;

export type Mark = "right" | "wrong" | "";

export interface TroikaTheme {
  // The players' ids in the order the host asks them. Zero is an empty seat.
  order: number[];
  // [question][chair].
  answers: Mark[][];
}

export interface TroikaSide {
  themes: TroikaTheme[];
  // A written bout's sheet: per theme and question, how many answers were right.
  counts: number[][];
}

export interface TroikaState {
  // Each theme's value, written when the bout was built.
  values: number[];
  sides: TroikaSide[];
  // How many of the last themes are shootout themes the host added.
  shootout: number;
  // A place per side the host set by hand; 0 leaves it to the sheet.
  pin: number[];
  // The written qualifier: one sitting of every troika, counts instead of chairs.
  written: boolean;
  [key: string]: unknown;
}

// SHOOTOUT_VALUE is what a shootout theme is worth: one theme worth 1.
export const SHOOTOUT_VALUE = 1;

// parseState is the adapter from whatever the server stored to a TroikaState
// the renderers can trust: at least `seats` sides (two by default), as many
// themes as the document declares values for, each a three-by-three grid, or
// for a written bout a row of three counts per theme.
export function parseState(raw: unknown, seats = 2): TroikaState {
  const doc = (raw && typeof raw === "object" ? raw : {}) as Partial<TroikaState>;
  const values = (Array.isArray(doc.values) ? doc.values : [])
    .map((v) => (typeof v === "number" && Number.isFinite(v) && v > 0 ? v : DEFAULT_THEME_VALUE));
  const themes = values.length;
  const written = doc.written === true;
  const sideCount = Math.max(seats, 2, Array.isArray(doc.sides) ? doc.sides.length : 0);
  const sides: TroikaSide[] = [];
  for (let s = 0; s < sideCount; s++) {
    const raw = Array.isArray(doc.sides) ? doc.sides[s] : undefined;
    const rawThemes = raw && Array.isArray(raw.themes) ? raw.themes : [];
    const rawCounts = raw && Array.isArray(raw.counts) ? raw.counts : [];
    const counts: number[][] = [];
    for (let t = 0; t < (written ? themes : 0); t++) {
      const row: number[] = [];
      for (let q = 0; q < THEME_QUESTIONS; q++) {
        const n = Array.isArray(rawCounts[t]) ? Number(rawCounts[t][q]) : 0;
        row.push(Number.isInteger(n) && n > 0 ? Math.min(n, CHAIRS) : 0);
      }
      counts.push(row);
    }
    const parsed: TroikaTheme[] = [];
    for (let t = 0; t < themes; t++) {
      const theme = rawThemes[t];
      const order: number[] = [];
      for (let c = 0; c < CHAIRS; c++) {
        const id = Array.isArray(theme?.order) ? theme!.order[c] : undefined;
        order.push(typeof id === "number" && Number.isFinite(id) ? id : 0);
      }
      const answers: Mark[][] = [];
      for (let q = 0; q < THEME_QUESTIONS; q++) {
        const row: Mark[] = [];
        for (let c = 0; c < CHAIRS; c++) {
          const mark = Array.isArray(theme?.answers) && Array.isArray(theme!.answers[q])
            ? theme!.answers[q][c]
            : "";
          row.push(mark === "right" || mark === "wrong" ? mark : "");
        }
        answers.push(row);
      }
      parsed.push({order, answers});
    }
    sides.push({themes: written ? [] : parsed, counts});
  }
  const shootout = Number(doc.shootout);
  const pin = (Array.isArray(doc.pin) ? doc.pin : []).map((p) => (typeof p === "number" && p > 0 ? p : 0));
  return {
    values, sides, written,
    shootout: Number.isInteger(shootout) && shootout > 0 ? Math.min(shootout, themes) : 0,
    pin: sides.map((_, i) => pin[i] || 0),
  };
}

// isShootoutTheme is whether theme t is one of the shootout themes the host
// added after the bout's own.
export function isShootoutTheme(state: TroikaState, theme: number): boolean {
  return theme >= state.values.length - state.shootout;
}

// countAt is a written bout's cell: how many of the troika's answers to that
// question were right.
export function countAt(state: TroikaState, side: number, theme: number, question: number): number {
  return state.sides[side]?.counts[theme]?.[question] ?? 0;
}

export function themeValue(state: TroikaState, theme: number): number {
  const value = state.values[theme];
  return typeof value === "number" && value > 0 ? value : DEFAULT_THEME_VALUE;
}

export function markAt(state: TroikaState, side: number, theme: number, question: number, chair: number): Mark {
  return state.sides[side]?.themes[theme]?.answers[question]?.[chair] ?? "";
}

// questionScore is what one question paid a side: every correct answer at its
// value, so three players who all took it pay three times over.
export function questionScore(state: TroikaState, side: number, theme: number, question: number): number {
  if (state.written) return countAt(state, side, theme, question) * themeValue(state, theme);
  let correct = 0;
  for (let c = 0; c < CHAIRS; c++) {
    if (markAt(state, side, theme, question, c) === "right") correct++;
  }
  return correct * themeValue(state, theme);
}

export function themeScore(state: TroikaState, side: number, theme: number): number {
  let total = 0;
  for (let q = 0; q < THEME_QUESTIONS; q++) total += questionScore(state, side, theme, q);
  return total;
}

export function sideTotal(state: TroikaState, side: number): number {
  let total = 0;
  for (let t = 0; t < state.values.length; t++) total += themeScore(state, side, t);
  return total;
}

// places rank the sides by total; sides that are level share the mean of the
// places between them (1.5 for a drawn pair) — the regulations pay half a
// rating ball for a draw, so the bout does not invent a winner. A pinned place
// wins over the sheet.
export function places(state: TroikaState): number[] {
  const totals = state.sides.map((_, side) => sideTotal(state, side));
  return totals.map((total, side) => {
    if (state.pin[side] > 0) return state.pin[side];
    const above = totals.filter((other) => other > total).length;
    const level = totals.filter((other) => other === total).length;
    return above + (level + 1) / 2;
  });
}

// level is whether any two sides share a place the sheet computed — the bout a
// host may add a shootout theme to.
export function level(state: TroikaState): boolean {
  const totals = state.sides.map((_, side) => sideTotal(state, side));
  return new Set(totals).size < totals.length;
}

// chairAt is who sat in chair c for this theme; the page resolves the id to a
// name through the bout's roster.
export function chairAt(state: TroikaState, side: number, theme: number, chair: number): number {
  return state.sides[side]?.themes[theme]?.order[chair] ?? 0;
}

// swapFrom rewrites the seating from theme t onward — what the host's swap
// button does. Seats are a fact per theme, so a swap is
// simply the new order written into every theme after it; an earlier one is
// left exactly as it was played.
export function swapFrom(state: TroikaState, side: number, from: number, order: number[]): void {
  for (let t = from; t < state.values.length; t++) {
    const theme = state.sides[side]?.themes[t];
    if (theme) theme.order = order.slice(0, CHAIRS);
  }
}

// turnedAt is whether either side sits differently for this theme than for the
// one before — where the sheet shows a seating column again.
export function turnedAt(state: TroikaState, theme: number): boolean {
  if (theme <= 0) return false;
  for (let side = 0; side < state.sides.length; side++) {
    for (let c = 0; c < CHAIRS; c++) {
      if (chairAt(state, side, theme, c) !== chairAt(state, side, theme - 1, c)) return true;
    }
  }
  return false;
}

export function started(state: TroikaState): boolean {
  if (state.pin.some((p) => p > 0)) return true;
  for (const side of state.sides) {
    if (side.counts.some((row) => row.some((n) => n > 0))) return true;
    for (const theme of side.themes) {
      if (theme.order.some((id) => id !== 0)) return true;
      if (theme.answers.some((row) => row.some((mark) => mark !== ""))) return true;
    }
  }
  return false;
}
