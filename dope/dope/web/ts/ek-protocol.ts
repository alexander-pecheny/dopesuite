// ek-protocol.ts — the EK protocol's edits as the page sends them (ADR-0018):
// a cell edit on the sheet, the view path the writer tracks it under, and the
// wire op the server stores. Pure: every function takes what it reads. EK,
// Erudit-Sextet and individual SI share it.
//
// The sheet addresses a team by its slot in the bout and a player by name; the
// server's blob keys a team by its id, a player by id, and a host's place is a
// pin (ADR-0005). The two translations below are the whole of that difference.

import {parseMark} from "./sheet-cursor.js";
import type {Mark} from "./sheet-cursor.js";

// A place op's view path is participants/slot/place.
const PLACE_OP_PATH_LEN = 3;

// EKCellPayload is one cell edit as the sheet makes it.
export type EKCellPayload = {
  team: number;
  theme?: number;
  answer?: number;
  mark?: string;
  // The theme's whole seating, by display name: a seat edit sends the list it
  // leaves behind, never a difference.
  players?: string[];
  place?: number;
  shootout?: boolean;
};

// BlobOp is one wire operation against a match's Protocol state blob: a path of
// object keys / array indices, and the value to set there. Team sections are
// keyed by team id (a string), theme players are player ids, and a host's place
// is a pin: the server resolves nothing by name (ADR-0005).
export type BlobOp = {op?: "set" | "remove"; path: Array<string | number>; value?: unknown};

// ViewOp is an edit as the writer queues it: a path on the match view.
export type ViewOp = {path: Array<string | number>; value: unknown};

// EKBoutView is what the translation reads of a bout's view: who sits in each
// slot, and the people each may seat.
export interface EKBoutView {
  participants?: Array<{id?: number; roster?: Array<{id: number; name: string}>} | null | undefined>;
}

// opPath maps a cell edit to its match-view path (the server's matchDeltaOps
// shape). Null for a payload that is not a cell, which must not be tracked as
// an overlay.
export function opPath(payload: EKCellPayload): Array<string | number> | null {
  if (payload.place !== undefined) return ["participants", payload.team, "place"];
  const themesKey = payload.shootout ? "shootoutThemes" : "themes";
  if (payload.players !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "players"];
  if (payload.mark !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "answers", payload.answer!];
  return null;
}

// opValue is the value a cell edit sets at its path.
export function opValue(payload: EKCellPayload): unknown {
  if (payload.place !== undefined) return payload.place;
  if (payload.players !== undefined) return payload.players;
  return payload.mark;
}

// blobOp translates a queued view-path op into the blob path the server stores
// it at: the team's slot becomes its id, a place becomes a pin (an empty one
// clears it, handing the place back to the scorer), and a player's name
// becomes their id. Null when the view no longer holds that team, or no longer
// knows a seated name: the op is then unsendable and is dropped rather than
// retried forever.
export function blobOp(op: ViewOp, view: EKBoutView): BlobOp | null {
  const [, slot, key, theme, leaf, answer] = op.path;
  const team = view.participants?.[slot as number];
  if (!team?.id) return null;
  const teamKey = String(team.id);
  if (op.path.length === PLACE_OP_PATH_LEN) {
    const path = ["participants", teamKey, "pin"];
    return op.value ? {path, value: op.value} : {op: "remove", path};
  }
  if (leaf === "players") {
    const ids: number[] = [];
    for (const name of (op.value as string[]) || []) {
      const member = (team.roster || []).find((player) => player.name === name);
      if (!member) return null;
      ids.push(member.id);
    }
    return {path: ["participants", teamKey, key as string, theme as number, "players"], value: ids};
  }
  return {path: ["participants", teamKey, key as string, theme as number, "answers", answer as number], value: op.value};
}

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
