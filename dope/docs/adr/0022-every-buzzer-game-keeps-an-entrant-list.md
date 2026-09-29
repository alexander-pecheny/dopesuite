---
status: accepted
date: 2026-09-29
---

# Every buzzer Game keeps an entrant list

A buzzer Game used to learn who it seats in one of two ways. ЭК, ЭС and брейн
kept a seed ladder (`state_json.seedImport`) that one import button filled from
one source — the fest's first КСИ, or what the scheme's `[init]` declared —
and the host could only tick «Отказалась». Тройка, Хамса and личная СИ seated
the entrants ticked on the creation form, or the whole fest, and nothing
changed them afterwards. Nobody could add a team late, take one out, move one,
or seed from a different Game without editing the scheme.

## Decision

Every buzzer Game keeps one **entrant list** (CONTEXT.md), stored where the
ladder was, and one tab edits it for all six formats (`domain/entrants`,
`web/ts/entrants.ts`).

- **Seating.** The list's active entrants take the Structure's seed numbers in
  order (`imports.seatListTx`). An entrant already sitting in a бой that has
  begun keeps its number, unless it declined, and the rest fill the numbers
  left. Entrants past the last seat are numbered on past it: the waiting list.
  A бой that has begun is never reseated, as before.
- **Sizing.** A Game whose DSL has no `seed:` in `[init]` is compiled against
  its entrants, so while nothing is entered in it, a changed list recompiles
  it for the list's active entrants (`gamebuild.ApplyListTx`). A scheme of a
  fixed size (a roundrobin group of four) turns another count down; then the
  Structure stays, the extra entrants wait, and the tab says why. Recompile now
  writes each pristine бой from its own Protocol, which a Тройка or Хамса
  needed.
- **Sources.** The scheme's `[init]` only preselects. The host can seed from
  any Game with one table, kept to a зачёт, the fest's own roster, the fest's
  troikas, a lot, an xlsx sheet, or a declared by-players seeding. The chosen
  source and зачёт are stored with the list so a re-import repeats them; a
  re-import after hand edits asks first.
- **Тройка by зачёт** becomes the troikas source. The list follows the troikas
  page until the host edits it (or imports from another source), or until
  anything is entered.
- **One-off entrants** are Participants with `participants.game_id` set
  (migration v34): every fest-wide lookup and picker leaves them out, and
  the Game's deletion takes them with it.
- **Refusals.** An entrant that sits in a бой that has begun can be neither
  removed, renamed nor moved. Only a one-off is renamed in a Game.

The old `/seed-import` routes stay as aliases over the same list.

## Consequences

The seeding of a Game that predates this reads what its Structure seats until
the host first saves a list. A written отбор that has begun keeps its rows: a
troika added after that waits on the list for a seat a decline frees.
