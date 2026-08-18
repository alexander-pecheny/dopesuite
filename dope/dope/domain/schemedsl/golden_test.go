package schemedsl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The championship's five schemes compile to exactly what they compiled to:
// a бой's code is its identity (state, journal, SSE scopes hang on it), so a
// change to the compiler that moves one is a change to every running game.
// DOPE_UPDATE_GOLDEN=1 rewrites the pins when a change is meant.
func TestStudchrSchemesCompileAsPinned(t *testing.T) {
	for _, c := range []struct{ name, gameType string }{
		{"ek", "ek"}, {"brain", "brain"}, {"si", "si"}, {"tpsh", "si"}, {"od", "od"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "studchr", c.name+".dsl"))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Parse(string(src))
			if err != nil {
				t.Fatal(err)
			}
			scheme, err := Compile(doc, Input{Slug: c.name, Title: c.name, GameType: c.gameType})
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.MarshalIndent(scheme, "", " ")
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", c.name+".json")
			if os.Getenv("DOPE_UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v — DOPE_UPDATE_GOLDEN=1 writes it", err)
			}
			if string(want) != string(got) {
				t.Errorf("%s compiles differently from testdata/golden/%s.json (DOPE_UPDATE_GOLDEN=1 if that is meant)", c.name, c.name)
			}
		})
	}
}
