// Package roster holds the fest roster/seed data layer: the imported-team shape,
// the pure transforms that fold a roster into a game's OD/KSI scheme+state, and
// the per-game tx helpers that propagate a roster change and report the affected
// game states. Depends only on the games/store/util leaves (no server coupling),
// so the server and the host-page handlers share one definition.
package roster

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/protocol"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

type FestRosterImportTeam struct {
	RatingID int64
	Name     string
	City     string
	Number   int64
	Players  []FestRosterImportPlayer
}

type GameStateBroadcast struct {
	GameID    int64
	GameType  string
	StateJSON []byte
}

func SortedFestRosterImportTeams(teams []FestRosterImportTeam) []FestRosterImportTeam {
	out := make([]FestRosterImportTeam, len(teams))
	for i, team := range teams {
		out[i] = team
		out[i].Players = append([]FestRosterImportPlayer(nil), team.Players...)
		sort.SliceStable(out[i].Players, func(a, b int) bool {
			return util.CompareAlpha(importPlayerName(out[i].Players[a]), importPlayerName(out[i].Players[b])) < 0
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if cmp := util.CompareAlpha(out[i].Name, out[j].Name); cmp != 0 {
			return cmp < 0
		}
		if cmp := util.CompareAlpha(out[i].City, out[j].City); cmp != 0 {
			return cmp < 0
		}
		return out[i].RatingID < out[j].RatingID
	})
	return out
}

// PropagateRosterTx folds the fest roster into every Game whose Protocol
// carries it (protocol.RosterFolder) — ОД's teams and entries, КСИ's
// participants and answer rows — and reports the documents it rewrote.
// entryRemap (old Number → new) renumbers the cells keyed on a Number; nil on
// a plain re-import.
func PropagateRosterTx(ctx context.Context, tx *sql.Tx, festID int64, teams []FestRosterImportTeam, entryRemap map[int]int) ([]GameStateBroadcast, error) {
	docs, err := store.LoadGameDocs(ctx, tx, festID)
	if err != nil {
		return nil, err
	}
	folded := RosterTeams(teams)
	var updates []GameStateBroadcast
	for _, doc := range docs {
		schemeJSON, stateJSON, ok, err := protocol.FoldRoster(doc.GameType, doc.SchemeJSON, doc.State, folded, entryRemap)
		if err != nil {
			return nil, fmt.Errorf("game %d: %w", doc.GameID, err)
		}
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
update games set scheme_json = ?, updated_at = ?
where id = ? and fest_id = ?`, string(schemeJSON), util.UtcNow(), doc.GameID, festID); err != nil {
			return nil, err
		}
		if err := flatgame.SetStateTx(ctx, tx, festID, doc.GameID, string(stateJSON)); err != nil {
			return nil, err
		}
		updates = append(updates, GameStateBroadcast{GameID: doc.GameID, GameType: doc.GameType, StateJSON: stateJSON})
	}
	return updates, nil
}

// RosterTeams is the roster as a flat Protocol folds it.
func RosterTeams(teams []FestRosterImportTeam) []protocol.RosterTeam {
	out := make([]protocol.RosterTeam, 0, len(teams))
	for _, team := range teams {
		out = append(out, protocol.RosterTeam{Name: team.Name, City: team.City, Number: team.Number})
	}
	return out
}

// FestRosterPlayerView is one player in a team's roster line. RatingID (when
// present) links to the player's rating.chgk.info page in the Составы view.
type FestRosterPlayerView struct {
	Name     string `json:"name"`
	RatingID int64  `json:"ratingID,omitempty"`
}

// FestRosterTeamView is one team with its ordered players, for the read-only
// "Составы" roster view shown to every visitor of a fest. Sourced from the
// canonical fest roster tables (fest_teams/fest_players/fest_team_players) so all
// game types (EK/OD/KSI) surface the same "who plays for what team" list. The
// team's and each player's RatingID (when present) link to their respective
// rating.chgk.info pages.
type FestRosterTeamView struct {
	Number   int64                  `json:"number,omitempty"`
	Name     string                 `json:"name"`
	City     string                 `json:"city,omitempty"`
	RatingID int64                  `json:"ratingID,omitempty"`
	Players  []FestRosterPlayerView `json:"players"`
}

// LoadFestRosterView loads every active team of a fest with its ordered players,
// for the public Составы view. Teams keep their roster position order; players
// keep their roster_order within each team.
func LoadFestRosterView(ctx context.Context, q store.Queryer, festID int64) ([]FestRosterTeamView, error) {
	rows, err := q.QueryContext(ctx, `
select tt.id, coalesce(tt.number, 0), tt.name, tt.city, coalesce(tt.rating_id, 0),
       coalesce(p.first_name, ''), coalesce(p.last_name, ''), coalesce(p.rating_id, 0)
from fest_teams tt
left join fest_team_players ttp on ttp.team_id = tt.id
left join fest_players p on p.id = ttp.player_id
where tt.fest_id = ? and tt.deleted = 0
order by tt.position, tt.id, ttp.roster_order, p.id`, festID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []FestRosterTeamView
	byID := make(map[int64]int)
	for rows.Next() {
		var teamID, number, teamRatingID, playerRatingID int64
		var name, city, firstName, lastName string
		if err := rows.Scan(&teamID, &number, &name, &city, &teamRatingID, &firstName, &lastName, &playerRatingID); err != nil {
			return nil, err
		}
		idx, ok := byID[teamID]
		if !ok {
			teams = append(teams, FestRosterTeamView{Number: number, Name: name, City: city, RatingID: teamRatingID})
			idx = len(teams) - 1
			byID[teamID] = idx
		}
		if player := store.JoinPlayerName(firstName, lastName); player != "" {
			teams[idx].Players = append(teams[idx].Players, FestRosterPlayerView{Name: player, RatingID: playerRatingID})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return teams, nil
}

func LoadFestRosterImportTeamsTx(ctx context.Context, q store.Queryer, festID int64) ([]FestRosterImportTeam, error) {
	teams, err := store.CollectRows(ctx, q, `
select coalesce(rating_id, 0), name, city, coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}, func(rows *sql.Rows) (FestRosterImportTeam, error) {
		var team FestRosterImportTeam
		if err := rows.Scan(&team.RatingID, &team.Name, &team.City, &team.Number); err != nil {
			return team, err
		}
		return team, nil
	})
	if err != nil {
		return nil, err
	}
	return SortedFestRosterImportTeams(teams), nil
}

type FestRosterImportPlayer struct {
	RatingID  int64
	FirstName string
	LastName  string
}

func importPlayerName(player FestRosterImportPlayer) string {
	return store.JoinPlayerName(player.FirstName, player.LastName)
}
