package ui

// newElement is the shared constructor body behind every generated primitive
// function (Row, Button, …): it partitions items into props (in authored
// order) and children (in authored order), then picks a content mode.
//
// Automatic content-mode inference only covers the unambiguous cases: no
// children (empty element), all-Text children (inline), or any Element/Run
// child (block, one per line). Mixed text-and-element content on one line —
// a label followed by an inline badge span — has no unambiguous automatic
// reading, so callers build it explicitly with Line(...) (a single RunNode
// child) or force the outer element's own inline content with Inline(...).
// New builds a primitive element by tag — the shared body every generated
// primitive constructor delegates to, including app-overlay constructors in
// other packages.
func New(tag string, items ...Item) *Element { return newElement(tag, items) }

func newElement(tag string, items []Item) *Element {
	e := &Element{Tag: tag}
	var children []Node
	var forcedInline []Item
	forced := false
	for _, it := range items {
		switch v := it.(type) {
		case Attr:
			e.Attrs = append(e.Attrs, v)
		case inlineForce:
			forced = true
			forcedInline = v.items
		case Node:
			children = append(children, v)
		}
	}
	if forced {
		e.Inline = forcedInline
		return e
	}
	if len(children) == 0 {
		return e
	}
	allText := true
	for _, c := range children {
		if _, ok := c.(*TextNode); !ok {
			allText = false
			break
		}
	}
	if allText {
		for _, c := range children {
			e.Inline = append(e.Inline, c.(Item))
		}
		return e
	}
	e.Block = children
	return e
}

// inlineForce is the sentinel Item produced by Inline(...): it forces the
// enclosing element's own content onto one line, overriding the automatic
// all-Text/any-Element inference in newElement.
type inlineForce struct{ items []Item }

func (inlineForce) item() {}

// Inline forces one-line content on the element it's passed to, e.g. mixed
// text and inline elements that would otherwise be ambiguous.
func Inline(items ...Item) Item {
	return inlineForce{items: items}
}

// Line builds one inline-run child line: items concatenated with no
// separator, as a single Node to pass as a child (e.g. the unread-dot badge
// idiom: Button(Line(Text("🔔"), Span(...)))).
func Line(items ...Item) *RunNode {
	return &RunNode{Items: items}
}

// Text is a text leaf; pass it as a child to get inline element content.
func Text(s string) *TextNode {
	return &TextNode{Value: s}
}

// CommentNode builds an HTML comment; multiple lines render as one
// multi-line comment.
func CommentNode(lines ...string) *Comment {
	return &Comment{Lines: lines}
}

// Blank preserves a blank line between siblings.
func Blank() *BlankLine {
	return &BlankLine{}
}

// ID sets the id prop.
func ID(v string) Attr {
	return Attr{Name: "id", Value: v}
}

// Aria sets an aria-<name> prop; an empty value produces a bare
// attribute (e.g. Aria("hidden", "") -> aria-hidden).
func Aria(name, value string) Attr {
	return patternAttr("aria-"+name, value)
}

// Data sets a data-<name> attribute; an empty value produces a bare
// attribute.
func Data(name, value string) Attr {
	return patternAttr("data-"+name, value)
}

func patternAttr(name, value string) Attr {
	if value == "" {
		return Attr{Name: name, Bare: true}
	}
	return Attr{Name: name, Value: value}
}
