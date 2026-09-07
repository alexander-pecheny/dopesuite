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

	dopestrings "dope/i18nstrings"
)

var ErrHasResults = errors.New(dopestrings.Default.Venues.Errors.TeamHasResults())

// Towns answers where a team is from. A team's town is its own — teams travel
// to play, and an all-star side belongs to no town at all — so it is the rating
// site's answer, and the Venue's own city only stands in for a team the rating
// site has never heard of.
type Towns func(ratingTeamID int64) string

// FixedTown is the Towns of a Venue whose teams the rating site does not know.
func FixedTown(city string) Towns {
	return func(int64) string { return city }
}

func SetStatusTx(ctx context.Context, tx *sql.Tx, slot Slot, towns Towns, appID int64, status string) error {
	app, err := LoadApplication(ctx, tx, appID)
	if err != nil {
		return err
	}
	if app.SlotID != slot.ID {
		return sql.ErrNoRows
	}
	if app, err = resolveSeatTx(ctx, tx, slot, app); err != nil {
		return err
	}
	unseating := app.Status == StatusAccepted && status != StatusAccepted
	if unseating {
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
	if !unseating {
		return reseatTx(ctx, tx, slot, towns, 0)
	}
	if _, err := tx.ExecContext(ctx, `update slot_applications set participant_id = null where id = ?`, appID); err != nil {
		return err
	}
	// Reseat first: it is what clears the Game's rows for that seat, and the
	// Participant cannot go while they still point at it.
	if err := reseatTx(ctx, tx, slot, towns, app.Number); err != nil {
		return err
	}
	if app.ParticipantID <= 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `delete from participants where id = ? and game_id = ?`, app.ParticipantID, slot.GameID)
	return err
}

// WithdrawApplicationTx takes an application back: its filer changed their
// mind, or the team is not coming. It goes through the same unseating a host's
// decline does, so a seat with results in it refuses to go, and then the
// application itself is gone rather than sitting there declined — the person is
// free to file again.
func WithdrawApplicationTx(ctx context.Context, tx *sql.Tx, slot Slot, towns Towns, appID int64) error {
	if err := SetStatusTx(ctx, tx, slot, towns, appID, StatusDeclined); err != nil {
		return err
	}
	// slot_application_versions cascades on the delete.
	_, err := tx.ExecContext(ctx, `delete from slot_applications where id = ?`, appID)
	return err
}

// ReseatTx folds the accepted applications into the Slot's Game: each keeps the
// Number it holds, a newly accepted one takes the lowest free one. A team the
// host seated by hand is left where it is — a application owns its own seat and no
// other.
func ReseatTx(ctx context.Context, tx *sql.Tx, slot Slot, towns Towns) error {
	return reseatTx(ctx, tx, slot, towns, 0)
}

func reseatTx(ctx context.Context, tx *sql.Tx, slot Slot, towns Towns, drop int64) error {
	apps, err := SlotApplications(ctx, tx, slot.ID)
	if err != nil {
		return err
	}
	accepted := make([]Application, 0, len(apps))
	for _, a := range apps {
		if a.Status != StatusAccepted {
			continue
		}
		if a, err = resolveSeatTx(ctx, tx, slot, a); err != nil {
			return err
		}
		accepted = append(accepted, a)
	}
	seated, err := gameTeams(ctx, tx, slot.FestID, slot.GameID)
	if err != nil {
		return err
	}
	delete(seated, drop)
	assignNumbers(accepted, seated)
	for _, a := range accepted {
		seated[a.Number] = protocol.RosterTeam{Name: a.TeamName, City: towns(a.RatingTeamID), Number: a.Number}
	}

	teams := make([]protocol.RosterTeam, 0, len(seated))
	for _, team := range seated {
		teams = append(teams, team)
	}
	sort.SliceStable(teams, func(i, j int) bool { return teams[i].Number < teams[j].Number })
	if err := foldTeamsTx(ctx, tx, slot.FestID, slot.GameID, teams); err != nil {
		return err
	}
	if err := pruneRenumberedTx(ctx, tx, slot.GameID, teams); err != nil {
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
		if err := linkRegistryTeamTx(ctx, tx, slot.FestID, participantID, a, towns(a.RatingTeamID)); err != nil {
			return err
		}
		if err := writeGameRosterTx(ctx, tx, slot.FestID, slot.GameID, participantID, a.Roster); err != nil {
			return err
		}
	}
	return nil
}

// resolveSeatTx re-finds a application's seat after the host renumbered its team on
// the game page: renumbering mints a Participant at the new number and leaves
// the old row behind, so the application's own pointer goes stale. The team is
// looked up again by the name and registry row it plays under, and the application
// is repointed; a team that is no longer in the Game reads as unseated.
func resolveSeatTx(ctx context.Context, tx *sql.Tx, slot Slot, app Application) (Application, error) {
	if app.Number > 0 || app.ParticipantID == 0 {
		return app, nil
	}
	var (
		participantID int64
		number        int64
	)
	err := tx.QueryRowContext(ctx, `
select p.id, gp.number
from game_participants gp
join participants p on p.id = gp.participant_id
left join fest_teams ft on ft.id = p.fest_team_id
where gp.game_id = ? and p.name = ?
order by case when coalesce(ft.rating_id, 0) = ? then 0 else 1 end, gp.number
limit 1`, slot.GameID, app.TeamName, app.RatingTeamID).Scan(&participantID, &number)
	if errors.Is(err, sql.ErrNoRows) {
		participantID, number = 0, 0
	} else if err != nil {
		return app, err
	}
	if participantID > 0 && participantID != app.ParticipantID {
		if err := carryOverSeatTx(ctx, tx, slot.GameID, app.ParticipantID, participantID); err != nil {
			return app, err
		}
	}
	if _, err := tx.ExecContext(ctx, `update slot_applications set participant_id = ? where id = ?`,
		util.NullableInt64(participantID), app.ID); err != nil {
		return app, err
	}
	app.ParticipantID, app.Number = participantID, number
	return app, nil
}

