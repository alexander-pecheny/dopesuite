---
status: proposed
date: 2026-09-30
---

# A Кубок Дружбы is an ОД played over tables

Кубок Дружбы (Octobearfest's regulations) is an ОД of short tours, nine tours
of four questions, played by players rather than teams. Each registered
player draws a маршрутный лист with a number, and the card sends him to one
table in each tour. The people at a table change every tour. A table is
scored like an ОД team, and a player scores what the tables on his card took
in their tours. The tie-break is how many tours his table took in full, then
one short, then two short. Until now dope could not hold it: an ОД seats the
fest's teams, and the regulations' personal standings had nowhere to live.

## Decision

A new game type, `kd`. Its document is an ОД document. Its teams are the
tables («Стол 1» … «Стол n»), and a `players` list sits beside them:
`{card, name, team}`. The ОД page serves it (`static/od.html`). Entry, the
detailed sheet, the tables' results and the Экран work on the tables
unchanged. The page adds a «Личный зачёт» tab, and an «Игроки» tab where the
host registers players and prints their cards: blank ones numbered 1…N for
the vases the players draw from at the door, whose number the host then types
in beside the player's name, or the registered players' cards with their names. The server computes the same
standings (`games.ComputeKDResults`, `GET …/results`).

The card's route is last year's sheet's formula. With n tables, card c
sits at `((c−1) mod n + ⌊(c−1)/n⌋·(t−1)) mod n + 1` in tour t. Cards 1…n
never move: they are the regulations' джокеры. For a prime n two cards meet
at most once in n tours, and every tour seats as many players at each table
as at any other, give or take one. So the host sets n when creating the game,
and it must be a prime. The hint asks for one no smaller than the number of
tours.

A table is nobody on the fest roster. Its Protocol seats it under a negative
number, as Мультиигры seat a guest team, so no Participant is minted for it.
A table numbered 1 would otherwise rename fest team 1, because a flat game
finds a Participant by number. The Protocol folds no roster in, so a roster
import leaves the tables alone. The tables are the document's rating-roster
key, so a state PATCH cannot rename them. A clear wipes the answers and keeps
the players.

A player is a name, not a fest player. The regulations allow anybody, and the
registration sheet has people with no team on the fest. The page suggests the
fest roster's names and fills in the team from them, but a name typed by hand
is just as good. The team is shown for reference and ranks nothing.

## Consequences

- A Кубок Дружбы seats no Participant, so the fest pages that read
  `stage_standings` show no table for it. Its standings are on its own page
  and in `GET …/results`.
- Numbering guards still follow the fest: while a fest team has no number,
  entry is refused here as in every other game.
- The xlsx export has the tables' ОД sheet only. The personal standings are
  not in it yet.
