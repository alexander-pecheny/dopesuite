// Package numbering holds the fest team-numbering data layer: the team row shape
// used by the numbering UI/import and the read helpers that report whether a
// fest's teams are fully numbered. These are pure data-access functions over a
// store.Queryer — no server coupling — so both the server and the import/handler
// packages can depend on them directly.
package numbering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"dope/dope/domain/games"
	"dope/dope/storage/store"
)

// MaxNumber is the largest team number accepted by the numbering UI/import.
const MaxNumber = 9999

// Team is a fest team as seen by the numbering flow.
type Team struct {
	ID     int64
	Name   string
	City   string
	Number int
}

// DisplayName renders a team as "Name (City)", or just "Name" when city is empty.
func DisplayName(team Team) string {
	if team.City == "" {
		return team.Name
	}
	return fmt.Sprintf("%s (%s)", team.Name, team.City)
}

// LoadTeams returns the active teams a numbering covers in display order, each
// with its current number (0 when unset): a site's teams under the site's own
// numbers, or the whole fest's under the fest's for site 0.
func LoadTeams(ctx context.Context, q store.Queryer, festID, siteID int64) ([]Team, error) {
	query, args := `
select id, name, city, coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}
	if siteID > 0 {
		query, args = `
select id, name, city, coalesce(site_number, 0)
from fest_teams
where fest_id = ? and site_id = ? and deleted = 0
order by position, id`, []any{festID, siteID}
	}
	return store.CollectRows(ctx, q, query, args, func(rows *sql.Rows) (Team, error) {
		var team Team
		if err := rows.Scan(&team.ID, &team.Name, &team.City, &team.Number); err != nil {
			return team, err
		}
		return team, nil
	})
}

// Numbered is the SQL condition that a fest team has the number its Games
// show: its site's, for a team on a site.
const Numbered = `(case when site_id is null then number else site_number end) is not null`

// AllNumbered reports whether every active team in the fest has a number, plus
// the active-team count.
func AllNumbered(ctx context.Context, q store.Queryer, festID int64) (bool, int, error) {
	var total, numbered int
	if err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(case when `+Numbered+` then 1 else 0 end), 0)
from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&total, &numbered); err != nil {
		return false, 0, err
	}
	if total == 0 {
		return false, 0, nil
	}
	return numbered == total, total, nil
}

// HasUnnumbered reports whether the fest has active teams of which some lack a
// number — the precondition that blocks game editing (see requireNumberedTeams).
// A fest with no teams at all returns false: there is nothing to number yet, so
// editing (e.g. player-mode KSI) is not blocked.
func HasUnnumbered(ctx context.Context, q store.Queryer, festID int64) (bool, error) {
	allNumbered, total, err := AllNumbered(ctx, q, festID)
	if err != nil {
		return false, err
	}
	return total > 0 && !allNumbered, nil
}

// GameHasUnnumbered is the guard scoped to one Game (ADR-0009). A Game that
// names its entrants numbers them from 1 as it seats them, so it is never
// blocked: a fest may register a team that plays nothing — Studchr registered 65
// and its EK seated 48 — and an unnumbered row the Game never seats says
// nothing about whether the Game can be scored.
//
// A Game with no entrant list of its own falls back to the fest's registry,
// which is what every Game did before Games could differ.
//
// A Game whose Protocol refers to no fest team by number (a friendship cup,
// whose tables are nobody on the roster) is never blocked either.
func GameHasUnnumbered(ctx context.Context, q store.Queryer, festID, gameID int64) (bool, error) {
	if gameID > 0 {
		var gameType string
		err := q.QueryRowContext(ctx, `select game_type from games where fest_id = ? and id = ?`, festID, gameID).Scan(&gameType)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil && !games.UsesFestNumbers(gameType) {
			return false, nil
		}
		var entrants, unnumbered int
		if err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(case when number > 0 then 0 else 1 end), 0)
from game_participants where game_id = ?`, gameID).Scan(&entrants, &unnumbered); err != nil {
			return false, err
		}
		if entrants > 0 {
			return unnumbered > 0, nil
		}
		var site sql.NullInt64
		if err := q.QueryRowContext(ctx, `select site_id from games where id = ?`, gameID).Scan(&site); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if site.Int64 > 0 {
			var unnumberedOnSite bool
			err := q.QueryRowContext(ctx, `
select exists(select 1 from fest_teams where site_id = ? and deleted = 0 and site_number is null)`, site.Int64).Scan(&unnumberedOnSite)
			return unnumberedOnSite, err
		}
	}
	return HasUnnumbered(ctx, q, festID)
}
