// Package util holds small, stateless, dependency-light helpers shared across the
// server and the extracted leaf/handler packages: time formatting, best-effort
// JSON, slug/username validation, a SQLite unique-violation check, and the
// locale-aware alphabetical comparison used for team/name ordering. Everything
// here is pure (stdlib only, no server coupling).
package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	usernameMinLen = 2
	usernameMaxLen = 32
)

// UtcNow returns the current time as an RFC3339 UTC string.
func UtcNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// MustJSON marshals value, returning "{}" on error (best-effort; callers use it
// where a malformed value should degrade gracefully rather than fail).
func MustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// MaxInt returns the larger of a and b.
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ValidateSlug checks a URL slug: 1–64 chars of [a-z0-9-], not all digits.
func ValidateSlug(slug string) error {
	if len(slug) == 0 {
		return errors.New("slug is empty")
	}
	if len(slug) > 64 {
		return errors.New("slug is longer than 64 characters")
	}
	allDigit := true
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
			allDigit = false
		case r == '-':
			allDigit = false
		case r >= '0' && r <= '9':
			// ok
		default:
			return errors.New("slug may contain only a-z, 0-9 and hyphen")
		}
	}
	if allDigit {
		return errors.New("slug cannot be all digits")
	}
	return nil
}

// ValidUsername reports whether s is a syntactically valid username
// (2–32 chars of [A-Za-z0-9_.-]).
func ValidUsername(s string) bool {
	if len(s) < usernameMinLen || len(s) > usernameMaxLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// IsUniqueViolation reports whether err looks like a SQLite UNIQUE/constraint
// violation.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

// AlphaKey normalizes a string for alphabetical comparison: lowercased, trimmed,
// with Cyrillic ё folded to е.
func AlphaKey(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "ё", "е")
}

// CompareAlpha orders two strings by their AlphaKey, falling back to a trimmed
// raw comparison so distinct values never compare equal.
func CompareAlpha(a, b string) int {
	ak := AlphaKey(a)
	bk := AlphaKey(b)
	if ak < bk {
		return -1
	}
	if ak > bk {
		return 1
	}
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// BoolToInt maps false→0, true→1 (for SQLite integer-boolean columns).
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NullableString returns nil for an empty string, else the string (for nullable
// SQL params).
func NullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ParseOptionalInt64 parses s as int64, returning nil when empty/invalid.
func ParseOptionalInt64(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return v
}

// FormatFestDates renders a fest's start/end as "start — end", or one date, or "".
func FormatFestDates(start, end string) string {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	switch {
	case start == "" && end == "":
		return ""
	case start != "" && end != "" && start != end:
		return start + " — " + end
	case start != "":
		return start
	default:
		return end
	}
}

var festMonthsGenitive = [...]string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// parseFestYMD parses a "YYYY-MM-DD" date, returning the components and whether
// it is a valid in-range calendar date.
func parseFestYMD(s string) (year, month, day int, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return 0, 0, 0, false
	}
	y, err1 := strconv.Atoi(s[0:4])
	m, err2 := strconv.Atoi(s[5:7])
	d, err3 := strconv.Atoi(s[8:10])
	if err1 != nil || err2 != nil || err3 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	return y, m, d, true
}

// HumanizeFestDates renders a fest's start/end as a Russian human-readable date
// range, e.g. "13–14 июня", "31 июля — 1 августа", or "5 марта". The year is
// appended only when it differs from currentYear (e.g. "5 марта 2025"). Dates
// must be "YYYY-MM-DD"; on a parse failure it falls back to FormatFestDates.
func HumanizeFestDates(start, end string, currentYear int) string {
	sy, sm, sd, sok := parseFestYMD(start)
	ey, em, ed, eok := parseFestYMD(end)
	switch {
	case !sok && !eok:
		return FormatFestDates(start, end)
	case sok && !eok:
		ey, em, ed, eok = sy, sm, sd, true
	case !sok && eok:
		sy, sm, sd, sok = ey, em, ed, true
	}

	day := func(y, m, d int, withYear bool) string {
		out := fmt.Sprintf("%d %s", d, festMonthsGenitive[m-1])
		if withYear {
			out += fmt.Sprintf(" %d", y)
		}
		return out
	}

	// Single date.
	if sy == ey && sm == em && sd == ed {
		return day(sy, sm, sd, sy != currentYear)
	}
	// Cross-year range: spell the year on both sides.
	if sy != ey {
		return day(sy, sm, sd, true) + " — " + day(ey, em, ed, true)
	}
	yearSuffix := ""
	if sy != currentYear {
		yearSuffix = fmt.Sprintf(" %d", sy)
	}
	// Same month: "13–14 июня".
	if sm == em {
		return fmt.Sprintf("%d–%d %s%s", sd, ed, festMonthsGenitive[sm-1], yearSuffix)
	}
	// Same year, different month: "31 июля — 1 августа".
	return day(sy, sm, sd, false) + " — " + day(ey, em, ed, false) + yearSuffix
}

// Fest date buckets returned by ClassifyFestDate.
const (
	FestCurrent = iota
	FestFuture
	FestPast
)

// ClassifyFestDate buckets a fest by its start/end ("YYYY-MM-DD") relative to
// today: FestCurrent when start <= today <= end, FestFuture when it starts
// after today, otherwise FestPast (including fests with no usable date). An
// empty start falls back to end and vice-versa.
func ClassifyFestDate(start, end, today string) int {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if start == "" {
		start = end
	}
	if end == "" {
		end = start
	}
	switch {
	case start == "":
		return FestPast
	case start <= today && today <= end:
		return FestCurrent
	case start > today:
		return FestFuture
	default:
		return FestPast
	}
}

// MaxInt64 returns the larger of two int64 values.
func MaxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// NullableInt64 maps a zero value to a SQL NULL and any other value to itself.
func NullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
