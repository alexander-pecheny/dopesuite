package qyserver

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strconv"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/domain/tally"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// What only the admins see: the first-stage counts with the shortlist they
// make, and the final count. Each page also holds the step that makes its
// result public.

func whyName(why string) string {
	switch why {
	case tally.ByExpert:
		return str.Admin.Stage1.ByExpert()
	case tally.ByPeople:
		return str.Admin.Stage1.ByPeople()
	case tally.ByPin:
		return str.Admin.Stage1.ByPin()
	}
	return ""
}

func (s *server) handleStage1Report(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	cands, err := candidates(ctx, s.db, c)
	if err != nil {
		return err
	}
	why := map[int64]string{}
	for _, p := range computeShortlist(c, cands) {
		why[p.ID] = whyName(p.Why)
	}
	slices.SortStableFunc(cands, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.expert, a.expert), cmp.Compare(b.people, a.people), cmp.Compare(a.QY, b.QY))
	})
	rows := []ui.Item{ui.Scroll(), headRow(str.Admin.Questions.Number(), str.Admin.Questions.Answer(),
		str.Admin.Stage1.Expert(), str.Admin.Stage1.People(), str.Admin.Stage1.Shortlist())}
	for _, cd := range cands {
		note := why[cd.ID]
		if cd.AuthorDeclined {
			note = str.Admin.Question.AuthorDeclined()
		} else if cd.Pin == "remove" {
			note = str.Admin.Question.PinRemove()
		}
		rows = append(rows, ui.Trow(
			ui.Cell(ui.Button(ui.Href(questionHref(cd.ID)), ui.Ghost, ui.Small(), text(qyLabel(cd.QY)))),
			ui.Cell(text(clip(cd.Orig.Answer, answerPreview))),
			ui.Cell(text(strconv.Itoa(cd.expert))), ui.Cell(text(strconv.Itoa(cd.people))),
			ui.Cell(text(note))))
	}
	base := contestHref(c.Year)
	step := []ui.Item{subhead(str.Admin.Stage1.FreezeTitle())}
	if c.ShortlistFrozenAt == "" {
		step = append(step, hint(str.Admin.Stage1.FreezeHint(len(why))),
			ui.Form(ui.Method("post"), ui.Action(base+"/freeze"), ui.Data("confirm", str.Admin.Stage1.FreezeConfirm()),
				ui.Button(ui.Submit(), ui.Primary, ui.IconLock, text(str.Admin.Stage1.Freeze()))))
	} else {
		step = append(step, hint(str.Admin.Stage1.Frozen(sinceStamp(c.ShortlistFrozenAt))),
			ui.Form(ui.Method("post"), ui.Action(base+"/unfreeze"), ui.Data("confirm", str.Admin.Stage1.UnfreezeConfirm()),
				ui.Button(ui.Submit(), ui.Ghost, text(str.Admin.Stage1.Unfreeze()))))
	}
	y := strconv.Itoa(c.Year)
	return s.ok(w, r, view{
		title: str.Admin.Contest.Stage1(),
		trail: adminTrail(crumb{base, str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Contest.Stage1()}),
		body: []ui.Item{
			ui.Section(hint(str.Admin.Stage1.Lead(c.ExpertCutoff, c.PeopleCutoff))),
			ui.Section(step...),
			ui.Section(ui.Table(rows...)),
		},
	})
}

func (s *server) handleFreeze(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	err = s.withWriteTx(r.Context(), "freeze", func(ctx context.Context, tx *sql.Tx) error {
		cands, err := candidates(ctx, tx, c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from shortlist where year = ?`, c.Year); err != nil {
			return err
		}
		for _, p := range computeShortlist(c, cands) {
			if _, err := tx.ExecContext(ctx, `insert into shortlist(year, question_id) values(?, ?)`, c.Year, p.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `update contests set shortlist_frozen_at = ? where year = ?`, s.stamp(), c.Year)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year)+"/stage1")
}

func (s *server) handleUnfreeze(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	if c.ResultsPublishedAt != "" {
		return userError(str.Admin.Stage1.UnfreezePublished())
	}
	err = s.withWriteTx(r.Context(), "unfreeze", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update contests set shortlist_frozen_at = null where year = ?`, c.Year)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year)+"/stage1")
}

