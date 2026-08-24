// troika-protocol.ts — the Тройка Protocol's document as the page reads it
// (ADR-0018): the shape, the adapter from the server's JSON, and the
// arithmetic. Pure: every function takes the state it reads.
//
// A бой is two sides over темы of three вопросы. Three players sit at each
// table in the order the ведущий asks them — chair 0 first — and all three
// answer every вопрос their team plays. Every correct answer pays that тема's
// нарицательная on its own, so one вопрос yields nought to three times its
// value; the sheet's familiar 1/2/3 is a count of correct answers, not a rank.

export const CHAIRS = 3;
export const THEME_QUESTIONS = 3;
export const DEFAULT_THEME_VALUE = 1;

export type Mark = "right" | "wrong" | "";

export interface TroikaTheme {
  // The players' ids in the order the ведущий asks them. Zero is an empty seat.
  order: number[];
  // [вопрос][кресло].
  answers: Mark[][];
}

export interface TroikaSide {
  themes: TroikaTheme[];
}

export interface TroikaState {
  // Each тема's нарицательная, written when the бой was built.
  values: number[];
  sides: TroikaSide[];
  [key: string]: unknown;
}

// parseState is the adapter from whatever the server stored to a TroikaState
// the renderers can trust: two sides, as many темы as the document declares
// values for, each a three-by-three grid.
export function parseState(raw: unknown): TroikaState {
  const doc = (raw && typeof raw === "object" ? raw : {}) as Partial<TroikaState>;
  const values = (Array.isArray(doc.values) ? doc.values : [])
    .map((v) => (typeof v === "number" && Number.isFinite(v) && v > 0 ? v : DEFAULT_THEME_VALUE));
  const themes = values.length;
  const sides: TroikaSide[] = [];
  for (let s = 0; s < 2; s++) {
    const raw = Array.isArray(doc.sides) ? doc.sides[s] : undefined;
    const rawThemes = raw && Array.isArray(raw.themes) ? raw.themes : [];
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
    sides.push({themes: parsed});
  }
  return {values, sides};
}

export function themeValue(state: TroikaState, theme: number): number {
  const value = state.values[theme];
  return typeof value === "number" && value > 0 ? value : DEFAULT_THEME_VALUE;
}

export function markAt(state: TroikaState, side: number, theme: number, question: number, chair: number): Mark {
  return state.sides[side]?.themes[theme]?.answers[question]?.[chair] ?? "";
}

// questionScore is what one вопрос paid a side: every correct answer at its
// value, so three players who all took it pay three times over.
export function questionScore(state: TroikaState, side: number, theme: number, question: number): number {
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

// places are 1 and 2, shared at 1.5 on a ничья — the регламент pays half a
// рейтинговый балл for one, so the бой does not invent a winner.
export function places(state: TroikaState): number[] {
  const a = sideTotal(state, 0);
  const b = sideTotal(state, 1);
  if (a > b) return [1, 2];
  if (a < b) return [2, 1];
  return [1.5, 1.5];
}

// chairAt is who sat in chair c for this тема; the page resolves the id to a
// name through the бой's roster.
export function chairAt(state: TroikaState, side: number, theme: number, chair: number): number {
  return state.sides[side]?.themes[theme]?.order[chair] ?? 0;
}

// swapFrom rewrites the seating from theme t onward — what the host's «здесь
// поменялись местами» button does. Seats are a fact per тема, so a swap is
// simply the new order written into every тема after it; an earlier one is
// left exactly as it was played.
export function swapFrom(state: TroikaState, side: number, from: number, order: number[]): void {
  for (let t = from; t < state.values.length; t++) {
    const theme = state.sides[side]?.themes[t];
    if (theme) theme.order = order.slice(0, CHAIRS);
  }
}

export function started(state: TroikaState): boolean {
  for (const side of state.sides) {
    for (const theme of side.themes) {
      if (theme.order.some((id) => id !== 0)) return true;
      if (theme.answers.some((row) => row.some((mark) => mark !== ""))) return true;
    }
  }
  return false;
}
