// Package ui is DopeUIKit's constrained UI DSL engine: a line-oriented source
// language (.dopeui, parse.go) and an equivalent Go builder (builder.go) that
// both produce one primitive tree (Element.Tag = primitive name, Attr = prop);
// a vocabulary loader/merger + validator (vocab.go, validate.go); an App
// (app.go) that expands a validated tree into HTML via caller-supplied
// ExpandFuncs (render.go), printed by one deterministic printer. The engine is
// design-system-agnostic: it holds no vocabulary, no expanders, and no CSS class
// names — those live in a design-system layer (the kit) that supplies the Base
// vocabulary and the expander tables through Options.
package ui

// Item is anything that can appear in a primitive's argument list: a prop
// (Attr) or a child node.
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
	// Ref marks a value parsed as a String Id (`label=@common.save`): it holds
	// the id until the App resolves it against its Catalog.
	Ref bool
}

func (Attr) item() {}

// Element is a tag with attributes and either Block children (each on its own
// output line) or Inline content (Text/Element items concatenated on one line).
// Block and Inline are mutually exclusive; both empty means an empty element.
// Line is the 1-based source line the tag was written on (0 for
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

// TextNode is a text leaf, valid only inside inline content (Element.Inline or
// a RunNode's Items).
type TextNode struct {
	Value string
	Line  int
	Ref   bool // a String Id, like Attr.Ref
}

func (*TextNode) item() {}
func (*TextNode) node() {}

// RunNode is one inline-run child line: a sequence of items rendered
// concatenated on one output line, no separator.
type RunNode struct {
	Items []Item
	Line  int
}

func (*RunNode) item() {}
func (*RunNode) node() {}

// Comment is an HTML comment. Multiple Lines render as one multi-line comment.
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

// RawHTML is markup written through the printer verbatim, the engine's one
// escape hatch. It exists for content an app has ALREADY rendered to HTML —
// dope's fest descriptions, which are markdown run through a server-side
// renderer — and which would otherwise force those pages to stay hand-written
// templates outside the DSL entirely.
//
// The parser never produces one: no `.dopeui` syntax maps to it, and no vocab
// prop carries it. Only the typed builder can, so a page author opts in from Go
// and the value's provenance is visible at the call site. Never build one from
// request data.
type RawHTML struct {
	HTML string
	Line int
}

func (*RawHTML) item() {}
func (*RawHTML) node() {}

// Doctype emits `<!doctype html>`. Not authored directly — the page expander
// emits it as the first node.
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
