package games

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"
	corei18n "pecheny.me/dopecore/i18nstrings"
)

// Multi (Daugavpils, media games, handout contests) pure domain logic.
//
// Several small games in one sitting: every team gets the same sheets, one per
// minigame, and solves them at the same time. A cell holds the points that
// task earned — not a mark — because a task may be worth anything and may be
// solved by halves, and the total is their plain sum with a subtotal per
// minigame.
//
// What a cell may hold is the minigame's own business, so the scheme declares
// a domain per column: a set of values or a range of them. That is validation
// and it is the cell editor — a domain of two or three values is a cell you
// click through, a wider one is a cell you type into.

// MultiColumn is one task: the values its cell may hold, ascending, and the
// block of the sheet it stands in — «|» in the spec closes one.
type MultiColumn struct {
	Values []int `json:"values"`
	Block  int   `json:"block,omitempty"`
}

// Max is the most a task can pay — what the sheet prints as its nominal value.
func (c MultiColumn) Max() int {
	max := 0
	for i, v := range c.Values {
		if i == 0 || v > max {
			max = v
		}
	}
	return max
}

// Signed reports whether this task can cost a team points.
func (c MultiColumn) Signed() bool {
	for _, v := range c.Values {
		if v < 0 {
			return true
		}
	}
	return false
}

// MultiGame is one minigame: its name, its tasks in order, and whether it
// pays raw points or is scored against the best result in it.
type MultiGame struct {
	Name    string        `json:"name"`
	Columns []MultiColumn `json:"columns"`
	// Normalized: the minigame contributes its score as a share of the best
	// result, out of a hundred, rather than its own points — written
	// «→0..100». It is what lets minigames of quite different scales weigh the
	// same in the total: a media quiz worth 1570 and a song contest worth 57
	// both top out at 100.
	Normalized bool `json:"normalized,omitempty"`
}

// MultiNormalMax is what the best result in a normalised minigame is worth.
const MultiNormalMax = 100.0

// multiRangeSpan caps a range so a typo — {0-100000} — is a complaint rather
// than a sheet nobody can draw.
const multiRangeSpan = 1000

var multiSpecRe = regexp.MustCompile(`^\{([^}]*)\}(?:[xх]([0-9]+))?$`)

// The arrow may be typed either way — «→0..100» reads best, «->0..100» is what
// a keyboard offers.
var multiNormalRe = regexp.MustCompile(`\s*(?:→|->)\s*0\.\.100\s*$`)
var multiRangeRe = regexp.MustCompile(`^(-?[0-9]+)-(-?[0-9]+)$`)

// ParseMultiGames reads the minigame spec a host writes: one line per game,
// `Name: {values}xN {domain}…`, the specs of a line concatenating into its
// columns. Blank lines and # comments are skipped.
func ParseMultiGames(src string) ([]MultiGame, error) {
	var games []MultiGame
	for n, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, specs, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		normalized := multiNormalRe.MatchString(name)
		if normalized {
			name = strings.TrimSpace(multiNormalRe.ReplaceAllString(name, ""))
		}
		if !ok || name == "" {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.LineExpected(strconv.Itoa(n + 1)))
		}
		game := MultiGame{Name: name, Normalized: normalized}
		block, sinceBar := 0, 0
		for _, spec := range strings.Fields(specs) {
			if spec == "|" {
				if sinceBar == 0 {
					return nil, corei18n.User(dopestrings.Default.Games.Multi.BarNoTasksBefore(strconv.Itoa(n+1), name))
				}
				block++
				sinceBar = 0
				continue
			}
			columns, err := parseMultiSpec(spec)
			if err != nil {
				return nil, corei18n.User(dopestrings.Default.Games.Multi.LinePrefix(strconv.Itoa(n+1), name, err.Error()))
			}
			for i := range columns {
				columns[i].Block = block
			}
			game.Columns = append(game.Columns, columns...)
			sinceBar += len(columns)
		}
		if block > 0 && sinceBar == 0 {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.BarNoTasksAfter(strconv.Itoa(n+1), name))
		}
		if len(game.Columns) == 0 {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.NoTasks(strconv.Itoa(n+1), name))
		}
		games = append(games, game)
	}
	if len(games) == 0 {
		return nil, corei18n.User(dopestrings.Default.Games.Multi.NoGames())
	}
	return games, nil
}

