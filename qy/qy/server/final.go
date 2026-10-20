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

// The final stage: the expert jury marks every shortlisted question from 1 to
// 10, and anybody logged in gives three of them 5, 4 and 3 points.

// maxScore is the top of the jury's scale.
const maxScore = 10

// finalOpen is the shortlist the final stage is about, or the reason it is
// not open.
func (s *server) finalOpen(ctx context.Context, c contest.Contest) ([]store.Question, string, error) {
	if c.StageAt(s.now()) != contest.Final {
		return nil, str.Final.Closed(dayText(c.FinalStart), dayText(c.FinalEnd)), nil
	}
	if c.ShortlistFrozenAt == "" {
		return nil, str.Final.NoShortlist(), nil
	}
	qs, err := shortlistQuestions(ctx, s.db, c)
	return qs, "", err
}

// openFinal is the current contest and its shortlist, for a form posted to
// the final stage; a closed stage is the poster's error.
func (s *server) openFinal(ctx context.Context) (contest.Contest, []store.Question, error) {
	c, err := s.currentContest(ctx)
	if err != nil {
		return c, nil, err
	}
	qs, closed, err := s.finalOpen(ctx, c)
	if err == nil && closed != "" {
		err = userError(closed)
	}
	return c, qs, err
}

// closedOr ends a final-stage page early: with the error, or with the reason
// the stage is not open.
func (s *server) closedOr(w http.ResponseWriter, r *http.Request, v view, closed string, err error) error {
	if err != nil {
		return err
	}
	v.body = []ui.Item{ui.Section(hint(closed))}
	return s.ok(w, r, v)
}

func (s *server) scoresOf(ctx context.Context, q store.Querier, userID int64) (map[int64]int, error) {
	return store.Counts(ctx, q, `select question_id, score from scores where user_id = ?`, userID)
}

func (s *server) handleJuryPage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	c, j, err := s.currentJuror(ctx, sc)
	if err != nil {
		return err
	}
	v := view{title: str.Final.Jury.Title(), trail: []crumb{{"", str.Final.Jury.Title()}}}
	if j.kind != store.Expert {
		v.body = []ui.Item{ui.Section(hint(str.Final.Jury.NotExpert()))}
		return s.ok(w, r, v)
	}
	qs, closed, err := s.finalOpen(ctx, c)
	if err != nil || closed != "" {
		return s.closedOr(w, r, v, closed, err)
	}
	scores, err := s.scoresOf(ctx, s.db, j.userID)
	if err != nil {
		return err
	}
	marked := 0
	form := []ui.Item{ui.Method("post"), ui.Action("/final"), ui.DirCol, ui.SpaceMD, ui.Data("autosave", "")}
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/final/%d", c.Year, j.userID)) {
		if scores[q.ID] > 0 {
			marked++
		}
		name := "score" + idstr.Format(q.ID)
		var radios []ui.Item
		for n := 1; n <= maxScore; n++ {
			items := []ui.Item{ui.Name(name), ui.Value(strconv.Itoa(n)), text(strconv.Itoa(n))}
			if scores[q.ID] == n {
				items = append(items, ui.Checked())
			}
			radios = append(radios, ui.Radio(items...))
		}
		form = append(form, ui.Card(ui.DirCol, ui.SpaceSM,
			ui.Richtext(ui.Raw(questionHTML(card{qy: q.QY, q: q.Clean, extra: q.ExtraContext, factcheck: q.Factcheck}))),
			ui.Row(append([]ui.Item{ui.SpaceSM, ui.Wrap(), ui.Aria("label", str.Final.Jury.Scale())}, radios...)...)))
	}
	form = append(form, ui.Row(ui.SpaceSM, ui.AlignCenter,
		ui.Button(ui.Submit(), ui.Primary, text(str.Common.Save())), ui.Message(ui.ID("autosaveStatus"))))
	v.body = []ui.Item{
		ui.Section(append([]ui.Item{hint(str.Final.Jury.Lead(dayText(c.FinalEnd))), hint(str.Final.Jury.Progress(strconv.Itoa(marked), len(qs)))}, noticeOf(r)...)...),
		ui.Section(ui.Form(form...)),
	}
	return s.ok(w, r, v)
}

