# Architecture backlog — hand-off notes

The 15 Aug 2026 architecture review of branch `dope-brain` named ten
deepening candidates. Six are done (four on `dope-brain`, #7 and #6 on
`dope-refactor`); four are open, each with a note in this directory written
for a fresh agent. The vocabulary is
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
| 6 | one `web/ts/game-tabs.ts`: `gameTabs(stages, {game, viewer, seeded})` for ЭК, брейн, КСИ, ЧГК; Blocks off `grain`, `blockLabel`/`groupLabel` the only «Группа» readers (the Сетка's column titles too) | `7bf4f85` |

## Open

| # | note | strength (15 Aug) | depends on |
|---|---|---|---|
| 5 | [protocol-seam.md](protocol-seam.md) — widen `protocol.Protocol`, delete the string switches | worth exploring | — |
| 8 | [replay-adapters.md](replay-adapters.md) — a Structure-level `replay.Game` adapter, coordinates and standings on the interface, one transcript codec per Protocol | worth exploring | #1, #2 (done) |
| 9 | [kind-config-typing.md](kind-config-typing.md) — the Kind config as a Go type the DSL emits and the resolver reads | speculative | #1 (done, halved it) |
| 10 | [fest-grid-planner.md](fest-grid-planner.md) — a pure layout planner in front of the Сетка painter | speculative | do with #7 or not at all |

Suggested order: 5 and 9 together (both about what a Protocol and a Kind
declare), then 8. 10 only if the grid changes anyway; #7 rebuilt the group
table as a бой box, so a planner would now front one box kind, not two.

## How to work a note

- Grill first (`/grilling` with the note as the spec), then `/tdd` at the
  seams the note names, then `/code-review`, then commit. Commit messages
  pass `noslop`.
- Every note ends with acceptance and traps. The Go conformance gate is the
  studchr replay (`just test-full`, or `go test ./dope/server/tests -run
  TestReplayStudchr`; ~90 s; skipped by `just test`'s `-short`; ADR-0010);
  the JS gate is `deno test --parallel dope/web/jstest/`;
  UI work runs the verify skill's hand-over matrix and diffs against dopetest.
- Read `dope/CLAUDE.md` for the toolchain and `AGENTS.md` (root and dope) for
  the module map before touching a file the note names by line — the lines
  were true on 16 Aug 2026 and drift.
