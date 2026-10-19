package qyserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The first stage, for the two juries. During the stage a juror reads the
// open portions one at a time, each in an order of their own, and likes what
// they like. During the factcheck they see the preliminary shortlist with its
// factcheck, and may withdraw a like but not add one.

// juror is the caller as the voting pages need them.
type juror struct {
	userID int64
	kind   string
}

// currentJuror is the current contest and the caller's place on its juries.
func (s *server) currentJuror(ctx context.Context, sc route.Scope) (contest.Contest, juror, error) {
	c, err := s.currentContest(ctx)
	if err != nil {
		return c, juror{}, err
	}
	kind, err := store.JurorKind(ctx, s.db, c.Year, sc.User.Telegram.String)
	return c, juror{sc.User.UserID, kind}, err
}

// postedIDs reads the ids a form posted under name.
func postedIDs(r *http.Request, name string) map[int64]bool {
	out := map[int64]bool{}
	for _, v := range r.Form[name] {
		if id, err := idstr.Parse(v); err == nil {
			out[id] = true
		}
	}
	return out
}

// portionQuestions are the questions of portion k a juror can read: approved
// ones, and for the people's jury only those already published.
func portionQuestions(ctx context.Context, q store.Querier, c contest.Contest, j juror, k int) ([]store.Question, error) {
	from, to := (k-1)*c.PortionSize+1, k*c.PortionSize
	where := "status = 'approved' and qy between ? and ?"
	if j.kind == store.People {
		where += " and published = 1"
	}
	return store.Questions(ctx, q, c.Year, where, from, to)
}

func portionCount(ctx context.Context, q store.Querier, c contest.Contest) (int, error) {
	var maxQY int
	if err := q.QueryRowContext(ctx, `select coalesce(max(qy), 0) from questions where year = ?`, c.Year).Scan(&maxQY); err != nil {
		return 0, err
	}
	if maxQY == 0 || c.PortionSize < 1 {
		return 0, nil
	}
	return (maxQY-1)/c.PortionSize + 1, nil
}

func (s *server) likedBy(ctx context.Context, userID int64) (map[int64]bool, error) {
	ids, err := store.Collect(ctx, s.db, store.Scalar[int64], `select question_id from likes where user_id = ?`, userID)
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// inReaderOrder is qs in the order this reader sees them on this page.
func inReaderOrder(qs []store.Question, seed string) []store.Question {
	byID := map[int64]store.Question{}
	ids := make([]int64, len(qs))
	for i, q := range qs {
		byID[q.ID], ids[i] = q, q.ID
	}
	out := make([]store.Question, len(qs))
	for i, id := range contest.Shuffle(ids, seed) {
		out[i] = byID[id]
	}
	return out
}

func voteTrail() []crumb { return []crumb{{"", str.Vote.Page.Title()}} }

func (s *server) handleVotePage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	c, j, err := s.currentJuror(ctx, sc)
	if err != nil {
		return err
	}
	v := view{title: str.Vote.Page.Title(), trail: voteTrail()}
	if j.kind == "" {
		v.body = []ui.Item{ui.Section(hint(str.Vote.Page.NotJuror()))}
		return s.ok(w, r, v)
	}
	switch c.StageAt(s.now()) {
	case contest.Prelim:
		v.body, err = s.portionBody(ctx, r, c, j)
	case contest.Factcheck:
		v.body, err = s.factcheckBody(ctx, c, j)
	default:
		v.body = []ui.Item{ui.Section(hint(str.Vote.Page.Closed(dayText(c.PrelimStart), dayText(c.FactcheckEnd))))}
	}
	if err != nil {
		return err
	}
	return s.ok(w, r, v)
}

func (s *server) portionBody(ctx context.Context, r *http.Request, c contest.Contest, j juror) ([]ui.Item, error) {
	total, err := portionCount(ctx, s.db, c)
	if err != nil {
		return nil, err
	}
	open := c.PortionsOpen(s.now(), total)
	if open == 0 {
		return []ui.Item{ui.Section(hint(str.Vote.Page.NothingYet()))}, nil
	}
	k, err := strconv.Atoi(r.URL.Query().Get("portion"))
	if err != nil || k < 1 || k > open {
		k = open
	}
	qs, err := portionQuestions(ctx, s.db, c, j, k)
	if err != nil {
		return nil, err
	}
	liked, err := s.likedBy(ctx, j.userID)
	if err != nil {
		return nil, err
	}
	var tabs []ui.Item
	for i := 1; i <= open; i++ {
		b := []ui.Item{ui.Href("/vote?portion=" + strconv.Itoa(i)), ui.Small(), text(strconv.Itoa(i))}
		if i == k {
			b = append(b, ui.Primary)
		} else {
			b = append(b, ui.Ghost)
		}
		tabs = append(tabs, ui.Button(b...))
	}
	head := []ui.Item{
		hint(str.Vote.Portion.Lead(c.PortionDays)),
		ui.Row(append([]ui.Item{ui.SpaceXS, ui.Wrap(), ui.Aria("label", str.Vote.Portion.Tabs())}, tabs...)...),
		hint(str.Vote.Portion.Title(strconv.Itoa(k), dayText(c.PortionOpens(k).Format(contest.DateLayout)))),
	}
	if k < total && k == open {
		head = append(head, hint(str.Vote.Portion.Next(dayText(c.PortionOpens(k+1).Format(contest.DateLayout)))))
	}
	if len(qs) == 0 {
		return []ui.Item{ui.Section(head...), ui.Section(ui.Empty(text(str.Vote.Portion.Empty())))}, nil
	}
	form := []ui.Item{ui.Method("post"), ui.Action("/vote"), ui.DirCol, ui.SpaceMD, ui.Data("autosave", ""),
		ui.Hiddenfield(ui.Name("portion"), ui.Value(strconv.Itoa(k)))}
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/%d/%d", c.Year, k, j.userID)) {
		form = append(form, likeCard(q, card{qy: q.QY, q: q.Clean, extra: q.ExtraContext}, liked[q.ID]))
	}
	form = append(form, ui.Row(ui.SpaceSM, ui.AlignCenter,
		ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save())), ui.Message(ui.ID("autosaveStatus"))))
	return []ui.Item{ui.Section(head...), ui.Section(ui.Form(form...))}, nil
}

