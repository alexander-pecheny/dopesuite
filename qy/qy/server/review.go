package qyserver

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/anon"
	"qy/qy/domain/contest"
	"qy/qy/domain/question"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// ---- numbering ----

// counted applies the nomination rule to what is in the database. A merged
// question counts as the one it was merged into.
func counted(ctx context.Context, q store.Querier, c contest.Contest) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `
select n.user_id, n.slot, coalesce(qq.merged_into, qq.id), coalesce(t.status, qq.status)
from nominations n
join questions qq on qq.id = n.question_id
left join questions t on t.id = qq.merged_into
join (select user_id, min(created_at) first, min(id) first_id from nominations where year = ? group by user_id) f on f.user_id = n.user_id
where n.year = ?
order by f.first, f.first_id, n.slot`, c.Year, c.Year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nominators [][]contest.Slot
	var last int64
	for rows.Next() {
		var user, qid int64
		var slot int
		var status string
		if err := rows.Scan(&user, &slot, &qid, &status); err != nil {
			return nil, err
		}
		if len(nominators) == 0 || user != last {
			nominators = append(nominators, nil)
			last = user
		}
		i := len(nominators) - 1
		nominators[i] = append(nominators[i], contest.Slot{Question: qid, Skip: status == store.StatusRejected})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return contest.Counted(nominators, c.MainSlots), nil
}

func (s *server) numberingSection(ctx context.Context, c contest.Contest) ([]ui.Item, error) {
	if c.NumberedAt != "" {
		var n int
		if err := s.db.QueryRowContext(ctx, `select count(*) from questions where year = ? and qy is not null`, c.Year).Scan(&n); err != nil {
			return nil, err
		}
		return []ui.Item{hint(str.Admin.Numbering.Done(n))}, nil
	}
	ids, err := counted(ctx, s.db, c)
	if err != nil {
		return nil, err
	}
	out := []ui.Item{hint(str.Admin.Numbering.Pending(len(ids)))}
	if st := c.StageAt(s.now()); st == contest.Before || st == contest.Nomination {
		return append(out, hint(str.Admin.Numbering.AfterNomination(dayText(c.NominationEnd)))), nil
	}
	return append(out, ui.Form(ui.Method("post"), ui.Action(contestHref(c.Year)+"/deal"), ui.Data("confirm", str.Admin.Numbering.Confirm()),
		ui.Button(ui.Submit(), ui.Primary, ui.IconDices, text(str.Admin.Numbering.Deal())))), nil
}

// cryptoRand is the source the deal shuffles with: nobody, the organisers
// included, can tell the numbers in advance.
func cryptoRand() *rand.Rand {
	var seed [32]byte
	_, _ = crand.Read(seed[:])
	return rand.New(rand.NewChaCha8(seed))
}

