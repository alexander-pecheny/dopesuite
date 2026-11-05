package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/anon"
	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/platform/telegram"
	"qy/qy/storage/store"
	"qy/qy/web/route"
)

// The nomination form: up to MainSlots questions plus ReserveSlots reserves.
// A question published on gotquestions is just its link, and the question is
// read from the dump. Any other question, published elsewhere (a telegram
// post) or not at all, is its links and its fields, which the nominator
// pastes as one text, has parsed and then checks; it may carry a handout
// file. A slot can be
// changed until the stage ends; what the form shows is what the database
// holds. A form with a mistake in any slot is shown again unsaved.

func (s *server) ratingID(ctx context.Context, userID int64) (string, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `select rating_id from users where id = ?`, userID).Scan(&id)
	if err != nil || !id.Valid {
		return "", err
	}
	return idstr.Format(id.Int64), nil
}

func (s *server) currentContest(ctx context.Context) (contest.Contest, error) {
	c, err := store.Current(ctx, s.db)
	if errors.Is(err, store.ErrNoContest) {
		return c, route.NotFound(str.Contest.None())
	}
	return c, err
}

// nominationOpen says whether sc may use the form today. Admins may use it
// outside the stage, to try it before it opens.
func (s *server) nominationOpen(c contest.Contest, sc route.Scope) bool {
	return c.StageAt(s.now()) == contest.Nomination || sc.IsAdmin
}

// nominationContest is the contest of the form's year, which must be the
// current one: an earlier year's form is gone with its stage.
func (s *server) nominationContest(r *http.Request) (contest.Contest, error) {
	c, err := s.currentContest(r.Context())
	if err != nil {
		return c, err
	}
	if yearOf(strings.TrimPrefix(r.URL.Path, "/")) != c.Year {
		return c, route.NotFound(str.Cms.NotFound())
	}
	return c, nil
}

func nominatePath(year int) string { return "/" + strconv.Itoa(year) + "/nominate" }

// isNominatePath says whether p is some year's form, /{year}/nominate.
func isNominatePath(p string) bool {
	y := yearOf(strings.TrimPrefix(p, "/"))
	return y != 0 && p == nominatePath(y)
}

// handleOldNominate sends the form's first address, /nominate, on to the
// current year's.
func (s *server) handleOldNominate(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.currentContest(r.Context())
	if err != nil {
		return err
	}
	http.Redirect(w, r, nominatePath(c.Year), http.StatusMovedPermanently)
	return nil
}

func (s *server) handleNominatePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	c, err := s.nominationContest(r)
	if err != nil {
		return err
	}
	if !s.nominationOpen(c, sc) {
		return s.ok(w, r, closedNominationView(c))
	}
	slots, err := s.nominationSlots(r.Context(), c.Year, sc.User.UserID)
	if err != nil {
		return err
	}
	rating, err := s.ratingID(r.Context(), sc.User.UserID)
	if err != nil {
		return err
	}
	if err := s.lintSlots(r.Context(), c, slots); err != nil {
		return err
	}
	return s.ok(w, r, s.nominationView(r, c, sc, nominationForm{rating: rating, slots: slots}))
}

// parseRatingID reads the rating id field: digits only, and required.
func parseRatingID(raw string) (int64, error) {
	id, err := idstr.Parse(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		return 0, userError(str.Common.RatingIdInvalid())
	}
	return id, nil
}

