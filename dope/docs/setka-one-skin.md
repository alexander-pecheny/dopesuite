# One skin for the Сетка and its tables — hand-off

Status: open. Written 16 Aug 2026 after the third round of "why does this
game's table look different from ЭК's" fixes. Read with `CONTEXT.md` (Сетка,
Group, Block, Бой) and the architecture note in root `AGENTS.md`.

## What happened

The user reviewed dopetest on a phone (`~/2026-08-16-dope.html`) and found
four things: КИнСБФ group tables and the ТПШ отбор far wider than ЭК's
columns; the СИ group tables a different font size from the бой boxes beside
them and drifting off their rows; the bronze бой compiled after the final.
Three of the four had one cause: the phone media block in `styles.css`
restated the Сетка geometry in literals (`minmax(170px, 1fr)`, `gap: 5px`,
`font-size` on `.grid-slot-cell` only), so the desktop cap and the newer
`.grid-standings` skin never reached the phone. Nobody looked at a phone.

## What is done (commit after `0bcf14a`)

- The Сетка geometry is tokens on `:root` (`--fest-col-min/max`, `--grid-row`,
  `--grid-cell-pad`, `--grid-cell-text`, `--grid-head-text`, `--grid-num-col`,
  `--grid-metric-col`, `--grid-place-col`, `--grid-fade`, …). The phone media
  block redefines values, not rules.
- `scripts/classcheck` fails on any length literal inside a `.fest-*` /
  `.grid-*` rule (`cssgeom.go`). It runs in the root `just pre-commit`.
- The verify skill carries a hand-over matrix (phone × desktop × light ×
  dark, per game type) and `dope/scripts/imgdiff.py`.
- The bronze бой compiles before the final (`schemedsl/compile.go`,
  `appendBronze`); games compiled earlier need a recompile from the settings
  page to reorder.

## What is left: one cell, one builder

The Сетка still has two builders for the same cell — a бой box's
`.grid-slot-cell` (`fest-grid.ts buildMatchBox`) and a group table's
`.grid-standings td` (`buildStandingsTable`) — and their rules restate each
other ("same font, same cell height, same paper" says the comment; the tokens
now make it true, nothing makes it stay true). Around the Сетка there are
seven more standings-shaped tables, each its own class on the shared
`.results-table` skin:

| builder | file | class |
|---|---|---|
| `buildReseedStagePanel` | fest-grid.ts:342 | `results-table reseed-results-table` |
| `buildGroupStandingsView` | match-table.ts:820 | `results-table group-standings-table` |
| `buildEKStatsTable` / `buildIndividualStatsTable` | match-table.ts:1462, 1523 | `results-table ek-stats-table` |
| `buildVenuesTable` | match-table.ts:1035 | `results-table venues-results-table` |
| `buildRosterView` | match-table.ts:1169 | `results-table roster-results-table` |
| brain crosstab | brain.ts:1044 | `results-table group-standings-table brain-crosstable` |
| brain stats | brain.ts:741 | `results-table ek-stats-table` |

and the fading name cell (`results-team-name-wrap` / `results-team-name`) is
built by hand in five files (`fest-grid.ts`, `match-table.ts` ×4, `si.ts` ×2,
`od.ts`, `ek.ts`); `resultsTeamCell` in `match-table.ts:785` is the one
function that should build all of them. `styles.css` has 66 rules on those
`.results-table` variants.

The job: one `standingsTable(spec)` in `match-table.ts` (or a new
`standings.ts`) that takes `{head, columns: [{key, label, align}], rows,
nameCell}` and returns the one skin, with the Сетка's `.grid-standings` and
`.grid-slot-cell` sharing one cell class; every builder above becomes a call
to it with data. Then a table cannot restate the skin, and the media block
cannot restate the geometry. This is candidate #7 of the 15 Aug architecture
report, narrowed to the Сетка and the standings tables.

## Acceptance

- One class for a Сетка cell; `grid-slot-cell` and `grid-standings td` rules
  collapse into it (the phone tokens then reach both by construction).
- Every table in the list above is built by the shared builder; the
  per-table classes survive only where a rule genuinely differs (column
  widths), and `classcheck` shows no dead names.
- The verify matrix (skill) run for ЭК, Личная СИ, ТПШ, КИнСБФ; screenshots
  diffed against dopetest with `imgdiff.py`; only the topbar's viewer count
  may differ.
- No new CSS literal: `just class-check` green.

## Traps

- `fest-grid.ts` measures the group table's row count (`placeOnRows`) and the
  block stack's unit (`layoutBlockColumns` reads `grid-template-rows`); keep
  `--grid-unit` = rows × `--grid-row` + gaps, or the columns stop lining up.
- `patchScoreTable` in `match-table.ts` patches cells by `data-*`
  coordinates; the standings tables are rebuilt whole and carry none.
- The brain crosstab has a fixed 600px track (`brain-groups`) so eight groups
  fit two abreast; a shared builder must keep the wrapper's track a token.
- The DOM stub in `web/jstest` has no layout, so tests assert classes and
  `dataset`, never sizes.
