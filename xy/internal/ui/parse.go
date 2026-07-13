package ui

import (
	"fmt"
	"regexp"
	"strings"
)

// Error is a compile error with a source position, formatted "file:line: msg"
// (or "file: msg" when no line applies, e.g. a builder-tree validation
// error).
type Error struct {
	File string
	Line int
	Msg  string
}

func (e *Error) Error() string {
	switch {
	case e.File != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	case e.File != "":
		return fmt.Sprintf("%s: %s", e.File, e.Msg)
	default:
		return e.Msg
	}
}

func errf(file string, line int, format string, args ...any) *Error {
	return &Error{File: file, Line: line, Msg: fmt.Sprintf(format, args...)}
}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// rawLine is one source line, pre-split into its indentation and content.
type rawLine struct {
	no      int
	indent  int
	blank   bool
	content string
}

func splitLines(file string, src []byte) ([]rawLine, error) {
	text := string(src)
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	lines := make([]rawLine, 0, len(parts))
	for idx, raw := range parts {
		no := idx + 1
		if strings.Contains(raw, "\t") {
			return nil, errf(file, no, "tabs are not allowed for indentation")
		}
		if strings.TrimSpace(raw) == "" {
			lines = append(lines, rawLine{no: no, blank: true})
			continue
		}
		trimmed := strings.TrimRight(raw, " ")
		indent := 0
		for indent < len(trimmed) && trimmed[indent] == ' ' {
			indent++
		}
		lines = append(lines, rawLine{no: no, indent: indent, content: trimmed[indent:]})
	}
	return lines, nil
}

// Parse compiles .xui source into a primitive tree (each Element.Tag is a
// primitive name; each Attr is a prop). file is used only to label errors.
func Parse(file string, src []byte) (*Doc, error) {
	lines, err := splitLines(file, src)
	if err != nil {
		return nil, err
	}
	p := &parser{file: file, lines: lines}
	i := 0
	nodes, err := p.parseSiblings(&i, 0)
	if err != nil {
		return nil, err
	}
	return &Doc{Nodes: nodes}, nil
}

type parser struct {
	file  string
	lines []rawLine
}

func (p *parser) errf(no int, format string, args ...any) error {
	return errf(p.file, no, format, args...)
}

// parseSiblings consumes lines at exactly the given depth (plus interleaved
// blank lines, which carry no depth of their own) until a shallower line, an
// element line whose parsed children are handled recursively, or EOF.
func (p *parser) parseSiblings(i *int, depth int) ([]Node, error) {
	var nodes []Node
	for *i < len(p.lines) {
		ln := p.lines[*i]
		if ln.blank {
			// A blank line belongs to whichever sibling level the content
			// after it (skipping further blanks) resumes at — not
			// necessarily this one. If that's shallower, let the ancestor
			// call (which requested that shallower depth) consume it.
			nextDepth, ok := p.peekDepth(*i + 1)
			if !ok || nextDepth < depth {
				return nodes, nil
			}
			nodes = append(nodes, &BlankLine{})
			*i++
			continue
		}
		if ln.indent%2 != 0 {
			return nil, p.errf(ln.no, "indentation must be a multiple of 2 spaces")
		}
		lnDepth := ln.indent / 2
		if lnDepth < depth {
			return nodes, nil
		}
		if lnDepth > depth {
			return nil, p.errf(ln.no, "indentation jumps more than one level")
		}

		switch {
		case strings.HasPrefix(ln.content, "#"):
			*i++

		case strings.HasPrefix(ln.content, "--"):
			com := &Comment{Line: ln.no}
			for *i < len(p.lines) {
				cur := p.lines[*i]
				if cur.blank || cur.indent/2 != depth || !strings.HasPrefix(cur.content, "--") {
					break
				}
				text := strings.TrimSpace(strings.TrimPrefix(cur.content, "--"))
				com.Lines = append(com.Lines, text)
				*i++
			}
			nodes = append(nodes, com)

		case strings.HasPrefix(ln.content, `"`) || strings.HasPrefix(ln.content, "("):
			items, err := p.parseItems(ln.content, ln.no)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, &RunNode{Items: items, Line: ln.no})
			*i++

		default:
			elem, err := p.parseElementLine(ln)
			if err != nil {
				return nil, err
			}
			*i++
			childDepth, hasNext := p.peekDepth(*i)
			switch {
			case hasNext && childDepth == depth+1:
				children, err := p.parseSiblings(i, depth+1)
				if err != nil {
					return nil, err
				}
				if len(elem.Inline) > 0 {
					return nil, p.errf(elem.Line, "element <%s> has both inline content and block children", elem.Tag)
				}
				elem.Block = children
			case hasNext && childDepth > depth+1:
				return nil, p.errf(p.lines[*i].no, "indentation jumps more than one level")
			}
			nodes = append(nodes, elem)
		}
	}
	return nodes, nil
}

