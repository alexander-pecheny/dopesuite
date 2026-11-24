package qyserver

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// ---- the portions, as the admins read them before the jury ----

// The admins read each portion of the first stage as the jury will, fix
// what is off in place, and mark the portion checked: that approves its
// questions and lets the jury read it once its day has come
// (portionsOpen). A question rejected after the deal leaves a gap in its
// portion.

// portionPath is the admins' page of portion k.
func portionPath(year, k int) string {
	return contestHref(year) + "/portions?portion=" + strconv.Itoa(k)
}

// adminPortion is portion k's questions as numbered, rejected ones too: the
// admin sees what the jury will not.
func adminPortion(ctx context.Context, q store.Querier, c contest.Contest, k int) ([]store.Question, error) {
	from, to := (k-1)*c.PortionSize+1, k*c.PortionSize
	return store.Questions(ctx, q, c.Year, "qy between ? and ?", from, to)
}

func (s *server) handlePortions(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	total, err := portionCount(ctx, s.db, c)
	if err != nil {
		return err
	}
	y := strconv.Itoa(c.Year)
	v := view{
		title: str.Admin.Portions.Title(),
		trail: adminTrail(crumb{contestHref(c.Year), str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Portions.Title()}),
	}
	if total == 0 {
		v.body = []ui.Item{ui.Section(hint(str.Admin.Portions.NotDealt()))}
		return s.ok(w, r, v)
	}
	k, err := strconv.Atoi(r.URL.Query().Get("portion"))
	if err != nil || k < 1 || k > total {
		k = 1
	}
	checked, err := checkedPortions(ctx, s.db, c.Year)
	if err != nil {
		return err
	}
	qs, err := adminPortion(ctx, s.db, c, k)
	if err != nil {
		return err
	}
	head := []ui.Item{portionAdminTabs(c, total, k, checked)}
	head = append(head, noticeOf(r)...)
	head = append(head, hint(str.Admin.Portions.About(strconv.Itoa(k), dayText(c.PortionOpens(k).Format(contest.DateLayout)))))
	items := []ui.Item{ui.Section(head...)}
	for _, q := range qs {
		items = append(items, portionQuestion(c, k, q))
	}
	v.body = append(items, ui.Section(portionCheck(c, k, qs, checked[k])))
	return s.ok(w, r, v)
}

func portionAdminTabs(c contest.Contest, total, current int, checked map[int]bool) ui.Item {
	items := []ui.Item{ui.SpaceXS, ui.Wrap(), ui.Aria("label", str.Vote.Portion.Tabs())}
	for i := 1; i <= total; i++ {
		look := ui.Ghost
		if i == current {
			look = ui.Primary
		}
		tab := []ui.Item{ui.Href(portionPath(c.Year, i)), ui.Small(), look, text(strconv.Itoa(i))}
		if checked[i] {
			tab = append(tab, ui.IconCheck, ui.Title(str.Admin.Portions.CheckedTab()))
		}
		items = append(items, ui.Button(tab...))
	}
	return ui.Row(items...)
}

// portionQuestion is one question as the jury will read it, with what the
// admin needs beside it, and the fields to fix it in place under the
// disclosure that opens them.
func portionQuestion(c contest.Contest, k int, q store.Question) ui.Item {
	badges := []ui.Item{ui.SpaceSM, ui.AlignCenter, ui.Wrap(),
		ui.Button(ui.Href(questionHref(q.ID)), ui.Ghost, ui.Small(), ui.IconLink, text(str.Admin.Portions.Open(qyLabel(q.QY)))),
		publishedBadge(q)}
	if q.Status == store.StatusRejected {
		badges = append(badges, ui.Badge(ui.BadgeNegative, text(str.Admin.Portions.Rejected())))
	}
	edit := ui.Form(ui.Method("post"), ui.Action(questionHref(q.ID)+"/jury-copy"), ui.DirCol, ui.SpaceSM,
		ui.Hiddenfield(ui.Name("portion"), ui.Value(strconv.Itoa(k))),
		questionEditor(q, viewFields),
		ui.Row(ui.Button(ui.Primary, ui.Submit(), text(str.Common.Save()))))
	// The card's frame carries the anchor (#QY12) the save comes back to.
	return ui.Section(
		ui.Card(ui.DirCol, ui.SpaceSM,
			ui.Row(badges...),
			ui.Richtext(ui.Raw(questionHTML(juryCard(q).withoutFactcheck()))),
			ui.Details(ui.Summary(text(str.Admin.Portions.Edit())), edit)))
}

