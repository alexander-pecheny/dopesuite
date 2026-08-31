// Package docx is a Go port of chgksuite's DocxExporter (the non-screen,
// spoilers-off "host" docx ChGK editors export). It renders a parsed fsource.Doc
// to a .docx by generating word/document.xml and repackaging chgksuite's
// template.docx (reused verbatim for its named styles / page setup). Inline 4s
// markup and the non-breaking-space gluing are ported from the validated xy
// client logic (chgk.js). Images referenced by (img …) are re-encoded for the size
// Word draws them at and embedded (see images.go). See docx_test.go for parity
// checks against chgksuite's own `compose docx` output.
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
	"slices"
	"strings"

	"xy/internal/chgk/fsource"
	"xy/internal/chgk/i18n"
	"xy/internal/chgk/inline"
)

//go:embed assets/template.docx
var templateDocx []byte

const (
	imageRelType     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	hyperlinkRelType = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	// NO_BREAK_HYPHEN_REPLACEMENT (docx.py): word-joiner + hyphen + word-joiner.
	noBreakHyphenRepl = "⁠-⁠"
	// srcSz: source/author runs are set 2pt below the 12pt body (half-points).
	// A deliberate deviation from chgksuite's output.
	srcSz = 20
	// srcGapTw: the shrunk source/author paragraph starts one BODY line below the
	// answer paragraph, not one small line — the 2pt shrink × Arial's 1.15em line
	// box, in twips.
	srcGapTw = 46
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
	opts    Options
	labels  i18n.Labels
	// body is the document body in order. Paragraphs are objects until the very
	// end, because a page break inside a question starts a new paragraph while
	// its caller keeps writing runs into the old one — python-docx's model, and
	// the only way spoilers=pagebreak/dots land where chgksuite puts them.
	body []bodyItem
}

// bodyItem is a paragraph or a table.
type bodyItem interface{ xml() string }

// Export renders the parsed structure to .docx bytes. images maps the names used
// in (img …) directives to their bytes (any format; re-encoded for export).
func Export(doc fsource.Doc, images map[string][]byte, opts Options) ([]byte, error) {
	labels, err := i18n.LoadLabels(opts.Language)
	if err != nil {
		return nil, err
	}
	e := &exporter{images: images, nextRel: 7, nextDoc: 1000, opts: opts, labels: labels}
	body := e.renderBody(doc)
	if opts.OptimizeSize {
		e.optimizeMedia()
	}
	return e.repackage(body)
}

// addPara appends an empty paragraph to the body and returns it.
func (e *exporter) addPara() *para {
	p := &para{}
	e.body = append(e.body, p)
	return p
}

// ── paragraph builder (mirrors a python-docx paragraph) ──

