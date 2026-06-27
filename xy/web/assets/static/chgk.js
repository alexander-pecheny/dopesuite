// chgk.js — minimal client-side parser for the chgksuite "4s" markup, used to
// derive readable card previews (question text + number, headings, meta) from a
// card's raw description. Mirrors chgksuite's line-leading markers
// (see chgksuite/common.py `types_mapping`). Display-only: the editor keeps the
// raw 4s source; this module never rewrites it.
//
// ES module + window.xyChgk global.

// Line-leading markers, longest/most-specific first so e.g. "№№" wins over "№"
// and "###" over "#". A marker matches a line that equals it or starts with
// "<marker> ".
const MARKERS = [
  ["№№", "numnum"],
  ["№", "num"],
  ["###LJ", "ljheading"],
  ["###", "heading"],
  ["##", "section"],
  ["#EDITOR", "editor"],
  ["#DATE", "date"],
  ["#", "meta"],
  ["?", "question"],
  ["!=", "nezachet"],
  ["!", "answer"],
  ["=", "zachet"],
  ["^", "source"],
  ["/", "comment"],
  ["@", "author"],
  [">", "handout"],
];

function matchMarker(line) {
  for (const [marker, type] of MARKERS) {
    if (line === marker) return { type, rest: "" };
    if (line.startsWith(marker + " ")) return { type, rest: line.slice(marker.length + 1) };
  }
  return null;
}

// parseBlocks splits a raw description into [{type, text}] blocks. Lines without
// a marker continue the current block (multi-line questions/answers). Leading
// lines before any marker form a "pre" block (a question card whose author
// didn't prefix "? ").
function parseBlocks(desc) {
  const lines = (desc || "").split(/\r?\n/);
  const blocks = [];
  let cur = null;
  for (const line of lines) {
    const m = matchMarker(line);
    if (m) {
      cur = { type: m.type, text: m.rest };
      blocks.push(cur);
    } else if (cur) {
      cur.text += "\n" + line;
    } else {
      cur = { type: "pre", text: line };
      blocks.push(cur);
    }
  }
  for (const b of blocks) b.text = b.text.trim();
  return blocks.filter((b) => b.text !== "" || b.type !== "pre");
}

// numberDirective returns the explicit number directive of a question, if any:
// {value, base} where base=true for "№№" (sets the auto-numbering base) and
// false for a plain "№".
function numberDirective(blocks) {
  for (const b of blocks) {
    if (b.type === "numnum") return { value: b.text, base: true };
    if (b.type === "num") return { value: b.text, base: false };
  }
  return null;
}

// questionText returns the displayable question text (the "? " block, or the
// preamble, or the whole description) — never including the "? " marker.
function questionText(desc) {
  const blocks = parseBlocks(desc);
  const q = blocks.find((b) => b.type === "question");
  if (q) return q.text;
  const pre = blocks.find((b) => b.type === "pre");
  if (pre) return pre.text;
  return (desc || "").trim();
}

// blockText returns the first block of `type`, falling back to preamble / whole
// text. Used for meta (# ) and heading (### ) cards.
function blockText(desc, type) {
  const blocks = parseBlocks(desc);
  const b = blocks.find((x) => x.type === type);
  if (b) return b.text;
  const pre = blocks.find((x) => x.type === "pre");
  if (pre) return pre.text;
  return (desc || "").trim();
}

// previewText returns the marker-stripped text used to derive a card title for a
// given kind (number prefix for questions is added by the caller, since it needs
// the card's position in the list).
function previewText(kind, desc) {
  if (kind === "question") return questionText(desc);
  if (kind === "meta") return blockText(desc, "meta");
  if (kind === "heading") return blockText(desc, "heading");
  return (desc || "").trim();
}

// isZeroNumber mirrors chgksuite's is_zero: a number that starts with "0" or
// isn't an integer (e.g. a warm-up "0" / "разминка") — it's shown verbatim and
// does not advance the auto-counter.
function isZeroNumber(value) {
  const s = String(value).trim();
  return s.startsWith("0") || !/^\d+$/.test(s);
}

