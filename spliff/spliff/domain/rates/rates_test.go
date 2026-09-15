package rates

import (
	"errors"
	"math/big"
	"testing"

	"spliff/spliff/domain/money"
)

// day is the fixture table: one USD, plus two currencies whose rates are round
// enough to compute by hand and one three-digit currency.
func day(d string) Table {
	return Table{Day: d, Rates: map[string]string{
		"USD": "1",
		"EUR": "0.5",
		"GEL": "2.5",
		"JPY": "150",
		"KWD": "0.25",
	}}
}

func TestResolveDay(t *testing.T) {
	days := []string{"2026-09-01", "2026-09-05", "2026-09-09"}
	cases := []struct {
		want string
		day  string
	}{
		{"2026-09-05", "2026-09-05"}, // exact
		{"2026-09-05", "2026-09-04"}, // one day either side
		{"2026-09-05", "2026-09-06"},
		{"2026-09-01", "2026-08-01"}, // before every table
		{"2026-09-09", "2026-12-01"}, // after every table
		{"2026-09-01", "2026-09-03"}, // exactly between 09-01 and 09-05: the earlier
		{"2026-09-05", "2026-09-07"}, // exactly between 09-05 and 09-09: the earlier
	}
	for _, c := range cases {
		got, ok := ResolveDay(days, c.day)
		if !ok || got != c.want {
			t.Errorf("ResolveDay(%q) = %q, %v; want %q", c.day, got, ok, c.want)
		}
	}
	if _, ok := ResolveDay(nil, "2026-09-05"); ok {
		t.Error("ResolveDay with no tables reported one")
	}
}

func TestConvertIdentity(t *testing.T) {
	// The same currency needs no table at all — an empty one proves it.
	got, err := Convert(money.Amount{Minor: 1234, Currency: "EUR"}, "EUR", Table{}, nil)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1234 || got.Currency != "EUR" {
		t.Errorf("Convert identity = %+v", got)
	}
}

func TestConvertThroughTable(t *testing.T) {
	tbl := day("2026-09-10")
	cases := []struct {
		from     string
		minor    int64
		to       string
		want     int64
		wantCode string
	}{
		// 10.00 EUR at 0.5 EUR/USD is 20 USD is 50.00 GEL at 2.5 GEL/USD.
		{"EUR", 1000, "GEL", 5000, "GEL"},
		{"GEL", 5000, "EUR", 1000, "EUR"},
		{"USD", 100, "EUR", 50, "EUR"},
		// A 0-exponent target: 1.00 USD is 150 JPY.
		{"USD", 100, "JPY", 150, "JPY"},
		// A 0-exponent source: 150 JPY is 1.00 USD.
		{"JPY", 150, "USD", 100, "USD"},
		// A 3-exponent target: 1.00 USD is 0.250 KWD.
		{"USD", 100, "KWD", 250, "KWD"},
		{"KWD", 250, "USD", 100, "USD"},
		// Across two exotic exponents: 300 JPY is 2 USD is 0.500 KWD.
		{"JPY", 300, "KWD", 500, "KWD"},
	}
	for _, c := range cases {
		got, err := Convert(money.Amount{Minor: c.minor, Currency: c.from}, c.to, tbl, nil)
		if err != nil {
			t.Fatalf("Convert(%d %s -> %s): %v", c.minor, c.from, c.to, err)
		}
		if got.Minor != c.want || got.Currency != c.wantCode {
			t.Errorf("Convert(%d %s -> %s) = %+v, want %d %s",
				c.minor, c.from, c.to, got, c.want, c.wantCode)
		}
	}
}

