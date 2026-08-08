---
status: accepted
date: 2026-08-08
---

# Participant is a first-class row, not a team

The Structure layer has always spoken Participant (`SlotOutcome.Participant`, `RankedEntry.Participant`, `stage_standings.participant_id`), but storage only offered `teams`, so личная СИ would have meant a `teams` row holding a player's name. We decided to rename the game-side registry instead: `teams` becomes `participants`, carrying the roster it was drawn from (a `fest_teams` or a `fest_players` entry, exactly one), and `team_id` becomes `participant_id` in `match_slots`, `match_results`, `game_teams`, `team_players` and `game_assignments`. The alternative — leaving `teams` alone and plumbing the nullable `player_id` that `match_slots` and `game_assignments` already carry — keeps two nullable keys and two joins on every read path, and we judged a column whose name lies about its contents to be the more expensive mistake for both people and agents reading the code later.

## Consequences

- A one-time migration, small in practice: ~90 registry rows in prod, and no frontend touches `team_id` at all.
- The Fest roster tables (`fest_teams`, `fest_players`, `fest_team_players`, `game_player_team_overrides`) keep their names — they really are teams and players.
- The discriminator column is `participants.roster`, not `kind`: Kind is spoken for by Block Kinds (`stages.kind`, `structure.StageKind`), and reusing it here would mislead exactly the readers this rename is for.
- A player-Participant has no roster of its own. Per-player statistics in an individual game are its Participant standings — which is what the reference СИ sheet's «Статистика» tab shows — so code that walks a team's players branches on `roster` rather than meeting a Participant that plays for itself.
