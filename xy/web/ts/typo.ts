// typo.ts — the typography pass the card editor's "typograph" button and the
// board's "Typograph whole board" run: chgksuite's typotools (quotes -> guillemets,
// hyphen runs -> em dashes, percent-escapes -> the text they encode) plus the
// non-breaking-space/hyphen gluing, applied to 4s SOURCE.
//
// It runs in the browser because everything it touches is question text, and
// question text is exactly what the server must never see: a board-wide pass
// through an endpoint would have posted a whole package in the clear. The Go
// packages it was ported from (internal/chgk/typo + typoedit) stay where they
// are as the parity oracle — both suites read the same fixtures
// (internal/chgk/typoedit/testdata/pass_cases.json), so the two implementations
// cannot drift in silence.
//
// A port, quirks included: where chgksuite does something odd, so does this.
// Only the knobs the button promises are here — quotes, dashes and percent
// decoding. Whitespace normalisation would fight the editor, and accents have
// their own button (detect_accent rewrites any mixed-case word whether stress
// was meant or not).
//
// ONE KNOWN DIVERGENCE, older than this file and left alone deliberately: the
// non-breaking-hyphen rule. Go's inline.ReplaceNoBreak matches
// `[а-яё]{0,3}-[а-яё]{0,3}`, which — the bounds being zero — glues nearly every
// surviving hyphen; chgk.ts's replaceNoBreak requires `{1,3}` on both sides, so
// "raz- dva" keeps a plain hyphen here and gets a non-breaking one there. The Go
// side feeds the docx/PDF exporters, whose output is byte-parity-tested against
// chgksuite, so aligning them is a decision about which is right rather than a
// typo to fix. Until it is made, the fixtures deliberately avoid hyphens that
// fall in the gap.
//
// ES module.

import { xyChgk } from "./chgk.js";
import { xyVersions } from "./versions.js";

// ── quotes ──────────────────────────────────────────────────────────────────

// QuoteFixer walks the string tracking nesting depth and rewrites every quote to
// the Russian convention («» at odd levels, „“ at even). If the quotes do not
// balance it gives up and returns the input untouched — a half-fixed quotation
// is worse than none.
interface QuoteMark { opening: boolean; level: number }

function isSpaceChar(c: string): boolean { return c === " " || c === "\u00a0"; }

function fixQuotes(src: string): string {
  const out = [...src];
  const marks = new Map<number, QuoteMark>();
  const last = new Map<number, string>();
  let level = 0;
  for (let i = 0; i < out.length; i++) {
    const c = out[i];
    if (c === "«" || c === "„") {
      level++;
      marks.set(i, { opening: true, level });
      last.set(level, c);
    } else if (c === "»" || c === "”") {
      marks.set(i, { opening: false, level });
      level--;
    } else if (c === '"') {
      const prev = i === 0 ? null : out[i - 1];
      const next = i + 1 === out.length ? null : out[i + 1];
      const openish = level === 0 || prev === null ||
        (isSpaceChar(prev) && next !== null && !isSpaceChar(next));
      if (openish || last.get(level) !== '"') {
        level++;
        marks.set(i, { opening: true, level });
        last.set(level, '"');
      } else {
        marks.set(i, { opening: false, level });
        level--;
      }
    } else if (c === "“") {
      if (last.get(level) === "„") {
        marks.set(i, { opening: false, level });
        level--;
      } else {
        level++;
        marks.set(i, { opening: true, level });
      }
    }
  }
  if (level !== 0) return src;
  for (const [i, m] of marks) {
    out[i] = m.opening ? (m.level % 2 !== 0 ? "«" : "„") : (m.level % 2 !== 0 ? "»" : "“");
  }
  return out.join("");
}

function getQuotesRight(s: string): string {
  if (s.includes('"') || (s.includes("“") && !s.includes("„"))) s = fixQuotes(s);
  // JS \w is ASCII-only, like Go's RE2 and unlike Python's re.UNICODE: without
  // the class spelled out a Ukrainian apostrophe (Slov'yans'ka) keeps its quote.
  return s.replace(/([\p{L}\p{N}_])'/gu, "$1’").replace(/'([\p{L}\p{N}_])/gu, "‘$1");
}

// ── dashes ──────────────────────────────────────────────────────────────────

// Go's unicode.IsSpace, spelled out. JS's \s is NOT the same set: it omits
// U+0085 (NEL) and includes U+FEFF, so using it here would disagree with the
// oracle on both characters.
const RE_GO_SPACE = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/;
const RE_GO_SPACE_RUN = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/;

