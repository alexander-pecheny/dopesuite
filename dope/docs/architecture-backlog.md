# Architecture backlog — hand-off notes

The 15 Aug 2026 architecture review of branch `dope-brain` named ten
deepening candidates. All ten are done (four on `dope-brain`; #7, #6, #5,
#9, #8, 8′ and #10 on `dope-refactor`), each with a note in this directory
that ends in «What was done». The vocabulary is
`CONTEXT.md`'s for the domain (Fest, Game, Block, Round, Wave, Group, Сетка,
Бой, Protocol, Metric, Slot) and the `/codebase-design` skill's for the
architecture (module, interface, depth, seam, adapter, locality, leverage).
Decisions the user already made are recorded in the notes; do not reopen
them, and ask before widening a note's scope.

## Done

| # | what | commit |
|---|---|---|
| 1 | one Standings module: `structure.Expander` / `structure.Ranker`, the server ranks every Block (`stage_standings`), ships `sort` rules with each table; the client draws | `c98a5f4` |
| 3 | `domain/festview.Load` owns the fest view: `StageView.Kind/Sort/Grain`, `FestMatchView.Letter` | `7e6370f` |
| 2 | `domain/gamebuild` (`Spec` / `Create` / `Recompile` / `Rebuild`) out of the host pages | `cba2d5f` |
| 4 | one ЭК page, `web/ts/ek.ts`, host and spectator gated on a `viewer` flag | `0bcf14a` |
| — | Сетка geometry as tokens + `classcheck` geometry lint + verify matrix (from the 16 Aug phone review) | `74b1379` |
| 7 | one skin: the Сетка's group table is a бой box (`grid-box` / `grid-slot-cell`), `standingsTable(spec)` + `resultsTeamCell` build every standings table and name cell | `8ad113c` |
| 5 | the Protocol declares `Params`, `TeamBlob`, `Started`; every Ranker declares `Metrics`; one insert into match_results (`scoring`) | `12a2173` |
| 9 | one Go type per Kind config (`structure.RRConfig` …), the compiler writes it and the Kind reads it; `Standings` takes `structure.Inputs` for the seed and contenders | `7d905b7` |
| 8 | the studchr replay runs direct in 25 s on every `just test` (one driver, two transports; the engine's tx entry points exported from `editbatch`); `replay.Codec` per Protocol; the discrepancies page is generated | `2a9cbe8` |
| 6 | one `web/ts/game-tabs.ts`: `gameTabs(stages, {game, viewer, seeded})` for ЭК, брейн, КСИ, ЧГК; Blocks off `grain`, `blockLabel`/`groupLabel` the only «Группа» readers (the Сетка's column titles too) | `7bf4f85` |
| 8′ | `replay.StandingsReader` and the `[таблица s1/g3]` transcript section: 27 tables in the studchr transcripts, checked against `stage_standings`; found and fixed a boundary reseed of winners only, flat ranks shared on бой place, and ЭК's пересев rule | `9e21855` |
| 10 | `planGrid` / `packBlock` in `fest-grid.ts`: the Сетка's layout as data before its DOM, the painters read it; no module state, two grids on a page coexist | `4365b1b` |

## Open

Nothing. The notes stay as the record of what each item was and what it
became.

## How to work a note

- Grill first (`/grilling` with the note as the spec), then `/tdd` at the
  seams the note names, then `/code-review`, then commit. Commit messages
  pass `noslop`.
- Every note ends with acceptance and traps. The Go conformance gate is the
  studchr replay (`go test ./dope/server/tests -run TestReplayStudchr`; ~25 s
  on the direct transport, part of `just test`; the HTTP variants play under
  `just test-full`; ADR-0010);
  the JS gate is `deno test --parallel dope/web/jstest/`;
  UI work runs the verify skill's hand-over matrix and diffs against dopetest.
- Read `dope/CLAUDE.md` for the toolchain and `AGENTS.md` (root and dope) for
  the module map before touching a file the note names by line — the lines
  were true on 16 Aug 2026 and drift.
