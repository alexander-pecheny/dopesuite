package games

import (
	"encoding/json"
	"fmt"
)

// Troika (Троечка) pure domain logic.
//
// A бой is head-to-head over темы of three вопросы each. Three players sit at
// a table — the коренной, who signals the team's readiness, and two
// пристяжные — and all three answer every вопрос their team plays, in the
// order the ведущий asks them. Each correct answer pays that вопрос's
// нарицательная стоимость on its own, so one вопрос yields nought to three
// times its value.
//
// Who sat where belongs to the тема, not to the бой: the регламент turns the
// пристяжные round at the половина, and teams swap oftener than that. The
// order is what tells a first correct answer from a repeat of one already on
// the table, which is the only distinction the статистика tab draws.
//
// The document is slot-ordered, as брейн's is: a Protocol's Score answers per
// slot and never learns which Participant sits there, so Started guards a бой
// with marks in it against a пересев that would shuffle the seats under them.

const (
	// TroikaChairs is the table: the коренной and two пристяжные.
	TroikaChairs = 3
	// TroikaThemeQuestions — «каждая разыгрываемая тема состоит из трёх вопросов».
	TroikaThemeQuestions = 3
	// TroikaThemeCount is a бой's themes when nothing says otherwise; the
	// регламент plays 6 or 8.
	TroikaThemeCount = 6
	// TroikaThemeValue is a тема's нарицательная стоимость by default — the
	// «темы за 1 балл» every published Троечка has played.
	TroikaThemeValue = 1
)

// TroikaTheme is one тема on one side. Order is the players' ids in the order
// the ведущий asks them (chair 0 answers first); Answers is
// [вопрос][кресло] of "right", "wrong" or "" — nothing entered, which is what
// a вопрос the other team took reads as.
type TroikaTheme struct {
	Order   []int64    `json:"order,omitempty"`
	Answers [][]string `json:"answers,omitempty"`
}

// TroikaSide is one team's half of the протокол.
type TroikaSide struct {
	Themes []TroikaTheme `json:"themes,omitempty"`
}

// TroikaState mirrors matches.state_json. Values is each тема's нарицательная
// стоимость, written when the бой is built: what a вопрос was worth is a fact
// about the бой that played it, not about the scheme as it stands today.
type TroikaState struct {
	Values []int        `json:"values,omitempty"`
	Sides  []TroikaSide `json:"sides,omitempty"`
}

// TroikaThemeValues resolves a бой's per-тема нарицательные from its stage
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

// TroikaEmptyStateJSON builds the pristine document for one бой: two sides of
// themes темы, each a grid of three вопросы by three кресла, with the бой's
// нарицательные recorded alongside.
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

// TroikaStateStarted reports whether a host has entered anything — a mark or a
// seated player. A started бой is one a scheme recompile must not reseat.
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

// troikaValue is тема t's нарицательная, defaulting where the document is
// shorter than its themes (a бой built before the scheme grew a тема).
func troikaValue(state TroikaState, theme int) int {
	if theme < len(state.Values) && state.Values[theme] > 0 {
		return state.Values[theme]
	}
	return TroikaThemeValue
}

// TroikaResultsSide is one side's computed outcome of a бой.
type TroikaResultsSide struct {
	Total   int     `json:"total"`   // игровые очки
	Correct int     `json:"correct"` // правильные ответы, без учёта номинала
	Place   float64 `json:"place"`   // 1 / 2, 1.5 shared on a ничья
}

// ComputeTroikaResults scores a бой from its state JSON, sides in slot order.
// Every correct answer pays its вопрос's нарицательная on its own, so a вопрос
// three players all took pays three times over.
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
