package roster

import (
	"context"
	"database/sql"
	"encoding/json"

	"dope/dope/domain/games"
	"dope/dope/domain/multifest"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"

	corei18n "code.pecheny.me/pecheny/dopesuite/dopecore/i18nstrings"
)

// A flat Game's teams (OD, the minefield, KSI, Multi): by default the
// fest's whole roster, its venue's part for a Game on one, kept in step with
// every rating import and every hand edit on the fest teams page
// (PropagateRosterTx). The host may give the Game a list of its own on its
// entrants tab: the roster as it stands at that moment, which from then on
// changes only there. The teams on it still take their names, cities,
// players and Flags from the fest roster; who plays is the Game's own.

// GameTeam is a team on a flat Game's entrants tab, or one it may add.
// Entered says the Game holds something a host entered for it.
type GameTeam struct {
	ID      int64  `json:"id"`
	Number  int64  `json:"number,omitempty"`
	Name    string `json:"name"`
	City    string `json:"city,omitempty"`
	Entered bool   `json:"entered,omitempty"`
}

// GameTeamsView is a flat Game's entrants tab: whether it keeps a list of
// its own, who plays it, and, with a list of its own, the fest's teams that
// are not on it.
type GameTeamsView struct {
	Own        bool       `json:"own"`
	Teams      []GameTeam `json:"teams"`
	Candidates []GameTeam `json:"candidates"`
}

// ownTeamsTx is the Game's own list by fest team id, nil for a Game that
// seats the fest's roster, and for one not made yet (gameID 0).
func ownTeamsTx(ctx context.Context, q store.Queryer, gameID int64) (map[int64]bool, error) {
	if gameID == 0 {
		return nil, nil
	}
	var own bool
	if err := q.QueryRowContext(ctx, `select own_teams from games where id = ?`, gameID).Scan(&own); err != nil || !own {
		return nil, err
	}
	ids, err := store.CollectRows(ctx, q, `select team_id from game_teams where game_id = ?`, []any{gameID}, func(rows *sql.Rows) (int64, error) {
		var id int64
		return id, rows.Scan(&id)
	})
	if err != nil {
		return nil, err
	}
	members := make(map[int64]bool, len(ids))
	for _, id := range ids {
		members[id] = true
	}
	return members, nil
}

// onList is the roster's teams that are on the list, in the roster's order.
func onList(teams []FestRosterImportTeam, members map[int64]bool) []FestRosterImportTeam {
	out := make([]FestRosterImportTeam, 0, len(members))
	for _, team := range teams {
		if members[team.LocalID] {
			out = append(out, team)
		}
	}
	return out
}

