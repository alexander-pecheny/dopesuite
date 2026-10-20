package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"qy/qy/domain/anon"
	"qy/qy/storage/store"
)

// The background pass over new questions. A nomination is saved at once and
// the slow parts come after: the tournament's dates from rating.chgk.info and
// the language model's redactions. Each question is taken in turn, the network
// calls happen outside the write lock, and the results are written in one
// short transaction.

// passInterval is how often the pass looks for work it was not woken for.
const passInterval = time.Minute

func (s *server) runReviewPass(ctx context.Context) {
	ticker := time.NewTicker(passInterval)
	defer ticker.Stop()
	for {
		for {
			more, err := s.reviewOne(ctx)
			// sloplint: ignore[error-only-printed] nobody waits on the pass; the question stays pending and the next tick retries it
			if err != nil {
				log.Printf("review pass: %v", err)
			}
			if !more || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

// reviewOne handles the oldest pending question and reports whether there
// was one.
func (s *server) reviewOne(ctx context.Context) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `select id from questions where ai_status = ? order by id limit 1`, store.AIPending).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q, err := store.GetQuestion(ctx, s.db, id)
	if err != nil {
		return false, fmt.Errorf("question %d: %w", id, err)
	}
	// A tournament the rating site does not answer for keeps blank dates, which
	// the admin sees as unknown and checks by hand.
	_ = s.fillTournament(ctx, &q)
	var found []anon.Suggestion
	q.AIStatus, q.AIError = store.AIDone, ""
	if s.llm != nil && !q.Orig.Empty() {
		found, err = s.askModel(ctx, q)
		if err != nil {
			q.AIStatus, q.AIError = store.AIFailed, err.Error()
		}
	}
	err = s.withWriteTx(ctx, "review-pass", func(ctx context.Context, tx *sql.Tx) error {
		// Only the pass's own columns: an admin may have edited the question
		// while the model was thinking.
		if _, err := tx.ExecContext(ctx, `
update questions set tournament_title = ?, tournament_start = ?, tournament_end = ?, ai_status = ?, ai_error = ?
where id = ?`, q.TournamentTitle, q.TournamentStart, q.TournamentEnd, q.AIStatus, q.AIError, q.ID); err != nil {
			return err
		}
		return store.AddSuggestions(ctx, tx, q.ID, store.FromAI, found, s.stamp())
	})
	if err != nil {
		return false, fmt.Errorf("question %d: %w", id, err)
	}
	return true, nil
}

// fillTournament reads the tournament's name and dates once.
func (s *server) fillTournament(ctx context.Context, q *store.Question) error {
	if q.TournamentID == 0 || q.TournamentStart != "" {
		return nil
	}
	t, err := s.rating.Tournament(ctx, q.TournamentID)
	if err != nil {
		return err
	}
	if q.TournamentTitle == "" {
		q.TournamentTitle = t.Title
	}
	q.TournamentStart, q.TournamentEnd = t.Start, t.End
	return nil
}

func (s *server) askModel(ctx context.Context, q store.Question) ([]anon.Suggestion, error) {
	// The model reads the copy the jury will read, and the credits to look for.
	in := q.Clean
	in.Author = q.Orig.Author
	system, user := anon.Prompt(in, q.TournamentTitle, str.Anon.Placeholder())
	reply, err := s.llm.Complete(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return anon.ParseReply(reply, q.Clean)
}