func (s *server) handleDeal(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	if st := c.StageAt(s.now()); st == contest.Before || st == contest.Nomination {
		return userError(str.Admin.Numbering.AfterNomination(dayText(c.NominationEnd)))
	}
	err = s.withWriteTx(r.Context(), "deal", func(ctx context.Context, tx *sql.Tx) error {
		var numbered sql.NullString
		if err := tx.QueryRowContext(ctx, `select numbered_at from contests where year = ?`, c.Year).Scan(&numbered); err != nil {
			return err
		}
		if numbered.Valid {
			return userError(str.Admin.Numbering.AlreadyDone())
		}
		ids, err := counted(ctx, tx, c)
		if err != nil {
			return err
		}
		for id, n := range contest.Deal(ids, cryptoRand()) {
			if _, err := tx.ExecContext(ctx, `update questions set qy = ? where id = ?`, n, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `update contests set numbered_at = ? where year = ?`, s.stamp(), c.Year)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year)+"/questions")
}

// ---- the question list ----

func questionHref(id int64) string { return "/admin/question/" + idstr.Format(id) }

func questionName(q store.Question) string {
	if q.QY > 0 {
		return qyLabel(q.QY)
	}
	return str.Admin.Question.Unnumbered(idstr.Format(q.ID))
}

func statusName(q store.Question) string {
	switch {
	case q.MergedInto != 0:
		return str.Admin.Question.Merged()
	case q.Status == store.StatusApproved:
		return str.Admin.Question.Approved()
	case q.Status == store.StatusRejected:
		return str.Admin.Question.Rejected()
	case q.Orig.Empty():
		return str.Admin.Question.NoText()
	}
	return str.Admin.Question.New()
}

// datesNote says whether the tournament started inside the window of
// eligible tournaments.
func datesNote(c contest.Contest, q store.Question) string {
	switch {
	case q.TournamentStart == "":
		return str.Admin.Question.DatesUnknown()
	case q.TournamentStart >= c.QuestionsFrom && q.TournamentStart <= c.QuestionsTo:
		return str.Admin.Question.DatesOk()
	}
	return str.Admin.Question.DatesOff()
}

func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// answerPreview is how much of an answer a list row shows.
const answerPreview = 40

func (s *server) handleQuestionList(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	qs, err := store.Questions(ctx, s.db, c.Year, "1")
	if err != nil {
		return err
	}
	pending, err := s.pendingSuggestions(ctx, c.Year)
	if err != nil {
		return err
	}
	rows := []ui.Item{ui.Scroll(), headRow(str.Admin.Questions.Number(), str.Admin.Questions.Answer(),
		str.Admin.Questions.Status(), str.Admin.Questions.Suggestions(), str.Admin.Questions.Dates())}
	for _, q := range qs {
		rows = append(rows, ui.Trow(
			ui.Cell(ui.Button(ui.Href(questionHref(q.ID)), ui.Ghost, ui.Small(), text(questionName(q)))),
			ui.Cell(text(clip(q.Orig.Answer, answerPreview))),
			ui.Cell(text(statusName(q))),
			ui.Cell(text(strconv.Itoa(pending[q.ID]))),
			ui.Cell(text(datesNote(c, q)))))
	}
	y := strconv.Itoa(c.Year)
	body := []ui.Item{ui.Section(hint(str.Admin.Questions.Count(len(qs))))}
	if len(qs) > 0 {
		body = append(body, ui.Section(ui.Table(rows...)))
	}
	return s.ok(w, r, view{
		title: str.Admin.Contest.Questions(),
		trail: adminTrail(crumb{contestHref(c.Year), str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Contest.Questions()}),
		body:  body,
	})
}

func (s *server) pendingSuggestions(ctx context.Context, year int) (map[int64]int, error) {
	return store.Counts(ctx, s.db, `
select s.question_id, count(*) from suggestions s join questions q on q.id = s.question_id
where q.year = ? and s.status = 'pending' group by s.question_id`, year)
}

// ---- one question ----

func (s *server) pathQuestion(r *http.Request) (store.Question, contest.Contest, error) {
	id, err := idstr.Parse(r.PathValue("id"))
	if err != nil {
		return store.Question{}, contest.Contest{}, route.NotFound(str.Admin.Question.Missing())
	}
	q, err := store.GetQuestion(r.Context(), s.db, id)
	if errors.Is(err, store.ErrNoQuestion) {
		return q, contest.Contest{}, route.NotFound(str.Admin.Question.Missing())
	}
	if err != nil {
		return q, contest.Contest{}, err
	}
	c, err := store.Contest(r.Context(), s.db, int(q.Year))
	return q, c, err
}

type nominator struct {
	telegram, rating string
	slot             int
}

func (s *server) nominatorsOf(ctx context.Context, questionID int64) ([]nominator, error) {
	rows, err := s.db.QueryContext(ctx, `
select coalesce(u.telegram_username, u.username, ''), coalesce(u.rating_id, 0), n.slot
from nominations n join users u on u.id = n.user_id where n.question_id = ? order by n.created_at`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nominator
	for rows.Next() {
		var n nominator
		var rating int64
		if err := rows.Scan(&n.telegram, &rating, &n.slot); err != nil {
			return nil, err
		}
		if rating > 0 {
			n.rating = idstr.Format(rating)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// duplicates are the other questions of the year with the same answer, which
// are probably the same question nominated twice.
func (s *server) duplicates(ctx context.Context, q store.Question) ([]store.Question, error) {
	key := question.NormalAnswer(q.Orig.Answer)
	if key == "" {
		return nil, nil
	}
	all, err := store.Questions(ctx, s.db, int(q.Year), "id != ? and merged_into is null", q.ID)
	if err != nil {
		return nil, err
	}
	var out []store.Question
	for _, o := range all {
		if question.NormalAnswer(o.Orig.Answer) == key {
			out = append(out, o)
		}
	}
	return out, nil
}

func fieldName(f string) string {
	switch f {
	case question.FieldText:
		return str.Admin.Fields.Text()
	case question.FieldAnswer:
		return str.Admin.Fields.Answer()
	case question.FieldZachet:
		return str.Admin.Fields.Zachet()
	case question.FieldNezachet:
		return str.Admin.Fields.Nezachet()
	case question.FieldComment:
		return str.Admin.Fields.Comment()
	case question.FieldSource:
		return str.Admin.Fields.Source()
	}
	return str.Admin.Fields.Author()
}

func reasonName(sg store.Suggestion) string {
	switch sg.Reason {
	case anon.ReasonAuthorName:
		return str.Admin.Suggestions.AuthorName()
	case anon.ReasonLink:
		return str.Admin.Suggestions.Link()
	}
	return sg.Reason
}

func (s *server) handleQuestionPage(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, c, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	noms, err := s.nominatorsOf(ctx, q.ID)
	if err != nil {
		return err
	}
	sugs, err := store.Suggestions(ctx, s.db, q.ID)
	if err != nil {
		return err
	}
	dups, err := s.duplicates(ctx, q)
	if err != nil {
		return err
	}
	href := questionHref(q.ID)
	y := strconv.Itoa(c.Year)

	about := []ui.Item{hint(str.Admin.Question.StatusLine(statusName(q), datesNote(c, q)))}
	for _, n := range noms {
		about = append(about, hint(str.Admin.Question.NominatedBy(n.telegram, n.rating, slotLabel(c, n.slot))))
	}
	if q.Link != "" {
		about = append(about, ui.Row(ui.Button(ui.Href(q.Link), ui.Ghost, ui.Small(), ui.IconLink, text(str.Admin.Question.OpenLink()))))
	}
	if q.AIStatus == store.AIFailed {
		about = append(about, dangerHint(str.Admin.Question.AiFailed(q.AIError)))
	}
	about = append(about, ui.Details(ui.Summary(text(str.Admin.Question.SourceText())), ui.Richtext(ui.Raw("<p>"+plain(q.SourceText)+"</p>"))))
	about = append(about, noticeOf(r)...)

	body := []ui.Item{ui.Section(about...)}
	if q.Orig.Empty() {
		body = append(body, ui.Section(subhead(str.Admin.Question.PasteTitle()), hint(str.Admin.Question.PasteHint()),
			ui.Form(ui.Method("post"), ui.Action(href+"/reparse"), ui.DirCol, ui.SpaceSM,
				ui.Editor(ui.Name("source"), ui.Rows("10"), ui.Required(), ui.Text(q.SourceText)),
				ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Admin.Question.Parse()))))))
	}
	if len(dups) > 0 {
		dupItems := []ui.Item{subhead(str.Admin.Question.Duplicates()), hint(str.Admin.Question.DuplicatesHint())}
		for _, d := range dups {
			dupItems = append(dupItems, ui.Row(ui.SpaceSM, ui.AlignCenter,
				ui.Button(ui.Href(questionHref(d.ID)), ui.Ghost, ui.Small(), text(questionName(d))),
				ui.Form(ui.Method("post"), ui.Action(href+"/merge"), ui.Data("confirm", str.Admin.Question.MergeConfirm()),
					ui.Hiddenfield(ui.Name("target"), ui.Value(idstr.Format(d.ID))),
					ui.Button(ui.Submit(), ui.Small(), text(str.Admin.Question.MergeInto(questionName(d)))))))
		}
		body = append(body, ui.Section(dupItems...))
	}
	body = append(body, ui.Section(s.suggestionItems(href, sugs)...))
	if !q.Clean.Empty() {
		body = append(body, ui.Section(subhead(str.Admin.Question.Preview()),
			ui.Card(ui.Richtext(ui.Raw(questionHTML(card{qy: q.QY, q: q.Clean, factcheck: q.Factcheck, extra: q.ExtraContext}))))))
	}
	body = append(body, ui.Section(subhead(str.Admin.Question.Edit()), ui.Form(questionForm(href, q)...)))
	return s.ok(w, r, view{
		title: questionName(q),
		trail: adminTrail(crumb{contestHref(c.Year), str.Admin.Contests.Row(y)},
			crumb{contestHref(c.Year) + "/questions", str.Admin.Contest.Questions()}, crumb{"", questionName(q)}),
		body: body,
	})
}

func (s *server) suggestionItems(href string, sugs []store.Suggestion) []ui.Item {
	out := []ui.Item{subhead(str.Admin.Suggestions.Title())}
	if len(sugs) == 0 {
		out = append(out, ui.Empty(text(str.Admin.Suggestions.None())))
	}
	for _, sg := range sugs {
		source := str.Admin.Suggestions.ByRule()
		if sg.Source == store.FromAI {
			source = str.Admin.Suggestions.ByAi()
		}
		replacement := sg.Replacement
		if replacement == "" {
			replacement = str.Admin.Suggestions.Delete()
		}
		item := []ui.Item{ui.DirCol, ui.SpaceXS,
			ui.Strong(text(str.Admin.Suggestions.Change(fieldName(sg.Field), sg.Quote, replacement))),
			ui.Muted(text(str.Admin.Suggestions.Why(source, reasonName(sg))))}
		switch sg.Status {
		case store.SuggestionPending:
			action := href + "/suggestions/" + idstr.Format(sg.ID)
			item = append(item, ui.Row(ui.SpaceSM,
				ui.Form(ui.Method("post"), ui.Action(action), ui.Hiddenfield(ui.Name("decision"), ui.Value("accept")),
					ui.Button(ui.Submit(), ui.Primary, ui.Small(), ui.IconCheck, text(str.Admin.Suggestions.Accept()))),
				ui.Form(ui.Method("post"), ui.Action(action), ui.Hiddenfield(ui.Name("decision"), ui.Value("reject")),
					ui.Button(ui.Submit(), ui.Ghost, ui.Small(), ui.IconX, text(str.Admin.Suggestions.Reject())))))
		case store.SuggestionAccepted:
			item = append(item, ui.Badge(ui.BadgePositive, text(str.Admin.Suggestions.Accepted())))
		default:
			item = append(item, ui.Badge(ui.BadgeNeutral, text(str.Admin.Suggestions.Rejected())))
		}
		out = append(out, ui.Card(item...))
	}
	return append(out, ui.Form(ui.Method("post"), ui.Action(href+"/rerun"),
		ui.Button(ui.Submit(), ui.Ghost, ui.IconWandSparkles, text(str.Admin.Suggestions.Rerun()))))
}

func questionForm(href string, q store.Question) []ui.Item {
	status := ui.Selectfield(ui.Name("status"),
		option(store.StatusNew, str.Admin.Question.New(), q.Status),
		option(store.StatusApproved, str.Admin.Question.Approved(), q.Status),
		option(store.StatusRejected, str.Admin.Question.Rejected(), q.Status))
	form := []ui.Item{ui.Method("post"), ui.Action(href), ui.DirCol, ui.SpaceSM,
		ui.Field(ui.Label(str.Admin.Question.Status()), status), hint(str.Admin.Question.EditHint())}
	for _, f := range question.Fields {
		value, rows := q.Clean.Get(f), "3"
		if f == question.FieldAuthor {
			value = q.Orig.Author
		}
		if f == question.FieldText || f == question.FieldComment {
			rows = "6"
		}
		form = append(form, ui.Field(ui.Label(fieldName(f)), ui.Editor(ui.Name(f), ui.Rows(rows), ui.Text(value))))
	}
	pin := ui.Selectfield(ui.Name("pin"),
		option("", str.Admin.Question.PinNone(), q.Pin),
		option("add", str.Admin.Question.PinAdd(), q.Pin),
		option("remove", str.Admin.Question.PinRemove(), q.Pin))
	form = append(form,
		ui.Field(ui.Label(str.Admin.Question.Tournament()), ui.Textfield(ui.Name("tournament_title"), ui.Value(q.TournamentTitle))),
		ui.Field(ui.Label(str.Admin.Question.TournamentId()), ui.Textfield(ui.Name("tournament_id"), ui.Value(idText(q.TournamentID)), ui.Inputmode("numeric"))),
		ui.Field(ui.Label(str.Admin.Question.TournamentStart()), ui.Textfield(ui.Name("tournament_start"), ui.Value(q.TournamentStart), ui.Placeholder(str.Admin.Dates.Placeholder()))),
		ui.Field(ui.Label(str.Admin.Question.Link()), ui.Textfield(ui.Name("link"), ui.Value(q.Link))),
		checkbox("published", str.Admin.Question.Published(), q.Published),
		ui.Field(ui.Label(str.Admin.Question.Factcheck()), ui.Editor(ui.Name("factcheck"), ui.Rows("4"), ui.Text(q.Factcheck))),
		ui.Field(ui.Label(str.Admin.Question.Extra()), ui.Editor(ui.Name("extra_context"), ui.Rows("3"), ui.Text(q.ExtraContext))),
		checkbox("author_declined", str.Admin.Question.AuthorDeclined(), q.AuthorDeclined),
		ui.Field(ui.Label(str.Admin.Question.Pin()), pin),
		ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save()))),
	)
	return form
}

