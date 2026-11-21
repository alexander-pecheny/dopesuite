package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
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

// fileItems are the question page's attached files, each with its remove
// button, and the form that attaches one more: a handout a nominator sent
// some other way than the form.
func (s *server) fileItems(ctx context.Context, href, keep string, id int64) ([]ui.Item, error) {
	files, err := store.Files(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	var items []ui.Item
	for _, f := range files {
		items = append(items, ui.Row(ui.SpaceSM, ui.AlignCenter,
			ui.Button(ui.Href(fileHref(f.ID)), ui.Ghost, ui.Small(), ui.IconPaperclip, text(f.Name)),
			ui.Form(ui.Method("post"), ui.Action(href+"/files/"+idstr.Format(f.ID)+"/remove"+keep),
				ui.Data("confirm", str.Admin.Question.FileRemoveConfirm(f.Name)),
				ui.Button(ui.Submit(), ui.Ghost, ui.Small(), text(str.Admin.Question.FileRemove())))))
	}
	items = append(items, ui.Form(ui.Method("post"), ui.Action(href+"/files"+keep), ui.Enctype("multipart/form-data"),
		ui.Row(ui.SpaceSM, ui.AlignEnd, ui.Wrap(),
			ui.Field(ui.Label(str.Admin.Question.FileAdd()), ui.Filefield(ui.Name("file"), ui.Required())),
			ui.Button(ui.Submit(), ui.Small(), text(str.Admin.Question.FileAttach())))))
	return items, nil
}

func (s *server) handleAddFile(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxHandoutFile+formMemory)
	if err := r.ParseMultipartForm(formMemory); err != nil {
		return userError(str.Nominate.Error.FileTooBig())
	}
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	f, tooBig := readUpload(r, "file")
	if tooBig {
		return userError(str.Nominate.Error.FileTooBig())
	}
	if f == nil {
		return userError(str.Admin.Question.FileMissing())
	}
	if err := s.withWriteTx(r.Context(), "add-file", func(ctx context.Context, tx *sql.Tx) error {
		return store.AddFile(ctx, tx, store.File{QuestionID: q.ID, Name: f.name, ContentType: f.contentType}, f.data, s.stamp())
	}); err != nil {
		return err
	}
	return seeOther(w, r, questionBack(r, q.ID, doneSaved))
}

func (s *server) handleRemoveFile(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	fid, err := idstr.Parse(r.PathValue("fid"))
	if err != nil {
		return route.NotFound(str.Cms.NotFound())
	}
	if err := s.withWriteTx(r.Context(), "remove-file", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from question_files where id = ? and question_id = ?`, fid, q.ID)
		return err
	}); err != nil {
		return err
	}
	return seeOther(w, r, questionBack(r, q.ID, doneSaved))
}
