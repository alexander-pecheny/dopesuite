package games

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ODTeam and ODState mirror the persisted OD game state JSON (see static/od.js).
// They are shared by the xlsx export and the server-side results view so the two
// scoring paths can't drift.
type ODTeam struct {
	Name   string `json:"name"`
	City   string `json:"city"`
	Number int64  `json:"number"`
}

type ODState struct {
	Teams     []ODTeam  `json:"teams"`
	Entries   [][]int64 `json:"entries"`
	Completed []bool    `json:"completed"`
}

// ODResultsTeam is one ranked row of an OD standings view.
type ODResultsTeam struct {
	Place      string `json:"place"`      // tie-grouped label ("1", "2–4"); "" before any question is completed
	Index      int    `json:"index"`      // roster index in state.teams
	Number     int64  `json:"number"`     // printed team number (0 if unnumbered)
	Name       string `json:"name"`       //
	City       string `json:"city"`       //
	Total      int    `json:"total"`      // questions taken across all tours (Σ on the page)
	TourTotals []int  `json:"tourTotals"` // questions taken per tour, aligned with tourComp
	Rating     int    `json:"rating"`     // Buchholz-style R: sum over taken questions of (teamCount − takers + 1)
	Mask       string `json:"mask"`       // per-question "1"/"0" (took/not), rating.chgk.info style, length = sum(tourComp)
}

// ODResults is the computed OD standings: the same totals and per-tour standings
// the #results page renders client-side, computed server-side so callers (bots,
// exports, integrations) don't have to replicate the scoring.
type ODResults struct {
	TourComp []int           `json:"tourComp"`
	Teams    []ODResultsTeam `json:"teams"` // ranked: total desc, then roster index
}

// ODEmptyGameJSON builds the pristine scheme/state for an OD game (no teams, no
// entries). Shared by game creation and the clear-to-pristine path so the two
// can't drift.
func ODEmptyGameJSON(slug, title string, tourComp []int) ([]byte, []byte) {
	totalQuestions := 0
	for _, n := range tourComp {
		totalQuestions += n
	}
	entries := make([][]int, totalQuestions)
	for i := range entries {
		entries[i] = []int{}
	}
	schemeJSON := []byte(mustJSON(map[string]any{
		"schemaVersion": 2,
		"slug":          slug,
		"title":         title,
		"gameType":      OD,
		"tourComp":      tourComp,
		"nTeams":        0,
		"teams":         []any{},
	}))
	stateJSON := []byte(mustJSON(map[string]any{
		"teams":          []any{},
		"entries":        entries,
		"completed":      make([]bool, totalQuestions),
		"shootoutRounds": []any{},
	}))
	return schemeJSON, stateJSON
}

// ParseTourComp reads scheme.tourComp, which is either a JSON array of ints or a
// comma-separated string with "count*repeat" segments (mirrors od.js).
func ParseTourComp(schemeJSON string) []int {
	if schemeJSON == "" {
		return nil
	}
	var probe struct {
		TourComp json.RawMessage `json:"tourComp"`
	}
	if err := json.Unmarshal([]byte(schemeJSON), &probe); err != nil || len(probe.TourComp) == 0 {
		return nil
	}
	var nums []int
	if err := json.Unmarshal(probe.TourComp, &nums); err == nil {
		return filterPositive(nums)
	}
	var s string
	if err := json.Unmarshal(probe.TourComp, &s); err != nil {
		return nil
	}
	var out []int
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if i := strings.IndexByte(seg, '*'); i >= 0 {
			count, _ := strconv.Atoi(strings.TrimSpace(seg[:i]))
			repeat, _ := strconv.Atoi(strings.TrimSpace(seg[i+1:]))
			for j := 0; j < repeat; j++ {
				if count > 0 {
					out = append(out, count)
				}
			}
			continue
		}
		if n, err := strconv.Atoi(seg); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func filterPositive(in []int) []int {
	out := in[:0:0]
	for _, n := range in {
		if n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// ComputeODResults scores an OD game from its scheme and state JSON, mirroring
// the client-side helpers in static/od.js (sumRow, tourSumsForTeam,
// ratingForTeam, computePlaces).
func ComputeODResults(schemeJSON, stateJSON string) (ODResults, error) {
	tours := ParseTourComp(schemeJSON)
	var state ODState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return ODResults{}, fmt.Errorf("parse OD state: %w", err)
		}
	}
	if len(tours) == 0 {
		// No tour composition recorded: treat every question as one tour, matching
		// the export's fallback so the view is never empty.
		tours = []int{len(state.Entries)}
	}

	teamCount := len(state.Teams)

	// number → team index, mirroring the live grid's number-keyed scoring.
	indexByNumber := make(map[int64]int, teamCount)
	for i, t := range state.Teams {
		if t.Number != 0 {
			indexByNumber[t.Number] = i
		}
	}
	// took[questionIndex][teamIndex]: a team "took" a completed question when its
	// number appears in that question's entries (questionStats in od.js).
	took := make([]map[int]bool, len(state.Entries))
	anyCompleted := false
	for q := range state.Entries {
		if q < len(state.Completed) && !state.Completed[q] {
			continue
		}
		anyCompleted = true
		set := make(map[int]bool)
		for _, num := range state.Entries[q] {
			if idx, ok := indexByNumber[num]; ok {
				set[idx] = true
			}
		}
		took[q] = set
	}

	teams := make([]ODResultsTeam, teamCount)
	totals := make([]int, teamCount)
	totalQuestions := 0
	for _, tourSize := range tours {
		totalQuestions += tourSize
	}
	for teamIdx, team := range state.Teams {
		tourTotals := make([]int, len(tours))
		total := 0
		rating := 0
		var mask strings.Builder
		mask.Grow(totalQuestions)
		qBase := 0
		for tourIdx, tourSize := range tours {
			for i := 0; i < tourSize; i++ {
				q := qBase + i
				if q < len(took) && took[q] != nil && took[q][teamIdx] {
					total++
					tourTotals[tourIdx]++
					rating += teamCount - len(took[q]) + 1
					mask.WriteByte('1')
				} else {
					mask.WriteByte('0')
				}
			}
			qBase += tourSize
		}
		totals[teamIdx] = total
		teams[teamIdx] = ODResultsTeam{
			Index:      teamIdx,
			Number:     team.Number,
			Name:       team.Name,
			City:       team.City,
			Total:      total,
			TourTotals: tourTotals,
			Rating:     rating,
			Mask:       mask.String(),
		}
	}

	// Rank by total desc, then roster index (stable), and assign tie-grouped place
	// labels, mirroring computePlaces in od.js. Without any completed question,
	// places stay blank.
	order := make([]int, teamCount)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return totals[order[a]] > totals[order[b]]
	})
	ranked := make([]ODResultsTeam, teamCount)
	for rank, teamIdx := range order {
		ranked[rank] = teams[teamIdx]
	}
	if anyCompleted {
		for i := 0; i < len(ranked); {
			j := i
			for j+1 < len(ranked) && ranked[j+1].Total == ranked[i].Total {
				j++
			}
			label := strconv.Itoa(i + 1)
			if j > i {
				label = fmt.Sprintf("%d–%d", i+1, j+1)
			}
			for k := i; k <= j; k++ {
				ranked[k].Place = label
			}
			i = j + 1
		}
	}

	return ODResults{TourComp: tours, Teams: ranked}, nil
}
