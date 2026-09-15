package money

import "testing"

func TestExponent(t *testing.T) {
	cases := map[string]int{
		"USD": 2, "EUR": 2, "GEL": 2, "RUB": 2,
		"JPY": 0, "KRW": 0, "ISK": 0, "CLP": 0,
		"KWD": 3, "BHD": 3, "JOD": 3, "OMR": 3, "TND": 3, "LYD": 3, "IQD": 3,
		"usd": 2, " jpy ": 0,
		"ZZZ": 2, // unknown: the ordinary case, not a crash
	}
	for code, want := range cases {
		if got := Exponent(code); got != want {
			t.Errorf("Exponent(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestScale(t *testing.T) {
	for code, want := range map[string]int64{"JPY": 1, "USD": 100, "KWD": 1000} {
		if got := Scale(code); got != want {
			t.Errorf("Scale(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestValidCode(t *testing.T) {
	for _, ok := range []string{"USD", "eur", " gel "} {
		if !ValidCode(ok) {
			t.Errorf("ValidCode(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "US", "USDD", "US1", "US-"} {
		if ValidCode(bad) {
			t.Errorf("ValidCode(%q) = true, want false", bad)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		currency string
		want     int64
		wantErr  bool
	}{
		{"12.34", "USD", 1234, false},
		{"12,34", "USD", 1234, false},
		{"12", "USD", 1200, false},
		{"12.3", "USD", 1230, false},
		{".5", "USD", 50, false},
		{"0", "USD", 0, false},
		{"-0.05", "USD", -5, false},
		{"+7.10", "USD", 710, false},
		{"1 234.50", "USD", 123450, false},
		{"12.345", "USD", 0, true}, // one decimal too many for a cent currency
		{"1234", "JPY", 1234, false},
		{"1234.0", "JPY", 0, true}, // there is no tenth of a yen
		{"1.234", "KWD", 1234, false},
		{"1.2345", "KWD", 0, true},
		{"", "USD", 0, true},
		{"abc", "USD", 0, true},
		{"1.2.3", "USD", 0, true},
		{"12.34", "US", 0, true},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.currency)
		if (err != nil) != c.wantErr {
			t.Errorf("Parse(%q, %q) err = %v, wantErr %v", c.in, c.currency, err, c.wantErr)
			continue
		}
		if err == nil && got.Minor != c.want {
			t.Errorf("Parse(%q, %q) = %d, want %d", c.in, c.currency, got.Minor, c.want)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		minor    int64
		currency string
		want     string
	}{
		{1234, "USD", "12.34"},
		{5, "USD", "0.05"},
		{0, "USD", "0.00"},
		{-1234, "USD", "-12.34"},
		{-5, "USD", "-0.05"},
		{1234, "JPY", "1234"},
		{-1234, "JPY", "-1234"},
		{0, "JPY", "0"},
		{1234, "KWD", "1.234"},
		{4, "KWD", "0.004"},
		{-4, "KWD", "-0.004"},
	}
	for _, c := range cases {
		if got := Format(c.minor, c.currency); got != c.want {
			t.Errorf("Format(%d, %q) = %q, want %q", c.minor, c.currency, got, c.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, currency := range []string{"USD", "JPY", "KWD"} {
		for _, minor := range []int64{0, 1, 7, 999, -1, -1000, 123456789} {
			text := Format(minor, currency)
			back, err := Parse(text, currency)
			if err != nil {
				t.Fatalf("Parse(Format(%d, %q)) = %v", minor, currency, err)
			}
			if back.Minor != minor {
				t.Errorf("round trip %d %s via %q = %d", minor, currency, text, back.Minor)
			}
		}
	}
}

func TestAmountHelpers(t *testing.T) {
	a := Amount{Minor: -1234, Currency: "EUR"}
	if got := a.WithCode(); got != "-12.34 EUR" {
		t.Errorf("WithCode() = %q", got)
	}
	if got := a.Abs().Minor; got != 1234 {
		t.Errorf("Abs() = %d", got)
	}
	if got := a.Neg().Minor; got != 1234 {
		t.Errorf("Neg() = %d", got)
	}
	if !(Amount{Currency: "EUR"}).IsZero() {
		t.Error("zero amount is not IsZero")
	}
	if a.IsZero() {
		t.Error("non-zero amount is IsZero")
	}
}
