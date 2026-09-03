// hndt.ts — the .hndt side of handouts: generation (the port of chgksuite
// handouts 4s2hndt) and the read-back of per-question settings. The corpus in
// internal/chgk/fsource/testdata/parity.json holds one document this writes
// and the Go side parses.

import { blockText, bracketSpans, dropHidden, imgInText, isHandoutBody, numberQuestionCards, parseBlocks, questionText } from "./chgk.js";
import type { ChgkCard, Handout } from "./chgk.js";

// chgksuite/handouter/utils.RESERVED_WORDS: keys treated as block settings (vs
// free handout text) in the .hndt format.
const HNDT_RESERVED = new Set([
  "image", "for_question", "columns", "rows", "resize_image", "font_size",
  "font_family", "no_center", "raw_tex", "color", "handouts_per_team",
  "grouping", "rotate", "tikz_mm", "hspace", "vspace", "max_width",
  "question_label",
]);
const HNDT_DEFAULT_META = "columns: 3";

function postprocessHandout(s: string | null | undefined): string {
  return dropHidden(s).replace(/\\_/g, "_").trim();
}

// handoutForCard extracts a question card's handout: the inline
// handout bracket (the "[<handout label>: …]" 4s construct — chgksuite-native,
// what 4s2hndt scans, and what the Fields editor composes) or a legacy standalone
// "> …" block. Returns {kind:'image',name} | {kind:'text',text} | null.
function handoutForCard(desc: string | null | undefined): Handout | null {
  const blocks = parseBlocks(desc);
  const h = blocks.find((b) => b.type === "handout");
  if (h) {
    const name = imgInText(h.text);
    if (name) return { kind: "image", name };
    return { kind: "text", text: postprocessHandout(h.text) };
  }
  const q = questionText(desc);
  for (const [s, e, body] of bracketSpans(q)) {
    void s; void e;
    if (!isHandoutBody(body)) continue;
    const idx = body.indexOf(":");
    const text = idx >= 0 ? body.slice(idx + 1).trim() : body;
    const name = imgInText(text);
    if (name) return { kind: "image", name };
    return { kind: "text", text: postprocessHandout(text) };
  }
  return null;
}

// hndtBlock formats one .hndt block: a for_question header, the saved per-question
// settings (or the default), a blank line, then the live handout content (text or
// an `image: file` line).
function hndtBlock(number: string | number, handout: Handout, metaText: string | null | undefined): string {
  const meta = (metaText && metaText.trim()) ? metaText.trim() : HNDT_DEFAULT_META;
  const header = `for_question: ${number}\n${meta}`;
  const content = handout.kind === "image" ? `image: ${handout.name}` : handout.text;
  return `${header}\n\n${content}`;
}

// generateHndt builds the full .hndt document for a list. `cards` are the list's
// cards in order, `numbers` the parallel display numbers (numberQuestionCards),
// `metas` a map cardId → saved handout settings text. Only question cards that
// actually carry a handout produce a block; blocks are joined with "\n---\n"
// (chgksuite's delimiter).
function generateHndt(
  cards: ReadonlyArray<ChgkCard & { id: number }>,
  numbers: ReadonlyArray<string | null>,
  metas: Record<number, string> = {},
): string {
  const blocks: string[] = [];
  cards.forEach((c, i) => {
    if (c.kind !== "question") return;
    // Version 1's handout, like every other reader outside the card editor. A
    // block per version would print two handouts under one question number, and
    // split-fit names its output by that number — the second would overwrite the
    // first in the zip.
    const handout = handoutForCard(c.desc);
    if (!handout) return;
    const n = numbers[i];
    const number = n != null ? n : i + 1;
    blocks.push(hndtBlock(number, handout, metas[c.id]));
  });
  return blocks.join("\n---\n");
}

// splitHndtBlocks splits a .hndt document on lines that are exactly "---"
// (chgksuite split_blocks).
function splitHndtBlocks(text: string | null | undefined): string[] {
  const parts: string[] = [];
  let cur: string[] = [];
  for (const line of String(text || "").split(/\r?\n/)) {
    if (line.trim() === "---") { parts.push(cur.join("\n")); cur = []; }
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


// hndtOf is what a list's cards generate: their display numbers and the .hndt
// document with each card's saved settings — export and the Generate-handouts
// both start here.
function hndtOf(cards: ReadonlyArray<ChgkCard & { id: number; handoutMeta?: string | null }>): { numbers: Array<string | null>; source: string } {
  const numbers = numberQuestionCards(cards);
  const metas: Record<number, string> = {};
  for (const c of cards) if (c.handoutMeta) metas[c.id] = c.handoutMeta;
  return { numbers, source: generateHndt(cards, numbers, metas) };
}

export const xyHndt = { generateHndt, hndtOf, handoutForCard, parseHndtMetaByQuestion, HNDT_DEFAULT_META };
