package qyserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/anon"
	"qy/qy/domain/contest"
	"qy/qy/domain/question"
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

func (s *server) handleNominatePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	c, err := s.currentContest(r.Context())
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
	// An empty slot starts as a published question: most are.
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		if _, ok := slots[slot]; !ok {
			slots[slot] = slotView{in: slotInput{published: true}}
		}
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
	c, err := s.currentContest(r.Context())
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
	if v, ok := form.slots[form.loadSlot]; ok {
		s.loadSlot(r.Context(), &v)
		form.slots[form.loadSlot] = v
	}
	rating, ratingErr := parseRatingID(form.rating)
	if form.parseOnly || form.hasErrors() || ratingErr != nil {
		if ratingErr != nil && !form.parseOnly {
			form.ratingErr = ratingErr
		}
		return s.render(w, r, unsavedStatus(form), s.nominationView(r, c, sc, form))
	}
	changes := s.slotChanges(r.Context(), c, before, form.slots)
	if err := s.saveNomination(r.Context(), c.Year, sc.User.UserID, rating, changes); err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, "/nominate?done="+doneSaved)
}

func (s *server) saveNomination(ctx context.Context, year int, userID, rating int64, changes map[int]slotChange) error {
	return s.withWriteTx(ctx, "nominate", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update users set rating_id = ?, updated_at = ? where id = ?`, rating, s.stamp(), userID); err != nil {
			return err
		}
		for slot, ch := range changes {
			if err := s.setSlot(ctx, tx, year, userID, slot, ch); err != nil {
				return err
			}
		}
		return nil
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
	add      *upload
	keepFrom int64
}

// slotChanges compares the form with what the slots held and builds the
// questions of those that changed. gotquestions is asked here, outside the
// write lock.
func (s *server) slotChanges(ctx context.Context, c contest.Contest, before, after map[int]slotView) map[int]slotChange {
	out := map[int]slotChange{}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		old, now := before[slot], after[slot]
		switch {
		case now.in.empty():
			if old.questionID != 0 {
				out[slot] = slotChange{}
			}
		case now.in.sameAs(old.in) && now.in.file == nil && !now.in.dropFile:
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
		Orig:         orig, Clean: orig.Anonymous(), Published: in.published,
	}
	if !in.published {
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

// loadSlot fills a slot's fields from gotquestions, the form's load button
// when the page's script does not run.
func (s *server) loadSlot(ctx context.Context, v *slotView) {
	found, err := s.gq.Question(ctx, question.FindLinks(v.in.link).GotQuestionsID)
	if err != nil {
		v.loadMiss = true
		return
	}
	v.in.fields = found.Question
	v.in.handout, v.in.fields.Text = question.SplitHandout(found.Question.Text)
	if v.in.tournament == "" {
		v.in.tournament = tournamentLink(found.TournamentID)
	}
	v.view = viewFields
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

// setSlot points a nominator's slot at the change's question, or empties it.
// A question nominated by link is shared: a second nominator of the same
// gotquestions question lands on the first one's row.
func (s *server) setSlot(ctx context.Context, tx *sql.Tx, year int, userID int64, slot int, ch slotChange) error {
	var oldID int64
	err := tx.QueryRowContext(ctx, `select question_id from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot).Scan(&oldID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot); err != nil {
		return err
	}
	if ch.q != nil {
		if err := s.fillSlot(ctx, tx, year, userID, slot, ch); err != nil {
			return err
		}
	}
	if oldID == 0 {
		return nil
	}
	// A question nobody nominates any more and nobody has numbered or reviewed
	// goes with its last nomination, and its files with it.
	_, err = tx.ExecContext(ctx, `
delete from questions where id = ? and qy is null and status = 'new'
  and not exists (select 1 from nominations where question_id = ?)`, oldID, oldID)
	return err
}

func (s *server) fillSlot(ctx context.Context, tx *sql.Tx, year int, userID int64, slot int, ch slotChange) error {
	id, err := s.storeQuestion(ctx, tx, *ch.q)
	if err != nil {
		return err
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
	_, err = tx.ExecContext(ctx, `insert into nominations(year, user_id, slot, question_id, created_at) values(?, ?, ?, ?, ?)`,
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
// a gotquestions link in, the question's fields out, nothing saved.
func (s *server) handleLoadQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Link string `json:"link"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	id := question.FindLinks(req.Link).GotQuestionsID
	found, err := s.gq.Question(r.Context(), id)
	if id == 0 || err != nil {
		return userError(str.Nominate.Slot.LoadFailed())
	}
	q := found.Question
	handout, rest := question.SplitHandout(q.Text)
	return writeJSON(w, map[string]any{
		"ok": true, "text": rest, "handout": handout, "answer": q.Answer, "zachet": q.Zachet,
		"nezachet": q.Nezachet, "comment": q.Comment, "source": q.Source, "author": q.Author,
		"tournament": tournamentLink(found.TournamentID),
	})
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
