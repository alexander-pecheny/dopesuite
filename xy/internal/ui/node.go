// Package ui implements a constrained UI DSL: a closed HTML vocabulary
// (vocab.json), a line-oriented source language (.xui, parse.go) and an
// equivalent Go builder (builder.go), both compiling to the same node tree
// and printed by one deterministic renderer (render.go).
package ui

// Item is anything that can appear in an element's argument list: an
// attribute or a child node.
type Item interface {
	item()
}

// Node is a member of the rendered tree.
type Node interface {
	Item
	node()
}

// Attr is a single HTML attribute. A bare attribute (hidden, required, …)
// carries no value.
type Attr struct {
	Name  string
	Value string
	Bare  bool
}

func (Attr) item() {}

// Element is a tag with attributes and either Block children (each on its
// own output line) or Inline content (Text/Element items concatenated on one
// line). Block and Inline are mutually exclusive; both empty means an empty
// element. Line is the 1-based source line the tag was written on (0 for
// builder-constructed elements).
type Element struct {
	Tag    string
	Attrs  []Attr
	Block  []Node
	Inline []Item
	Line   int
}

func (*Element) item() {}
func (*Element) node() {}

// TextNode is a text leaf, valid only inside inline content (Element.Inline
// or a RunNode's Items).
type TextNode struct {
	Value string
	Line  int
}

func (*TextNode) item() {}
func (*TextNode) node() {}

// RunNode is one inline-run child line: a sequence of items (text and/or
// inline elements) rendered concatenated on one output line, no separator.
type RunNode struct {
	Items []Item
	Line  int
}

func (*RunNode) item() {}
func (*RunNode) node() {}

// Comment is an HTML comment. Multiple Lines render as one multi-line
// comment node.
type Comment struct {
	Lines []string
	Line  int
}

func (*Comment) item() {}
func (*Comment) node() {}

// BlankLine is a preserved blank source line between siblings.
type BlankLine struct{}

func (*BlankLine) item() {}
func (*BlankLine) node() {}

// Doctype emits `<!doctype html>`. Valid only as the document's first node.
type Doctype struct {
	Line int
}

func (*Doctype) item() {}
func (*Doctype) node() {}

// Doc is a parsed or built page: the top-level sibling nodes (typically a
// Doctype followed by the <html> Element).
type Doc struct {
	Nodes []Node
}
