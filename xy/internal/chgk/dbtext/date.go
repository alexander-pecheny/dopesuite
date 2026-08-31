package dbtext

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// wrapDate is DbExporter.wrap_date: whatever the package's #DATE says, spelled
// the way the base wants it, "05-Jul-2015". A date it cannot read becomes
// chgksuite's own 2010-01-01, and one in the future is taken to be last year's.
//
// chgksuite reads it with dateparser, which guesses far more freely than this
// does — see the note in chgksuite_go_rewrite.md for what is not reproduced.
func wrapDate(s string) string {
	d, ok := parseDate(strings.TrimSpace(s))
	if !ok {
		return "01-Jan-2010"
	}
	if d.After(today()) {
		d = d.AddDate(-1, 0, 0)
	}
	return d.Format("02-Jan-2006")
}

// today is the clock wrap_date compares against; a variable so a test can hold it.
var today = func() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

var (
	reISODate     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	reDayMonYear  = regexp.MustCompile(`^(\d{1,2})-([A-Za-z]{3})-(\d{4})`)
	reDayNameYear = regexp.MustCompile(`^(\d{1,2})\s+([^\s\d]+)\s+(\d{4})`)
)

// monthNames are the month spellings a Russian package writes, nominative and
// genitive, plus the English abbreviations the base itself uses.
var monthNames = map[string]time.Month{
	"январь": time.January, "января": time.January, "jan": time.January,
	"февраль": time.February, "февраля": time.February, "feb": time.February,
	"март": time.March, "марта": time.March, "mar": time.March,
	"апрель": time.April, "апреля": time.April, "apr": time.April,
	"май": time.May, "мая": time.May, "may": time.May,
	"июнь": time.June, "июня": time.June, "jun": time.June,
	"июль": time.July, "июля": time.July, "jul": time.July,
	"август": time.August, "августа": time.August, "aug": time.August,
	"сентябрь": time.September, "сентября": time.September, "sep": time.September,
	"октябрь": time.October, "октября": time.October, "oct": time.October,
	"ноябрь": time.November, "ноября": time.November, "nov": time.November,
	"декабрь": time.December, "декабря": time.December, "dec": time.December,
}

// parseDate reads the shapes a package's date actually comes in, anchored at the
// start the way dateparser is: trailing words ("… 2015 года)") are ignored, a
// leading town is not.
func parseDate(s string) (time.Time, bool) {
	if m := reISODate.FindStringSubmatch(s); m != nil {
		return build(atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]))
	}
	if m := reDayMonYear.FindStringSubmatch(s); m != nil {
		if mon, ok := monthNames[strings.ToLower(m[2])]; ok {
			return build(atoi(m[3]), mon, atoi(m[1]))
		}
	}
	if m := reDayNameYear.FindStringSubmatch(s); m != nil {
		if mon, ok := monthNames[strings.ToLower(m[2])]; ok {
			return build(atoi(m[3]), mon, atoi(m[1]))
		}
	}
	return time.Time{}, false
}

// build turns a year/month/day into a date. Day 0 means the package named only
// a month, which dateparser fills with that month's last day.
func build(year int, month time.Month, day int) (time.Time, bool) {
	if year == 0 || month < time.January || month > time.December {
		return time.Time{}, false
	}
	if day == 0 {
		return time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1), true
	}
	d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if d.Day() != day || d.Month() != month {
		return time.Time{}, false
	}
	return d, true
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
