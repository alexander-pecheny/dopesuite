package ui

import "strings"

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"\n", "&#10;",
)

func indent(depth int) string {
	return strings.Repeat("  ", depth)
}

// render prints a validated node tree to HTML bytes, byte-exact with the
// repo's existing hand-authored pages (see DESIGN.md's printer rules).
func render(doc *Doc) []byte {
	var b strings.Builder
	renderNodes(&b, doc.Nodes, 0)
	return []byte(b.String())
}

func renderNodes(b *strings.Builder, nodes []Node, depth int) {
	for _, n := range nodes {
		renderNode(b, n, depth)
	}
}

func renderNode(b *strings.Builder, n Node, depth int) {
	switch v := n.(type) {
	case *Doctype:
		b.WriteString(indent(depth))
		b.WriteString("<!doctype html>\n")

	case *BlankLine:
		b.WriteString("\n")

	case *Comment:
		b.WriteString(indent(depth))
		b.WriteString("<!-- ")
		b.WriteString(v.Lines[0])
		contIndent := strings.Repeat(" ", depth*2+5)
		for _, line := range v.Lines[1:] {
			b.WriteString("\n")
			b.WriteString(contIndent)
			b.WriteString(line)
		}
		b.WriteString(" -->\n")

	case *RunNode:
		b.WriteString(indent(depth))
		renderInline(b, v.Items)
		b.WriteString("\n")

	case *Element:
		renderElement(b, v, depth)
	}
}

func renderElement(b *strings.Builder, e *Element, depth int) {
	b.WriteString(indent(depth))
	writeOpenTag(b, e)

	if Loaded.IsVoid(e.Tag) {
		b.WriteString("\n")
		return
	}

	if len(e.Block) > 0 {
		b.WriteString("\n")
		childDepth := depth + 1
		if e.Tag == "html" {
			childDepth = depth
		}
		renderNodes(b, e.Block, childDepth)
		b.WriteString(indent(depth))
		b.WriteString("</")
		b.WriteString(e.Tag)
		b.WriteString(">\n")
		return
	}

	renderInline(b, e.Inline)
	b.WriteString("</")
	b.WriteString(e.Tag)
	b.WriteString(">\n")
}

func writeOpenTag(b *strings.Builder, e *Element) {
	b.WriteString("<")
	b.WriteString(e.Tag)
	for _, a := range e.Attrs {
		b.WriteString(" ")
		b.WriteString(a.Name)
		if !a.Bare {
			b.WriteString(`="`)
			b.WriteString(attrEscaper.Replace(a.Value))
			b.WriteString(`"`)
		}
	}
	b.WriteString(">")
}

func renderInline(b *strings.Builder, items []Item) {
	for _, it := range items {
		switch v := it.(type) {
		case *TextNode:
			b.WriteString(textEscaper.Replace(v.Value))
		case *Element:
			writeOpenTag(b, v)
			if Loaded.IsVoid(v.Tag) {
				continue
			}
			renderInline(b, v.Inline)
			b.WriteString("</")
			b.WriteString(v.Tag)
			b.WriteString(">")
		}
	}
}