// GameSeatsTeamsTx is the fest's teams a flat Game seats now: its own list,
// or the roster, each on the Game's venue under the venue's numbers.
func GameSeatsTeamsTx(ctx context.Context, q store.Queryer, festID, gameID, venueID int64) ([]FestRosterImportTeam, error) {
	teams, err := LoadFestRosterImportTeamsTx(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	members, err := ownTeamsTx(ctx, q, gameID)
	if err != nil {
		return nil, err
	}
	if members != nil {
		teams = onList(teams, members)
	}
	placement, err := multifest.LoadPlacement(ctx, q, festID)
	if err != nil {
		return nil, err
	}
	return OnVenue(teams, placement, venueID), nil
}

// LoadGameTeams reads a flat Game's entrants tab.
func LoadGameTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (GameTeamsView, error) {
	doc, err := store.LoadGameDoc(ctx, q, festID, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	if _, ok := games.As[games.RosterFolder](doc.GameType); !ok {
		return GameTeamsView{}, corei18n.User(dopestrings.Default.Entrants.Own.NotFlat())
	}
	members, err := ownTeamsTx(ctx, q, gameID)
	if err != nil {
		return GameTeamsView{}, err
	}
	entered, err := enteredNumbers(ctx, q, festID, doc)
	if err != nil {
		return GameTeamsView{}, err
	}
	all, err := LoadFestRosterImportTeamsTx(ctx, q, festID)
	if err != nil {
		return GameTeamsView{}, err
	}
	placement, err := multifest.LoadPlacement(ctx, q, festID)
	if err != nil {
		return GameTeamsView{}, err
	}
	view := GameTeamsView{Own: members != nil, Teams: []GameTeam{}, Candidates: []GameTeam{}}
	for _, team := range OnVenue(all, placement, doc.MultiFestVenueID) {
		row := GameTeam{ID: team.LocalID, Number: team.Number, Name: team.Name, City: team.City, Entered: entered[team.LocalID]}
		if members == nil || members[team.LocalID] {
			view.Teams = append(view.Teams, row)
		} else {
			view.Candidates = append(view.Candidates, row)
		}
	}
	return view, nil
}

// enteredNumbers is the fest teams, by id, the Game holds something a host
// entered for.
func enteredNumbers(ctx context.Context, q store.Queryer, festID int64, doc store.GameDoc) (map[int64]bool, error) {
	out := map[int64]bool{}
	counter, ok := games.As[games.Entered](doc.GameType)
	if !ok {
		return out, nil
	}
	festNumber, err := multifest.FestNumbers(ctx, q, festID, doc.MultiFestVenueID)
	if err != nil {
		return nil, err
	}
	state := json.RawMessage(doc.State)
	numbers := map[int64]bool{}
	entered, seats := counter.EnteredSeats(state), counter.Seats(state)
	for i, seat := range seats {
		if number, ok := festNumber(seat.Number); ok && i < len(entered) && entered[i] {
			numbers[number] = true
		}
	}
	ids, err := store.CollectRows(ctx, q, `select id, coalesce(number, 0) from fest_teams where fest_id = ? and deleted = 0`, []any{festID}, func(rows *sql.Rows) ([2]int64, error) {
		var row [2]int64
		return row, rows.Scan(&row[0], &row[1])
	})
	if err != nil {
		return nil, err
	}
	for _, row := range ids {
		if numbers[row[1]] {
			out[row[0]] = true
		}
	}
	return out, nil
}

// SetOwnTeamsTx gives a flat Game a list of its own, the teams it seats now,
// or (own false) has it seat the fest's roster again.
func SetOwnTeamsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, own bool) (GameStateBroadcast, error) {
	doc, err := flatGameTx(ctx, tx, festID, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from game_teams where game_id = ?`, gameID); err != nil {
		return GameStateBroadcast{}, err
	}
	if own {
		if _, err := tx.ExecContext(ctx, `
insert into game_teams(game_id, team_id) select ?, id from fest_teams where fest_id = ? and deleted = 0`, gameID, festID); err != nil {
			return GameStateBroadcast{}, err
		}
		if doc.MultiFestVenueID > 0 {
			// A Game on a venue keeps the venue's teams only.
			if _, err := tx.ExecContext(ctx, `
delete from game_teams where game_id = ? and team_id in (
  select id from fest_teams where fest_id = ? and coalesce(multifest_venue_id, 0) != ?)`, gameID, festID, doc.MultiFestVenueID); err != nil {
				return GameStateBroadcast{}, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `update games set own_teams = ? where id = ? and fest_id = ?`, own, gameID, festID); err != nil {
		return GameStateBroadcast{}, err
	}
	return refoldTx(ctx, tx, festID, doc)
}

// AddGameTeamTx puts a fest team on a flat Game's own list.
func AddGameTeamTx(ctx context.Context, tx *sql.Tx, festID, gameID, teamID int64) (GameStateBroadcast, error) {
	doc, err := ownListGameTx(ctx, tx, festID, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `select count(*) > 0 from fest_teams where id = ? and fest_id = ? and deleted = 0`, teamID, festID).Scan(&exists); err != nil {
		return GameStateBroadcast{}, err
	}
	if !exists {
		return GameStateBroadcast{}, corei18n.User(dopestrings.Default.Entrants.Own.NoTeam())
	}
	if _, err := tx.ExecContext(ctx, `insert or ignore into game_teams(game_id, team_id) values(?, ?)`, gameID, teamID); err != nil {
		return GameStateBroadcast{}, err
	}
	return refoldTx(ctx, tx, festID, doc)
}

// RemoveGameTeamTx takes a team off a flat Game's own list. A team the Game
// holds answers for stays: its row would take them with it.
func RemoveGameTeamTx(ctx context.Context, tx *sql.Tx, festID, gameID, teamID int64) (GameStateBroadcast, error) {
	doc, err := ownListGameTx(ctx, tx, festID, gameID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	entered, err := enteredNumbers(ctx, tx, festID, doc)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	if entered[teamID] {
		var name string
		if err := tx.QueryRowContext(ctx, `select name from fest_teams where id = ?`, teamID).Scan(&name); err != nil {
			return GameStateBroadcast{}, err
		}
		return GameStateBroadcast{}, corei18n.User(dopestrings.Default.Entrants.Own.Entered(name))
	}
	if _, err := tx.ExecContext(ctx, `delete from game_teams where game_id = ? and team_id = ?`, gameID, teamID); err != nil {
		return GameStateBroadcast{}, err
	}
	return refoldTx(ctx, tx, festID, doc)
}

// flatGameTx reads a Game whose format folds the roster in.
func flatGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (store.GameDoc, error) {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil {
		return doc, err
	}
	if _, ok := games.As[games.RosterFolder](doc.GameType); !ok {
		return doc, corei18n.User(dopestrings.Default.Entrants.Own.NotFlat())
	}
	return doc, nil
}

// ownListGameTx is flatGameTx for a Game that keeps a list of its own.
func ownListGameTx(ctx context.Context, tx *sql.Tx, festID, gameID int64) (store.GameDoc, error) {
	doc, err := flatGameTx(ctx, tx, festID, gameID)
	if err != nil {
		return doc, err
	}
	var own bool
	if err := tx.QueryRowContext(ctx, `select own_teams from games where id = ?`, gameID).Scan(&own); err != nil {
		return doc, err
	}
	if !own {
		return doc, corei18n.User(dopestrings.Default.Entrants.Own.Follows())
	}
	return doc, nil
}

// refoldTx seats the Game's teams as they are now.
func refoldTx(ctx context.Context, tx *sql.Tx, festID int64, doc store.GameDoc) (GameStateBroadcast, error) {
	teams, err := GameSeatsTeamsTx(ctx, tx, festID, doc.GameID, doc.MultiFestVenueID)
	if err != nil {
		return GameStateBroadcast{}, err
	}
	return foldGameTx(ctx, tx, festID, doc, RosterTeams(teams), Remap{})
}
