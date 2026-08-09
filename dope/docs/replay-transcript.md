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

Then one line per seat: `who | marks | Σ | место`.

- **marks** — five characters per theme, themes separated by spaces. `R` taken,
  `W` lost, `-` never played. `---R-` is «взял сороковку».
- **Σ** and **место** are what the sheet printed. They are asserted, not
  applied: dope scores the marks itself and has to agree. A shared place is
  written as a fraction — `1.5` when two finish level.
- A место written `-` is one the sheet **never printed**. ТПШ's письменный отбор
  prints Σ and leaves the order to a standings tab, which is a different thing
  from a бой's место: a бой shares its place between seats that tie. There is
  nothing to hold dope to, so only the Σ is checked — the ranking is checked
  where it decides something, in whom the next round seats.
- A place written `3!` was **set by hand**. A перестрелка breaks a tie with
  material the protocol grid never records, so that place cannot be derived from
  the marks: it is input, exactly as a Draw is. The replayer writes it as a Pin
  and does not assert it. Use it only where the grid genuinely cannot imply an
  order — marking an ordinary place as pinned turns a real check into a
  tautology.

  СтудЧР is the evidence that these really are перестрелки and not a sorting
  rule nobody wrote down. Across ЭК and личная СИ, seats level on Σ are ranked
  by the sheets sometimes toward the lower Σ+ and sometimes toward the higher —
  no ordering of the protocol's own metrics produces both.

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

`testdata/studchr2026/` holds ЭК, личная СИ and ТПШ, emitted by the scripts in
`scripts/studchr/` and replayed by `TestReplayStudchr*`.

## Not covered yet

Брейн does not fit. Its бой is a score («4 : 0») plus who took each question,
not a grid of themes, so it needs a second form of seat line. That arrives when
КИнСБФ is transferred.

ОД does not fit either, and for the opposite reason: it has no бои at all. Its
whole document is one grid of which teams took which question, held on the game
rather than on a бой, so there is no seating and no place to assert.
