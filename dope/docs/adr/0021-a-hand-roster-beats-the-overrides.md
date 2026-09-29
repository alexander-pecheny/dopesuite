---
status: accepted
date: 2026-09-29
---

# A hand roster beats the overrides

A team's roster in a Game used to come from two places. The fest roster is
copied onto the Participant when the Game's seed is imported. A player
override moves one fest player from one fest team to another for some Games,
and it materialises those Games' rosters into `game_team_players`
(`roster_source = 'game'`). A host could not say "in this ЭС, Бобры play with
these five people" without inventing overrides for each change, and a person
who is on no rating roster could not be added at all.

The host now edits a team's roster for one Game on that Game's Составы tab
(CONTEXT.md, Game roster). This needs a rule for when a hand edit and an
override both touch the same team.

## Decision

A hand roster wins. The rows the host writes go into `game_team_players` with
`hand = 1`. `store.GameRosters` reads a team's hand rows when it has any, then
the materialised rows when overrides made the Game's own rosters, and then the
Participant's roster. `overrides.MaterializeGameRosterOverridesTx` deletes and
rewrites only the rows with `hand = 0`, and it skips a team that has hand rows.
A seed import writes only the Participant's roster, and a rating import only
re-materialises, so neither reaches a hand roster. The players page marks an
override as not acting in a Game where either of its two teams is kept by
hand. The host can give the team back its fest roster on the same tab, and
then the overrides apply to it again.

A hand roster may not drop anybody who already has something entered for the
team in that Game (a theme seated in ЭК/ЭС or Хамса, a buzz in брейн), and
neither may giving the fest roster back. Clearing the Game drops hand rosters
together with the overrides, since both are the Game's derived rows.

## Why

The two tools answer different questions. An override is about a person: this
player plays for another team in these Games. A hand roster is about one team
in one Game, and the host writes it looking at the full list. If an override
could still move a player into a list the host wrote by hand, the list on the
tab would stop being the list that plays. Letting the hand roster win keeps
that list true. Marking the override on the players page means the override
does not simply seem to vanish.

A column on `game_team_players`, and no separate table, keeps a hand roster in
the one table the journal, the archive export and the checkpoints already
cover. The price is that an empty hand roster cannot be written: there would be
no row to carry the mark. That is refused anyway, because a team with nobody
cannot play.