// numberQuestionCards assigns a display number to each card in list order.
// Question cards auto-number 1,2,3…; a "№ N" sets an explicit number and a
// "№№ N" resets the running base (matching chgksuite). Heading and meta cards
// carry no number of their own, but a standalone "№№ N" on them resets the base
// for the questions that follow (chgksuite's setcounter). "Other" and test cards
// are ignored entirely. Returns an array aligned with `cards`.
function numberQuestionCards(cards) {
  let next = 1;
  const out = [];
  for (const c of cards) {
    if (c.kind === "question") {
      const dir = numberDirective(parseBlocks(c.desc));
      let num;
      if (dir && dir.value !== "") {
        const n = parseInt(dir.value, 10);
        if (dir.base) {
          if (!Number.isNaN(n)) { num = String(n); next = n + 1; }
          else num = dir.value;
        } else {
          num = dir.value;
          if (!isZeroNumber(dir.value)) next = n + 1;
        }
      } else {
        num = String(next);
        next++;
      }
      out.push(num);
      continue;
    }
    if (c.kind === "heading" || c.kind === "meta") {
      const dir = numberDirective(parseBlocks(c.desc));
      if (dir && dir.base && dir.value !== "") {
        const n = parseInt(dir.value, 10);
        if (!Number.isNaN(n)) next = n;
      }
    }
    out.push(null);
  }
  return out;
}

// ── "screen mode" transforms (ported from chgksuite composer_common.py) ──────
// chgksuite's screen export strips two things that are meant for the host but not
// the players: combining stress accents (́) and the contents of square
// brackets (reading instructions). Square brackets whose body starts with
// "Раздат…" are *handout* markers — players DO see those, so they (and accents
// inside them) are preserved. Used by the "copy for testing" action.

// handout_short regex from chgksuite (regexes_ru.json): a bracket body that
// begins with "Раздат" (in any letter case) is a handout, not a host note.
const HANDOUT_SHORT = /^Р[Аа][Зз][Дд][Аа][Тт]/;

function isEscapedBracket(s, i) {
  return s[i] === "\\" && i + 1 < s.length && (s[i + 1] === "[" || s[i + 1] === "]");
}

// findMatchingBracket returns the index of the "]" closing the "[" at `i`
// (respecting nesting and escaped brackets), or -1 if unbalanced.
function findMatchingBracket(s, i) {
  if (i >= s.length || s[i] !== "[") return -1;
  let depth = 0;
  while (i < s.length) {
    if (isEscapedBracket(s, i)) { i += 2; continue; }
    if (s[i] === "[") depth++;
    else if (s[i] === "]") { depth--; if (depth === 0) return i; }
    i++;
  }
  return -1;
}

// bracketSpans yields [start, endExclusive, body] for each top-level "[...]"
// span, skipping escaped brackets (\[ \]).
function* bracketSpans(s) {
  let i = 0;
  while (i < s.length) {
    if (isEscapedBracket(s, i)) { i += 2; continue; }
    if (s[i] !== "[") { i++; continue; }
    const end = findMatchingBracket(s, i);
    if (end === -1) { i++; continue; }
    yield [i, end + 1, s.slice(i + 1, end)];
    i = end + 1;
  }
}

const isHandoutBody = (body) => HANDOUT_SHORT.test(body);

// removeAccents strips combining stress marks everywhere except inside handout
// brackets (which are shown verbatim to players).
function removeAccents(s) {
  let result = "", prev = 0;
  for (const [start, end] of bracketSpans(s)) {
    if (!isHandoutBody(s.slice(start + 1, end - 1))) continue;
    result += s.slice(prev, start).replace(/\u0301/g, "");
    result += s.slice(start, end); // keep the handout span verbatim
    prev = end;
  }
  result += s.slice(prev).replace(/\u0301/g, "");
  return result;
}

