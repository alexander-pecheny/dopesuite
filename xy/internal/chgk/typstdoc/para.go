package typstdoc

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"xy/internal/chgk/inline"
)

// A para is one Word paragraph: a list of typst content expressions plus the
// paragraph properties that came off the docx (spacing, keep-together, the heading
// font). It renders to one #block(…) — or to several, since typst refuses a
// pagebreak inside a container ("pagebreaks are not allowed inside of containers"),
// so a (PAGEBREAK) in mid-paragraph closes the block, breaks the page, and opens
// the next one.
type para struct {
	above     float64 // spacing before, pt
	below     float64 // spacing after, pt
	keepLines bool    // w:keepLines → breakable: false
	sticky    bool    // w:keepNext  → sticky: true
	pageBreak bool    // w:pageBreakBefore
	size      float64 // block-level text size (headings); 0 = body size
	bold      bool
	italic    bool
	runSize   float64 // per-run text size, pt; 0 = inherit. Expressions are built at
	// append time, so flipping this mid-paragraph resizes only the runs that follow
	// (author glued onto the answer paragraph).
	exprs []string // content expressions, with pbMarker where a page break falls
}

// pbMarker separates the block-chunks of a paragraph interrupted by a page break.
// It can't collide with a real expression: those are all function calls.
const pbMarker = "\x00pagebreak\x00"

func (p *para) add(expr string) {
	if expr != "" {
		p.exprs = append(p.exprs, expr)
	}
}

// sized wraps a content expression in the paragraph's per-run size, if one is set.
func (p *para) sized(expr string) string {
	if expr == "" || p.runSize == 0 {
		return expr
	}
	return "text(size: " + pt(p.runSize) + ", " + expr + ")"
}

// addStyled appends verbatim text (labels, list markers, "\n" separators) — no
// backtick/nbsp processing, mirroring docx's addRaw.
func (p *para) addStyled(text, kind string) { p.add(p.sized(styled(text, kind))) }

// addContent appends editorial text: backtick stress accents, then the
// non-breaking-space gluing, exactly as the docx export does it. The
// non-breaking hyphen (U+2011) and NBSP (U+00A0) go into the PDF as themselves —
// typst's line breaker honours both, so no substitution is needed (the docx export
// has to swap U+2011 for word-joiner+hyphen+word-joiner, which Word needs).
func (p *para) addContent(text, kind string, nbsp bool) {
	text = inline.BacktickReplace(text)
	if nbsp {
		text = inline.ReplaceNoBreak(text)
	}
	p.add(p.sized(styled(text, kind)))
}

func (p *para) addBreak()     { p.add("linebreak()") }
func (p *para) addPageBreak() { p.exprs = append(p.exprs, pbMarker) }

// addLink appends a link styled like the docx Hyperlink character style (blue,
// underlined). The URL is both the target and the visible text, as in the 4s source.
func (p *para) addLink(url string) {
	p.add(p.sized(fmt.Sprintf("link(%s, underline(text(fill: rgb(%q), %s)))",
		typstString(url), linkColor, typstString(url))))
}

// typ renders the paragraph to typst source.
func (p *para) typ() string {
	var out strings.Builder
	if p.pageBreak {
		out.WriteString("#pagebreak(weak: true)\n")
	}
	for i, chunk := range p.chunks() {
		if i > 0 {
			out.WriteString("#pagebreak(weak: true)\n")
		}
		above := p.above
		if i > 0 {
			above = 0
		}
		body := strings.Join(chunk, " + ")
		if body == "" {
			body = "[]"
		}
		if params := p.textParams(); params != "" {
			body = fmt.Sprintf("text(%s, %s)", params, body)
		}
		fmt.Fprintf(&out, "#block(above: %s, below: %s, breakable: %t, sticky: %t, %s)\n",
			pt(above), pt(p.below), !p.keepLines, p.sticky, body)
	}
	return out.String()
}

// chunks splits the paragraph's expressions at the page breaks.
func (p *para) chunks() [][]string {
	chunks := [][]string{{}}
	for _, e := range p.exprs {
		if e == pbMarker {
			chunks = append(chunks, []string{})
			continue
		}
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], e)
	}
	return chunks
}

