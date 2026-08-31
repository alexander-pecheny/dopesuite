package textparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/textenc"
)

// TestDBCanonParity reads db.chgk.info's own export the way chgksuite does and
// requires the 4s chgksuite's canon holds, byte for byte.
func TestDBCanonParity(t *testing.T) {
	home, _ := os.UserHomeDir()
	src := filepath.Join(home, "chgksuite", "chgksuite", "tests", "balt09-1.txt")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("no chgksuite checkout: %v", err)
	}
	want, err := os.ReadFile(src + ".canon")
	if err != nil {
		t.Fatal(err)
	}
	text, err := textenc.Decode(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !IsDBExport(text) {
		t.Fatal("the fixture is not recognised as a db.chgk.info export")
	}
	got := fsource.Compose(ParseDB(text, nil), fsource.NumbersDefault)
	if got != string(want) {
		t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
	}
}

// TestDBCases covers what the corpus fixture does not reach — a <раздатка>, a
// blitz, numbered comments and sources, a second Ответ, an (aud …) reference —
// against the 4s chgksuite's own CLI writes for the same file.
func TestDBCases(t *testing.T) {
	files, _ := filepath.Glob("testdata/db_cases/*.txt")
	if len(files) == 0 {
		t.Skip("no cases")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(f, ".txt") + ".canon")
			if err != nil {
				t.Fatal(err)
			}
			text, err := textenc.Decode(raw, "")
			if err != nil {
				t.Fatal(err)
			}
			got := fsource.Compose(ParseDB(text, nil), fsource.NumbersDefault)
			if got != string(want) {
				t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
			}
		})
	}
}
