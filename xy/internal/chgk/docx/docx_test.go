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

// documentXML returns the word/document.xml part of a .docx.
func documentXML(t *testing.T, docx []byte) string {
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
		return string(data)
	}
	t.Fatal("no document.xml")
	return ""
}

// bodyXML extracts the <w:body>…</w:body> inner content (the part we generate),
// dropping the closing <w:sectPr> which we copy verbatim from the template.
func bodyXML(s string) string {
	open := strings.Index(s, "<w:body")
	if open < 0 {
		return s
	}
	open = strings.IndexByte(s[open:], '>') + open + 1
	close := strings.LastIndex(s, "</w:body>")
	inner := s[open:close]
	if sect := strings.LastIndex(inner, "<w:sectPr"); sect >= 0 {
		inner = inner[:sect]
	}
	return inner
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
			mine, err := Export(fsource.Parse(string(src), "chgk"), nil)
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

// stripSrcSz removes the source/author font-size run props so the body parity
// check below still locks in everything else. Applied to both sides: xy emits
// sz+szCs, chgksuite (python-docx) emits sz only, and older oracles none.
func stripSrcSz(s string) string {
	for _, frag := range []string{`<w:sz w:val="20"/>`, `<w:szCs w:val="20"/>`} {
		s = strings.ReplaceAll(s, frag, "")
	}
	s = strings.ReplaceAll(s, "<w:rPr></w:rPr>", "")
	s = strings.ReplaceAll(s, "<w:rPr/>", "")
	return strings.ReplaceAll(s, "<w:r></w:r>", "<w:r/>") // empty token run, size-stripped
}

// TestDocxBodyParity compares the generated <w:body> XML byte-for-byte against
// chgksuite's, which locks in paragraph spacing (keepLines/keepNext/spacing),
// run boundaries (python-docx's br-inside-run + conditional xml:space), and
// hyperlink markup — the formatting the text-only parity test can't see.
func TestDocxBodyParity(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.4s")
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".4s")
		t.Run(name, func(t *testing.T) {
			oracle, err := os.ReadFile(filepath.Join("testdata", name+".docx"))
			if err != nil {
				t.Skipf("no oracle: %v", err)
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			mine, err := Export(fsource.Parse(string(src), "chgk"), nil)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			want := stripSrcSz(bodyXML(documentXML(t, oracle)))
			got := stripSrcSz(bodyXML(documentXML(t, mine)))
			if want != got {
				t.Errorf("body XML mismatch for %s\n--- chgksuite ---\n%s\n--- go ---\n%s", name, want, got)
			}
		})
	}
}
