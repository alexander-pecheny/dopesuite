package games

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Мультиигры (Даугавпилс, медиаигры, раздаточные конкурсы) pure domain logic.
//
// Several small games in one sitting: every team gets the same sheets, one per
// мини-игра, and solves them at the same time. A cell holds the points that
// task earned — not a mark — because a task may be worth anything and may be
// solved by halves, and Итог is their plain sum with a subtotal per мини-игра.
//
// What a cell may hold is the мини-игра's own business, so the scheme declares
// a domain per column: a set of values or a range of them. That is validation
// and it is the cell editor — a domain of two or three values is a cell you
// click through, a wider one is a cell you type into.

// MultiColumn is one task: the values its cell may hold, ascending.
type MultiColumn struct {
	Values []int `json:"values"`
}

// Max is the most a task can pay — what the sheet prints as its номинал.
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

// MultiGame is one мини-игра: its name and its tasks in order.
type MultiGame struct {
	Name    string        `json:"name"`
	Columns []MultiColumn `json:"columns"`
}

// multiRangeSpan caps a range so a typo — {0-100000} — is a complaint rather
// than a sheet nobody can draw.
const multiRangeSpan = 1000

var multiSpecRe = regexp.MustCompile(`^\{([^}]*)\}(?:[xх]([0-9]+))?$`)
var multiRangeRe = regexp.MustCompile(`^(-?[0-9]+)-(-?[0-9]+)$`)

// ParseMultiGames reads the мини-игра spec a host writes: one line per game,
// `Имя: {домен}xN {домен}…`, the specs of a line concatenating into its
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
		if !ok || name == "" {
			return nil, fmt.Errorf("строка %d: жду «Имя: {значения}xN»", n+1)
		}
		game := MultiGame{Name: name}
		for _, spec := range strings.Fields(specs) {
			columns, err := parseMultiSpec(spec)
			if err != nil {
				return nil, fmt.Errorf("строка %d, %s: %w", n+1, name, err)
			}
			game.Columns = append(game.Columns, columns...)
		}
		if len(game.Columns) == 0 {
			return nil, fmt.Errorf("строка %d, %s: ни одного задания", n+1, name)
		}
		games = append(games, game)
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("ни одной мини-игры")
	}
	return games, nil
}

func parseMultiSpec(spec string) ([]MultiColumn, error) {
	parts := multiSpecRe.FindStringSubmatch(spec)
	if parts == nil {
		return nil, fmt.Errorf("%q — жду {значения} или {a-b}, можно с xN", spec)
	}
	values, err := parseMultiDomain(parts[1])
	if err != nil {
		return nil, err
	}
	count := 1
	if parts[2] != "" {
		if count, err = strconv.Atoi(parts[2]); err != nil || count < 1 {
			return nil, fmt.Errorf("%q — повтор xN считается от 1", spec)
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
		return nil, fmt.Errorf("пустой домен {}")
	}
	if !strings.Contains(inner, ",") {
		if ends := multiRangeRe.FindStringSubmatch(inner); ends != nil {
			from, _ := strconv.Atoi(ends[1])
			to, _ := strconv.Atoi(ends[2])
			if to < from {
				return nil, fmt.Errorf("{%s} — диапазон читается снизу вверх", inner)
			}
			if to-from > multiRangeSpan {
				return nil, fmt.Errorf("{%s} — диапазон шире %d значений", inner, multiRangeSpan)
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
			return nil, fmt.Errorf("{%s} — %q не число", inner, item)
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

// MultiScheme is the game document's shape: its мини-игры and, when a fest
// wants one, the comparators that break a tie on Итог.
type MultiScheme struct {
	Minigames []MultiGame `json:"minigames"`
	Sorting   []string    `json:"sorting"`
}

// MultiState is the persisted state JSON: the participants and one cell grid
// per мини-игра, each row a participant in participants order.
type MultiState struct {
	Participants []KSIParticipant `json:"participants"`
	Declined     map[string]bool  `json:"declined"`
	Games        []struct {
		Cells [][]int `json:"cells"`
	} `json:"games"`
	Finished bool `json:"finished"`
}

// MultiEmptyGameJSON builds the pristine scheme/state for a Мультиигры game.
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
// Итог, Σ+ and the per-мини-игра subtotals.
type MultiResultsTeam struct {
	Index int
	Place float64
	Total int
	Plus  int
	Games []int
}

// MultiMetricNames is what a scheme may rank a Мультиигры game on: Итог, Σ+
// and one name per мини-игра, numbered from 1 in the order they are played.
func MultiMetricNames(games []MultiGame) []string {
	names := []string{"total", "plus"}
	for i := range games {
		names = append(names, fmt.Sprintf("game%d", i+1))
	}
	return names
}

// ComputeMultiResults scores a Мультиигры game from its scheme and state.
// Cells outside a мини-игра's declared width are ignored: the scheme is what
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
		team := MultiResultsTeam{Index: p, Games: make([]int, len(scheme.Minigames))}
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
				team.Games[g] += value
				team.Total += value
				if value > 0 {
					team.Plus += value
				}
			}
		}
		ranked = append(ranked, team)
	}

	order := scheme.Sorting
	if len(order) == 0 {
		order = []string{"total"}
	}
	metric := func(team MultiResultsTeam, name string) (int, bool) {
		switch name {
		case "total":
			return team.Total, true
		case "plus":
			return team.Plus, true
		}
		if index, err := strconv.Atoi(strings.TrimPrefix(name, "game")); err == nil &&
			strings.HasPrefix(name, "game") && index >= 1 && index <= len(team.Games) {
			return team.Games[index-1], true
		}
		return 0, false
	}
	for _, name := range order {
		if _, ok := metric(MultiResultsTeam{Games: make([]int, len(scheme.Minigames))}, name); !ok {
			return nil, fmt.Errorf("sorting: %s не считается — есть %s", name,
				strings.Join(MultiMetricNames(scheme.Minigames), ", "))
		}
	}
	same := func(a, b MultiResultsTeam) bool {
		for _, name := range order {
			av, _ := metric(a, name)
			bv, _ := metric(b, name)
			if av != bv {
				return false
			}
		}
		return true
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		for _, name := range order {
			av, _ := metric(ranked[i], name)
			bv, _ := metric(ranked[j], name)
			if av != bv {
				return av > bv
			}
		}
		return ranked[i].Index < ranked[j].Index
	})
	// A shared place is the mean of the places it covers, as everywhere in
	// dope: место is what a Structure pays очки on, and splitting a tie the
	// game did not break would invent a difference.
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
