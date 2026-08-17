# The Protocol seam: ask the Protocol, stop branching on strings (#5)

Status: done 17 Aug 2026 (see «What was done» at the end). Written earlier as
the hand-off note; pairs with [kind-config-typing.md](kind-config-typing.md).

## The problem

`domain/protocol.Protocol` is narrow — `Code`, `Metrics`, `EmptyState`,
`Score` (`protocol.go:29`) — and its callers already assume more than it
declares, so they branch on the game type or protocol code by string:

- `schemedsl/compile.go:229` `protocolParams`: the DSL keys a game type
  accepts (`questions`, `themes`, `tiebreak_questions`, …) as a
  `map[gameType]map[dslKey]configKey`; `compile.go:450` hard-codes
  `games.BrainQuestionCount`.
- `storage/store/matchstate.go:54` `IsEKShaped`: which blob shape a match's
  state follows, decided from the game type.
- `domain/imports/seed.go:641, :671`: `gameType == "brain"`, `gameType != "ek"`
  to decide what "started" means and how a seed lands.
- `domain/structure/reseed.go:90–108`: reads `takenBase` by name, the metric
  the brain Protocol writes (`protocol/brain.go:23`).
- `protocol/ek.go:24` `Metrics()` says `total, plus, shootoutTotal, tiebreak`;
  `store/queries.go:89` writes `correct_50 … correct_10` as well. So the
  compile-time "does this scheme sort on a metric that exists" check is
  dishonest in both directions: `structure/rules.go:25` `DerivedMetrics`
  promises `h2h` and `losses` for every Kind, and a scheme sorting on them
  gets zeros.
- Three `insert into match_results`: `protocol/si.go:59`,
  `domain/scoring/scoring.go:62`, `store/queries.go:69` (plus
  `imports/ek.go:157`).

A new Protocol today touches five packages and gets no compile error for
what it forgets.

## The change

Widen the interface to what the callers assume, and delete the switches:

```go
type Protocol interface {
    Code() string
    // Params: the DSL keys this Protocol accepts and the config keys they set.
    Params() map[string]string
    // Metrics: every metric Score writes — the compile-time check reads this.
    Metrics() []string
    EmptyState(cfg json.RawMessage) (json.RawMessage, error)
    // Started: has a host entered anything (seed import's "started" rule).
    Started(state json.RawMessage) bool
    Score(cfg, state json.RawMessage) ([]structure.SlotOutcome, error)
    // WriteResults: the one insert into match_results.
    WriteResults(ctx, tx, matchID int64, outcomes []structure.SlotOutcome) error
}
```

`IsEKShaped` becomes a question to the Protocol (`Shape()` or simply the
`Score` that knows its own blob); `protocolParams` becomes
`protocol.Get(gameType).Params()`; `DerivedMetrics` splits into what a Kind
actually adds (each Ranker declares its own, or `structure.Ranker` gains
`Metrics()`), so `compile.go`'s check is `Protocol.Metrics() ∪
Ranker.Metrics()` and nothing else.

## Acceptance

- No `== "ek"` / `== "brain"` / `== "si"` outside `domain/games` and the
  Protocol implementations (grep the four files above).
- One `insert into match_results` in production code.
- A test that compiles a scheme sorting on a metric no Protocol or Kind
  writes and gets a compile error naming it; today's ЭК `correct_50` sort
  compiles because the check knows about it.
- `TestReplayStudchr*` (all four games) unchanged: the seam moves, the
  numbers do not.

## Traps

- `store/queries.go:57` `RecalculateMatchResultsForStateTx` writes the ЭК
  results from the scored *view* (correct counts, pins), not from
  `Protocol.Score`; read it and `matchview.go`'s apply path before moving the
  insert. The replay will tell you if Σ+ or the 50…10 columns drift.
- The КСИ Protocol (`si.go`) writes `tiebreak`; the ЭК one calls the same
  number `shootoutTotal` in the view. Do not rename in this change.
- `imports/seed.go`'s "started" rule blocks a re-seat; test both a brain and
  an ЭК game (`gameroster_test.go`, `seed_import_test.go` in hostpages).

## What was done (17 Aug 2026, branch `dope-refactor`)

- `protocol.Protocol` gained `Params() []Param` (DSL key → config field,
  bool or count, an optional default), `TeamBlob() bool` and
  `Started(state) bool`. `Register` announces a team-blob Protocol to the
  store (`store.RegisterTeamBlob`), whose `TeamBlobShaped(gameType)` reads
  that registry — the store is a leaf and cannot ask the Protocol itself;
  `IsEKShaped` is gone. `DBMatchState.ProtocolState()` is the
  document a Protocol scores: the projected MatchState for a team blob, the
  raw document otherwise.
- The compiler reads `protocol.Params(gameType)`; the brain's always-written
  `questions` is that Param's `Default`. `structure.DerivedMetrics` is gone;
  every `Ranker` declares `Metrics()` (rr: points, h2h, taken, conceded, diff,
  place_sum, bouts; reseed: place_sum, points_share, taken_share, taken_base,
  diff, draw; de: losses; flat: place; se: none) and the compiler checks a
  block's sorting against `Protocol.Metrics() ∪ RankerMetrics(kind) ∪ rules`
  — rr for a group, reseed for a reseed, flat for a flat game (its sorting
  was never checked before). ЭК's Score now writes and
  declares `correct_N` / `wrong_N`, so `sorting: [correct_50]` compiles.
- One insert: `scoring.RecalculateMatchResultsTx` scores every Protocol
  through `Score(nil, match.ProtocolState())` and writes place (pin over
  scorer), total, plus, tiebreak and the metrics. `LegacyResultWriter`, both
  `WriteResultsTx` and `store.RecalculateMatchResultsForStateTx` are deleted;
  ЭК's `correctCounts` arrays no longer land in metrics_json (nothing read
  them). The ЭК sheet importer sets the sheet's place as a pin instead of
  inserting a row.
- `imports/seed.go` and `gamebuild.Recompile` ask `protocol.Started` whether
  a бой has begun (brain: any mark; every other Protocol: no — an unfinished
  бой re-seats or rebuilds, as before; `gamebuild` used to ask the brain's
  rule of every game) and
  prunes the blob for every team-blob game (личная СИ included; it was ЭК
  alone). The reseed reads `structure.MetricTakenBase`, the same name the
  brain Protocol writes — a shared constant, not a config key.
- Left as they were, outside the note's four files: `imports/seed.go`'s
  seed-source dispatch (which flat games seed a scheme: КСИ, ОД),
  `overrides.go`'s three `gameType == "ek"`, `festview.go`'s brain score
  column, `storeutil/scheme_ops.go`.
- Gates: `TestSortingKnowsProtocolAndKindMetrics` (an unmeasured metric
  fails naming it; a reseed's share on a group and a group's разница on a
  reseed fail; ЭК `correct_50` compiles on both); protocol tests check every
  Protocol declares what it writes, the blob shape and brain's Started;
  `TestReplayStudchr*` unchanged.
