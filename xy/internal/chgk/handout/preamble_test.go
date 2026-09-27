package handout

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/preamble_cases.json is chgksuite's apply_preamble run over each
// input (tests/handouter_preamble_test.py's SOURCE and a few edges).
func TestApplyPreambleMatchesChgksuite(t *testing.T) {
	raw, err := os.ReadFile("testdata/preamble_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string   `json:"name"`
		In     string   `json:"in"`
		Ignore []string `json:"ignore"`
		Want   string   `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		a := DefaultArgs()
		for _, k := range c.Ignore {
			switch k {
			case "font_size":
				a.FontSize = 20
			case "font_family":
				a.Font = "PT Serif"
			}
		}
		got, err := ApplyPreamble(c.In, a)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got != c.Want {
			t.Errorf("%s:\n got %q\nwant %q", c.Name, got, c.Want)
		}
	}
}

func TestPreambleRefusesWhatDependsOnTheHandout(t *testing.T) {
	for _, line := range []string{"columns: 3", "for_question: 1", "image: a.png", "просто текст"} {
		if _, err := ApplyPreamble("///preamble\n"+line+"\n---\nfor_question: 1\ncolumns: 3\n\nа", DefaultArgs()); err == nil {
			t.Errorf("%q was accepted", line)
		}
	}
}
