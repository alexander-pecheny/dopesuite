// Package docx is a Go port of chgksuite's DocxExporter (the non-screen,
// spoilers-off "host" docx ChGK editors export). It renders a parsed fsource.Doc
// to a .docx by generating word/document.xml and repackaging chgksuite's
// template.docx (reused verbatim for its named styles / page setup). Inline 4s
// markup and the non-breaking-space gluing are ported from the validated xy
// client logic (chgk.js). See docx_test.go for text-parity checks against
// chgksuite's own `compose docx` output.
//
// Not yet ported: image embedding ((img …) runs render no text, like a missing
// image), screen-mode versions, and PDF/size optimization. xy routes
// image-bearing exports accordingly (see the server handler).
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

// Export renders the parsed structure to .docx bytes.
func Export(doc fsource.Doc) ([]byte, error) {
	body := renderBody(doc)
	return repackage(body)
}

// ── document body generation (DocxExporter.export loop, chgk/non-screen) ──

func renderBody(doc fsource.Doc) string {
	var b strings.Builder
	firstHeading := true
	firstTour := true
	for i, p := range doc {
		switch p.Type {
		case "meta":
			b.WriteString(para("", renderValue(p.Content, true)))
			b.WriteString("<w:p/>") // chgksuite adds a trailing empty paragraph
		case "heading", "ljheading":
			style := ""
			if p.Type == "heading" {
				style = "Heading1"
			}
			_ = firstHeading
			firstHeading = false
			b.WriteString(para(style, renderValue(p.Content, true)+brk()))
		case "section":
			pb := !firstTour
			firstTour = false
			b.WriteString(paraEx("Heading2", renderValue(p.Content, true)+brk(), pb))
		case "editor", "date":
			b.WriteString(para("", renderValue(p.Content, true)+brk()))
		case "Question":
			if q, ok := p.Content.(*fsource.Question); ok {
				b.WriteString(renderQuestion(q))
			}
		default:
			// battle/round/theme/number/setcounter etc. — not used by xy exports
		}
		_ = i
	}
	return b.String()
}

func renderQuestion(q *fsource.Question) string {
	var p1 strings.Builder
	// "Вопрос N. " (bold)
	p1.WriteString(boldRun(questionLabel(q) + ". "))
	if h := q.Get("handout"); h != nil {
		p1.WriteString(brk())
		p1.WriteString(plainRun("[" + labelFor(q, "handout") + ": "))
		p1.WriteString(renderValue(h, false))
		p1.WriteString(brk())
		p1.WriteString(plainRun("]"))
	}
	p1.WriteString(brk())
	p1.WriteString(renderValue(q.Get("question"), true))

	out := para("", p1.String())

	// answer paragraph
	var p2 strings.Builder
	p2.WriteString(boldRun(labelFor(q, "answer") + ": "))
	p2.WriteString(renderValue(q.Get("answer"), true))

	srcPara := "" // source starts a fresh paragraph
	for _, field := range []string{"zachet", "nezachet", "comment", "source", "author"} {
		v := q.Get(field)
		if v == nil {
			continue
		}
		nbsp := field != "source"
		seg := boldRun(labelFor(q, field)+": ") + renderValue(v, nbsp)
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

// renderValue renders a field value (string or list) to run XML. nbsp applies
// the non-breaking-space gluing (everything but sources).
func renderValue(v any, nbsp bool) string {
	switch val := v.(type) {
	case string:
		return renderRuns(val, nbsp)
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
			b.WriteString(renderRuns(preamble, nbsp))
		}
		for i, it := range items {
			b.WriteString(brk())
			b.WriteString(plainRun(fmt.Sprintf("%d. ", i+1)))
			b.WriteString(renderRuns(fmt.Sprintf("%v", it), nbsp))
		}
		return b.String()
	}
	return ""
}

// renderRuns tokenizes inline 4s markup and emits run XML.
func renderRuns(text string, nbsp bool) string {
	var b strings.Builder
	for _, r := range parse4sElem(text) {
		switch r.Kind {
		case "linebreak":
			b.WriteString(brk())
		case "pagebreak":
			b.WriteString(`<w:r><w:br w:type="page"/></w:r>`)
		case "img":
			// not embedded yet — render nothing (like a missing image)
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

// ── repackage template.docx with the generated body ──

var reBodyOpen = regexp.MustCompile(`<w:body[^>]*>`)

func repackage(body string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(templateDocx), int64(len(templateDocx)))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if f.Name == "word/document.xml" {
			data = []byte(injectBody(string(data), body))
		}
		if _, err := w.Write(data); err != nil {
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
		if e := strings.Index(inner[s:], "</w:sectPr>"); e >= 0 {
			sect = inner[s : s+e+len("</w:sectPr>")]
		} else {
			sect = inner[s:] // self-closing or trailing
		}
	}
	return docXML[:openLoc[1]] + body + sect + docXML[closeIdx:]
}
