package ui

// Validate checks a primitive tree against the closed vocabulary: unknown
// primitive, prop not allowed on that primitive, invalid enum value, missing
// required prop, wrong prop kind (bare vs value), duplicate id per page, and
// children-policy violations. file labels errors; pass "" for builder-made
// trees with no source file.
func Validate(file string, doc *Doc) error {
	val := &validator{file: file, ids: make(map[string]int)}
	return val.walkNodes(doc.Nodes, false)
}

type validator struct {
	file string
	ids  map[string]int // id value -> line first seen
}

func (val *validator) errf(line int, format string, args ...any) error {
	return errf(val.file, line, format, args...)
}

func (val *validator) walkNodes(nodes []Node, inline bool) error {
	for _, n := range nodes {
		if err := val.walk(n, inline); err != nil {
			return err
		}
	}
	return nil
}

func (val *validator) walk(n Node, inline bool) error {
	switch v := n.(type) {
	case *Element:
		return val.walkPrim(v)
	case *RunNode:
		return val.walkNodes(itemsToNodes(v.Items), true)
	case *TextNode, *Comment, *BlankLine:
		return nil
	}
	return nil
}

func (val *validator) walkPrim(e *Element) error {
	spec, ok := Loaded.Primitive(e.Tag)
	if !ok {
		return val.errf(e.Line, "unknown primitive %q", e.Tag)
	}
	if err := val.checkProps(e, spec); err != nil {
		return err
	}
	return val.checkChildren(e, spec)
}

func (val *validator) checkProps(e *Element, spec PrimitiveSpec) error {
	seen := make(map[string]bool, len(e.Attrs))
	for _, a := range e.Attrs {
		seen[a.Name] = true
		if a.Name == "id" {
			if first, dup := val.ids[a.Value]; dup {
				return val.errf(e.Line, "duplicate id %q (first used at line %d)", a.Value, first)
			}
			val.ids[a.Value] = e.Line
		}
		if Loaded.PatternProp(a.Name) {
			continue
		}
		ps, ok := Loaded.PropFor(e.Tag, a.Name)
		if !ok {
			return val.errf(e.Line, "prop %q is not allowed on %q", a.Name, e.Tag)
		}
		if err := val.checkPropValue(e, ps, a); err != nil {
			return err
		}
	}
	for _, ps := range spec.Props {
		if ps.Required && !seen[ps.Name] {
			return val.errf(e.Line, "%q requires prop %q", e.Tag, ps.Name)
		}
	}
	return nil
}

func (val *validator) checkPropValue(e *Element, ps PropSpec, a Attr) error {
	if ps.Kind == "bare" {
		if !a.Bare {
			return val.errf(e.Line, "prop %q on %q is a flag; it takes no value", a.Name, e.Tag)
		}
		return nil
	}
	if a.Bare {
		return val.errf(e.Line, "prop %q on %q requires a value", a.Name, e.Tag)
	}
	if ps.Enum != "" && !Loaded.EnumHas(ps.Enum, a.Value) {
		return val.errf(e.Line, "prop %q on %q: %q is not a valid %s", a.Name, e.Tag, a.Value, ps.Enum)
	}
	return nil
}

func (val *validator) checkChildren(e *Element, spec PrimitiveSpec) error {
	switch spec.ChildPolicy {
	case "none":
		if len(e.Inline) > 0 || len(e.Block) > 0 {
			return val.errf(e.Line, "%q takes no children", e.Tag)
		}
	case "text":
		return val.checkTextChildren(e)
	case "any":
		if len(e.Inline) > 0 {
			return val.errf(e.Line, "%q takes block children, not inline text", e.Tag)
		}
		return val.walkNodes(e.Block, false)
	case "named":
		return val.checkNamedChildren(e, spec)
	}
	return nil
}

// checkTextChildren allows inline text/inline-primitives (`hint "…"`) and
// multi-line run children (`hint` with `"…"` lines), nothing structural.
func (val *validator) checkTextChildren(e *Element) error {
	if err := val.walkNodes(itemsToNodes(e.Inline), true); err != nil {
		return err
	}
	for _, c := range e.Block {
		switch v := c.(type) {
		case *RunNode, *TextNode, *Comment, *BlankLine:
		case *Element:
			if !isInlinePrimitive(v.Tag) {
				return val.errf(v.Line, "%q allows only text, not %q", e.Tag, v.Tag)
			}
		}
		if err := val.walk(c, true); err != nil {
			return err
		}
	}
	return nil
}

func (val *validator) checkNamedChildren(e *Element, spec PrimitiveSpec) error {
	if len(e.Inline) > 0 {
		return val.errf(e.Line, "%q takes block children only", e.Tag)
	}
	for _, c := range e.Block {
		switch v := c.(type) {
		case *Comment, *BlankLine:
			continue
		case *Element:
			if !spec.ChildSet[v.Tag] {
				return val.errf(v.Line, "%q is not allowed inside %q", v.Tag, e.Tag)
			}
			if err := val.walkPrim(v); err != nil {
				return err
			}
		default:
			return val.errf(e.Line, "%q allows only named children", e.Tag)
		}
	}
	return nil
}

func isInlinePrimitive(tag string) bool {
	switch tag {
	case "strong", "code", "muted", "unreaddot":
		return true
	}
	return false
}

func itemsToNodes(items []Item) []Node {
	out := make([]Node, 0, len(items))
	for _, it := range items {
		if n, ok := it.(Node); ok {
			out = append(out, n)
		}
	}
	return out
}
