// Package docx is a Go port of chgksuite's DocxExporter (the non-screen,
// spoilers-off "host" docx ChGK editors export). It renders a parsed fsource.Doc
// to a .docx by generating word/document.xml and repackaging chgksuite's
// template.docx (reused verbatim for its named styles / page setup). Inline 4s
// markup and the non-breaking-space gluing are ported from the validated xy
// client logic (chgk.js). Images referenced by (img …) are re-encoded to PNG and
// embedded (see images.go). See docx_test.go for parity checks against
// chgksuite's own `compose docx` output.
//
// The run/paragraph emission deliberately mirrors python-docx (which chgksuite
// drives): a run's text is split on "\n"/"\t" into <w:br/>/<w:tab/> *inside* one
// <w:r>; xml:space="preserve" is emitted only when text has leading/trailing
// whitespace; empty text tokens become a bare <w:r/>. Paragraph properties
// (keepLines/keepNext/pageBreakBefore/spacing) match chgksuite's spacing model.
//
// Not ported (rare for xy): screen-mode versions and PDF/size optimization.
package docx

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"strings"

	"xy/internal/chgk/fsource"
)

//go:embed assets/template.docx
var templateDocx []byte

// labels (labels_ru.toml question_labels).
var labels = map[string]string{
	"question": "Вопрос", "answer": "Ответ", "zachet": "Зачёт", "nezachet": "Незачёт",
	"comment": "Комментарий", "source": "Источник", "sources": "Источники",
	"author": "Автор", "handout": "Раздаточный материал",
}

const (
	imageRelType     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	hyperlinkRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	// NO_BREAK_HYPHEN_REPLACEMENT (docx.py): word-joiner + hyphen + word-joiner.
	noBreakHyphenRepl = "⁠-⁠"
)

// relItem is a relationship appended to word/_rels/document.xml.rels in document
// order (images and hyperlinks share one rId sequence starting after the
// template's rId1–6, matching python-docx).
type relItem struct {
	id, typ, target string
	external        bool
}

// exporter holds the per-export image/hyperlink collectors. Relationship ids
// start at 7 (after the template's rId1–6); drawing object ids start high to
// avoid colliding with anything in the template.
type exporter struct {
	images  map[string][]byte // referenced image name → bytes (any format)
	media   []mediaItem       // image parts, written into the docx
	rels    []relItem         // image + hyperlink relationships, in document order
	nextRel int
	nextDoc int
}

// Export renders the parsed structure to .docx bytes. images maps the names used
// in (img …) directives to their bytes (any format; re-encoded to PNG).
func Export(doc fsource.Doc, images map[string][]byte) ([]byte, error) {
	e := &exporter{images: images, nextRel: 7, nextDoc: 1000}
	body := e.renderBody(doc)
	return e.repackage(body)
}

// ── paragraph builder (mirrors a python-docx paragraph) ──

type para struct {
	style           string // "", "Normal", "Heading1", "Heading2"
	keepNext        bool
	keepLines       bool
	pageBreakBefore bool
	spacingBefore   int  // twips; 0 = none
	lang            bool // template para0 carries <w:rPr><w:lang w:val="en-US"/>
	runs            []string
}

// pPr child order follows the OOXML CT_PPr schema (pStyle, keepNext, keepLines,
// pageBreakBefore, spacing, …, rPr last).
func (p *para) xml() string {
	var ppr strings.Builder
	if p.style != "" {
		ppr.WriteString(`<w:pStyle w:val="` + p.style + `"/>`)
	}
	if p.keepNext {
		ppr.WriteString("<w:keepNext/>")
	}
	if p.keepLines {
		ppr.WriteString("<w:keepLines/>")
	}
	if p.pageBreakBefore {
		ppr.WriteString("<w:pageBreakBefore/>")
	}
	if p.spacingBefore > 0 {
		ppr.WriteString(fmt.Sprintf(`<w:spacing w:before="%d"/>`, p.spacingBefore))
	}
	if p.lang {
		ppr.WriteString(`<w:rPr><w:lang w:val="en-US"/></w:rPr>`)
	}
	var b strings.Builder
	b.WriteString("<w:p>")
	if ppr.Len() > 0 {
		b.WriteString("<w:pPr>" + ppr.String() + "</w:pPr>")
	}
	for _, r := range p.runs {
		b.WriteString(r)
	}
	b.WriteString("</w:p>")
	return b.String()
}

