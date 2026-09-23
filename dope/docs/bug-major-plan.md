# Bug Major III — implementation plan

Status: agreed with the owner on 2026-09-23, on branch `bug-major`.
Regulations: `.tmp/bugmajor.txt` at the repo root, exported from the organisers'
Google Doc. The Swiss plan picture is `.tmp/bm/images/image1.png`. The
tournament is on 3–4 October 2026 in Брест.

The words used here are `CONTEXT.md`'s. New ones added for this plan: Сборная,
the `swiss` Kind, the pool, and the written бой of Тройка.

## What the regulations ask of dope

| Game | What it is | What dope lacked |
|---|---|---|
| ОД | 84 questions, 7 tours. Student and общий зачёт | nothing once зачёты are merged |
| Тройка | Troikas of 2–4 players, not teams. A written отбор, then a Swiss stage of 12, then a play-off of 6. Student and adult brackets are separate games | the troika registry, the written бой, 3-seat бои, a перестрелка, the Swiss Kind, a single-group play-off feed |
| Эрудит-секстет | Student and adult games are separate. Two group games of 4 seeded from ОД after 3 tours, rotation between halls, then semifinals of 4 and a final of 4 | the branch (now merged), a snake first deal and a rotation in `placement`, a seed restricted to a зачёт, a lot as the last tiebreak |
| Мелотрек | Written, 5 questions a theme, 2 points each | nothing: Мультиигры |
| Кубок | Sum of places across games | deferred: the organisers add it up from exports |

## Decisions taken

