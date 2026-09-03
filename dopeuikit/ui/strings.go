package ui

import "regexp"

// StringSet is a Catalog as the DSL sees it: the `@surface.group.key` an
// attribute value or a text child may be written as, resolved to its text.
// The generated Strings of a module's i18nstrings package implements it.
type StringSet interface {
	// Lookup returns an untemplated string's text.
	Lookup(id string) (string, bool)
	// Defines reports whether the Catalog holds the id at all, templated or not.
	Defines(id string) bool
}

// Chain resolves against the first set that defines an id, skipping nils:
// an app's Catalog over the kit's, so the kit's pages resolve inside any App.
func Chain(sets ...StringSet) StringSet {
	var out chain
	for _, s := range sets {
		if s != nil {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type chain []StringSet

func (c chain) Lookup(id string) (string, bool) {
	for _, s := range c {
		if s.Defines(id) {
			return s.Lookup(id)
		}
	}
	return "", false
}

func (c chain) Defines(id string) bool {
	for _, s := range c {
		if s.Defines(id) {
			return true
		}
	}
	return false
}

var stringIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// resolveStrings replaces every parsed @id — an attribute value or a text child
// — with its text, in place, before the tree is validated.
func resolveStrings(file string, doc *Doc, set StringSet) error {
	return (&refs{file: file, set: set}).nodes(doc.Nodes)
}

type refs struct {
	file string
	set  StringSet
}

func (r *refs) nodes(nodes []Node) error {
	for _, n := range nodes {
		if err := r.node(n); err != nil {
			return err
		}
	}
	return nil
}

func (r *refs) node(n Node) error {
	switch v := n.(type) {
	case *Element:
		for i := range v.Attrs {
			a := &v.Attrs[i]
			if !a.Ref {
				continue
			}
			text, err := r.text(a.Value, v.Line)
			if err != nil {
				return err
			}
			a.Value, a.Ref = text, false
		}
		if err := r.items(v.Inline); err != nil {
			return err
		}
		return r.nodes(v.Block)
	case *RunNode:
		return r.items(v.Items)
	case *TextNode:
		if !v.Ref {
			return nil
		}
		text, err := r.text(v.Value, v.Line)
		if err != nil {
			return err
		}
		v.Value, v.Ref = text, false
	}
	return nil
}

func (r *refs) items(items []Item) error {
	for _, it := range items {
		if n, ok := it.(Node); ok {
			if err := r.node(n); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *refs) text(id string, line int) (string, error) {
	if r.set == nil {
		return "", errf(r.file, line, "@%s: this App was given no Catalog (Options.Strings)", id)
	}
	if text, ok := r.set.Lookup(id); ok {
		return text, nil
	}
	if r.set.Defines(id) {
		return "", errf(r.file, line, "@%s takes parameters; only an untemplated string can be written in .dopeui", id)
	}
	return "", errf(r.file, line, "unknown string id @%s", id)
}
