# The Kind config as a type (#9)

Status: open. Strength: speculative on 15 Aug; #1 did half of it (the
`Schedule(cfg, results)` interface split into `Expander.Schedule(cfg)` and
`Ranker.Standings(cfg, results)` / `Order(cfg)`), so what is left is the
typing alone. Best done together with [protocol-seam.md](protocol-seam.md).

## The problem

A Kind's config crosses four seams as an untyped map whose keys must match
struct tags by luck:

- `schemedsl/compile.go:434` `protocolConfig` and `:1819` build
  `map[string]any` with string keys (`"pairings"`, `"matchSize"`,
  `"entrants"`, `"lives"`, `"winning_places"`, `"sort"`, …).
- `structure/rr.go:26` `rrConfig`, `flat.go`, `se.go`, `de.go`, `reseed.go`
  each unmarshal `json.RawMessage` into their own struct with those tags.
- `resolver/resolver.go:230` `KindConfig` / `kindStageConfig` re-reads the
  stage's config to inject `seed` and the reseed's `contenders`, nesting
  under `"config"` for scheme-imported stages.
- `web/ts/fest-grid.ts:702` `parseScheme` and the brain page read stage
  config in TypeScript for the same keys.

Rename `matchSize` in one place and nothing fails until a tournament.

## The change

- One Go type per Kind, owned by `structure` (`structure.RRConfig`,
  `FlatConfig`, `SEConfig`, `PodConfig`, `ReseedConfig`), with the JSON tags
  that exist today. The compiler builds the struct and marshals it; the
  Ranker/Expander unmarshals into the same struct; the resolver adds
  `Seed`/`Contenders` as fields, not map keys.
- `store.SchemeStage.Config` stays `json.RawMessage` on the wire (the DB and
  the client read it), but every Go producer and consumer goes through the
  typed structs, so a rename is a compile error.
- The client reads `StageView.Kind/Sort/Grain` (#3) rather than parsing
  config; audit `fest-grid.ts` and `brain.ts` for what they still take
  from `config` and move it onto the view if anything is left.

## Acceptance

- No `map[string]any` for Kind config in `schemedsl` or `resolver`
  (Protocol params may keep theirs until #5 lands).
- A test renames a field and fails to compile — i.e. the compiler and the
  Kind share one type; `structure_test.go`'s `mustSchedule` builds configs
  from the type, not from JSON strings.
- `TestReplayStudchr*` unchanged.

## Traps

- Legacy schemes in the DB were imported with config nested under
  `"config"` (`kindStageConfig`'s outer struct); the reader must keep
  accepting both shapes or migrate them (`db.go` v24 filled legacy grain the
  same way).
- The reseed's config is assembled at resolve time (`seed`, `contenders`),
  not at compile time; keep that split.
