---
status: accepted
date: 2026-08-18
---

# A Game is built, seated and ranked in one place each; the schema is a list

The 18 Aug 2026 architecture review took the server side of dope where the
15 Aug review had not gone, and its six items are one decision in six parts:
every thing a Game is made of has one writer, and a Kind is one module on
both sides of the compiler.

- `games.Definition` carries a game type's page and init payload; the viewer
  route, the host route and the lockdown snapshot ask it (`4b8e217`).
- `domain/gamebuild` owns every way a Game's Structure comes to exist —
  `Create`, `Recompile`, `Materialise` (a pasted scheme, ADR-0006's escape
  hatch), `Clear` — through one `writeStructureTx`; the pasted-scheme
  importer clears the fest and calls it, the fest-creating importer is gone
  (`41c5e15`).
- `store.LoadMatchStates(MatchSelector)` reads any set of бои in four
  statements; there is no WHERE-string loader (`6e510b8`).
- A flat game (ОД, КСИ) is a Structure like every other: `domain/flatgame`
  is where its document is written, and every write seats the `main` бой
  from the document's team list (`protocol.Seater`), scores it and ranks its
  flat Block into `stage_standings`. A seed source is `imports.ImportSeeds`
  over one `SeedSource`; the `game` source reads the source Game's one table,
  re-sorted by `[init] sorting:` over any Metric its Protocol declares
  (`643bf30`).
- `storage/schema.Apply` runs `server/migrations.go` — the schema as a list of
  `Migration{Version, Name, Up}` — once each, in order; `testdata/schema.sql`
  pins what the list makes of an empty file, and `DOPE_REHEARSE_DB` walks a
  prod snapshot through it (`030d44e`).
- `structure.Macro` is a Kind's compile-time role: it declares its keys and
  expands one Block through the `structure.Block` the compiler adapts for it;
  the four Kinds live whole in `domain/structure`, `expandBlock` is a
  registry lookup, and the five studchr schemes are pinned as goldens
  (`6424b68`).

## Considered options

Reading `stage_standings` for seeds without seating flat games was proposed
and rejected: the sources people use are ОД and КСИ, whose `main` бой seated
nobody, so the table was empty for exactly them; scoring through the
Protocol instead would have kept a second ranking alive. Three interface
designs were drawn for the Kind seam — a compiler-side handle, a
structure-side `Block` interface, and a plan-as-data `Planner` whose emitter
owns every code — and the second was chosen: one type per Kind with all its
roles, at a byte-identity risk the goldens hold; the third stays open as a
later deepening of the same seam. Keeping the migration list in
`storage/schema` was rejected because four backfills call domain code and
storage may not import domain; the mechanism is storage's, the list the
server's.

## Consequences

- A host page or importer that writes `stages`/`matches` rows itself, a
  loader that takes SQL text, a second ranking of anything, or a
  `switch kind` in the compiler is a regression.
- A DSL `kind: flat` game still keeps its document on `games.state_json` (it
  has no `main` бой) and is not scored; folding it onto the flat match is
  the open follow-up of this decision.
- `structure.Block` has one adapter, the compiler's; a fake for
  `structure`'s own Kind tests is the second the seam is waiting for.
- A migration is a value: a new step takes the next number, goes at the end
  of the list, and is rehearsed on a prod snapshot before it ships.
