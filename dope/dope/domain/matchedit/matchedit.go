// Package matchedit validates a single EK match-edit request. The rules — team
// bounds, action shape, and place/theme/answer/mark checks — are pure, so they
// are unit-testable without a booted server. Applying a validated Plan is SQL
// against a transaction and stays in the server (the architecture keeps
// tx-bound logic there; domain holds the pure rules). This mirrors
// domain/resolver, where the server owns only the tx wrapper.
package matchedit

import (
	"errors"
	"strings"
)

// Action values a standalone edit may carry (mirrors the server's constants).
const (
	ActionAddShootoutTheme    = "addShootoutTheme"
	ActionRemoveShootoutTheme = "removeShootoutTheme"
)

// Request is the subset of a (non-batched) match-edit PATCH this validation
// reads. HasTeamEdit reports whether the request also carries any team-scoped
// field, which an action must not.
type Request struct {
	Action      string
	HasTeamEdit bool
	Team        int
	Tiebreak    *int
	Place       *float64
	Theme       *int
	Shootout    *bool
	Answer      *int
	Mark        *string
	Player      *string
}

// Plan is the validated set of operations to apply to the match.
type Plan struct {
	// Action is "", ActionAddShootoutTheme or ActionRemoveShootoutTheme.
	Action string
	// Place, when non-nil, upserts a place for the team.
	Place *float64
	// Theme, when non-nil, is a theme-scoped edit.
	Theme *ThemeEdit
}

// ThemeEdit is an edit to one theme cell: set its player and/or upsert an answer.
type ThemeEdit struct {
	Kind  string // "regular" | "shootout"
	Index int
	// Player, when non-nil, sets the theme's player (trimmed; "" clears it).
	Player *string
	// Answer, when non-nil, upserts an answer mark on the theme.
	Answer *AnswerEdit
}

// AnswerEdit upserts a mark on an answer cell. Mark is carried verbatim; the
// server normalizes it at insert.
type AnswerEdit struct {
	Index int
	Mark  string
}

// Validate checks req against a match with numTeams slots (teamPresent reports
// whether slot i holds a real team) and returns the operations to apply, or an
// error whose message the API surfaces verbatim. answerValueCount is the number
// of scorable answer columns (len(store.QuestionValues)).
func Validate(numTeams int, teamPresent func(i int) bool, answerValueCount int, req Request) (Plan, error) {
	if req.Action != "" {
		if req.HasTeamEdit {
			return Plan{}, errors.New("action update must be standalone")
		}
		switch req.Action {
		case ActionAddShootoutTheme, ActionRemoveShootoutTheme:
			return Plan{Action: req.Action}, nil
		default:
			return Plan{}, errors.New("bad action")
		}
	}

	if req.Team < 0 || req.Team >= numTeams || !teamPresent(req.Team) {
		return Plan{}, errors.New("bad team index")
	}

	if req.Tiebreak != nil {
		return Plan{}, errors.New("shootout total is calculated")
	}

	var plan Plan
	if req.Place != nil {
		if *req.Place < 0 {
			return Plan{}, errors.New("bad place")
		}
		plan.Place = req.Place
	}

	if req.Theme != nil || req.Player != nil || req.Answer != nil || req.Mark != nil || req.Shootout != nil {
		kind := "regular"
		if req.Shootout != nil && *req.Shootout {
			kind = "shootout"
		}
		if req.Theme == nil || *req.Theme < 0 {
			return Plan{}, errors.New("bad theme index")
		}
		te := ThemeEdit{Kind: kind, Index: *req.Theme}
		if req.Player != nil {
			p := strings.TrimSpace(*req.Player)
			te.Player = &p
		}
		if req.Answer != nil || req.Mark != nil {
			if req.Answer == nil || *req.Answer < 0 || *req.Answer >= answerValueCount {
				return Plan{}, errors.New("bad answer index")
			}
			if req.Mark == nil {
				return Plan{}, errors.New("missing mark")
			}
			te.Answer = &AnswerEdit{Index: *req.Answer, Mark: *req.Mark}
		}
		plan.Theme = &te
	}

	return plan, nil
}
