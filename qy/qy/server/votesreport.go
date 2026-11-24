package qyserver

import (
	"context"
	"database/sql"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
	"qy/qy/web/route"
	"qy/qy/web/ui"
)

// ---- how each juror voted ----

// The admins' page of every juror's votes, the expert jury and the people's
// alike: in the first stage the questions they liked and their favourite,
// in the final the marks they gave. A juror who has not logged in yet is
// there with nothing.

// jurorVotes is one juror and what they gave.
type jurorVotes struct {
	name, kind string
	anonymous  bool
	liked      []store.Question // in QY order
	favourite  int              // its QY, 0 for none
	scores     []jurorScore     // in QY order
}

type jurorScore struct {
	q     store.Question
	score int
}

func (s *server) handleVotesReport(w http.ResponseWriter, r *http.Request, _ route.Scope) error {
	c, err := s.pathContest(r)
	if err != nil {
		return err
	}
	jurors, err := s.jurorVotes(r.Context(), c)
	if err != nil {
		return err
	}
	base := contestHref(c.Year)
	y := strconv.Itoa(c.Year)
	body := []ui.Item{ui.Section(hint(str.Admin.Votes.Lead()))}
	if len(jurors) == 0 {
		body = append(body, ui.Section(hint(str.Admin.Votes.NoJurors())))
	} else {
		body = append(body, ui.Section(subhead(str.Admin.Votes.Stage1()), ui.Table(likesRows(c, jurors)...)))
		if scored := scoreRows(c, jurors); scored != nil {
			body = append(body, ui.Section(subhead(str.Admin.Votes.Final()), ui.Table(scored...)))
		}
	}
	return s.ok(w, r, view{
		title: str.Admin.Votes.Title(),
		trail: adminTrail(crumb{base, str.Admin.Contests.Row(y)}, crumb{"", str.Admin.Votes.Title()}),
		body:  body,
	})
}

func jurorCell(j jurorVotes) ui.Item {
	kind := str.Admin.Votes.Expert()
	if j.kind == store.People {
		kind = str.Admin.Votes.People()
	}
	items := []ui.Item{ui.SpaceXS, ui.Strong(text(j.name)), hint(kind)}
	if j.anonymous {
		items = append(items, hint(str.Admin.Votes.Anonymous()))
	}
	return ui.Cell(ui.Col(items...))
}

// qyLink is a question as an inline link: its number and its answer.
func qyLink(q store.Question, label string) string {
	return `<a href="` + html.EscapeString(questionHref(q.ID)) + `">` + html.EscapeString(label) + `</a>`
}

// qyName is a question's number with its answer, the first line of it.
func qyName(q store.Question) string {
	answer, _, _ := strings.Cut(strings.TrimSpace(q.Clean.Answer), "\n")
	return str.Admin.Votes.Question(qyLabel(q.QY), strings.TrimRight(answer, ". "))
}

// byPortion is the links, a line per portion in order, each line headed
// by its portion.
func byPortion[T any](c contest.Contest, items []T, qy func(T) int, link func(T) string) ui.Item {
	var lines []string
	last := 0
	for _, it := range items {
		if k := c.Portion(qy(it)); k != last {
			last = k
			lines = append(lines, "<strong>"+html.EscapeString(str.Admin.Votes.Portion(strconv.Itoa(k)))+"</strong> "+link(it))
			continue
		}
		lines[len(lines)-1] += ", " + link(it)
	}
	if len(lines) == 0 {
		return ui.Cell(text(""))
	}
	return ui.Cell(ui.Richtext(ui.Raw("<p>" + strings.Join(lines, "</p><p>") + "</p>")))
}

