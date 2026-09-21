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
		for _, m := range reWT.FindAllStringSubmatch(plainNoBreakHyphen(string(data)), -1) {
			b.WriteString(unescapeXML(m[1]))
		}
		return b.String()
	}
	t.Fatal("no document.xml")
	return ""
}

// reNBHyphenEl is the element xy writes for a non-breaking hyphen, with the
// <w:t> it interrupts.
var reNBHyphenEl = regexp.MustCompile(`</w:t><w:noBreakHyphen/><w:t(?: xml:space="preserve")?>`)

// plainNoBreakHyphen writes both spellings of the non-breaking hyphen as U+2011,
// so the parity checks can compare everything else. chgksuite fences a plain
// hyphen with word joiners, which Noto Sans draws 0.6em wide; xy emits
// <w:noBreakHyphen/>, and that ends the run's current <w:t> (issue #87, and
// nbHyphenRune in docx.go).
func plainNoBreakHyphen(s string) string {
	s = reNBHyphenEl.ReplaceAllString(s, "\u2011")
	// A hyphen at either end of a run has no <w:t> on that side to merge into.
	s = strings.ReplaceAll(s, "<w:noBreakHyphen/>", "<w:t>\u2011</w:t>")
	return strings.ReplaceAll(s, "\u2060-\u2060", "\u2011")
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
			mine, err := Export(fsource.Parse(string(src), "chgk"), nil, Options{})
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

// stripSrcSz removes xy's deliberate deviations — the 10pt source/author run
// props, the compensating paragraph gap, the author-without-source paragraph
// split and the spelling of the non-breaking hyphen — so the body parity check
// below still locks in everything else. Applied to both sides: xy emits
// sz+szCs, chgksuite (python-docx) emits sz only, and older oracles none.
func stripSrcSz(s string) string {
	s = plainNoBreakHyphen(s)
	for _, frag := range []string{`<w:sz w:val="20"/>`, `<w:szCs w:val="20"/>`, `<w:spacing w:before="46"/>`} {
		s = strings.ReplaceAll(s, frag, "")
	}
	s = strings.ReplaceAll(s, "<w:rPr></w:rPr>", "")
	s = strings.ReplaceAll(s, "<w:rPr/>", "")
	// Collapse the (spacing-stripped) source/author paragraph boundary to the
	// in-paragraph line break older oracles used for author-without-source.
	s = strings.ReplaceAll(s, "</w:p><w:p><w:pPr><w:keepLines/></w:pPr>", "<w:r><w:br/></w:r>")
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
			mine, err := Export(fsource.Parse(string(src), "chgk"), nil, Options{})
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

// TestDocxSIParity is the same two checks on the СИ layout: themes, battles,
// rounds and the theme-level author/comment, against chgksuite's own output for
// the same source read as a .si4s (which is how its CLI picks the game up).
func TestDocxSIParity(t *testing.T) {
	files, _ := filepath.Glob("testdata/si/*.4s")
	if len(files) == 0 {
		t.Skip("no si testdata")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".4s")
		t.Run(name, func(t *testing.T) {
			oracle, err := os.ReadFile(filepath.Join("testdata", "si", name+".docx"))
			if err != nil {
				t.Skipf("no oracle: %v", err)
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			mine, err := Export(fsource.Parse(string(src), "si"), nil, Options{Game: "si"})
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			if want, got := docText(t, oracle), docText(t, mine); want != got {
				t.Errorf("text mismatch\n--- chgksuite ---\n%q\n--- go ---\n%q", want, got)
			}
			want := stripSrcSz(bodyXML(documentXML(t, oracle)))
			got := stripSrcSz(bodyXML(documentXML(t, mine)))
			if want != got {
				t.Errorf("body XML mismatch\n--- chgksuite ---\n%s\n--- go ---\n%s", want, got)
			}
		})
	}
}

// TestNoBreakHyphenElement pins issue #87: a glued hyphen reaches the .docx as
// <w:noBreakHyphen/>, which every reader draws with the font's own hyphen glyph.
// Neither character that would need font coverage is written out — not U+2011
// itself, and not the word joiners chgksuite fences a plain hyphen with.
func TestNoBreakHyphenElement(t *testing.T) {
	src := "### Турнир\n\n## Тур\n\n? Из-за чего появляются пробелы?\n! хз\n"
	mine, err := Export(fsource.Parse(src, "chgk"), nil, Options{})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	xml := documentXML(t, mine)
	if !strings.Contains(xml, "<w:t>Из</w:t><w:noBreakHyphen/><w:t>за") {
		t.Errorf("the glued hyphen is not a noBreakHyphen element:\n%s", bodyXML(xml))
	}
	for _, c := range []string{"\u2060", "\u2011"} {
		if strings.Contains(xml, c) {
			t.Errorf("%q is in the document:\n%s", c, bodyXML(xml))
		}
	}
}