// removeSquareBrackets drops host-only "[...]" notes; handout brackets are kept,
// escaped brackets (\[ \]) are unescaped to literal brackets.
function removeSquareBrackets(s) {
  let result = "", i = 0, removed = false;
  while (i < s.length) {
    if (isEscapedBracket(s, i)) { result += s.slice(i, i + 2); i += 2; continue; }
    if (s[i] !== "[") { result += s[i]; i++; continue; }
    const end = findMatchingBracket(s, i);
    if (end === -1) { result += s[i]; i++; continue; }
    if (isHandoutBody(s.slice(i + 1, end))) {
      result += s.slice(i, end + 1); // keep the handout (brackets included)
    } else {
      while (result.endsWith(" ")) result = result.slice(0, -1);
      removed = true;
    }
    i = end + 1;
  }
  if (removed) result = result.trim();
  return result.replace(/\\\[/g, "[").replace(/\\\]/g, "]");
}

// ── inline 4s directive parser (ported from chgksuite composer_common
// `_parse_4s_elem` + `backtick_replace`) ─────────────────────────────────────
// Splits a single 4s text element into [type, value] runs so the directives that
// aren't just leading-line markers — (LINEBREAK), (PAGEBREAK), (screen a|b),
// (img …), (sc …), _italic_/**bold**/~strike~, hyperlinks, %-encoding, \_ \. and
// backtick stress — are handled when composing the "copy for testing" text,
// instead of leaking through verbatim. value is a string except for "screen"
// runs, which carry {for_print, for_screen}.

const UNDERSCORE_PLACEHOLDER = "UNDERSCORE";
const TILDE_PLACEHOLDER = "TILDE";

// backtickReplace: a backtick before a Cyrillic letter is shorthand for a
// combining stress accent on that letter (chgksuite `backtick_replace`).
function backtickReplace(el) {
  while (el.includes("`")) {
    const idx = el.indexOf("`");
    if (idx + 1 >= el.length) { el = el.replace("`", ""); continue; }
    const ch = el[idx + 1];
    if (/[а-яё]/.test(ch)) {
      el = el.slice(0, idx) + ch + "́" + el.slice(idx + 2);
    } else if (/[А-ЯЁ]/.test(ch)) {
      el = el.slice(0, idx) + ch + "́" + el.slice(idx + 2);
    } else {
      el = el.slice(0, idx) + el.slice(idx + 1);
    }
  }
  return el;
}

// iterHttpUrlSpans yields [start, end) spans of bare http(s) URLs, so their
// underscores aren't mistaken for italic markers (chgksuite `_iter_http_url_spans`).
function* iterHttpUrlSpans(s) {
  let i = 0;
  while (i < s.length) {
    if (s.startsWith("http://", i) || s.startsWith("https://", i)) {
      let j = i + 1, bracketLevel = 0;
      while (j < s.length && !(/\s/.test(s[j]) || (s[j] === ")" && bracketLevel === 0))) {
        if (s[j] === "(") bracketLevel++;
        else if (s[j] === ")" && bracketLevel > 0) bracketLevel--;
        j++;
      }
      const end = [",", ".", ";"].includes(s[j - 1]) ? j - 1 : j;
      yield [i, end];
      i = j;
    } else i++;
  }
}

// findMatchingClosingBracket: index of the ")" closing the bracket at `index`.
function findMatchingClosingBracket(s, index) {
  const ob = s[index];
  const cb = ob === "(" ? ")" : ob === "[" ? "]" : ob === "{" ? "}" : null;
  if (cb === null) return null;
  let counter = 0;
  for (let i = index; i < s.length; i++) {
    if (s[i] === ob) counter++;
    if (s[i] === cb) { counter--; if (counter === 0) return i; }
  }
  return null;
}

