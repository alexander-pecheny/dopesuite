package qyserver

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"qy/qy/domain/contest"
	"qy/qy/domain/eligibility"
	"qy/qy/domain/question"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// The automatic check of rule 2 on a nominated question (domain/eligibility).
// It warns the nominator and never refuses a save; one who disagrees writes
// an appeal, which the admins read on the question's page.

// lintResult is what the check found for one question: unchecked says the
// rating site could not be asked, so the date rules were not checked.
type lintResult struct {
	problems  []eligibility.Problem
	unchecked bool
}

// barred are the people who may not have written or edited a nominated
// question: the year's jurors of both kinds and the admins.
func (s *server) barred(ctx context.Context, year int) ([]eligibility.Person, error) {
	rows, err := s.db.QueryContext(ctx, `
select name, 0 from jurors where year = ?
union all
select coalesce(u.telegram_name, ''), coalesce(u.rating_id, 0)
from admins a join users u on u.telegram_username = a.telegram_username collate nocase`, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []eligibility.Person
	for rows.Next() {
		var p eligibility.Person
		if err := rows.Scan(&p.Name, &p.RatingID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func lintWindow(c contest.Contest) eligibility.Window {
	return eligibility.Window{From: c.QuestionsFrom, To: c.QuestionsTo, Shortlist: c.FinalStart}
}

// lint checks one question by its tournament's link and its authors.
func (s *server) lint(ctx context.Context, c contest.Contest, barred []eligibility.Person, tournamentLink, authors string) lintResult {
	var t eligibility.Tournament
	res := lintResult{}
	if id := question.FindLinks(tournamentLink).TournamentID; id != 0 {
		got, err := s.rating.Tournament(ctx, id)
		if err != nil {
			res.unchecked = true
		} else {
			t.Start, t.End = got.Start, got.End
			for _, e := range got.Editors {
				t.Editors = append(t.Editors, eligibility.Person{Name: e.Name, RatingID: e.ID})
			}
		}
	} else if tournamentLink != "" {
		res.unchecked = true
	}
	res.problems = eligibility.Check(lintWindow(c), t, authors, barred)
	return res
}

// lintSlots checks every filled slot of a form.
func (s *server) lintSlots(ctx context.Context, c contest.Contest, slots map[int]slotView) error {
	barred, err := s.barred(ctx, c.Year)
	if err != nil {
		return err
	}
	for slot, v := range slots {
		if v.in.empty() {
			continue
		}
		v.lint = s.lint(ctx, c, barred, v.in.tournament, v.in.fields.Author)
		slots[slot] = v
	}
	return nil
}

func problemText(c contest.Contest, p eligibility.Problem) string {
	switch p.Kind {
	case eligibility.DatesOff:
		return str.Nominate.Lint.DatesOff(dayText(p.Date), dayText(c.QuestionsFrom), dayText(c.QuestionsTo))
	case eligibility.StillPlayable:
		return str.Nominate.Lint.StillPlayable(dayText(p.Date), dayText(c.FinalStart))
	case eligibility.AuthorBarred:
		return str.Nominate.Lint.AuthorBarred(p.Who)
	}
	return str.Nominate.Lint.EditorBarred(p.Who)
}

func (r lintResult) texts(c contest.Contest) []string {
	var out []string
	for _, p := range r.problems {
		out = append(out, problemText(c, p))
	}
	return out
}

// lintBox is the red box under a slot that fails a rule, with the appeal;
// nominate.ts refills it as the slot changes, so it is there, hidden, for
// every slot.
func lintBox(c contest.Contest, slot int, v slotView) ui.Item {
	texts := v.lint.texts(c)
	list := []ui.Item{ui.SpaceXS, ui.Data("lint-list", "")}
	for _, t := range texts {
		list = append(list, ui.Paragraph(text(t)))
	}
	box := []ui.Item{ui.SpaceSM, ui.Data("lint", ""),
		ui.Strong(text(str.Nominate.Lint.Title())),
		ui.Col(list...),
		hint(str.Nominate.Lint.WhatNow()),
		ui.Field(ui.Label(str.Nominate.Lint.Appeal()),
			ui.Editor(ui.Name(slotKey(slot, "appeal")), ui.Rows("1"), ui.Data("autogrow", ""), ui.Text(v.in.appeal)))}
	if len(texts) == 0 {
		box = append(box, ui.Hidden())
	}
	unchecked := []ui.Item{ui.Data("lint-unchecked", ""), ui.Text(str.Nominate.Lint.Unchecked())}
	if !v.lint.unchecked {
		unchecked = append(unchecked, ui.Hidden())
	}
	return ui.Col(ui.SpaceSM, ui.Col(box...), ui.Hint(unchecked...))
}

// handleCheckQuestion is the check when the page's script runs: a slot's
// tournament link and authors in, what is wrong out.
func (s *server) handleCheckQuestion(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	var req struct {
		Tournament string `json:"tournament"`
		Author     string `json:"author"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	c, err := s.currentContest(r.Context())
	if err != nil {
		return err
	}
	barred, err := s.barred(r.Context(), c.Year)
	if err != nil {
		return err
	}
	res := s.lint(r.Context(), c, barred, strings.TrimSpace(req.Tournament), strings.TrimSpace(req.Author))
	problems := res.texts(c)
	if problems == nil {
		problems = []string{}
	}
	return writeJSON(w, map[string]any{"problems": problems, "unchecked": res.unchecked})
}

// saveAppeals writes what each slot's nominator said against the check.
func saveAppeals(ctx context.Context, tx *sql.Tx, year int, userID int64, slots map[int]slotView) error {
	for slot, v := range slots {
		if _, err := tx.ExecContext(ctx, `update nominations set appeal = ? where year = ? and user_id = ? and slot = ?`,
			v.in.appeal, year, userID, slot); err != nil {
			return err
		}
	}
	return nil
}

// rulesSection is the check on the admin's page of a question, with what
// its nominators said against it.
func (s *server) rulesSection(ctx context.Context, c contest.Contest, q store.Question, noms []nominator) (ui.Item, error) {
	barred, err := s.barred(ctx, c.Year)
	if err != nil {
		return nil, err
	}
	link := q.TournamentLink
	if question.FindLinks(link).TournamentID == 0 && q.TournamentID != 0 {
		link = tournamentLink(q.TournamentID)
	}
	res := s.lint(ctx, c, barred, link, q.Orig.Author)
	items := []ui.Item{subhead(str.Admin.Question.Rules())}
	for _, t := range res.texts(c) {
		items = append(items, dangerHint(t))
	}
	if res.unchecked {
		items = append(items, hint(str.Admin.Question.RulesUnchecked()))
	} else if len(res.problems) == 0 {
		items = append(items, hint(str.Admin.Question.RulesOk()))
	}
	for _, n := range noms {
		if n.appeal != "" {
			items = append(items, hint(str.Admin.Question.Appeal(n.telegram, n.appeal)))
		}
	}
	return ui.Section(items...), nil
}
