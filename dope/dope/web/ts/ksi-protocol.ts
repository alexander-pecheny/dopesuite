// ksi-protocol.ts — the KSI protocol's document as the page reads it: the
// shape (KSIState), the rules a scheme sets (rulesOf: team mode, theme count,
// stickers), the adapter from the server's JSON (parseState), and the scoring
// — a mark's value under a sticker, a theme's value, the score sheet, the
// ranked results with shared places. Pure: every function takes the state and
// the rules it reads.
import {computePlaces} from "./score-table.js";
import S from "./i18nstrings_ru_gen.js";

export const QUESTION_VALUES = [10, 20, 30, 40, 50];
export const RESULT_VALUES = QUESTION_VALUES.slice().reverse();
export const KSI_THEMES = 20;
// The sticker whose rules match a regular KSI theme: the implicit sticker of
// a plain game and the fallback for an unknown id.
export const STICKER_NEUTRAL = "neutral";

export interface StickerType {
  id: string;
  label: string;
  color: string;
  max: number | null; // how many a team may use; null = unlimited (the neutral one)
}

export interface KSIScheme {
  gameType?: string;
  title?: string;
  themes?: unknown;
  participants?: string[];
  teams?: Array<{name?: string}>;
  stickers?: {types?: Array<{id?: unknown; label?: unknown; color?: unknown; max?: unknown} | null | undefined>} | null;
  [key: string]: unknown;
}

// Participants are {number, name} objects in team mode — the Number is the
// universal team identity — and bare name strings in player mode and legacy
// states. Every reader takes either shape.
export type ParticipantEntry = string | {number?: unknown; name?: unknown} | null | undefined;

export interface KSIState {
  participants: ParticipantEntry[];
  themes: Array<{answers: string[][]}>;
  finished: boolean;
  declined: Record<string, boolean>;
  stickers?: string[][];
  [key: string]: unknown;
}

// KSIRules is what the scheme fixes for a game: team or player mode, how many
// themes, which stickers exist.
export interface KSIRules {
  teamMode: boolean;
  themesCount: number;
  stickers: StickerType[];
  stickerById: Map<string, StickerType>;
}

export function isTeamMode(scheme: KSIScheme): boolean {
  return scheme.gameType === "ksi";
}

export function schemeParticipants(scheme: KSIScheme): string[] {
  if (Array.isArray(scheme.participants) && scheme.participants.length > 0) {
    return scheme.participants.slice();
  }
  if (isTeamMode(scheme) && Array.isArray(scheme.teams) && scheme.teams.length > 0) {
    return scheme.teams.map((team) => team.name || "");
  }
  if (isTeamMode(scheme)) return [];
  return [1, 2, 3, 4].map((n) => S.si.participant.fallbackPlayer(String(n)));
}

export function rulesOf(scheme: KSIScheme): KSIRules {
  const teamMode = isTeamMode(scheme);
  const themesCount = Number(scheme.themes) > 0 ? Number(scheme.themes) : (teamMode ? KSI_THEMES : 8);
  const stickers: StickerType[] = [];
  const types = scheme.stickers && Array.isArray(scheme.stickers.types) ? scheme.stickers.types : [];
  for (const raw of types) {
    if (!raw || typeof raw.id !== "string" || !raw.id) continue;
    stickers.push({
      id: raw.id,
      label: typeof raw.label === "string" && raw.label ? raw.label : raw.id,
      color: typeof raw.color === "string" ? raw.color : "",
      max: typeof raw.max === "number" && Number.isFinite(raw.max) ? raw.max : null,
    });
  }
  return {teamMode, themesCount, stickers, stickerById: new Map(stickers.map((t) => [t.id, t]))};
}

// stickersEnabled gates the sticker UI and scoring: only a KSI team game that
// carries a sticker configuration.
export function stickersEnabled(rules: KSIRules): boolean {
  return rules.teamMode && rules.stickers.length > 0;
}

