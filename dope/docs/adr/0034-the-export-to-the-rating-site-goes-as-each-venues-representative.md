---
status: accepted
date: 2026-10-26
---

# The export to the rating site goes as each venue's representative

When every roster and every answer of a tournament is entered in dope, the
organizers want them on rating.chgk.info without typing them in again. The
site's «ввод» API takes rosters and answers from a token with the right to
write.

## Decision

A user saves their own token for the site on /profile. dope stores it whole,
because it sends it, and never shows it back. The export of a fest's ОД of
every team (`/host/fest/<fest>/rating/export`, and its API twin
`/api/fest/<fest>/rating-export`) goes out with the token of whoever presses
the button.

On a synch tournament the site puts a team into a заявка when the team first
arrives, and an organizer's call names that заявка by its representative
(`representative`). So the export sends a roster call per площадка, as that
площадка's representative, and then one results call for every team, since a
team on the tournament keeps its заявка. A team the site already holds in
another заявка is moved first (`results_entry/move`), and the export checks
again after the rosters, because a representative of two площадки gets new
teams in their first заявка.

A player's flag is Б when the player is in the team's base roster for the
season, and Л otherwise. buff answers the base rosters when it mirrors the
season, and the site answers the rest.

The page shows the whole plan before it sends anything: each площадка's
representative and rosters, the moves, the teams and players that stay
behind for want of a site id, and the sheets with questions left open. It
refuses to send when the ОД has a different number of questions from the
site's tournament.

## Consequences

The export does not send зачёты, and does not register players or teams the
site does not know. Those stay on the page as skipped, for the
representative to add on the site.