function findNextUnescaped(ss, index, length) {
  let j = index + length;
  while (j < ss.length) {
    if (ss[j] === "\\" && j + 2 < ss.length) j += 2;
    if (ss.slice(j, j + length) === ss.slice(index, index + length)) return j;
    j++;
  }
  return -1;
}

function partition(s, indices) {
  const bounds = [0, ...indices, s.length];
  const out = [];
  for (let k = 0; k < bounds.length - 1; k++) out.push(s.slice(bounds[k], bounds[k + 1]));
  return out;
}

function parse4sElem(s) {
  s = s.replace(/\\_/g, UNDERSCORE_PLACEHOLDER).replace(/\\~/g, TILDE_PLACEHOLDER);

  // protect underscores/tildes inside URLs
  {
    let res = "", last = 0;
    for (const [start, end] of iterHttpUrlSpans(s)) {
      res += s.slice(last, start);
      res += s.slice(start, end).replace(/_/g, UNDERSCORE_PLACEHOLDER).replace(/~/g, TILDE_PLACEHOLDER);
      last = end;
    }
    res += s.slice(last);
    s = res;
  }

  // percent-decode (longest matches first so a short one can't split a long one)
  const grs = [...new Set(s.match(/(%[0-9a-fA-F]{2})+/g) || [])].sort((a, b) => b.length - a.length);
  for (const gr of grs) {
    try { s = s.split(gr).join(decodeURIComponent(gr)); } catch (_) { /* leave as-is */ }
  }

  let i = 0;
  const topart = [];
  while (i < s.length) {
    if (s[i] === "_" || s[i] === "~") {
      let j = i + 1;
      while (j < s.length && s[j] === s[i]) j++;
      const length = j - i;
      topart.push(i);
      const nxt = findNextUnescaped(s, i, length);
      if (nxt !== -1) {
        topart.push(nxt + length);
        i = nxt + length + 1;
        continue;
      }
    }
    if (s[i] === "(" && s.startsWith("(img", i)) {
      topart.push(i);
      const close = findMatchingClosingBracket(s, i);
      if (close !== null) { topart.push(close + 1); i = close; }
    }
    if (s[i] === "(" && s.startsWith("(screen", i)) {
      topart.push(i);
      const close = findMatchingClosingBracket(s, i);
      if (close !== null) { topart.push(close + 1); i = close; }
    }
    if (s.startsWith("(PAGEBREAK)", i)) {
      topart.push(i);
      topart.push(i + "(PAGEBREAK)".length);
    }
    if (s.startsWith("(LINEBREAK)", i)) {
      topart.push(i);
      topart.push(i + "(LINEBREAK)".length);
    }
    if (s.startsWith("http://", i) || s.startsWith("https://", i)) {
      topart.push(i);
      let j = i + 1, bracketLevel = 0;
      while (j < s.length && !(/\s/.test(s[j]) || (s[j] === ")" && bracketLevel === 0))) {
        if (s[j] === "(") bracketLevel++;
        else if (s[j] === ")" && bracketLevel > 0) bracketLevel--;
        j++;
      }
      if ([",", ".", ";"].includes(s[j - 1])) topart.push(j - 1);
      else topart.push(j);
      i = j;
    }
    i++;
  }

  topart.sort((a, b) => a - b);
  const parts = partition(s, topart).map((x) => ["", x.replace(/敥/g, "")]);

  const process = (str) => String(str)
    .replace(/\\_/g, "_")
    .replace(/\\\./g, ".")
    .split(UNDERSCORE_PLACEHOLDER).join("_")
    .split(TILDE_PLACEHOLDER).join("~");

  for (const part of parts) {
    if (!part[1]) continue;
    try {
      if (part[1].startsWith("_") && part[1].endsWith("_")) {
        let j = 1;
        while (j < part[1].length && part[1][j] === "_" && part[1][part[1].length - j - 1] === "_") j++;
        part[1] = part[1].slice(j, part[1].length - j);
        if (j === 1) part[0] = "italic";
        else if (j === 2) part[0] = "bold";
        else if (j === 3) part[0] = "underline";
        else if (j === 4) part[0] = "italicbold";
        else if (j === 5) part[0] = "boldunderline";
        else if (j >= 6) part[0] = "italicboldunderline";
      }
      if (part[1].startsWith("~") && part[1].endsWith("~")) {
        part[0] = "strike";
        part[1] = part[1].slice(1, -1);
      }
      if (part[1] === "(PAGEBREAK)") { part[0] = "pagebreak"; part[1] = ""; }
      if (part[1] === "(LINEBREAK)") { part[0] = "linebreak"; part[1] = ""; }
      if (part[1].length > 4 && part[1].slice(0, 4) === "(img") {
        if (part[1][part[1].length - 1] !== ")") part[1] += ")";
        part[1] = part[1].slice(4, -1);
        part[0] = "img";
      }
      if (part[1].length > 7 && part[1].slice(0, 7) === "(screen") {
        if (part[1][part[1].length - 1] !== ")") part[1] += ")";
        const [forPrint, forScreen] = part[1].slice(8, -1).split("|");
        part[1] = { for_print: process(forPrint), for_screen: process(forScreen) };
        part[0] = "screen";
        continue;
      }
      if (part[1].startsWith("http://") || part[1].startsWith("https://")) part[0] = "hyperlink";
      if (part[1].length > 3 && part[1].slice(0, 4) === "(sc") {
        if (part[1][part[1].length - 1] !== ")") part[1] += ")";
        part[1] = part[1].slice(3, -1);
        part[0] = "sc";
      }
      part[1] = process(part[1]);
    } catch (_) { /* leave the run as plain text */ }
  }

  return parts;
}

