package store

import (
	"context"
	"database/sql"
	"errors"

	"qy/qy/domain/contest"
)

// Querier is what both *sql.DB and *sql.Tx offer, so a read can run inside a
// write transaction or outside one.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const contestColumns = `year, questions_from, questions_to, nomination_start, nomination_end,
  prelim_start, prelim_end, factcheck_start, factcheck_end, final_start, final_end, results_date,
  main_slots, reserve_slots, portion_size, portion_days, expert_cutoff, people_cutoff,
  coalesce(numbered_at, ''), coalesce(shortlist_frozen_at, ''), coalesce(results_published_at, '')`

func scanContest(row interface{ Scan(...any) error }) (contest.Contest, error) {
	var c contest.Contest
	d := &c.Dates
	err := row.Scan(&c.Year, &d.QuestionsFrom, &d.QuestionsTo, &d.NominationStart, &d.NominationEnd,
		&d.PrelimStart, &d.PrelimEnd, &d.FactcheckStart, &d.FactcheckEnd, &d.FinalStart, &d.FinalEnd, &d.ResultsDate,
		&c.MainSlots, &c.ReserveSlots, &c.PortionSize, &c.PortionDays, &c.ExpertCutoff, &c.PeopleCutoff,
		&c.NumberedAt, &c.ShortlistFrozenAt, &c.ResultsPublishedAt)
	return c, err
}

// ErrNoContest is the answer when no contest has been set up.
var ErrNoContest = errors.New("no such contest")

func Contest(ctx context.Context, q Querier, year int) (contest.Contest, error) {
	c, err := scanContest(q.QueryRowContext(ctx, `select `+contestColumns+` from contests where year = ?`, year))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoContest
	}
	return c, err
}

// Current is the latest contest, the one the site's forms are about.
func Current(ctx context.Context, q Querier) (contest.Contest, error) {
	c, err := scanContest(q.QueryRowContext(ctx, `select `+contestColumns+` from contests order by year desc limit 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoContest
	}
	return c, err
}

func Contests(ctx context.Context, q Querier) ([]contest.Contest, error) {
	return Collect(ctx, q, func(r *sql.Rows) (contest.Contest, error) { return scanContest(r) },
		`select `+contestColumns+` from contests order by year desc`)
}

// SaveContest writes the settings an admin edits. The three moments are
// steps of their own and are not touched here.
func SaveContest(ctx context.Context, q Querier, c contest.Contest) error {
	d := c.Dates
	_, err := q.ExecContext(ctx, `
insert into contests(year, questions_from, questions_to, nomination_start, nomination_end,
  prelim_start, prelim_end, factcheck_start, factcheck_end, final_start, final_end, results_date,
  main_slots, reserve_slots, portion_size, portion_days, expert_cutoff, people_cutoff)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(year) do update set questions_from = excluded.questions_from, questions_to = excluded.questions_to,
  nomination_start = excluded.nomination_start, nomination_end = excluded.nomination_end,
  prelim_start = excluded.prelim_start, prelim_end = excluded.prelim_end,
  factcheck_start = excluded.factcheck_start, factcheck_end = excluded.factcheck_end,
  final_start = excluded.final_start, final_end = excluded.final_end, results_date = excluded.results_date,
  main_slots = excluded.main_slots, reserve_slots = excluded.reserve_slots,
  portion_size = excluded.portion_size, portion_days = excluded.portion_days,
  expert_cutoff = excluded.expert_cutoff, people_cutoff = excluded.people_cutoff`,
		c.Year, d.QuestionsFrom, d.QuestionsTo, d.NominationStart, d.NominationEnd,
		d.PrelimStart, d.PrelimEnd, d.FactcheckStart, d.FactcheckEnd, d.FinalStart, d.FinalEnd, d.ResultsDate,
		c.MainSlots, c.ReserveSlots, c.PortionSize, c.PortionDays, c.ExpertCutoff, c.PeopleCutoff)
	return err
}

// Juror kinds.
const (
	Expert = "expert"
	People = "people"
)

// JurorKind is what jury, if any, the telegram handle sits on in year.
func JurorKind(ctx context.Context, q Querier, year int, telegram string) (string, error) {
	if telegram == "" {
		return "", nil
	}
	var kind string
	err := q.QueryRowContext(ctx, `select kind from jurors where year = ? and telegram_username = ?`, year, telegram).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

type Juror struct {
	Telegram, Kind, Name string
}

func Jurors(ctx context.Context, q Querier, year int) ([]Juror, error) {
	return Collect(ctx, q, func(r *sql.Rows) (Juror, error) {
		var j Juror
		err := r.Scan(&j.Telegram, &j.Kind, &j.Name)
		return j, err
	}, `select telegram_username, kind, name from jurors where year = ? order by kind, name`, year)
}
