# Хамса — implementation plan

Status: agreed with the tournament author on 2026-09-22, not yet built.
Regulations: `.tmp/hamsa-regs.txt` at the repo root (exported from the
organisers' Google Doc; tournament on 2026-10-03 in Волгоград).

The words used here are `CONTEXT.md`'s. New ones added for this format:
Game round, Ставка, the `placement` Kind, and the mid-game Draw.

## Decisions taken

| # | Decision |
|---|----------|
| 1 | The письменный отбор is a separate КСИ Game (`themes: 10`), and Хамса is seeded from it with `[init] seed: {ksi-game}` and the existing decline ladder. КСИ's ranking already matches the regulations (total, Σ+, correct at 50/40/30/20, mean shared places). Nothing changes in КСИ. |
| 2 | Игра №2's three fourth-place seats are a **host-entered Draw**, made on the Сетка. |
| 3 | Teams level in a ГЭ бой share the **averaged** place (1.5, 1.5, 3, 4). |
| 4 | The Финал tiebreak (an extra Персональный game round on a reserve theme) is the Protocol's перестрелка, switched on per Block. |
| 5 | Round-5 bets are stored exactly as typed. No clamping, no admission check; a hint of the ceiling is fine if free. |
| 6 | No theme names anywhere. A тема is five marks and the player who sat for it. |
| 7 | Статистика tab in v1, same folds as ЭК's. |
| 8 | **Own state, own page** (`hamsa`), like Тройка, reusing the shared primitives. |
| 9 | ГЭ is a **new Kind `placement`**; Финал is a `reseed` over it into a `flat` Block of one бой. |
| 10 | The Draw panel lives on the Сетка under Игра №2, like a reseed's «Рассчитать» panel. |
| 11 | One wide бой sheet, two rows per team, columns grouped under game-round headers. |
| 12 | xlsx and JSON export in v1. A `hamsa` replay codec and a **hand-written transcript** with invented teams and scores are the conformance test. |
| 13 | Appeals are not tracked. |
| 14 | In code, a бой's round is a `GameRound` and the Structure's Round is a `BlockRound`, renamed across dope as a prerequisite commit. On screen both say «раунд». Stored keys, DSL words and ОД's shootout rounds keep their names. |

## 1. Protocol `hamsa` (Go)

Files: `dope/domain/games/hamsa.go` (state + pure scoring, with tests),
`dope/domain/protocol/hamsa.go` (the registration), following
`games/troika.go` and `protocol/troika.go`.

### Params (ADR-0012)

| DSL key | config | default | meaning |
|---|---|---|---|
| `game_rounds` | `gameRounds` (List) | `[5, 5, 5, 1]` | темы per game round 1..4 |
| `multipliers` | `multipliers` (List) | `[1, 2, 3, 4]` | per game round |
| `values` | `values` (List) | `[100, 200, 300, 400, 500]` | base номиналы of a тема |
| `shootout` | `shootout` (Bool) | false | whether the host may add перестрелка темы (Финал only) |

`EmptyState` writes the effective per-тема values into the document, the way
Тройка writes `theme_values` — what a question was worth is a fact about the
бой that played it. Do **not** touch `store.QuestionValues`.

### State (JSON, one document per бой, participants keyed by id)

```
{
  "rounds":   [{"themes": 5, "values": [100,…,500]}, {"themes": 5, "values": [200,…]}, …4 entries…],
  "participants": {
    "<id>": {
      "themes":   [{"player": <playerID>, "answers": ["right"|"wrong"|"", ×5]}, ×16],   // in game-round order
      "bet":      {"amount": <int|null>, "answer": "right"|"wrong"|""},
      "shootout": [{"player": …, "answers": […]}],                                     // only when the Block allows it
      "pin":      <float|null>
    }
  }
}
```

The marks language is ЭК's (`right`/`wrong`/empty; the cursor's `parseMark`
already reads +/−, 1/0, й/ц …). A question may carry several `wrong` and one
`right` across teams; nothing validates that.

### Scoring

- Theme i of a participant is worth `Σ ±values[q]` for its marks, values from
  the document's round entry for that тема.
