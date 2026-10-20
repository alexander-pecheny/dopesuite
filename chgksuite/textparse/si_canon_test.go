package textparse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.pecheny.me/pecheny/dopesuite/chgksuite/corpus"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/docxread"
	"code.pecheny.me/pecheny/dopesuite/chgksuite/fsource"
)

// siFixtures are the СИ and троика packages in chgksuite's corpus that are not
// encrypted. Both games read the document's own outline, so these go through
// docxread with the heading markers on, exactly as `chgksuite parse --game si`
// does.
var siFixtures = []struct{ file, game string }{
	{"schr16_ek_all.docx", "si"},
	{"schr18_otbor_ssi_all.docx", "si"},
	{"nesova2025_troika.docx", "troika"},
	{"01 SI studchr 2026_Otbor.docx.encrypted", "si"},
	{"14 SI studchr 2026_GF.docx.encrypted", "si"},
	{"7. ЭК СЧР 2026. Весь пакет.docx.encrypted", "si"},
}

// TestSICanonParity requires the composed 4s to be chgksuite's own, byte for
// byte. Both games always write every number out (a СИ question's number is its
// point value, a троика's repeats in every theme), which is what parse_wrapper
// forces and what the canons were written with.
func TestSICanonParity(t *testing.T) {
	for _, f := range siFixtures {
		t.Run(f.file, func(t *testing.T) {
			want := corpus.Canon(t, f.file)
			name := corpus.Name(f.file)
			base := strings.TrimSuffix(name, filepath.Ext(name))
			text, _, err := docxread.ToText(corpus.Read(t, f.file), docxread.Options{
				ImagePrefix:       strings.ReplaceAll(base, " ", "_") + "_",
				HeadingMarkers:    true,
				PreserveListStart: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			doc := ParseSI(text, SIOptions{})
			if f.game == "troika" {
				doc = ParseTroika(text, SIOptions{})
			}
			if got := fsource.Compose(doc, fsource.NumbersAll); got != want {
				t.Errorf("mismatch:\n%s", firstDiff(want, got))
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
			got := fsource.Compose(parse(string(text), SIOptions{}), fsource.NumbersAll)
			if got != string(want) {
				t.Errorf("mismatch:\n%s", firstDiff(string(want), got))
			}
		})
	}
}
