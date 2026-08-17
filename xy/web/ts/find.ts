// find.ts — the matching rules behind board-wide search and find-and-replace.
// Pure: no DOM, no crypto, no storage, so jstest exercises every rule.
//
// Search folds and replace does not. The asymmetry is deliberate: a search that
// over-finds costs a glance, a replacement that over-matches costs a package.
//
// ES module.

import { xyChgk } from "./chgk.js";

// One matched run, as offsets into the ORIGINAL text — the folded copy is an
// internal affair and no caller ever sees its offsets.
export interface Span { start: number; end: number }

// ── folding ─────────────────────────────────────────────────────────────────

// A folded string plus, per folded character, where it came from. `starts[i]` is
// the source offset of folded character i and `starts[length]` closes the last
// one, so a folded range [a, b) is the source range [starts[a], starts[b]).
interface Folded { text: string; starts: number[] }

const NBSP = " ";
const NB_HYPHEN = "‑";
// The whole point of the fold: every one of these is something the typography
// pass (typo.ts) writes over text an editor typed the plain way.
const DASHES = "‐‑‒–—−";
const QUOTES = "«»„“”‘’‘’";
const RE_COMBINING = /\p{Mn}/u;

// per-character rules. Returns "" to drop the character (a combining mark the
// editor never typed), or the characters it folds to.
function foldChar(c: string, opts: FoldOpts): string {
  if (opts.typography) {
    if (RE_COMBINING.test(c)) return "";
    if (DASHES.includes(c)) return "-";
    if (QUOTES.includes(c)) return '"';
    if (c === "ё") return "е";
    if (c === "Ё") return opts.caseless ? "е" : "Е";
  }
  // The gluing half of the pass, folded even for a literal match: a plain space
  // is what an editor types and a non-breaking one is what the button left.
  if (c === NBSP) return " ";
  if (c === NB_HYPHEN) return "-";
  return opts.caseless ? c.toLowerCase() : c;
}

// How forgiving a comparison is. `typography` folds what the pass writes over
// the text (accents, quotes, dashes, ё); it is off for a replacement, which
// matches what is on screen. NBSP/NBHY folding is not optional — see foldChar.
export interface FoldOpts { caseless?: boolean; typography?: boolean }

function fold(s: string, opts: FoldOpts): Folded {
  let text = "";
  const starts: number[] = [];
  let i = 0;
  for (const c of s) {
    const f = foldChar(c, opts);
    for (let k = 0; k < f.length; k++) starts.push(i);
    text += f;
    i += c.length;
  }
  starts.push(s.length);
  return { text, starts };
}

// foldQuery folds a needle the same way, discarding the offsets.
function foldQuery(q: string, opts: FoldOpts): string {
  return fold(q, opts).text;
}

// ── matching ────────────────────────────────────────────────────────────────

// spansIn walks an ALREADY folded haystack and maps every hit back onto the
// source. `skip` vetoes a hit (the marker/version guard); a vetoed hit does not
// consume the text, so an overlapping legal one can still be found after it.
function spansIn(hay: Folded, query: string, opts: FoldOpts, skip?: (s: Span) => boolean): Span[] {
  const out: Span[] = [];
  const q = foldQuery(query, opts);
  if (!q) return out;
  let from = 0;
  for (;;) {
    const i = hay.text.indexOf(q, from);
    if (i < 0) return out;
    const span = { start: hay.starts[i], end: hay.starts[i + q.length] };
    if (!skip || !skip(span)) {
      out.push(span);
      from = i + q.length;
    } else {
      from = i + 1;
    }
  }
}

const SEARCH_FOLD: FoldOpts = { caseless: true, typography: true };

// A text folded once for searching. Folding is the expensive half of a match, so
// anything searched over and over — every card of every board, on every
// keystroke — folds once and keeps this instead.
export type Haystack = Folded;

export function prepare(text: string): Haystack {
  return fold(text, SEARCH_FOLD);
}