type para struct {
	style           string // "", "Normal", "Heading1", "Heading2"
	keepNext        bool
	keepLines       bool
	pageBreakBefore bool
	spacingBefore   int  // twips; 0 = none
	lang            bool // template para0 carries <w:rPr><w:lang w:val="en-US"/>
	sz              int  // run font size, half-points; 0 = style default
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
	if ppr.Len() == 0 && len(p.runs) == 0 {
		return "<w:p/>"
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
	p.runs = append(p.runs, runXML(text, rPr(kind, p.sz, false)))
}

// leadEmpty appends the template para0's leading empty run (<w:r><w:rPr/></w:r>).
func (p *para) leadEmpty() {
	p.runs = append(p.runs, "<w:r><w:rPr/></w:r>")
}

// addContent appends a run for editorial text, mirroring set_docx_run_text:
// backtick accents, optional nbsp gluing, then the non-breaking-hyphen swap.
func (e *exporter) addContent(p *para, text, kind string, o textOpts) {
	text = inline.BacktickReplace(text)
	if o.nbsp {
		text = inline.ReplaceNoBreak(text, inline.NoBreak{})
	}
	text = strings.ReplaceAll(text, inline.NBHyphen, noBreakHyphenRepl)
	p.runs = append(p.runs, runXML(text, rPr(kind, p.sz, o.whiten)))
}

// addHyperlink appends a <w:hyperlink> wrapping a Hyperlink-styled run, and
// records the external relationship (URL-quoted target).
func (e *exporter) addHyperlink(p *para, urlText string) {
	relID := e.externalRel(inline.URLQuote(urlText))
	text := strings.ReplaceAll(urlText, inline.NBHyphen, noBreakHyphenRepl)
	inner := runXML(text, `<w:rPr><w:rStyle w:val="Hyperlink"/>`+szXML(p.sz)+`</w:rPr>`)
	p.runs = append(p.runs, `<w:hyperlink r:id="`+relID+`">`+inner+`</w:hyperlink>`)
}

// externalRel returns the relationship id for a hyperlink target, minting one
// only for a target the document hasn't linked to yet — python-docx reuses an
// external relationship, and a question printed twice must not double the rels.
func (e *exporter) externalRel(target string) string {
	for _, r := range e.rels {
		if r.external && r.typ == hyperlinkRelType && r.target == target {
			return r.id
		}
	}
	id := fmt.Sprintf("rId%d", e.nextRel)
	e.nextRel++
	e.rels = append(e.rels, relItem{id: id, typ: hyperlinkRelType, target: target, external: true})
	return id
}

// ── document body generation (DocxExporter.export loop, chgk) ──

func (e *exporter) renderBody(doc fsource.Doc) string {
	paraIsNone := true   // mirrors chgksuite's `para is None`
	firstSection := true // chgk: only sections after the first page-break
	headingPB := false   // sticky page_break_before_heading
	prevType := ""

	// flushLead emits the template's leftover empty Normal para0 when the first
	// content paragraph is one that does not reuse it (meta / Question).
	flushLead := func() {
		if paraIsNone {
			p := e.addPara()
			p.style, p.lang = "Normal", true
			p.leadEmpty()
			paraIsNone = false
		}
	}

	for _, el := range doc {
		switch el.Type {
		case "meta":
			flushLead()
			p := e.addPara()
			if prevType == "Question" {
				p.spacingBefore = 360
			}
			e.addValue(p, el.Content, textOpts{nbsp: true})
			e.addPara() // trailing empty paragraph
			paraIsNone = false

		case "heading", "ljheading", "section", "editor", "date":
			wasNone := paraIsNone
			p := e.addPara()
			p.keepNext = true
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
			e.addValue(p, el.Content, textOpts{nbsp: true})
			p.addRaw("\n", "")

		case "Question":
			flushLead()
			if q, ok := el.Content.(*fsource.Question); ok {
				e.renderQuestion(q)
			}

		default:
			// battle/round/theme/number/setcounter etc. — SI's, not chgk's
		}
		prevType = el.Type
	}
	var b strings.Builder
	for _, item := range e.body {
		b.WriteString(item.xml())
	}
	return b.String()
}

// screenVersions is the pair add_versions writes, in order.
var screenVersions = []struct {
	title  string
	screen bool
}{{"Версия для ведущего", false}, {"Версия для экрана", true}}

// renderQuestion writes one question, in as many copies as the screen mode asks
// for.
func (e *exporter) renderQuestion(q *fsource.Question) {
	switch e.opts.ScreenMode {
	case ScreenAddVersionsColumns:
		e.renderQuestionColumns(q)
	case ScreenAddVersions:
		for _, v := range screenVersions {
			e.addPara()
			e.addPara().addRaw(v.title+":", "bold")
			e.renderQuestionInto(q, nil, v.screen)
		}
	case ScreenReplaceAll:
		e.renderQuestionInto(q, nil, true)
	default:
		e.renderQuestionInto(q, nil, false)
	}
}

// renderQuestionColumns puts the two copies side by side in a one-row table,
// each cell holding the whole question in a single paragraph.
func (e *exporter) renderQuestionColumns(q *fsource.Question) {
	t := &table{}
	e.body = append(e.body, t)
	for i, v := range screenVersions {
		cell := &para{}
		t.cells[i] = cell
		cell.addRaw(v.title+"\n", "bold")
		e.renderQuestionInto(q, cell, v.screen)
	}
	e.addPara()
}

// renderQuestionInto ports add_question_to_docx. into is the paragraph a table
// cell hands it (nil for the ordinary flow, where each part gets its own
// paragraph); screen says which copy of the text to print.
func (e *exporter) renderQuestionInto(q *fsource.Question, into *para, screen bool) {
	p := into
	if p == nil {
		p = e.addPara()
	}
	p.keepLines = true
	p.spacingBefore = 360

	p.addRaw(e.questionLabel(q, e.opts.OnlyQuestionNumber)+". ", "bold")
	if h := q.Get("handout"); h != nil {
		p.addRaw("\n["+e.labelFor(q, "handout")+": ", "")
		e.addValue(p, h, textOpts{removeAccents: screen, removeBrackets: screen})
		p.addRaw("\n]", "")
	}
	if !e.opts.NoParagraph {
		p.addRaw("\n", "")
	}
	e.addValue(p, q.Get("question"), textOpts{nbsp: true, removeAccents: screen, removeBrackets: screen})

	if e.opts.NoAnswers {
		return
	}

	// The answers start below whatever the spoiler puts between them and the
	// question: nothing, a page break, or thirty lines of dots.
	if e.opts.Spoilers == SpoilersDots {
		for i := 0; i < dotLines; i++ {
			if into != nil {
				into.addRaw("\n.", "")
			} else {
				e.addPara().addRaw(".", "")
			}
		}
	}
	switch {
	case e.opts.Spoilers == SpoilersPagebreak:
		// chgksuite starts the answers on a new body paragraph even inside a
		// table cell; so does this.
		p = e.addPara()
		p.runs = append(p.runs, pageBreakRun)
		p.keepLines, p.spacingBefore = true, 120
	case into != nil:
		into.addRaw("\n", "")
		// chgksuite sets the spacing on whatever paragraph it holds, so in a cell
		// the answer's 6pt overwrites the question's 18pt.
		into.spacingBefore = 120
	default:
		p = e.addPara()
		p.keepLines, p.spacingBefore = true, 120
	}

	whiten := e.opts.Spoilers == SpoilersWhiten
	p.addRaw(e.labelFor(q, "answer")+": ", "bold")
	// The answer keeps its brackets even on screen — chgksuite passes only the
	// accent switch here.
	e.addValue(p, q.Get("answer"), textOpts{nbsp: true, whiten: whiten, removeAccents: screen})

	// Source and author share a paragraph of their own, set 2pt smaller and
	// spaced to start one body line below the answer (whichever comes first
	// opens it). In a cell they are runs like the rest, only smaller.
	var src *para
	for _, field := range []string{"zachet", "nezachet", "comment", "source", "author"} {
		v := q.Get(field)
		if v == nil {
			continue
		}
		o := textOpts{
			nbsp:          field != "source",
			whiten:        whiten && whitenField[field],
			removeAccents: screen,
			// зачёт keeps its brackets: they are part of the answer, not a note.
			removeBrackets: screen && field != "zachet",
		}
		if field == "source" || field == "author" {
			small := !e.opts.SameSourceAndAuthorSize
			if into != nil {
				into.addRaw("\n", "")
				if small {
					into.sz = srcSz
				}
				into.addRaw(e.labelFor(q, field)+": ", "bold")
				e.addValue(into, v, o)
				continue
			}
			switch {
			case field == "source" || (small && src == nil):
				src = e.addPara()
				src.keepLines = true
				if small {
					src.sz, src.spacingBefore = srcSz, srcGapTw
				}
			case src != nil:
				src.addRaw("\n", "")
			default:
				// An author with no source and no shrinking to open a paragraph
				// of its own trails the answer instead.
				src = p
				src.addRaw("\n", "")
			}
			src.addRaw(e.labelFor(q, field)+": ", "bold")
			e.addValue(src, v, o)
			continue
		}
		p.addRaw("\n", "")
		p.addRaw(e.labelFor(q, field)+": ", "bold")
		e.addValue(p, v, o)
	}
}

func (e *exporter) questionLabel(q *fsource.Question, onlyNumber bool) string {
	num := ""
	if n := q.Get("number"); n != nil {
		num = fmt.Sprintf("%v", n)
	}
	if onlyNumber {
		return num
	}
	return i18n.QuestionLabel(e.labelFor(q, "question"), num, e.opts.Language)
}

// labelFor returns the field label, honouring per-question overrides and the
// plural "Источники" when source is a list.
func (e *exporter) labelFor(q *fsource.Question, field string) string {
	if ov, ok := q.Get("overrides").(map[string]string); ok {
		if v, ok := ov[field]; ok {
			return v
		}
	}
	if field == "source" {
		if _, isList := q.Get("source").([]any); isList {
			return e.labels.Field("sources")
		}
	}
	return e.labels.Field(field)
}

// addValue renders a field value (string or list) into the paragraph's runs.
func (e *exporter) addValue(p *para, v any, o textOpts) {
	switch val := v.(type) {
	case string:
		e.addRuns(p, val, o)
	case []any:
		// [preamble, [items…]] renders the preamble then a numbered list; a flat
		// list renders just the numbered items (mirrors format_docx_element).
		if len(val) >= 2 {
			if items, ok := val[1].([]any); ok {
				e.addRuns(p, fmt.Sprintf("%v", val[0]), o)
				for i, it := range items {
					p.addRaw(fmt.Sprintf("\n%d. ", i+1), "")
					e.addRuns(p, fmt.Sprintf("%v", it), o)
				}
				return
			}
		}
		for i, it := range val {
			p.addRaw(fmt.Sprintf("\n%d. ", i+1), "")
			e.addRuns(p, fmt.Sprintf("%v", it), o)
		}
	}
}

// addRuns tokenizes inline 4s markup and appends one run per token. A (PAGEBREAK)
// starts a new paragraph and the rest of the text goes there, so the paragraph it
// was called with may not be the one it ends in — as in chgksuite.
func (e *exporter) addRuns(p *para, text string, o textOpts) {
	if o.removeAccents {
		text = inline.RemoveAccents(text)
	}
	if o.removeBrackets {
		text = inline.RemoveSquareBrackets(text)
	} else {
		text = inline.ReplaceEscaped(text)
	}
	for _, r := range inline.Parse4sElem(text) {
		switch r.Kind {
		case "linebreak":
			p.addRaw("\n", "")
		case "pagebreak":
			if e.opts.Spoilers == SpoilersDots {
				for i := 0; i < dotLines; i++ {
					e.addPara().addRaw(".", "")
				}
				p = e.addPara()
			} else {
				p = e.addPara()
				p.runs = append(p.runs, pageBreakRun)
			}
		case "img":
			p.runs = append(p.runs, e.embedImage(r.Text))
		case "screen":
			text := r.ForPrint
			if o.screen() {
				text = r.ForScreen
			}
			e.addContent(p, text, "", o)
		case "hyperlink":
			// A whitened hyperlink is printed as its own (whitened) text, not as
			// a link — an invisible link is a trap in a printout.
			if o.whiten {
				e.addContent(p, r.Text, "", o)
				continue
			}
			e.addHyperlink(p, r.Text)
		default:
			e.addContent(p, r.Text, r.Kind, o)
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
// (b, i, smallCaps, strike, sz, szCs, u).
func rPr(kind string, sz int, whiten bool) string {
	var props string
	if whiten {
		props += `<w:rStyle w:val="Whitened"/>`
	}
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
	props += szXML(sz)
	if strings.Contains(kind, "underline") {
		props += `<w:u w:val="single"/>`
	}
	if props == "" {
		return ""
	}
	return "<w:rPr>" + props + "</w:rPr>"
}

func szXML(sz int) string {
	if sz == 0 {
		return ""
	}
	return fmt.Sprintf(`<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, sz, sz)
}

func brk() string { return "<w:r><w:br/></w:r>" }

// pageBreakRun is the run python-docx's add_page_break puts in its paragraph.
const pageBreakRun = `<w:r><w:br w:type="page"/></w:r>`

// dotLines is how many lines of dots spoilers=dots pushes an answer down by.
const dotLines = 30

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
			if len(e.rels) > 0 {
				data = []byte(injectRels(string(data), e.rels))
			}
		case "[Content_Types].xml":
			if len(e.media) > 0 {
				data = []byte(injectMediaContentTypes(string(data), e.media))
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

// injectMediaContentTypes declares a <Default> for every image extension the
// document actually embeds. Word refuses to open a part whose extension has no
// content type, so this has to follow what imgconv.ForExport chose, not a fixed
// guess.
func injectMediaContentTypes(ct string, media []mediaItem) string {
	types := map[string]string{"png": "image/png", "jpg": "image/jpeg"}
	var add strings.Builder
	for _, ext := range []string{"jpg", "png"} { // deterministic order
		if !slices.ContainsFunc(media, func(m mediaItem) bool { return m.ext == ext }) {
			continue
		}
		if strings.Contains(ct, `Extension="`+ext+`"`) {
			continue
		}
		fmt.Fprintf(&add, `<Default Extension="%s" ContentType="%s"/>`, ext, types[ext])
	}
	if add.Len() == 0 {
		return ct
	}
	return strings.Replace(ct, "</Types>", add.String()+"</Types>", 1)
}

// ── the screen-versions table (DocxExporter._add_question_columns) ──

// cellWidth is half the template's text width in twips: A4 (11906) less its
// 1080-twip margins, split in two — what python-docx computes for a two-column
// autofit table.
const cellWidth = 4873

// table is the one-row, two-column table the add_versions_columns screen mode
// puts a question in, one copy per cell, one paragraph per copy.
type table struct{ cells [2]*para }

func (t *table) xml() string {
	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:type="auto" w:w="0"/><w:tblLayout w:type="autofit"/>` +
		`<w:tblLook w:firstColumn="1" w:firstRow="1" w:lastColumn="0" w:lastRow="0" w:noHBand="0" w:noVBand="1" w:val="04A0"/></w:tblPr>`)
	fmt.Fprintf(&b, `<w:tblGrid><w:gridCol w:w="%d"/><w:gridCol w:w="%d"/></w:tblGrid><w:tr>`, cellWidth, cellWidth)
	for _, c := range t.cells {
		fmt.Fprintf(&b, `<w:tc><w:tcPr><w:tcW w:type="dxa" w:w="%d"/>`, cellWidth)
		// chgksuite writes w:topBorder/w:leftBorder/… rather than the schema's
		// w:tcBorders>w:top; Word draws the borders anyway, and parity is parity.
		for _, edge := range []string{"top", "left", "bottom", "right"} {
			fmt.Fprintf(&b, `<w:%sBorder w:val="single" w:sz="4" w:space="0" w:color="auto"/>`, edge)
		}
		b.WriteString("</w:tcPr>" + c.xml() + "</w:tc>")
	}
	b.WriteString("</w:tr></w:tbl>")
	return b.String()
}