func parseMultiSpec(spec string) ([]MultiColumn, error) {
	parts := multiSpecRe.FindStringSubmatch(spec)
	if parts == nil {
		return nil, corei18n.User(dopestrings.Default.Games.Multi.SpecExpected(spec))
	}
	values, err := parseMultiDomain(parts[1])
	if err != nil {
		return nil, err
	}
	count := 1
	if parts[2] != "" {
		if count, err = strconv.Atoi(parts[2]); err != nil || count < 1 {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.RepeatCount(spec))
		}
	}
	columns := make([]MultiColumn, count)
	for i := range columns {
		columns[i] = MultiColumn{Values: values}
	}
	return columns, nil
}

func parseMultiDomain(inner string) ([]int, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil, corei18n.User(dopestrings.Default.Games.Multi.DomainEmpty())
	}
	if !strings.Contains(inner, ",") {
		if ends := multiRangeRe.FindStringSubmatch(inner); ends != nil {
			from, _ := strconv.Atoi(ends[1])
			to, _ := strconv.Atoi(ends[2])
			if to < from {
				return nil, corei18n.User(dopestrings.Default.Games.Multi.RangeDescending("{" + inner + "}"))
			}
			if to-from > multiRangeSpan {
				return nil, corei18n.User(dopestrings.Default.Games.Multi.RangeTooWide("{"+inner+"}", strconv.Itoa(multiRangeSpan)))
			}
			values := make([]int, 0, to-from+1)
			for v := from; v <= to; v++ {
				values = append(values, v)
			}
			return values, nil
		}
	}
	seen := map[int]bool{}
	var values []int
	for _, item := range strings.Split(inner, ",") {
		item = strings.TrimSpace(item)
		v, err := strconv.Atoi(item)
		if err != nil {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.NotANumber("{"+inner+"}", item))
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		values = append(values, v)
	}
	sort.Ints(values)
	return values, nil
}

// MultiSigned reports whether any task can cost points — what decides whether
// the sheet is worth a Σ+ column at all.
func MultiSigned(games []MultiGame) bool {
	for _, game := range games {
		for _, column := range game.Columns {
			if column.Signed() {
				return true
			}
		}
	}
	return false
}

// MultiScheme is the game document's shape: its minigames and, when a fest
// wants one, the comparators that break a tie on the total.
type MultiScheme struct {
	Minigames []MultiGame `json:"minigames"`
	Sorting   []string    `json:"sorting"`
}

// MultiState is the persisted state JSON: the participants and one cell grid
// per minigame, each row a participant in participants order.
type MultiState struct {
	Participants []KSIParticipant `json:"participants"`
	Declined     map[string]bool  `json:"declined"`
	Games        []struct {
		Cells [][]int `json:"cells"`
	} `json:"games"`
	Finished bool `json:"finished"`
}

// MultiEmptyGameJSON builds the pristine scheme/state for a Multi game.
func MultiEmptyGameJSON(slug, title string, games []MultiGame, sorting []string) ([]byte, []byte) {
	scheme := map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      Multi,
		"participants":  []string{},
		"minigames":     games,
	}
	if len(sorting) > 0 {
		scheme["sorting"] = sorting
	}
	cells := make([]map[string]any, len(games))
	for i := range cells {
		cells[i] = map[string]any{"cells": [][]int{}}
	}
	return []byte(mustJSON(scheme)), []byte(mustJSON(map[string]any{
		"participants": []string{},
		"games":        cells,
		"finished":     false,
	}))
}

// MultiResultsTeam is one ranked team: its participant index, shared place,
// total, Σ+ and, per minigame, what it contributed and what it scored raw.
// The two differ only where a minigame is normalised, and both are shown —
// the sheet reads what was earned beside what it cost.
type MultiResultsTeam struct {
	Index int
	Place float64
	Total float64
	Plus  int
	Games []float64
	Raw   []int
}

// ParseMultiSorting reads the comparators a fest breaks a tie on the total
// with, checked against what these minigames measure — a name nobody counts
// would otherwise surface as an unranked table on the day. Blank is no
// comparator at all, which leaves equal totals sharing a place.
func ParseMultiSorting(minigames []MultiGame, raw string) ([]string, error) {
	known := map[string]bool{}
	for _, name := range MultiMetricNames(minigames) {
		known[name] = true
	}
	var order []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !known[item] {
			return nil, corei18n.User(dopestrings.Default.Games.Multi.MetricUnknown(item,
				strings.Join(MultiMetricNames(minigames), ", ")))
		}
		order = append(order, item)
	}
	return order, nil
}

