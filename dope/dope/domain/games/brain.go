package games

import (
	"encoding/json"
	"fmt"
)

// Brain (брейн-ринг) pure domain logic.
//
// A brain match is a head-to-head бой between two teams over K buzzer
// questions: per question each side records at most one answering player and a
// mark ("right" — took it, "wrong" — missed, "" — untouched). Extra tiebreak
// questions (the sheet's "П" rows) are appended after the base K when a draw
// must be broken. The shapes below mirror matches.state_json; who the sides
// are is the Structure's knowledge (match_slots), and finishedness is
// matches.status — the state holds play data only.

// BrainRow is one team's cell pair for one question: who buzzed and how it went.
type BrainRow struct {
	Player string `json:"player"`
	Mark   string `json:"mark"` // "right" | "wrong" | ""
}

// BrainSide is one side of a бой: one row per question, index-aligned with the
// other side (base questions first, then tiebreaks).
type BrainSide struct {
	Rows []BrainRow `json:"rows"`
}

// BrainState mirrors the persisted brain match state JSON. The last Tiebreaks
// rows are the "П" questions.
type BrainState struct {
	Tiebreaks int         `json:"tiebreaks"`
	Teams     []BrainSide `json:"teams"`
}

// BrainQuestionCount is the default number of base questions in a бой.
const BrainQuestionCount = 5

// BrainEmptyStateJSON builds the pristine state blob for one бой of the given
// base question count.
func BrainEmptyStateJSON(questions int) []byte {
	if questions <= 0 {
		questions = BrainQuestionCount
	}
	teams := make([]BrainSide, 2)
	for i := range teams {
		teams[i] = BrainSide{Rows: make([]BrainRow, questions)}
	}
	return []byte(mustJSON(BrainState{Teams: teams}))
}

// BrainStateStarted reports whether a бой has any entered protocol data — a
// mark, a player attribution or appended tiebreak rows. Started бои are the
// ones a scheme recompile must not touch.
func BrainStateStarted(stateJSON string) bool {
	var state BrainState
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return true // unreadable state is data, not pristine
	}
	if state.Tiebreaks > 0 {
		return true
	}
	for _, side := range state.Teams {
		for _, row := range side.Rows {
			if row.Mark != "" || row.Player != "" {
				return true
			}
		}
	}
	return false
}

// BrainQuestions reads scheme.questions, falling back to the default count.
func BrainQuestions(schemeJSON string) int {
	var probe struct {
		Questions int `json:"questions"`
	}
	if schemeJSON != "" {
		_ = json.Unmarshal([]byte(schemeJSON), &probe)
	}
	if probe.Questions <= 0 {
		return BrainQuestionCount
	}
	return probe.Questions
}

// BrainTaken counts the questions a side took; withTiebreaks includes the
// trailing "П" rows.
func BrainTaken(state BrainState, side int, withTiebreaks bool) int {
	if side >= len(state.Teams) {
		return 0
	}
	rows := state.Teams[side].Rows
	if !withTiebreaks && state.Tiebreaks > 0 && state.Tiebreaks < len(rows) {
		rows = rows[:len(rows)-state.Tiebreaks]
	}
	taken := 0
	for _, row := range rows {
		if row.Mark == "right" {
			taken++
		}
	}
	return taken
}

// BrainResultsTeam is one side's computed outcome of a бой.
type BrainResultsTeam struct {
	Taken     int     `json:"taken"`     // questions taken, tiebreaks included
	TakenBase int     `json:"takenBase"` // without tiebreaks — the reshuffle stat
	Place     float64 `json:"place"`     // 1 / 2, 1.5 shared on a draw
}

// ComputeBrainResults scores a бой from its state JSON, sides in slot order.
// Places track the running score unconditionally; whether they count (group
// points, advancement) is the Structure's call, gated on matches.status.
func ComputeBrainResults(stateJSON string) ([]BrainResultsTeam, error) {
	var state BrainState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("parse brain state: %w", err)
		}
	}
	results := make([]BrainResultsTeam, len(state.Teams))
	for i := range state.Teams {
		results[i] = BrainResultsTeam{
			Taken:     BrainTaken(state, i, true),
			TakenBase: BrainTaken(state, i, false),
		}
	}
	if len(results) == 2 {
		a, b := &results[0], &results[1]
		switch {
		case a.Taken > b.Taken:
			a.Place, b.Place = 1, 2
		case a.Taken < b.Taken:
			a.Place, b.Place = 2, 1
		default:
			a.Place, b.Place = 1.5, 1.5
		}
	}
	return results, nil
}
