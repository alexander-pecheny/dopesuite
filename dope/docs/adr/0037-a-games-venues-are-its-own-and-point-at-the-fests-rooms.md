---
status: proposed
date: 2026-11-27
---

# A game's venues are its own and point at the fest's rooms

A tournament takes a building's rooms for the day. Each game is played at
its own venues, numbered from 1 in its scheme: Вопросики at ten, ЭК at
fifteen. For one game venue 1 is room Б-414, for another it is Б-209, and
the same room has other hosts in each game. Hosts belong to a game's venue,
not to the room.

dope had one list of numbered venues for the whole fest (`venues`), with
the title and hosts on each number. Venue 1 meant the same room in every
game, and a game that differed wrote an override (`game_venues`), or hid the
fest's venues it did not play at. The fest's page listed game facts (hosts,
which numbers are taken), its delete button was greyed out for every number
some game used, and the organizers worked around the model: crowlenok26 has
ten host overrides and seventeen hidden rows; bugmajor2026 wrote the hosts
into the titles («Кабинет 415 (Дрозд)», «Кабинет 415 (Гедрис, Гологуш)»,
one room twice); ps-polyfest2026 gave one game the fest's venues 9–12 and
renamed another's venue 1 to the room of venue 9.

## Decision

Two things, both «Площадка» on screen:

- A **room** (`rooms`, code `Room`) is a physical place of the fest: a
  title and nothing else. The fest's Площадки page lists the rooms, and
  under each the schedule that falls out of the games: every бой played in
  it, in time order, with its game and hosts. A room is deleted when no
  game's venue is in it; the refusal names the games that use it.
- A **venue** (code `Venue`) is one of a game's numbered places. A game has
  exactly the venues its scheme deals its бои to, and its **venue map**
  (`game_venues`: game, number, room, hosts) says for each which room it is
  in (none yet is allowed, and reads «пл. N») and who hosts it. The game's
  Площадки tab lists the venues, each with a room select («Где проходит»)
  and a hosts field.

A бой is at one of its game's venues by number (`matches.venue`), and may
name hosts of its own (`matches.hosts`), which stand in for the venue's for
that бой: a final in the same room run by the head judge. The бой's dialog
offers its game's venues, each with its room when it has one, and the hosts
field.

A бой reads its room when its venue has one («Б-414 18:00»), and «пл. N»
when it has none. The game switch «Называть площадки по названию» goes: the
room is what the switch used to ask for.

Two venues of one game may be in the same room; nothing offers or forbids
it, as swapping two venues' rooms passes through that state.

## Consequences

- A scheme names no rooms. It is a format, reusable from one tournament to
  the next, and only deals numbers: `venues: 10`, a block's
  `venues: [9, 10, 11, 12]`, `rotate_venues`, `group_venues`. Compiling makes
  the game's venue rows for the numbers it dealt; a recompile adds the rows
  for new numbers and drops those no бой is dealt to any more. A stored
  scheme that still lists titles (`venues: [А, Б, В]`) compiles as that many
  numbered venues, and its titled block subsets resolve by position; the
  titles place nothing, and the DSL's docs stop offering them.
- Which room each venue is in, and its hosts, is the game's **venue map**,
  written apart from the scheme: on the game's Площадки tab, or through the
  API in one call for the whole game
  (`PUT …/games/<game>/venues {venues: [{number, room, hosts}]}`) or one
  venue (`PUT …/games/<game>/venues/<n>`). A room is named by its id or its
  title (case, spaces and ё do not count); a title the fest has no room for
  is refused, so a typo makes no room. Rooms are made on the fest's page or
  with `POST …/rooms`.
- The tab's «Скопировать из игры [игра]» puts another game's rooms on this
  game's venues of the same numbers, and leaves the hosts: the М and Ш games
  of a fest share the rooms and seldom the hosts. It is the same map write.
- A бой's venue is a number, not a row id, so the journal and the game
  checkpoints, which hold raw rows, stay meaningful across the change: a
  revert puts a бой back at the venue number it had. The venue map is the
  game's setup, like its title, and a revert leaves it, as it leaves the
  rooms; a room a map still uses cannot be deleted.
- Moving a бой to another venue is limited to its game's venues.
- The API: `…/rooms` (list with schedule, add, rename, delete), the venue
  map above, and `POST …/matches/<code>/venue {number, hosts}`. The
  fest-wide `…/venues` writes and the game overrides go. The dope-api skill
  documents the map, so an agent can set up a day's rooms from a list.
- The synch fest's площадка stays `multifest.Venue` (ADR-0031); in code
  `Room` keeps the two apart better than `Venue` alone did.

## Migration

Mechanical, for every fest:

1. Every old venue with a title becomes a room of that title, in number
   order; titles equal under the matching rule become one room.
2. Every game gets a venue for each number its scheme deals or its бои sit
   at. Its room is that of the game's title override when it had one (so
   polyfest's «ауд. 213» on КИнСбФ's venue 1 lands in the room «ауд. 213»),
   else the old venue's. Its hosts are the game's override, else the old
   venue's.
3. Each бой takes the number it sat at (`matches.venue`). The overrides,
   hidden rows and game-only venues are folded into the maps, and the
   override table goes. The old `venues` table (renamed `legacy_venues`)
   and `matches.venue_id` stay unread: dropping them would rewrite every бой
   for nothing.

The rest is not mechanical and is done after deploy through the new API, as
a list of calls the organizer reviews first: chr2026 («Рим, Алиев» → room
«Рим», hosts «Алиев») and bugmajor2026 (hosts out of the parentheses, the two
«Кабинет 415» rooms made one, its final moved to the game's venue 9 with the
hosts «Дрозд»).

## Why not keep the fest's numbers

Overrides could have carried on: a room per fest number, hosts per game.
But a number would still mean the same place in every game, which is not
how a building is shared, and the page that shows rooms would still be
keyed by numbers no organizer thinks in.