| # | Decision |
|---|----------|
| 1 | ЭС and зачёты come in by merging `worktree/green-cloud-37cb`. Its migration becomes v28. **Done** in 5d2d4b20. |
| 2 | Student and adult ЭС are separate Games, with the same bracket. Student and adult Тройка are separate Games, with the same bracket. |
| 3 | ЭС semifinal: the best 8 of the group stage, two бои of 4 (1-4-5-8, 2-3-6-7), two from each to a final of 4. The same for both зачёты (the organisers' reply overrides the regulations' «у взрослых нет полуфинала»). |
| 4 | The student ЭС group draw is the ordinary snake. The regulations' 1-5-6-12 is a typo. |
| 5 | Тройка play-off: three 2-troika бои, paired 1–6, 2–5, 3–4 by Swiss rank; the three winners play a 3-troika гранд-финал of 9 темы at 1, 1, 1, 2, 2, 2, 3, 3, 3. |
| 6 | Troikas live **inside the main fest** as Сборные: Participants assembled from fest players, not rows of the rating roster. The flat games (ОД, Мелотрек) never see them because those seat the fest roster, and a bracket Game chooses them as entrants. |
| 7 | The written отбор is the **first Block of each Тройка Game**, a `flat` Block of one written бой. So each зачёт's Game has its own отбор table, and nothing has to be split or re-seeded across Games. |
| 8 | The Кубок is out of scope. Every entry must still end with a place. |
| 9 | Team editing lifts the roster editor from `origin/dope-venues` (`roster-editor.ts`, the kit's suggest). |

## 1. Сборная — troikas as fest Participants

A **Сборная** is a Participant put together for one format out of fest players:
a troika. It has a name and 2–4 players. It is not a fest team, so ОД and the
other flat games, which seat the fest roster, never see it. A bracket Game
lists it in the entrant picker.

- Migration v29: `participants.assembled integer not null default 0`.
- Its players are `participant_players` rows, exactly as the Тройка page's seat
  picker already reads them (`store.loadRosters`, roster source `fest`).
- The team it plays for is derived and shown, never stored: the fest team that
  holds at least two of its players (регламент VII.2.1), found by name.
- Host page `/host/fest/{fest}/troikas` («Тройки»): the table of Сборные with
  their players and derived team; an add/edit dialog built on the venues
  branch's roster editor, suggesting fest players; a bulk paste, one troika a
  line, `Название: Игрок, Игрок, Игрок`; delete, refused while a Game seats it.
- Editing the players of a Сборная is a substitution: the next тема's seat
  picker offers the new list. Nothing already entered changes.
- The entrant picker on game creation labels Сборные apart from teams.

## 2. Тройка protocol

Files: `domain/games/troika.go`, `domain/protocol/troika.go`,
`web/ts/troika-protocol.ts`, `web/ts/troika.ts`.

### 2a. N sides

`EmptyState` builds as many sides as the match has seats (`participants` in
the config). Places rank by total, and sides that are level share the mean
place. The page draws N side blocks.

### 2b. Перестрелка and a pin

- `shootout` in the document: how many trailing темы are перестрелка темы. The
  host adds one on a бой whose sides are level ("+ перестрелка"); it has the
  value 1 and counts into the total. That is регламент IV.2.4: one тема, then
  темы until the first correct answer, which the host simply stops entering
  after.
- `pin`: places a host sets by hand, winning over the computed ones, for
  anything the regulations settle outside the sheet.

### 2c. The written бой

Protocol param `written` (Bool). A written бой is one sitting of every entrant:
a row per troika, and per вопрос the count of correct answers, 0–3. A вопрос
pays the count times its тема's value (`theme_values: [1,1,1,2,2,2,3,3,3]`).
Metrics: `total`, `threes` (вопросы with three correct), `twos`. The Block
sorts `[total, threes, twos, draw]`, where `draw` is the deterministic lot —
регламент IV.2.3's coin.

The page draws the written бой as one sheet, rows × (темы × вопросы), with the
cells cycling 0→3 on a click and taking a typed digit.

## 3. Kind `swiss`

A Swiss stage in the Major format: a Participant leaves on its `wins`-th win
(it proceeds) or its `losses`-th loss (it is out), and each round pairs
Participants that have the same record. The pool of a record is ranked by the
seed it entered the Block with, which for Тройка is its отбор place.

The regulations' plan for 12 is written into the Kind as a table, because its
choice of бой size and winning places per pool is the organisers' and not a
rule:

| Round | Pool (W-L) | Size | Бои | Seats | Winners per бой |
|---|---|---|---|---|---|
| 1 | 0-0 | 12 | 6 | 2 | 1 |
| 2 | 1-0, 0-1 | 6 each | 3 each | 2 | 1 |
| 3 | 2-0 | 3 | 1 | 3 | 1 |
| 3 | 1-1 | 6 | 2 | 3 | 2 |
| 3 | 0-2 | 3 | 1 | 3 | 1 |
| 4 | 2-1 | 6 | 2 | 3 | 2 |
| 4 | 1-2 | 3 | 1 | 3 | 1 |
| 5 | 2-2 | 3 | 1 | 3 | 1 |

Six proceed: one 3-0, four 3-1, one 3-2. `participants` other than 12 is a
compile error that names the sizes there are.

- Round 1 seats incoming rank i against 13 − i.
- A pool played as one бой seats its members straight from the бои before it
  (`FromMatch` refs).
- A pool played as several бои is a **pool**: a reseed stage that ranks its
  members by the seed they entered with, and that the resolver calculates on
  its own as soon as its source бои are finished (`auto`), since there is no
  judgement in it for the host to make. The pool is dealt by the snake, dope's
  one dealing rule: two-seat бои pair 1 with n, 2 with n − 1; six into two бои
  of three is 1, 4, 5 / 2, 3, 6. The picture pairs its three-seat pools by hand
  (1, 3, 5 / 2, 4, 6 in 1-1 and 1, 3, 4 / 2, 5, 6 in 2-1); the snake balances
  the seeds better than either and is what dope keeps.
  **Built:** a pool is `EmitPool`, a reseed stage with `auto` and `seedFrom`
  (the отбор's stage), sorted by `seed`; the resolver ranks a stage that names
  `seedFrom` by that stage's ranks.
- The Block's table ranks by record: proceeded first by fewer losses, then
  the rest by more wins and fewer losses, then by entry seed. Ranks are
  distinct, so the play-off can seat by them.
- A бой whose places are shared across its winning cut seats nobody onward,
  as everywhere: the host plays a перестрелка.

## 4. The play-off from one ranked Group

`single_elimination` after a Block with a single Group and no reseed deals
that Group's ranks by the snake: six into three бои of two is 1–6, 2–5, 3–4,
and eight into two бои of four is 1-4-5-8, 2-3-6-7. The Тройка
гранд-финал is `match_size.r2: 3` and ЭС's final is the terminal бой of four.

## 5. ЭС group stage

`placement` gains two keys:

- `deal: snake` — the first Round deals the seed by the snake rather than in
  straight bands.
- `rotation: true` — every later Round seats place p of table t at table
  t + p − 1 (mod the table count), регламент V.2.3. Hamsa's rule (table k takes
  place k of every table) stays the default.

The stage score is a scoring rule, `bout.stage: total + 200 - 50 * place`,
summed over both Games, then Σ+, then the 50s taken, then the lot
(регламент V.2.4–2.5). ЭК's Protocol gains the `taken50` metric if it lacks
it, and `placement` gains `draw`.

## 6. Seed restricted to a зачёт

`[init] division: Студ` seeds only the teams that carry that Flag, and
`division: -Студ` only those that do not. The ЭС Games seed from ОД this way,
so the 12 student and the 16 adult teams come out of one ОД table without
anybody unticking teams by hand.

## 7. The schemes for this Fest

Тройка (one Game per зачёт, entrants the зачёт's troikas):

```
[defaults]
themes: 6

[scheme]
kind: flat
title: Отбор
written: true
themes: 9
theme_values: [1, 1, 1, 2, 2, 2, 3, 3, 3]
letters: false
sorting: [total, threes, twos, draw]
proceeding_participants: 12
---
kind: swiss
participants: 12
wins: 3
losses: 3
---
kind: single_elimination
participants: 6
themes: 8
match_size.r2: 3
themes.r2: 9
theme_values.r2: [1, 1, 1, 2, 2, 2, 3, 3, 3]
```

ЭС (one Game per зачёт):

```
[init]
seed: od
division: Студ

[scheme]
kind: placement
participants: 12
match_size: 4
deal: snake
rotation: true
bout.stage: total + 200 - 50 * place
sorting: [stage, plus, taken50, draw]
proceeding_participants: 8
---
kind: single_elimination
participants: 8
match_size: 4
winning_places: 2
```

**Built:** the schemes live in `dope/scripts/bugmajor/` (`troika.dsl`, `es-students.dsl`, `es-adults.dsl`) rather than as the form's defaults, which other fests still use. Paste one into the game form; for ЭС, put the ОД Game's code after `seed:`. `TestBugMajorDemoFest` (with `DOPE_BUGMAJOR_DEMO=<new db>`) builds a demo fest to look at; log in as demo / demopass123.

## 8. Order of work

1. Merge ЭС + зачёты. **Done.**
2. Тройка protocol: N sides, перестрелка, pin (Go + page).
3. Тройка written бой (Go + page), `draw` in `flat`.
4. Kind `swiss`, auto pools, the Swiss table on the page and the Сетка.
5. Play-off from one ranked Group.
6. Сборные: migration, host page, bulk paste, entrant picker.
7. `placement`: `deal`, `rotation`, `draw`; ЭК `taken50`.
8. Seed restricted to a зачёт.
9. Default DSLs, an invented fixture for both formats, design review and the
   verify matrix, then deploy to a branch staging site.

## Out of scope

The Кубок; appeals; ЭС's перестрелка rules beyond ЭК's own; Мелотрек beyond
Мультиигры as it is.