func likeCard(q store.Question, c card, liked bool) ui.Item {
	return ui.Card(ui.DirCol, ui.SpaceSM,
		ui.Richtext(ui.Raw(questionHTML(c))),
		checkboxOf("like", idstr.Format(q.ID), str.Vote.Like(), liked))
}

func (s *server) factcheckBody(ctx context.Context, c contest.Contest, j juror) ([]ui.Item, error) {
	qs, err := shortlistQuestions(ctx, s.db, c)
	if err != nil {
		return nil, err
	}
	liked, err := s.likedBy(ctx, j.userID)
	if err != nil {
		return nil, err
	}
	head := ui.Section(hint(str.Vote.Factcheck.Lead(dayText(c.FactcheckEnd))))
	form := []ui.Item{ui.Method("post"), ui.Action("/vote"), ui.DirCol, ui.SpaceMD, ui.Data("autosave", "")}
	shown := 0
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/factcheck/%d", c.Year, j.userID)) {
		if !liked[q.ID] {
			continue
		}
		shown++
		form = append(form, likeCard(q, card{qy: q.QY, q: q.Clean, extra: q.ExtraContext, factcheck: q.Factcheck}, true))
	}
	if shown == 0 {
		return []ui.Item{head, ui.Section(ui.Empty(text(str.Vote.Factcheck.Nothing())))}, nil
	}
	form = append(form, ui.Row(ui.SpaceSM, ui.AlignCenter,
		ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save())), ui.Message(ui.ID("autosaveStatus"))))
	return []ui.Item{head, ui.Section(ui.Form(form...))}, nil
}

// handleVote saves the likes on one page. What the page showed is the scope:
// a like on a question the juror could not see is ignored, and during the
// factcheck a like can only be taken back.
func (s *server) handleVote(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	c, j, err := s.currentJuror(ctx, sc)
	if err != nil {
		return err
	}
	if j.kind == "" {
		return route.Forbidden(str.Vote.Page.NotJuror())
	}
	posted := postedIDs(r, "like")
	stage := c.StageAt(s.now())
	k, _ := strconv.Atoi(r.FormValue("portion"))
	err = s.withWriteTx(ctx, "vote", func(ctx context.Context, tx *sql.Tx) error {
		scope, err := s.voteScope(ctx, tx, c, j, stage, k)
		if err != nil {
			return err
		}
		for _, q := range scope {
			if err := s.setLike(ctx, tx, j.userID, q.ID, posted[q.ID], stage == contest.Prelim); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if wantsJSON(r) {
		return writeJSON(w, map[string]string{"status": str.Common.Saved()})
	}
	target := "/vote?done=" + doneSaved
	if k > 0 {
		target += "&portion=" + strconv.Itoa(k)
	}
	return seeOther(w, r, target)
}

// voteScope is what the page being saved showed the juror.
func (s *server) voteScope(ctx context.Context, tx *sql.Tx, c contest.Contest, j juror, stage contest.Stage, k int) ([]store.Question, error) {
	switch stage {
	case contest.Prelim:
		total, err := portionCount(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		if k < 1 || k > c.PortionsOpen(s.now(), total) {
			return nil, userError(str.Vote.Portion.NotOpen())
		}
		return portionQuestions(ctx, tx, c, j, k)
	case contest.Factcheck:
		return shortlistQuestions(ctx, tx, c)
	}
	return nil, userError(str.Vote.Page.Closed(dayText(c.PrelimStart), dayText(c.FactcheckEnd)))
}

// setLike writes one question's like as the form left it. canAdd is false
// during the factcheck, when a like can only be withdrawn.
func (s *server) setLike(ctx context.Context, tx *sql.Tx, userID, questionID int64, on, canAdd bool) error {
	var err error
	switch {
	case on && canAdd:
		_, err = tx.ExecContext(ctx, `insert or ignore into likes(user_id, question_id, created_at) values(?, ?, ?)`, userID, questionID, s.stamp())
	case !on:
		_, err = tx.ExecContext(ctx, `delete from likes where user_id = ? and question_id = ?`, userID, questionID)
	}
	return err
}

// wantsJSON is a form posted by forms.ts, which saves without leaving the page.
func wantsJSON(r *http.Request) bool { return r.Header.Get("Accept") == "application/json" }
