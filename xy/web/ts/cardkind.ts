// cardkind.ts — the Card Kinds and what each one does. Every "is this card
// numbered?", "does it go into the export?" and "which game is this tour?" is
// asked here, so a new kind is one row in the table and not an edit in every
// file that used to spell the answer out.
//
// The table is Go's internal/cardkind, which the server's allow-list and xy-cli
// read too. cardkind_gen.ts is generated from it (`go generate
// ./internal/cardkind`), so a rule changes there, never here.

import S from "./i18nstrings.js";
import { KINDS, type KindName } from "./cardkind_gen.js";

export type { KindName };
type Kind = (typeof KINDS)[number];

const byName = new Map<string, Kind>(KINDS.map((k) => [k.name, k]));

// kindOf is the named kind, or undefined for a name the table does not have.
function kindOf(name: string | null | undefined): Kind | undefined {
  return name ? byName.get(name) : undefined;
}

// The stored names, for the places that mean one kind in particular.
export const KIND = {
  normal: "normal", question: "question", test: "test", meta: "meta",
  heading: "heading", other: "other", theme: "theme", handoutsPreamble: "handouts_preamble",
} as const satisfies Record<string, KindName>;

// counter is the number sequence a kind is numbered in: "question" (1, 2, 3…
// across a tour) or "theme" (a count of its own). "" for an unnumbered kind.
export function counter(kind: string | null | undefined): "" | "question" | "theme" {
  return kindOf(kind)?.counter ?? "";
}

// numbered is whether a Card of this kind gets a number. Those are the Cards
// that hold question fields and that the List's head and the board's title
// count.
export function numbered(kind: string | null | undefined): boolean {
  return counter(kind) !== "";
}

// testable is whether a Test Session plays a Card of this kind, so whether the
// Tester List counts it. That is every numbered kind: a theme is tested as a
// whole and its Playings stay at the Card (ADR-0018), so a tester who saw a
// theme saw one unit of the tour, the same as one question.
export function testable(kind: string | null | undefined): boolean {
  return numbered(kind);
}

// exported is whether a Card of this kind goes into the 4s the package is
// rendered from. A handouts preamble goes only into the .hndt.
export function exported(kind: string | null | undefined): boolean {
  return kindOf(kind)?.exported ?? false;
}

// carriesHandouts is whether a Card's Handout goes into the .hndt and earns the
// board's handout badge. Only a question's: the generator keys a handout by
// question number, and every theme has a `№ 10`.
export function carriesHandouts(kind: string | null | undefined): boolean {
  return kindOf(kind)?.handouts ?? false;
}

// versioned is whether a Card of this kind shows and edits Versions.
export function versioned(kind: string | null | undefined): boolean {
  return kindOf(kind)?.versioned ?? false;
}

// exportMarker is the 4s marker a plain-text Card of this kind is given on
// export, "" for none.
export function exportMarker(kind: string | null | undefined): string {
  return kindOf(kind)?.marker ?? "";
}

// restartsThemes is whether a Card of this kind restarts the Theme count.
export function restartsThemes(kind: string | null | undefined): boolean {
  return kindOf(kind)?.section ?? false;
}

// setsBase is whether a standalone `№№ N` in a Card of this kind sets the
// question count for the Cards after it.
export function setsBase(kind: string | null | undefined): boolean {
  return kindOf(kind)?.setsBase ?? false;
}

// gameOf is the game a scope exports as: one Theme makes it SI, whatever the
// List Type says, since a theme's name and author survive only an SI compose.
export function gameOf(cards: ReadonlyArray<{ kind?: string | null }>): string {
  for (const c of cards) {
    const g = kindOf(c.kind)?.game;
    if (g) return g;
  }
  return "chgk";
}

// forListType is the kind the add-card button makes in a List of this
// type. The type decides that and nothing else.
export function forListType(type: string | null | undefined): KindName {
  return type === "si" ? KIND.theme : KIND.question;
}

// pickable is the kinds the card editor's kind menu offers, in its order.
export const pickable: ReadonlyArray<KindName> = KINDS.filter((k) => k.pickable).map((k) => k.name);

// label is what a kind is called on screen, or null for a kind nobody picks or
// sees by name.
export function label(kind: string | null | undefined): string | null {
  switch (kind) {
    case KIND.question: return S.board.card.kindQuestion();
    case KIND.theme: return S.board.card.kindTheme();
    case KIND.meta: return S.board.card.kindMeta();
    case KIND.heading: return S.board.card.kindHeading();
    case KIND.other: return S.board.card.kindOther();
    case KIND.handoutsPreamble: return S.card.kind.handoutsPreamble();
    default: return null;
  }
}

export const xyCardKind = {
  KIND, counter, numbered, testable, exported, carriesHandouts, versioned, exportMarker,
  restartsThemes, setsBase, gameOf, forListType, pickable, label,
};
