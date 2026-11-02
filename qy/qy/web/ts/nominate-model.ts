// The pure half of nominate.ts: which parts of a nomination slot are shown.

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

// needsFields says whether a slot's question is typed in: it is unpublished,
// or published somewhere gotquestions cannot read it from. A published slot
// shows no fields until its link says which.
export function needsFields(published: boolean, link: string): boolean {
  return !published || (link.trim() !== "" && !isGotQuestions(link));
}

// The fields the parse button fills, as the parse endpoint names them.
export const PARSED_FIELDS = ["text", "handout", "answer", "zachet", "nezachet", "comment", "source", "author"] as const;
export type ParsedField = (typeof PARSED_FIELDS)[number];
export type Parsed = { ok: boolean; tournament?: string } & Record<ParsedField, string>;