// peekDepth looks ahead from index i, skipping blank lines, and reports the
// depth of the next non-blank line without consuming anything.
func (p *parser) peekDepth(i int) (int, bool) {
	for i < len(p.lines) {
		if p.lines[i].blank {
			i++
			continue
		}
		return p.lines[i].indent / 2, true
	}
	return 0, false
}

func (p *parser) parseElementLine(ln rawLine) (*Element, error) {
	tag, attrs, items, err := p.parseHead(ln.content, ln.no)
	if err != nil {
		return nil, err
	}
	return &Element{Tag: tag, Attrs: attrs, Inline: items, Line: ln.no}, nil
}

// parseItems parses a whole inline-run line's content: items only, no tag.
func (p *parser) parseItems(content string, line int) ([]Item, error) {
	hp := &headParser{p: p, line: line, s: []rune(content)}
	items, err := hp.parseItemsRest()
	if err != nil {
		return nil, err
	}
	hp.skipSpaces()
	if hp.pos != len(hp.s) {
		return nil, p.errf(line, "unexpected trailing content %q", string(hp.s[hp.pos:]))
	}
	return items, nil
}

// parseHead parses `tag attr* inline-item*` — used both for a top-level
// element line and for the content of a parenthesized inline element.
func (p *parser) parseHead(content string, line int) (tag string, attrs []Attr, items []Item, err error) {
	hp := &headParser{p: p, line: line, s: []rune(content)}
	tag, err = hp.parseIdent()
	if err != nil {
		return "", nil, nil, err
	}
	attrs, items, err = hp.parseAttrsAndItems()
	if err != nil {
		return "", nil, nil, err
	}
	hp.skipSpaces()
	if hp.pos != len(hp.s) {
		return "", nil, nil, p.errf(line, "unexpected trailing content %q", string(hp.s[hp.pos:]))
	}
	return tag, attrs, items, nil
}

// headParser is a small hand-rolled recursive-descent parser over one
// line's content (or one parenthesized group's inner content).
type headParser struct {
	p    *parser
	line int
	s    []rune
	pos  int
}

func (hp *headParser) atEnd() bool { return hp.pos >= len(hp.s) }

func (hp *headParser) skipSpaces() {
	for hp.pos < len(hp.s) && hp.s[hp.pos] == ' ' {
		hp.pos++
	}
}

func (hp *headParser) parseIdent() (string, error) {
	start := hp.pos
	for hp.pos < len(hp.s) && isIdentRune(hp.s[hp.pos]) {
		hp.pos++
	}
	ident := string(hp.s[start:hp.pos])
	if !identRe.MatchString(ident) {
		return "", hp.p.errf(hp.line, "invalid identifier %q", ident)
	}
	return ident, nil
}

