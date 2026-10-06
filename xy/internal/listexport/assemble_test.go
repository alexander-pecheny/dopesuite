package listexport

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// testdata/cases.json is the table: Cards in, what they export as out. This
// side is the reference, so -update rewrites the outputs from Assemble; the
// browser's copy reads the same file (jstest/listexport_parity.test.js), and a
// case added here is a case both sides answer.
var update = flag.Bool("update", false, "rewrite testdata/cases.json from Assemble")

const casesPath = "testdata/cases.json"

type assembleCase struct {
	Name  string `json:"name"`
	Cards []Card `json:"cards"`
	Assembly
}

func TestAssemble(t *testing.T) {
	raw, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []assembleCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if *update {
		for i := range cases {
			cases[i].Assembly = Assemble(cases[i].Cards)
		}
		// The file is read by people too, so "> " stays "> ".
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		if err := enc.Encode(cases); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(casesPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := Assemble(c.Cards)
			if got.Source != c.Source {
				t.Errorf("source =\n%s\nwant\n%s", got.Source, c.Source)
			}
			if got.Game != c.Game {
				t.Errorf("game = %q, want %q", got.Game, c.Game)
			}
			if got.Hndt != c.Hndt {
				t.Errorf("hndt =\n%s\nwant\n%s", got.Hndt, c.Hndt)
			}
		})
	}
}