// addRaw appends a run for verbatim text (mirrors python-docx paragraph.add_run
// for labels / list markers / "\n" separators — no nbsp/backtick processing).
func (p *para) addRaw(text, kind string) {
	p.runs = append(p.runs, runXML(text, rPr(kind)))
}

// leadEmpty appends the template para0's leading empty run (<w:r><w:rPr/></w:r>).
func (p *para) leadEmpty() {
	p.runs = append(p.runs, "<w:r><w:rPr/></w:r>")
}

// addContent appends a run for editorial text, mirroring set_docx_run_text:
// backtick accents, optional nbsp gluing, then the non-breaking-hyphen swap.
func (e *exporter) addContent(p *para, text, kind string, nbsp bool) {
	text = backtickReplace(text)
	if nbsp {
		text = replaceNoBreak(text)
	}
	text = strings.ReplaceAll(text, nbHyphen, noBreakHyphenRepl)
	p.runs = append(p.runs, runXML(text, rPr(kind)))
}

// addHyperlink appends a <w:hyperlink> wrapping a Hyperlink-styled run, and
// records the external relationship (URL-quoted target).
func (e *exporter) addHyperlink(p *para, urlText string) {
	relID := fmt.Sprintf("rId%d", e.nextRel)
	e.nextRel++
	e.rels = append(e.rels, relItem{id: relID, typ: hyperlinkRelType, target: urlQuote(urlText), external: true})
	text := strings.ReplaceAll(urlText, nbHyphen, noBreakHyphenRepl)
	inner := runXML(text, `<w:rPr><w:rStyle w:val="Hyperlink"/></w:rPr>`)
	p.runs = append(p.runs, `<w:hyperlink r:id="`+relID+`">`+inner+`</w:hyperlink>`)
}

// ── document body generation (DocxExporter.export loop, chgk/non-screen) ──

func (e *exporter) renderBody(doc fsource.Doc) string {
	var out []string
	paraIsNone := true   // mirrors chgksuite's `para is None`
	firstSection := true // chgk: only sections after the first page-break
	headingPB := false   // sticky page_break_before_heading
	prevType := ""

	// flushLead emits the template's leftover empty Normal para0 when the first
	// content paragraph is one that does not reuse it (meta / Question).
	flushLead := func() {
		if paraIsNone {
			p := &para{style: "Normal", lang: true}
			p.leadEmpty()
			out = append(out, p.xml())
			paraIsNone = false
		}
	}

	for _, el := range doc {
		switch el.Type {
		case "meta":
			flushLead()
			p := &para{}
			if prevType == "Question" {
				p.spacingBefore = 360
			}
			e.addValue(p, el.Content, true)
			out = append(out, p.xml())
			out = append(out, "<w:p/>") // trailing empty paragraph
			paraIsNone = false

		case "heading", "ljheading", "section", "editor", "date":
			wasNone := paraIsNone
			p := &para{keepNext: true}
			if wasNone {
				p.lang = true
				p.leadEmpty()
				paraIsNone = false
			}
			switch el.Type {
			case "heading":
				p.style = "Heading1"
				if !wasNone {
					headingPB = true
				}
				if headingPB {
					p.pageBreakBefore = true
				}
			case "section":
				p.style = "Heading2"
				if !firstSection {
					p.pageBreakBefore = true
				} else {
					firstSection = false
				}
			}
			e.addValue(p, el.Content, true)
			p.addRaw("\n", "")
			out = append(out, p.xml())

		case "Question":
			flushLead()
			if q, ok := el.Content.(*fsource.Question); ok {
				out = append(out, e.renderQuestion(q)...)
			}

		default:
			// battle/round/theme/number/setcounter etc. — not used by xy exports
		}
		prevType = el.Type
	}
	return strings.Join(out, "")
}

