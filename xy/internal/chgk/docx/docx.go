// Package docx is a Go port of chgksuite's DocxExporter (the non-screen,
// spoilers-off "host" docx ChGK editors export). It renders a parsed fsource.Doc
// to a .docx by generating word/document.xml and repackaging chgksuite's
// template.docx (reused verbatim for its named styles / page setup). Inline 4s
// markup and the non-breaking-space gluing are ported from the validated xy
// client logic (chgk.js). Images referenced by (img …) are re-encoded to PNG and
// embedded (see images.go). See docx_test.go for text-parity checks against
// chgksuite's own `compose docx` output.
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

// exporter holds the per-export image collector. Image relationship and docPr
// ids start high to avoid colliding with the template's existing rIds (1–6).
type exporter struct {
	images  map[string][]byte // referenced image name → bytes (any format)
	media   []mediaItem       // collected, written into the docx
	nextRel int
	nextDoc int
}

// Export renders the parsed structure to .docx bytes. images maps the names used
// in (img …) directives to their bytes (any format; re-encoded to PNG).
func Export(doc fsource.Doc, images map[string][]byte) ([]byte, error) {
	e := &exporter{images: images, nextRel: 1000, nextDoc: 1000}
	body := e.renderBody(doc)
	return e.repackage(body)
}

// ── document body generation (DocxExporter.export loop, chgk/non-screen) ──

func (e *exporter) renderBody(doc fsource.Doc) string {
	var b strings.Builder
	firstTour := true
	for _, p := range doc {
		switch p.Type {
		case "meta":
			b.WriteString(para("", e.renderValue(p.Content, true)))
			b.WriteString("<w:p/>") // chgksuite adds a trailing empty paragraph
		case "heading", "ljheading":
			style := ""
			if p.Type == "heading" {
				style = "Heading1"
			}
			b.WriteString(para(style, e.renderValue(p.Content, true)+brk()))
		case "section":
			pb := !firstTour
			firstTour = false
			b.WriteString(paraEx("Heading2", e.renderValue(p.Content, true)+brk(), pb))
		case "editor", "date":
			b.WriteString(para("", e.renderValue(p.Content, true)+brk()))
		case "Question":
			if q, ok := p.Content.(*fsource.Question); ok {
				b.WriteString(e.renderQuestion(q))
			}
		default:
			// battle/round/theme/number/setcounter etc. — not used by xy exports
		}
	}
	return b.String()
}

