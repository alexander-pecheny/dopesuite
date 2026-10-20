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
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The nomination form: up to MainSlots questions plus ReserveSlots reserves,
// each a gotquestions link or the full text. A slot can be changed until the
// stage ends; what the form shows is what the database holds.

type slotView struct {
	source string
	parsed question.Question
	ok     bool
	link   string
}

func (s *server) nominationSlots(ctx context.Context, year int, userID int64) (map[int]slotView, error) {
	rows, err := s.db.QueryContext(ctx, `
select n.slot, q.source_text, q.orig, q.link from nominations n join questions q on q.id = n.question_id
where n.year = ? and n.user_id = ?`, year, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]slotView{}
	for rows.Next() {
		var slot int
		var v slotView
		var orig string
		if err := rows.Scan(&slot, &v.source, &orig, &v.link); err != nil {
			return nil, err
		}
		v.parsed = question.FromJSON(orig)
		v.ok = v.parsed.Answer != ""
		out[slot] = v
	}
	return out, rows.Err()
}

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

func slotLabel(c contest.Contest, slot int) string {
	if slot <= c.MainSlots {
		return str.Nominate.Slot.Main(strconv.Itoa(slot))
	}
	return str.Nominate.Slot.Reserve(strconv.Itoa(slot - c.MainSlots))
}

func (s *server) handleNominatePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	c, err := s.currentContest(r.Context())
	if err != nil {
		return err
	}
	v := view{title: str.Nominate.Page.Title(), trail: []crumb{{"", str.Nominate.Page.Title()}}}
	if c.StageAt(s.now()) != contest.Nomination {
		v.body = []ui.Item{ui.Section(hint(str.Nominate.Page.Closed(dayText(c.NominationStart), dayText(c.NominationEnd))))}
		return s.ok(w, r, v)
	}
	slots, err := s.nominationSlots(r.Context(), c.Year, sc.User.UserID)
	if err != nil {
		return err
	}
	rating, err := s.ratingID(r.Context(), sc.User.UserID)
	if err != nil {
		return err
	}
	form := []ui.Item{ui.Method("post"), ui.Action("/nominate"), ui.DirCol, ui.SpaceLG,
		ui.Field(ui.Label(str.Common.RatingId()), ui.Textfield(ui.Name("rating_id"), ui.Value(rating), ui.Inputmode("numeric"), ui.Required())),
	}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		if slot == c.MainSlots+1 {
			form = append(form, subhead(str.Nominate.Page.Reserves()))
		}
		sv := slots[slot]
		field := []ui.Item{ui.Label(slotLabel(c, slot)), ui.Editor(ui.Name("slot"+strconv.Itoa(slot)), ui.Rows("6"), ui.Text(sv.source))}
		switch {
		case sv.source == "":
		case sv.ok:
			field = append(field, hint(str.Nominate.Slot.Parsed(strings.TrimRight(sv.parsed.Answer, "."))))
		case sv.link != "":
			field = append(field, hint(str.Nominate.Slot.LinkOnly()))
		default:
			field = append(field, dangerHint(str.Nominate.Slot.Unparsed()))
		}
		form = append(form, ui.Field(field...))
	}
	form = append(form, ui.Row(ui.Button(ui.Primary, ui.Submit(), text(str.Nominate.Page.Submit()))))
	v.body = []ui.Item{
		ui.Section(append([]ui.Item{
			hint(str.Nominate.Page.Lead(strconv.Itoa(c.MainSlots), strconv.Itoa(c.ReserveSlots), dayText(c.NominationEnd))),
			hint(str.Nominate.Page.Eligible(dayText(c.QuestionsFrom), dayText(c.QuestionsTo))),
			hint(str.Nominate.Page.HowTo()),
		}, noticeOf(r)...)...),
		ui.Section(ui.Form(form...)),
	}
	return s.ok(w, r, v)
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
	if err := parseForm(w, r); err != nil {
		return err
	}
	c, err := s.currentContest(r.Context())
	if err != nil {
		return err
	}
	if c.StageAt(s.now()) != contest.Nomination {
		return userError(str.Nominate.Page.Closed(dayText(c.NominationStart), dayText(c.NominationEnd)))
	}
	rating, err := parseRatingID(r.FormValue("rating_id"))
	if err != nil {
		return err
	}
	before, err := s.nominationSlots(r.Context(), c.Year, sc.User.UserID)
	if err != nil {
		return err
	}
	// gotquestions is asked outside the write lock: it is a network call.
	fresh := map[int]store.Question{}
	for slot := 1; slot <= c.MainSlots+c.ReserveSlots; slot++ {
		raw := strings.TrimSpace(strings.ReplaceAll(r.FormValue("slot"+strconv.Itoa(slot)), "\r\n", "\n"))
		if raw != "" && raw != before[slot].source {
			fresh[slot] = s.questionFrom(r.Context(), c.Year, raw)
		}
		if raw == "" && before[slot].source != "" {
			fresh[slot] = store.Question{}
		}
	}
	err = s.withWriteTx(r.Context(), "nominate", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update users set rating_id = ?, updated_at = ? where id = ?`, rating, s.stamp(), sc.User.UserID); err != nil {
			return err
		}
		for slot, q := range fresh {
			if err := s.setSlot(ctx, tx, c.Year, sc.User.UserID, slot, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, "/nominate?done="+doneSaved)
}

// questionFrom builds a question out of one slot's text. A gotquestions link
// marks it published; when the text alone has no answer, gotquestions is asked
// for it, and if that fails the question waits for an admin to fill it in.
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

// setSlot points a nominator's slot at q, or empties it when q has no source.
// A question nominated by link is shared: a second nominator of the same
// gotquestions question lands on the first one's row.
func (s *server) setSlot(ctx context.Context, tx *sql.Tx, year int, userID int64, slot int, q store.Question) error {
	var oldID int64
	err := tx.QueryRowContext(ctx, `select question_id from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot).Scan(&oldID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from nominations where year = ? and user_id = ? and slot = ?`, year, userID, slot); err != nil {
		return err
	}
	if q.SourceText != "" {
		id, err := s.storeQuestion(ctx, tx, q)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into nominations(year, user_id, slot, question_id, created_at) values(?, ?, ?, ?, ?)`,
			year, userID, slot, id, s.stamp()); err != nil {
			return err
		}
	}
	if oldID == 0 {
		return nil
	}
	// A question nobody nominates any more and nobody has numbered or reviewed
	// goes with its last nomination.
	_, err = tx.ExecContext(ctx, `
delete from questions where id = ? and qy is null and status = 'new'
  and not exists (select 1 from nominations where question_id = ?)`, oldID, oldID)
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

// nudge wakes the background pass without waiting for it.
func (s *server) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