// portionCheck is the button that marks the portion read, or the note that
// it is, with the way back.
func portionCheck(c contest.Contest, k int, qs []store.Question, checked bool) ui.Item {
	action := contestHref(c.Year) + "/portions/" + strconv.Itoa(k)
	if checked {
		return ui.Col(ui.SpaceSM, hint(str.Admin.Portions.IsChecked()),
			ui.Form(ui.Method("post"), ui.Action(action+"/uncheck"),
				ui.Button(ui.Submit(), ui.Ghost, text(str.Admin.Portions.Uncheck()))))
	}
	n := 0
	for _, q := range qs {
		if q.Status != store.StatusRejected {
			n++
		}
	}
	return ui.Form(ui.Method("post"), ui.Action(action+"/check"), ui.Data("confirm", str.Admin.Portions.Confirm(n)),
		ui.Button(ui.Submit(), ui.Primary, ui.IconCheck, text(str.Admin.Portions.Check())))
}

func pathPortion(r *http.Request, c contest.Contest, total int) (int, error) {
	k, err := strconv.Atoi(r.PathValue("k"))
	if err != nil || k < 1 || k > total {
		return 0, route.NotFound(str.Admin.Portions.Missing())
	}
	return k, nil
}

// handleCheckPortion approves the portion's questions, all but the rejected,
// and marks it checked.
func (s *server) handleCheckPortion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	err = s.withWriteTx(r.Context(), "check-portion", func(ctx context.Context, tx *sql.Tx) error {
		total, err := portionCount(ctx, tx, c)
		if err != nil {
			return err
		}
		k, err := pathPortion(r, c, total)
		if err != nil {
			return err
		}
		qs, err := adminPortion(ctx, tx, c, k)
		if err != nil {
			return err
		}
		for _, q := range qs {
			if q.Status == store.StatusRejected || q.Status == store.StatusApproved {
				continue
			}
			if q.Clean.Empty() {
				return userError(str.Admin.Portions.Empty(qyLabel(q.QY)))
			}
			q.Status = store.StatusApproved
			if err := store.SaveQuestion(ctx, tx, q, s.stamp()); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `insert or ignore into portion_checks(year, portion, checked_at) values(?, ?, ?)`, c.Year, k, s.stamp())
		return err
	})
	if err != nil {
		return err
	}
	k, _ := strconv.Atoi(r.PathValue("k"))
	return seeOther(w, r, portionPath(c.Year, k)+"&done="+doneSaved)
}

// handleUncheckPortion takes the mark back, so the jury no longer reads the
// portion; its questions stay approved.
func (s *server) handleUncheckPortion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	k, err := strconv.Atoi(r.PathValue("k"))
	if err != nil {
		return route.NotFound(str.Admin.Portions.Missing())
	}
	if err := s.withWriteTx(r.Context(), "uncheck-portion", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from portion_checks where year = ? and portion = ?`, c.Year, k)
		return err
	}); err != nil {
		return err
	}
	return seeOther(w, r, portionPath(c.Year, k)+"&done="+doneSaved)
}

// handleSaveJuryCopy saves the jury's copy from a portion's page and goes
// back to the question there.
func (s *server) handleSaveJuryCopy(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	q, c, err := s.pathQuestion(r)
	if err != nil {
		return err
	}
	edited := editedQuestion(r)
	q.Orig.Author, edited.Author = edited.Author, q.Clean.Author
	if q.Status == store.StatusApproved && edited.Empty() {
		return userError(str.Admin.Question.ApproveEmpty())
	}
	q.Clean = edited
	if err := s.withWriteTx(r.Context(), "save-jury-copy", func(ctx context.Context, tx *sql.Tx) error {
		return store.SaveQuestion(ctx, tx, q, s.stamp())
	}); err != nil {
		return err
	}
	k, err := strconv.Atoi(r.FormValue("portion"))
	if err != nil || k < 1 {
		k = c.Portion(q.QY)
	}
	return seeOther(w, r, portionPath(c.Year, k)+"&done="+doneSaved+"#"+qyLabel(q.QY))
}