func option(value, label, current string) ui.Item {
	items := []ui.Item{ui.Value(value), text(label)}
	if value == current {
		items = append(items, ui.Selected())
	}
	return ui.Option(items...)
}

func checkbox(name, label string, on bool) ui.Item { return checkboxOf(name, "1", label, on) }

func checkboxOf(name, value, label string, on bool) ui.Item {
	items := []ui.Item{ui.Name(name), ui.Value(value), text(label)}
	if on {
		items = append(items, ui.Checked())
	}
	return ui.Checkbox(items...)
}

func idText(id int64) string {
	if id == 0 {
		return ""
	}
	return idstr.Format(id)
}

func (s *server) handleSaveQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	if err := readQuestionForm(r, &q); err != nil {
		return err
	}
	if err := s.withWriteTx(r.Context(), "save-question", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveQuestion(ctx, tx, q, s.stamp())
	}); err != nil {
		return err
	}
	return seeOther(w, r, questionHref(q.ID)+"?done="+doneSaved)
}

// readQuestionForm applies the review form to q: the jury's copy, the
// credits, and the admin's decisions.
func readQuestionForm(r *http.Request, q *store.Question) error {
	for _, f := range question.Fields {
		v := strings.TrimSpace(strings.ReplaceAll(r.FormValue(f), "\r\n", "\n"))
		if f == question.FieldAuthor {
			q.Orig.Author = v
			continue
		}
		*q.Clean.Ptr(f) = v
	}
	status, pin := r.FormValue("status"), r.FormValue("pin")
	if !validStatus(status) || !validPin(pin) {
		return userError(str.Server.Error.BadRequest())
	}
	if status == store.StatusApproved && q.Clean.Empty() {
		return userError(str.Admin.Question.ApproveEmpty())
	}
	start := strings.TrimSpace(r.FormValue("tournament_start"))
	if _, ok := contest.Day(start); start != "" && !ok {
		return userError(str.Admin.Dates.Invalid(str.Admin.Question.TournamentStart()))
	}
	tid, _ := idstr.Parse(strings.TrimSpace(r.FormValue("tournament_id")))
	if tid != q.TournamentID {
		q.TournamentEnd = ""
	}
	q.Status, q.Pin = status, pin
	q.TournamentID, q.TournamentStart = tid, start
	q.TournamentTitle = strings.TrimSpace(r.FormValue("tournament_title"))
	q.Link = strings.TrimSpace(r.FormValue("link"))
	q.Published = r.FormValue("published") == "1"
	q.AuthorDeclined = r.FormValue("author_declined") == "1"
	q.Factcheck = strings.TrimSpace(r.FormValue("factcheck"))
	q.ExtraContext = strings.TrimSpace(r.FormValue("extra_context"))
	return nil
}