func isIdentRune(r rune) bool {
	return r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// parseAttrsAndItems consumes attr* inline-item* after a tag/parenthesis has
// already been read: attrs until the first '"'/'(' token, then items to the
// end.
func (hp *headParser) parseAttrsAndItems() ([]Attr, []Item, error) {
	var attrs []Attr
	for {
		hp.skipSpaces()
		if hp.atEnd() {
			return attrs, nil, nil
		}
		c := hp.s[hp.pos]
		if c == '"' || c == '(' {
			items, err := hp.parseItemsRest()
			if err != nil {
				return nil, nil, err
			}
			return attrs, items, nil
		}
		name, err := hp.parseIdent()
		if err != nil {
			return nil, nil, err
		}
		if !hp.atEnd() && hp.s[hp.pos] == '=' {
			hp.pos++
			if hp.atEnd() || hp.s[hp.pos] != '"' {
				return nil, nil, hp.p.errf(hp.line, "attribute %q: expected a quoted value", name)
			}
			val, err := hp.parseQuoted()
			if err != nil {
				return nil, nil, err
			}
			attrs = append(attrs, Attr{Name: name, Value: val})
		} else {
			attrs = append(attrs, Attr{Name: name, Bare: true})
		}
	}
}

func (hp *headParser) parseItemsRest() ([]Item, error) {
	var items []Item
	for {
		hp.skipSpaces()
		if hp.atEnd() {
			return items, nil
		}
		switch hp.s[hp.pos] {
		case '"':
			v, err := hp.parseQuoted()
			if err != nil {
				return nil, err
			}
			items = append(items, &TextNode{Value: v, Line: hp.line})
		case '(':
			elem, err := hp.parseParenElement()
			if err != nil {
				return nil, err
			}
			items = append(items, elem)
		default:
			return nil, hp.p.errf(hp.line, "expected an inline item, found %q", string(hp.s[hp.pos:]))
		}
	}
}

func (hp *headParser) parseParenElement() (*Element, error) {
	start := hp.pos
	hp.pos++ // consume '('
	depth := 1
	for hp.pos < len(hp.s) && depth > 0 {
		switch hp.s[hp.pos] {
		case '"':
			if err := hp.skipQuotedRaw(); err != nil {
				return nil, err
			}
			continue
		case '(':
			depth++
		case ')':
			depth--
		}
		hp.pos++
	}
	if depth != 0 {
		return nil, hp.p.errf(hp.line, "unclosed '('")
	}
	inner := string(hp.s[start+1 : hp.pos-1])
	tag, attrs, items, err := hp.p.parseHead(inner, hp.line)
	if err != nil {
		return nil, err
	}
	return &Element{Tag: tag, Attrs: attrs, Inline: items, Line: hp.line}, nil
}

// skipQuotedRaw advances past a quoted string (honoring \" escapes) without
// decoding it — used only while scanning for a paren group's matching ')'.
func (hp *headParser) skipQuotedRaw() error {
	hp.pos++ // opening quote
	for hp.pos < len(hp.s) {
		c := hp.s[hp.pos]
		if c == '\\' && hp.pos+1 < len(hp.s) {
			hp.pos += 2
			continue
		}
		if c == '"' {
			hp.pos++
			return nil
		}
		hp.pos++
	}
	return hp.p.errf(hp.line, "unterminated string")
}

// parseQuoted decodes a "..." literal (escapes \" \\ \n) starting at the
// opening quote.
func (hp *headParser) parseQuoted() (string, error) {
	hp.pos++ // opening quote
	var b strings.Builder
	for hp.pos < len(hp.s) {
		c := hp.s[hp.pos]
		if c == '\\' {
			if hp.pos+1 >= len(hp.s) {
				return "", hp.p.errf(hp.line, "unterminated escape sequence")
			}
			switch hp.s[hp.pos+1] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			default:
				return "", hp.p.errf(hp.line, "unknown escape sequence \\%c", hp.s[hp.pos+1])
			}
			hp.pos += 2
			continue
		}
		if c == '"' {
			hp.pos++
			return b.String(), nil
		}
		b.WriteRune(c)
		hp.pos++
	}
	return "", hp.p.errf(hp.line, "unterminated string")
}
