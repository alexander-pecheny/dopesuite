# A Version is a whole card body

ADR-0005 made a Version a run of question text between `(PAGEBREAK)` directives,
and said outright that only the question could be versioned — never the answer or
the comment — because only the question was one field the directive could split.

That is not what a rewording is. A question rewritten «полегче» answers to a
different зачёт; a version built around a picture carries its own раздатка; a
question co-authored in one wording is one author's alone in the other. Switching
tabs changed the question and left every other field of a card that no longer
matched it, which is what the feature felt wrong doing.

A Version is now a WHOLE 4s body. A card's `description_enc` holds its versions
concatenated, each introduced by a standalone `(hidden-comment xy-version: имя)`
line — the directive ADR-0006 already chose for a name, now doing the separating
too. A card with one version carries no such line, so a plain question is stored
exactly as it was before any of this existed.

Every reader but the card editor is untouched: `parseBlocks` drops the separator,
so the board, the numbering, the раздатки and copy-for-testers all see version 1
and never learn the others are there. The card editor scopes all three views —
Просмотр, Поля and Текст — to the selected version, so the concatenation is never
on screen.

`(PAGEBREAK)` goes back to meaning a page break.

## Consequences

- **The export merges the versions back into one question** (`composeVersions`,
  called from `exportSource` and nowhere else), so a versioned card is still one
  numbered question and a tour of 12 exports as 12. The `?` field carries every
  wording, page-broken, each prefixed «Версия N: »; a field the versions agree on
  prints once; a field they disagree on prints every value, labelled «версия N: ».
  A field one version simply lacks counts as disagreement — inheriting it silently
  would put words in that version's mouth.
- **The label is always the number, never the name.** «полегче» above a question
  tells a tester how hard it is meant to be before they have tried it, so ADR-0006's
  promise — a name reaches no rendering — survives.
- **Structural leftovers come from version 1**: a `№` directive and anything else
  the field editor does not model belong to the question, not to a wording of it.
- **Timeline diffs are version-aware.** The bodies are near-duplicates, and token
  LCS across the whole card aligns version 2's words with version 1's and reports
  a change nobody made, so `desc_edit` diffs each version against its counterpart
  under its own heading.
- **The old cards convert on unlock**, whole-board, through the ordinary patch
  path with a `desc_edit` entry each. Content is ciphertext, so no server-side
  migration was ever possible. A card in the new form OPENS with a separator, and
  that first line is what tells the two apart.
- **Inside a versioned card, `(PAGEBREAK)` is a page break again.** In a card
  that has only one version it is still read as the old scheme and split on the
  next unlock — a conversion cannot both fire on legacy data and leave a lone
  card's page break alone, since neither carries a mark saying which it is. That
  is a deliberate trade for this board's data, where a page break has only ever
  meant a version; a question genuinely broken across pages is written as one
  version once the card has two.
- **A chgksuite pull over the Trello API receives the raw concatenation.** The
  server holds only ciphertext, so it cannot merge; the separator lines reach that
  one consumer. chgksuite drops a hidden comment written as a file's FIRST line
  (`structure` is still empty — `composer/chgksuite_parser.py`), so version 1's
  name is what such a pull loses. That is an upstream fix, not a reason to spell
  the separator differently.
