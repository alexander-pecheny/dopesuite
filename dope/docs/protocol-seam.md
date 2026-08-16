# The Protocol seam: ask the Protocol, stop branching on strings (#5)

Status: open. Strength: worth exploring. Depends on nothing; pairs with
[kind-config-typing.md](kind-config-typing.md).

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