- `total` = Σ over the 16 темы ± `bet.amount` on the bet's answer.
- `plus` = only the positive contributions (как Σ+ у ЭК).
- `shootoutTotal` = Σ over shootout темы; ranking key after `total`.
- `first` = 1 for the participant(s) in place 1, else 0 — summed by every
  ranker, it is «количество первых мест».
- `correct_<v>` / `wrong_<v>` per base value (for stats and the xlsx).
- Places are **computed**, ties share the mean place (`si.go`'s `placesBySum`
  is the precedent), by `total`, then `shootoutTotal`, then `plus`; a host
  Pin still wins. `Started` is real: any mark, bet or player set.

### Metric for the КСИ place

The Финал's last comparator is «более высокое место на этапе КСИ». Add one
derived metric available to every ranker, `seed`, equal to the participant's
seed rank in the game (what the seed import produced; verify in
`imports/seed.go` and `structure.Inputs` how the rank reaches the resolver —
if the Number is dealt in seed order, `seed` may simply be the Number).
Ascending by default, like `place`.

## 2. Kind `placement` (Go)

File: `dope/domain/structure/placement.go`, registered like the others
(`structure.go` `Register`), with a `PlacementConfig` the compiler writes and
the resolver reads (`resolver.KindConfig`). Adapter in `schemedsl/block.go`,
keys refused if not its own.

DSL keys it reads: `participants`, `match_size`, `rounds`, `venues`,
`title.r{N}`, `sorting`, `letters`. Expansion:

- Round 1: `participants / match_size` Matches, seats dealt in **straight
  bands** of the seed (`straightChunks` in `elimination.go` is the existing
  helper): 1–4 → table 1, 5–8 → table 2, 9–12 → table 3.
- Round r+1, table k: seats place k of every round-r table for
  k < match_size (match-grain Edges, `structure.FromMatch`). The remaining
  `tables` seats of every table are **Draw Slots**: a new `SchemeSlot` source
  (`Draw: true` plus the set of candidate refs, i.e. place `match_size` of
  every round-r table), left empty until the host seats them.
- Ranker: `place_sum`, `total`, `first`, the Protocol's metrics; ties share
  ranks. `bouts` as in `rr.go`'s `multiSeatStandings` — reuse it.

Golden test in `schemedsl/testdata/golden/hamsa.json`; a unit test of the
expansion for 12/4/2 and for a second shape (e.g. 8/4/3) so it is not
hard-wired to this Fest.

## 3. The Draw (server + Сетка)

- Resolver: a Draw Slot is filled only from a stored draw (`match_slots` row
  or a small `slot_draws` table — pick whichever the existing Draw for
  `[init]` uses and extend it); the resolver never derives it. A candidate
  must be one of the slot's candidate refs' current occupants; the server
  refuses anything else with a User error.
- API row in `routes_api.go`: `PUT /api/fest/{fest}/game/{game}/draw` with
  `{slot: code, participant: id}`; Manager access, numbered guard; broadcast
  like a reseed.
- Сетка (`fest-grid.ts`): under a Round that has Draw Slots, a panel titled
  from the Catalog («Жеребьёвка») with one select per empty Draw Slot,
  options = the candidates not yet seated. Shown only to hosts, only once all
  source Matches are finished; viewers see the seats as «—» with a note.
  Reuse the reseed panel's markup and classes.

## 4. The scheme for this Fest

`scripts/hamsa/hamsa.dsl` (and the fixture copy):

```
[defaults]
venues: [А, Б, В]

[init]
seed: <ksi-game-slug>

[scheme]
title: Групповой этап
kind: placement
participants: 12
match_size: 4
rounds: 2
title.r1: Игра №1
title.r2: Игра №2
sorting: [place_sum, total, first, seed]
---
title: Финал
kind: flat
participants: 4
reseed: true
stats_from: [s1]
sorting: [place_sum, total, first, seed]
shootout: true
```

Check with the compiler that `reseed: true` on a `flat` Block is accepted
(`tpsh.dsl` uses `proceeding_participants` on flat; ТПШ's плей-офф uses
`reseed: every`); if not, make the reseed its own line the way the DSL spec
describes.

## 5. Frontend

Files, mirroring Тройка (commit `73f98ea1` is the checklist):

