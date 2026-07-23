// trellomodel.ts — how a Trello card becomes an xy card. Pure rules, lifted out
// of import.ts so jstest can exercise them without a DOM or a Trello token;
// import.ts keeps the network and the encryption.

import { xyChgk } from "./chgk.js";

export type CardKind = "question" | "heading" | "meta" | "other";
export interface MappedCard { desc: string; alias: string | null; kind: CardKind }
export interface RawDescEdit { before: string; date: string; author: string }
export interface DescEdit { before: string; after: string; date: string; author: string }

// Markers that make a block part of a ЧГК question, whatever line it sits on:
// compose_4s puts "№ N" ahead of "? …", and an answer/zachet/source is as much
// a question's field as the question line itself.
const QUESTION_BLOCKS = new Set<string>([
  "question", "answer", "zachet", "nezachet", "comment", "source", "author", "handout", "num", "numnum",
]);
const HEADING_BLOCKS = new Set<string>(["heading", "ljheading", "section"]);
const META_BLOCKS = new Set<string>(["meta", "editor", "date"]);

// cardKind classifies a card by the chgksuite markers in its text. A Trello
// board holds plenty of cards that are neither a question nor a caption — notes,
// links, checklists — and reading those as questions makes them a numbered part
// of the package, so anything unmarked lands in «Другое».
export function cardKind(desc: string): CardKind {
  const blocks = xyChgk.parseBlocks(desc);
  if (blocks.some((b) => QUESTION_BLOCKS.has(b.type))) return "question";
  if (blocks.some((b) => HEADING_BLOCKS.has(b.type))) return "heading";
  if (blocks.some((b) => META_BLOCKS.has(b.type))) return "meta";
  return "other";
}

// mapCard turns a Trello card's title + body into the xy card fields. The title
// becomes the card's alias (its short label), the body its 4s text. A card with
// an empty body keeps the title as its text instead — and then carries no alias,
// which would only repeat what the card already shows.
export function mapCard(name: string | null | undefined, rawDesc: string | null | undefined): MappedCard {
  const title = String(name || "").trim();
  const body = String(rawDesc || "").trim();
  const desc = xyChgk.fixTrelloFormatting(body || title);
  return { desc, alias: body ? title || null : null, kind: cardKind(desc) };
}

// descEdits rebuilds a card's description history as xy desc_edit payloads.
// Trello records an edit as the value it replaced (data.old.desc), so the chain
// is walked backwards from what the card holds now: each edit's "before" is the
// next older edit's "after". `edits` are that card's description changes newest
// first (the order Trello returns actions in), `currentDesc` the text the card
// was imported with (already de-Trello'd); the result is chronological.
export function descEdits(edits: RawDescEdit[] | undefined, currentDesc: string): DescEdit[] {
  const out: DescEdit[] = [];
  let after = String(currentDesc || "");
  for (const e of (edits || [])) {
    const before = xyChgk.fixTrelloFormatting(e.before || "");
    out.push({ before, after, date: e.date || "", author: e.author || "" });
    after = before;
  }
  return out.filter((e) => e.before !== e.after).reverse();
}

export const xyTrello = { cardKind, mapCard, descEdits };
