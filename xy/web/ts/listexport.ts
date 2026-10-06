// listexport.ts — what a List exports as: the 4s document, the game it is
// composed as, and the .hndt of its handouts, all assembled from the List's
// Cards. The export modal, the handouts panel and the board's handout badge all
// read it here.
//
// The reference is Go's internal/listexport, which xy-cli exports through. This
// copy stays in the browser because a bare .4s is written offline and the badge
// is drawn on every render. Its corpus, internal/listexport/testdata/cases.json,
// is written by the Go side (`go test ./internal/listexport -update`), and
// jstest/listexport_parity.test.js holds this file to it. A rule changed here
// is changed there first.

import S from "./i18nstrings.js";
import { bracketSpans, dropHidden, imgInText, isHandoutBody, numberQuestionCards, opensQuestion, parseBlocks, questionText, startsBlock } from "./chgk.js";
import type { ChgkCard, Handout } from "./chgk.js";
import { xyVersions } from "./versions.js";
import { xyHndt } from "./hndt.js";

// A Card as the assembly reads it, in board order.
export interface ExportCard extends ChgkCard { id?: number; handoutMeta?: string | null }

// ---- the 4s ----

// exportSource is the Cards' descriptions in board order, blank-line separated.
// Every format is rendered from this one string, which is why the Versions are
// folded back into one question block here and nowhere else: a versioned Card
// is still one numbered question. A handouts preamble is for the handouts alone.
function exportSource(cards: ReadonlyArray<Pick<ExportCard, "desc"> & { kind?: string }>): string {
  return cards.filter((c) => c.kind !== xyHndt.PREAMBLE_KIND).map((c) => foldBlankLines(withQuestionMarker(c.kind, withKindMarker(c.kind, xyVersions.composeVersions(c.desc).trim())))).filter(Boolean).join("\n\n") + "\n";
}

// exportGame is the game a scope exports as: one Theme makes it SI, whatever
// the List Type says, since a theme's name and author survive only an SI compose.
function exportGame(cards: ReadonlyArray<{ kind?: string }>): string {
  return cards.some((c) => c.kind === "theme") ? "si" : "chgk";
}

// The card reads text with no marker at the top of a question, or right under a
// theme's `№`, as the question itself (splitFields). 4s does not: there it
// continues the element above it, so the question has no `?`, never closes, and
// swallows everything after it up to the next theme. Such text gets its `?`.
function withQuestionMarker(kind: string | undefined, desc: string): string {
  if (kind !== "question" && kind !== "theme") return desc;
  const lines = desc.split("\n");
  const typeOf = (l: string): string | undefined => parseBlocks(l)[0]?.type;
  // Each part is one question: the lines under a `№`, or for a question card
  // also the lines above the first one. A theme's head is not a question.
  const starts = lines.flatMap((l, i) => (typeOf(l) === "number" ? [i + 1] : []));
  if (kind === "question") starts.unshift(0);
  for (let k = 0; k < starts.length; k++) {
    const end = k + 1 < starts.length ? starts[k + 1] - 1 : lines.length;
    const part = lines.slice(starts[k], end);
    if (part.some((l) => typeOf(l) === "question")) continue;
    const first = part.findIndex((l) => l.trim() !== "");
    if (first < 0 || typeOf(part[first]) !== "pre") continue;
    lines[starts[k] + first] = "? " + part[first];
  }
  return lines.join("\n");
}

// A heading or meta card may be plain text: the board shows it by its
// kind, so nobody has to type a `###` or a `#`. 4s, though, drops a line that has
// no marker and follows nothing, so such a card gives its first line the marker
// its kind stands for. A heading becomes a `##` section: that is what restarts
// the theme count in SI, as the board's numbering does after a heading.
const KIND_MARKER: Record<string, string> = { heading: "##", meta: "#" };

function withKindMarker(kind: string | undefined, desc: string): string {
  const marker = kind ? KIND_MARKER[kind] : undefined;
  if (!marker || !desc || startsBlock(desc.split("\n")[0])) return desc;
  return `${marker} ${desc}`;
}

