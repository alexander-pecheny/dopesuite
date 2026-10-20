# Брейн and тройка follow the СИ precedent

chgksuite already reads and writes both games: a тройка is `--game troika`,
read by the SI parser's troika variant, and a брейн is `--game brain`, read
by the ChGK parser, which knows its `#B` battles. xy now holds them too.

**A тройка theme is a Theme with a Ladder of 1, 2, 3.** It is the card of
ADR-0018, with the same two rules: the ladder never moves, and nothing is
invented. It is a kind of its own, `troika_theme`, rather than a Theme in a
List typed тройка, because the game a scope exports as follows the cards and
not the List Type (board 272 keeps its SI themes in ChGK lists). The ladder a
new theme is written with is a column of the Card Kind table.

**A брейн keeps its questions as Cards.** A брейн question is a ChGK question:
it is tested, labelled, versioned and played one at a time. What sets a брейн
apart is the battle, so the new card is `battle`, a `#B` heading. It restarts
the Theme count in every game and, in a брейн, the question count, as
chgksuite's parse_4s does. A battle makes its scope a брейн game only while no
theme says otherwise, since SI and тройка packets have battles too.

## Consequences

- **The docx and the PDF print a брейн's battles.** chgksuite drops `#B`
  outside si_mode, which leaves a брейн as «Вопрос 1.» five times over with
  nothing between. The Go port prints them as Heading 1, and so diverges from
  the Python tool on `--game brain`. The тройка docx stays byte-for-byte
  with it (`docx/testdata/troika`).
- **A брейн tour has no раздатки.** Its numbers repeat in every battle, and
  the generator keys a handout by its number. The stats panel is off for the
  same reason.
- **pptx, openquiz, telegram and раздатки are greyed out on every game but
  ChGK**, as they were on SI: their composers drop themes and battles.
- **The 4s goes out as `.tr4s` or `.br4s`**, and an import of either reads
  its game off the extension, whatever the picker says.
- **An import makes a `##` section a Заголовок**, not a note: that is the kind
  that stands for `##` and restarts the Theme count. A theme stops folding in
  blocks at a battle, a round or a section, and at a note once it has its
  rungs.