func (s *server) handleNominate(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseNominationForm(w, r); err != nil {
		return err
	}
	c, err := s.nominationContest(r)
	if err != nil {
		return err
	}
	if !s.nominationOpen(c, sc) {
		return userError(str.Nominate.Page.Closed(dayText(c.NominationStart), dayText(c.NominationEnd)))
	}
	before, err := s.nominationSlots(r.Context(), c.Year, sc.User.UserID)
	if err != nil {
		return err
	}
	form := readNominationForm(r, c, before)
	for _, slot := range form.loads {
		v := form.slots[slot]
		s.loadSlot(r.Context(), &v)
		form.slots[slot] = v
	}
	form.check()
	if err := s.lintSlots(r.Context(), c, form.slots); err != nil {
		return err
	}
	rating, ratingErr := parseRatingID(form.rating)
	if form.parseOnly || form.hasErrors() || ratingErr != nil {
		if ratingErr != nil && !form.parseOnly {
			form.ratingErr = ratingErr
		}
		return s.render(w, r, unsavedStatus(form), s.nominationView(r, c, sc, form))
	}
	changes := s.slotChanges(r.Context(), c, before, form.slots)
	if err := s.saveNomination(r.Context(), c.Year, sc.User.UserID, rating, changes, form.slots); err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, nominatePath(c.Year)+"?done="+doneSaved)
}

