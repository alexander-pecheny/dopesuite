---
status: accepted
date: 2026-08-19
---

# One spelling for the slot ref, the stage config, the Game's document and an elimination's ranking

The second architecture review (19 Aug 2026) found four things the domain
said more than once:

- The slot ref (`match_slots.source_type` + `source_ref_json`) was written
  typed from the scheme and read back as `map[string]any` in four places, each
  with its own `IntFromMap` walk; `flatgame` wrote its own ref inline.
- The stage config envelope (`stages.config_json`) was built as a map and read
  by four functions that each re-guessed where `questions`, `themes`, the
  reseed's `teams`/`bands`/`sources` and the Kind's own config sat.
- A Game's document — the flat document on the 'main' бой, else the game
  row's blob — was read by nine hand-written `COALESCE` queries and written by
  the same two-branch block in three places; the roster was folded into ОД and
  КСИ documents by twins keyed on `game_type`, bypassing the Protocol registry.
- Ranking, which ADR-0011 put in one place, had grown two more spellings: the
  single-elimination Kind recovered the round by parsing the match code and
  skipped any бой that was not two seats — while the glossary says an
  elimination Block is a Match of any seat count and the two Kinds differ only
  by how many Losses end a run — and the seed import sorted a table the
  Structure had already ranked by its own comparator. A third copy of
  `store.AssignComputedPlaces` sat in `server/main.go`.

## Decision

- **`store.SlotRef`** and **`store.StageConfig`** are the envelopes. Each is
  built from the scheme (`SlotRefOf`, `StageConfigOf`), parsed from a row
  (`ParseSlotRef`, `ParseStageConfig`) and asked what the readers used to
  compute (`Identity`, `DisplayLabel`, `KindConfig`, `Questions`, `Themes`).
  The stored bytes are unchanged; no row migrates.
- **`store.LoadGameDoc`** (by id, by code, all of a fest) is the one reader of
  a Game's document and says where it lives (`MatchID`); **`flatgame.SaveDocumentTx`**
  is the one writer, settling the бой when there is one. **`protocol.RosterFolder`**
  is how a flat Protocol takes the fest roster into its scheme and document;
  `roster.PropagateRosterTx` walks a fest's Games once through it, and
  `gamebuild` folds the roster into a pristine Game the same way.
- **`structure.eliminationStandings(lives, winningPlaces, results)`** ranks
  both elimination Kinds: alive first (fewer Losses first), then the eliminated
  by the round they fell in, later first, and within a round by total Losses,
  so a bronze бой splits the two semifinal losers. A survivor is unplaced until
  the Block is played out. **`structure.LessBy(rules)`** is the one comparator
  over Metrics; the reseed and the seed import use it, and a seed import with
  no `[init]` sorting rules takes the table's own order.

## Consequences

- Single-elimination standings changed in two visible ways: a бой of any seat
  count counts (it was skipped), and mid-play survivors show no place where
  they shared first. `SEConfig` gained `winning_places`.
- A seed import without sorting rules now seeds in the source table's order,
  not by «place, then the Protocol's Metrics» — the table already ranked by
  its Kind's rules, and a second sort could disagree with it.
- `store.FlatGameStateJSON`, `storeutil.SlotSource`, `storeutil.StageConfigJSON`,
  `store.SlotSourceLabel`, `IntFromMap`/`StringFromMap`, the four `ApplyRoster*`
  functions and the `main.go` ranking copy are gone.
- The document still has two homes by design — a flat Game's бой, any other
  Game's row — but one reader knows both; ADR-0014's note that a DSL
  `kind: flat` Game keeps its document on the row (and is not scored) stands.

## Addendum (19 Aug 2026, card F4): the resolver keeps the rules, the store keeps the SQL

- **`store.MatchOutcome`, `store.SlotOutcome`, `store.RankedEntry`** are the
  rows a Ranker sums and returns; `structure` aliases them, as it already did
  `SortRule`. **`store.LoadMatchOutcomes(ids)`** loads any number of бои in two
  statements (the resolver used to run one query per бой); **`store.WriteStandings`**
  is the one writer of `stage_standings` (nil clears).
- **`resolver.Sources`** is the seam advancement asks through: who took a place
  in a finished бой, who holds a rank in a source table. `dbSources` is the DB
  adapter; the tests' map is the other. `desiredOccupant`, `slotTransition`,
  `prerequisites` and `contenders` are pure over it and tested directly.
