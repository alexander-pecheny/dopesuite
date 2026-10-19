package qyserver

import (
	"context"
	"strconv"
	"strings"
	"time"

	"qy/qy/domain/contest"
	"qy/qy/storage/store"
)

// Dates as the rules write them: the day and the month in the genitive, with
// the year added only when it is not the contest's own.

func monthName(m time.Month) string {
	names := [...]string{
		str.Dates.Months.January(), str.Dates.Months.February(), str.Dates.Months.March(),
		str.Dates.Months.April(), str.Dates.Months.May(), str.Dates.Months.June(),
		str.Dates.Months.July(), str.Dates.Months.August(), str.Dates.Months.September(),
		str.Dates.Months.October(), str.Dates.Months.November(), str.Dates.Months.December(),
	}
	return names[m-1]
}

func dayMonth(t time.Time) string { return strconv.Itoa(t.Day()) + " " + monthName(t.Month()) }

// dayText is a stored date with its year, for a page outside the rules.
func dayText(s string) string {
	t, ok := contest.Day(s)
	if !ok {
		return s
	}
	return dayMonth(t) + " " + strconv.Itoa(t.Year())
}

// inYear is a date for the rules of year's contest.
func inYear(t time.Time, year int) string {
	if t.Year() == year {
		return dayMonth(t)
	}
	return dayMonth(t) + " " + strconv.Itoa(t.Year())
}

func sameMonth(a, b time.Time) bool { return a.Year() == b.Year() && a.Month() == b.Month() }

// dashRange is "18–24" and the month, or two full dates around a dash when
// the range crosses a month.
func dashRange(a, b time.Time, year int) string {
	if sameMonth(a, b) {
		return strconv.Itoa(a.Day()) + "–" + inYear(b, year)
	}
	return inYear(a, year) + " — " + inYear(b, year)
}

// fromTo is the same range as the rules phrase it for nomination.
func fromTo(a, b time.Time, year int) string {
	if sameMonth(a, b) {
		return str.Dates.FromTo(strconv.Itoa(a.Day()), inYear(b, year))
	}
	return str.Dates.FromTo(inYear(a, year), inYear(b, year))
}

// joinNames lists names the way a sentence does: commas, and the word for
// "and" before the last one.
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + str.Dates.And() + names[len(names)-1]
}

// rulesVars are the variables a page of the contest's year may use, named as
// the old rules.src.md named them.
func (s *server) rulesVars(ctx context.Context, c contest.Contest) (map[string]string, error) {
	day := func(v string) time.Time { t, _ := contest.Day(v); return t }
	jurors, err := store.Jurors(ctx, s.db, c.Year)
	if err != nil {
		return nil, err
	}
	var expert []string
	for _, j := range jurors {
		if j.Kind == store.Expert {
			expert = append(expert, j.Name)
		}
	}
	y := c.Year
	return map[string]string{
		"year":                         strconv.Itoa(y),
		"results_year":                 strconv.Itoa(day(c.ResultsDate).Year()),
		"date_questions_allowed_start": dayText(c.QuestionsFrom),
		"date_questions_allowed_end":   dayText(c.QuestionsTo),
		"date_nomination_start":        inYear(day(c.NominationStart), y),
		"dates_nomination":             fromTo(day(c.NominationStart), day(c.NominationEnd), y),
		"dates_preliminary_voting":     dashRange(day(c.PrelimStart), day(c.PrelimEnd), y),
		"dates_factcheck":              dashRange(day(c.FactcheckStart), day(c.FactcheckEnd), y),
		"date_final_voting_start":      inYear(day(c.FinalStart), y),
		"dates_final_voting":           dashRange(day(c.FinalStart), day(c.FinalEnd), y),
		"date_results":                 dayMonth(day(c.ResultsDate)),
		"jury":                         joinNames(expert),
	}, nil
}
