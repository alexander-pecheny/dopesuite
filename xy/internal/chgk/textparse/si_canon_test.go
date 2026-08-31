package textparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/docxread"
	"xy/internal/chgk/fsource"
	"xy/internal/chgk/typo"
)

// siFixtures are the СИ and троика packages in chgksuite's corpus that are not
// encrypted. Both games read the document's own outline, so these go through
// docxread with the heading markers on, exactly as `chgksuite parse --game si`
// does.
var siFixtures = []struct{ file, game string }{
	{"schr16_ek_all.docx", "si"},
	{"schr18_otbor_ssi_all.docx", "si"},
	{"nesova2025_troika.docx", "troika"},
}

// TestSICanonParity requires the composed 4s to be chgksuite's own, byte for
// byte. Both games always write every number out (a СИ question's number is its
// point value, a троика's repeats in every theme), which is what parse_wrapper
// forces and what the canons were written with.
func TestSICanonParity(t *testing.T) {
	dir := chgksuiteTests(t)
	for _, f := range siFixtures {
		t.Run(f.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, f.file))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, f.file+".canon"))
			if err != nil {
				t.Fatal(err)
			}
			base := strings.TrimSuffix(f.file, filepath.Ext(f.file))
			text, _, err := docxread.ToText(raw, docxread.Options{
				ImagePrefix:       strings.ReplaceAll(base, " ", "_") + "_",
				HeadingMarkers:    true,
				PreserveListStart: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			doc := ParseSI(text, typo.Options{})
			if f.game == "troika" {
				doc = ParseTroika(text, typo.Options{})
			}
			if got := fsource.Compose(doc, fsource.NumbersAll); got != string(want) {
				t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
			}
		})
	}
}

// TestSITextCases replays the awkward packages chgksuite keeps as literals in
// its own unit tests — a theme written after a source, a source list numbered
// exactly like the questions, the «Мультифора» variant — and requires the same
// 4s. Regenerate with scripts/gen_si_cases.py.
func TestSITextCases(t *testing.T) {
	files, _ := filepath.Glob("testdata/si_cases/*.txt")
	if len(files) == 0 {
		t.Skip("no cases; run scripts/gen_si_cases.py")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(f, ".txt") + ".canon")
			if err != nil {
				t.Fatal(err)
			}
			parse := ParseSI
			if strings.HasSuffix(name, ".troika") {
				parse = ParseTroika
			}
			got := fsource.Compose(parse(string(text), typo.Options{}), fsource.NumbersAll)
			if got != string(want) {
				t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
			}
		})
	}
}