func (s *server) handleJuryScores(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	_, j, err := s.currentJuror(ctx, sc)
	if err != nil {
		return err
	}
	if j.kind != store.Expert {
		return route.Forbidden(str.Final.Jury.NotExpert())
	}
	_, qs, err := s.openFinal(ctx)
	if err != nil {
		return err
	}
	err = s.withWriteTx(ctx, "jury-scores", func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range qs {
			n, _ := strconv.Atoi(r.FormValue("score" + idstr.Format(q.ID)))
			if err := s.setScore(ctx, tx, j.userID, q.ID, n); err != nil {
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
	return seeOther(w, r, "/final?done="+doneSaved)
}

// setScore writes one mark; anything off the scale clears it.
func (s *server) setScore(ctx context.Context, tx *sql.Tx, userID, questionID int64, n int) error {
	if n < 1 || n > maxScore {
		_, err := tx.ExecContext(ctx, `delete from scores where user_id = ? and question_id = ?`, userID, questionID)
		return err
	}
	_, err := tx.ExecContext(ctx, `
insert into scores(user_id, question_id, score, updated_at) values(?, ?, ?, ?)
on conflict(user_id, question_id) do update set score = excluded.score, updated_at = excluded.updated_at`,
		userID, questionID, n, s.stamp())
	return err
}

// ---- the popular vote ----

func (s *server) popularVoteOf(ctx context.Context, year int, userID int64) (tally.PopularVote, error) {
	var v tally.PopularVote
	err := s.db.QueryRowContext(ctx, `select first_id, second_id, third_id from popular_votes where year = ? and user_id = ?`, year, userID).
		Scan(&v[0], &v[1], &v[2])
	if err == sql.ErrNoRows {
		return v, nil
	}
	return v, err
}

func (s *server) handlePopularPage(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	ctx := r.Context()
	c, err := s.currentContest(ctx)
	if err != nil {
		return err
	}
	v := view{title: str.Final.Popular.Title(), trail: []crumb{{"", str.Final.Popular.Title()}}}
	qs, closed, err := s.finalOpen(ctx, c)
	if err != nil || closed != "" {
		return s.closedOr(w, r, v, closed, err)
	}
	vote, err := s.popularVoteOf(ctx, c.Year, sc.User.UserID)
	if err != nil {
		return err
	}
	rating, err := s.ratingID(ctx, sc.User.UserID)
	if err != nil {
		return err
	}
	labels := []string{str.Final.Popular.First(), str.Final.Popular.Second(), str.Final.Popular.Third()}
	form := []ui.Item{ui.Method("post"), ui.Action("/popular"), ui.DirCol, ui.SpaceSM,
		ui.Field(ui.Label(str.Common.RatingId()), ui.Textfield(ui.Name("rating_id"), ui.Value(rating), ui.Inputmode("numeric"), ui.Required()))}
	for i, label := range labels {
		opts := []ui.Item{ui.Name("pick" + strconv.Itoa(i)), option("", str.Final.Popular.Choose(), idText(vote[i]))}
		for _, q := range qs {
			opts = append(opts, option(idstr.Format(q.ID), qyLabel(q.QY), idText(vote[i])))
		}
		form = append(form, ui.Field(ui.Label(label), ui.Selectfield(opts...)))
	}
	form = append(form, ui.Row(ui.Button(ui.Submit(), ui.Primary, text(str.Final.Popular.Submit()))))
	var cards []ui.Item
	for _, q := range inReaderOrder(qs, fmt.Sprintf("%d/popular/%d", c.Year, sc.User.UserID)) {
		cards = append(cards, ui.Card(ui.Richtext(ui.Raw(questionHTML(card{qy: q.QY, q: q.Clean, extra: q.ExtraContext, factcheck: q.Factcheck})))))
	}
	lead := []ui.Item{hint(str.Final.Popular.Lead(dayText(c.FinalEnd)))}
	if vote[0] != 0 {
		lead = append(lead, hint(str.Final.Popular.Counted()))
	}
	v.body = []ui.Item{
		ui.Section(append(lead, noticeOf(r)...)...),
		ui.Section(ui.Form(form...)),
		ui.Section(cards...),
	}
	return s.ok(w, r, v)
}

func (s *server) handlePopularVote(w http.ResponseWriter, r *http.Request, sc route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	ctx := r.Context()
	c, qs, err := s.openFinal(ctx)
	if err != nil {
		return err
	}
	rating, err := parseRatingID(r.FormValue("rating_id"))
	if err != nil {
		return err
	}
	onList := map[int64]bool{}
	for _, q := range qs {
		onList[q.ID] = true
	}
	var vote tally.PopularVote
	seen := map[int64]bool{}
	for i := range vote {
		id, _ := idstr.Parse(r.FormValue("pick" + strconv.Itoa(i)))
		if !onList[id] || seen[id] {
			return userError(str.Final.Popular.Invalid())
		}
		vote[i], seen[id] = id, true
	}
	err = s.withWriteTx(ctx, "popular-vote", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update users set rating_id = ?, updated_at = ? where id = ?`, rating, s.stamp(), sc.User.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into popular_votes(year, user_id, first_id, second_id, third_id, updated_at) values(?, ?, ?, ?, ?, ?)
on conflict(year, user_id) do update set first_id = excluded.first_id, second_id = excluded.second_id,
  third_id = excluded.third_id, updated_at = excluded.updated_at`,
			c.Year, sc.User.UserID, vote[0], vote[1], vote[2], s.stamp())
		return err
	})
	if err != nil {
		return err
	}
	return seeOther(w, r, "/popular?done="+doneSaved)
}

// ---- the count ----

// finalTally is the shortlist's standing in both votes, counting only the
// expert ballots that mark every shortlisted question.
type finalTally struct {
	results  []tally.Result
	byID     map[int64]store.Question
	complete int
	experts  int
	voters   int
}

func (s *server) countFinal(ctx context.Context, c contest.Contest) (finalTally, error) {
	var t finalTally
	qs, err := shortlistQuestions(ctx, s.db, c)
	if err != nil {
		return t, err
	}
	t.byID = map[int64]store.Question{}
	qy := map[int64]int{}
	for _, q := range qs {
		t.byID[q.ID], qy[q.ID] = q, q.QY
	}
	rows, err := s.db.QueryContext(ctx, `
select u.id from jurors j join users u on j.telegram_username = u.telegram_username
where j.year = ? and j.kind = ?`, c.Year, store.Expert)
	if err != nil {
		return t, err
	}
	var experts []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return t, err
		}
		experts = append(experts, id)
	}
	rows.Close()
	t.experts = len(experts)
	var ballots []tally.Ballot
	for _, id := range experts {
		marks, err := s.scoresOf(ctx, s.db, id)
		if err != nil {
			return t, err
		}
		b := tally.Ballot{}
		for qid := range qy {
			if marks[qid] > 0 {
				b[qid] = marks[qid]
			}
		}
		if len(b) == len(qy) && len(qy) > 0 {
			ballots = append(ballots, b)
		}
	}
	t.complete = len(ballots)
	votes, err := s.popularVotes(ctx, c.Year, qy)
	if err != nil {
		return t, err
	}
	t.voters = len(votes)
	t.results = tally.Results(qy, ballots, votes)
	return t, nil
}

// popularVotes are the year's ballots whose three picks are all still on the
// shortlist.
func (s *server) popularVotes(ctx context.Context, year int, onList map[int64]int) ([]tally.PopularVote, error) {
	rows, err := s.db.QueryContext(ctx, `select first_id, second_id, third_id from popular_votes where year = ?`, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tally.PopularVote
	for rows.Next() {
		var v tally.PopularVote
		if err := rows.Scan(&v[0], &v[1], &v[2]); err != nil {
			return nil, err
		}
		if onList[v[0]] > 0 && onList[v[1]] > 0 && onList[v[2]] > 0 {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

func placesText(p tally.Places) string {
	if p.From == p.To {
		return strconv.Itoa(p.From)
	}
	return strconv.Itoa(p.From) + "–" + strconv.Itoa(p.To)
}

func rankSumText(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

func placesLine(r tally.Result) string {
	return str.Results.Places(placesText(r.Jury.Place), placesText(r.Popular.Place), rankSumText(r.Jury.RankSum), strconv.Itoa(r.Popular.Points))
}