// textParams is the heading font, if this paragraph is one.
func (p *para) textParams() string {
	var params []string
	if p.size != 0 && p.size != bodyPt {
		params = append(params, "size: "+pt(p.size))
	}
	if p.bold {
		params = append(params, `weight: "bold"`)
	}
	if p.italic {
		params = append(params, `style: "italic"`)
	}
	return strings.Join(params, ", ")
}

// emptyLine is the empty paragraph docx emits after a meta block: one blank line
// of body text (a bare block would collapse to nothing).
func emptyLine() string {
	return fmt.Sprintf("#block(above: 0pt, below: 0pt, text(%s))\n", typstString(inline.NBSP))
}

// ── inline styling ──

// styled turns one run of text into a content expression, applying the 4s inline
// kind (bold/italic/underline/strike/sc, and their combinations). Line breaks and
// tabs inside the text become linebreak()/h(…) between expressions, as they become
// <w:br/>/<w:tab/> inside a docx run.
func styled(text, kind string) string {
	if text == "" {
		return ""
	}
	var params []string
	if strings.Contains(kind, "bold") {
		params = append(params, `weight: "bold"`)
	}
	if strings.Contains(kind, "italic") {
		params = append(params, `style: "italic"`)
	}
	inner := textExpr(text, strings.Join(params, ", "), kind == "sc")
	if strings.Contains(kind, "underline") {
		inner = "underline(" + inner + ")"
	}
	if kind == "strike" {
		inner = "strike(" + inner + ")"
	}
	return inner
}

// textExpr builds the content for one run's text: the literal, with breaks and
// tabs lifted out into their own expressions.
func textExpr(text, params string, smallCaps bool) string {
	var parts []string
	emit := func(s string) {
		if s == "" {
			return
		}
		if smallCaps {
			parts = append(parts, scExpr(s, params))
			return
		}
		parts = append(parts, wrapText(s, params))
	}
	cur := strings.Builder{}
	for _, r := range text {
		switch r {
		case '\n', '\r':
			emit(cur.String())
			cur.Reset()
			parts = append(parts, "linebreak()")
		case '\t':
			emit(cur.String())
			cur.Reset()
			parts = append(parts, "h("+tabWidth+")")
		default:
			cur.WriteRune(r)
		}
	}
	emit(cur.String())
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " + ")
}

func wrapText(s, params string) string {
	if params == "" {
		return "text(" + typstString(s) + ")"
	}
	return "text(" + params + ", " + typstString(s) + ")"
}

// scExpr synthesizes small caps: the lowercase letters are uppercased and set
// smaller, the rest is left alone. Word synthesizes w:smallCaps the same way, and
// it has to be done by hand here because Noto Sans carries no `smcp` feature — so
// typst's smallcaps() would leave the text exactly as it was.
func scExpr(s, params string) string {
	const scale = 0.8 // of the surrounding size, as Word renders small caps
	var parts []string
	var cur []rune
	curLower := false
	flush := func() {
		if len(cur) == 0 {
			return
		}
		if curLower {
			p := params
			if p != "" {
				p += ", "
			}
			p += "size: " + strconv.FormatFloat(scale, 'g', -1, 64) + "em"
			parts = append(parts, wrapText(strings.ToUpper(string(cur)), p))
		} else {
			parts = append(parts, wrapText(string(cur), params))
		}
		cur = cur[:0]
	}
	for _, r := range s {
		lower := unicode.IsLower(r)
		if len(cur) > 0 && lower != curLower {
			flush()
		}
		curLower = lower
		cur = append(cur, r)
	}
	flush()
	return strings.Join(parts, " + ")
}

// ── literals ──

// typstString renders a Go string as a typst string literal. Every piece of
// editorial text reaches typst through this, which is why the exporter can stay in
// code mode and never has to escape typst *markup*.
func typstString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u{%x}`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// pt formats a length in points, the way typst wants it.
func pt(v float64) string {
	return strconv.FormatFloat(inline.Round2(v), 'f', -1, 64) + "pt"
}

// mm formats a length in millimetres.
func mm(v float64) string {
	return strconv.FormatFloat(inline.Round2(v), 'f', -1, 64) + "mm"
}