// carryOverSeatTx moves what a seat owns from the Participant a renumber
// retired to the one it minted: the contested answers are the jury's to rule on and
// the roster is the sitting's, and both are keyed on the Participant.
func carryOverSeatTx(ctx context.Context, tx *sql.Tx, gameID, from, to int64) error {
	for _, table := range []string{"od_contested", "game_team_players"} {
		if _, err := tx.ExecContext(ctx,
			`update or ignore `+table+` set participant_id = ? where game_id = ? and participant_id = ?`,
			to, gameID, from); err != nil {
			return err
		}
		// What the update ignored is a row the new Participant already owns.
		if _, err := tx.ExecContext(ctx,
			`delete from `+table+` where game_id = ? and participant_id = ?`, gameID, from); err != nil {
			return err
		}
	}
	return nil
}

// pruneRenumberedTx drops the Participants the document no longer names — what
// a renumber leaves behind, since it mints a row at the new number rather than
// moving the old one.
func pruneRenumberedTx(ctx context.Context, tx *sql.Tx, gameID int64, teams []protocol.RosterTeam) error {
	args := []any{gameID}
	holes := ""
	for _, team := range teams {
		if holes != "" {
			holes += ", "
		}
		holes += "?"
		args = append(args, team.Number)
	}
	where := `where game_id = ?`
	if holes != "" {
		where += ` and number not in (` + holes + `)`
	}
	// A contested answer outlives the sitting it was given at, so a Participant that
	// still owns one is never debris.
	var owned int
	if err := tx.QueryRowContext(ctx, `
select count(*) from od_contested
where participant_id in (select id from participants `+where+`)`, args...).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return ErrHasResults
	}
	_, err := tx.ExecContext(ctx, `delete from participants `+where, args...)
	return err
}

func gameTeams(ctx context.Context, q store.Queryer, festID, gameID int64) (map[int64]protocol.RosterTeam, error) {
	doc, err := store.LoadGameDoc(ctx, q, festID, gameID)
	if err != nil {
		return nil, err
	}
	var state games.ODState
	_ = json.Unmarshal([]byte(doc.State), &state)
	out := make(map[int64]protocol.RosterTeam, len(state.Teams))
	for _, team := range state.Teams {
		if team.Number > 0 {
			out[team.Number] = protocol.RosterTeam{Name: team.Name, City: team.City, Number: team.Number}
		}
	}
	return out, nil
}

func assignNumbers(accepted []Application, seated map[int64]protocol.RosterTeam) {
	taken := map[int64]bool{}
	for number := range seated {
		taken[number] = true
	}
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

func linkRegistryTeamTx(ctx context.Context, tx *sql.Tx, festID, participantID int64, app Application, town string) error {
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
values(?, ?, ?, ?, ?, 0)`, festID, app.RatingTeamID, app.TeamName, town, position); err != nil {
			return err
		}
	} else if err != nil {
		return err
		// The town comes from the rating site, and a Venue that used to stamp
		// its own city on every team it seated leaves rows to correct.
	} else if _, err := tx.ExecContext(ctx, `
update fest_teams set name = ?, city = coalesce(nullif(?, ''), city) where id = ?`, app.TeamName, town, teamID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `update participants set fest_team_id = ? where id = ?`, teamID, participantID)
	return err
}

// writeGameRosterTx mirrors a roster into the Slot's own roster
// (game_team_players, which the Game reads because its roster_source is
// 'game'), so a team's players belong to the sitting they played, not to the
// Venue. The rating player ids stay on the application — they are what the players
// export needs.
func writeGameRosterTx(ctx context.Context, tx *sql.Tx, festID, gameID, participantID int64, players []RosterPlayer) error {
	if _, err := tx.ExecContext(ctx,
		`delete from game_team_players where game_id = ? and participant_id = ?`, gameID, participantID); err != nil {
		return err
	}
	for order, p := range players {
		playerID, err := roster.EnsureSeedPlayerNamed(ctx, tx, festID,
			strings.TrimSpace(p.Name), strings.TrimSpace(p.Surname), strings.TrimSpace(p.Patronymic))
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
	contested, err := store.LoadContested(ctx, q, gameID)
	if err != nil {
		return false, err
	}
	for _, c := range contested {
		if c.Number == number {
			return true, nil
		}
	}
	return false, nil
}

func RetourTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, comp []int) error {
	doc, err := store.LoadGameDoc(ctx, tx, festID, gameID)
	if err != nil {
		return err
	}
	if sameComp(games.ParseTourComp(doc.SchemeJSON), comp) {
		return nil
	}
	seated, err := gameTeams(ctx, tx, festID, gameID)
	if err != nil {
		return err
	}
	teams := make([]protocol.RosterTeam, 0, len(seated))
	for _, team := range seated {
		teams = append(teams, team)
	}
	sort.SliceStable(teams, func(i, j int) bool { return teams[i].Number < teams[j].Number })
	emptyScheme, emptyState := games.ODEmptyGameJSON(doc.Slug.String, "", comp)
	if _, err := tx.ExecContext(ctx, `update games set scheme_json = ?, updated_at = ? where id = ? and fest_id = ?`,
		string(emptyScheme), util.UtcNow(), gameID, festID); err != nil {
		return err
	}
	if err := flatgame.SetStateTx(ctx, tx, festID, gameID, string(emptyState)); err != nil {
		return err
	}
	return foldTeamsTx(ctx, tx, festID, gameID, teams)
}

func sameComp(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
