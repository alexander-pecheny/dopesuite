package docx

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
)

var reWT = regexp.MustCompile(`(?s)<w:t[^>]*>(.*?)</w:t>`)

// docText extracts and concatenates all <w:t> run text from a .docx's
// word/document.xml — the visible text content, ignoring formatting/breaks.
func docText(t *testing.T, docx []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		var b strings.Builder
		for _, m := range reWT.FindAllStringSubmatch(string(data), -1) {
			b.WriteString(unescapeXML(m[1]))
		}
		return b.String()
	}
	t.Fatal("no document.xml")
	return ""
}

func unescapeXML(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// TestDocxTextParity compares the visible text of our .docx to chgksuite's own
// `compose docx` output (testdata/*.docx) for the same 4s source. Verifies the
// rendered text content + order + numbering + labels + nbsp match.
func TestDocxTextParity(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.4s")
	if len(files) == 0 {
		t.Skip("no testdata")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".4s")
		t.Run(name, func(t *testing.T) {
			oraclePath := filepath.Join("testdata", name+".docx")
			oracle, err := os.ReadFile(oraclePath)
			if err != nil {
				t.Skipf("no oracle %s", oraclePath)
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			mine, err := Export(fsource.Parse(string(src), "chgk"))
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			want := docText(t, oracle)
			got := docText(t, mine)
			if want != got {
				t.Errorf("text mismatch for %s\n--- chgksuite ---\n%q\n--- go ---\n%q", name, want, got)
			}
		})
	}
}