// A run of hyphens flanked by whitespace becomes an em dash. Anything else — a
// hyphenated word, a list item's leading "-" (which never reaches here, see
// pass) — is left alone.
function getDashesRight(s: string): string {
  const rs = [...s];
  let out = "";
  for (let i = 0; i < rs.length;) {
    if (rs[i] === "-" && i > 0 && RE_GO_SPACE.test(rs[i - 1])) {
      let j = i;
      while (j < rs.length && rs[j] === "-") j++;
      if (j < rs.length && RE_GO_SPACE.test(rs[j])) {
        out += "—";
        i = j;
        continue;
      }
      for (; i < j; i++) out += rs[i];
      continue;
    }
    out += rs[i];
    i++;
  }
  return out.split(" – ").join(" — ");
}

// ── percent decoding ────────────────────────────────────────────────────────

const RE_PERCENT = /(?:%[0-9a-fA-F]{2})+/g;
// ignoreBOM keeps a decoded U+FEFF as a character instead of eating it, which is
// what Go's utf8.Valid + string() does: %EF%BB%BF decodes to the BOM, not to "".
const utf8 = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });

// Percent-escapes that decode to valid UTF-8 become the text they stand for —
// chgk sources are made of pasted Wikipedia links. Longest runs first, so a
// shorter run cannot clobber a longer one it sits inside.
function percentDecode(s: string): string {
  const groups = (s.match(RE_PERCENT) || []).slice().sort((a, b) => b.length - a.length);
  for (const g of groups) {
    const bytes = new Uint8Array(g.length / 3);
    for (let i = 0; i * 3 < g.length; i++) bytes[i] = parseInt(g.slice(i * 3 + 1, i * 3 + 3), 16);
    let text: string;
    try {
      text = utf8.decode(bytes);
    } catch {
      continue; // not UTF-8: leave the escape exactly as it was
    }
    s = s.split(g).join(text);
  }
  return s;
}

// ── stress accents ──────────────────────────────────────────────────────────
// chgk marks stress by capitalising the vowel ("brAzer"), and detect_accent
// turns that into a real combining acute ("brazer" + acute). It is a HEURISTIC on
// capitalisation, so its guards are the whole substance: an all-caps word is a
// word in caps, not a stressed one; a capital next to another capital is part of
// a run; "Mac..." and "O'..." are name prefixes, not stress.
const COMB_ACUTE = "\u0301";
const LOWER_RU = "абвгдеёжзийклмнопрстуфхцчшщъыьэюя";
const UPPER_RU = "АБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯ";
const POTENTIAL_ACCENT = "АОУЫЭЯЕЮИ";
const BAD_BEGINNINGS = new Set(["Мак", "мак", "О'", "о’", "О’", "о'"]);
const RE_NOT_RUSSIAN = new RegExp("[^" + LOWER_RU + UPPER_RU + "]+");
const ACCENTS_TO_FIX = new Set(["\u0300", "\u0341", "\u0301"]);
const LETTERS_MAPPING: Record<string, string> = {
  a: "а", e: "е", y: "у", o: "о", u: "и",
  A: "А", E: "Е", Y: "У", O: "О", U: "И",
};

const isUpper = (c: string): boolean => /\p{Lu}/u.test(c);
const isLetter = (c: string): boolean => /\p{L}/u.test(c);
const isCyrillic = (c: string): boolean => LOWER_RU.includes(c.toLowerCase());

// One word the heuristic would accent, as the board's review list shows it.
export interface AccentPick { from: string; to: string }

// `seen` collects what the pass WOULD do without doing it; `allow` restricts it
// to the pairs the editor ticked. Both are undefined for a plain pass, which is
// then chgksuite's own function exactly — the parity fixtures run that way.
// Reviewing is xy's, not chgksuite's, so the algorithm is untouched: this only
// watches it and, at the last moment, declines.
function detectAccent(s: string, seen?: Map<string, AccentPick>, allow?: Set<string>): string {
  for (const word of s.split(RE_NOT_RUSSIAN)) {
    if (word === "" || word.toUpperCase() === word || [...word].length <= 1) continue;
    let w = [...word];
    for (let i = 1; i < w.length; i++) {
      if (!POTENTIAL_ACCENT.includes(w[i])) continue;
      if (BAD_BEGINNINGS.has(w.slice(0, i).join(""))) continue;
      if (i !== 1 && isUpper(w[i - 1])) continue;
      if (i + 1 !== w.length && isUpper(w[i + 1])) continue;
      w = [...w.slice(0, i), w[i].toLowerCase(), COMB_ACUTE, ...w.slice(i + 1)];
      i++; // step over the combining mark just inserted
    }
    const nw = w.join("");
    if (nw === word) continue;
    if (seen) seen.set(word, { from: word, to: nw });
    if (allow && !allow.has(word)) continue;
    const idx = s.indexOf(word);
    if (idx >= 0) s = s.slice(0, idx) + nw + s.slice(idx + word.length);
  }
  return s;
}

