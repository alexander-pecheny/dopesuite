---
status: accepted
date: 2026-10-16
---

# A synch fest plays each площадка apart

A strictly synchronous tournament is one package played at the same hour at
dozens of площадки: Гёмбёц-9 had 168 teams at 38 of them. Each площадка's
organizers run their own table, and the players want to see both their
площадка's standings and everyone's. dope could not hold it. A flat Game (ОД,
КСИ, Мультиигры) always seats the whole fest roster under the fest's numbers,
so a Game per площадка would have listed all 168 teams 38 times.

## Decision

A fest has a type, chosen when it is made: **regular** (everything as
before), **synch** (one rating tournament, many площадки) or **multi**
(several in-person tournaments, each площадка with its own rating id, as with
Nevermore's variations). The type changes only while the fest has no Games.

A площадка (`fest_sites`, called `site` in code to keep it apart from a бой's
`venues`, though both are «Площадка» on screen) belongs to a fest. A fest team
is on at most one площадка (`fest_teams.site_id`), and so is a Game
(`games.site_id`). A flat Game on a площадка folds in that площадка's teams
alone; a Game on none folds in the whole roster, as every Game did before.
That is one filter in the two places that fold the roster
(`roster.PropagateRosterTx` and gamebuild's `pristineFlatTx`), so the
numbering, the Participants and every roster path keep working unchanged.

In a synch fest the rating import reads each team's `synchRequest.venue`,
makes the площадки it has not seen, and puts each rating team on its
площадка; a team the host made by hand keeps the one it has. A team the site
moves to another площадка moves between the Games with its row. After the
import, every площадка without a Game gets an ОД of the tournament's own
tours (`questionQty`).

## Why not a team subset per Game

The other shape was a flat Game with an entrant list of its own, as the
bracket Games have (ADR-0023). It would have needed per-Game numbers in the
flat documents, a roster fold that reads each Game's list, and a rule for
what a roster import does to every list. A площадка is what the tournament
itself is divided by, it is already in the rating data, and with it the
roster stays one list that every Game cuts the same way.

## Consequences

- Each площадка numbers its own teams 1…n (`fest_teams.site_number`), as
  the площадки do on the day. The import numbers a площадка's new teams after
  its last number, in the order of their names, and a team that moves takes
  the next number at its new площадка. A Game on a площадка shows and stores
  those numbers. The fest's Number stays: it is the team's identity for the
  Participants, which are the fest's, so the few places that join a Game's
  document to the fest (seating, the scored check, the ОД export, seeding
  from an ОД) turn a площадка's number into the fest's through
  `sites.FestNumbers` and `sites.ByNumber`. The numbers page and its API
  number one площадка at a time.
- Who may run a площадка's Games and edit its roster is still the fest's
  access list, limited by Game (ADR-0030). Scoping a host to a площадка,
  including its roster, is the next step.
- The summary table over the площадки' Games, and the multi type's import,
  are still to come.
