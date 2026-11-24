package qyserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"code.pecheny.me/pecheny/dopesuite/dopecore/idstr"

	"qy/qy/domain/contest"
	"qy/qy/domain/tally"
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

// yearJuror is the contest of the page's year and the caller's place on its
// juries.
func (s *server) yearJuror(r *http.Request, sc route.Scope) (contest.Contest, juror, error) {
	c, err := s.yearContest(r)
	if err != nil {
		return c, juror{}, err
	}
	kind, err := store.JurorKind(r.Context(), s.db, c.Year, sc.User.UserID)
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

// checkedPortions are the year's portions an admin has marked checked.
func checkedPortions(ctx context.Context, q store.Querier, year int) (map[int]bool, error) {
	ks, err := store.Collect(ctx, q, store.Scalar[int64], `select portion from portion_checks where year = ?`, year)
	out := make(map[int]bool, len(ks))
	for _, k := range ks {
		out[int(k)] = true
	}
	return out, err
}

// portionsOpen is how many portions the jury can read now: those whose day
// has come, up to the first one no admin has checked yet.
func (s *server) portionsOpen(ctx context.Context, q store.Querier, c contest.Contest, total int) (int, error) {
	checked, err := checkedPortions(ctx, q, c.Year)
	if err != nil {
		return 0, err
	}
	open := c.PortionsOpen(s.now(), total)
	for k := 1; k <= open; k++ {
		if !checked[k] {
			return k - 1, nil
		}
	}
	return open, nil
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
	c, j, err := s.yearJuror(r, sc)
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
		if r.URL.Query().Get("portion") == likedTab {
			v.body, err = s.likedBody(ctx, c, j)
		} else {
			v.body, err = s.portionBody(ctx, r, c, j)
		}
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
	open, err := s.portionsOpen(ctx, s.db, c, total)
	if err != nil {
		return nil, err
	}
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
	tabs, err := s.portionTabs(ctx, c, strconv.Itoa(k))
	if err != nil {
		return nil, err
	}
	head := []ui.Item{
		hint(str.Vote.Portion.Lead(c.PortionDays)),
		tabs,
		hint(str.Vote.Portion.Title(strconv.Itoa(k), dayText(c.PortionOpens(k).Format(contest.DateLayout)))),
	}
	if k < total && k == open {
		next := str.Vote.Portion.Next(dayText(c.PortionOpens(k + 1).Format(contest.DateLayout)))
		if k < c.PortionsOpen(s.now(), total) {
			next = str.Vote.Portion.NextPreparing()
		}
		head = append(head, hint(next))
	}
	if len(qs) == 0 {
		return []ui.Item{ui.Section(head...), ui.Section(ui.Empty(text(str.Vote.Portion.Empty())))}, nil
	}
	head = append(head, answersToggle())
	form := []ui.Item{
		ui.Hiddenfield(ui.Name("portion"), ui.Value(strconv.Itoa(k)))}
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/%d/%d", c.Year, k, j.userID)) {
		form = append(form, likeCard(q, juryCard(q).withoutFactcheck(), liked[q.ID]))
	}
	return []ui.Item{ui.Section(head...), ui.Section(likesForm(c, form))}, nil
}

// likesForm is a page of likes: it saves itself on every change, and its
// button saves it without the script.
func likesForm(c contest.Contest, items []ui.Item) ui.Item {
	form := append([]ui.Item{ui.Method("post"), ui.Action(yearPath(c.Year, votePage)), ui.DirCol, ui.SpaceMD, ui.Data("autosave", "")}, items...)
	return ui.Form(append(form, ui.Row(ui.SpaceSM, ui.AlignCenter,
		ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save())), ui.Message(ui.ID("autosaveStatus"))))...)
}

// likedTab is the portion tab, after the last portion, with every question
// the juror liked, where they pick their favourite.
const likedTab = "liked"

