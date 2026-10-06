package roster

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"dope/dope/storage/store"
)

// This file is the one home of the fest's Divisions (CONTEXT.md, Division): which
// ones the fest offers, and who plays in one. A Division is a view over the
// teams' Flags (ADR-0020), so both answers come from fest_team_flags.

// FestDivisions is every Flag the fest's teams carry, by short name, in the
// order the roster lists the teams and each team its Flags: the Divisions a
// Game that seats the fest's teams can offer.
func FestDivisions(ctx context.Context, q store.Queryer, festID int64) ([]string, error) {
	flags, err := store.CollectRows(ctx, q, `
select f.short from fest_team_flags f join fest_teams t on t.id = f.team_id
where t.fest_id = ? and t.deleted = 0
order by t.position, t.id, f.position`, []any{festID}, func(rows *sql.Rows) (string, error) {
		var short string
		return short, rows.Scan(&short)
	})
	if err != nil {
		return nil, err
	}
	return CleanDivisions(flags), nil
}

// InDivision reports whether a Participant with these Flags plays in a
// Division as a scheme writes it: a Flag it carries, or with a leading minus a
// Flag it does not carry (ADR-0020).
func InDivision(flags []string, division string) bool {
	flag, exclude := strings.CutPrefix(strings.TrimSpace(division), "-")
	return slices.Contains(flags, strings.TrimSpace(flag)) != exclude
}

// NumberedTeamFlags maps each of the fest's numbered teams, by its fest number,
// to its Flags' short names: what InDivision asks about a candidate known by
// its number.
func NumberedTeamFlags(ctx context.Context, q store.Queryer, festID int64) (map[int][]string, error) {
	type flag struct {
		number int
		short  string
	}
	rows, err := store.CollectRows(ctx, q, `
select t.number, f.short from fest_teams t join fest_team_flags f on f.team_id = t.id
where t.fest_id = ? and t.deleted = 0 and t.number is not null
order by t.id, f.position`, []any{festID}, func(rows *sql.Rows) (flag, error) {
		var f flag
		return f, rows.Scan(&f.number, &f.short)
	})
	if err != nil {
		return nil, err
	}
	out := map[int][]string{}
	for _, f := range rows {
		out[f.number] = append(out[f.number], strings.TrimSpace(f.short))
	}
	return out, nil
}

// CleanDivisions trims a list of Division names and drops the empty and the
// repeated ones, keeping the first spelling's order.
func CleanDivisions(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
