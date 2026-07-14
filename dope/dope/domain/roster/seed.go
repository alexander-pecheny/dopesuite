package roster

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"dope/dope/platform/util"
	"dope/dope/storage/store"
)

type SeedRosterPlayer struct {
	FirstName string
	LastName  string
}

func EnsureSeedTeam(ctx context.Context, tx *sql.Tx, festID int64, name, city string, players []SeedRosterPlayer) (int64, string, error) {
	name = strings.TrimSpace(name)
	city = strings.TrimSpace(city)
	if name == "" {
		return 0, "", errors.New("empty team name")
	}

	var teamID int64
	var existingCity string
	err := tx.QueryRowContext(ctx, `
select id, city
from teams
where fest_id = ? and name = ?
order by case when city = ? then 0 when city = '' then 1 else 2 end, id
limit 1`, festID, name, city).Scan(&teamID, &existingCity)
	if errors.Is(err, sql.ErrNoRows) {
		teamID, err = store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, name, city)
		if err != nil {
			return 0, "", err
		}
		existingCity = city
	} else if err != nil {
		return 0, "", err
	}
	if city != "" && existingCity == "" {
		if _, err := tx.ExecContext(ctx, `update teams set city = ? where id = ?`, city, teamID); err != nil {
			return 0, "", err
		}
		existingCity = city
	}
	if len(players) > 0 {
		if err := ReplaceSeedTeamRoster(ctx, tx, festID, teamID, players); err != nil {
			return 0, "", err
		}
	}
	return teamID, existingCity, nil
}

func ReplaceSeedTeamRoster(ctx context.Context, tx *sql.Tx, festID, teamID int64, players []SeedRosterPlayer) error {
	if _, err := tx.ExecContext(ctx, `delete from team_players where team_id = ?`, teamID); err != nil {
		return err
	}
	for rosterOrder, player := range players {
		firstName := strings.TrimSpace(player.FirstName)
		lastName := strings.TrimSpace(player.LastName)
		if firstName == "" && lastName == "" {
			continue
		}
		playerID, err := EnsureSeedPlayer(ctx, tx, festID, firstName, lastName)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
			return err
		}
	}
	return nil
}

func EnsureSeedPlayer(ctx context.Context, tx *sql.Tx, festID int64, firstName, lastName string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
select id from players
where fest_id = ? and first_name = ? and last_name = ?
order by id
limit 1`, festID, firstName, lastName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.InsertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
	}
	return id, err
}

func SeedTeamNameKey(name string) string {
	return util.AlphaKey(name)
}
