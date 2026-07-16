// Package typstdoc renders a parsed 4s document to PDF with typst, laying it out
// to look like the .docx the docx package produces.
//
// It is the same document twice, in two formats: the two exporters share the whole
// text pipeline (internal/chgk/inline — 4s markup, backtick accents, non-breaking
// spaces and hyphens) and the same image sizing, and this package's preamble
// reproduces chgksuite's template.docx page setup — A4, 1"/0.75" margins, 12pt
// body, 16pt/14pt headings, blue underlined links, no auto-hyphenation, a page
// number bottom-left. The one deliberate substitution is the typeface: the docx
// asks for Arial, which we cannot ship, so the PDF uses the Noto Sans faces already
// embedded for handouts (also a neo-grotesque, so the page reads the same).
//
// Word's paragraph-level "keep" flags map onto typst blocks:
//
//	w:keepLines (question / answer / source)  → block(breakable: false)
//	w:keepNext  (headings)                    → block(sticky: true)
//	w:pageBreakBefore                         → #pagebreak(weak: true)
//
// so a question never straddles a page break in either format.
//
// Everything is emitted in typst *code* mode — content is built out of text("…")
// calls whose arguments are typst string literals — so no editorial text is ever
// interpreted as typst markup. A question containing "#let x = 1" or "$e^x$" is
// just those characters on the page.
package typstdoc

import (
	"context"
	"fmt"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/inline"
)

// Typesetter compiles a typst document. Structurally the same interface the
// handout package defines, so the server's shared typst (wasm) pool satisfies both
// — and, like handouts, this keeps the plaintext in memory: typst reads its source
// and images from RAM, never from a file.
type Typesetter interface {
	SetImages(ctx context.Context, images map[string][]byte) error
	Compile(ctx context.Context, typ string, wantPDF bool) (pdf []byte, pages int, err error)
}

// labels (labels_ru.toml question_labels) — the same set the docx export uses.
var labels = map[string]string{
	"question": "Вопрос", "answer": "Ответ", "zachet": "Зачёт", "nezachet": "Незачёт",
	"comment": "Комментарий", "source": "Источник", "sources": "Источники",
	"author": "Автор", "handout": "Раздаточный материал",
}

// Page setup, transcribed from template.docx (twips → mm/pt).
const (
	marginV     = "25.4mm"  // w:top / w:bottom = 1440tw
	marginH     = "19.05mm" // w:left / w:right = 1080tw
	bodyPt      = 12.0      // Normal: sz 24 half-points
	h1Pt        = 16.0      // Heading1: sz 32
	h2Pt        = 14.0      // Heading2: sz 28
	headingPt   = 12.0      // Heading{1,2} w:spacing w:before=240tw
	headingBelo = 3.0       // …w:after=60tw
	questionPt  = 18.0      // question paragraph w:before=360tw
	answerPt    = 6.0       // answer paragraph w:before=120tw
	srcPt       = 10.0      // source/author runs: 2pt below body (matches the docx export)
	srcGapPt    = 2.72      // the shrunk block starts one BODY line below: 2pt × Noto Sans's 1.362em line box (asc 1.069 + desc 0.293)
	linkColor   = "#0000ff" // Hyperlink character style
	tabWidth    = "36pt"    // Word's default tab stop (0.5in)
	fontFamily  = "Noto Sans"
)

// Export renders the parsed structure to PDF bytes. images maps the names used in
// (img …) directives to their bytes (any format; re-encoded to PNG).
func Export(ctx context.Context, doc fsource.Doc, images map[string][]byte, ts Typesetter) ([]byte, error) {
	e := &exporter{images: images, used: map[string][]byte{}}
	src := e.generate(doc)
	if err := ts.SetImages(ctx, e.used); err != nil {
		return nil, err
	}
	pdf, _, err := ts.Compile(ctx, src, true)
	if err != nil {
		return nil, fmt.Errorf("typst compile failed: %w", err)
	}
	return pdf, nil
}

// GenerateTyp returns the typst source for a document, without compiling it (the
// unit-testable half; also what you want when a PDF comes out looking wrong).
// Referenced images are resolved, so their sizes land in the source.
func GenerateTyp(doc fsource.Doc, images map[string][]byte) string {
	e := &exporter{images: images, used: map[string][]byte{}}
	return e.generate(doc)
}

type exporter struct {
	images map[string][]byte
	used   map[string][]byte // images actually referenced, keyed by the name the source uses
}

// preamble is template.docx's page setup, in typst.
//
// top-edge/bottom-edge and leading are load-bearing, not taste. By default typst
// measures a line box from cap-height to baseline, so a block's height leaves out
// its descenders — fine when blocks are separated by par.spacing, ruinous here,
// because Word's paragraphs are flush (spacing before = 0) and the exporter
// reproduces that: consecutive blocks then overlap by a descender, and the source
// line lands on top of the comment above it. Measuring the full ascender→descender
// line box makes flush blocks sit flush. Dropping leading to 0 keeps the line
// advance where typst's default had it (≈1.36em: the ascender and descender we just
// took in are what the 0.65em leading used to add), i.e. Word's single spacing.
func (e *exporter) preamble() string {
	return fmt.Sprintf(`#set page(paper: "a4", margin: (top: %s, bottom: %s, left: %s, right: %s), footer: context align(left, text(size: %s, counter(page).display())))
#set text(font: %q, size: %s, lang: "ru", hyphenate: false, top-edge: "ascender", bottom-edge: "descender")
#set par(spacing: 0pt, leading: 0pt, justify: false)
`, marginV, marginV, marginH, marginH, pt(bodyPt), fontFamily, pt(bodyPt))
}

