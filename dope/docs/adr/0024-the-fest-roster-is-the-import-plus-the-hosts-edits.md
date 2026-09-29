---
status: accepted
date: 2026-09-29
---

# The fest roster is the import plus the host's edits

Until now the fest roster was whatever the last rating.chgk.info import said.
Nobody could change it in dope, and a re-import rewrote every team's players,
names and Flags. That hurt in two ways. A fest the rating site does not carry,
such as a local СИ tournament, had no roster at all. And an organizer who
imported, fixed things on the spot and then imported again, for example to pick
up late registrations, lost the fixes without a word.

## Decision

The fest roster is the rating import with the host's own edits on top. dope
keeps the edits apart from what they change, so an import can be applied under
them again.

- **Teams.** A host adds a team by hand (`fest_teams.hand = 1`, no rating id,
  the next free number). An import never touches such a team. A rating team
  the host renamed keeps its name and city in `hand_name` and `hand_city`, which
  win over the import's. A rating team the host removed is soft-deleted with
  `hand_removed = 1`, so an import that still lists it does not bring it back.
  A team that has results cannot be removed.
- **Flags.** A team whose Flags the host typed has `hand_flags = 1`, and the
  import leaves its Flags alone.
- **Players.** Every player the host adds to a team, or takes off it, is a row
  in `fest_roster_edits`: the team, the player (by rating id, or by name for a
  person the rating site does not know), and `add` or `remove`. The fest roster
  shows the result. An import rebuilds each rating team's players from the site
  and applies these rows again. Moving a player is a remove on one team and an
  add on the other.
- **Conflicts.** When the site now puts a player on a team, and the host had
  placed that player elsewhere by hand, the import cannot honour both. It asks.
  The host's placement is preselected, because the person who fixed the roster
  on the spot usually knows better than a registration made weeks ago.
- **Preview.** The import page shows what an import would do before doing it:
  the teams it adds and drops, the players it adds and removes per team, the
  edits it keeps, and the conflicts. Nothing is written until the host confirms.
- **Undo.** Every import first saves the fest roster, with its edits, as a
  snapshot (`fest_roster_snapshots`). «Отменить импорт» puts the last one back
  through the same code that applies an import, and undoing again goes one
  import further back. A hand edit drops the snapshots: the roster before an
  import does not have the edits made after it, so putting it back would lose
  them. Undo is for the import just made.

One function applies a roster, whether it came from an import, a hand edit or
a snapshot. It writes only the rows that changed, keeps player ids stable,
refreshes the flat games and the overrides, and re-seats the Тройка games that
follow a зачёт.

## Why

Edits kept apart from what they change are what lets two sources be combined
again later. A plain "keep local changes" flag on a whole team would not do:
the same team can gain a late player on the site and lose one by hand, and both
should stand. A remove has to be remembered as a row of its own, because
otherwise the next import cannot tell a player the host took off from one the
site has just added.

Detaching a fest from the site after its first edit would be simpler, but it
would lose what imports are for: registrations keep arriving until the day.

## Consequences

The fest roster no longer equals the rating site's list, and the import page
says so: the preview lists the edits it keeps. A team or a player added by hand
has no rating id, so a later export to the rating site cannot name them.
