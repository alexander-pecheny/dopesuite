// kindlabel.ts — what xy's board calls each Card Kind on screen. The kinds
// themselves and their rules are chgksuite's (chgk/cardkind.js).
import S from "./i18nstrings.js";
import { KIND } from "./chgk/cardkind.js";

// label is what a kind is called on screen, or null for a kind nobody picks or
// sees by name.
export function label(kind: string | null | undefined): string | null {
  switch (kind) {
    case KIND.question: return S.board.card.kindQuestion();
    case KIND.theme: return S.board.card.kindTheme();
    case KIND.troikaTheme: return S.board.card.kindTroikaTheme();
    case KIND.battle: return S.board.card.kindBattle();
    case KIND.meta: return S.board.card.kindMeta();
    case KIND.heading: return S.board.card.kindHeading();
    case KIND.other: return S.board.card.kindOther();
    case KIND.handoutsPreamble: return S.card.kind.handoutsPreamble();
    default: return null;
  }
}
