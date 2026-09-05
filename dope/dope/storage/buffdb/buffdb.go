// Package buffdb reads buff's mirror of rating.chgk.info (ADR-0020): player
// names for the roster suggest, teams and their base rosters, and the
// tournaments playable on a date. It is opened read-only and fails soft — a
// missing file, a missing table or a broken query gives an empty answer, never
// an error page, because every caller is a suggest or a flag.
package buffdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	Games      int    `json:"games"`
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
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Type            string  `json:"type"`
	QuestionsByTour string  `json:"questionsByTour"`
	DateStart       string  `json:"dateStart"`
	Editors         string  `json:"editors"`
	Difficulty      float64 `json:"difficulty"`
	Teams           int     `json:"teams"`
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

// Players suggests by name: the first word is a surname prefix, the second a
// first-name one, the third a patronymic — "Plotnikov D" is one player, not a
// surname nobody has. The order is how many games each has played, so a
// namesake with hundreds comes before one with two.
func (s *Store) Players(ctx context.Context, query string, limit int) []Player {
	surname, name, patronymic := splitNameQuery(query)
	if !s.Enabled() || surname == "" {
		return nil
	}
	where, args := likeAny("surname", surname, likePrefix)
	for _, part := range []struct {
		col, word string
	}{{"name", name}, {"patronymic", patronymic}} {
		if part.word == "" {
			continue
		}
		clause, more := likeAny(part.col, part.word, likePrefix)
		where += " and " + clause
		args = append(args, more...)
	}
	args = append(args, capLimit(limit))
	rows, err := s.db.QueryContext(ctx, `
select p.id, coalesce(p.surname, ''), coalesce(p.name, ''), coalesce(p.patronymic, ''), coalesce(g.games, 0)
from players p
left join player_games g on g.player_id = p.id
where `+where+`
order by coalesce(g.games, 0) desc, p.surname, p.name, p.id
limit ?`, args...)
	if err != nil {
		return s.playersAlphabetical(ctx, where, args)
	}
	defer rows.Close()
	return scanPlayers(rows)
}