func (s *server) handleFinalReport(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	t, err := s.countFinal(r.Context(), c)
	if err != nil {
		return err
	}
	rows := []ui.Item{ui.Scroll(), headRow(str.Admin.Questions.Number(), str.Admin.Questions.Answer(),
		str.Admin.Final.JuryPlace(), str.Admin.Final.RankSum(), str.Admin.Final.Points(),
		str.Admin.Final.PopularPlace(), str.Admin.Final.PopularPoints())}
	for _, res := range t.results {
		q := t.byID[res.ID]
		rows = append(rows, ui.Trow(
			ui.Cell(ui.Button(ui.Href(questionHref(q.ID)), ui.Ghost, ui.Small(), text(qyLabel(q.QY)))),
			ui.Cell(text(clip(q.Orig.Answer, answerPreview))),
			ui.Cell(text(placesText(res.Jury.Place))), ui.Cell(text(rankSumText(res.Jury.RankSum))), ui.Cell(text(strconv.Itoa(res.Jury.Points))),
			ui.Cell(text(placesText(res.Popular.Place))), ui.Cell(text(strconv.Itoa(res.Popular.Points)))))
	}
	base := contestHref(c.Year)
	publish := []ui.Item{subhead(str.Admin.Final.PublishTitle())}
	if c.ResultsPublishedAt == "" {
		publish = append(publish, hint(str.Admin.Final.PublishHint(strconv.Itoa(c.Year))),
			ui.Form(ui.Method("post"), ui.Action(base+"/publish"), ui.Data("confirm", str.Admin.Final.PublishConfirm()),
				ui.Button(ui.Submit(), ui.Primary, text(str.Admin.Final.Publish()))))
	} else {
		publish = append(publish, hint(str.Admin.Final.Published(sinceStamp(c.ResultsPublishedAt))),
			ui.Form(ui.Method("post"), ui.Action(base+"/unpublish"),
				ui.Button(ui.Submit(), ui.Ghost, text(str.Admin.Final.Unpublish()))))
	}
	y := strconv.Itoa(c.Year)
	body := []ui.Item{ui.Section(hint(str.Admin.Final.Ballots(strconv.Itoa(t.complete), t.experts)), hint(str.Admin.Final.Voters(t.voters)))}
	if len(t.results) > 0 {
		body = append(body, ui.Section(ui.Table(rows...)))
	}
	return s.ok(w, r, view{
		title: str.Admin.Contest.Final(),
		trail: adminTrail(crumb{base, str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Contest.Final()}),
		body:  append(body, ui.Section(publish...)),
	})
}

func (s *server) setPublished(w http.ResponseWriter, r *http.Request, on bool) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	if on && c.ShortlistFrozenAt == "" {
		return userError(str.Final.NoShortlist())
	}
	var at any
	if on {
		at = s.stamp()
	}
	err = s.withWriteTx(r.Context(), "publish", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update contests set results_published_at = ? where year = ?`, at, c.Year)
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, contestHref(c.Year)+"/final")
}

func (s *server) handlePublish(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.setPublished(w, r, true)
}

func (s *server) handleUnpublish(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	return s.setPublished(w, r, false)
}

// ---- the public pages of a year ----

// yearTrail is the trail of a page under /{year}/.
func yearTrail(year int, title string) []crumb {
	y := strconv.Itoa(year)
	return []crumb{{"/" + y + "/", str.Contest.Name(y)}, {"", title}}
}

// intro is a CMS page an admin writes to open a generated page, if there is
// one.
func (s *server) intro(ctx context.Context, path string, vars map[string]string) ([]ui.Item, error) {
	p, err := s.loadPage(ctx, path)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []ui.Item{ui.Richtext(ui.Raw(renderPage(p, vars)))}, nil
}

// shortlistVisible reports whether a year's shortlist is public: frozen, and
// the final stage reached.
func (s *server) shortlistVisible(c contest.Contest) bool {
	st := c.StageAt(s.now())
	return c.ShortlistFrozenAt != "" && st >= contest.Final
}

func (s *server) serveShortlist(w http.ResponseWriter, r *http.Request, sc route.Scope, c contest.Contest) error {
	ctx := r.Context()
	qs, err := shortlistQuestions(ctx, s.db, c)
	if err != nil {
		return err
	}
	vars, err := s.rulesVars(ctx, c)
	if err != nil {
		return err
	}
	body, err := s.intro(ctx, strconv.Itoa(c.Year)+"/shortlist-intro", vars)
	if err != nil {
		return err
	}
	if c.StageAt(s.now()) == contest.Final {
		body = append(body, ui.Row(ui.SpaceSM, ui.Wrap(),
			ui.Button(ui.Href("/popular"), ui.Primary, text(str.Final.Popular.Title())),
			ui.Button(ui.Href("/final"), ui.Ghost, text(str.Final.Jury.Title()))))
	}
	seed := strconv.Itoa(c.Year) + "/shortlist"
	if sc.HasUser {
		seed += "/" + idstr.Format(sc.User.UserID)
	}
	for _, q := range inReaderOrder(qs, seed) {
		body = append(body, ui.Card(ui.Richtext(ui.Raw(questionHTML(card{qy: q.QY, q: q.Clean, extra: q.ExtraContext, factcheck: q.Factcheck})))))
	}
	return s.ok(w, r, view{title: str.Results.Shortlist(), trail: yearTrail(c.Year, str.Results.Shortlist()),
		body: []ui.Item{ui.Section(body...)}})
}

func (s *server) serveResults(w http.ResponseWriter, r *http.Request, c contest.Contest) error {
	ctx := r.Context()
	t, err := s.countFinal(ctx, c)
	if err != nil {
		return err
	}
	vars, err := s.rulesVars(ctx, c)
	if err != nil {
		return err
	}
	body, err := s.intro(ctx, strconv.Itoa(c.Year)+"/results-intro", vars)
	if err != nil {
		return err
	}
	for _, res := range t.results {
		q := t.byID[res.ID]
		body = append(body, ui.Card(ui.Richtext(ui.Raw(questionHTML(card{
			qy: q.QY, q: q.Clean, tournament: q.TournamentTitle, places: placesLine(res),
			extra: q.ExtraContext, factcheck: q.Factcheck,
		}.withAuthor(q.Orig.Author))))))
	}
	return s.ok(w, r, view{title: str.Results.Title(), trail: yearTrail(c.Year, str.Results.Title()),
		body: []ui.Item{ui.Section(body...)}})
}

func (c card) withAuthor(author string) card {
	c.q.Author = author
	return c
}
