package imports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/domain/expr"
	"dope/dope/storage/store"
)

// The посев a сборная format needs: a Participant here is three people who
// played the фест's other Games for three other teams, so its seed number
// comes from what those teams did, not from anything this Participant has
// ever done. Троечка's регламент §4.4.2 — «номер посева определяется средним
// арифметическим суммы мест в „Вопросиках“ и „Командном своячке“ команд
// игроков» — with §4.4.3's tiebreak, the best single сумма мест, right behind
// it.
//
// The scheme says it like this:
//
//	[init]
//	seed: players
//	games: [вопросики, своячок]
//	player.place_sum: place1 + place2
//	seed.mean: mean(place_sum)
//	seed.best: min(place_sum)
//	sorting: [mean asc, best asc]
//
// place1..placeN are the player's own team's places in the games named, in
// that order. A player whose team did not play one of them counts as one
// place worse than that game's last — a сборная is not rewarded for sitting a
// discipline out, and the import does not stop because of one.

var seedAggRe = regexp.MustCompile(`^(mean|min|max|sum|count)\(\s*([^)\s]+)\s*\)$`)

// FromPlayers is the composing посев the Game's [init] declares.
func FromPlayers(spec *store.SchemePlayerSeed, sort []store.SchemeSortRule) SeedSource {
	return fromPlayers{spec: spec, sort: sort}
}

type fromPlayers struct {
	spec *store.SchemePlayerSeed
	sort []store.SchemeSortRule
}

// seedPlayer is one person on a Participant, with the places their own team
// took in each source Game.
type seedPlayer struct {
	id     int64
	places []float64
}

func (f fromPlayers) resolve(ctx context.Context, tx *sql.Tx, scope core.FestScope) (seeding, error) {
	if f.spec == nil || len(f.spec.Games) == 0 {
		return seeding{}, errors.New("посев по игрокам: схема не называет игры-источники")
	}
	if len(f.sort) == 0 {
		return seeding{}, errors.New("посев по игрокам: схема не говорит, чем сортировать ([init] sorting)")
	}

	playerRules, err := compileNamedExprs("player", f.spec.Player)
	if err != nil {
		return seeding{}, err
	}
	aggregates, err := parseSeedAggregates(f.spec.Seed)
	if err != nil {
		return seeding{}, err
	}
	for _, rule := range f.sort {
		if _, ok := aggregates[rule.Metric]; !ok {
			return seeding{}, fmt.Errorf("sorting: %s не считается — есть %s",
				rule.Metric, strings.Join(sortedKeys(aggregates), ", "))
		}
	}

	sources := make([]seedSourceGame, len(f.spec.Games))
	for i, code := range f.spec.Games {
		if sources[i], err = loadSeedSourceGame(ctx, tx, scope.FestID, code); err != nil {
			return seeding{}, err
		}
	}

	entries, err := seedParticipantPlayers(ctx, tx, scope, sources)
	if err != nil {
		return seeding{}, err
	}
	if len(entries) == 0 {
		return seeding{}, errors.New("посев по игрокам: у команд этой игры нет составов")
	}

	type scored struct {
		name    string
		number  int
		metrics map[string]float64
	}
	table := make([]scored, 0, len(entries))
	for _, entry := range entries {
		perPlayer := map[string][]float64{}
		for _, player := range entry.players {
			scope := expr.Vars{}
			for i, place := range player.places {
				scope[fmt.Sprintf("place%d", i+1)] = place
			}
			for _, rule := range playerRules {
				value, err := rule.expr.Eval(scope)
				if err != nil {
					return seeding{}, fmt.Errorf("player.%s: %w", rule.name, err)
				}
				scope[rule.name] = value
				perPlayer[rule.name] = append(perPlayer[rule.name], value)
			}
		}
		metrics := map[string]float64{}
		for name, agg := range aggregates {
			values, ok := perPlayer[agg.over]
			if !ok {
				return seeding{}, fmt.Errorf("seed.%s: %s — такой метрики игрока нет", name, agg.over)
			}
			metrics[name] = agg.fold(values)
		}
		table = append(table, scored{name: entry.name, number: entry.number, metrics: metrics})
	}

	rules := f.sort
	sort.SliceStable(table, func(i, j int) bool {
		for _, rule := range rules {
			a, b := table[i].metrics[rule.Metric], table[j].metrics[rule.Metric]
			if a == b {
				continue
			}
			if rule.Dir == "asc" {
				return a < b
			}
			return a > b
		}
		return table[i].number < table[j].number
	})
	candidates := make([]seedCandidate, len(table))
	for i, row := range table {
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: row.name, Number: row.number}
	}
	return seeding{source: "players", label: "по игрокам", candidates: candidates}, nil
}

