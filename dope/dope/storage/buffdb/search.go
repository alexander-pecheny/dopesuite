package buffdb

import (
	"context"
	"strings"
	"time"
)

// The rating site's people and teams, for the fest roster editor to suggest
// (ADR-0024): anybody the site knows, not only the fest's own.

// Person is one player of the rating site.
type Person struct {
	ID         int64
	Name       string
	Patronymic string
	Surname    string
	// Games is how many tournaments buff counts for them: how a host tells
	// namesakes apart.
	Games int
}

// Team is one team of the rating site.
type Team struct {
	ID   int64
	Name string
	Town string
}

// SearchPlayers finds people by what a host types: a surname, or a surname
// and a name in either order, each as the start of the word. The most played
// come first.
func (s *Store) SearchPlayers(ctx context.Context, query string, limit int) []Person {
	words := strings.Fields(query)
	if !s.Enabled() || len(words) == 0 || len(words) > 3 {
		return nil
	}
	var where []string
	var args []any
	if len(words) == 1 {
		where = append(where, `p.surname like ?`)
		args = append(args, titleFold(words[0])+"%")
	} else {
		a, b := titleFold(words[0]), titleFold(words[1])
		where = append(where, `(p.surname like ? and p.name like ?) or (p.surname like ? and p.name like ?)`)
		args = append(args, a+"%", b+"%", b+"%", a+"%")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
select p.id, coalesce(p.name, ''), coalesce(p.patronymic, ''), coalesce(p.surname, ''), coalesce(g.games, 0)
from players p left join player_games g on g.player_id = p.id
where `+strings.Join(where, " and ")+`
order by coalesce(g.games, 0) desc, p.surname, p.name
limit ?`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Patronymic, &p.Surname, &p.Games); err != nil {
			return out
		}
		out = append(out, p)
	}
	return out
}

// SearchTeams finds teams whose name, or a word of it, starts with what a host
// types. Teams that played most recently come first.
func (s *Store) SearchTeams(ctx context.Context, query string, limit int) []Team {
	q := strings.ToLower(strings.TrimSpace(query))
	if !s.Enabled() || q == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
select id, name, town from teams
where name_fold like ? or name_fold like ?
order by coalesce(last_tournament_id, 0) desc, name
limit ?`, q+"%", "% "+q+"%", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Town); err != nil {
			return out
		}
		out = append(out, t)
	}
	return out
}

// BaseRoster is a team's base roster in the season running
// on the day given: the people still on it, in the site's order. It answers
// nothing when buff has not mirrored that team's season, so the caller can ask
// the site.
func (s *Store) BaseRoster(ctx context.Context, teamID int64, day time.Time) []Person {
	if !s.Enabled() || teamID <= 0 {
		return nil
	}
	stamp := day.UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx, `
select p.id, coalesce(p.name, ''), coalesce(p.patronymic, ''), coalesce(p.surname, ''), coalesce(g.games, 0)
from team_seasons ts
join seasons se on se.id = ts.season_id
join players p on p.id = ts.player_id
left join player_games g on g.player_id = p.id
where ts.team_id = ? and se.date_start <= ? and se.date_end > ? and ts.player_id > 0
  and (ts.date_removed is null or ts.date_removed > ?)
order by ts.player_number, p.surname, p.name`, teamID, stamp, stamp, stamp)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Patronymic, &p.Surname, &p.Games); err != nil {
			return out
		}
		out = append(out, p)
	}
	return out
}