func (e *exporter) renderQuestion(q *fsource.Question) string {
	var p1 strings.Builder
	p1.WriteString(boldRun(questionLabel(q) + ". "))
	if h := q.Get("handout"); h != nil {
		p1.WriteString(brk())
		p1.WriteString(plainRun("[" + labelFor(q, "handout") + ": "))
		p1.WriteString(e.renderValue(h, false))
		p1.WriteString(brk())
		p1.WriteString(plainRun("]"))
	}
	p1.WriteString(brk())
	p1.WriteString(e.renderValue(q.Get("question"), true))

	out := para("", p1.String())

	var p2 strings.Builder
	p2.WriteString(boldRun(labelFor(q, "answer") + ": "))
	p2.WriteString(e.renderValue(q.Get("answer"), true))

	srcPara := "" // source starts a fresh paragraph
	for _, field := range []string{"zachet", "nezachet", "comment", "source", "author"} {
		v := q.Get(field)
		if v == nil {
			continue
		}
		nbsp := field != "source"
		seg := boldRun(labelFor(q, field)+": ") + e.renderValue(v, nbsp)
		if field == "source" {
			srcPara = seg
		} else if srcPara != "" {
			srcPara += brk() + seg
		} else {
			p2.WriteString(brk())
			p2.WriteString(seg)
		}
	}
	out += para("", p2.String())
	if srcPara != "" {
		out += para("", srcPara)
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

// renderValue renders a field value (string or list) to run XML.
func (e *exporter) renderValue(v any, nbsp bool) string {
	switch val := v.(type) {
	case string:
		return e.renderRuns(val, nbsp)
	case []any:
		preamble := ""
		var items []any
		if len(val) == 2 {
			if _, ok := val[1].([]any); ok {
				preamble, _ = val[0].(string)
				items = val[1].([]any)
			}
		}
		if items == nil {
			items = val // no-preamble list of strings
		}
		var b strings.Builder
		if preamble != "" {
			b.WriteString(e.renderRuns(preamble, nbsp))
		}
		for i, it := range items {
			b.WriteString(brk())
			b.WriteString(plainRun(fmt.Sprintf("%d. ", i+1)))
			b.WriteString(e.renderRuns(fmt.Sprintf("%v", it), nbsp))
		}
		return b.String()
	}
	return ""
}

// renderRuns tokenizes inline 4s markup and emits run XML.
func (e *exporter) renderRuns(text string, nbsp bool) string {
	var b strings.Builder
	for _, r := range parse4sElem(text) {
		switch r.Kind {
		case "linebreak":
			b.WriteString(brk())
		case "pagebreak":
			b.WriteString(`<w:r><w:br w:type="page"/></w:r>`)
		case "img":
			b.WriteString(e.embedImage(r.Text))
		case "screen":
			b.WriteString(emitText(r.ForPrint, nbsp, ""))
		case "hyperlink":
			b.WriteString(emitText(r.Text, false, ""))
		default:
			b.WriteString(emitText(r.Text, nbsp, r.Kind))
		}
	}
	return b.String()
}

// emitText writes one run with the given style kind, applying nbsp gluing and
// backtick stress accents, then splitting on newlines into <w:br/>.
func emitText(text string, nbsp bool, kind string) string {
	text = backtickReplace(text)
	if nbsp {
		text = replaceNoBreak(text)
	}
	// chgksuite renders a non-breaking hyphen (U+2011) as word-joiner + hyphen +
	// word-joiner (docx.py NO_BREAK_HYPHEN_REPLACEMENT).
	text = strings.ReplaceAll(text, "‑", "⁠-⁠")
	rpr := rPr(kind)
	parts := strings.Split(text, "\n")
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(brk())
		}
		if part == "" {
			continue
		}
		b.WriteString("<w:r>" + rpr + `<w:t xml:space="preserve">` + xmlEscape(part) + "</w:t></w:r>")
	}
	return b.String()
}

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
	if strings.Contains(kind, "underline") {
		props += `<w:u w:val="single"/>`
	}
	if kind == "strike" {
		props += "<w:strike/>"
	}
	if kind == "sc" {
		props += "<w:smallCaps/>"
	}
	if props == "" {
		return ""
	}
	return "<w:rPr>" + props + "</w:rPr>"
}

func boldRun(text string) string {
	return "<w:r><w:rPr><w:b/></w:rPr>" + `<w:t xml:space="preserve">` + xmlEscape(text) + "</w:t></w:r>"
}

func plainRun(text string) string {
	return "<w:r>" + `<w:t xml:space="preserve">` + xmlEscape(text) + "</w:t></w:r>"
}

func brk() string { return "<w:r><w:br/></w:r>" }

func para(style, inner string) string { return paraEx(style, inner, false) }

func paraEx(style, inner string, pageBreakBefore bool) string {
	var ppr string
	if style != "" || pageBreakBefore {
		ppr = "<w:pPr>"
		if style != "" {
			ppr += `<w:pStyle w:val="` + style + `"/>`
		}
		if pageBreakBefore {
			ppr += "<w:pageBreakBefore/>"
		}
		ppr += "</w:pPr>"
	}
	return "<w:p>" + ppr + inner + "</w:p>"
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
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
			if len(e.media) > 0 {
				data = []byte(injectRels(string(data), e.media))
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

func injectRels(rels string, media []mediaItem) string {
	const imgType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	var add strings.Builder
	for _, m := range media {
		add.WriteString(fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s"/>`, m.relID, imgType, m.partName))
	}
	return strings.Replace(rels, "</Relationships>", add.String()+"</Relationships>", 1)
}

func injectPNGContentType(ct string) string {
	if strings.Contains(ct, `Extension="png"`) {
		return ct
	}
	return strings.Replace(ct, "</Types>", `<Default Extension="png" ContentType="image/png"/></Types>`, 1)
}
