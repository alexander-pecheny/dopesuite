package typstdoc_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/handout"
	"xy/internal/chgk/typstdoc"
	"xy/internal/chgk/typstwasm"
)

const sample = `### Тестовый пакет

## Тур 1

? Вопрос _с курсивом_ и __жирным__ про то, что из-за этого не ломается.
! Ответ
= Зачёт
/ Комментарий с ссылкой https://example.com/a_b~c
^ Источник
@ Автор
`

func parse(t *testing.T, src string) fsource.Doc {
	t.Helper()
	doc := fsource.Parse(src, "chgk")
	if len(doc) == 0 {
		t.Fatal("parsed to nothing")
	}
	return doc
}

// The layout properties the docx export gets from Word's paragraph flags have to
// survive into the typst source, or a question splits across a page.
func TestGenerateTypLayout(t *testing.T) {
	typ := typstdoc.GenerateTyp(parse(t, sample), nil)

	for _, want := range []string{
		`#set text(font: "Noto Sans"`,
		"hyphenate: false",     // docx: suppressAutoHyphens
		"breakable: false",     // docx: keepLines on question/answer/source
		"sticky: true",         // docx: keepNext on headings
		`weight: "bold"`,       // the labels ("Вопрос 1. ", "Ответ: ")
		`style: "italic"`,      // _с курсивом_
		`link("https://`,       // the source's URL
		`underline(text(fill:`, // …styled like the docx Hyperlink style
	} {
		if !strings.Contains(typ, want) {
			t.Errorf("generated source is missing %q\n---\n%s", want, typ)
		}
	}
}

// The non-breaking pass is the whole point of sharing internal/chgk/inline with the
// docx exporter: the same prepositions glue, and the same short hyphenated words get
// a non-breaking hyphen.
func TestGenerateTypNoBreak(t *testing.T) {
	typ := typstdoc.GenerateTyp(parse(t, sample), nil)
	if !strings.Contains(typ, "из‑за") {
		t.Error("short hyphenated word did not get a non-breaking hyphen (U+2011)")
	}
	if !strings.Contains(typ, " ") {
		t.Error("no non-breaking space was glued anywhere")
	}
	// …but not inside a URL, which the gluing skips.
	if strings.Contains(typ, "https://example.com/a ") {
		t.Error("the URL was mangled by the nbsp pass")
	}
}

// Editorial text is data, never typst code: a question full of typst syntax must
// come out as those characters, not as markup (nor as a compile error).
func TestGenerateTypEscaping(t *testing.T) {
	doc := parse(t, "? #let x = 1 $e^x$ \"кавычки\" и #[markup]\n! Ответ\n")
	typ := typstdoc.GenerateTyp(doc, nil)
	if !strings.Contains(typ, `\"кавычки\"`) {
		t.Errorf("quotes were not escaped into the string literal:\n%s", typ)
	}
	if !strings.Contains(typ, `#let x = 1 $e^x$`) {
		t.Errorf("typst-looking text did not survive verbatim:\n%s", typ)
	}
	// The only '#' that starts a typst expression is the one opening a block/pagebreak.
	for _, line := range strings.Split(typ, "\n") {
		if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#set ") &&
			!strings.HasPrefix(line, "#block(") && !strings.HasPrefix(line, "#pagebreak(") {
			t.Errorf("unexpected top-level typst expression: %q", line)
		}
	}
}

// A (PAGEBREAK) inside a question can't stay inside the block — typst rejects a
// pagebreak in a container — so the paragraph splits around it.
func TestGenerateTypPageBreak(t *testing.T) {
	typ := typstdoc.GenerateTyp(parse(t, "? До(PAGEBREAK)после\n! Ответ\n"), nil)
	i := strings.Index(typ, "#pagebreak(")
	if i < 0 {
		t.Fatalf("no pagebreak emitted:\n%s", typ)
	}
	before, after := typ[:i], typ[i:]
	if !strings.Contains(before, "До") || !strings.Contains(after, "после") {
		t.Errorf("the paragraph did not split around the page break:\n%s", typ)
	}
}

// Noto Sans has no `smcp` feature, so small caps are synthesized (uppercase +
// smaller), the way Word synthesizes w:smallCaps.
//
// (sc…) only becomes its own run when it directly follows another token — a quirk
// of chgksuite's tokenizer that the shared inline package reproduces — hence the
// (LINEBREAK) in front of it here.
func TestGenerateTypSmallCaps(t *testing.T) {
	typ := typstdoc.GenerateTyp(parse(t, "? Имя(LINEBREAK)(scВасилий)\n! Ответ\n"), nil)
	if !strings.Contains(typ, "size: 0.8em") || !strings.Contains(typ, "АСИЛИЙ") {
		t.Errorf("small caps were not synthesized:\n%s", typ)
	}
}

// End-to-end: the same typst (wasm) pool the server uses turns the source into a
// real PDF. This is what catches a typst syntax error in a generated expression.
func TestExportPDF(t *testing.T) {
	fonts, err := handout.BundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("XY_WASM_CACHE")
	if cache == "" {
		cache = filepath.Join(os.TempDir(), "xy-wazero-cache")
	}
	pool, err := typstwasm.NewPool(context.Background(), fonts, cache, 1)
	if err != nil {
		t.Fatalf("typst pool: %v", err)
	}
	defer pool.Close()

	// Everything at once: headings, markup, an image, a link, a page break, small
	// caps, a numbered source list — if any expression is malformed, typst says so.
	src := sample + `
? Вопрос с картинкой:
(img w=200 pic.png)
И (scкапителью), и ~зачёркнутым~, и (PAGEBREAK)разрывом.
! Ответ
^
- Первый источник
- https://example.com/second
`
	pdf, err := typstdoc.Export(context.Background(), parse(t, src),
		map[string][]byte{"pic.png": testPNG(t)}, pool)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("not a PDF (%d bytes)", len(pdf))
	}
	t.Logf("rendered %d-byte PDF", len(pdf))
}

// testPNG is a 2×1 PNG (the smallest thing the image pipeline will accept).
func testPNG(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
