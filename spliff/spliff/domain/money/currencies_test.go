package money

import (
	"sort"
	"testing"
)

// The table is data, and data rots quietly. These are the shapes everything
// downstream assumes of it: the picker reads it in code order, the exponent
// arithmetic reads it by code, and the API hands both to the browser.
func TestCurrencyTableShape(t *testing.T) {
	if len(Currencies) < 150 {
		t.Fatalf("the table holds %d currencies — ISO 4217 has about 180", len(Currencies))
	}
	seen := map[string]bool{}
	codes := make([]string, 0, len(Currencies))
	for _, c := range Currencies {
		if !ValidCode(c.Code) || c.Code != Normalise(c.Code) {
			t.Errorf("%q is not a normalised ISO 4217 code", c.Code)
		}
		if c.Name == "" {
			t.Errorf("%s has no name", c.Code)
		}
		if c.Exponent != 0 && c.Exponent != 2 && c.Exponent != 3 {
			t.Errorf("%s has exponent %d", c.Code, c.Exponent)
		}
		if seen[c.Code] {
			t.Errorf("%s appears twice", c.Code)
		}
		seen[c.Code] = true
		codes = append(codes, c.Code)
	}
	if !sort.StringsAreSorted(codes) {
		t.Error("the table is not in code order")
	}
}

// The exponents the table now carries are the ones the arithmetic was written
// against: a change to them moves money.
func TestExponentsSurvivedTheTable(t *testing.T) {
	for _, code := range []string{"BIF", "CLP", "DJF", "GNF", "ISK", "JPY", "KMF", "KRW", "PYG", "RWF", "UGX", "UYI", "VND", "VUV", "XAF", "XOF", "XPF"} {
		if got := Exponent(code); got != 0 {
			t.Errorf("Exponent(%q) = %d, want 0", code, got)
		}
	}
	for _, code := range []string{"BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND"} {
		if got := Exponent(code); got != 3 {
			t.Errorf("Exponent(%q) = %d, want 3", code, got)
		}
	}
	for _, code := range []string{"EUR", "USD", "GEL", "RUB"} {
		if got := Exponent(code); got != 2 {
			t.Errorf("Exponent(%q) = %d, want 2", code, got)
		}
	}
	// A code ISO adds after this table was written still counts in cents.
	if got := Exponent("QQQ"); got != 2 {
		t.Errorf("Exponent of an unknown code = %d, want 2", got)
	}
}

func TestName(t *testing.T) {
	for code, want := range map[string]string{
		"EUR": "Euro",
		"USD": "US Dollar",
		"GEL": "Georgian Lari",
		"KZT": "Kazakhstani Tenge",
	} {
		if got := Name(code); got != want {
			t.Errorf("Name(%q) = %q, want %q", code, got, want)
		}
		if got := Name(" " + code + " "); got != want {
			t.Errorf("Name is not normalising: %q", got)
		}
	}
	if got := Name("QQQ"); got != "" {
		t.Errorf("Name of an unknown code = %q, want empty", got)
	}
}
