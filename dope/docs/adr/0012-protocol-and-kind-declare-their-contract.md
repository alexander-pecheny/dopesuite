---
status: accepted
date: 2026-08-17
---

# A Protocol and a Kind declare their contract; nothing else names them

`protocol.Protocol` declares what a scheme may say about it and what it writes:
`Params()` (the DSL keys it takes, each with its config field, type and
default), `TeamBlob()` (whether its document is the per-team blob the store
projects), `Started(state)` (whether a бой has begun) and `Metrics()` (what
its `Score` measures). `structure.Ranker` declares `Metrics()` too, and every
Kind reads one exported config type of its own (`RRConfig`, `FlatConfig`,
`SEConfig`, `PodConfig`, `ReseedConfig`, `ManualConfig`) that the compiler
writes; what the resolver knows at ranking time — the game's lot seed, the
contenders — travels as `structure.Inputs`, not as config. One writer,
`scoring.RecalculateMatchResultsTx`, scores every Protocol and inserts
`match_results`.

The scheme DSL's vocabulary is CONTEXT.md's: a Block has a `kind` and
`participants` (never «team» — a Participant is a team or a player), a Group a
`group_size`, a бой a `match_size` and `winning_places` (the per-бой Loss
threshold, distinct from a Block's `proceeding_participants`); an elimination
Round is `r{N}` by its number in every dotted key, a halving bracket's last two
also `semifinal` and `final`. Each Kind takes its own keys and refuses the
rest, naming what it takes.

Items #5, #9 and the DSL pass of the 15 Aug 2026 review (`12a2173`,
`7d905b7`, `c24bc93`).

## Considered options

Before this the compiler carried a table of Protocol params by game name, the
resolver a `DerivedMetrics` list, the store an `IsEKShaped` predicate, and
three writers inserted `match_results` (the scorer, ЭК's and СИ's own
`WriteResultsTx`, and a `RecalculateMatchResultsForStateTx` in the store).
Adding a Protocol meant editing all of them; a metric a Protocol wrote could
not be sorted by until the compiler was told. Keeping the DSL's `teams` and
the two meanings of `r4` was rejected because a scheme is read by people:
`teams: 91` on ТПШ and `title.r2` naming the final of a 16-team bracket are
the kind of wrong that survives review.

## Consequences

- A new Protocol registers itself and declares Params, TeamBlob, Started and
  Metrics; the compiler, the store and the scorer learn nothing by name. Its
  metrics are rankable everywhere the moment it declares them
  (`sorting: [correct_50]`).
- A Kind's config is a Go type on both sides of the wire; a renamed field is
  a compile error, not a tournament-day surprise. `resolver.KindConfig` only
  unwraps the `"config"` envelope.
- The DSL rejects a key its Kind does not read, so nothing is dropped on the
  floor; unknown-key errors name the allowed set. No game in prod carries DSL
  text, so no alias for the old spellings exists — dopetest's fixture DSLs
  were rewritten in place.
- The metric spellings a Protocol writes into `metrics_json` (`taken50`,
  `correct_50`, `shootoutTotal`) are stored data and were left as they are.
