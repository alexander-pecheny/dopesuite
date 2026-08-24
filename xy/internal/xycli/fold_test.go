package xycli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestFoldParity holds Folding to the browser's (find.ts foldSearch): a search
// from the shell must find what a search in the app finds. Regenerate the corpus
// with `deno run --allow-read --allow-write scripts/gen_fold_fixture.js`.
func TestFoldParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/fold.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text   string `json:"text"`
		Folded string `json:"folded"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("corpus has %d cases — regenerate it", len(cases))
	}
	for _, c := range cases {
		if got := Fold(c.Text); got != c.Folded {
			t.Errorf("Fold(%q) = %q, want %q", c.Text, got, c.Folded)
		}
	}
}

func TestFoldMakesSearchForgiving(t *testing.T) {
	// What an editor typed, and what the board holds after the pass ran.
	typed, stored := "чехов - как писал", "Че́хов — как писал"
	if !strings.Contains(Fold(stored), Fold(typed)) {
		t.Fatalf("folded %q does not contain folded %q", stored, typed)
	}
}
