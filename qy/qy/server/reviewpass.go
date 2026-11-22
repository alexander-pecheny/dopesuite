package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"qy/qy/domain/anon"
	"qy/qy/domain/lang"
	"qy/qy/domain/question"
	"qy/qy/domain/translate"
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
		for _, step := range s.passSteps() {
			for {
				more, err := step(ctx)
				// sloplint: ignore[error-only-printed] nobody waits on the pass; the question stays pending and the next tick retries it
				if err != nil {
					log.Printf("review pass: %v", err)
				}
				if !more || err != nil {
					break
				}
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

// passSteps are the pass's parts in order, each called until it reports no
// more work: the redactions and the handout's pictures first, since the
// translation is made from the copy they produce.
func (s *server) passSteps() []func(context.Context) (bool, error) {
	return []func(context.Context) (bool, error){s.reviewOne, s.keepPictureOne, s.detectLangs, s.translateOne}
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
		if err := store.AddSuggestions(ctx, tx, q.ID, store.FromAI, found, s.stamp()); err != nil {
			return err
		}
		return s.autoMerge(ctx, tx, q.ID)
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
	system, user := anon.Prompt(in, q.TournamentTitle, str.Anon.Placeholder(), anon.Rules(in, str.Anon.Placeholder()))
	reply, err := s.llm.Complete(ctx, system, user)
	if err != nil {
		return nil, err
	}
	return anon.ParseReply(reply, q.Clean)
}

// langBatch is how many questions one run of the language detector takes:
// the binary loads its models anew each run.
const langBatch = 50

// detectLangs names the language of the questions that have none yet: new
// ones, and ones re-parsed since. It reads the text and the answer, the
// parts in the question's own words.
func (s *server) detectLangs(ctx context.Context) (bool, error) {
	if s.langs == nil {
		return false, nil
	}
	texts := map[int64]string{}
	_, err := store.Collect(ctx, s.db, func(r *sql.Rows) (struct{}, error) {
		var id int64
		var orig string
		err := r.Scan(&id, &orig)
		q := question.FromJSON(orig)
		texts[id] = q.Text + "\n" + q.Answer
		return struct{}{}, err
	}, `select id, orig from questions where lang is null order by id limit ?`, langBatch)
	if err != nil || len(texts) == 0 {
		return false, err
	}
	langs, err := s.langs.Detect(ctx, texts)
	if err != nil {
		return false, err
	}
	err = s.withWriteTx(ctx, "detect-lang", func(ctx context.Context, tx *sql.Tx) error {
		for id := range texts {
			l := langs[id]
			if !lang.Valid(l) {
				l = lang.Russian
			}
			// Only a question still undetected: an admin may have chosen meanwhile.
			if _, err := tx.ExecContext(ctx, `update questions set lang = ? where id = ? and lang is null`, l, id); err != nil {
				return err
			}
		}
		return nil
	})
	return len(texts) == langBatch && err == nil, err
}

// translateOne translates the oldest question that needs it: one not in
// Russian whose translation is missing or was made from an older clean copy,
// and that no admin has edited. It waits for the model's redactions, since
// the translation is made from the clean copy. A failure stops the pass on
// that question until an admin asks again, as a failed redaction pass does.
func (s *server) translateOne(ctx context.Context) (bool, error) {
	if s.llm == nil {
		return false, nil
	}
	var id int64
	var l, cleanJSON string
	err := s.db.QueryRowContext(ctx, `
select id, lang, clean from questions
where lang is not null and lang != ? and merged_into is null and ai_status != ?
  and translation_edited = 0 and translation_of != clean and translation_error = ''
order by id limit 1`, lang.Russian, store.AIPending).Scan(&id, &l, &cleanJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	clean := question.FromJSON(cleanJSON)
	var tr question.Question
	var failure string
	if !clean.Empty() {
		system, user := translate.Prompt(clean, inLanguage(l))
		reply, err := s.llm.Complete(ctx, system, user)
		if err == nil {
			tr, err = translate.ParseReply(reply, clean)
		}
		if err != nil {
			failure = err.Error()
		}
	}
	err = s.withWriteTx(ctx, "translate", func(ctx context.Context, tx *sql.Tx) error {
		// Written only over what it was made from: an admin may have edited the
		// clean copy or the translation while the model was thinking.
		if failure != "" {
			_, err := tx.ExecContext(ctx, `
update questions set translation_error = ? where id = ? and clean = ? and translation_edited = 0`,
				failure, id, cleanJSON)
			return err
		}
		_, err := tx.ExecContext(ctx, `
update questions set translation = ?, translation_of = clean, translation_error = '' where id = ? and clean = ? and translation_edited = 0`,
			tr.JSON(), id, cleanJSON)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("question %d: %w", id, err)
	}
	return true, nil
}

// inLanguage names a language for the translation prompt.
func inLanguage(l string) string {
	if l == lang.Belarusian {
		return str.Translate.InLanguage.Be()
	}
	return str.Translate.InLanguage.Uk()
}

// autoMerge merges a question with another of its year that has the same
// text (question.Same): one question nominated twice, once by a link and once
// pasted, say. The copy with a gotquestions link stays, else the fuller one,
// else the older; the other is merged into it. A question an admin has
// already approved, rejected or numbered is left for the admins to merge.
func (s *server) autoMerge(ctx context.Context, tx *sql.Tx, id int64) error {
	q, err := store.GetQuestion(ctx, tx, id)
	if err != nil || q.MergedInto != 0 || q.Orig.Empty() {
		return err
	}
	others, err := store.Questions(ctx, tx, int(q.Year), "id != ? and merged_into is null", q.ID)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.Orig.Empty() || !question.Same(q.Orig, o.Orig) {
			continue
		}
		keep, drop := q, o
		if better(o, q) {
			keep, drop = o, q
		}
		if drop.Status != store.StatusNew || drop.QY != 0 {
			continue
		}
		log.Printf("review pass: question %d is question %d, merged", drop.ID, keep.ID)
		return s.mergeInto(ctx, tx, drop, keep.ID)
	}
	return nil
}

// better says which of two copies of one question to keep.
func better(a, b store.Question) bool {
	if (a.GQID != 0) != (b.GQID != 0) {
		return a.GQID != 0
	}
	if fa, fb := filled(a.Orig), filled(b.Orig); fa != fb {
		return fa > fb
	}
	return a.ID < b.ID
}

func filled(q question.Question) int {
	n := 0
	for _, f := range question.Fields {
		if q.Get(f) != "" {
			n++
		}
	}
	return n
}
