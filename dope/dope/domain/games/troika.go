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

// TroikaSide is one team's part of the protocol. A written bout keeps Counts
// instead of Themes: per theme and question, how many of the troika's answers
// were right, 0 to 3.
type TroikaSide struct {
	Themes []TroikaTheme `json:"themes,omitempty"`
	Counts [][]int       `json:"counts,omitempty"`
}

// TroikaState mirrors matches.state_json. Values is each theme's nominal
// value, written when the match is built: what a question was worth is a fact
// about the match that played it, not about the scheme as it stands today.
//
// Shootout is how many of the last themes are shootout themes, added by the
// host to a bout whose sides are level (regulations IV.2.4). They count into the
// total like any other theme. Pin is a place per side that a host set by hand;
// a zero leaves that side's place to the sheet. Written marks the qualifier, which
// is one sitting of every troika on paper: its sides carry Counts.
type TroikaState struct {
	Values   []int        `json:"values,omitempty"`
	Sides    []TroikaSide `json:"sides,omitempty"`
	Shootout int          `json:"shootout,omitempty"`
	Pin      []float64    `json:"pin,omitempty"`
	Written  bool         `json:"written,omitempty"`
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

// TroikaEmptyStateJSON builds the pristine document for one match: a side per
// seat (two at the least), each of themes of three questions by three chairs,
// with the match's nominals recorded alongside. A written match has a grid of
// counts per side instead.
func TroikaEmptyStateJSON(values []int, seats int, written bool) []byte {
	if seats < 2 {
		seats = 2
	}
	state := TroikaState{Values: values, Sides: make([]TroikaSide, seats), Written: written}
	for s := range state.Sides {
		if written {
			counts := make([][]int, len(values))
			for t := range counts {
				counts[t] = make([]int, TroikaThemeQuestions)
			}
			state.Sides[s] = TroikaSide{Counts: counts}
			continue
		}
		themes := make([]TroikaTheme, len(values))
		for t := range themes {
			themes[t] = emptyTroikaTheme()
		}
		state.Sides[s] = TroikaSide{Themes: themes}
	}
	return []byte(mustJSON(state))
}

func emptyTroikaTheme() TroikaTheme {
	answers := make([][]string, TroikaThemeQuestions)
	for q := range answers {
		answers[q] = make([]string, TroikaChairs)
	}
	return TroikaTheme{Order: make([]int64, TroikaChairs), Answers: answers}
}

// TroikaStateStarted reports whether a host has entered anything — a mark,
// a count or a seated player. A started match is one a scheme recompile must
// not reseat.
func TroikaStateStarted(stateJSON string) bool {
	var state TroikaState
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return true // unreadable state is data, not pristine
	}
	for _, pin := range state.Pin {
		if pin != 0 {
			return true
		}
	}
	for _, side := range state.Sides {
		for _, theme := range side.Counts {
			for _, count := range theme {
				if count != 0 {
					return true
				}
			}
		}
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
	Place   float64 `json:"place"`   // 1 / 2 / …, the mean of the places a tie shares
	// Threes and Twos are the questions of a written bout a troika answered
	// three and two times right — the qualifier's tiebreak (regulations IV.2.3).
	Threes int `json:"threes"`
	Twos   int `json:"twos"`
}

// ComputeTroikaResults scores a match from its state JSON, sides in slot
// order. Every correct answer pays its question's nominal on its own, so a
// question three players all took pays three times over. Sides rank by total
// and share the mean place when level; a pinned place wins over the sheet.
func ComputeTroikaResults(stateJSON string) ([]TroikaResultsSide, error) {
	var state TroikaState
	if stateJSON != "" {
		if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
			return nil, fmt.Errorf("parse troika state: %w", err)
		}
	}
	results := make([]TroikaResultsSide, len(state.Sides))
	for i, side := range state.Sides {
		for t, theme := range side.Counts {
			value := troikaValue(state, t)
			for _, count := range theme {
				if count < 0 {
					count = 0
				}
				if count > TroikaChairs {
					count = TroikaChairs
				}
				results[i].Total += count * value
				results[i].Correct += count
				switch count {
				case 3:
					results[i].Threes++
				case 2:
					results[i].Twos++
				}
			}
		}
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
	for i := range results {
		above, level := 0, 0
		for j := range results {
			switch {
			case results[j].Total > results[i].Total:
				above++
			case results[j].Total == results[i].Total:
				level++
			}
		}
		// Places above+1 … above+level, shared: their mean.
		results[i].Place = float64(above) + float64(level+1)/2
		if i < len(state.Pin) && state.Pin[i] > 0 {
			results[i].Place = state.Pin[i]
		}
	}
	return results, nil
}
