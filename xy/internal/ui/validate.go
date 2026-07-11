package ui

import "strings"

// Validate checks a node tree against the closed vocabulary: unknown
// elements/attributes, unknown class tokens, duplicate ids, void elements
// with content, and inline elements smuggling block children (only reachable
// from builder-made trees — the .xui grammar can't express it). file labels
// errors; pass "" for builder-made trees with no source file.
func Validate(file string, doc *Doc) error {
	val := &validator{file: file, ids: make(map[string]int)}
	for _, n := range doc.Nodes {
		if err := val.walk(n, false); err != nil {
			return err
		}
	}
	return nil
}

type validator struct {
	file string
	ids  map[string]int // id value -> line first seen
}

func (val *validator) errf(line int, format string, args ...any) error {
	return errf(val.file, line, format, args...)
}

func (val *validator) walk(n Node, inlineContext bool) error {
	switch v := n.(type) {
	case *Element:
		return val.walkElement(v, inlineContext)
	case *RunNode:
		for _, it := range v.Items {
			if e, ok := it.(*Element); ok {
				if err := val.walk(e, true); err != nil {
					return err
				}
			}
		}
	case *TextNode:
		if inlineContext {
			return nil
		}
		return val.errf(v.Line, "bare text is not allowed as a block child; wrap it with Line(...)")
	}
	return nil
}

func (val *validator) walkElement(e *Element, inlineContext bool) error {
	if inlineContext && len(e.Block) > 0 {
		return val.errf(e.Line, "inline element <%s> cannot have block children", e.Tag)
	}
	if !Loaded.ElementAllowed(e.Tag) {
		return val.errf(e.Line, "unknown element <%s>", e.Tag)
	}
	if Loaded.IsVoid(e.Tag) && (len(e.Block) > 0 || len(e.Inline) > 0) {
		return val.errf(e.Line, "void element <%s> cannot have content", e.Tag)
	}
	for _, a := range e.Attrs {
		if !Loaded.AttrAllowed(e.Tag, a.Name) {
			return val.errf(e.Line, "attribute %q is not allowed on <%s>", a.Name, e.Tag)
		}
		if a.Name == "id" {
			if first, dup := val.ids[a.Value]; dup {
				return val.errf(e.Line, "duplicate id %q (first used at line %d)", a.Value, first)
			}
			val.ids[a.Value] = e.Line
		}
		if a.Name == "class" {
			for _, token := range strings.Fields(a.Value) {
				if !Loaded.ClassAllowed(token) {
					return val.errf(e.Line, "unknown class token %q on <%s>", token, e.Tag)
				}
			}
		}
	}
	for _, c := range e.Block {
		if err := val.walk(c, false); err != nil {
			return err
		}
	}
	for _, it := range e.Inline {
		if child, ok := it.(*Element); ok {
			if err := val.walk(child, true); err != nil {
				return err
			}
		}
	}
	return nil
}
