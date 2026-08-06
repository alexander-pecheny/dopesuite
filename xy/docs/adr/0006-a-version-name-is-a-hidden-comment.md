# A version's name is a hidden comment

> **Amended by [ADR-0007](0007-a-version-is-a-whole-card-body.md).** The directive
> chosen here now separates the versions as well as naming them, and it sits on
> its own line rather than inside the question text.

ADR-0005 left a version with no name and nowhere to hang one. Editors asked for
one anyway: «полегче» and «посложнее» say in one word what a tab reading
«Версия 2» cannot. chgksuite 1.4.0b1 added `(hidden-comment …)`, an inline
directive whose payload reaches no rendering, so a name now has somewhere to
live that still costs no column, no migration and no sync verb — the first
`(hidden-comment xy-version: …)` in a version's text is that version's name. The
`xy-` prefix is a namespace, not decoration: chgksuite's own tests use
`typst: …` as their example payload, so the keyword space is not ours alone.

## Consequences

- A name reaches no docx, no PDF and no раздатка, so nothing a tester sees can
  carry it. The .4s does carry it, because the .4s is the source xy imports back
  rather than a rendering of it — an export that stripped names would quietly
  destroy them on the next round trip.
- The name may be written anywhere in the version's text, and Поля re-emits it as
  the segment's first line on every save. A hand-typed name is therefore never
  ignored, but it does move.
- Any hidden comment that does not open with the prefix is an ordinary note.
  Поля shows notes because its fields are raw 4s and it strips only the name;
  Просмотр, the board and the copy-for-testers buttons drop them.
- «Добавить версию» clones the wording without the name. Two tabs reading
  «полегче» distinguish nothing, and the clone is about to be rewritten anyway.
- A lone version's name is visible only in Текст: the tab strip still appears
  only once a card has two versions, so a plain question grows no chrome.
