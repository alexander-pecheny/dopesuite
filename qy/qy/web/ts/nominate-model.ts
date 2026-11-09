// The pure half of nominate.ts: what a slot's source box holds, and its
// question as a 4s card for the preview.
import { chgk4s } from "../../../../chgksuite/web/ts/chgk.js";

const { applyOverride, composeFields } = chgk4s;

const gotQuestionsRe = /^https?:\/\/(?:www\.)?gotquestions\.online\/question\/\d+/;

export function isGotQuestions(link: string): boolean {
  return gotQuestionsRe.test(link.trim());
}

// A post in a public telegram channel, as platform/telegram reads it.
const telegramPostRe = /^https?:\/\/(?:www\.)?(?:t\.me|telegram\.me)\/(?:s\/)?[A-Za-z0-9_]{4,}\/\d+/;

// isLoadable says the form can load the question by its link.
export function isLoadable(link: string): boolean {
  return isGotQuestions(link) || telegramPostRe.test(link.trim());
}

// isSingleLink says the source box holds one link and nothing else, which
// the form loads rather than parses.
export function isSingleLink(raw: string): boolean {
  const s = raw.trim();
  return /^https?:\/\/\S+$/.test(s);
}

// The fields the parse and load endpoints fill, as they name them.
export const PARSED_FIELDS = ["text", "handout", "answer", "zachet", "nezachet", "comment", "source", "author"] as const;
export type ParsedField = (typeof PARSED_FIELDS)[number];
export type Parsed = { ok: boolean; tournament?: string } & Record<ParsedField, string>;

const numberedRe = /^\s*\d+[.)]\s+/;
const imageLinkRe = /^https?:\/\/\S+\.(?:jpe?g|png|gif|webp)(?:\?\S*)?$/i;

// sourceItems splits the source field into its sources: numbered lines each
// start one, and anything else is a single source.
export function sourceItems(source: string): string[] {
  const lines = source.split("\n");
  if (!lines.filter((l) => l.trim()).every((l) => numberedRe.test(l))) return [source.trim()];
  return lines.map((l) => l.replace(numberedRe, "").trim()).filter(Boolean);
}

// imageLinks are the handout's lines that link a picture; the preview shows
// each at its own address.
export function imageLinks(handout: string): string[] {
  return handout.split("\n").map((l) => l.trim()).filter((l) => imageLinkRe.test(l));
}

// fourS writes the form's fields as a 4s card, the text chgksuite's question
// renderer reads: a picture's link in the handout becomes an (img …), a
// numbered source field a list of sources, and the author field's "!!Label"
// override its caption.
export function fourS(f: Record<ParsedField, string>): string {
  const val = (v: string): string | null => (v.trim() ? v.trim() : null);
  const handout = f.handout.trim()
    .split("\n").map((l) => (imageLinkRe.test(l.trim()) ? `(img ${l.trim()})` : l)).join("\n");
  const author = applyOverride(f.author.trim());
  return composeFields({
    question: val(f.text),
    handout: handout ? { kind: "text", text: handout } : null,
    answer: val(f.answer), zachet: val(f.zachet), nezachet: val(f.nezachet), comment: val(f.comment),
    sources: f.source.trim() ? sourceItems(f.source) : null,
    authors: author.text ? [author.text] : null,
    authorLabel: author.label,
  });
}
