---
status: accepted
date: 2026-08-24
---

# Every ranking scope ranks the same way

A round-robin группа said how it ranked its Participants — `points` for each
бой's outcome, the Protocol's metrics summed, the scheme's Scoring rules over
those sums, then `sorting`. Nothing else did. A финал played as a series of
бои was a bracket step: `best_of` emitted N бои into a `matches` stage, which
ranks nobody, and who had won came out of `eliminationStandings` counting
Losses — majority of бои, spelled in Go.

Троечка's финал is three бои decided by the summed рейтинговый балл, and its
регламент says so out loud: «победа в первых двух боях не гарантирует общую
победу». There was no way to write it. Two smaller walls stood beside it: the
DSL parsed `points` as integers, so the 1 / **0.5** / 0 the same регламент pays
was inexpressible; and a Scoring rule on a two-seat группа diverted it onto
`multiSeatStandings`, which has neither личная встреча nor разница, so the
рейтинговый балл and the cross-table could not coexist.

## Decision

- The two-seat table is `duelStandings` (`structure/duel.go`), taking the
  win/draw/loss values, the Protocol metric забито counts, the comparator
  order and the Scoring rules. `rr` calls it for a группа of duels; the new
  `series` Kind calls it for a Round the same two Participants play several
  times. A Scoring rule now rides **on** that table instead of replacing it —
  the multi-seat path is for бои of more than two seats and nothing else.
- `best_of` emits a `series` stage rather than a `matches` one, carrying the
  block's `points`, `metric`, `sorting` and rules. «До большинства побед» is
  its default — `points: [1, 0, 0]` sorted on points — so брейн's финал means
  exactly what it meant, and Троечка writes four lines instead.
- `points` parses fractions. `rr` and `se` take `metric`, naming the Protocol
  metric забито counts when it is not `taken`.
- `se` accepts a bracket seeded straight into its final: with `participants: 2`
  and `bronze: true` it consumes four out of the previous block's two Groups,
  the Group winners meeting in the финал and the places below them in the матч
  за 3-е место. `best_of.bronze` plays that as a series too.

## Considered options

Giving Троечка its own Kind, or a `decided_by:` key read only where a series
exists, were both rejected for the same reason: they add a third spelling of a
thing the DSL already says. The vocabulary was never missing — `points`,
`standings.<metric>` and `sorting` describe the регламент exactly. What was
missing was a series consulting them.

## Consequences

- A series stage ranks itself, so `RanksItsOwnStage` is true for it and the
  Сетка draws it as a table rather than as its бои. A tournament that reads
  its финал off the бои says `rollout`, which emits the same `matches` stage
  as before and computes no standing for it. СтудЧР's брейн carries it, so the
  conformance replay compiles byte-identically to what it compiled before.
- Nothing computed a series' winner before this — the бои sat there and a
  person read them — so the table is new information rather than different
  information, and no advancement moves either way: a series is terminal.
- A two-seat block's standings gain `place_sum` and `bouts`, which the
  multi-seat table already carried. Nothing sorts on them unless a scheme says
  so, and a comparator that ascends now ascends on both paths.