func likesRows(c contest.Contest, jurors []jurorVotes) []ui.Item {
	rows := []ui.Item{ui.Scroll(), headRow(str.Admin.Votes.Juror(), str.Admin.Votes.Likes(), str.Admin.Votes.Favourite(), str.Admin.Votes.Liked())}
	for _, j := range jurors {
		fav := ui.Cell(text(""))
		for _, q := range j.liked {
			if q.QY == j.favourite {
				fav = ui.Cell(ui.Richtext(ui.Raw(qyLink(q, qyName(q)))))
			}
		}
		liked := byPortion(c, j.liked, func(q store.Question) int { return q.QY }, func(q store.Question) string { return qyLink(q, qyName(q)) })
		rows = append(rows, ui.Trow(jurorCell(j), ui.Cell(text(strconv.Itoa(len(j.liked)))), fav, liked))
	}
	return rows
}

// scoreRows are the final's marks, juror by juror; nil while nobody has
// given one.
func scoreRows(c contest.Contest, jurors []jurorVotes) []ui.Item {
	rows := []ui.Item{ui.Scroll(), headRow(str.Admin.Votes.Juror(), str.Admin.Votes.Scored(), str.Admin.Votes.Scores())}
	marked := false
	for _, j := range jurors {
		if j.kind != store.Expert {
			continue
		}
		marked = marked || len(j.scores) > 0
		marks := byPortion(c, j.scores, func(sc jurorScore) int { return sc.q.QY }, func(sc jurorScore) string {
			return qyLink(sc.q, str.Admin.Votes.Score(qyName(sc.q), strconv.Itoa(sc.score)))
		})
		rows = append(rows, ui.Trow(jurorCell(j), ui.Cell(text(strconv.Itoa(len(j.scores)))), marks))
	}
	if !marked {
		return nil
	}
	return rows
}

// jurorVotes are the year's jurors, the experts first, each with what they
// liked and marked.
func (s *server) jurorVotes(ctx context.Context, c contest.Contest) ([]jurorVotes, error) {
	qs, err := store.Questions(ctx, s.db, c.Year, "qy is not null")
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.Question, len(qs))
	for _, q := range qs {
		byID[q.ID] = q
	}
	rows, err := s.db.QueryContext(ctx, `
select j.telegram_username, j.name, j.kind, j.anonymous, coalesce(u.id, 0)
from jurors j left join users u on `+store.JurorIsUser+`
where j.year = ?
order by j.kind = 'people', j.name collate nocase`, c.Year)
	if err != nil {
		return nil, err
	}
	var out []jurorVotes
	users := map[string][]int64{}
	index := map[string]int{}
	for rows.Next() {
		var key string
		var j jurorVotes
		var user int64
		if err := rows.Scan(&key, &j.name, &j.kind, &j.anonymous, &user); err != nil {
			rows.Close()
			return nil, err
		}
		if _, seen := index[key]; !seen {
			index[key] = len(out)
			out = append(out, j)
		}
		if user != 0 {
			users[key] = append(users[key], user)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for key, ids := range users {
		j := &out[index[key]]
		for _, id := range ids {
			if err := s.addVotes(ctx, j, id, byID); err != nil {
				return nil, err
			}
		}
		slices.SortFunc(j.liked, func(a, b store.Question) int { return a.QY - b.QY })
		slices.SortFunc(j.scores, func(a, b jurorScore) int { return a.q.QY - b.q.QY })
	}
	return out, nil
}

// addVotes adds user's likes and marks on the year's numbered questions to j.
func (s *server) addVotes(ctx context.Context, j *jurorVotes, user int64, byID map[int64]store.Question) error {
	err := scanRows(ctx, s.db, `select question_id, favourite from likes where user_id = ?`, []any{user}, func(rows *sql.Rows) error {
		var id int64
		var fav bool
		if err := rows.Scan(&id, &fav); err != nil {
			return err
		}
		if q, ok := byID[id]; ok {
			j.liked = append(j.liked, q)
			if fav {
				j.favourite = q.QY
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return scanRows(ctx, s.db, `select question_id, score from scores where user_id = ?`, []any{user}, func(rows *sql.Rows) error {
		var id int64
		var score int
		if err := rows.Scan(&id, &score); err != nil {
			return err
		}
		if q, ok := byID[id]; ok {
			j.scores = append(j.scores, jurorScore{q, score})
		}
		return nil
	})
}

// scanRows runs query and hands each row to each.
func scanRows(ctx context.Context, q store.Querier, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
