// Package buffdb reads buff's mirror of rating.chgk.info (ADR-0020): player
// names for the состав suggest, teams and their base rosters, and the
// tournaments playable on a date. It is opened read-only and fails soft — a
// missing file, a missing table or a broken query gives an empty answer, never
// an error page, because every caller is a suggest or a flag.
package buffdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const PathEnv = "DOPE_BUFF_DB"

// Store answers the venue pages' questions about rating.chgk.info. The zero
// value is Disabled: every method returns nothing.
type Store struct {
	db *sql.DB
}

type Player struct {
	ID         int64  `json:"id"`
	Surname    string `json:"surname"`
	Name       string `json:"name"`
	Patronymic string `json:"patronymic"`
}

func (p Player) FullName() string {
	return strings.TrimSpace(strings.Join(strings.Fields(p.Surname+" "+p.Name+" "+p.Patronymic), " "))
}

type Team struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Town string `json:"town"`
}

type Tournament struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	QuestionsByTour string `json:"questionsByTour"`
	DateStart       string `json:"dateStart"`
}

func Disabled() *Store { return &Store{} }

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Disabled(), nil
	}
	if _, err := os.Stat(path); err != nil {
		return Disabled(), nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return Disabled(), err
	}
	db.SetMaxOpenConns(4)
	return &Store{db: db}, nil
}

func (s *Store) Enabled() bool { return s != nil && s.db != nil }

func (s *Store) Close() error {
	if !s.Enabled() {
		return nil
	}
	return s.db.Close()
}

func TourComposition(questionsByTour string) []int {
	var out []int
	for _, part := range strings.Split(questionsByTour, ",") {
		n := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &n); err != nil || n <= 0 {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (s *Store) Players(ctx context.Context, prefix string, limit int) []Player {
	prefix = strings.TrimSpace(prefix)
	if !s.Enabled() || prefix == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
select id, coalesce(surname, ''), coalesce(name, ''), coalesce(patronymic, '')
from players
where surname like ? escape '\'
order by surname, name, id
limit ?`, likePrefix(prefix), capLimit(limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Player
	for rows.Next() {
		var p Player
		if err := rows.Scan(&p.ID, &p.Surname, &p.Name, &p.Patronymic); err != nil {
			return out
		}
		out = append(out, p)
	}
	return out
}

func (s *Store) Player(ctx context.Context, id int64) (Player, bool) {
	if !s.Enabled() || id <= 0 {
		return Player{}, false
	}
	var p Player
	err := s.db.QueryRowContext(ctx, `
select id, coalesce(surname, ''), coalesce(name, ''), coalesce(patronymic, '')
from players where id = ?`, id).Scan(&p.ID, &p.Surname, &p.Name, &p.Patronymic)
	if err != nil {
		return Player{}, false
	}
	return p, true
}

func (s *Store) TeamName(ctx context.Context, teamID int64) string {
	if !s.Enabled() || teamID <= 0 {
		return ""
	}
	var name string
	err := s.db.QueryRowContext(ctx, `
select coalesce(r.team_current_name, '')
from tournament_results r join tournaments t on t.id = r.id
where r.team_id = ?
order by t.date_start desc
limit 1`, teamID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

func (s *Store) Team(ctx context.Context, teamID int64) (Team, bool) {
	if !s.Enabled() || teamID <= 0 {
		return Team{}, false
	}
	team := Team{ID: teamID}
	err := s.db.QueryRowContext(ctx, `
select coalesce(r.team_current_name, ''), coalesce(r.team_current_town, '')
from tournament_results r join tournaments t on t.id = r.id
where r.team_id = ?
order by t.date_start desc
limit 1`, teamID).Scan(&team.Name, &team.Town)
	if err != nil {
		return Team{}, false
	}
	return team, true
}

func (s *Store) Teams(ctx context.Context, prefix string, limit int) []Team {
	prefix = strings.TrimSpace(prefix)
	if !s.Enabled() || prefix == "" {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
select r.team_id, r.team_current_name, coalesce(r.team_current_town, '')
from tournament_results r
where r.team_current_name like ? escape '\'
group by r.team_id
order by r.team_current_name, r.team_id
limit ?`, likePrefix(prefix), capLimit(limit))
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

// BaseRoster is the team's base roster for the season containing at: the
// player ids the rating site declared. The second result is false when the
// mirror knows no roster, which is what makes every player read as легионер.
func (s *Store) BaseRoster(ctx context.Context, teamID int64, at time.Time) (map[int64]bool, bool) {
	if !s.Enabled() || teamID <= 0 {
		return nil, false
	}
	day := at.Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
select ts.player_id
from team_seasons ts
join seasons s on s.id = ts.season_id
where ts.team_id = ?
  and substr(s.date_start, 1, 10) <= ?
  and substr(s.date_end, 1, 10) >= ?`, teamID, day, day)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, false
		}
		out[id] = true
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// playableTypes are the tournament types a Слот can play: a синхрон is played
// on its own week, an асинхрон any time inside its window.
var playableTypes = []string{"Синхрон", "Строго синхронный", "Асинхрон"}

func (s *Store) PlayableTournaments(ctx context.Context, on time.Time) []Tournament {
	if !s.Enabled() {
		return nil
	}
	day := on.Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
select id, coalesce(name, ''), coalesce(tournament_type, ''), coalesce(questions_by_tour, ''), coalesce(date_start, '')
from tournaments
where tournament_type in (?, ?, ?)
  and substr(date_start, 1, 10) <= ?
  and substr(date_end, 1, 10) >= ?
order by case when tournament_type = 'Асинхрон' then 1 else 0 end, date_start, id`,
		playableTypes[0], playableTypes[1], playableTypes[2], day, day)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanTournaments(rows)
}

func (s *Store) SearchTournaments(ctx context.Context, q string, on time.Time, limit int) []Tournament {
	q = strings.TrimSpace(q)
	if !s.Enabled() || q == "" {
		return nil
	}
	day := "0000-00-00"
	if !on.IsZero() {
		day = on.Format("2006-01-02")
	}
	rows, err := s.db.QueryContext(ctx, `
select id, coalesce(name, ''), coalesce(tournament_type, ''), coalesce(questions_by_tour, ''), coalesce(date_start, '')
from tournaments
where name like ? escape '\'
  and (? = '0000-00-00' or (substr(date_start, 1, 10) <= ? and substr(date_end, 1, 10) >= ?))
order by date_start desc, id
limit ?`, "%"+escapeLike(q)+"%", day, day, day, capLimit(limit))
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanTournaments(rows)
}

func (s *Store) Tournament(ctx context.Context, id int64) (Tournament, bool) {
	if !s.Enabled() || id <= 0 {
		return Tournament{}, false
	}
	t := Tournament{ID: id}
	err := s.db.QueryRowContext(ctx, `
select coalesce(name, ''), coalesce(tournament_type, ''), coalesce(questions_by_tour, ''), coalesce(date_start, '')
from tournaments where id = ?`, id).Scan(&t.Name, &t.Type, &t.QuestionsByTour, &t.DateStart)
	if err != nil {
		return Tournament{}, false
	}
	return t, true
}

func scanTournaments(rows *sql.Rows) []Tournament {
	var out []Tournament
	for rows.Next() {
		var t Tournament
		if err := rows.Scan(&t.ID, &t.Name, &t.Type, &t.QuestionsByTour, &t.DateStart); err != nil {
			return out
		}
		out = append(out, t)
	}
	return out
}

func capLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 20
	}
	return limit
}

func likePrefix(prefix string) string { return escapeLike(prefix) + "%" }

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
