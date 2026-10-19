// Package gq is gotquestions.online, the database most published questions
// live in. Its API is down as of October 2026, so the only Source here is
// Unavailable: a nomination by link waits for its text from the nominator or
// an admin. A client that fetches goes behind the same interface.
package gq

import (
	"context"
	"errors"

	"qy/qy/domain/question"
)

// Found is a question as gotquestions has it.
type Found struct {
	Question     question.Question
	TournamentID int64
	Tournament   string
}

type Source interface {
	Question(ctx context.Context, id int64) (Found, error)
}

var ErrUnavailable = errors.New("gotquestions is unavailable")

// Unavailable answers every lookup with ErrUnavailable.
type Unavailable struct{}

func (Unavailable) Question(context.Context, int64) (Found, error) { return Found{}, ErrUnavailable }

// Fixed answers from a map, for tests and for a dev checkout.
type Fixed map[int64]Found

func (f Fixed) Question(_ context.Context, id int64) (Found, error) {
	if q, ok := f[id]; ok {
		return q, nil
	}
	return Found{}, ErrUnavailable
}
