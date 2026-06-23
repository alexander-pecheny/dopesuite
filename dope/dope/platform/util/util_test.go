package util

import "testing"

func TestHumanizeFestDates(t *testing.T) {
	cases := []struct {
		name        string
		start, end  string
		currentYear int
		want        string
	}{
		{"same month", "2026-06-13", "2026-06-14", 2026, "13–14 июня"},
		{"cross month", "2026-07-31", "2026-08-01", 2026, "31 июля — 1 августа"},
		{"single date", "2026-03-05", "2026-03-05", 2026, "5 марта"},
		{"single date only start", "2026-03-05", "", 2026, "5 марта"},
		{"other year same month", "2025-06-13", "2025-06-14", 2026, "13–14 июня 2025"},
		{"other year cross month", "2025-07-31", "2025-08-01", 2026, "31 июля — 1 августа 2025"},
		{"other year single", "2025-03-05", "2025-03-05", 2026, "5 марта 2025"},
		{"cross year", "2025-12-31", "2026-01-01", 2026, "31 декабря 2025 — 1 января 2026"},
		{"empty", "", "", 2026, ""},
		{"garbage falls back", "notadate", "", 2026, "notadate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HumanizeFestDates(c.start, c.end, c.currentYear); got != c.want {
				t.Errorf("HumanizeFestDates(%q, %q, %d) = %q, want %q", c.start, c.end, c.currentYear, got, c.want)
			}
		})
	}
}
