# Хамса — implementation plan

Status: agreed with the tournament author on 2026-09-22; built on branch
`hamsa` the same day, steps 0 to 7. Where building it made a choice the plan
left open, or found the plan wrong, that is written into the step as **Built**.
Regulations: `.tmp/hamsa-regs.txt` at the repo root (exported from the
organisers' Google Doc; tournament on 2026-10-03 in Волгоград).

The words used here are `CONTEXT.md`'s. New ones added for this format:
Game round, Ставка, the `placement` Kind, and the mid-game Draw.

## Decisions taken

| # | Decision |
|---|----------|
| 1 | The письменный отбор is a separate КСИ Game (`themes: 10`), and Хамса is seeded from it with `[init] seed: {ksi-game}` — the source Game's **code**, so `seed: ksi-1` — and the existing decline ladder. КСИ's ranking already matches the regulations (total, Σ+, correct at 50/40/30/20, mean shared places). Nothing changes in КСИ. |
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

**Built:** the document is the Protocol's own, not ЭК's team blob. The blob was
the closer fit — it is already `participants: {id: {themes: [{player,
answers[5]}], pin}}` — but it has no room for a Ставка or for the per-round
номиналы, and `MatchBlob` drops what it does not declare, so both would be lost
on the first write.

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

**Built:** a document keyed by Participant cannot answer `Score` in slot order,
and a seat that entered nothing still took a place, so the seats have to reach
the scorer. The smallest seam that fits is one optional interface,
`protocol.SeatedScorer` (`ScoreSeated(cfg, state, seats)`), asked for by
`protocol.ScoreSeats`, which `scoring.RecalculateMatchResultsTx` now calls;
every other Protocol answers in slot order as before. The outcome carries its
`Participant`, which is what the rest of the model already means by that field.

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
  is the precedent), by `total`, then `shootoutTotal`; a host
  Pin still wins. `Started` is real: any mark, bet or player set.

**Built**, where the plan left a choice:

- `plus` counts what the темы took and leaves the Ставка out. Σ+ measures what
  a team knew, not what it gambled.
- `first` is 1 for every team **nobody finished ahead of**, so two teams
  sharing 1.5 have each taken a первое место. Reading it as «place == 1» would
  have counted neither.
- `correct_<v>` / `wrong_<v>` are named after the **base** номиналы, which are
  the first game round's values in the document; a 300 taken in the Тёмный
  round counts as a 300.
- A перестрелка тема's вопросы are worth the **last** game round's номиналы —
  the tiebreak is another Персональный round.
- **Σ+ does not split a place.** The plan had `total`, then `shootoutTotal`,
  then `plus`; the регламент has «команды, набравшие равное количество игровых
  очков по итогам конкретного боя, считаются разделившими соответствующие
  места», and the only tiebreak it gives a бой is the extra Персональный round,
  which is the перестрелка. Σ+ is measured all the same — a Block's table may
  rank on it — but it decides no place inside a бой.
- A Pin is applied after the places are computed, exactly as `si.go` does it,
  so the seats around a pinned one keep the places the marks gave them.

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

**Built**, where the plan left a choice:

- The Block is **one ranking scope**, because «наименьшая сумма мест… в обеих
  играх ГЭ» cannot be read off a table per Round. So the Kind emits a `matches`
  stage per Round — the бои the Сетка draws — plus one `placement` stage that
  holds no Matches of its own and names the Rounds as its `sources`. The
  resolver already ranked a reseed that way; it now does the same for a Kind
  stage that declares sources, and a rank ref into such a stage waits on those
  Rounds' бои instead of resolving on day one.
- `match_size` must be at least the number of tables, since place k of every
  table has to have a table k to go to. Twelve teams two to a table is refused
  rather than compiled.
- **A Draw Slot is a `placeholder` whose ref carries `draw`** — the code and the
  candidate places. That needed no migration, and it is honest: a placeholder is
  already «a seat no rule derives», which is exactly a Draw, and the resolver
  leaves placeholders alone. The host's draw is written onto the slot with
  `locked = 1`.
- `seed` reaches a Ranker through a new `structure.Inputs.Seeds`, which the
  resolver loads from `game_assignments` — basket 1's number is the rank the
  seed import dealt, which for Хамса is the place in the КСИ отбор. Both the
  reseed and `placement` write it onto their rows, and `Ascending` knows it
  reads better the smaller.
- `flat` now ignores its own `sorting` when the Block has an incoming reseed,
  the way `roundrobin` already did: there the key describes the Edge, and the
  Финал's `[place_sum, total, first, seed]` is not something a single бой's
  table can rank by.
- `[init] seed:` names the source Game's **code**, so the scheme says
  `seed: ksi-1` — the code a fest's first КСИ game is given.

## 3. The Draw (server + Сетка)

