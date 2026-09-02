package venues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dope/dope/domain/flatgame"
	"dope/dope/domain/games"
	"dope/dope/domain/protocol"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

// ErrHasResults refuses to unseat a team the Слот's Game already scored.
var ErrHasResults = errors.New("у команды уже есть результаты в игре — сначала уберите их")

// SetStatusTx accepts, declines or returns a Заявка to the queue and reseats
// the Слот's Game accordingly: an accepted team takes the next free Number,
// a declined one leaves its seat. Unseating a team the Game already scored is
// refused (ErrHasResults).
func SetStatusTx(ctx context.Context, tx *sql.Tx, slot Slot, venueCity string, appID int64, status string) error {
	app, err := LoadApplication(ctx, tx, appID)
	if err != nil {
		return err
	}
	if app.SlotID != slot.ID {
		return sql.ErrNoRows
	}
	if app.Status == StatusAccepted && status != StatusAccepted {
		scored, err := numberScored(ctx, tx, slot.FestID, slot.GameID, app.Number)
		if err != nil {
			return err
		}
		if scored {
			return ErrHasResults
		}
	}
	if _, err := tx.ExecContext(ctx, `update slot_applications set status = ?, updated_at = ? where id = ?`,
		status, util.UtcNow(), appID); err != nil {
		return err
	}
	if status != StatusAccepted {
		if _, err := tx.ExecContext(ctx, `update slot_applications set participant_id = null where id = ?`, appID); err != nil {
			return err
		}
	}
	return ReseatTx(ctx, tx, slot, venueCity)
}

// ReseatTx rewrites the Слот's Game team list from its accepted Заявки: every
// accepted team keeps the Number it already holds, a newly accepted one takes
// the lowest free number, and the Состав follows into the Слот's own roster.
func ReseatTx(ctx context.Context, tx *sql.Tx, slot Slot, venueCity string) error {
	apps, err := SlotApplications(ctx, tx, slot.ID)
	if err != nil {
		return err
	}
	accepted := make([]Application, 0, len(apps))
	for _, a := range apps {
		if a.Status == StatusAccepted {
			accepted = append(accepted, a)
		}
	}
	assignNumbers(accepted)

	teams := make([]protocol.RosterTeam, 0, len(accepted))
	for _, a := range accepted {
		teams = append(teams, protocol.RosterTeam{Name: a.TeamName, City: venueCity, Number: a.Number})
	}
	sort.SliceStable(teams, func(i, j int) bool { return teams[i].Number < teams[j].Number })
	if err := foldTeamsTx(ctx, tx, slot.FestID, slot.GameID, teams); err != nil {
		return err
	}
	for _, a := range accepted {
		participantID, err := participantByNumber(ctx, tx, slot.FestID, slot.GameID, a.Number)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update slot_applications set participant_id = ? where id = ?`,
			util.NullableInt64(participantID), a.ID); err != nil {
			return err
		}
		if participantID == 0 {
			continue
		}
		if err := linkRegistryTeamTx(ctx, tx, slot.FestID, participantID, a, venueCity); err != nil {
			return err
		}
		if err := writeGameRosterTx(ctx, tx, slot.FestID, slot.GameID, participantID, a.Roster); err != nil {
			return err
		}
	}
	return dropUnseatedTx(ctx, tx, slot.GameID, accepted)
}

// dropUnseatedTx removes the Participants the Слот no longer seats: a declined
// Заявка leaves no team behind, and its Number is free again.
func dropUnseatedTx(ctx context.Context, tx *sql.Tx, gameID int64, accepted []Application) error {
	keep := make([]any, 0, len(accepted)+1)
	keep = append(keep, gameID)
	placeholders := ""
	for _, a := range accepted {
		if a.Number <= 0 {
			continue
		}
		if placeholders != "" {
			placeholders += ", "
		}
		placeholders += "?"
		keep = append(keep, a.Number)
	}
	query := `delete from participants where game_id = ?`
	if placeholders != "" {
		query += ` and number not in (` + placeholders + `)`
	}
	_, err := tx.ExecContext(ctx, query, keep...)
	return err
}

// assignNumbers deals the lowest free Number to every accepted Заявка that has
// none, keeping the ones already seated where they are.
func assignNumbers(accepted []Application) {
	taken := map[int64]bool{}
	for _, a := range accepted {
		if a.Number > 0 {
			taken[a.Number] = true
		}
	}
	next := int64(1)
	for i := range accepted {
		if accepted[i].Number > 0 {
			continue
		}
		for taken[next] {
			next++
		}
		accepted[i].Number = next
		taken[next] = true
	}
}

// foldTeamsTx makes the Game's document hold exactly these teams; flatgame
// settles the бой, which is what mints the Participants and their numbers.
func foldTeamsTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, teams []protocol.RosterTeam) error {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil {
		return err
	}
	schemeJSON, stateJSON, ok, err := protocol.FoldRoster(doc.GameType, doc.SchemeJSON, doc.State, teams, nil)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", flatgame.ErrNotFlat, doc.GameType)
	}
	if _, err := tx.ExecContext(ctx, `update games set scheme_json = ?, updated_at = ? where id = ? and fest_id = ?`,
		string(schemeJSON), util.UtcNow(), gameID, festID); err != nil {
		return err
	}
	return flatgame.SetStateTx(ctx, tx, festID, gameID, string(stateJSON))
}

func participantByNumber(ctx context.Context, q store.Queryer, festID, gameID, number int64) (int64, error) {
	if number <= 0 {
		return 0, nil
	}
	var id int64
	err := q.QueryRowContext(ctx,
		`select id from participants where fest_id = ? and game_id = ? and roster = 'team' and number = ?`,
		festID, gameID, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// linkRegistryTeamTx keeps the Venue's registry — every team that has ever
// played there — in step with the accepted Заявка, keyed on the rating team id.
func linkRegistryTeamTx(ctx context.Context, tx *sql.Tx, festID, participantID int64, app Application, city string) error {
	if app.RatingTeamID <= 0 {
		return nil
	}
	var teamID int64
	err := tx.QueryRowContext(ctx, `select id from fest_teams where fest_id = ? and rating_id = ?`,
		festID, app.RatingTeamID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		var position float64
		if err := tx.QueryRowContext(ctx, `select coalesce(max(position), 0) + 1 from fest_teams where fest_id = ?`, festID).Scan(&position); err != nil {
			return err
		}
		if teamID, err = store.InsertReturningID(ctx, tx, `
insert into fest_teams(fest_id, rating_id, name, city, position, deleted)
values(?, ?, ?, ?, ?, 0)`, festID, app.RatingTeamID, app.TeamName, city, position); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if _, err := tx.ExecContext(ctx, `update fest_teams set name = ? where id = ?`, app.TeamName, teamID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update participants set fest_team_id = ? where id = ?`, teamID, participantID)
	return err
}

