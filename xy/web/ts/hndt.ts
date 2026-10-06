// hndt.ts — reading a .hndt document back: its blocks, the per-question
// settings, the preamble, and the Fields form the handouts panel edits it in.
// Writing one out of a list's cards is listexport.ts's job.

// chgksuite/handouter/utils.RESERVED_WORDS: keys treated as block settings (vs
// free handout text) in the .hndt format.
const HNDT_RESERVED = new Set([
  "image", "for_question", "columns", "rows", "resize_image", "font_size",
  "font_family", "no_center", "raw_tex", "color", "handouts_per_team",
  "grouping", "rotate", "tikz_mm", "hspace", "vspace", "max_width",
  "question_label",
]);
const HNDT_DEFAULT_META = "columns: 3";

// splitHndtBlocks splits a .hndt document on lines that are exactly "---"
// (chgksuite split_blocks). Exactly: "--- " is handout text to chgksuite and to
// the server's renderer, so the form must not show it as a break.
function splitHndtBlocks(text: string | null | undefined): string[] {
  const parts: string[] = [];
  let cur: string[] = [];
  for (const line of String(text || "").split(/\r?\n/)) {
    if (line === "---") { parts.push(cur.join("\n")); cur = []; }
    else cur.push(line);
  }
  parts.push(cur.join("\n"));
  return parts;
}

// parseHndtBlock pulls {forQuestion, meta} out of one .hndt block: the
// for_question target plus the persistable settings (reserved keys other than
// for_question and the image content line), as `key: value` lines.
function parseHndtBlock(blockText: string | null | undefined): { forQuestion: string | null; meta: string } {
  let forQuestion: string | null = null;
  const meta: string[] = [];
  for (const line of String(blockText || "").split("\n")) {
    const i = line.indexOf(":");
    if (i < 0) continue;
    const key = line.slice(0, i).trim();
    if (!HNDT_RESERVED.has(key)) continue;
    const val = line.slice(i + 1).trim();
    if (key === "for_question") { forQuestion = val; continue; }
    if (key === "image") continue; // content, derived from the card
    meta.push(`${key}: ${val}`);
  }
  return { forQuestion, meta: meta.join("\n") };
}

// parseHndtMetaByQuestion maps each block's for_question number → its settings
// text, so the modal can persist edited settings back onto the matching cards.
function parseHndtMetaByQuestion(text: string | null | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const block of splitHndtBlocks(text)) {
    if (!block.trim()) continue;
    const { forQuestion, meta } = parseHndtBlock(block);
    if (forQuestion != null && forQuestion !== "") out[forQuestion] = meta;
  }
  return out;
}


// A tour's ///preamble block lives in a card of its own kind, whose description
// is the block itself.
const PREAMBLE_KIND = "handouts_preamble";
const PREAMBLE_MARKER = "///preamble";

function isPreambleBlock(block: string): boolean {
  return block.split("\n").find((l) => l.trim())?.trim() === PREAMBLE_MARKER;
}

// preambleOf is the document's ///preamble block, or null when it opens with none.
function preambleOf(source: string | null | undefined): string | null {
  const first = splitHndtBlocks(source).find((b) => b.trim());
  return first && isPreambleBlock(first) ? first.trim() : null;
}

// ---- the fields view's model ----
// One .hndt block as the form edits it: the settings lines in their own order
// (every reserved key but image, including the ones the form has no control
// for, which it passes through untouched), and the one handout it prints —
// text or a picture. A block with nothing in it is the page break chgksuite
// reads it as; the form shows none of those and gives them back as they were.
export interface HndtFormBlock {
  head: Array<[string, string]>;
  // The ///preamble block: its text holds its comment lines, and it has no handout.
  preamble: boolean;
  kind: "text" | "image";
  text: string;
  image: string;
  blank: boolean;
}

// parseHndtForm reads a .hndt document block by block, the way the Go
// generator does: a line is a setting only when what stands before its first
// colon is exactly a reserved key; every other line is handout text.
function parseHndtForm(source: string | null | undefined): HndtFormBlock[] {
  let seen = false;
  return splitHndtBlocks(source).map((raw) => {
    const preamble = !seen && isPreambleBlock(raw);
    if (raw.trim()) seen = true;
    const head: Array<[string, string]> = [];
    const text: string[] = [];
    let image: string | null = null;
    for (const line of raw.split("\n")) {
      const i = line.indexOf(":");
      const key = i >= 0 ? line.slice(0, i) : "";
      if (i >= 0 && HNDT_RESERVED.has(key)) {
        const val = line.slice(i + 1).trim();
        if (key === "image") image = val;
        else head.push([key, val]);
      } else if (!preamble || line.trim() !== PREAMBLE_MARKER) {
        text.push(line.trim());
      }
    }
    const body = text.join("\n").trim();
    return {
      head, preamble, kind: image !== null ? "image" : "text", text: body, image: image ?? "",
      blank: !preamble && !head.length && image === null && !body,
    };
  });
}

// hndtGet and hndtSet read and write one setting of a form block. Setting a
// key that is not there yet adds it after the others; null removes it.
function hndtGet(b: HndtFormBlock, key: string): string | null {
  const hit = b.head.find(([k]) => k === key);
  return hit ? hit[1] : null;
}
function hndtSet(b: HndtFormBlock, key: string, val: string | null): void {
  const i = b.head.findIndex(([k]) => k === key);
  if (val === null) { if (i >= 0) b.head.splice(i, 1); return; }
  if (i >= 0) b.head[i] = [key, val];
  else b.head.push([key, val]);
}

// composeHndtForm writes the form back as the document listexport.ts would
// have written: settings, a blank line, then the handout.
function composeHndtForm(blocks: ReadonlyArray<HndtFormBlock>): string {
  return blocks.map((b) => {
    if (b.blank) return "";
    if (b.preamble) return [PREAMBLE_MARKER, b.text, ...b.head.map(([k, v]) => `${k}: ${v}`)].filter(Boolean).join("\n");
    const head = b.head.map(([k, v]) => `${k}: ${v}`).join("\n");
    const content = b.kind === "image" ? (b.image ? `image: ${b.image}` : "") : b.text;
    return [head, content].filter(Boolean).join("\n\n");
  }).join("\n---\n");
}

export const xyHndt = { PREAMBLE_KIND, PREAMBLE_MARKER, preambleOf, parseHndtMetaByQuestion, parseHndtForm, composeHndtForm, hndtGet, hndtSet, HNDT_DEFAULT_META };