- Resolver: a Draw Slot is filled only from a stored draw (`match_slots` row
  or a small `slot_draws` table — pick whichever the existing Draw for
  `[init]` uses and extend it); the resolver never derives it. A candidate
  must be one of the slot's candidate refs' current occupants; the server
  refuses anything else with a User error.
- API row in `routes_api.go`: `PUT /api/fest/{fest}/games/{game}/draw` with
  `{slot: code, participant: id}`; Manager access, numbered guard; broadcast
  like a reseed. **Built:** it shares the reseed's write-and-reload
  (`writeAndReloadFest`), so both answer the fest view, the бои whose seats
  moved and the revision. A participant of 0 clears the seat; a candidate the
  slot does not name, or one already drawn into another table of the same
  Round, is a User error.
- The candidates reach the page resolved: `MatchParticipantSummary.Draw`
  carries the slot's code, who is seated and the Participants it may be filled
  from, because a match summary carries names alone and the panel has to send
  an id back. They stay empty until the source бои are finished.
- Сетка (`fest-grid.ts`): under a Round that has Draw Slots, a panel titled
  from the Catalog («Жеребьёвка») with one select per empty Draw Slot,
  options = the candidates not yet seated. Shown only to hosts, only once all
  source Matches are finished; viewers see the seats as «—» with a note.
  Reuse the reseed panel's markup and classes.

  **Built:** a viewer gets **no panel and no note** — the seat in the бой box
  already reads «Жребий», which says the same thing in the place a reader is
  looking. The panel is shown to a host only, under the Round's бои, and it
  says «Жребий откроется…» until the server resolves candidates, which it does
  only once the Round it draws from is finished. A team already drawn is off
  the other seats' lists.

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

**Built**, where the plan left a choice:

- The page is Тройка's shape, **not `stage-cache.ts`**. A Хамса Game is seven
  бои in three stages; the pane cache's prefetching and per-pane lifecycle were
  written for ЭК's twenty-five over five, and Тройка already proved the simpler
  shape — one `/stages/matches` fetch, a Map, one SSE scope per бой — for a
  page of many бои.
- The player select is Тройка's `chairPicker`, not ЭК's
  `buildPlayerSelectCell`: the document keeps a player **id**, while ЭК's cell
  is a name select wired to ЭК's own edit queue. Nothing was lifted.
- The **Ставка is a column group like a тема**, so `buildTwoRowScoreTable`
  gives it the input over the mark for nothing, with the signed contribution as
  its score cell. The перестрелка is another column group the same way, and its
  score cell **is** the П the plan asked for, so there is no separate column.
- The five raunds are named in a **header row above the тема heads**, inserted
  into the table the builder made; a round's cell spans its own темы.
- `ek-stats.ts` was generalised only over the **value scale its tables print**.
  The fold itself is Хамса's own (`hamsa-stats.ts`): ЭК reads a projection that
  names players by name, and Хамса reads a document keyed by Participant that
  names them by id.
- The tabs are one per stage, in scheme order — Игра №1, Игра №2, Общий зачёт,
  Пересев, Финал — plus the Сетка, the Статистика and the Составы. A stage's
  title carries its Block's name in front, which the tab bar drops.

## 6. Strings

`i18nstrings/ru/hamsa.toml` (+ label in `games.toml`): game-round names,
«Ставка», «Жеребьёвка», the draw panel's hint and errors, tab titles. Run
`just generate-strings`; every id referenced in full at its call site.

## 7. Export, replay, fixtures

- `export/xlsxexport`: a Хамса sheet — team, per-тема player and marks, per
  game-round Σ, ставка, П, Σ, место. `export/gameexport` JSON as for the others.

  **Built:** a sheet per stage and a block per бой, as Тройка's export does;
  the per-тема Σ is per тема rather than per game round, because that is the
  column the sheet in front of a host has. The JSON archive needed nothing at
  all: it is a dump of the Game's rows and knows no game type.