type namedExpr struct {
	name string
	expr *expr.Expr
}

// compileNamedExprs parses a grain's rules and orders them so one reading
// another runs second — the same derivation the scoring rules use, since a
// map of keys has no order to rely on.
func compileNamedExprs(grain string, sources map[string]string) ([]namedExpr, error) {
	parsed := map[string]*expr.Expr{}
	for name, src := range sources {
		e, err := expr.Parse(src)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", grain, name, err)
		}
		parsed[name] = e
	}
	var ordered []namedExpr
	state := map[string]int{}
	var visit func(string, []string) error
	visit = func(name string, trail []string) error {
		switch state[name] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("%s.%s: правило зависит от самого себя", grain, name)
		}
		state[name] = 1
		for _, dep := range parsed[name].Vars() {
			if _, ours := parsed[dep]; ours {
				if err := visit(dep, append(trail, name)); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		ordered = append(ordered, namedExpr{name: name, expr: parsed[name]})
		return nil
	}
	for _, name := range sortedKeys(parsed) {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

type seedAggregate struct {
	over string
	fold func([]float64) float64
}

func parseSeedAggregates(sources map[string]string) (map[string]seedAggregate, error) {
	out := map[string]seedAggregate{}
	for name, src := range sources {
		parts := seedAggRe.FindStringSubmatch(strings.TrimSpace(src))
		if parts == nil {
			return nil, fmt.Errorf("seed.%s: жду mean(метрика), min, max, sum или count", name)
		}
		out[name] = seedAggregate{over: parts[2], fold: seedFolds[parts[1]]}
	}
	return out, nil
}

func seedSum(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}

var seedFolds = map[string]func([]float64) float64{
	"mean": func(values []float64) float64 {
		if len(values) == 0 {
			return 0
		}
		return seedSum(values) / float64(len(values))
	},
	"sum": seedSum,
	"min": func(values []float64) float64 {
		best := 0.0
		for i, v := range values {
			if i == 0 || v < best {
				best = v
			}
		}
		return best
	},
	"max": func(values []float64) float64 {
		best := 0.0
		for i, v := range values {
			if i == 0 || v > best {
				best = v
			}
		}
		return best
	},
	"count": func(values []float64) float64 { return float64(len(values)) },
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// teamPlacesByFestTeam is a Game's table as places against фест teams — what a
// player's own team took there. A Game with several tables has no single
// place, so it is refused rather than guessed at.
func loadSeedSourceGame(ctx context.Context, q store.Queryer, festID int64, code string) (seedSourceGame, error) {
	var gameID int64
	if err := q.QueryRowContext(ctx, `select id from games where fest_id = ? and code = ?`, festID, code).Scan(&gameID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return seedSourceGame{}, fmt.Errorf("в фесте нет игры с кодом %s", code)
		}
		return seedSourceGame{}, err
	}
	places, err := teamPlacesByFestTeam(ctx, q, festID, code)
	if err != nil {
		return seedSourceGame{}, err
	}
	roster, err := gameRoster(ctx, q, festID, gameID)
	if err != nil {
		return seedSourceGame{}, err
	}
	return seedSourceGame{places: places, roster: roster}, nil
}

func teamPlacesByFestTeam(ctx context.Context, q store.Queryer, festID int64, code string) (map[int64]float64, error) {
	rows, err := store.CollectRows(ctx, q, `
select p.fest_team_id, st.rank
from stage_standings st
join participants p on p.id = st.participant_id
join stages s on s.id = st.stage_id
join games g on g.id = s.game_id
where g.fest_id = ? and g.code = ? and p.fest_team_id is not null
order by st.rank`, []any{festID, code}, func(rs *sql.Rows) ([2]int64, error) {
		var pair [2]int64
		return pair, rs.Scan(&pair[0], &pair[1])
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("у игры %s ещё нет таблицы", code)
	}
	places := make(map[int64]float64, len(rows))
	worst := 0.0
	for _, pair := range rows {
		place := float64(pair[1])
		if _, seen := places[pair[0]]; seen {
			return nil, fmt.Errorf("у игры %s несколько таблиц — посев по игрокам берётся из игры с одной", code)
		}
		places[pair[0]] = place
		if place > worst {
			worst = place
		}
	}
	// A team that did not play stands one place behind the last that did.
	places[0] = worst + 1
	return places, nil
}

type seedEntry struct {
	name    string
	number  int
	players []seedPlayer
}

// gameRoster is who played a Game and for whom: фест team by player, the
// Game's own overrides applied. A player overridden into a team plays for
// that team and no longer for the one the registry lists them under — which
// is how three people from three teams come to be one Троечка team, and how
// the same three are still found on their own teams in the source Games.
func gameRoster(ctx context.Context, q store.Queryer, festID, gameID int64) (map[int64]int64, error) {
	teamOf, err := store.CollectRows(ctx, q, `
select ftp.player_id, ftp.team_id
from fest_team_players ftp
join participants p on p.fest_team_id = ftp.team_id and p.fest_id = ?
join game_assignments ga on ga.participant_id = p.id and ga.game_id = ?`,
		[]any{festID, gameID}, func(rs *sql.Rows) ([2]int64, error) {
			var pair [2]int64
			return pair, rs.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return nil, err
	}
	roster := make(map[int64]int64, len(teamOf))
	for _, pair := range teamOf {
		if _, seen := roster[pair[0]]; !seen {
			roster[pair[0]] = pair[1]
		}
	}
	overrides, err := store.CollectRows(ctx, q, `
select player_id, override_team_id from game_player_team_overrides where game_id = ?`,
		[]any{gameID}, func(rs *sql.Rows) ([2]int64, error) {
			var pair [2]int64
			return pair, rs.Scan(&pair[0], &pair[1])
		})
	if err != nil {
		return nil, err
	}
	for _, pair := range overrides {
		roster[pair[0]] = pair[1]
	}
	return roster, nil
}

// seedParticipantPlayers lists this Game's Participants with their people and
// each person's place in every source Game — the place their own team took
// there, which is a different team for each of the three.
func seedParticipantPlayers(ctx context.Context, q store.Queryer, scope core.FestScope,
	sources []seedSourceGame) ([]seedEntry, error) {
	here, err := gameRoster(ctx, q, scope.FestID, scope.GameID)
	if err != nil {
		return nil, err
	}
	type participant struct {
		id     int64
		team   int64
		name   string
		number int
	}
	participants, err := store.CollectRows(ctx, q, `
select p.id, coalesce(p.fest_team_id, 0), p.name, coalesce(ga.number, 0)
from participants p
join game_assignments ga on ga.participant_id = p.id and ga.game_id = ?
where p.fest_id = ?
order by ga.number, p.id`, []any{scope.GameID, scope.FestID}, func(rs *sql.Rows) (participant, error) {
		var row participant
		return row, rs.Scan(&row.id, &row.team, &row.name, &row.number)
	})
	if err != nil {
		return nil, err
	}

	byTeam := map[int64][]int64{}
	for playerID, teamID := range here {
		byTeam[teamID] = append(byTeam[teamID], playerID)
	}
	for teamID := range byTeam {
		sort.Slice(byTeam[teamID], func(i, j int) bool { return byTeam[teamID][i] < byTeam[teamID][j] })
	}

	entries := make([]seedEntry, 0, len(participants))
	for _, row := range participants {
		entry := seedEntry{name: row.name, number: row.number}
		for _, playerID := range byTeam[row.team] {
			player := seedPlayer{id: playerID, places: make([]float64, len(sources))}
			for i, source := range sources {
				player.places[i] = source.places[source.roster[playerID]]
			}
			entry.players = append(entry.players, player)
		}
		if len(entry.players) == 0 {
			return nil, fmt.Errorf("посев по игрокам: у команды %s нет состава", row.name)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// seedSourceGame is one Game a player's place is read from: its table by фест
// team, and who played it for whom.
type seedSourceGame struct {
	places map[int64]float64
	roster map[int64]int64
}
