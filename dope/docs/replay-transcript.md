# Replay transcript

A transcript is what the hosts actually entered, бой by бой, written so a person
can read it. The replayer plays it through dope's real handlers and holds the
result against what the sheet claimed.

One format serves every tournament. Each has its own flavour of Google Sheets,
so each gets a small reader (`scripts/<tournament>/read-*.py`) that emits this;
the replayer never learns what an `.xlsx` is. Vocabulary — Block, Round, Wave,
Group, Draw — is defined in [CONTEXT.md](../CONTEXT.md); the decision to build
this at all is [ADR-0010](adr/0010-studchr-replay-is-the-conformance-harness.md).

## Example

```
# ЭК СтудЧР-2026 — reconciled against the sheet 2026-08-09
[game]
type: ek
title: ЭК
scheme: ek.dsl

[roster]
1 | Ктулху          | Москва
2 | ВШЭстером       | Санкт-Петербург

[s1/r1/w1/m1] жребий
Ктулху          | ----- ---R- RR--W | 120 | 1
ВШЭстером       | ----- ----- R---- |  10 | 4

[s1/r2/w1/m1]
Ктулху          | R---- ----- ----- |  10 | 2

override [s1/r2/w1/m1] место Ктулху: the sheet ranks him first on a lower Σ
```

## Header

`[game]` takes three keys: `type` (`ek`, `si`, `od`, `brain`), `title` for a
human, and `scheme` — the scheme file the Structure is built from.

## Roster

`[roster]` is one line per entrant: `number | name | city`. The city is
optional. The separator is a bar rather than whitespace because «Ушки на
макушке Казань» has no unambiguous reading without one.

## Составы

`[составы]` is one line per team — `Ктулху | Иван Петров, Анна Ким` — its
players comma-separated, in roster order. It is input: the replay registers
them before the first бой, and the theme players below are checked against it
at the door. A personal game has no `[составы]`: the участник is the player.

## Статистика

`[статистика]` is the sheet's own per-player aggregates, asserted after the
last бой the way Σ and место are asserted after each one: dope adds the бои up
itself and has to agree, player by player, both ways. A line is
`Игрок | Команда | a | b | c` (no team column in a personal game), and what
the three numbers mean is the game's affair:

- ЭК — Σ, positive themes, themes played;
- брейн — попытки, верно, неверно, counted over the regular questions
  (перестрелки stay out, as the sheet leaves them out);
- личная СИ — Σ, Σ+ and бои.

A stats disagreement the author has ruled on is silenced by
`override [статистика] поле игрок: причина` — the section name standing in for
the бой coordinate, because an aggregate holds the whole game.

## Таблица

`[таблица s1/g3]` is the sheet's standings of one Group — or of a Block,
`[таблица s1]`, where the Block has one table: a flat отбор, a пересев — one
`место | участник` line per row, asserted after the last бой the way
статистика is: dope ranks the Block itself and has to agree, both ways. The
место is the one the sheet printed, or the row where it printed none — so a
pair level on every key the Block sorts by is a disagreement the sheet's author
rules on (`override [таблица s1] место Трубечкова Вероника: …`), since dope
shares such a place and a sheet numbers on. A Block holding several tables
(a пересев before every round) cannot be named this way yet.

## Бой

A бой's header is its coordinate: `[block/round/wave/match]`, e.g.
`[s1/r3/w2/m4]` — or `[block/group/round/wave/match]` in a Block that has
Groups, e.g. `[s1/g3/r2/w1/m4]`. Round, wave and match are always required.

The coordinate is how a sheet's бой joins dope's. They cannot be joined by who
sat at the table, because the seating is the thing under test.

The Group is not decoration. All six групп of личная СИ play круги 1–4 in Block
s1, so without a group number one coordinate names six different бои and five of
them go unchecked. Inside a Group the бои of one круг are заходы — a Group holds
one стол and plays them one after another — so личная СИ's group stage addresses
`s1/g3/r2/w1/m1`, not three matches of one wave.

`жребий` after the coordinate marks a Draw — this table was set by a person, not
derived: the opening round of a bracket, the deal into groups, a swap for a
no-show. That seating is input, and the replayer writes it into the Edges before
play. Without `жребий` the seating is the resolver's, and the replayer asserts it
seated exactly these participants.

Then one line per seat: `who | marks | Σ | место`, with an optional fifth
field in ЭК naming who played each theme — comma-separated, aligned with the
marks, `-` where the sheet named nobody. Each named player must be in his
team's `[составы]`.

- **marks** — five characters per theme, themes separated by spaces. `R` taken,
  `W` lost, `-` never played. `---R-` is «взял сороковку».
