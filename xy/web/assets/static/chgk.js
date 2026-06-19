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
// "№№ N" resets the running base (matching chgksuite). Non-question cards get
// null and don't consume a number. Returns an array aligned with `cards`.
function numberQuestionCards(cards) {
  let next = 1;
  const out = [];
  for (const c of cards) {
    if (c.kind !== "question") {
      out.push(null);
      continue;
    }
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
  }
  return out;
}

export const xyChgk = {
  parseBlocks, numberDirective, questionText, blockText, previewText,
  isZeroNumber, numberQuestionCards,
};
if (typeof window !== "undefined") window.xyChgk = xyChgk;
