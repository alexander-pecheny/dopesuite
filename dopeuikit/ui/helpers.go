package ui

import "strings"

// The engine's expansion helper surface: pure prop→attr / class-join helpers
// with no design-system knowledge (no class-name literals). El/Inl build HTML
// elements; the getters read props; MetaAttrs/Passthrough move universal props
// through. The class-vocabulary helpers (grow/gap/align, RootAttrs, Leaf, …)
// live in the kit design-system layer, which owns the class names.

// El builds a block HTML element (children each on their own output line).
func El(tag string, attrs []Attr, children ...Node) *Element {
	return &Element{Tag: tag, Attrs: attrs, Block: children}
}

// Inl builds an inline HTML element (items concatenated on one line).
func Inl(tag string, attrs []Attr, items ...Item) *Element {
	return &Element{Tag: tag, Attrs: attrs, Inline: items}
}

// ClassAttr joins classes into a single class attribute.
func ClassAttr(classes ...string) Attr {
	return Attr{Name: "class", Value: strings.Join(classes, " ")}
}

// At builds a name="value" attribute; BareAt builds a valueless one.
func At(name, value string) Attr { return Attr{Name: name, Value: value} }
func BareAt(name string) Attr    { return Attr{Name: name, Bare: true} }

// Get returns the value of a value-prop; Flag reports a bare prop's presence.
func Get(p *Element, name string) (string, bool) {
	for _, a := range p.Attrs {
		if a.Name == name && !a.Bare {
			return a.Value, true
		}
	}
	return "", false
}

func Flag(p *Element, name string) bool {
	for _, a := range p.Attrs {
		if a.Name == name && a.Bare {
			return true
		}
	}
	return false
}

// IDAttr returns the primitive's id as a single attr slice (or nil).
func IDAttr(p *Element) []Attr {
	if v, ok := Get(p, "id"); ok {
		return []Attr{At("id", v)}
	}
	return nil
}

// Passthrough returns the primitive's data-*/aria-* props verbatim.
func Passthrough(p *Element) []Attr {
	var out []Attr
	for _, a := range p.Attrs {
		if strings.HasPrefix(a.Name, "data-") || strings.HasPrefix(a.Name, "aria-") {
			out = append(out, a)
		}
	}
	return out
}

// CopyProps emits the named value-props that are present, in the given order.
func CopyProps(p *Element, names ...string) []Attr {
	var out []Attr
	for _, n := range names {
		if v, ok := Get(p, n); ok {
			out = append(out, At(n, v))
		}
	}
	return out
}

// CopyFlags emits the named bare props that are present, in the given order.
func CopyFlags(p *Element, names ...string) []Attr {
	var out []Attr
	for _, n := range names {
		if Flag(p, n) {
			out = append(out, BareAt(n))
		}
	}
	return out
}

// MetaAttrs appends the universal props after a primitive's structural attrs:
// title (tooltip), hidden, then data-*/aria-*.
func MetaAttrs(p *Element) []Attr {
	var out []Attr
	if v, ok := Get(p, "title"); ok {
		out = append(out, At("title", v))
	}
	if Flag(p, "hidden") {
		out = append(out, BareAt("hidden"))
	}
	return append(out, Passthrough(p)...)
}