// cyrLatCheckChar: a Latin letter wedged between Cyrillic ones and carrying a
// combining accent is a homoglyph typo — "moskva" typed with a Latin "o".
function cyrLatCheckChar(i: number, word: string[]): string {
  const char = word[i];
  if (isCyrillic(char)) return "";
  const leftOK = i === 0 || isCyrillic(word[i - 1]) || !isLetter(word[i - 1]);
  const rightOK = i === word.length - 1 || isCyrillic(word[i + 1]) || !isLetter(word[i + 1]);
  if (!leftOK || !rightOK) return "";
  const norm = [...char.normalize("NFD")];
  if (norm.length > 1 && norm[0] !== char) {
    const mapped = LETTERS_MAPPING[norm[0]];
    if (mapped && ACCENTS_TO_FIX.has(norm[1])) return mapped + COMB_ACUTE + norm.slice(2).join("");
  }
  return "";
}

function cyrLatCheckWord(word: string): string {
  const w = [...word];
  if (w.length === 1) return "";
  const reps: Array<[string, string]> = [];
  for (let i = 0; i < w.length; i++) {
    const fixed = cyrLatCheckChar(i, w);
    if (fixed !== "") reps.push([w[i], fixed]);
    else if (isCyrillic(w[i]) && i < w.length - 1 && ACCENTS_TO_FIX.has(w[i + 1])) {
      reps.push([w[i] + w[i + 1], w[i] + COMB_ACUTE]);
    }
  }
  if (!reps.length) return "";
  let out = word;
  const seen = new Set<string>();
  for (const [from, to] of reps) {
    if (seen.has(from)) continue;
    seen.add(from);
    out = out.split(from).join(to);
  }
  return out;
}

function fixAccents(s: string, seen?: Map<string, AccentPick>, allow?: Set<string>): string {
  s = detectAccent(s, seen, allow);
  const reps: Array<[string, string]> = [];
  const done = new Set<string>();
  for (const word of s.split(RE_GO_SPACE_RUN).filter((x) => x !== "")) {
    if (done.has(word)) continue;
    const fixed = cyrLatCheckWord(word);
    if (fixed !== "") {
      done.add(word);
      reps.push([word, fixed]);
    }
  }
  for (const [from, to] of reps) s = s.split(from).join(to);
  return s;
}

// ── the pass ────────────────────────────────────────────────────────────────

// How the caller wants stress marks handled: off, on, watched (`seen` fills with
// what it would do), or restricted to the words in `allow`.
export interface AccentOpts { seen?: Map<string, AccentPick>; allow?: Set<string> }

function typography(s: string, accents = false, o: AccentOpts = {}): string {
  s = getQuotesRight(s);
  s = s.split("'s").join("’s");
  s = getDashesRight(s);
  if (accents) s = fixAccents(s, o.seen, o.allow);
  return percentDecode(s);
}

// pass typographs 4s source, marker by marker. Splitting each line at its marker
// first is the whole point: a pass let loose on raw source reads a list item's
// leading "-" as a stray hyphen and turns it into an em dash, eating the list.
// Idempotent — the gluing rules match plain spaces, so text that already carries
// its NBSPs is left alone, and a button is something a user presses twice.
export function pass(source: string | null | undefined, accents = false, o: AccentOpts = {}): string {
  const lines = (source || "").split("\n");
  return lines.map((line) => {
    const { prefix, rest } = xyChgk.splitMarker(line);
    if (rest.trim() === "") return line;
    return prefix + xyChgk.replaceNoBreak(typography(rest, accents, o));
  }).join("\n");
}

// passAccents is pass plus stress-mark detection — what the buttons run.
export function passAccents(source: string | null | undefined, o: AccentOpts = {}): string {
  return pass(source, true, o);
}

// accentPicks reports every word the pass WOULD accent in these texts, without
// touching them: the board's review list, where an editor drops the compound
// nouns the heuristic cannot tell from a stress mark ("GazpromInvest"). Keyed by
// the original word, so the same word across thirty cards is one decision.
export function accentPicks(texts: ReadonlyArray<string>): AccentPick[] {
  const seen = new Map<string, AccentPick>();
  for (const t of texts) passAccents(t, { seen });
  return [...seen.values()];
}

// passVersions typographs a whole card — every version of it — and reassembles.
// The separators are xy's own markup, not prose, so they are never handed to the
// pass; and a wording the editor is not looking at needs straightening just as
// much as the one it is.
export function passVersions(desc: string | null | undefined, o: AccentOpts = {}): string {
  let out = desc || "";
  xyVersions.splitVersions(out).forEach((body, i) => {
    out = xyVersions.setVersionBody(out, i, passAccents(body, o));
  });
  return out;
}

export const xyTypo = {
  pass, passAccents, passVersions, accentPicks, typography,
  getQuotesRight, getDashesRight, percentDecode,
};
