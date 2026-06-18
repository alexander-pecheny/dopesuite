// Package numbering holds the fest team-numbering data layer: the team row shape
// used by the numbering UI/import and the read helpers that report whether a
// fest's teams are fully numbered. These are pure data-access functions over a
// store.Queryer — no server coupling — so both the server and the import/handler
// packages can depend on them directly.
package numbering

import (
	"context"
	"database/sql"
	"fmt"

	"dope/dope/store"
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

// LoadFestTeams returns the fest's active teams in display order, each with its
// current number (0 when unset).
func LoadFestTeams(ctx context.Context, q store.Queryer, festID int64) ([]Team, error) {
	return store.CollectRows(ctx, q, `
select id, name, city, coalesce(number, 0)
from fest_teams
where fest_id = ? and deleted = 0
order by position, id`, []any{festID}, func(rows *sql.Rows) (Team, error) {
		var team Team
		if err := rows.Scan(&team.ID, &team.Name, &team.City, &team.Number); err != nil {
			return team, err
		}
		return team, nil
	})
}

// AllNumbered reports whether every active team in the fest has a number, plus
// the active-team count.
func AllNumbered(ctx context.Context, q store.Queryer, festID int64) (bool, int, error) {
	var total, numbered int
	if err := q.QueryRowContext(ctx, `
select count(*), coalesce(sum(case when number is not null then 1 else 0 end), 0)
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