// portionTabs are the first stage's tabs: every portion, those not open yet
// disabled with the day they open, then the questions the juror liked.
func (s *server) portionTabs(ctx context.Context, c contest.Contest, current string) (ui.Item, error) {
	total, err := portionCount(ctx, s.db, c)
	if err != nil {
		return nil, err
	}
	open, err := s.portionsOpen(ctx, s.db, c, total)
	if err != nil {
		return nil, err
	}
	dated := c.PortionsOpen(s.now(), total)
	tab := func(to, label string, icon ...ui.Item) ui.Item {
		look := ui.Ghost
		if to == current {
			look = ui.Primary
		}
		return ui.Button(append([]ui.Item{ui.Href(yearPath(c.Year, votePage) + "?portion=" + to), ui.Small(), look, text(label)}, icon...)...)
	}
	items := []ui.Item{ui.SpaceXS, ui.Wrap(), ui.Aria("label", str.Vote.Portion.Tabs())}
	for i := 1; i <= total; i++ {
		n := strconv.Itoa(i)
		if i > open {
			// A portion whose day has come waits for the admins to check it.
			title := str.Vote.Portion.Opens(dayText(c.PortionOpens(i).Format(contest.DateLayout)))
			if i <= dated {
				title = str.Vote.Portion.Preparing()
			}
			items = append(items, ui.Button(ui.Small(), ui.Ghost, ui.Disabled(), ui.Title(title), text(n)))
			continue
		}
		items = append(items, tab(n, n))
	}
	items = append(items, tab(likedTab, str.Vote.Liked.Tab(), ui.IconHeart))
	return ui.Row(items...), nil
}

func likeToggle(q store.Question, liked bool) ui.Item {
	items := []ui.Item{ui.Name("like"), ui.Value(idstr.Format(q.ID)), ui.IconHeart, text(str.Vote.Like())}
	if liked {
		items = append(items, ui.Checked())
	}
	return ui.Toggle(items...)
}

func likeCard(q store.Question, c card, liked bool) ui.Item {
	return ui.Card(ui.DirCol, ui.SpaceSM,
		ui.Richtext(ui.Raw(questionHTML(c))),
		ui.Row(likeToggle(q, liked)))
}

// likedQuestions are the questions the juror liked that they can still read,
// in QY order.
func likedQuestions(ctx context.Context, q store.Querier, c contest.Contest, j juror) ([]store.Question, error) {
	where := "status = 'approved' and qy is not null and id in (select question_id from likes where user_id = ?)"
	if j.kind == store.People {
		where += " and published = 1"
	}
	return store.Questions(ctx, q, c.Year, where, j.userID)
}

// favouriteOf is the question the juror picked as their favourite this year,
// 0 if none.
func favouriteOf(ctx context.Context, q store.Querier, year int, userID int64) (int64, error) {
	ids, err := store.Collect(ctx, q, store.Scalar[int64], `select l.question_id from likes l
join questions qq on qq.id = l.question_id and qq.year = ?
where l.user_id = ? and l.favourite = 1`, year, userID)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// likedBody is the liked tab: every question the juror liked, each with its
// like, and the choice of one favourite among them.
func (s *server) likedBody(ctx context.Context, c contest.Contest, j juror) ([]ui.Item, error) {
	tabs, err := s.portionTabs(ctx, c, likedTab)
	if err != nil {
		return nil, err
	}
	qs, err := likedQuestions(ctx, s.db, c, j)
	if err != nil {
		return nil, err
	}
	fav, err := favouriteOf(ctx, s.db, c.Year, j.userID)
	if err != nil {
		return nil, err
	}
	lead := []ui.Item{hint(str.Vote.Portion.Lead(c.PortionDays)), tabs, hint(str.Vote.Liked.Lead(tally.FavouritePoints))}
	if len(qs) == 0 {
		return []ui.Item{ui.Section(lead...), ui.Section(ui.Empty(text(str.Vote.Liked.Empty())))}, nil
	}
	head := ui.Section(append(lead, answersToggle())...)
	form := []ui.Item{
		ui.Hiddenfield(ui.Name("portion"), ui.Value(likedTab)),
		ui.Row(favouriteToggle("", str.Vote.Liked.NoFavourite(), fav == 0, nil))}
	for _, q := range qs {
		form = append(form, ui.Card(ui.DirCol, ui.SpaceSM,
			ui.Richtext(ui.Raw(questionHTML(juryCard(q).withoutFactcheck()))),
			ui.Row(ui.SpaceSM, ui.Wrap(), likeToggle(q, true),
				favouriteToggle(idstr.Format(q.ID), str.Vote.Liked.Favourite(), fav == q.ID, ui.IconStar))))
	}
	return []ui.Item{head, ui.Section(likesForm(c, form))}, nil
}

func favouriteToggle(value, label string, on bool, icon ui.Item) ui.Item {
	items := []ui.Item{ui.Name("favourite"), ui.Value(value), ui.Exclusive(), text(label)}
	if icon != nil {
		items = append(items, icon)
	}
	if on {
		items = append(items, ui.Checked())
	}
	return ui.Toggle(items...)
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
	var form []ui.Item
	shown := 0
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/factcheck/%d", c.Year, j.userID)) {
		if !liked[q.ID] {
			continue
		}
		shown++
		form = append(form, likeCard(q, juryCard(q), true))
	}
	if shown == 0 {
		return []ui.Item{head, ui.Section(ui.Empty(text(str.Vote.Factcheck.Nothing())))}, nil
	}
	return []ui.Item{head, ui.Section(likesForm(c, form))}, nil
}

