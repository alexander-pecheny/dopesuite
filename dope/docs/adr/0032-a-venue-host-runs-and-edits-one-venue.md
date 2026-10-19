---
status: accepted
date: 2026-10-19
---

# A venue host runs and edits one venue

In a synch or multi fest each venue (ADR-0031) has its own organizers. They
run the venue's Game, and on the day they are also the people who know which
teams came, who plays for whom and which number each team got. ADR-0030 let
an admin hand out a link to one Game, but the roster and the numbers stayed
the admins' alone, so a venue's organizers had to ask an admin for every
change.

## Decision

An admin can limit a host to venues as well as to Games
(`fest_venue_hosts`, the twin of `fest_game_hosts`). A host limited to a venue
runs every Game on it, and also edits that venue's teams and numbers: the
teams page and the numbers page show them that venue's teams alone, a team
they add plays there, and a team, a number or a player of another venue is
refused. A host limited to venues and to Games runs both kinds of Game.

An admin mints a link to a venue as to a Game (`venue_invites`, a second
binding of `dopecore/invitelink`). Whoever follows it becomes a host limited to
that venue, and a link still never narrows anyone: a host nobody limited stays
so.

The roster routes take a level of their own, `route.Roster`: an admin passes
with every team, a host limited to venues passes with those venues in
`Scope.Venues`, and each handler keeps to them. Everything that rewrites the
whole roster stays the admins': the rating import and its undo, the roster
sheet, player overrides and troikas.

A team the host makes in a fest with venues is put on a venue, chosen in the
team editor, and takes that venue's next number.

## Consequences

- A host nobody limited runs every Game but edits no roster, as before. Only a
  host limited to venues edits one, because only then is it clear whose teams
  are theirs.
- Moving a player between venues is an admin's edit: a venue host who adds a
  person who plays at another venue is told so.
- A rating team plays where the site says. Its venue is the rating import's,
  and the editor offers no venue for it.
