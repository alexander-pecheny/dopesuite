package chgkimport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/docx"
	"xy/internal/chgk/fsource"
)

// A 4s exported to .docx and imported back is the same 4s: in particular a
// handout, which the export sets as a captioned box (a two-row table) and the
// import reads back as the bracket in the block form. Only the export's
// non-breaking spaces are told apart from plain ones, and a legacy "> "
// handout line comes back as that bracket. The fixtures are the docx
// exporter's that hold handouts; the others have older gaps of their own
// (#EDITOR comes back as meta, a multi-line field loses its line breaks).
func TestDocxRoundTrip(t *testing.T) {
	for _, name := range []string{"handouts.4s", "basic.4s", "screen.4s"} {
		f := filepath.Join("..", "docx", "testdata", name)
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out, err := docx.Export(fsource.Parse(string(src), "chgk"), nil, docx.Options{})
			if err != nil {
				t.Fatal(err)
			}
			res, err := Parse("packet.docx", out, "chgk")
			if err != nil {
				t.Fatal(err)
			}
			want := canonical(string(src))
			if got := canonical(res.Source); got != want {
				t.Errorf("round trip changed the 4s\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

func canonical(s string) string {
	s = strings.NewReplacer("\u00a0", " ", "\u2011", "-").Replace(s)
	doc := fsource.Parse(s, "chgk")
	for _, el := range doc {
		q, ok := el.Content.(*fsource.Question)
		if !ok || !q.Has("handout") {
			continue
		}
		q.Set("question", fmt.Sprintf("[Раздаточный материал:\n%v\n]\n%v", q.Get("handout"), q.Get("question")))
		q.Delete("handout")
	}
	return fsource.Compose(doc, fsource.NumbersDefault)
}