- `web/ts/hamsa-protocol.ts` — `parseState`, `themeScore`, `totals`,
  `placesFor`, `rows`; pure, tested in `web/jstest/hamsa-protocol.test.js`.
- `web/ts/hamsa.ts` — the page. Mount through `mountGamePage`; a бой per
  stage pane via `stage-cache.ts` (ЭК's way, since a Хамса Game has many бои),
  one SSE scope per бой.
- Sheet: `buildTwoRowScoreTable` from `score-table.ts` with a theme group per
  game round; headers «Раунд 1 · Светлый», «Раунд 2 · Полутёмный ×2», …,
  «Раунд 5 · Командный» (strings from the Catalog). Round 5 = «Ставка» input
  cell + mark cell. Then П (перестрелка, only when the Block allows it), Σ,
  Место, Σ+. Player selects per тема from the roster (ЭК's
  `buildPlayerSelectCell`; lift it into a shared module if it is page-private).
  Edits go through the sheet cursor; a bet cell is a number cell.
- `web/ts/hamsa-stats.ts` — generalise `ek-stats.ts` over a value scale taken
  from the document instead of the hard-coded `[10..50]`, then reuse it.
- `web/ts/pages/hamsa.ts`, `web/assets/ui/hamsa.dopeui`, `web/ui/app.go` +
  `vocab.json` kind enum, `game-shell.ts` app union, `game-tabs.ts` GameKind
  and tab set (Игра №1, Игра №2, Финал, Сетка, Статистика, Составы),
  `scripts/webbuild/main.go` entry, `server/pages.go`.
- Host creation form (`hostpages/host_games.go`): «Хамса» radio, default DSL
  above as prefill.
- Styles: reuse ЭК's sheet classes; anything new goes in `styles.css` from
  variables only. Run `design-review` and `verify` at both screen sizes.

## 6. Strings

`i18nstrings/ru/hamsa.toml` (+ label in `games.toml`): game-round names,
«Ставка», «Жеребьёвка», the draw panel's hint and errors, tab titles. Run
`just generate-strings`; every id referenced in full at its call site.

## 7. Export, replay, fixtures

- `export/xlsxexport`: a Хамса sheet — team, per-тема player and marks, per
  game-round Σ, ставка, П, Σ, место. `export/gameexport` JSON as for the others.
- `domain/replay/codec.go`: a `hamsa` entry; `parse.go` seat form gains an
  optional `ставка ±N` token after the 16 theme groups. Transcript
  `testdata/hamsa2026/hamsa.txt` with 12 invented teams: КСИ отбор as its own
  `[game]` (or a pre-seeded roster if the harness cannot chain games —
  check `studchr_test.go`), Игра №1, the Draw tagged `жребий` on Игра №2's
  fourth seats, Игра №2, `[таблица s1]`, the Финал with a перестрелка, final
  `[таблица s2]`. Expected standings worked out by hand in the file, so the
  replay is an oracle. Wire into `studchr_test.go` (direct transport on
  `just test`, HTTP twin on `just test-full`).
- `domain/fixture`: `data/hamsa.dsl` and a `hamsaDocument` in `play.go` so
  `seed-fixture`, the gallery and `just matrix` cover the page. Bless new
  goldens with `just matrix --bless` and commit them.

## 8. Order of work

0. Rename the structural Round to `BlockRound` in Go and TS (own commit, gates green). Done before anything below starts.
1. `games/hamsa.go` + `protocol/hamsa.go` with tests (scoring, places, metrics
   conformance test passes).
2. `structure/placement.go` + DSL adapter + golden; the `seed` and `first`
   metrics; the Draw Slot in the scheme and resolver; the draw API.
3. Replay codec + transcript; make the whole day pass through the direct
   transport. This is the gate before any UI.
4. Page, protocol module, stats, strings, dopeui, tabs, creation form.
5. Draw panel on the Сетка.
6. Export. Fixture, matrix goldens, design-review, verify on phone and desktop.
7. `just pre-commit` green; commit on branch `hamsa` in small steps with
   plain English messages; do not merge or deploy. Deploy to `dopetest` with
   `just deploy-staging` for the hand test only.

## Out of scope

Appeals, theme names, bet validation, tracking who struck which theme in
game round 4, a Python sheet reader (there is no sheet).
