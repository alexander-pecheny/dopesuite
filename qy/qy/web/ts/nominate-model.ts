// The pure half of nominate.ts: what a slot's source box holds.

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
