# A СИ theme is one card holding a ladder of questions

xy was built for ЧГК, where the unit of editing is the question and a Card is
one. Своя игра is written a тема at a time: five questions on one subject, worth
10 through 50, and judged as a set — a theme where the 30 is easier than the 20
is a bad theme even if every question in it is a good question.

A List is therefore typed — `вопросы ОД` (the default) or `темы СИ` — and a Card
typed `theme` holds a whole one: a `#T` head, an optional theme-level `@` author
and `/` comment, then a LADDER of `№` blocks, each opened by its own point value
and holding the ordinary question fields.

Two rules follow, and everything else follows from them.

**The ladder never moves.** ↑ and ↓ swap two slots' CONTENT between their `№`
heads; the values themselves stay where they were. Points still follow position,
which is what the editor wants — but an imported theme numbered 10/30/50, or an
EK one running past 50, survives a nudge exactly as it arrived. Renumbering on
every edit would have been simpler and would have quietly rewritten questions
nobody touched.

**Nothing is invented.** Поля draws the slots the 4s holds and no others. The
creation template writes five blank ones (`№ 10`…`№ 50`, bare `?` and `!`), which
is what lets the 50 be written before the 10 and survive a reload; a theme that
came from an import, or from a card switched over from a question, opens as it
is, with «+ вопрос» to grow it. Changing a card's kind rewrites nothing.

A question's author is its own `@`, or the theme's when it names none: a theme
marked once counts correctly, and a mixed theme counts per question.

## Consequences

- **`themes.ts` is the algebra** (split / compose / swap / nextNumber /
  authorsOf), pure and jstest-covered, the sibling of `versions.ts`.
- **Versions are hidden on a theme card.** A Version is a whole alternative body
  (ADR-0007); on a theme that would mean duplicating four questions to reword
  one. Per-slot versions are the honest shape and are not built.
- **Labels and Playings stay at the Card.** «взяли» on a theme says the theme was
  played, not which of its five were taken — the grain an SI editor actually
  rebalances by. Recorded here as a known limitation, not an oversight: a
  per-slot verdict means a schema change on `card_labels` and a grain below the
  Card in the label filter, the mass actions, the лента, Bundle and xy-cli.
- **Раздатки are off on a СИ list.** The `.hndt` generator keys a question's
  settings by its number and every theme has a `№ 10`. The inline
  `[Раздаточный материал: …]` is content and still reaches the .docx and the
  .pdf; it is the PDF generation that is not modelled at this grain.
- **Only .si4s, .docx and .pdf set a theme.** chgksuite's `si_mode` is ported
  into `internal/chgk/docx` and `typstdoc` and oracle-tested against its own
  output; pptx, telegram, openquiz and markdown still drop `#T`/`#B`/`#R`, so the
  export modal greys them out rather than shipping a themeless package.
- **The 4s export is `.si4s`**, because that extension is how chgksuite's own CLI
  reads the game back (`composer_common.ext_to_game`).
