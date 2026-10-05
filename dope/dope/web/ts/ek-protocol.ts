// ek-protocol.ts — the EK protocol's document as the page reads it
// (ADR-0018): its shape, the adapter from the server's JSON, the arithmetic,
// and where an edit goes. Pure: every function takes what it reads. EK,
// Erudit-Sextet and individual SI share it.

import {parseMark} from "./sheet-cursor.js";
import type {Mark} from "./sheet-cursor.js";

// ---- the document ----
//
// A bout's document as the server stores it (store.MatchBlob): a section per
// seated Participant, keyed by its id, with the themes it played, the
// shootout themes, and a host's pinned place.

export type {Mark};

export interface EKTheme {
  // The player ids fielded on the theme: one in EK, up to three in
  // Erudit-Sextet; none is nobody chosen yet.
  players: number[];
  // One mark per question, in the order the values run.
  answers: Mark[];
}

export interface EKSection {
  themes: EKTheme[];
  shootoutThemes: EKTheme[];
  pin: number | null;
}

export interface EKState {
  sections: Map<number, EKSection>;
}

// QUESTIONS is how many questions a theme has; their values are the view's
// questionValues.
export const QUESTIONS = 5;

// normalizeMark reads a stored mark. The page writes "right" and "wrong";
// anything else is read the way a typed mark is (sheet-cursor's parseMark).
export function normalizeMark(raw: unknown): Mark {
  return parseMark(raw);
}

function parseTheme(raw: unknown): EKTheme {
  const theme = (raw && typeof raw === "object" ? raw : {}) as {players?: unknown; player?: unknown; answers?: unknown};
  let players = Array.isArray(theme.players) ? theme.players.map(Number).filter((id) => id > 0) : [];
  // A row written before Erudit-Sextet holds one `player`.
  if (players.length === 0 && Number(theme.player) > 0) players = [Number(theme.player)];
  const answers = Array.isArray(theme.answers) ? theme.answers : [];
  return {players, answers: Array.from({length: QUESTIONS}, (_, i) => normalizeMark(answers[i]))};
}

function parseThemes(raw: unknown, count: number): EKTheme[] {
  const list = Array.isArray(raw) ? raw.map(parseTheme) : [];
  while (list.length < count) list.push(parseTheme(null));
  return list;
}

// parseState reads a bout's document for the seats it has, each section
// padded to the bout's theme count, so a renderer can index every cell.
export function parseState(raw: unknown, seats: number[], themes: number): EKState {
  const doc = (raw && typeof raw === "object" ? raw : {}) as {participants?: Record<string, unknown>};
  const sections = new Map<number, EKSection>();
  for (const id of seats) {
    if (!id) continue;
    const section = (doc.participants?.[String(id)] || {}) as {themes?: unknown; shootoutThemes?: unknown; pin?: unknown};
    const pin = section.pin === null || section.pin === undefined ? null : Number(section.pin);
    sections.set(id, {
      themes: parseThemes(section.themes, themes),
      shootoutThemes: parseThemes(section.shootoutThemes, 0),
      pin: pin && pin > 0 ? pin : null,
    });
  }
  return {sections};
}

// themeScore is what one theme is worth: each right answer adds its value,
// each wrong one takes it away.
export function themeScore(theme: EKTheme, values: readonly number[]): number {
  let score = 0;
  theme.answers.forEach((mark, i) => {
    if (mark === "right") score += values[i] || 0;
    if (mark === "wrong") score -= values[i] || 0;
  });
  return score;
}

// SectionScore is a side's sheet totals, as the server scores them
// (store.ScoreParticipant): Σ, Σ+ (right answers alone), the shootout, and
// how many questions of each value it took and missed.
export interface SectionScore {
  total: number;
  plus: number;
  shootout: number;
  correct: number[];
  wrong: number[];
}

export function scoreSection(section: EKSection | undefined, values: readonly number[]): SectionScore {
  const out: SectionScore = {total: 0, plus: 0, shootout: 0, correct: new Array(QUESTIONS).fill(0), wrong: new Array(QUESTIONS).fill(0)};
  for (const theme of section?.themes || []) {
    theme.answers.forEach((mark, i) => {
      if (mark === "right") {
        out.total += values[i] || 0;
        out.plus += values[i] || 0;
        out.correct[i]++;
      } else if (mark === "wrong") {
        out.total -= values[i] || 0;
        out.wrong[i]++;
      }
    });
  }
  for (const theme of section?.shootoutThemes || []) out.shootout += themeScore(theme, values);
  return out;
}

// ---- where an edit goes ----

export type ThemeKind = "themes" | "shootoutThemes";

export function answerPath(id: number, kind: ThemeKind, theme: number, answer: number): Array<string | number> {
  return ["participants", String(id), kind, theme, "answers", answer];
}

export function playersPath(id: number, kind: ThemeKind, theme: number): Array<string | number> {
  return ["participants", String(id), kind, theme, "players"];
}

export function pinPath(id: number): Array<string | number> {
  return ["participants", String(id), "pin"];
}