// writeGameRosterTx mirrors a Состав into the Слот's own roster
// (game_team_players, which the Game reads because its roster_source is
// 'game'), so a team's players belong to the sitting they played, not to the
// Venue. The rating player ids stay on the Заявка — they are what the players
// export needs.
func writeGameRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID, participantID int64, players []RosterPlayer) error {
	if _, err := tx.ExecContext(ctx,
		`delete from game_team_players where game_id = ? and participant_id = ?`, gameID, participantID); err != nil {
		return err
	}
	for order, p := range players {
		playerID, err := roster.EnsureSeedPlayer(ctx, tx, festID,
			strings.TrimSpace(p.Name+" "+p.Patronymic), strings.TrimSpace(p.Surname))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into game_team_players(game_id, participant_id, player_id, roster_order)
values(?, ?, ?, ?)`, gameID, participantID, playerID, order); err != nil {
			return err
		}
	}
	return nil
}

// numberScored reports whether the Слот's Game already holds an answer for a
// team number, which is what makes unseating it a 409.
func numberScored(ctx context.Context, q store.Queryer, festID, gameID, number int64) (bool, error) {
	if number <= 0 {
		return false, nil
	}
	doc, err := store.LoadGameDoc(ctx, q, festID, gameID)
	if err != nil {
		return false, err
	}
	var state games.ODState
	if err := json.Unmarshal([]byte(doc.State), &state); err != nil {
		return false, nil
	}
	for _, question := range state.Entries {
		for _, taken := range question {
			if taken == number {
				return true, nil
			}
		}
	}
	return false, nil
}
