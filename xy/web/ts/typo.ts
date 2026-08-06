// typo.ts — the typography pass the card editor's «типограф» button and the
// board's «Типографить всю доску» run: chgksuite's typotools (quotes → «ёлочки»,
// hyphen runs → em dashes, percent-escapes → the text they encode) plus the
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
// «раз- два» keeps a plain hyphen here and gets a non-breaking one there. The Go
// side feeds the docx/PDF exporters, whose output is byte-parity-tested against
// chgksuite, so aligning them is a decision about which is right rather than a
// typo to fix. Until it is made, the fixtures deliberately avoid hyphens that
// fall in the gap.
//
// ES module.

import { xyChgk } from "./chgk.js";

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
  return s.replace(/(\w)'/g, "$1’").replace(/'(\w)/g, "‘$1");
}

// ── dashes ──────────────────────────────────────────────────────────────────

// Go's unicode.IsSpace, spelled out. JS's \s is NOT the same set: it omits
// U+0085 (NEL) and includes U+FEFF, so using it here would disagree with the
// oracle on both characters.
const RE_GO_SPACE = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]/;

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

// ── the pass ────────────────────────────────────────────────────────────────

function typography(s: string): string {
  s = getQuotesRight(s);
  s = s.split("'s").join("’s");
  s = getDashesRight(s);
  return percentDecode(s);
}

// pass typographs 4s source, marker by marker. Splitting each line at its marker
// first is the whole point: a pass let loose on raw source reads a list item's
// leading "-" as a stray hyphen and turns it into an em dash, eating the list.
// Idempotent — the gluing rules match plain spaces, so text that already carries
// its NBSPs is left alone, and a button is something a user presses twice.
export function pass(source: string | null | undefined): string {
  const lines = (source || "").split("\n");
  return lines.map((line) => {
    const { prefix, rest } = xyChgk.splitMarker(line);
    if (rest.trim() === "") return line;
    return prefix + xyChgk.replaceNoBreak(typography(rest));
  }).join("\n");
}

// passVersions typographs a whole card — every version of it — and reassembles.
// The separators are xy's own markup, not prose, so they are never handed to the
// pass; and a wording the editor is not looking at needs straightening just as
// much as the one it is.
export function passVersions(desc: string | null | undefined): string {
  let out = desc || "";
  xyChgk.splitVersions(out).forEach((body, i) => {
    out = xyChgk.setVersionBody(out, i, pass(body));
  });
  return out;
}

export const xyTypo = { pass, passVersions, typography, getQuotesRight, getDashesRight, percentDecode };