// handleVote saves the likes on one page. What the page showed is the scope:
// a like on a question the juror could not see is ignored, and during the
// factcheck a like can only be taken back.
func (s *server) handleVote(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	c, j, err := s.yearJuror(r, sc)
	if err != nil {
		return err
	}
	if j.kind == "" {
		return route.Forbidden(str.Vote.Page.NotJuror())
	}
	posted := postedIDs(r, "like")
	stage := c.StageAt(s.now())
	liked := stage == contest.Prelim && r.FormValue("portion") == likedTab
	k, _ := strconv.Atoi(r.FormValue("portion"))
	fav, _ := idstr.Parse(r.FormValue("favourite"))
	err = s.withWriteTx(ctx, "vote", func(ctx context.Context, tx *sql.Tx) error {
		return s.saveVote(ctx, tx, c, j, voteForm{stage, k, liked, posted, fav})
	})
	if err != nil {
		return err
	}
	if wantsJSON(r) {
		return writeJSON(w, map[string]string{"status": str.Common.Saved()})
	}
	target := yearPath(c.Year, votePage) + "?done=" + doneSaved
	switch {
	case liked:
		target += "&portion=" + likedTab
	case k > 0:
		target += "&portion=" + strconv.Itoa(k)
	}
	return seeOther(w, r, target)
}

// voteForm is one posted page of likes: its stage, its portion (or the liked
// tab), the likes it left on, and the favourite on the liked tab.
type voteForm struct {
	stage  contest.Stage
	k      int
	liked  bool
	posted map[int64]bool
	fav    int64
}

func (s *server) saveVote(ctx context.Context, tx *sql.Tx, c contest.Contest, j juror, f voteForm) error {
	var scope []store.Question
	var err error
	if f.liked {
		scope, err = likedQuestions(ctx, tx, c, j)
	} else {
		scope, err = s.voteScope(ctx, tx, c, j, f.stage, f.k)
	}
	if err != nil {
		return err
	}
	for _, q := range scope {
		if err := s.setLike(ctx, tx, j.userID, q.ID, f.posted[q.ID], f.stage == contest.Prelim); err != nil {
			return err
		}
	}
	if !f.liked {
		return nil
	}
	return setFavourite(ctx, tx, c.Year, j.userID, f.fav)
}

// voteScope is what the page being saved showed the juror.
func (s *server) voteScope(ctx context.Context, tx *sql.Tx, c contest.Contest, j juror, stage contest.Stage, k int) ([]store.Question, error) {
	switch stage {
	case contest.Prelim:
		total, err := portionCount(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		open, err := s.portionsOpen(ctx, tx, c, total)
		if err != nil {
			return nil, err
		}
		if k < 1 || k > open {
			return nil, userError(str.Vote.Portion.NotOpen())
		}
		return portionQuestions(ctx, tx, c, j, k)
	case contest.Factcheck:
		return shortlistQuestions(ctx, tx, c)
	}
	return nil, userError(str.Vote.Page.Closed(dayText(c.PrelimStart), dayText(c.FactcheckEnd)))
}

// setFavourite makes fav the juror's one favourite of the year, or clears it
// when fav is 0. Only a question they still like can be it.
func setFavourite(ctx context.Context, tx *sql.Tx, year int, userID, fav int64) error {
	_, err := tx.ExecContext(ctx, `update likes set favourite = (question_id = ?)
where user_id = ? and question_id in (select id from questions where year = ?)`, fav, userID, year)
	return err
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