// playersAlphabetical is the answer without player_games — a mirror that has
// not been rebuilt since the table was added still suggests, just unranked.
func (s *Store) playersAlphabetical(ctx context.Context, where string, args []any) []Player {
	rows, err := s.db.QueryContext(ctx, `
select id, coalesce(surname, ''), coalesce(name, ''), coalesce(patronymic, ''), 0
from players
where `+where+`
order by surname, name, id
limit ?`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanPlayers(rows)
}

func scanPlayers(rows *sql.Rows) []Player {
	var out []Player
	for rows.Next() {
		var p Player
		if err := rows.Scan(&p.ID, &p.Surname, &p.Name, &p.Patronymic, &p.Games); err != nil {
			return out
		}
		out = append(out, p)
	}
	return out
}

// splitNameQuery reads "Surname Name Patronymic" as far as it was typed; the
// surname always comes first, which is what keeps idx_players_surname useful.
func splitNameQuery(query string) (surname, name, patronymic string) {
	words := strings.Fields(query)
	for i, word := range words {
		switch i {
		case 0:
			surname = word
		case 1:
			name = word
		case 2:
			patronymic = word
		}
	}
	return surname, name, patronymic
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
	where, args := likeAny("r.team_current_name", prefix, likePrefix)
	rows, err := s.db.QueryContext(ctx, `
select r.team_id, r.team_current_name, coalesce(r.team_current_town, '')
from tournament_results r
where `+where+`
group by r.team_id
order by r.team_current_name, r.team_id
limit ?`, append(args, capLimit(limit))...)
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

// BaseRoster is the team's base roster as of at: the player ids the rating site
// declared for the season containing at, or — while the team has declared
// nothing for it, which most teams have not months in — for the last season it
// did. The `player_id = 0` rows the mirror writes for a fetched-but-empty
// roster are not players and never make a season the answer. The second result
// is false when the mirror knows no roster at all, which is what makes every
// player read as legionnaire.
func (s *Store) BaseRoster(ctx context.Context, teamID int64, at time.Time) (map[int64]bool, bool) {
	if !s.Enabled() || teamID <= 0 {
		return nil, false
	}
	rows, err := s.db.QueryContext(ctx, `
select ts.season_id, ts.player_id
from team_seasons ts
join seasons s on s.id = ts.season_id
where ts.team_id = ? and ts.player_id > 0
  and substr(s.date_start, 1, 10) <= ?
order by s.date_start desc`, teamID, at.Format("2006-01-02"))
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	out := map[int64]bool{}
	season := int64(0)
	for rows.Next() {
		var seasonID, id int64
		if err := rows.Scan(&seasonID, &id); err != nil {
			return nil, false
		}
		if season == 0 {
			season = seasonID
		} else if seasonID != season {
			break
		}
		out[id] = true
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// playableTypes are the tournament types a Slot can play: a sync tournament is played
// on its own week, an async tournament any time inside its window.
var playableTypes = []string{"Синхрон", "Строго синхронный", "Асинхрон"}

// IsAsync says a tournament is played any time inside its window rather than on
// its own week — the one distinction a caller outside this package draws.
func IsAsync(kind string) bool { return kind == playableTypes[2] }

// ratingClock is the clock rating.chgk.info keeps: a sync tournament's window runs
// from Saturday 10:00 to the next Saturday 10:00 Moscow time. A Slot's wall
// time carries no zone, so it is read on the same clock.
var ratingClock = time.FixedZone("MSK", 3*60*60)

// instant renders a Slot's wall time the way SQLite's datetime() renders the
// mirror's offset-bearing timestamps, so the two compare as strings.
func instant(wall time.Time) string {
	if wall.IsZero() {
		return ""
	}
	at := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, ratingClock)
	return at.UTC().Format("2006-01-02 15:04:05")
}

const withinWindow = "(? = '' or (datetime(t.date_start) <= ? and datetime(t.date_end) > ?))"

// tournamentCols is everything the picker shows about a tournament. The editors
// are a list of player ids in the mirror, so their names are joined here rather
// than asked for a second time, and the requests count is buff's own sum of
// what the venues declared.
const tournamentCols = `
t.id, coalesce(t.name, ''), coalesce(t.tournament_type, ''), coalesce(t.questions_by_tour, ''), coalesce(t.date_start, ''),
coalesce((select group_concat(trim(coalesce(p.name, '') || ' ' || coalesce(p.surname, '')), ', ')
          from json_each(case when json_valid(t.editors) then t.editors else '[]' end) e
          join players p on p.id = e.value), ''),
coalesce(t.difficulty_forecast, 0), coalesce(r.teams, 0)`

const tournamentFrom = `
from tournaments t left join tournament_requests r on r.tournament_id = t.id`

func (s *Store) PlayableTournaments(ctx context.Context, at time.Time) []Tournament {
	if !s.Enabled() || at.IsZero() {
		return nil
	}
	when := instant(at)
	rows, err := s.db.QueryContext(ctx, `
select `+tournamentCols+tournamentFrom+`
where t.tournament_type in (?, ?, ?)
  and `+withinWindow+`
order by case when t.tournament_type = 'Асинхрон' then 1 else 0 end, t.date_start, t.id`,
		playableTypes[0], playableTypes[1], playableTypes[2], when, when, when)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return scanTournaments(rows)
}

func (s *Store) SearchTournaments(ctx context.Context, q string, at time.Time, limit int) []Tournament {
	q = strings.TrimSpace(q)
	if !s.Enabled() || q == "" {
		return nil
	}
	when := instant(at)
	where, args := likeAny("t.name", q, likeInfix)
	// An id is typed to name one tournament and no other, so it answers even
	// when that tournament is not playable at the time asked about.
	id, _ := strconv.ParseInt(q, 10, 64)
	args = append([]any{id}, args...)
	args = append(args, when, when, when, capLimit(limit))
	rows, err := s.db.QueryContext(ctx, `
select `+tournamentCols+tournamentFrom+`
where t.id = ? or (`+where+` and `+withinWindow+`)
order by t.date_start desc, t.id
limit ?`, args...)
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
	var t Tournament
	err := s.db.QueryRowContext(ctx, `
select `+tournamentCols+tournamentFrom+`
where t.id = ?`, id).Scan(scanTargets(&t)...)
	if err != nil {
		return Tournament{}, false
	}
	return t, true
}

func scanTournaments(rows *sql.Rows) []Tournament {
	var out []Tournament
	for rows.Next() {
		var t Tournament
		if err := rows.Scan(scanTargets(&t)...); err != nil {
			return out
		}
		out = append(out, t)
	}
	return out
}

func scanTargets(t *Tournament) []any {
	return []any{&t.ID, &t.Name, &t.Type, &t.QuestionsByTour, &t.DateStart, &t.Editors, &t.Difficulty, &t.Teams}
}

func capLimit(limit int) int {
	if limit <= 0 || limit > 200 {
		return 20
	}
	return limit
}

func likePrefix(prefix string) string { return escapeLike(prefix) + "%" }

func likeInfix(s string) string { return "%" + escapeLike(s) + "%" }

// likeAny matches col against the word as the mirror spells it and, when that
// differs, as it was typed. SQLite's LIKE folds case for ASCII only, so
// `surname like 'peche%'` never finds "Pecheny"; each branch is still a plain
// prefix, so the index carries it. wrap turns a word into its LIKE pattern.
func likeAny(col, word string, wrap func(string) string) (string, []any) {
	words := []string{titleFold(word)}
	if words[0] != word {
		words = append(words, word)
	}
	parts := make([]string, len(words))
	args := make([]any, len(words))
	for i, w := range words {
		parts[i] = col + ` like ? escape '\'`
		args[i] = wrap(w)
	}
	if len(parts) == 1 {
		return parts[0], args
	}
	return "(" + strings.Join(parts, " or ") + ")", args
}

// titleFold is the rating site's spelling of a name: first rune upper, rest
// lower.
func titleFold(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	out := make([]rune, len(runes))
	out[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		out[i] = unicode.ToLower(runes[i])
	}
	return string(out)
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
