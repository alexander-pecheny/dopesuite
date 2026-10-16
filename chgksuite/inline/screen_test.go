package inline

import (
	"encoding/json"
	"os"
	"testing"
)

// TestScreenParity checks the screen-mode passes against chgksuite's own output
// for the same strings (testdata/screen.json, written by
// composer_common.remove_{accents,square_brackets}_standalone).
func TestScreenParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/screen.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		In       string `json:"in"`
		Accents  string `json:"accents"`
		Brackets string `json:"brackets"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := RemoveAccents(c.In); got != c.Accents {
			t.Errorf("RemoveAccents(%q) = %q, want %q", c.In, got, c.Accents)
		}
		if got := RemoveSquareBrackets(c.In); got != c.Brackets {
			t.Errorf("RemoveSquareBrackets(%q) = %q, want %q", c.In, got, c.Brackets)
		}
	}
}