func TestConvertRoundsHalfEven(t *testing.T) {
	// 1 unit of X is exactly 0.005 USD, so an odd count of X lands exactly on
	// half a cent and the tie goes to the even cent.
	tbl := Table{Day: "2026-09-10", Rates: map[string]string{"USD": "1", "XXX": "200"}}
	cases := []struct {
		minor int64 // whole XXX units (exponent 2, so 100 = 1 XXX)
		want  int64
	}{
		{100, 0},  // 0.005 USD = 0.5 cents -> 0 (even)
		{300, 2},  // 0.015 USD = 1.5 cents -> 2 (even)
		{500, 2},  // 0.025 USD = 2.5 cents -> 2 (even)
		{700, 4},  // 0.035 USD = 3.5 cents -> 4 (even)
		{-300, 2}, // symmetry, magnitude only
		{-500, 2},
	}
	for _, c := range cases {
		in := money.Amount{Minor: c.minor, Currency: "XXX"}
		got, err := Convert(in, "USD", tbl, nil)
		if err != nil {
			t.Fatalf("Convert: %v", err)
		}
		want := c.want
		if c.minor < 0 {
			want = -want
		}
		if got.Minor != want {
			t.Errorf("Convert(%d XXX -> USD) = %d, want %d", c.minor, got.Minor, want)
		}
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		num, den int64
		want     int64
	}{
		{5, 2, 2}, {7, 2, 4}, {3, 2, 2}, {1, 2, 0},
		{-5, 2, -2}, {-7, 2, -4}, {-3, 2, -2}, {-1, 2, 0},
		{4, 1, 4}, {0, 3, 0},
		{2, 3, 1}, {-2, 3, -1},
		{1, 3, 0}, {-1, 3, 0},
	}
	for _, c := range cases {
		if got := RoundHalfEven(big.NewRat(c.num, c.den)); got != c.want {
			t.Errorf("RoundHalfEven(%d/%d) = %d, want %d", c.num, c.den, got, c.want)
		}
	}
}

// The Pinned rate is not offered in v1; its rule is tested from the start so
// that shipping it is a migration and a form field, not a redesign.
func TestConvertWithPin(t *testing.T) {
	tbl := day("2026-09-10")

	// A pin naming the target is used as-is and the table is never opened:
	// 10.00 EUR at a pinned 3.1 GEL each is 31.00 GEL, not the table's 50.00.
	got, err := Convert(money.Amount{Minor: 1000, Currency: "EUR"}, "GEL", tbl,
		&Pin{Currency: "GEL", Rate: "3.1"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 3100 || got.Currency != "GEL" {
		t.Errorf("pin naming the target = %+v, want 3100 GEL", got)
	}

	// A pin naming another currency goes through the pin first and the table
	// for the rest: 10.00 EUR at a pinned 3.1 GEL each is 31.00 GEL, and GEL to
	// USD at 2.5 is 12.40 USD.
	got, err = Convert(money.Amount{Minor: 1000, Currency: "EUR"}, "USD", tbl,
		&Pin{Currency: "GEL", Rate: "3.1"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1240 || got.Currency != "USD" {
		t.Errorf("pin naming another currency = %+v, want 1240 USD", got)
	}

	// A pin that is not a number is refused rather than silently ignored.
	if _, err := Convert(money.Amount{Minor: 1000, Currency: "EUR"}, "USD", tbl,
		&Pin{Currency: "GEL", Rate: "not a rate"}); !errors.Is(err, ErrBadRate) {
		t.Errorf("bad pin rate = %v, want ErrBadRate", err)
	}
}

func TestConvertMissingRate(t *testing.T) {
	tbl := day("2026-09-10")
	if _, err := Convert(money.Amount{Minor: 100, Currency: "ZZZ"}, "USD", tbl, nil); !errors.Is(err, ErrNoRate) {
		t.Errorf("unknown source = %v, want ErrNoRate", err)
	}
	if _, err := Convert(money.Amount{Minor: 100, Currency: "USD"}, "ZZZ", tbl, nil); !errors.Is(err, ErrNoRate) {
		t.Errorf("unknown target = %v, want ErrNoRate", err)
	}
	bad := Table{Day: "2026-09-10", Rates: map[string]string{"USD": "1", "EUR": "nonsense"}}
	if _, err := Convert(money.Amount{Minor: 100, Currency: "EUR"}, "USD", bad, nil); !errors.Is(err, ErrBadRate) {
		t.Errorf("unreadable rate = %v, want ErrBadRate", err)
	}
}