// searchIn is the forgiving match the Search Index reads with, over text already
// prepared; searchSpans is the same thing for a one-off.
export function searchIn(hay: Haystack, query: string): Span[] {
  return spansIn(hay, query, SEARCH_FOLD);
}

export function searchSpans(text: string, query: string): Span[] {
  return searchIn(prepare(text), query);
}

// ── what a replacement may never eat ────────────────────────────────────────

// A version separator is not prose: it both starts a Version and names it
// (ADR-0006), so only the name inside it may be rewritten.
const VERSION_LINE = /^\(hidden-comment\s+xy-version:([^()]*)\)$/;

// guarded lists the source ranges a replacement must not overlap: every line's
// marker prefix, and every version separator save the name it carries. Both are
// structure — a replacement that ate one would break the card's versions or its
// export parity, silently and board-wide.
function guarded(source: string): Span[] {
  const out: Span[] = [];
  let at = 0;
  for (const line of source.split("\n")) {
    const end = at + line.length;
    const version = VERSION_LINE.exec(line.trim());
    if (version) {
      const nameAt = at + line.indexOf(version[1], line.indexOf("xy-version:"));
      out.push({ start: at, end: nameAt }, { start: nameAt + version[1].length, end });
    } else {
      const { prefix } = xyChgk.splitMarker(line);
      if (prefix) out.push({ start: at, end: at + prefix.length });
    }
    at = end + 1;
  }
  return out;
}

// replaceSpans is the literal match a replacement runs on: what you typed, case
// and all. Only the pass's gluing is folded — a plain space finds the
// non-breaking one it became, and so does a hyphen.
export function replaceSpans(source: string, from: string, caseSensitive: boolean): Span[] {
  const off = guarded(source);
  const hits = (s: Span): boolean => off.some((g) => s.start < g.end && g.start < s.end);
  const opts = { caseless: !caseSensitive };
  return spansIn(fold(source, opts), from, opts, hits);
}

// applySpans rewrites `source`, replacing the given spans with `to` and leaving
// everything else — including the occurrences nobody ticked — alone. Walked
// back to front so the offsets stay true as the text under them changes.
export function applySpans(source: string, picked: ReadonlyArray<Span>, to: string): string {
  const ordered = [...picked].sort((a, b) => b.start - a.start);
  let out = source;
  for (const s of ordered) out = out.slice(0, s.start) + to + out.slice(s.end);
  return out;
}

// ── showing a hit ───────────────────────────────────────────────────────────

// One hit as a result tile shows it: a window of text, and the place of every
// match INSIDE that window — offsets into the window, since the ellipsis shifts
// them all. `hidden` counts the matches the window does not reach, so a tile
// says «+2» about matches that are genuinely elsewhere rather than about ones
// the reader can see sitting there unmarked.
export interface Snippet { text: string; marks: Span[]; hidden: number }

// The window is drawn around the FIRST match; every other match that falls
// inside it is marked too. Spans arrive in order — that is how both matchers
// report them.
export function snippet(text: string, spans: ReadonlyArray<Span>, radius = 40): Snippet {
  if (!spans.length) return { text, marks: [], hidden: 0 };
  const from = Math.max(0, spans[0].start - radius);
  const to = Math.min(text.length, spans[0].end + radius);
  const head = from > 0 ? "…" : "";
  const shift = head.length - from;
  const marks = spans.filter((s) => s.start >= from && s.end <= to)
    .map((s) => ({ start: s.start + shift, end: s.end + shift }));
  return {
    text: head + text.slice(from, to) + (to < text.length ? "…" : ""),
    marks,
    hidden: spans.length - marks.length,
  };
}

// foldSearch is the search's forgiving comparison as a key: what two strings
// must agree on to be the same to an editor (accents, quotes, dashes, ё, case).
export function foldSearch(text: string): string {
  return foldQuery(text, SEARCH_FOLD);
}

export const xyFind = { prepare, searchIn, searchSpans, replaceSpans, applySpans, snippet, foldSearch };