func (e *exporter) renderQuestion(q *fsource.Question) []string {
	var out []string

	// Paragraph 1: label [+ handout] + question text.
	p1 := &para{keepLines: true, spacingBefore: 360}
	p1.addRaw(questionLabel(q)+". ", "bold")
	if h := q.Get("handout"); h != nil {
		p1.addRaw("\n["+labelFor(q, "handout")+": ", "")
		e.addValue(p1, h, false)
		p1.addRaw("\n]", "")
	}
	p1.addRaw("\n", "")
	e.addValue(p1, q.Get("question"), true)
	out = append(out, p1.xml())

	// Paragraph 2: answer (+ zachet/nezachet/comment glued in). The source field
	// starts a fresh paragraph; author then glues onto that source paragraph.
	p2 := &para{keepLines: true, spacingBefore: 120}
	p2.addRaw(labelFor(q, "answer")+": ", "bold")
	e.addValue(p2, q.Get("answer"), true)

	var src *para
	for _, field := range []string{"zachet", "nezachet", "comment", "source", "author"} {
		v := q.Get(field)
		if v == nil {
			continue
		}
		nbsp := field != "source"
		if field == "source" {
			src = &para{keepLines: true}
			src.addRaw(labelFor(q, field)+": ", "bold")
			e.addValue(src, v, nbsp)
			continue
		}
		cur := p2
		if src != nil {
			cur = src
		}
		cur.addRaw("\n", "")
		cur.addRaw(labelFor(q, field)+": ", "bold")
		e.addValue(cur, v, nbsp)
	}
	out = append(out, p2.xml())
	if src != nil {
		out = append(out, src.xml())
	}
	return out
}

