package qyserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/storage/store"
	"qy/qy/web/route"
)

// handleNominationFile serves a file attached to a question to whoever may
// see the question's card. Its link may end in an extension, which the card's
// script reads a picture by (questionview.go); the id is what counts.
func (s *server) handleNominationFile(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	raw, _, _ := strings.Cut(r.PathValue("id"), ".")
	id, err := idstr.Parse(raw)
	if err != nil {
		return route.NotFound(str.Cms.NotFound())
	}
	f, data, err := store.FileData(r.Context(), s.db, id)
	if errors.Is(err, store.ErrNoFile) {
		return route.NotFound(str.Cms.NotFound())
	}
	if err != nil {
		return err
	}
	ok, err := s.maySeeQuestion(r.Context(), sc, f.QuestionID)
	if err != nil {
		return err
	}
	if !ok {
		return route.NotFound(str.Cms.NotFound())
	}
	serveFile(w, f, data)
	return nil
}

// maySeeQuestion says whether the caller is shown the question somewhere: an
// admin always, a nominator their own, a juror an approved one their jury
// reads, and anyone one on a shortlist or results page made public.
func (s *server) maySeeQuestion(ctx context.Context, sc route.Scope, id int64) (bool, error) {
	if sc.IsAdmin {
		return true, nil
	}
	if sc.HasUser {
		var n int
		err := s.db.QueryRowContext(ctx, `select count(*) from nominations where question_id = ? and user_id = ?`, id, sc.User.UserID).Scan(&n)
		if err != nil || n > 0 {
			return n > 0, err
		}
	}
	q, err := store.GetQuestion(ctx, s.db, id)
	if err != nil || q.Status != store.StatusApproved {
		return false, ignoreMissing(err)
	}
	if sc.HasUser {
		kind, err := store.JurorKind(ctx, s.db, int(q.Year), sc.User.UserID)
		if err != nil {
			return false, err
		}
		if kind != "" && (kind != store.People || q.Published) {
			return true, nil
		}
	}
	c, err := store.Contest(ctx, s.db, int(q.Year))
	if err != nil || !s.shortlistVisible(c) && c.ResultsPublishedAt == "" {
		return false, err
	}
	ids, err := shortlistIDs(ctx, s.db, c)
	return slices.Contains(ids, id), err
}

func ignoreMissing(err error) error {
	if errors.Is(err, store.ErrNoQuestion) {
		return nil
	}
	return err
}