- `domain/replay/codec.go`: a `hamsa` entry; `parse.go` seat form gains an
  optional `ставка ±N` token after the 16 theme groups. Transcript
  `testdata/hamsa2026/hamsa.transcript` with 12 invented teams: Игра №1, the
  Draw on Игра №2's fourth seats, Игра №2, `[таблица s1]`, the Финал with a
  перестрелка, final `[таблица s2]`. Expected standings worked out by hand in
  the file, so the replay is an oracle. Direct transport on `just test`, HTTP
  twin on `just test-full`.

  **Built**, where the plan left a choice:

  - A transcript describes **one Game**, not a chain of them, so the КСИ отбор
    is not in the file: the roster order is the seed it produced. The fest the
    test builds still creates a КСИ Game, because `[init] seed: ksi-1` names it.
  - `жребий` on a бой header already means «the whole table was set by a
    person», and Игра №2 has three derived seats beside the drawn one — so a
    `жребий Команда` **line inside** the бой draws one seat and leaves the rest
    asserted. `replay.Drawer` is the seam; the driver presses the same endpoint
    the Сетка's panel does.
  - The codec says where a перестрелка's themes sit (`shootout`) and what they
    are worth (the last game round's номиналы), so the driver stayed free of
    game names.
  - No `[статистика]`: the Статистика tab reads the players a team fielded, and
    an invented sheet asserting invented aggregates would be a tautology. The
    `[составы]` check at the door already holds every named player to his team.
  - A Block may now hold its own table and the пересев feeding the next Block,
    so `[таблица s2]` reads the Block's own and leaves the пересев out.
- `domain/fixture`: `data/hamsa.dsl` and a `hamsaDocument` in `play.go` so
  `seed-fixture`, the gallery and `just matrix` cover the page. Bless new
  goldens with `just matrix --bless` and commit them.

  **Built:** the fixture seats **twelve** of its sixteen teams, so the fourth
  places are a real Draw rather than a fourth table's worth of derived seats,
  and it runs the lot itself — the first candidate each Slot will take, which
  is deterministic and is what a golden needs. It also gives its Participants
  people (`participant_players`), which nothing needed before: Хамса records
  who played a тема, and without them the sheet names nobody and the
  Статистика tab counts nothing.

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

**Built:** one commit per step, seven of them on `hamsa` after the rename.
Nothing was merged, pushed or deployed — the staging hand test is still owed,
and with it the two things a fixture cannot answer: whether a host can keep up
with a бой on the wide sheet, and whether the Жеребьёвка panel is where the
organiser looks for it when the lot is actually drawn.

## What the design review and the four screen cells changed

Looking at it found ten things the code was wrong about, all in step 4's page
or the Сетка:

- **A colgroup.** The round names span their темы, and a table whose first row
  spans columns cannot take its widths from that row — fixed layout divides a
  spanning width over the columns it covers. So the sheet declares its columns.
- **Хамса counts in thousands.** Σ is five figures and a тема's score four, so
  the sheet's own total and score columns are wider than the 10..50 ones, and
  the Сетка takes a wider number column on a `.hamsa-page`. **The mark cells are
  not**: a mark is a mark, and the author, holding the two sheets side by side,
  found Хамса's plainly bigger than ЭК's. The colgroup reads ЭК's own
  `--question-col`, and `--narrow-col` for the counts.
- A тема's head is «Т1», not «Тема 1 · 100–500»: it stands over one column.
  What the вопросы are worth is written across their own headers, and the
  round's multiplier in the round head above them.
- The **Ставка** is headed by the word, over the number the host types, and the
  round's score cell is «Т17» — numbered after the sixteen темы, `themes + 1`.
  Both columns are a score column wide; the number input drops its spinner, and
  the word is written at `--text-xs`, the size a three-figure номинал is.
- The **бой names itself in the sheet's own frozen head**, as ЭК's does: the
  title beside the «Закончен» tick, in the cell that stays put while the темы
  scroll under it. It was tried above the sheet, as Тройка's is, and that left
  the frozen cell empty — so the тема headers showed through it and a scrolled
  sheet read «100» to the left of Σ. A spectator gets the name alone, and the
  fade off the frozen columns is ЭК's `.stage-scroll-left`.
- The **Пересев tab keeps its place in the chain** rather than being folded to
  the end: it is played between the групповой этап and the Финал.
- A **flat Block ranked on the бой's own place shows no metric column** — the
  place column is already there, and a second one headed `place` said nothing
  twice in the Финал's table.
- The **Общий зачёт table is as wide as its columns need**, like a пересев's,
  rather than stretched across the screen.
- The **sheet ends as ЭК's does**: Σ+, then one narrow column per position of a
  тема, «Q5»…«Q1». A Хамса вопрос is worth a different номинал in every раунд,
  so they are named by position rather than by value; they count the sixteen
  темы and leave the ставка and the перестрелка out, and they are the client's
  reading of the same marks the server's `correct_<номинал>` metrics count.
- A **перестрелка тема is written the first time somebody marks it.** A бой
  starts without one — most Блоки never play one — so the page created one and
  patched it whole rather than dropping the edit on the floor, which is what it
  was doing.
- The **Жеребьёвка panel takes a row of its own** under the Round's бои (the
  Сетка's columns hold a head and their boxes, so a third child landed at the
  top of the next column), and its rows are the kit's `.field` rather than a
  layout of their own.

Cells looked at: the бой sheet, the Общий зачёт table, the Статистика tab and
the Сетка with the panel, at 1280×800 and on an iPhone 16, light and dark.

## Out of scope

Appeals, theme names, bet validation, tracking who struck which theme in
game round 4, a Python sheet reader (there is no sheet).
