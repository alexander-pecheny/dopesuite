package games

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// The Friendship Cup (CONTEXT.md, Kubok Druzhby) is an OD played by players
// rather than teams: a host seats n tables, each registered player draws a
// route card with a number, and the card sends him to one table in each tour,
// so the people at a table change from tour to tour. A table is scored exactly
// as an OD team — the document is an OD document whose teams are the tables —
// and a player scores what the tables on his card took in their tours
// (ADR-0026).

// KDPlayer is one registered player: the card he drew, his name, and — for
// the standings only — the fest team he came with.
type KDPlayer struct {
	Card int    `json:"card"`
	Name string `json:"name"`
	Team string `json:"team,omitempty"`
}

// KDState is the Friendship Cup document: an OD state with its players.
type KDState struct {
	ODState
	Players []KDPlayer `json:"players"`
}

// KDTables reads how many tables the scheme seats.
func KDTables(schemeJSON string) int {
	var probe struct {
		Tables int `json:"kdTables"`
	}
	_ = json.Unmarshal([]byte(schemeJSON), &probe)
	return probe.Tables
}

// IsPrime reports whether n is a prime.
func IsPrime(n int) bool {
	if n < 2 {
		return false
	}
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// KDTable is the table card c sends its holder to in tour t (both from 1)
// when n tables play: ((c−1) mod n + ⌊(c−1)/n⌋·(t−1)) mod n + 1. Cards 1…n
// stay at their table all game (the jokers); for a prime n two cards meet
// at one table at most once in n tours, and every tour seats as many players
// at each table as at any other, give or take one. It is 0 for a card or a
// tour out of range.
func KDTable(card, tour, n int) int {
	if card < 1 || tour < 1 || n < 1 {
		return 0
	}
	i := card - 1
	return (i%n+(i/n)*(tour-1))%n + 1
}

// KDEmptyGameJSON is a pristine Friendship Cup: the OD scheme and state with
// n tables for teams and no players.
func KDEmptyGameJSON(slug, title string, tourComp []int, n int, tableName func(int) string) ([]byte, []byte) {
	totalQuestions := 0
	for _, size := range tourComp {
		totalQuestions += size
	}
	entries := make([][]int, totalQuestions)
	for i := range entries {
		entries[i] = []int{}
	}
	tables := make([]ODTeam, n)
	for i := range tables {
		tables[i] = ODTeam{Name: tableName(i + 1), Number: int64(i + 1)}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      KD,
		"tourComp":      tourComp,
		"kdTables":      n,
		"nTeams":        n,
		"teams":         []any{},
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"teams":          tables,
		"entries":        entries,
		"completed":      make([]bool, totalQuestions),
		"shootoutRounds": []any{},
		"players":        []any{},
	}))
	return schemeJSON, stateJSON
}

// KDResultsPlayer is one row of the personal standings.
type KDResultsPlayer struct {
	Place string `json:"place"` // tie-grouped ("1", "2–4"); "" before any question is completed
	Card  int    `json:"card"`
	Name  string `json:"name"`
	Team  string `json:"team,omitempty"`
	Total int    `json:"total"`
	// Tables is the table the card seats him at in each tour, Tours what that
	// table took there.
	Tables []int `json:"tables"`
	Tours  []int `json:"tours"`
	// Best counts his tours by what his table took, from a full tour down:
	// Best[0] tours with every question, Best[1] with all but one, and so on
	// for as many places as the tie-break reads (Regulations: 4, 3, 2 of 4).
	Best []int `json:"best"`
}

// KDResults is the Friendship Cup personal standings.
type KDResults struct {
	TourComp []int             `json:"tourComp"`
	Tables   int               `json:"tables"`
	Players  []KDResultsPlayer `json:"players"`
}

// kdTiebreaks is how many "tours with all but k questions" the tie-break
// reads: a full tour, one short, two short.
const kdTiebreaks = 3

// ComputeKDResults ranks the players: by the sum of their tables' tours, then
// by how many tours their table took in full, one short, two short. Players
// equal on all of it share a place.
func ComputeKDResults(schemeJSON, stateJSON string) (KDResults, error) {
	tables, err := ComputeODResults(schemeJSON, stateJSON)
	if err != nil {
		return KDResults{}, err
	}
	var state KDState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return KDResults{}, fmt.Errorf("parse friendship cup state: %w", err)
		}
	}
	n := KDTables(schemeJSON)
	if n <= 0 {
		n = len(state.Teams)
	}
	byNumber := make(map[int64][]int, len(tables.Teams))
	anyPlace := false
	for _, table := range tables.Teams {
		byNumber[table.Number] = table.TourTotals
		anyPlace = anyPlace || table.Place != ""
	}
	out := KDResults{TourComp: tables.TourComp, Tables: n, Players: make([]KDResultsPlayer, 0, len(state.Players))}
	for _, player := range state.Players {
		row := KDResultsPlayer{Card: player.Card, Name: player.Name, Team: player.Team,
			Tables: make([]int, len(tables.TourComp)), Tours: make([]int, len(tables.TourComp)), Best: make([]int, kdTiebreaks)}
		for t, size := range tables.TourComp {
			table := KDTable(player.Card, t+1, n)
			row.Tables[t] = table
			took := 0
			if sums := byNumber[int64(table)]; t < len(sums) {
				took = sums[t]
			}
			row.Tours[t] = took
			row.Total += took
			if short := size - took; short >= 0 && short < kdTiebreaks {
				row.Best[short]++
			}
		}
		out.Players = append(out.Players, row)
	}
	sort.SliceStable(out.Players, func(i, j int) bool {
		a, b := out.Players[i], out.Players[j]
		if kdAhead(a, b) || kdAhead(b, a) {
			return kdAhead(a, b)
		}
		return a.Card < b.Card
	})
	if anyPlace {
		for i := 0; i < len(out.Players); {
			j := i
			for j+1 < len(out.Players) && !kdAhead(out.Players[i], out.Players[j+1]) {
				j++
			}
			label := strconv.Itoa(i + 1)
			if j > i {
				label = fmt.Sprintf("%d–%d", i+1, j+1)
			}
			for k := i; k <= j; k++ {
				out.Players[k].Place = label
			}
			i = j + 1
		}
	}
	return out, nil
}

// kdAhead reports whether a stands above b: a larger sum, else more full
// tours, then more tours one short, then two short.
func kdAhead(a, b KDResultsPlayer) bool {
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	for k := range a.Best {
		if a.Best[k] != b.Best[k] {
			return a.Best[k] > b.Best[k]
		}
	}
	return false
}