func questionLabel(q *fsource.Question) string {
	lbl := labelFor(q, "question")
	num := ""
	if n := q.Get("number"); n != nil {
		num = fmt.Sprintf("%v", n)
	}
	return lbl + " " + num
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

// addValue renders a field value (string or list) into the paragraph's runs.
func (e *exporter) addValue(p *para, v any, nbsp bool) {
	switch val := v.(type) {
	case string:
		e.addRuns(p, val, nbsp)
	case []any:
		// [preamble, [items…]] renders the preamble then a numbered list; a flat
		// list renders just the numbered items (mirrors format_docx_element).
		if len(val) >= 2 {
			if items, ok := val[1].([]any); ok {
				e.addRuns(p, fmt.Sprintf("%v", val[0]), nbsp)
				for i, it := range items {
					p.addRaw(fmt.Sprintf("\n%d. ", i+1), "")
					e.addRuns(p, fmt.Sprintf("%v", it), nbsp)
				}
				return
			}
		}
		for i, it := range val {
			p.addRaw(fmt.Sprintf("\n%d. ", i+1), "")
			e.addRuns(p, fmt.Sprintf("%v", it), nbsp)
		}
	}
}

// addRuns tokenizes inline 4s markup and appends one run per token.
func (e *exporter) addRuns(p *para, text string, nbsp bool) {
	for _, r := range parse4sElem(text) {
		switch r.Kind {
		case "linebreak":
			p.addRaw("\n", "")
		case "pagebreak":
			p.runs = append(p.runs, `<w:r><w:br w:type="page"/></w:r>`)
		case "img":
			p.runs = append(p.runs, e.embedImage(r.Text))
		case "screen":
			e.addContent(p, r.ForPrint, "", nbsp)
		case "hyperlink":
			e.addHyperlink(p, r.Text)
		default:
			e.addContent(p, r.Text, r.Kind, nbsp)
		}
	}
}

// runXML converts text into one <w:r>, splitting "\n"/"\r"→<w:br/> and
// "\t"→<w:tab/> within the run (the python-docx _RunContentAppender algorithm).
// xml:space="preserve" is added per <w:t> only when its text has leading or
// trailing whitespace; an empty run with no props serializes as <w:r/>.
func runXML(text, rpr string) string {
	var content strings.Builder
	var buf []rune
	flush := func() {
		if len(buf) == 0 {
			return
		}
		s := string(buf)
		content.WriteString("<w:t")
		if strings.TrimSpace(s) != s {
			content.WriteString(` xml:space="preserve"`)
		}
		content.WriteString(">" + xmlEscape(s) + "</w:t>")
		buf = buf[:0]
	}
	for _, r := range text {
		switch r {
		case '\t':
			flush()
			content.WriteString("<w:tab/>")
		case '\n', '\r':
			flush()
			content.WriteString("<w:br/>")
		default:
			buf = append(buf, r)
		}
	}
	flush()
	c := content.String()
	if rpr == "" && c == "" {
		return "<w:r/>"
	}
	return "<w:r>" + rpr + c + "</w:r>"
}

// rPr renders run properties. Child order follows the OOXML CT_RPr schema
// (b, i, smallCaps, strike, u).
func rPr(kind string) string {
	if kind == "" {
		return ""
	}
	var props string
	if strings.Contains(kind, "bold") {
		props += "<w:b/>"
	}
	if strings.Contains(kind, "italic") {
		props += "<w:i/>"
	}
	if kind == "sc" {
		props += "<w:smallCaps/>"
	}
	if kind == "strike" {
		props += "<w:strike/>"
	}
	if strings.Contains(kind, "underline") {
		props += `<w:u w:val="single"/>`
	}
	if props == "" {
		return ""
	}
	return "<w:rPr>" + props + "</w:rPr>"
}

func brk() string { return "<w:r><w:br/></w:r>" }

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// urlQuote mirrors urllib.parse.quote(url, safe=HYPERLINK_SAFE_CHARS): keep
// unreserved chars (alnum + "_.-~") and the hyperlink-safe set; percent-encode
// every other byte (UTF-8).
func urlQuote(s string) string {
	const safe = "%/:?#[]@!$&'()*+,;="
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '-', c == '~', strings.IndexByte(safe, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ── repackage template.docx with the generated body + embedded images ──

var reBodyOpen = regexp.MustCompile(`<w:body[^>]*>`)

func (e *exporter) repackage(body string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(templateDocx), int64(len(templateDocx)))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		switch f.Name {
		case "word/document.xml":
			data = []byte(injectBody(string(data), body))
		case "word/_rels/document.xml.rels":
			if len(e.rels) > 0 {
				data = []byte(injectRels(string(data), e.rels))
			}
		case "[Content_Types].xml":
			if len(e.media) > 0 {
				data = []byte(injectPNGContentType(string(data)))
			}
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	// add the media parts
	for _, m := range e.media {
		w, err := zw.Create("word/" + m.partName)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(m.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// injectBody replaces the template body's content with our paragraphs, keeping
// the closing <w:sectPr> (page size / margins / header & footer refs).
func injectBody(docXML, body string) string {
	openLoc := reBodyOpen.FindStringIndex(docXML)
	closeIdx := strings.LastIndex(docXML, "</w:body>")
	if openLoc == nil || closeIdx < 0 {
		return docXML
	}
	inner := docXML[openLoc[1]:closeIdx]
	sect := ""
	if s := strings.LastIndex(inner, "<w:sectPr"); s >= 0 {
		if end := strings.Index(inner[s:], "</w:sectPr>"); end >= 0 {
			sect = inner[s : s+end+len("</w:sectPr>")]
		} else {
			sect = inner[s:]
		}
	}
	return docXML[:openLoc[1]] + body + sect + docXML[closeIdx:]
}

func injectRels(rels string, items []relItem) string {
	var add strings.Builder
	for _, m := range items {
		if m.external {
			fmt.Fprintf(&add, `<Relationship Id="%s" Type="%s" Target="%s" TargetMode="External"/>`, m.id, m.typ, xmlEscape(m.target))
		} else {
			fmt.Fprintf(&add, `<Relationship Id="%s" Type="%s" Target="%s"/>`, m.id, m.typ, xmlEscape(m.target))
		}
	}
	return strings.Replace(rels, "</Relationships>", add.String()+"</Relationships>", 1)
}

func injectPNGContentType(ct string) string {
	if strings.Contains(ct, `Extension="png"`) {
		return ct
	}
	return strings.Replace(ct, "</Types>", `<Default Extension="png" ContentType="image/png"/></Types>`, 1)
}
