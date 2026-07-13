package ui

import "strings"

// ExpandCtx carries the recursion into app-supplied ExpandFuncs: it dispatches
// child primitives back through the app's expander table and exposes the app's
// mounts, chrome, and vocab placement.
type ExpandCtx struct{ app *App }

func (a *App) render(doc *Doc) []byte {
	ctx := &ExpandCtx{app: a}
	return printDoc(&Doc{Nodes: ctx.Nodes(doc.Nodes)})
}

// Nodes expands a run of block children.
func (c *ExpandCtx) Nodes(nodes []Node) []Node {
	var out []Node
	for _, n := range nodes {
		out = append(out, c.node(n)...)
	}
	return out
}

func (c *ExpandCtx) node(n Node) []Node {
	switch v := n.(type) {
	case *Element:
		return c.Expand(v)
	case *RunNode:
		return []Node{&RunNode{Items: c.Items(v.Items)}}
	case *TextNode, *Comment, *BlankLine:
		return []Node{n}
	}
	return nil
}

// Expand dispatches one primitive element to its registered expander.
func (c *ExpandCtx) Expand(p *Element) []Node {
	if fn, ok := c.app.expand[p.Tag]; ok {
		return fn(c, p)
	}
	return nil
}

// Items expands a run of inline items (text + inline primitives).
func (c *ExpandCtx) Items(items []Item) []Item {
	var out []Item
	for _, it := range items {
		switch v := it.(type) {
		case *TextNode:
			out = append(out, v)
		case *Element:
			out = append(out, c.InlineOne(v))
		}
	}
	return out
}

// InlineOne expands one inline primitive to an HTML item.
func (c *ExpandCtx) InlineOne(p *Element) Item {
	if fn, ok := c.app.inline[p.Tag]; ok {
		return fn(c, p)
	}
	return &TextNode{}
}

// Chrome returns the app's page chrome configuration.
func (c *ExpandCtx) Chrome() Chrome { return c.app.chrome }

// Mount resolves a mount kind to its tag+classes.
func (c *ExpandCtx) Mount(kind string) (MountSpec, bool) {
	m, ok := c.app.mounts[kind]
	return m, ok
}

// Placement returns a primitive's page placement ("header"/"overlay"/"").
func (c *ExpandCtx) Placement(tag string) string {
	if s, ok := c.app.vocab.Primitive(tag); ok {
		return s.Placement
	}
	return ""
}

// ---- printer: HTML node tree -> bytes ---------------------------------------

var htmlVoid = map[string]bool{"meta": true, "link": true, "input": true, "br": true, "img": true, "hr": true, "source": true}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;")

func indent(depth int) string { return strings.Repeat("  ", depth) }

func printDoc(doc *Doc) []byte {
	var b strings.Builder
	printNodes(&b, doc.Nodes, 0)
	return []byte(b.String())
}

func printNodes(b *strings.Builder, nodes []Node, depth int) {
	for _, n := range nodes {
		printNode(b, n, depth)
	}
}

func printNode(b *strings.Builder, n Node, depth int) {
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
		printInline(b, v.Items)
		b.WriteString("\n")
	case *Element:
		printElement(b, v, depth)
	}
}

func printElement(b *strings.Builder, e *Element, depth int) {
	b.WriteString(indent(depth))
	writeOpenTag(b, e)
	if htmlVoid[e.Tag] {
		b.WriteString("\n")
		return
	}
	if len(e.Block) > 0 {
		b.WriteString("\n")
		childDepth := depth + 1
		if e.Tag == "html" {
			childDepth = depth
		}
		printNodes(b, e.Block, childDepth)
		b.WriteString(indent(depth))
		b.WriteString("</")
		b.WriteString(e.Tag)
		b.WriteString(">\n")
		return
	}
	printInline(b, e.Inline)
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

func printInline(b *strings.Builder, items []Item) {
	for _, it := range items {
		switch v := it.(type) {
		case *TextNode:
			b.WriteString(textEscaper.Replace(v.Value))
		case *Element:
			writeOpenTag(b, v)
			if htmlVoid[v.Tag] {
				continue
			}
			printInline(b, v.Inline)
			b.WriteString("</")
			b.WriteString(v.Tag)
			b.WriteString(">")
		}
	}
}