// renderRunsForScreen flattens parsed runs to the plain text players see (screen
// mode): the for_screen side of (screen …), (LINEBREAK) → newline, images and
// page breaks dropped, all other runs as their stripped text (chgksuite docx
// screen-mode `token_text`).
function renderRunsForScreen(runs) {
  let res = "";
  for (const [type, val] of runs) {
    if (type === "linebreak") res += "\n";
    else if (type === "pagebreak" || type === "img") continue;
    else if (type === "screen") res += val.for_screen;
    else res += val;
  }
  return res;
}

// screenText applies the screen-mode transforms (accents first, then brackets,
// matching chgksuite's order), resolves backtick stress, then parses inline
// directives and composes the player-facing plain text.
function screenText(s) {
  s = removeSquareBrackets(removeAccents(s || ""));
  s = backtickReplace(s);
  return renderRunsForScreen(parse4sElem(s));
}

// applyOverride detects a chgksuite "!!Label " override at the start of a field
// value: if the first space-separated token begins with "!!", that token (minus
// the "!!", with "~" → space) replaces the field's printed label, and is stripped
// from the value. Mirrors chgksuite_parser's OVERRIDE_PREFIX handling (applies to
// question/answer/zachet/nezachet/comment/source/author). Returns {label, text}
// with label === null when there is no override.
function applyOverride(text) {
  const s = text || "";
  const idx = s.indexOf(" ");
  if (idx === -1) return { label: null, text: s };
  const first = s.slice(0, idx);
  if (!first.startsWith("!!")) return { label: null, text: s };
  return { label: first.slice(2).replace(/~/g, " "), text: s.slice(idx + 1) };
}

// renderRuns prepares a 4s text element for HTML rendering and returns its inline
// directive runs. Mirrors format_docx_element's preamble: optionally strip stress
// accents and/or host-only square brackets (screen mode), else unescape \[ \]
// (replace_escaped), then resolve backtick stress and parse. opts.accents /
// opts.brackets follow the per-field screen-mode rules (e.g. answers/zachet keep
// brackets even on screen). Used by the in-app list preview.
function renderRuns(text, opts = {}) {
  let s = text || "";
  if (opts.accents) s = removeAccents(s);
  if (opts.brackets) s = removeSquareBrackets(s);
  else s = s.replace(/\\\[/g, "[").replace(/\\\]/g, "]"); // replace_escaped
  s = backtickReplace(s);
  return parse4sElem(s);
}

