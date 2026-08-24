package xycli

import (
	"encoding/json"
	"os"
	"testing"
)

// TestExportSourceParity holds the Go assembly to the browser's, which is the
// authority on what a List exports as. Regenerate the corpus with
// `deno run --allow-read --allow-write scripts/gen_source_fixture.js`.
func TestExportSourceParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/exportsource.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string   `json:"name"`
		Cards  []string `json:"cards"`
		Source string   `json:"source"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := ExportSource(c.Cards); got != c.Source {
				t.Errorf("source =\n%q\nwant\n%q", got, c.Source)
			}
		})
	}
}
