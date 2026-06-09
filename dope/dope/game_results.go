package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// game_results.go exposes a computed "results" view for a game: the same total
// and per-tour standings the #results page renders client-side, but computed
// server-side so callers (bots, exports, integrations) don't have to replicate
// the scoring. Currently only OD games are supported; the scoring mirrors the
// helpers in static/od.js (sumRow, tourSumsForTeam, ratingForTeam,
// computePlaces) and reuses the per-question "took" matrix already built for the
// xlsx export.

type odResultsTeam struct {
	Place      string `json:"place"`      // tie-grouped label ("1", "2–4"); "" before any question is completed
	Index      int    `json:"index"`      // roster index in state.teams
	Number     int64  `json:"number"`     // printed team number (0 if unnumbered)
	Name       string `json:"name"`       //
	City       string `json:"city"`       //
	Total      int    `json:"total"`      // questions taken across all tours (Σ on the page)
	TourTotals []int  `json:"tourTotals"` // questions taken per tour, aligned with tourComp
	Rating     int    `json:"rating"`     // Buchholz-style R: sum over taken questions of (teamCount − takers)
	Mask       string `json:"mask"`       // per-question "1"/"0" (took/not), rating.chgk.info style, length = sum(tourComp)
}

type odResults struct {
	TourComp []int           `json:"tourComp"`
	Teams    []odResultsTeam `json:"teams"` // ranked: total desc, then roster index
}

func (s *server) handleScopedGameResults(w http.ResponseWriter, r *http.Request, scope festScope) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeFestRead(w, r, scope.FestID) {
		return
	}
	// Read seq before the row (same ordering rationale as handleScopedGameState)
	// so the X-State-Seq we report is never ahead of the state we scored.
	seq := s.currentStateSeq(fmt.Sprintf("game-state:%d", scope.GameID))
	var gameType, schemeJSON, stateJSON string
	err := s.db.QueryRowContext(r.Context(), `
select game_type, scheme_json, state_json from games where fest_id = ? and id = ?`,
		scope.FestID, scope.GameID).Scan(&gameType, &schemeJSON, &stateJSON)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if gameType != "od" {
		http.Error(w, fmt.Sprintf("results view not available for game type %q", gameType), http.StatusBadRequest)
		return
	}
	results, err := computeODResults(schemeJSON, stateJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(results)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-State-Seq", strconv.FormatUint(seq, 10))
	w.Header().Set("X-State-Epoch", s.epoch)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// computeODResults scores an OD game from its scheme and state JSON, mirroring
// the client-side helpers in static/od.js.
func computeODResults(schemeJSON, stateJSON string) (odResults, error) {
	tours := parseTourComp(schemeJSON)
	var state odExportState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return odResults{}, fmt.Errorf("parse OD state: %w", err)
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

	teams := make([]odResultsTeam, teamCount)
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
					rating += teamCount - len(took[q])
					mask.WriteByte('1')
				} else {
					mask.WriteByte('0')
				}
			}
			qBase += tourSize
		}
		totals[teamIdx] = total
		teams[teamIdx] = odResultsTeam{
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
	ranked := make([]odResultsTeam, teamCount)
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

	return odResults{TourComp: tours, Teams: ranked}, nil
}