// parseState is the adapter from whatever the server stored to a KSIState the
// renderers can trust: the participants (from the scheme when the document
// has none), themesCount themes of one five-cell answer row per participant,
// the finished flag, the declined map keyed by team identity (it survives a
// roster re-import, which rebuilds the participant objects), and for a
// stickers game a themes × participants grid of sticker ids.
export function parseState(raw: unknown, rules: KSIRules, participants: string[]): KSIState {
  const state = (raw && typeof raw === "object" ? raw : {}) as KSIState;
  if (!Array.isArray(state.participants) || state.participants.length === 0) {
    state.participants = participants.slice();
  }
  if (!Array.isArray(state.themes)) state.themes = [];
  while (state.themes.length < rules.themesCount) state.themes.push({answers: []});
  state.themes = state.themes.slice(0, rules.themesCount).map((theme) => {
    const answers = Array.isArray(theme.answers) ? theme.answers : [];
    const padded: string[][] = [];
    for (let p = 0; p < state.participants.length; p++) {
      const row: string[] = Array.isArray(answers[p]) ? answers[p].slice(0, QUESTION_VALUES.length) : [];
      while (row.length < QUESTION_VALUES.length) row.push("");
      padded.push(row);
    }
    return {answers: padded};
  });
  if (typeof state.finished !== "boolean") state.finished = false;
  if (!state.declined || typeof state.declined !== "object" || Array.isArray(state.declined)) {
    state.declined = {};
  }
  if (stickersEnabled(rules)) {
    const grid: string[][] = Array.isArray(state.stickers) ? state.stickers : [];
    const next: string[][] = [];
    for (let t = 0; t < rules.themesCount; t++) {
      const row: string[] = Array.isArray(grid[t]) ? grid[t] : [];
      const padded: string[] = [];
      for (let p = 0; p < state.participants.length; p++) {
        const id = row[p];
        padded.push(typeof id === "string" && rules.stickerById.has(id) ? id : "");
      }
      next.push(padded);
    }
    state.stickers = next;
  }
  return state;
}

// === who is who ===

export function participantName(state: KSIState, index: number): string {
  const p = state.participants?.[index];
  if (typeof p === "string") return p;
  return p && typeof p === "object" ? String(p.name ?? "") : "";
}

export function participantNumber(state: KSIState, index: number): number {
  const p = state.participants?.[index];
  return p && typeof p === "object" ? Number(p.number) || 0 : 0;
}

// declinedKey is the identity the refused-to-play map is keyed on: the team
// Number when known, else a name fallback for a legacy number-less state; ""
// when there is nothing to key on.
export function declinedKey(state: KSIState, index: number): string {
  const number = participantNumber(state, index);
  if (number > 0) return `n${number}`;
  const name = participantName(state, index).trim().toLowerCase();
  return name ? `s${name}` : "";
}

export function participantDeclined(state: KSIState, index: number): boolean {
  const key = declinedKey(state, index);
  return key ? Boolean(state.declined?.[key]) : false;
}

// participantsEqual compares two participant arrays by identity (number +
// name), either shape, so in-place patching survives fresh object references.
export function participantsEqual(a: ParticipantEntry[] | null | undefined, b: ParticipantEntry[] | null | undefined): boolean {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  const key = (p: ParticipantEntry) => (typeof p === "string" ? `n:${p}` : `${p?.number || 0}:${p?.name ?? ""}`);
  for (let i = 0; i < a.length; i++) {
    if (key(a[i]) !== key(b[i])) return false;
  }
  return true;
}

// === scoring ===

export function stickerValue(state: KSIState, player: number, theme: number): string {
  const id = state.stickers?.[theme]?.[player];
  return typeof id === "string" ? id : "";
}

// markContribution is the signed value of one answer mark under a sticker. It
// mirrors games.KSIStickerMarkValue on the server so the two cannot drift.
export function markContribution(stickerId: string, mark: string, answerIndex: number): number {
  const value = QUESTION_VALUES[answerIndex];
  switch (stickerId) {
    case "x2":
      return mark === "right" ? 2 * value : mark === "wrong" ? -2 * value : 0;
    case "nowrong":
      return mark === "right" ? value : 0;
    case "emptywrong":
      return mark === "right" ? value : -value; // wrong or empty → -value
    default: // neutral, and any unknown id
      return mark === "right" ? value : mark === "wrong" ? -value : 0;
  }
}

// computeThemeValue is one team's value for one theme and whether it is
// scored: a plain game scores every theme under neutral rules; a stickers
// game leaves a theme unscored until its sticker is chosen.
export function computeThemeValue(state: KSIState, rules: KSIRules, player: number, theme: number): {value: number; scored: boolean} {
  const row = state.themes[theme]?.answers?.[player] || [];
  let stickerId = STICKER_NEUTRAL;
  if (stickersEnabled(rules)) {
    stickerId = stickerValue(state, player, theme);
    if (!stickerId) return {value: 0, scored: false};
  }
  let value = 0;
  for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
    value += markContribution(stickerId, row[answerIndex], answerIndex);
  }
  return {value, scored: true};
}