func (s *server) saveNomination(ctx context.Context, year int, userID, rating int64, changes map[int]slotChange, slots map[int]slotView) error {
	return s.withWriteTx(ctx, "nominate", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update users set rating_id = ?, updated_at = ? where id = ?`, rating, s.stamp(), userID); err != nil {
			return err
		}
		if err := s.setSlots(ctx, tx, year, userID, changes); err != nil {
			return err
		}
		return saveAppeals(ctx, tx, year, userID, slots)
	})
}

// unsavedStatus is the status of a form shown again: a parse asked for is a
// success, a mistake is the nominator's.
func unsavedStatus(f nominationForm) int {
	if f.parseOnly && !f.hasErrors() {
		return http.StatusOK
	}
	return http.StatusBadRequest
}

// slotChange is what a save does to one slot: q is the question it should
// hold, nil to empty it. The files come along from the slot's old question
// unless the nominator dropped them or attached a new one.
type slotChange struct {
	q        *store.Question
	reuse    int64 // a question moved here from another slot, as it was
	add      *upload
	keepFrom int64
}

// slotChanges compares the form with what the slots held and builds the
// questions of those that changed. gotquestions is asked here, outside the
// write lock.
func (s *server) slotChanges(ctx context.Context, c contest.Contest, before, after map[int]slotView) map[int]slotChange {
	out := map[int]slotChange{}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		now := after[slot]
		old := before[now.origin(slot)]
		moved := now.origin(slot) != slot
		unchanged := now.in.sameAs(old.in) && now.in.file == nil && !now.in.dropFile
		switch {
		case now.in.empty():
			if before[slot].questionID != 0 {
				out[slot] = slotChange{}
			}
		case unchanged && !moved:
		case unchanged && old.questionID != 0:
			out[slot] = slotChange{reuse: old.questionID}
		case !now.in.needsFields() && !now.in.fieldsGiven():
			q := s.questionFrom(ctx, c.Year, now.in.link)
			out[slot] = slotChange{q: &q}
		default:
			q := s.fieldsQuestion(ctx, c.Year, now.in)
			ch := slotChange{q: &q, add: now.in.file}
			if now.in.file == nil && !now.in.dropFile {
				ch.keepFrom = old.questionID
			}
			out[slot] = ch
		}
	}
	return out
}

// fieldsQuestion is a question as the nominator's fields give it: typed in,
// or loaded from gotquestions and perhaps corrected. A gotquestions question
// keeps its id, so a second nominator of it lands on the first one's row and
// the first one's fields.
func (s *server) fieldsQuestion(ctx context.Context, year int, in slotInput) store.Question {
	orig := in.question()
	q := store.Question{
		Year: int64(year), SourceText: in.raw, TournamentLink: in.tournament,
		TournamentID: question.FindLinks(in.tournament).TournamentID,
		Orig:         orig, Clean: orig.Anonymous(), Published: in.published(),
	}
	if !in.published() {
		return q
	}
	links := question.FindLinks(in.link)
	q.Link, q.GQID = in.link, links.GotQuestionsID
	if q.GQID == 0 {
		return q
	}
	q.Link = links.GotQuestions
	if found, err := s.gq.Question(ctx, q.GQID); err == nil {
		q.TournamentTitle = found.Tournament
		if q.TournamentID == 0 {
			q.TournamentID = found.TournamentID
		}
	}
	return q
}

// loaded is a question read by its link, its handout taken out of the text.
type loaded struct {
	q          question.Question
	handout    string
	tournament string
}

var errNotLoadable = errors.New("no question at this link")

// loadQuestion reads a published question by its link: from the gotquestions
// dump, or from a telegram post, which is parsed like a pasted question. A
// post's pictures go into the handout as their addresses.
func (s *server) loadQuestion(ctx context.Context, link string) (loaded, error) {
	if id := question.FindLinks(link).GotQuestionsID; id != 0 {
		found, err := s.gq.Question(ctx, id)
		if err != nil {
			return loaded{}, err
		}
		l := loaded{q: found.Question, tournament: tournamentLink(found.TournamentID)}
		l.handout, l.q.Text = question.SplitHandout(found.Question.Text)
		return l, nil
	}
	if !telegram.IsPost(link) {
		return loaded{}, errNotLoadable
	}
	post, err := s.telegramPost(ctx, link)
	if err != nil {
		return loaded{}, err
	}
	q, ok := question.Parse(post.Text)
	if !ok {
		return loaded{}, errNotLoadable
	}
	l := loaded{q: q}
	l.handout, l.q.Text = question.SplitHandout(q.Text)
	l.handout = strings.TrimSpace(strings.Join(append([]string{l.handout}, post.Photos...), "\n"))
	return l, nil
}

// loadSlot fills a slot's fields by the link in its source box: the form's
// load button when the page's script does not run, or a link saved unloaded.
func (s *server) loadSlot(ctx context.Context, v *slotView) {
	l, err := s.loadQuestion(ctx, v.in.raw)
	if err != nil {
		v.loadMiss, v.view = true, viewText
		return
	}
	v.in.fields, v.in.handout, v.in.link = l.q, l.handout, v.in.raw
	if v.in.tournament == "" {
		v.in.tournament = l.tournament
	}
	v.view = viewPreview
}

func tournamentLink(id int64) string {
	if id == 0 {
		return ""
	}
	return "https://rating.chgk.info/tournament/" + idstr.Format(id)
}

// questionFrom builds a published question out of its gotquestions link,
// which gotquestions is asked for. If that fails the question waits for an
// admin to fill it in.
func (s *server) questionFrom(ctx context.Context, year int, raw string) store.Question {
	links := question.FindLinks(raw)
	q := store.Question{Year: int64(year), SourceText: raw, GQID: links.GotQuestionsID, Link: links.GotQuestions, TournamentID: links.TournamentID}
	q.Published = links.GotQuestionsID != 0
	parsed, ok := question.Parse(raw)
	if !ok && links.GotQuestionsID != 0 {
		if found, err := s.gq.Question(ctx, links.GotQuestionsID); err == nil {
			parsed, ok = found.Question, true
			q.TournamentTitle = found.Tournament
			if q.TournamentID == 0 {
				q.TournamentID = found.TournamentID
			}
		}
	}
	if ok {
		q.Orig = parsed
		q.Clean = parsed.Anonymous()
	}
	return q
}

// setSlots points a nominator's changed slots at their questions, or empties
// them. Every changed slot is emptied first and refilled after, so a question
// moved from one slot to another is never left without a nomination midway.
// A question nominated by link is shared: a second nominator of the same
// gotquestions question lands on the first one's row.
func (s *server) setSlots(ctx context.Context, tx *sql.Tx, year int, userID int64, changes map[int]slotChange) error {
	var oldIDs []int64
	for slot := range changes {
		var oldID int64
		err := tx.QueryRowContext(ctx, `select question_id from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot).Scan(&oldID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if oldID != 0 {
			oldIDs = append(oldIDs, oldID)
		}
		if _, err := tx.ExecContext(ctx, `delete from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot); err != nil {
			return err
		}
	}
	for slot, ch := range changes {
		if ch.q == nil && ch.reuse == 0 {
			continue
		}
		if err := s.fillSlot(ctx, tx, year, userID, slot, ch); err != nil {
			return err
		}
	}
	// A question nobody nominates any more and nobody has numbered or reviewed
	// goes with its last nomination, and its files with it.
	for _, id := range oldIDs {
		if _, err := tx.ExecContext(ctx, `
delete from questions where id = ? and qy is null and status = 'new'
  and not exists (select 1 from nominations where question_id = ?)`, id, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) fillSlot(ctx context.Context, tx *sql.Tx, year int, userID int64, slot int, ch slotChange) error {
	id := ch.reuse
	if id == 0 {
		var err error
		if id, err = s.storeQuestion(ctx, tx, *ch.q); err != nil {
			return err
		}
	}
	if ch.keepFrom != 0 && ch.keepFrom != id {
		if err := store.CopyFiles(ctx, tx, ch.keepFrom, id); err != nil {
			return err
		}
	}
	if ch.add != nil {
		if err := store.AddFile(ctx, tx, store.File{QuestionID: id, Name: ch.add.name, ContentType: ch.add.contentType}, ch.add.data, s.stamp()); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `insert into nominations(year, user_id, slot, question_id, created_at) values(?, ?, ?, ?, ?)`,
		year, userID, slot, id, s.stamp())
	return err
}

func (s *server) storeQuestion(ctx context.Context, tx *sql.Tx, q store.Question) (int64, error) {
	if q.GQID != 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `select id from questions where year = ? and gq_id = ?`, q.Year, q.GQID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	id, err := store.InsertQuestion(ctx, tx, q, s.stamp())
	if err != nil {
		return 0, err
	}
	return id, store.AddSuggestions(ctx, tx, id, store.FromRule, anon.Rules(q.Orig, str.Anon.Placeholder()), s.stamp())
}

// handleParseQuestion is the form's parse button when the page's script runs:
// the pasted text in, the fields out, nothing saved.
func (s *server) handleParseQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	q, ok := question.Parse(req.Text)
	handout, rest := question.SplitHandout(q.Text)
	return writeJSON(w, map[string]any{
		"ok": ok, "text": rest, "handout": handout, "answer": q.Answer, "zachet": q.Zachet,
		"nezachet": q.Nezachet, "comment": q.Comment, "source": q.Source, "author": q.Author,
	})
}

// handleLoadQuestion is the form's load button when the page's script runs:
// a published question's link in, its fields out, nothing saved.
func (s *server) handleLoadQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Link string `json:"link"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	l, err := s.loadQuestion(r.Context(), strings.TrimSpace(req.Link))
	if err != nil {
		return userError(loadFailure(req.Link))
	}
	q := l.q
	return writeJSON(w, map[string]any{
		"ok": true, "text": q.Text, "handout": l.handout, "answer": q.Answer, "zachet": q.Zachet,
		"nezachet": q.Nezachet, "comment": q.Comment, "source": q.Source, "author": q.Author,
		"tournament": l.tournament,
	})
}

// handlePreviewQuestion is the form's preview and text tabs when the page's
// script runs: the fields in, the question as the preview shows it and as
// text out.
func (s *server) handlePreviewQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var q question.Question
	for _, f := range question.Fields {
		*q.Ptr(f) = strings.TrimSpace(req[f])
	}
	handout := strings.TrimSpace(req["handout"])
	full := q
	full.Text = question.WithHandout(q.Text, handout)
	return writeJSON(w, map[string]any{"html": previewHTML(q, handout), "text": question.Format(full)})
}

// handleNominationFile serves a handout file to an admin or to a nominator of
// its question.
func (s *server) handleNominationFile(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	id, err := idstr.Parse(r.PathValue("id"))
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
	if !sc.IsAdmin {
		var n int
		err := s.db.QueryRowContext(r.Context(), `select count(*) from nominations where question_id = ? and user_id = ?`, f.QuestionID, sc.User.UserID).Scan(&n)
		if err != nil {
			return err
		}
		if n == 0 {
			return route.NotFound(str.Cms.NotFound())
		}
	}
	serveFile(w, f, data)
	return nil
}

// nudge wakes the background pass without waiting for it.
func (s *server) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
