package kit

import "strings"

// The kit's class-vocabulary helpers: the design-system layer owns the u-* /
// grow / gap / align class names and the RootAttrs/Leaf/Input conveniences.
// Core expanders and app ExpandFuncs share them (apps reach them through kit).

// RootAttrs assembles a container's attributes: class (base + u-grow), id, extra
// structural attrs, then universal meta.
func RootAttrs(classes []string, p *Element, extra ...Attr) []Attr {
	classes = GrowClasses(classes, p)
	var out []Attr
	if len(classes) > 0 {
		out = append(out, ClassAttr(classes...))
	}
	out = append(out, IDAttr(p)...)
	out = append(out, extra...)
	return append(out, MetaAttrs(p)...)
}

// GrowClasses appends u-grow when the grow flag is set.
func GrowClasses(base []string, p *Element) []string {
	if Flag(p, "grow") {
		return append(base, "u-grow")
	}
	return base
}

// FlexClasses builds the col/row class list: base + gap + align + justify + wrap.
func FlexClasses(base string, p *Element) []string {
	classes := []string{base, gapClass(p, "")}
	if a, ok := Get(p, "align"); ok && a != "stretch" {
		classes = append(classes, "u-align-"+a)
	}
	if j, ok := Get(p, "justify"); ok && j != "start" {
		classes = append(classes, "u-justify-"+j)
	}
	if Flag(p, "wrap") {
		classes = append(classes, "u-wrap")
	}
	return dropEmpty(classes)
}

// gapClass returns the u-gap-* token for the gap prop, or def when absent;
// "none"/"" yield no class.
func gapClass(p *Element, def string) string {
	g, ok := Get(p, "gap")
	if !ok {
		g = def
	}
	if g == "" || g == "none" {
		return ""
	}
	return "u-gap-" + g
}

// Leaf builds a text/inline leaf element: inline items when present, else its
// block children expanded. Exposed for app text primitives.
func Leaf(c *ExpandCtx, tag string, classes []string, extra []Attr, p *Element) *Element {
	attrs := RootAttrs(classes, p, extra...)
	if len(p.Inline) > 0 {
		return &Element{Tag: tag, Attrs: attrs, Inline: c.Items(p.Inline)}
	}
	return &Element{Tag: tag, Attrs: attrs, Block: c.Nodes(p.Block)}
}

// Input builds an <input class="input" …> with the given type and copied
// value-props (required/autofocus flags + meta are added). Exposed for app
// field primitives.
func Input(c *ExpandCtx, typ string, p *Element, valueProps ...string) *Element {
	attrs := []Attr{ClassAttr(GrowClasses([]string{"input"}, p)...)}
	attrs = append(attrs, IDAttr(p)...)
	attrs = append(attrs, At("type", typ))
	attrs = append(attrs, CopyProps(p, valueProps...)...)
	attrs = append(attrs, CopyFlags(p, "required", "autofocus", "readonly", "disabled")...)
	attrs = append(attrs, MetaAttrs(p)...)
	return El("input", attrs)
}

func forAttr(p *Element) []Attr {
	if v, ok := Get(p, "for"); ok {
		return []Attr{At("for", v)}
	}
	return nil
}

func tooltip(p *Element, fallback string) string {
	if v, ok := Get(p, "title"); ok {
		return v
	}
	return fallback
}

func dropAttr(attrs []Attr, name string) []Attr {
	var out []Attr
	for _, a := range attrs {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
}

func dropAttrPrim(p *Element, name string) *Element {
	return &Element{Tag: p.Tag, Attrs: dropAttr(p.Attrs, name), Block: p.Block, Inline: p.Inline, Line: p.Line}
}

func dropEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func fields(s string) []string { return strings.Fields(s) }

func one(e *Element) []Node { return []Node{e} }

func first(nodes []Node) Node {
	if len(nodes) > 0 {
		return nodes[0]
	}
	return nil
}
