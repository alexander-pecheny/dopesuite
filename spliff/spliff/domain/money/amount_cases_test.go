package money

import (
	"encoding/json"
	"os"
	"testing"
)

type amountCases struct {
	Parse []struct {
		Text     string `json:"text"`
		Currency string `json:"currency"`
		Minor    *int64 `json:"minor"`
	} `json:"parse"`
	Format []struct {
		Minor    int64  `json:"minor"`
		Currency string `json:"currency"`
		Text     string `json:"text"`
	} `json:"format"`
}

// TestAmountCases is the reference half of the parity check: the editor's
// parseAmount and formatMinor read the same file in
// web/jstest/money-parity.test.js.
func TestAmountCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/amount_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases amountCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Parse {
		got, err := Parse(c.Text, c.Currency)
		switch {
		case c.Minor == nil && err == nil:
			t.Errorf("Parse(%q, %s) = %d, want a refusal", c.Text, c.Currency, got.Minor)
		case c.Minor != nil && err != nil:
			t.Errorf("Parse(%q, %s): %v, want %d", c.Text, c.Currency, err, *c.Minor)
		case c.Minor != nil && got.Minor != *c.Minor:
			t.Errorf("Parse(%q, %s) = %d, want %d", c.Text, c.Currency, got.Minor, *c.Minor)
		}
	}
	for _, c := range cases.Format {
		if got := Format(c.Minor, c.Currency); got != c.Text {
			t.Errorf("Format(%d, %s) = %q, want %q", c.Minor, c.Currency, got, c.Text)
		}
	}
}