func (e *exporter) generate(doc fsource.Doc) string {
	var out strings.Builder
	out.WriteString(e.preamble())

	firstSection := true // only sections after the first get a page break
	headingPB := false   // sticky page_break_before_heading
	first := true        // nothing emitted yet
	prevType := ""

	for _, el := range doc {
		switch el.Type {
		case "meta":
			p := &para{}
			if prevType == "Question" {
				p.above = questionPt
			}
			e.addValue(p, el.Content, true)
			out.WriteString(p.typ())
			out.WriteString(emptyLine()) // docx follows meta with an empty paragraph

		case "heading", "ljheading", "section", "editor", "date":
			p := &para{above: headingPt, below: headingBelo, sticky: true, size: bodyPt}
			switch el.Type {
			case "heading":
				p.size, p.bold = h1Pt, true
				if !first {
					headingPB = true
				}
				p.pageBreak = headingPB
			case "section":
				p.size, p.bold, p.italic = h2Pt, true, true
				p.pageBreak = !firstSection
				firstSection = false
			}
			e.addValue(p, el.Content, true)
			p.addBreak()
			out.WriteString(p.typ())

		case "Question":
			if q, ok := el.Content.(*fsource.Question); ok {
				out.WriteString(e.renderQuestion(q))
			}

		default:
			// battle/round/theme/number/setcounter etc. — not used by xy exports
			continue
		}
		first = false
		prevType = el.Type
	}
	return out.String()
}

// renderQuestion mirrors docx.renderQuestion: the question (label + handout +
// text), the answer (with zachet/nezachet/comment glued on), and the shared
// source/author paragraph — smaller type, spaced to start one body line below —
// each a keep-together paragraph.
func (e *exporter) renderQuestion(q *fsource.Question) string {
	var out strings.Builder

	p1 := &para{above: questionPt, keepLines: true}
	p1.addStyled(questionLabel(q)+". ", "bold")
	if h := q.Get("handout"); h != nil {
		p1.addStyled("\n["+labelFor(q, "handout")+": ", "")
		e.addValue(p1, h, false)
		p1.addStyled("\n]", "")
	}
	p1.addBreak()
	e.addValue(p1, q.Get("question"), true)
	out.WriteString(p1.typ())

	p2 := &para{above: answerPt, keepLines: true}
	p2.addStyled(labelFor(q, "answer")+": ", "bold")
	e.addValue(p2, q.Get("answer"), true)

	var src *para
	for _, field := range []string{"zachet", "nezachet", "comment", "source", "author"} {
		v := q.Get(field)
		if v == nil {
			continue
		}
		nbsp := field != "source"
		if field == "source" || field == "author" {
			if src == nil {
				src = &para{keepLines: true, runSize: srcPt, above: srcGapPt}
			} else {
				src.addBreak()
			}
			src.addStyled(labelFor(q, field)+": ", "bold")
			e.addValue(src, v, nbsp)
			continue
		}
		p2.addBreak()
		p2.addStyled(labelFor(q, field)+": ", "bold")
		e.addValue(p2, v, nbsp)
	}
	out.WriteString(p2.typ())
	if src != nil {
		out.WriteString(src.typ())
	}
	return out.String()
}

func questionLabel(q *fsource.Question) string {
	num := ""
	if n := q.Get("number"); n != nil {
		num = fmt.Sprintf("%v", n)
	}
	return labelFor(q, "question") + " " + num
}

// labelFor returns the field label, honouring per-question overrides and the
// plural "Источники" when source is a list.
func labelFor(q *fsource.Question, field string) string {
	if ov, ok := q.Get("overrides").(map[string]string); ok {
		if v, ok := ov[field]; ok {
			return v
		}
	}
	if field == "source" {
		if _, isList := q.Get("source").([]any); isList {
			return labels["sources"]
		}
	}
	return labels[field]
}

// addValue renders a field value (string or list), mirroring docx.addValue: the
// [preamble, [items…]] form renders the preamble then a numbered list; a flat list
// renders just the numbered items.
func (e *exporter) addValue(p *para, v any, nbsp bool) {
	switch val := v.(type) {
	case string:
		e.addRuns(p, val, nbsp)
	case []any:
		if len(val) >= 2 {
			if items, ok := val[1].([]any); ok {
				e.addRuns(p, fmt.Sprintf("%v", val[0]), nbsp)
				for i, it := range items {
					p.addStyled(fmt.Sprintf("\n%d. ", i+1), "")
					e.addRuns(p, fmt.Sprintf("%v", it), nbsp)
				}
				return
			}
		}
		for i, it := range val {
			p.addStyled(fmt.Sprintf("\n%d. ", i+1), "")
			e.addRuns(p, fmt.Sprintf("%v", it), nbsp)
		}
	}
}

// addRuns tokenizes inline 4s markup and appends one content expression per token.
func (e *exporter) addRuns(p *para, text string, nbsp bool) {
	for _, r := range inline.Parse4sElem(text) {
		switch r.Kind {
		case "linebreak":
			p.addBreak()
		case "pagebreak":
			p.addPageBreak()
		case "img":
			e.addImage(p, r.Text)
		case "screen":
			p.addContent(r.ForPrint, "", nbsp)
		case "hyperlink":
			p.addLink(r.Text)
		default:
			p.addContent(r.Text, r.Kind, nbsp)
		}
	}
}