// printRuns is the host/print-mode shorthand (keeps accents and host brackets).
function printRuns(text) {
  return renderRuns(text, { accents: false, brackets: false });
}

// ── non-breaking spaces / hyphens (port of typotools.replace_no_break) ───────
// Glues short prepositions/conjunctions to the following word, trailing particles
// and dashes to the preceding word (→ U+00A0), and hyphenates short hyphenated
// words with a non-breaking hyphen (→ U+2011) — so lines never break in ugly
// places. URLs are skipped. Applied to question/answer/comment/etc. text (not
// sources, which carry links/citations).
const NBSP = " ";
const NB_HYPHEN = "‑";
const NB_RIGHT = ["а", "без", "в", "во", "где", "для", "же", "за", "и", "или", "из", "из-за",
  "к", "как", "на", "над", "не", "ни", "но", "о", "от", "по", "под", "при", "с", "со", "то", "у", "что", "перед"];
const NB_LEFT = ["бы", "ли", "же", "—", "–"];

function reEscape(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"); }
function capFirst(w) { return w ? w.charAt(0).toUpperCase() + w.slice(1) : w; }

function nbSegment(s) {
  for (const w of NB_RIGHT) {
    for (const v of new Set([w, capFirst(w)])) {
      s = s.replace(new RegExp("(^|[ \\u00a0])" + reEscape(v) + " ", "g"), "$1" + v + NBSP);
    }
  }
  for (const w of NB_LEFT) {
    for (const v of new Set([w, capFirst(w)])) {
      s = s.replace(new RegExp(" " + reEscape(v) + "([ \\u00a0]|$)", "g"), NBSP + v + "$1");
    }
  }
  // short hyphenated words (из-за, что-то, кто-то…): require a letter on each side
  // so a stray spaced "-" can't turn every hyphen non-breaking.
  const re = /(^|[^а-яё])([а-яё]{1,3}-[а-яё]{1,3})([^а-яё]|$)/i;
  let m;
  while ((m = re.exec(s))) {
    const word = m[2];
    s = s.split(word).join(word.replace(/-/g, NB_HYPHEN));
  }
  return s;
}

// replaceNoBreak applies nbSegment to every non-URL span of the text.
function replaceNoBreak(text) {
  const s = text || "";
  const spans = [...iterHttpUrlSpans(s)];
  if (!spans.length) return nbSegment(s);
  let out = "", pos = 0;
  for (const [start, end] of spans) {
    if (start < pos) continue;
    out += nbSegment(s.slice(pos, start));
    out += s.slice(start, end);
    pos = end;
  }
  out += nbSegment(s.slice(pos));
  return out;
}

// splitList ports chgksuite's process_list: lines beginning with "-" become list
// items (rendered as a numbered 1./2./… list); any text before the first "-" is a
// preamble. A lone "-" item is NOT a list (the marker is just stripped). Returns
// { preamble, items } with items === null when there is no multi-item list.
function splitList(text) {
  const s = text || "";
  if (!s.includes("-")) return { preamble: s, items: null };
  const sp = s.split("\n");
  const markers = [];
  for (let i = 0; i < sp.length; i++) if (sp[i].startsWith("-")) markers.push(i);
  if (!markers.length) return { preamble: s, items: null };
  const items = [];
  for (let n = 0; n < markers.length; n++) {
    const end = n + 1 < markers.length ? markers[n + 1] : sp.length;
    // drop the leading "-" then any spaces after it (chgksuite slices [1:] + rew)
    items.push(sp.slice(markers[n], end).join("\n").slice(1).replace(/^ +/, ""));
  }
  if (items.length === 1) {
    return { preamble: s.replace(/(^|\n)- +/g, "$1"), items: null };
  }
  return { preamble: sp.slice(0, markers[0]).join("\n"), items };
}

// ---- Trello import clean-up -------------------------------------------------
// Trello's new editor litters exported card descriptions with artefacts: double
// line breaks between every paragraph, markdown-escaped chgksuite markers
// (\#, \@, \-, \`), stray ``` fences and "smart link" cards that serialise as
// `[https://…](https://… )`. fixTrelloFormatting undoes all of this, mirroring
// chgksuite's `fix_trello_new_editor` cleanup (chgksuite/trello.py) so the
// chgksuite markers survive and smart links collapse to plain URLs.
function fixTrelloFormatting(s) {
  s = String(s || "");
  s = s.replace(/\n\n/g, "\n").replace(/\\@/g, "@");
  s = s.replace(/\n +/g, "\n");
  s = s.replace(/\n\\-/g, "\n-");
  s = s.replace(/\\#/g, "#");
  s = s.replace(/```/g, "");
  s = s.replace(/\\`/g, "`");
  return fixTrelloLinks(s);
}

// parseTrelloLink locates the `[text](target)` span whose `](` is at index i,
// walking out to the matching `[` and `)` (bracket-aware, like chgksuite's
// find_and_parse_link). Returns the span bounds + the bare URL when both the
// text and the target start with "http" (a Trello smart-link), else null link.
function parseTrelloLink(s, i) {
  let mvr = i, level = 0, found = false;
  while (mvr > 0) {
    mvr -= 1;
    if (s[mvr] === "]") level += 1;
    else if (s[mvr] === "[") { if (level) level -= 1; else { found = true; break; } }
  }
  if (!found || s[mvr] !== "[") return null;
  const start = mvr;
  const firstPart = s.slice(start, i + 1); // "[ … ]"
  let j = i + 1, lvl = 0; found = false;
  while (j < s.length - 1) {
    j += 1;
    if (s[j] === "(") lvl += 1;
    else if (s[j] === ")") { if (lvl) lvl -= 1; else { found = true; break; } }
  }
  if (!found || s[j] !== ")") return null;
  const secondPart = s.slice(i + 1, j + 1); // "( … )"
  const link = (firstPart.slice(1, 5) === "http" && secondPart.slice(1, 5) === "http")
    ? firstPart.slice(1, -1) : null;
  return { start, end: j, link };
}

// fixTrelloLinks collapses every Trello smart-link `[url](url)` to the bare url.
function fixTrelloLinks(desc) {
  let result = "";
  let idx = desc.indexOf("](");
  while (idx !== -1) {
    const parsed = parseTrelloLink(desc, idx);
    if (parsed && parsed.link) {
      result += desc.slice(0, parsed.start) + parsed.link;
      desc = desc.slice(parsed.end + 1);
    } else {
      result += desc.slice(0, idx + 2);
      desc = desc.slice(idx + 2);
    }
    idx = desc.indexOf("](");
  }
  return result + desc;
}

// shareText builds the plain text handed to testers over chat: the screen-mode
// question (prefixed "Вопрос N.") plus any handout block, so what the players
// would see is reproduced. `number` comes from numberQuestionCards.
function shareText(desc, number) {
  const blocks = parseBlocks(desc);
  const parts = [];
  for (const b of blocks) {
    if (b.type === "handout") parts.push("Раздаточный материал:\n" + screenText(b.text));
  }
  const q = screenText(questionText(desc));
  parts.push((number ? `Вопрос ${number}. ` : "") + q);
  return parts.join("\n\n");
}

export const xyChgk = {
  parseBlocks, numberDirective, questionText, blockText, previewText,
  isZeroNumber, numberQuestionCards,
  removeAccents, removeSquareBrackets, screenText, shareText, parse4sElem,
  printRuns, renderRuns, splitList, applyOverride, replaceNoBreak,
  fixTrelloFormatting,
};
if (typeof window !== "undefined") window.xyChgk = xyChgk;
