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
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// The seeding an assembled-team format needs: a Participant here is three
// people who played the fest's other Games for three other teams, so its
// seed number comes from what those teams did, not from anything this
// Participant has ever done. Troika's rulebook §4.4.2 — "the seed number is
// the arithmetic mean of the sum of places the players' teams took in
// Voprosiki and Team Svoyachok" — with §4.4.3's tiebreak, the best single
// place sum, right behind it.
//
// The scheme says it like this:
//
//	[init]
//	seed: players
//	games: [voprosiki, svoyachok]
//	player.place_sum: place1 + place2
//	seed.mean: mean(place_sum)
//	seed.best: min(place_sum)
//	sorting: [mean asc, best asc]
//
// place1..placeN are the player's own team's places in the games named, in
// that order. A player whose team did not play one of them counts as one
// place worse than that game's last — an assembled team is not rewarded for
// sitting a discipline out, and the import does not stop because of one.

var seedAggRe = regexp.MustCompile(`^(mean|min|max|sum|count)\(\s*([^)\s]+)\s*\)$`)

// FromPlayers is the composing seeding the Game's [init] declares.
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
		return seeding{}, corei18n.User(dopestrings.Default.Imports.SeedPlayers.NoGames())
	}
	if len(f.sort) == 0 {
		return seeding{}, corei18n.User(dopestrings.Default.Imports.SeedPlayers.NoSorting())
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
			return seeding{}, corei18n.User(dopestrings.Default.Imports.SeedPlayers.MetricUnknown(rule.Metric, strings.Join(sortedKeys(aggregates), ", ")))
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
		return seeding{}, corei18n.User(dopestrings.Default.Imports.SeedPlayers.NoRosters())
	}

	type scored struct {
		participant int64
		name        string
		number      int
		metrics     map[string]float64
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
				return seeding{}, corei18n.User(dopestrings.Default.Imports.SeedPlayers.MetricMissing(name, agg.over))
			}
			metrics[name] = agg.fold(values)
		}
		table = append(table, scored{participant: entry.participant, name: entry.name, number: entry.number, metrics: metrics})
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
		// Number is the Participant's number inside this Game, not a fest
		// number: the Participant id is what names it to the import.
		candidates[i] = seedCandidate{SourceRank: i + 1, Name: row.name, Number: row.number, Participant: row.participant}
	}
	return seeding{source: "players", label: dopestrings.Default.Imports.SeedSource.Players(), candidates: candidates}, nil
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
			return corei18n.User(dopestrings.Default.Imports.SeedPlayers.SelfReference(grain, name))
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
			return nil, corei18n.User(dopestrings.Default.Imports.SeedPlayers.AggregateExpected(name))
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

