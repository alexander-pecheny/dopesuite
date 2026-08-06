package typoedit

import (
	"encoding/json"
	"os"
	"testing"
)

// The cases live in testdata/pass_cases.json because the pass has two
// implementations now: this one, and the TypeScript port the browser runs
// (web/ts/typo.ts), which is where the button actually goes — question text must
// not be posted to a server that is never allowed to see it. Both suites read
// THIS file, so the two cannot drift apart in silence. jstest/typo.test.js is
// the other reader.
type passCases struct {
	Pass []struct {
		Name string `json:"name"`
		In   string `json:"in"`
		Want string `json:"want"`
	} `json:"pass"`
	Idempotent []string `json:"idempotent"`
}

func loadCases(t *testing.T) passCases {
	t.Helper()
	b, err := os.ReadFile("testdata/pass_cases.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var c passCases
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(c.Pass) == 0 {
		t.Fatal("fixtures hold no cases")
	}
	return c
}

// The pass inserts characters you cannot see:   is the non-breaking space,
// ‑ the non-breaking hyphen. The fixtures spell them out.
func TestPass(t *testing.T) {
	for _, c := range loadCases(t).Pass {
		t.Run(c.Name, func(t *testing.T) {
			if got := Pass(c.In); got != c.Want {
				t.Errorf("Pass(%q)\n got: %q\nwant: %q", c.In, got, c.Want)
			}
		})
	}
}

// TestPassIsIdempotent: the button is a button — a user will press it twice.
func TestPassIsIdempotent(t *testing.T) {
	for _, src := range loadCases(t).Idempotent {
		once := Pass(src)
		if twice := Pass(once); twice != once {
			t.Errorf("second pass changed the text:\n once: %q\ntwice: %q", once, twice)
		}
	}
}
