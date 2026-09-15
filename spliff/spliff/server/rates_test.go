package spliffserver

import (
	"os"
	"strings"
	"testing"

	"spliff/spliff/domain/money"
	"spliff/spliff/domain/rates"
)

// The fetcher is checked against a SAVED response, never against the network:
// a test that opens a socket fails on a plane, and a rate source that changes
// shape should fail here and not in production.
func TestParseRatesReadsTheSavedResponse(t *testing.T) {
	body, err := os.ReadFile("testdata/er-api-latest-usd.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	table, err := ParseRates(body)
	if err != nil {
		t.Fatalf("ParseRates: %v", err)
	}
	// The rates are kept as the LITERAL text the source sent: a rate that has
	// been through a float64 is a rate that has lost digits.
	for code, want := range map[string]string{
		"USD": "1", "EUR": "0.855402", "GEL": "2.705", "JPY": "147.318",
		"KWD": "0.305431", "BTC": "0.0000086",
	} {
		if got := table[code]; got != want {
			t.Errorf("%s = %q, want %q", code, got, want)
		}
	}
	// Anything that is not shaped like an ISO 4217 code is dropped rather than
	// stored: the exponent table has nothing to say about it.
	if _, ok := table["XYZ_NOT_A_CODE"]; ok {
		t.Error("a non-code was kept in the table")
	}
	// And the table it makes is one the domain can convert with.
	got, err := rates.Convert(money.Amount{Minor: 10000, Currency: "USD"}, "GEL",
		rates.Table{Day: "2026-09-15", Rates: table}, nil)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 27050 {
		t.Errorf("100.00 USD = %d GEL minor units, want 27050", got.Minor)
	}
}

func TestParseRatesRefusesWhatItCannotTrust(t *testing.T) {
	cases := map[string]string{
		"a failure the source reported": `{"result":"error","base_code":"USD","rates":{"USD":1}}`,
		"a base that is not USD":        `{"result":"success","base_code":"EUR","rates":{"EUR":1}}`,
		"no rates at all":               `{"result":"success","base_code":"USD","rates":{}}`,
		"no USD rate":                   `{"result":"success","base_code":"USD","rates":{"EUR":0.9}}`,
		"not JSON":                      `<html>502 Bad Gateway</html>`,
	}
	for name, body := range cases {
		if _, err := ParseRates([]byte(body)); err == nil {
			t.Errorf("%s: ParseRates accepted it", name)
		}
	}
}

func TestRateSourceIsTheOneTheADRNames(t *testing.T) {
	if !strings.HasPrefix(RateSource, "https://open.er-api.com/v6/latest/USD") {
		t.Errorf("RateSource = %q", RateSource)
	}
}
