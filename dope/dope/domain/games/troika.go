package games

import (
	"encoding/json"
	"fmt"
)

// Troika pure domain logic.
//
// A match is head-to-head over themes of three questions each. Three players
// sit at a table — the anchor, who signals the team's readiness, and two
// outriders — and all three answer every question their team plays, in the
// order the quizmaster asks them. Each correct answer pays that question's
// nominal value on its own, so one question yields nought to three times its
// value.
//
// Who sat where belongs to the theme, not to the match: the regulations turn
// the outriders round at the halfway point, and teams swap oftener than that.
// The order is what tells a first correct answer from a repeat of one already
// on the table, which is the only distinction the statistics tab draws.
//
// The document is slot-ordered, as brain's is: a Protocol's Score answers per
// slot and never learns which Participant sits there, so Started guards a
// match with marks in it against a reseed that would shuffle the seats under
// them.

const (
	// TroikaChairs is the table: the anchor and two outriders.
	TroikaChairs = 3
	// TroikaThemeQuestions — "each played theme consists of three questions".
	TroikaThemeQuestions = 3
	// TroikaThemeCount is a match's themes when nothing says otherwise; the
	// regulations play 6 or 8.
	TroikaThemeCount = 6
	// TroikaThemeValue is a theme's nominal value by default — the
	// "one-point themes" every published Troika has played.
	TroikaThemeValue = 1
)

// TroikaTheme is one theme on one side. Order is the players' ids in the order
// the quizmaster asks them (chair 0 answers first); Answers is
// [question][chair] of "right", "wrong" or "" — nothing entered, which is
// what a question the other team took reads as.
type TroikaTheme struct {
	Order   []int64    `json:"order,omitempty"`
	Answers [][]string `json:"answers,omitempty"`
}

// TroikaSide is one team's half of the protocol.
type TroikaSide struct {
	Themes []TroikaTheme `json:"themes,omitempty"`
}

// TroikaState mirrors matches.state_json. Values is each theme's nominal
// value, written when the match is built: what a question was worth is a fact
// about the match that played it, not about the scheme as it stands today.
type TroikaState struct {
	Values []int        `json:"values,omitempty"`
	Sides  []TroikaSide `json:"sides,omitempty"`
}

// TroikaThemeValues resolves a match's per-theme nominals from its stage
// config: the authored list, padded with the default to the theme count, or
// all-default when the scheme is silent.
func TroikaThemeValues(themes int, authored []int) []int {
	if themes <= 0 {
		themes = TroikaThemeCount
	}
	values := make([]int, themes)
	for i := range values {
		if i < len(authored) && authored[i] > 0 {
			values[i] = authored[i]
		} else {
			values[i] = TroikaThemeValue
		}
	}
	return values
}

// TroikaEmptyStateJSON builds the pristine document for one match: two sides
// of themes, each a grid of three questions by three chairs, with the
// match's nominals recorded alongside.
func TroikaEmptyStateJSON(values []int) []byte {
	state := TroikaState{Values: values, Sides: make([]TroikaSide, 2)}
	for s := range state.Sides {
		themes := make([]TroikaTheme, len(values))
		for t := range themes {
			answers := make([][]string, TroikaThemeQuestions)
			for q := range answers {
				answers[q] = make([]string, TroikaChairs)
			}
			themes[t] = TroikaTheme{Order: make([]int64, TroikaChairs), Answers: answers}
		}
		state.Sides[s] = TroikaSide{Themes: themes}
	}
	return []byte(mustJSON(state))
}

// TroikaStateStarted reports whether a host has entered anything — a mark or
// a seated player. A started match is one a scheme recompile must not reseat.
func TroikaStateStarted(stateJSON string) bool {
	var state TroikaState
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return true // unreadable state is data, not pristine
	}
	for _, side := range state.Sides {
		for _, theme := range side.Themes {
			for _, player := range theme.Order {
				if player != 0 {
					return true
				}
			}
			for _, question := range theme.Answers {
				for _, mark := range question {
					if mark != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

// troikaValue is theme t's nominal, defaulting where the document is shorter
// than its themes (a match built before the scheme grew a theme).
func troikaValue(state TroikaState, theme int) int {
	if theme < len(state.Values) && state.Values[theme] > 0 {
		return state.Values[theme]
	}
	return TroikaThemeValue
}

// TroikaResultsSide is one side's computed outcome of a match.
type TroikaResultsSide struct {
	Total   int     `json:"total"`   // game points
	Correct int     `json:"correct"` // correct answers, not counting the nominal
	Place   float64 `json:"place"`   // 1 / 2, 1.5 shared on a tie
}

// ComputeTroikaResults scores a match from its state JSON, sides in slot
// order. Every correct answer pays its question's nominal on its own, so a
// question three players all took pays three times over.
func ComputeTroikaResults(stateJSON string) ([]TroikaResultsSide, error) {
	var state TroikaState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("parse troika state: %w", err)
		}
	}
	results := make([]TroikaResultsSide, len(state.Sides))
	for i, side := range state.Sides {
		for t, theme := range side.Themes {
			value := troikaValue(state, t)
			for _, question := range theme.Answers {
				for _, mark := range question {
					if mark == "right" {
						results[i].Total += value
						results[i].Correct++
					}
				}
			}
		}
	}
	if len(results) == 2 {
		a, b := &results[0], &results[1]
		switch {
		case a.Total > b.Total:
			a.Place, b.Place = 1, 2
		case a.Total < b.Total:
			a.Place, b.Place = 2, 1
		default:
			a.Place, b.Place = 1.5, 1.5
		}
	}
	return results, nil
}
