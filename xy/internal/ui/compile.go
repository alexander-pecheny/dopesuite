package ui

// Compile parses, validates and expands a .xui page. name labels errors as
// "name:line: message". A page file's single meaningful top-level node must be
// a `page`.
func Compile(name string, src []byte) ([]byte, error) {
	doc, err := Parse(name, src)
	if err != nil {
		return nil, err
	}
	if err := requirePageRoot(name, doc); err != nil {
		return nil, err
	}
	if err := Validate(name, doc); err != nil {
		return nil, err
	}
	return render(doc), nil
}

func requirePageRoot(name string, doc *Doc) error {
	var roots []*Element
	for _, n := range doc.Nodes {
		switch v := n.(type) {
		case *Element:
			roots = append(roots, v)
		case *Comment, *BlankLine:
		default:
			return errf(name, 0, "a page's top level may only contain a single `page` node")
		}
	}
	if len(roots) != 1 || roots[0].Tag != "page" {
		line := 0
		if len(roots) > 0 {
			line = roots[0].Line
		}
		return errf(name, line, "a page file must have exactly one top-level `page` node")
	}
	return nil
}
