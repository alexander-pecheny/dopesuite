// themes.ts — the SI theme algebra, the sibling of versions.ts.
//
// A Card typed «An SI theme» holds one theme: a head (`#T` name, an optional
// theme-level `@` author and `/` comment) and a LADDER of questions, each opened
// by its own `№` line whose value is its point value. The ladder is the theme's
// order: slot 1 is what a tester sees first, and it is worth what its `№` says.
//
// Two rules everything else follows from:
//   • the ladder never moves. Reordering swaps two slots' CONTENT between their
//     `№` heads, so an imported 10/30/50 theme, or an EK one running to 100,
//     survives a nudge untouched.
//   • nothing is invented. What the 4s holds is what Fields draws; the creation
//     template writes five blank slots, and a card that came from elsewhere
//     opens as it is.
//
// The per-slot fields are chgk.ts's ordinary ones, so a question inside a theme
// is the same thing as an OD question everywhere but its number.

import { composeFields, splitFields, type CardFields } from "./chgk.js";
import { MARKERS } from "./markers_gen.js";
import S from "./i18nstrings.js";

export interface ThemeSlot {
  // The `№` value: "10".."50" on the standard ladder, "reserve1" past it, and
  // whatever an import brought otherwise. Never derived — always the 4s's own.
  number: string;
  fields: CardFields;
}

export interface Theme {
  name: string;
  author: string | null;
  comment: string | null;
  // Head lines the model doesn't name (`###`, `##`, `#`…), re-emitted verbatim.
  headExtra: string | null;
  slots: ThemeSlot[];
}

// SI_LADDER is what a full theme is worth, and the order «+ question» fills.
export const SI_LADDER = ["10", "20", "30", "40", "50"] as const;

const MARKER_OF: Partial<Record<string, string>> = (() => {
  const m: Partial<Record<string, string>> = {};
  for (const [marker, type] of MARKERS) if (!(type in m)) m[type] = marker;
  return m;
})();

const numberMarker = MARKER_OF["number"] ?? "№";
const themeMarker = MARKER_OF["theme"] ?? "#T";

// slotHead matches a line that opens a slot — the `№` marker and its value.
function slotHead(line: string): string | null {
  const t = line.trim();
  if (t === numberMarker) return "";
  return t.startsWith(numberMarker + " ") ? t.slice(numberMarker.length + 1).trim() : null;
}

// splitTheme reads a theme card's 4s. A description with no `№` line at all is a
// theme with no slots, which is exactly what Fields then draws.
export function splitTheme(desc: string | null | undefined): Theme {
  const lines = (desc || "").split(/\r?\n/);
  const headLines: string[] = [];
  const slots: ThemeSlot[] = [];
  let cur: { number: string; body: string[] } | null = null;
  for (const line of lines) {
    const n = slotHead(line);
    if (n !== null) {
      if (cur) slots.push({ number: cur.number, fields: splitFields(cur.body.join("\n")) });
      cur = { number: n, body: [] };
      continue;
    }
    if (cur) cur.body.push(line);
    else headLines.push(line);
  }
  if (cur) slots.push({ number: cur.number, fields: splitFields(cur.body.join("\n")) });
  return { ...splitHead(headLines.join("\n")), slots };
}

// splitHead reads the lines above the first slot: the `#T` name, the theme's own
// author and comment, and anything else verbatim. A line with no marker
// continues whichever of the three opened above it, as it does in any 4s field.
function splitHead(text: string): Omit<Theme, "slots"> {
  const head: Record<"theme" | "author" | "comment", string | null> = { theme: null, author: null, comment: null };
  const extra: string[] = [];
  let open: "theme" | "author" | "comment" | null = null;
  for (const line of text.split("\n")) {
    const m = leadingMarker(line);
    if (m === "theme" || m === "author" || m === "comment") {
      if (head[m] === null) {
        head[m] = afterMarker(line, MARKER_OF[m] ?? "");
        open = m;
        continue;
      }
    }
    if (m !== null) { extra.push(line); open = null; continue; }
    if (open !== null) { head[open] = (head[open] ?? "") + "\n" + line; continue; }
    if (line.trim() !== "") extra.push(line);
  }
  const trimmed = (v: string | null): string | null => (v === null ? null : v.trim());
  return {
    name: trimmed(head.theme) ?? "",
    author: trimmed(head.author),
    comment: trimmed(head.comment),
    headExtra: extra.length ? extra.join("\n").trim() : null,
  };
}

function leadingMarker(line: string): string | null {
  const t = line.trim();
  for (const [marker, type] of MARKERS) {
    if (t === marker || t.startsWith(marker + " ")) return type;
  }
  return null;
}

