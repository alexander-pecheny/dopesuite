---
status: accepted
date: 2026-11-27
---

# A game's teams follow a source plus its overrides

A fest kept one roster, the import from the rating site plus the hosts'
edits (ADR-0024), and every flat game seated it. A game that one-off teams
play (a «чёрный ЧГК», the minefield) could not differ from it, and the fest's
«Команды» page edited something every game shared. A game's separate list
(November 2026) fixed the first and made the second worse: two editors that
looked the same and changed different things.

## Decision

Every game says where its teams come from, on its «Участники» tab:

- **«Как на турнирном сайте»**: the fest's teams as the last sync from the
  rating site gave them, plus this game's overrides.
- **«Как в игре X»**: game X's teams as X has them, its overrides included,
  plus this game's overrides on top. A game follows X whatever X does,
  including X playing a separate list. A choice that would make a game follow
  itself, at any distance, is refused.
- **«Отдельный список»**: the game's own list, edited freely in the fest's
  team editor. Going to it copies the teams as they stand. Going back to a
  source drops the list, after asking when the list has changes.

An **override** is one change, kept by the game in order: add a team, remove
a team, rename a team, change its city, зачёты or number, add a player,
remove a player, move a player to another team. It names a team or a player
by a key that outlives a sync (the rating site's id, else the fest's row, else
the key an added team was given), so a sync applies it again. An override
whose team or player is gone is kept and shown as «оверрайд не применён». In
a tethered game the table is read-only but for the number; every other change
is «Добавить оверрайд». The words are the hosts': «оверрайд», not «правка».

A change to a source reaches every game that follows it, in dependency
order: a sync, an override of game X, X switching to a separate list.

### What a change upstream never does

- **It never reseats.** Who sits where in a bout game (the seeding, the grid,
  the waiting list) changes only when the host acts (ADR-0023). A seeding by
  another game's table is taken when the host loads the list and kept; new
  results in that game, an appeal verdict included, do not reach it.
- **It never drops results.** A team the game holds answers for stays seated
  when it leaves the source, under its last name and number, and the tab says
  it is not in the source. A team renumbered upstream takes its answers to
  the new number, as a fest renumbering did. When the new number is taken by
  a team the game seats, the game is not refolded and the tab says why.
- **It seats new teams only in a flat game**, which seats everyone. A bout
  game seats a team only through its entrant list.

## Consequences

The fest's «Команды», «Игроки», «Тройки» and «Номера» pages go once every
format reads its teams this way; the sync, which numbers new teams itself,
moves next to the rating site's id in the fest settings. Until then the
fest's roster, hand edits included, is what «Как на турнирном сайте» reads.
A synch fest keeps its pages until a design round of its own: a venue as a
game that follows the rating site's venue is the likely shape.