func validStatus(s string) bool {
	return s == store.StatusNew || s == store.StatusApproved || s == store.StatusRejected
}

func validPin(p string) bool { return p == "" || p == "add" || p == "remove" }

func (s *server) handleSuggestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	sid, _ := idstr.Parse(r.PathValue("sid"))
	accept := r.FormValue("decision") == "accept"
	err = s.withWriteTx(r.Context(), "suggestion", func(ctx context.Context, tx *sql.Tx) error {
		sugs, err := store.Suggestions(ctx, tx, q.ID)
		if err != nil {
			return err
		}
		for _, sg := range sugs {
			if sg.ID == sid && sg.Status == store.SuggestionPending {
				return s.decide(ctx, tx, q, sg, accept)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, questionHref(q.ID))
}

// decide accepts a suggestion, making its edit, or rejects it.
func (s *server) decide(ctx context.Context, tx *sql.Tx, q store.Question, sg store.Suggestion, accept bool) error {
	status := store.SuggestionRejected
	if accept {
		clean, err := anon.Apply(q.Clean, sg.Suggestion)
		if errors.Is(err, anon.ErrNotFound) {
			return userError(str.Admin.Suggestions.Stale())
		}
		q.Clean, status = clean, store.SuggestionAccepted
		if err := store.SaveQuestion(ctx, tx, q, s.stamp()); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `update suggestions set status = ? where id = ?`, status, sg.ID)
	return err
}

func (s *server) handleMerge(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	target, _ := idstr.Parse(r.FormValue("target"))
	err = s.withWriteTx(r.Context(), "merge", func(ctx context.Context, tx *sql.Tx) error {
		t, err := store.GetQuestion(ctx, tx, target)
		if err != nil || t.Year != q.Year || t.ID == q.ID || t.MergedInto != 0 {
			return userError(str.Admin.Question.MergeRefused())
		}
		// Whatever was merged into q follows it into t, so a chain never forms.
		if _, err := tx.ExecContext(ctx, `update questions set merged_into = ? where merged_into = ?`, t.ID, q.ID); err != nil {
			return err
		}
		q.MergedInto, q.Status = t.ID, store.StatusRejected
		return store.SaveQuestion(ctx, tx, q, s.stamp())
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, questionHref(target))
}

// handleReparse fills in a question that arrived without text it could read:
// a gotquestions link while gotquestions is down, or a paste with no answer.
func (s *server) handleReparse(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	parsed, ok := question.Parse(r.FormValue("source"))
	if !ok {
		return userError(str.Admin.Question.ParseFailed())
	}
	q.Orig, q.Clean, q.AIStatus = parsed, parsed.Anonymous(), store.AIPending
	err = s.withWriteTx(r.Context(), "reparse", func(ctx context.Context, tx *sql.Tx) error {
		if err := store.SaveQuestion(ctx, tx, q, s.stamp()); err != nil {
			return err
		}
		return store.AddSuggestions(ctx, tx, q.ID, store.FromRule, anon.Rules(q.Orig, str.Anon.Placeholder()), s.stamp())
	})
	if err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, questionHref(q.ID))
}

func (s *server) handleRerun(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	q, _, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	err = s.withWriteTx(r.Context(), "rerun", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update questions set ai_status = ?, ai_error = '', updated_at = ? where id = ?`, store.AIPending, s.stamp(), q.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.nudge()
	return seeOther(w, r, questionHref(q.ID))
}

// sinceStamp formats a stored moment for a hint, in Moscow time.
func sinceStamp(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return dayText(t.In(contest.Moscow).Format(contest.DateLayout))
}