// MultiMetricNames is what a scheme may rank a Multi game on: the total, Σ+
// and one name per minigame, numbered from 1 in the order they are played.
func MultiMetricNames(games []MultiGame) []string {
	names := []string{"total", "plus"}
	for i := range games {
		names = append(names, fmt.Sprintf("game%d", i+1))
	}
	return names
}

// ComputeMultiResults scores a Multi game from its scheme and state.
// Cells outside a minigame's declared width are ignored: the scheme is what
// says how wide a sheet is, and a stale grid must not pay.
func ComputeMultiResults(schemeJSON, stateJSON string) ([]MultiResultsTeam, error) {
	var scheme MultiScheme
	if schemeJSON != "" {
		if err := json.Unmarshal([]byte(schemeJSON), &scheme); err != nil {
			return nil, fmt.Errorf("parse multi scheme: %w", err)
		}
	}
	var state MultiState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("parse multi state: %w", err)
		}
	}

	ranked := make([]MultiResultsTeam, 0, len(state.Participants))
	for p := range state.Participants {
		if KSIParticipantDeclined(state.Declined, state.Participants[p]) {
			continue
		}
		team := MultiResultsTeam{
			Index: p,
			Games: make([]float64, len(scheme.Minigames)),
			Raw:   make([]int, len(scheme.Minigames)),
		}
		for g, game := range scheme.Minigames {
			if g >= len(state.Games) {
				continue
			}
			rows := state.Games[g].Cells
			if p >= len(rows) {
				continue
			}
			for c := range game.Columns {
				if c >= len(rows[p]) {
					break
				}
				value := rows[p][c]
				team.Raw[g] += value
				if value > 0 {
					team.Plus += value
				}
			}
		}
		ranked = append(ranked, team)
	}

	// A normalised minigame is scored against the best result in it — and the
	// best among the teams counted in the standings, since a team that refused
	// to play cannot set the scale for everyone else. Below nought is nought:
	// a team that finished on minus scores nothing for that minigame rather
	// than dragging its total down.
	best := make([]int, len(scheme.Minigames))
	for _, team := range ranked {
		for g, raw := range team.Raw {
			if raw > best[g] {
				best[g] = raw
			}
		}
	}
	for i := range ranked {
		team := &ranked[i]
		team.Total = 0
		for g, game := range scheme.Minigames {
			switch {
			case !game.Normalized:
				team.Games[g] = float64(team.Raw[g])
			case best[g] > 0 && team.Raw[g] > 0:
				team.Games[g] = MultiNormalMax * float64(team.Raw[g]) / float64(best[g])
			default:
				team.Games[g] = 0
			}
			team.Total += team.Games[g]
		}
	}

	order, err := ParseMultiSorting(scheme.Minigames, strings.Join(scheme.Sorting, ","))
	if err != nil {
		return nil, err
	}
	if len(order) == 0 {
		order = []string{"total"}
	}
	metric := func(team MultiResultsTeam, name string) float64 {
		switch name {
		case "total":
			return team.Total
		case "plus":
			return float64(team.Plus)
		}
		index, _ := strconv.Atoi(strings.TrimPrefix(name, "game"))
		return team.Games[index-1]
	}
	same := func(a, b MultiResultsTeam) bool {
		for _, name := range order {
			if metric(a, name) != metric(b, name) {
				return false
			}
		}
		return true
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		for _, name := range order {
			av, bv := metric(ranked[i], name), metric(ranked[j], name)
			if av != bv {
				return av > bv
			}
		}
		return ranked[i].Index < ranked[j].Index
	})
	// A shared place is the mean of the places it covers, as everywhere in
	// dope: a place is what a Structure pays points on, and splitting a tie
	// the game did not break would invent a difference.
	for start := 0; start < len(ranked); {
		end := start + 1
		for end < len(ranked) && same(ranked[end], ranked[start]) {
			end++
		}
		place := float64(start+end+1) / 2
		for i := start; i < end; i++ {
			ranked[i].Place = place
		}
		start = end
	}
	return ranked, nil
}