// teamPlacesByFestTeam is a Game's table as places against fest teams — what a
// player's own team took there. A Game with several tables has no single
// place, so it is refused rather than guessed at.
func loadSeedSourceGame(ctx context.Context, q store.Queryer, festID int64, code string) (seedSourceGame, error) {
	var gameID int64
	if err := q.QueryRowContext(ctx, `select id from games where fest_id = ? and code = ?`, festID, code).Scan(&gameID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return seedSourceGame{}, corei18n.User(dopestrings.Default.Imports.Seed.GameMissing(code))
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

// participantFestTeam is the rating team a Participant plays as: the link a
// migration once wrote, else the fest team under the Participant's number —
// the number is a team Participant's identity (ADR-0009), and nothing writes
// the link for a Participant minted since, so every source Game of a newer
// fest read as having no table at all. A troika is no fest team: null.
const participantFestTeam = `coalesce(p.fest_team_id, (
  select ft.id from fest_teams ft
  where ft.fest_id = p.fest_id and ft.deleted = 0 and ft.number = p.number
    and p.roster = 'team' and p.assembled = 0 limit 1))`

func teamPlacesByFestTeam(ctx context.Context, q store.Queryer, festID int64, code string) (map[int64]float64, error) {
	rows, err := store.CollectRows(ctx, q, `
select `+participantFestTeam+`, st.rank
from stage_standings st
join participants p on p.id = st.participant_id
join stages s on s.id = st.stage_id
join games g on g.id = s.game_id
where g.fest_id = ? and g.code = ? and `+participantFestTeam+` is not null
order by st.rank`, []any{festID, code}, func(rs *sql.Rows) ([2]int64, error) {
		var pair [2]int64
		return pair, rs.Scan(&pair[0], &pair[1])
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, corei18n.User(dopestrings.Default.Imports.Seed.NoStandings(code))
	}
	places := make(map[int64]float64, len(rows))
	worst := 0.0
	for _, pair := range rows {
		place := float64(pair[1])
		if _, seen := places[pair[0]]; seen {
			return nil, corei18n.User(dopestrings.Default.Imports.SeedPlayers.MultipleStandings(code))
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
	participant int64
	name        string
	number      int
	players     []seedPlayer
}

// gameRoster is who played a Game and for whom: fest team by player, the
// Game's own overrides applied. A player overridden into a team plays for
// that team and no longer for the one the registry lists them under — which
// is how three people from three teams come to be one Troika team, and how
// the same three are still found on their own teams in the source Games.
func gameRoster(ctx context.Context, q store.Queryer, festID, gameID int64) (map[int64]int64, error) {
	teamOf, err := store.CollectRows(ctx, q, `
select ftp.player_id, ftp.team_id
from fest_team_players ftp
join participants p on `+participantFestTeam+` = ftp.team_id and p.fest_id = ?
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
		id        int64
		team      int64
		name      string
		number    int
		assembled bool
	}
	participants, err := store.CollectRows(ctx, q, `
select p.id, coalesce(`+participantFestTeam+`, 0), p.name, coalesce(ga.number, 0), p.assembled
from participants p
join game_assignments ga on ga.participant_id = p.id and ga.game_id = ?
where p.fest_id = ?
order by ga.number, p.id`, []any{scope.GameID, scope.FestID}, func(rs *sql.Rows) (participant, error) {
		var row participant
		return row, rs.Scan(&row.id, &row.team, &row.name, &row.number, &row.assembled)
	})
	if err != nil {
		return nil, err
	}
	members, err := assembledFestPlayers(ctx, q, scope)
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
		entry := seedEntry{participant: row.id, name: row.name, number: row.number}
		people := byTeam[row.team]
		if row.assembled {
			// A troika's people are its own roster (the fest's troikas page), not a
			// fest team's: the rating roster names them, and the name is the
			// link — the same one the troikas page's credited-team column reads.
			people = members[row.id]
		}
		for _, playerID := range people {
			player := seedPlayer{id: playerID, places: make([]float64, len(sources))}
			for i, source := range sources {
				player.places[i] = source.places[source.roster[playerID]]
			}
			entry.players = append(entry.players, player)
		}
		if len(entry.players) == 0 {
			return nil, corei18n.User(dopestrings.Default.Imports.SeedPlayers.NoRoster(row.name))
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// seedSourceGame is one Game a player's place is read from: its table by fest
// team, and who played it for whom.
type seedSourceGame struct {
	places map[int64]float64
	roster map[int64]int64
}

// assembledFestPlayers maps each troika seated in this Game to its people as
// fest players — the rating roster's rows, whose teams the source Games
// ranked. A troika's roster lives in players, the fest roster in fest_players,
// and the two meet on the name only; a person the rating roster does not know
// is left out (their places are unknown), and a namesake resolves to the
// first fest player of that name.
func assembledFestPlayers(ctx context.Context, q store.Queryer, scope core.FestScope) (map[int64][]int64, error) {
	festPlayers, err := store.CollectRows(ctx, q, `
select id, first_name, last_name from fest_players where fest_id = ? order by id`,
		[]any{scope.FestID}, func(rs *sql.Rows) (struct {
			id          int64
			first, last string
		}, error) {
			var row struct {
				id          int64
				first, last string
			}
			return row, rs.Scan(&row.id, &row.first, &row.last)
		})
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	for _, fp := range festPlayers {
		key := util.AlphaKey(store.JoinPlayerName(fp.first, fp.last))
		if _, seen := byName[key]; !seen {
			byName[key] = fp.id
		}
	}
	rows, err := store.CollectRows(ctx, q, `
select pp.participant_id, pl.first_name, pl.last_name
from participant_players pp
join players pl on pl.id = pp.player_id
join participants p on p.id = pp.participant_id and p.assembled = 1
join game_assignments ga on ga.participant_id = p.id and ga.game_id = ?
order by pp.participant_id, pp.roster_order`, []any{scope.GameID}, func(rs *sql.Rows) (struct {
		team        int64
		first, last string
	}, error) {
		var row struct {
			team        int64
			first, last string
		}
		return row, rs.Scan(&row.team, &row.first, &row.last)
	})
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, row := range rows {
		if id, ok := byName[util.AlphaKey(store.JoinPlayerName(row.first, row.last))]; ok {
			out[row.team] = append(out[row.team], id)
		}
	}
	return out, nil
}