function afterMarker(line: string, marker: string): string {
  const t = line.trim();
  return t === marker ? "" : t.slice(marker.length).trim();
}

// composeTheme is splitTheme's inverse: head, then every slot under its own `№`.
// An empty slot keeps its head and its bare `?`/`!`, which is what holds its
// place in the ladder while the questions above it are still unwritten.
export function composeTheme(t: Theme): string {
  const out: string[] = [themeMarker + (t.name.trim() ? " " + t.name.trim() : "")];
  if (t.author !== null) out.push(marked(MARKER_OF["author"] ?? "@", t.author));
  if (t.comment !== null) out.push(marked(MARKER_OF["comment"] ?? "/", t.comment));
  if (t.headExtra && t.headExtra.trim()) out.push(t.headExtra.trim());
  for (const s of t.slots) {
    const body = composeFields(s.fields).trim();
    out.push(marked(numberMarker, s.number) + (body ? "\n" + body : ""));
  }
  return out.join("\n\n") + "\n";
}

function marked(marker: string, value: string): string {
  const v = value.trim();
  return v ? `${marker} ${v}` : marker;
}

// nextNumber is what «+ question» appends: the first unused rung of the standard
// ladder, then reserve1, reserve2… Never renumbers what is already there.
export function nextNumber(slots: ReadonlyArray<ThemeSlot>): string {
  const taken = new Set(slots.map((s) => s.number.trim()));
  for (const n of SI_LADDER) if (!taken.has(n)) return n;
  for (let i = 1; ; i++) {
    const n = S.fsource.theme.reserveNumber(String(i));
    if (!taken.has(n)) return n;
  }
}

// withSlot appends a rung: the next free point value, or the next reserve once
// the ladder is full. A new slot is blank but real — bare `?` and `!` hold its
// place in the ladder, so the 50 can be written before the 10.
export function withSlot(t: Theme): Theme {
  return { ...t, slots: [...t.slots, { number: nextNumber(t.slots), fields: splitFields("?\n!") }] };
}

// blankTheme is what the add-card button makes in a list typed SI: a nameless
// theme whose ladder is already the full 10–50, every rung blank. The blanks are
// real 4s — that is what lets the 50 be written before the 10 and survive a
// reload, and what an export ships as the visibly unfinished theme it is.
export function blankTheme(author: string | null): string {
  let t: Theme = { name: "", author, comment: null, headExtra: null, slots: [] };
  for (let i = 0; i < SI_LADDER.length; i++) t = withSlot(t);
  return composeTheme(t);
}

// swapSlots moves a question one rung: the two slots exchange their CONTENT and
// the ladder of `№` values stays exactly where it was. Out-of-range is a no-op.
export function swapSlots(t: Theme, i: number, j: number): Theme {
  if (i < 0 || j < 0 || i >= t.slots.length || j >= t.slots.length || i === j) return t;
  const slots = t.slots.slice();
  const a = slots[i], b = slots[j];
  slots[i] = { number: a.number, fields: b.fields };
  slots[j] = { number: b.number, fields: a.fields };
  return { ...t, slots };
}

// isFilled: a slot counts as written once it has question text. An answer alone
// is a note to oneself, not a question a tester could be asked.
export function isFilled(s: ThemeSlot): boolean {
  return (s.fields.question || "").trim() !== "";
}

// authorsOf is the authorship rule: a question's own `@` wins, and a theme
// marked once carries every slot that names nobody. What the author count reads.
export function authorsOf(t: Theme, s: ThemeSlot): string[] {
  const own = (s.fields.authors || []).filter((a) => a.trim() !== "");
  if (own.length) return own;
  return (t.author || "").trim() ? [t.author!.trim()] : [];
}

// themeName is the name a theme card is shown by, with no numbering — the number
// is the card's position and comes from numberCards.
export function themeName(desc: string | null | undefined): string {
  return splitTheme(desc).name;
}

// progress is the «3/5» a theme card wears on the board: how many of its slots
// carry a question, out of how many it has. A theme with more than five slots
// reports its own total, reserve included, rather than pretending to five.
export function progress(desc: string | null | undefined): { filled: number; total: number } {
  const slots = splitTheme(desc).slots;
  return { filled: slots.filter(isFilled).length, total: Math.max(slots.length, SI_LADDER.length) };
}

export const xyThemes = {
  splitTheme, composeTheme, nextNumber, withSlot, blankTheme, swapSlots, isFilled, authorsOf, themeName, progress, SI_LADDER,
};