export interface ScoreSheet {
  themeScores: number[][];
  themeScored: boolean[][];
  totals: number[];
  places: string[];
}

// scoreSheet folds the whole document: per team per theme value and whether it
// counts, the totals, the places.
export function scoreSheet(state: KSIState, rules: KSIRules): ScoreSheet {
  const themeScores: number[][] = state.participants.map(() => Array(rules.themesCount).fill(0));
  const themeScored: boolean[][] = state.participants.map(() => Array(rules.themesCount).fill(true));
  const totals: number[] = state.participants.map(() => 0);
  for (let playerIndex = 0; playerIndex < state.participants.length; playerIndex++) {
    for (let themeIndex = 0; themeIndex < rules.themesCount; themeIndex++) {
      const {value, scored} = computeThemeValue(state, rules, playerIndex, themeIndex);
      themeScores[playerIndex][themeIndex] = value;
      themeScored[playerIndex][themeIndex] = scored;
      if (scored) totals[playerIndex] += value;
    }
  }
  return {themeScores, themeScored, totals, places: computePlaces(totals)};
}

export interface ResultMetrics {
  total: number;
  plus: number;
  correct: Record<number, number>;
}

export interface ResultRow {
  index: number;
  name: string;
  metrics: ResultMetrics;
  placeText: string;
}

// resultMetrics is what the results table ranks a team by: the total, the
// plus (positive contributions only) and how many of each value it took.
export function resultMetrics(state: KSIState, rules: KSIRules, playerIndex: number): ResultMetrics {
  const correct: Record<number, number> = {};
  for (const value of QUESTION_VALUES) correct[value] = 0;
  let total = 0;
  let plus = 0;
  for (let themeIndex = 0; themeIndex < rules.themesCount; themeIndex++) {
    let stickerId = STICKER_NEUTRAL;
    if (stickersEnabled(rules)) {
      stickerId = stickerValue(state, playerIndex, themeIndex);
      if (!stickerId) continue; // unscored theme excluded from the ranking
    }
    const row = state.themes[themeIndex]?.answers?.[playerIndex] || [];
    for (let answerIndex = 0; answerIndex < QUESTION_VALUES.length; answerIndex++) {
      const value = QUESTION_VALUES[answerIndex];
      const mark = row[answerIndex];
      const contribution = markContribution(stickerId, mark, answerIndex);
      total += contribution;
      if (contribution > 0) plus += contribution;
      if (mark === "right") correct[value] += 1;
    }
  }
  return {total, plus, correct};
}

const nameCollator = new Intl.Collator("ru", {numeric: true, sensitivity: "base"});

export function compareResultRows(a: ResultRow, b: ResultRow): number {
  if (b.metrics.total !== a.metrics.total) return b.metrics.total - a.metrics.total;
  if (b.metrics.plus !== a.metrics.plus) return b.metrics.plus - a.metrics.plus;
  for (const value of RESULT_VALUES) {
    const diff = (b.metrics.correct[value] || 0) - (a.metrics.correct[value] || 0);
    if (diff) return diff;
  }
  return nameCollator.compare(a.name, b.name) || a.index - b.index;
}

export function sameResultMetrics(a: ResultMetrics, b: ResultMetrics): boolean {
  if (a.total !== b.total || a.plus !== b.plus) return false;
  for (const value of RESULT_VALUES) {
    if ((a.correct[value] || 0) !== (b.correct[value] || 0)) return false;
  }
  return true;
}

// rankedResultRows is the results table: every team that did not refuse to
// play, in rank order, with a shared place label ("2–3") where the metrics
// tie. A declined team takes no place and shifts nobody; it appears in
// the refusals tab alone. label names a team for the tie-break and the row.
export function rankedResultRows(state: KSIState, rules: KSIRules, label: (index: number) => string): ResultRow[] {
  const rows = state.participants
    .map((_, index) => ({index, name: label(index), metrics: resultMetrics(state, rules, index), placeText: ""}))
    .filter((row) => !participantDeclined(state, row.index));
  rows.sort(compareResultRows);
  let i = 0;
  while (i < rows.length) {
    let j = i;
    while (j + 1 < rows.length && sameResultMetrics(rows[i].metrics, rows[j + 1].metrics)) j++;
    const place = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
    for (let k = i; k <= j; k++) rows[k].placeText = place;
    i = j + 1;
  }
  return rows;
}