- **counts** — Троечка's form: one digit per вопрос, вопросы grouped by тема,
  `.` for a вопрос nobody took. `131 ..1` is «первый вопрос взял один, второй
  все трое, третий один; в следующей теме только третий». All three players
  answer every вопрос and each correct answer pays on its own, so what the sheet
  records is a count — it never says which кресло — and the кресла are
  synthesized on replay the way a перестрелка's marks are.
- **Σ** and **место** are what the sheet printed. They are asserted, not
  applied: dope scores the marks itself and has to agree. A shared place is
  written as a fraction — `1.5` when two finish level.
- A место written `-` is one the sheet **never printed**. ТПШ's письменный отбор
  prints Σ and leaves the order to a standings tab, which is a different thing
  from a бой's место: a бой shares its place between seats that tie. There is
  nothing to hold dope to, so only the Σ is checked — the ranking is checked
  where it decides something, in whom the next round seats.
- A place written `3!` was **set by hand**. Use it only where the grid genuinely
  cannot imply an order and no перестрелка line covers it — marking an ordinary
  place as pinned turns a real check into a tautology. The replayer writes it as
  a Pin and does not assert it.

  СтудЧР is the evidence that ties split by something outside the grid really
  are перестрелки and not a sorting rule nobody wrote down. Across ЭК and личная
  СИ, seats level on Σ are ranked by the sheets sometimes toward the lower Σ+
  and sometimes toward the higher — no ordering of the protocol's own metrics
  produces both.
- `перестрелка Ктулху: 60` inside a бой is that seat's **net перестрелка
  points** — extra material the theme grid never records, held outside Σ, the
  thing that split the tie. It is input: the replayer writes it into the blob's
  shootout theme (the marks are synthesized greedily from the nominals — total
  faithful, composition invented, since the sheets keep only the net) and the
  game ranks by Σ, then by it, so the sheet's места stay asserted. Zero is never
  written — a seat with no line nets zero.

The table is checked both ways: that everyone the sheet lists played, and that
dope seated nobody the sheet didn't.

Strictness at the door is the same idea. A бой with no seats, two entries at one
coordinate, a participant absent from `[roster]` — all are parse errors rather
than quietly accepted data, because an oracle that swallows a truncated
transcript reports success over work it never did. A `#` opens a comment only at
the start of a line, so a team called «Решётка #1» keeps its name.

## Disagreements

```
override [s1/r2/w1/m1] место Ктулху: the sheet ranks him first on a lower Σ
override [s1/r2/w1/m1] посадка: judges swapped the tables after a no-show
```

This is the tournament's author saying: here the sheet is not to be believed,
and why. The reason is mandatory — a disagreement without one is not a
disagreement but a silenced defect. Anything that diverges without an `override`
halts the replay.

Name the participant after the field. Without a name the override covers that
field for the whole table, so a real defect in the other three seats would pass
unreported; write it unnamed only for what genuinely concerns the whole бой,
like the seating.

An `override` that silenced nothing is itself reported: it asserts a defect that
isn't there, and on the discrepancies page it reads as a reviewed case.

The rule that matters: an `override` is written by the author of the sheets, not
by the implementer. When dope is the one that diverges, the code or the scheme
gets fixed.

A seating disagreement is the special case. Organisers really do move tables
mid-tournament — a no-show, a late arrival, a decision on the day. That is
neither our defect nor the sheet's, but a Draw we didn't know about, so the бой
gains `жребий` and its seating turns from an assertion into an input.

`docs/studchr2026-discrepancies.md` is generated from these lines by
`replay.Discrepancies`; it is never edited by hand.

## What is written down

`testdata/studchr2026/` holds ЭК, личная СИ, ТПШ and брейн — 263 бои and 27
tables — emitted by the scripts in `scripts/studchr/` and replayed by
`TestReplayStudchr*`.

## Брейн

Брейн does not play themes: its бой is a duel over buzzer questions, and what the
protocol records for each is who buzzed. So a брейн seat line lists its questions
instead — comma-separated, because a player's name has a space in the middle of
it and a бой has none to spare.

```
[s1/g1/r1/w1/m1]
Рыб'ending | R Виктория Корнеева, -, R Санжи Сундуев, W Тимофей Маркин | 3 | 1
Постпопс   | -, W Нина Андреева, -, R Нина Андреева | 1 | 2
```

Each entry is `-`, a bare mark, or a mark and whoever took it. Both sides can be
marked on one question: in брейн one team buzzes and misses, then the other does.
Questions past the бой's own are перестрелка — the sheets' «П» rows — and they
are appended to both sides exactly as the «+ П» button does.

Σ is the score, which for брейн is the questions a side took rather than points.
The two forms are not interchangeable, and a бой written in the wrong one is a
parse error rather than a transcript of a game nobody played.

## Not covered yet

ОД does not fit, and for the opposite reason to брейн: it has no бои at all. Its
whole document is one grid of which teams took which question, held on the game
rather than on a бой, so there is no seating and no place to assert.