// A blank line inside a card is xy's own liberty: to 4s it ends the element, so
// everything past it — the rest of the question, and any field after it — falls
// out of the docx. Each one becomes chgksuite's explicit (LINEBREAK) on the end
// of the line before, which keeps the field one element and the empty line
// visible (the directive plus the newline the join keeps = two breaks). Before a
// marker the blank line separates nothing that is printed, so it just goes.
//
// With one exception: the blank line before a `№`. A theme card is a ladder of
// questions in ONE card, and a blank line is the only thing that ends a question
// — drop it and the parser merges the rungs into a single question numbered
// «1020», whose text is every question of the theme in a row and whose answer is
// every answer (#81). So that one stays a blank line.
function foldBlankLines(desc: string): string {
  const out: string[] = [];
  let blanks = 0;
  for (const line of desc.split("\n")) {
    if (!line.trim()) { blanks++; continue; }
    if (blanks && out.length) {
      if (opensQuestion(line)) out.push("");
      else if (!startsBlock(line)) out[out.length - 1] += "(LINEBREAK)".repeat(blanks);
    }
    out.push(line);
    blanks = 0;
  }
  return out.join("\n");
}

// ---- the .hndt (the port of chgksuite handouts 4s2hndt, over Cards) ----

function postprocessHandout(s: string | null | undefined): string {
  return dropHidden(s).replace(/\\_/g, "_").trim();
}

// handoutForCard extracts a question card's handout: a legacy standalone "> …"
// block, or the inline handout bracket (the "[<handout label>: …]" 4s
// construct — chgksuite-native, what 4s2hndt scans, and what the Fields editor
// composes), or one written without the bracket. Returns
// {kind:'image',name} | {kind:'text',text} | null.
function handoutForCard(desc: string | null | undefined): Handout | null {
  const blocks = parseBlocks(desc);
  const h = blocks.find((b) => b.type === "handout");
  if (h) {
    const name = imgInText(h.text);
    if (name) return { kind: "image", name };
    return { kind: "text", text: postprocessHandout(h.text) };
  }
  const q = questionText(desc);
  for (const [, , body] of bracketSpans(q)) {
    if (!isHandoutBody(body)) continue;
    const idx = body.indexOf(":");
    const text = idx >= 0 ? body.slice(idx + 1).trim() : body;
    const name = imgInText(text);
    if (name) return { kind: "image", name };
    return { kind: "text", text: postprocessHandout(text) };
  }
  return unbracketedHandout(q);
}

// unbracketedHandout is the handout of a question that opens with the label,
// or carries a picture, without the bracket. A parsed .docx usually arrives
// like this: the handout label on a line of its own, and the picture or the
// text under it. The label goes, a picture is the handout, and otherwise the
// whole text is offered for the author to cut down in the .hndt.
//
// chgksuite's own 4s2hndt is looser: any mention of a handout in the text counts,
// with a warning printed for whoever runs it. Here the answer lights the
// board's badge and offers the handouts panel with nobody to warn, so the label
// has to open the text (internal/listexport/hndt.go says the same).
const HANDOUT_LABEL = new RegExp(`^${S.chgk.label.handout()}[.:]?\\s*`, "i");
function unbracketedHandout(q: string): Handout | null {
  const name = imgInText(q);
  if (name) return { kind: "image", name };
  if (!HANDOUT_LABEL.test(q)) return null;
  const text = postprocessHandout(q.replace(HANDOUT_LABEL, ""));
  return text ? { kind: "text", text } : null;
}

// hndtBlock formats one .hndt block: a for_question header, the saved per-question
// settings (or the default), a blank line, then the live handout content (text or
// an `image: file` line).
function hndtBlock(number: string, handout: Handout, metaText: string | null | undefined): string {
  const meta = (metaText && metaText.trim()) ? metaText.trim() : xyHndt.HNDT_DEFAULT_META;
  const content = handout.kind === "image" ? `image: ${handout.name}` : handout.text;
  return `for_question: ${number}\n${meta}\n\n${content}`;
}

// hndtOf is what a list's cards generate: their display numbers and the .hndt
// document — the tour's preamble, then one block per question card that carries
// a handout, each under its saved settings — joined with chgksuite's "\n---\n".
function hndtOf(cards: ReadonlyArray<ExportCard>): { numbers: Array<string | null>; source: string } {
  const numbers = numberQuestionCards(cards);
  const preamble = cards.find((c) => c.kind === xyHndt.PREAMBLE_KIND)?.desc.trim();
  const blocks: string[] = [];
  cards.forEach((c, i) => {
    if (c.kind !== "question") return;
    // Version 1's handout, like every other reader outside the card editor. A
    // block per version would print two handouts under one question number, and
    // split-fit names its output by that number — the second would overwrite the
    // first in the zip.
    const handout = handoutForCard(c.desc);
    if (handout) blocks.push(hndtBlock(numbers[i] ?? String(i + 1), handout, c.handoutMeta));
  });
  return { numbers, source: [preamble, ...blocks].filter(Boolean).join("\n---\n") };
}

export const xyListExport = { exportSource, exportGame, hndtOf, handoutForCard };
