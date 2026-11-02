package store

import (
	"context"
	"database/sql"
	"errors"

	"qy/qy/domain/anon"
	"qy/qy/domain/question"
)

// Question statuses.
const (
	StatusNew      = "new"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Background pass statuses.
const (
	AIPending = "pending"
	AIDone    = "done"
	AIFailed  = "failed"
)

type Question struct {
	ID, Year        int64
	QY              int
	Status          string
	MergedInto      int64
	GQID            int64
	Link            string
	Published       bool
	SourceText      string
	Orig, Clean     question.Question
	TournamentID    int64
	TournamentLink  string
	TournamentTitle string
	TournamentStart string
	TournamentEnd   string
	Factcheck       string
	ExtraContext    string
	AuthorDeclined  bool
	Pin             string
	AIStatus        string
	AIError         string
}

const questionColumns = `id, year, coalesce(qy, 0), status, coalesce(merged_into, 0), coalesce(gq_id, 0), link,
  published, source_text, orig, clean, coalesce(tournament_id, 0), tournament_title, tournament_start,
  tournament_end, factcheck, extra_context, author_declined, coalesce(shortlist_pin, ''), ai_status, ai_error, tournament_link`

func scanQuestion(row interface{ Scan(...any) error }) (Question, error) {
	var q Question
	var orig, clean string
	err := row.Scan(&q.ID, &q.Year, &q.QY, &q.Status, &q.MergedInto, &q.GQID, &q.Link,
		&q.Published, &q.SourceText, &orig, &clean, &q.TournamentID, &q.TournamentTitle, &q.TournamentStart,
		&q.TournamentEnd, &q.Factcheck, &q.ExtraContext, &q.AuthorDeclined, &q.Pin, &q.AIStatus, &q.AIError, &q.TournamentLink)
	q.Orig, q.Clean = question.FromJSON(orig), question.FromJSON(clean)
	return q, err
}

var ErrNoQuestion = errors.New("no such question")

func GetQuestion(ctx context.Context, q Querier, id int64) (Question, error) {
	out, err := scanQuestion(q.QueryRowContext(ctx, `select `+questionColumns+` from questions where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNoQuestion
	}
	return out, err
}

// Questions lists a year's questions that match where (an SQL condition over
// the questions table, "1" for all), in QY order and then in order of arrival.
func Questions(ctx context.Context, q Querier, year int, where string, args ...any) ([]Question, error) {
	return Collect(ctx, q, func(r *sql.Rows) (Question, error) { return scanQuestion(r) },
		`select `+questionColumns+` from questions where year = ? and (`+where+`) order by qy is null, qy, id`,
		append([]any{year}, args...)...)
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func InsertQuestion(ctx context.Context, q Querier, qq Question, now string) (int64, error) {
	res, err := q.ExecContext(ctx, `
insert into questions(year, gq_id, link, published, source_text, orig, clean, tournament_id, tournament_title,
  tournament_link, ai_status, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		qq.Year, nullInt(qq.GQID), qq.Link, qq.Published, qq.SourceText, qq.Orig.JSON(), qq.Clean.JSON(),
		nullInt(qq.TournamentID), qq.TournamentTitle, qq.TournamentLink, AIPending, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveQuestion writes back everything an admin can edit on the review page.
func SaveQuestion(ctx context.Context, q Querier, qq Question, now string) error {
	_, err := q.ExecContext(ctx, `
update questions set status = ?, merged_into = ?, link = ?, published = ?, orig = ?, clean = ?,
  tournament_id = ?, tournament_title = ?, tournament_start = ?, tournament_end = ?, factcheck = ?,
  extra_context = ?, author_declined = ?, shortlist_pin = ?, ai_status = ?, ai_error = ?, updated_at = ?
where id = ?`,
		qq.Status, nullInt(qq.MergedInto), qq.Link, qq.Published, qq.Orig.JSON(), qq.Clean.JSON(),
		nullInt(qq.TournamentID), qq.TournamentTitle, qq.TournamentStart, qq.TournamentEnd, qq.Factcheck,
		qq.ExtraContext, qq.AuthorDeclined, nullStr(qq.Pin), qq.AIStatus, qq.AIError, now, qq.ID)
	return err
}

type Suggestion struct {
	ID     int64
	Source string
	Status string
	anon.Suggestion
}

// Suggestion sources.
const (
	FromRule = "rule"
	FromAI   = "ai"
)

// Suggestion statuses.
const (
	SuggestionPending  = "pending"
	SuggestionAccepted = "accepted"
	SuggestionRejected = "rejected"
)

func Suggestions(ctx context.Context, q Querier, questionID int64) ([]Suggestion, error) {
	return Collect(ctx, q, func(r *sql.Rows) (Suggestion, error) {
		var s Suggestion
		err := r.Scan(&s.ID, &s.Source, &s.Status, &s.Field, &s.Quote, &s.Replacement, &s.Reason)
		return s, err
	}, `select id, source, status, field, quote, replacement, reason from suggestions where question_id = ? order by id`, questionID)
}

// AddSuggestions records new proposals, passing over any the question already
// has for the same field and quote.
func AddSuggestions(ctx context.Context, q Querier, questionID int64, source string, list []anon.Suggestion, now string) error {
	for _, s := range list {
		_, err := q.ExecContext(ctx, `
insert into suggestions(question_id, field, quote, replacement, reason, source, created_at)
select ?, ?, ?, ?, ?, ?, ? where not exists (
  select 1 from suggestions where question_id = ? and field = ? and quote = ?)`,
			questionID, s.Field, s.Quote, s.Replacement, s.Reason, source, now, questionID, s.Field, s.Quote)
		if err != nil {
			return err
		}
	}
	return nil
}

// File is a handout file attached to a question, without its bytes.
type File struct {
	ID          int64
	QuestionID  int64
	Name        string
	ContentType string
}

// Files lists a question's handout files.
func Files(ctx context.Context, q Querier, questionID int64) ([]File, error) {
	return Collect(ctx, q, func(r *sql.Rows) (File, error) {
		var f File
		err := r.Scan(&f.ID, &f.QuestionID, &f.Name, &f.ContentType)
		return f, err
	}, `select id, question_id, name, content_type from question_files where question_id = ? order by id`, questionID)
}

// AddFile attaches f to the question f.QuestionID names.
func AddFile(ctx context.Context, q Querier, f File, data []byte, now string) error {
	_, err := q.ExecContext(ctx, `insert into question_files(question_id, name, content_type, data, created_at) values(?, ?, ?, ?, ?)`,
		f.QuestionID, f.Name, f.ContentType, data, now)
	return err
}

// CopyFiles gives question to the files question from has.
func CopyFiles(ctx context.Context, q Querier, from, to int64) error {
	_, err := q.ExecContext(ctx, `
insert into question_files(question_id, name, content_type, data, created_at)
select ?, name, content_type, data, created_at from question_files where question_id = ? order by id`, to, from)
	return err
}

var ErrNoFile = errors.New("no such file")

// FileData is one file with its bytes.
func FileData(ctx context.Context, q Querier, id int64) (File, []byte, error) {
	var f File
	var data []byte
	err := q.QueryRowContext(ctx, `select id, question_id, name, content_type, data from question_files where id = ?`, id).
		Scan(&f.ID, &f.QuestionID, &f.Name, &f.ContentType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil, ErrNoFile
	}
	return f, data, err
}
