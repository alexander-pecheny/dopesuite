package docx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

// optionVariants maps a testdata suffix to the switches it was generated with
// (scripts/gen_docx_oracles.sh writes them from chgksuite itself).
var optionVariants = map[string]Options{
	"plain":       {},
	"whiten":      {Spoilers: SpoilersWhiten},
	"pagebreak":   {Spoilers: SpoilersPagebreak},
	"dots":        {Spoilers: SpoilersDots},
	"screen":      {ScreenMode: ScreenReplaceAll},
	"versions":    {ScreenMode: ScreenAddVersions},
	"columns":     {ScreenMode: ScreenAddVersionsColumns},
	"noanswers":   {NoAnswers: true},
	"noparagraph": {NoParagraph: true},
	"onlynumber":  {OnlyQuestionNumber: true},
	"samesize":    {SameSourceAndAuthorSize: true},
}

// TestOptionParity compares the generated body XML against chgksuite's own for
// every `compose docx` switch, on every fixture.
func TestOptionParity(t *testing.T) {
	oracles, _ := filepath.Glob("testdata/*__*.xml")
	if len(oracles) == 0 {
		t.Skip("no oracles; run scripts/gen_docx_oracles.sh")
	}
	for _, o := range oracles {
		base := strings.TrimSuffix(filepath.Base(o), ".xml")
		name, variant, _ := strings.Cut(base, "__")
		opts, ok := optionVariants[variant]
		if !ok {
			t.Errorf("unknown variant %q", variant)
			continue
		}
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", name+".4s"))
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := os.ReadFile(o)
			if err != nil {
				t.Fatal(err)
			}
			mine, err := Export(fsource.Parse(string(src), "chgk"), nil, opts)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			want := stripSrcSz(bodyXML(string(oracle)))
			got := stripSrcSz(bodyXML(documentXML(t, mine)))
			if want != got {
				t.Errorf("body XML mismatch\n--- chgksuite ---\n%s\n--- go ---\n%s", want, got)
			}
		})
	}
}
