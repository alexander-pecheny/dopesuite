# The Kind config as a type (#9)

Status: done 17 Aug 2026 (see «What was done» at the end). Written earlier as
the hand-off note; #1 had done half of it, #5 typed the Protocol's side.

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

## What was done (17 Aug 2026, branch `dope-refactor`)

- `structure` owns one exported type per Kind — `RRConfig` (schedule and
  cross-table rule in one struct; `RRPoints`), `FlatConfig`, `SEConfig`,
  `PodConfig`, `ReseedConfig`, `ManualConfig` — with the JSON tags that were
  on the wire. The compiler builds the struct and marshals it; the Kind
  unmarshals into the same one; a renamed field is a compile error on both
  sides. `Rules` rides as `*Rules` so an empty rule set stays off the wire.
- What the resolver adds at ranking time is not config any more:
  `Ranker.Standings(cfg, results, structure.Inputs{Seed, Contenders})`;
  `structure.Contender` replaces the two `reseedContender` copies.
  `resolver.KindConfig(raw)` only unwraps the `"config"` envelope
  (`storeutil.StageConfigJSON`) or hands back the envelope for a Kind whose
  config sits there (a reseed's `sort`), so both stored shapes still read.
- The Protocol's params stay a map — they are what `Protocol.Params()`
  declares, key by key — and one `compiler.stageConfig(kind, blk, rounds)`
  puts them beside the Kind's struct; the JSON round-trips through a map, so
  the wire is key-sorted exactly as before. The four production DSLs compile
  to the same scheme JSON before and after (parsed equality, all stages).
  `store.SchemeSortRule` is an alias of `store.SortRule`.
- `structure_test.go`'s `mustSchedule` and every `Standings` call build
  configs from the types (`seeds(1, 2, 3)` for entrants).
- Still crossing the seam untyped, on the client: `brain.ts` reads
  `entrants`, `order`, `points`, `questions`, `tiebreakQuestions` from the
  stage config for the crosstab's planned rows and the бой's row count;
  `ek.ts` reads `rules.bout.points` and `entrants` to draw the sheets'
  per-круг group table (`group-stats.ts` recomputes it, since the server's
  standings carry no per-круг split). Moving those onto the view is a
  server-side feature (per-круг standings, planned seats on `StageView`),
  not a typing change; a rename there still fails only at a tournament.
- One deliberate change: a block whose `sorting:` is an empty list used to
  write `"order":[]`, which the Group read as "sort by nothing"; the field is
  `omitempty` now, so it falls back to the canon order.
- Gates: `go test ./...` with `TestReplayStudchr*` unchanged;
  `TestKindConfigReadsBothStoredShapes`.
