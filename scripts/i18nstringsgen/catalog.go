package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template/parse"

	core "pecheny.me/dopecore/i18nstrings"
)

// A catalog is one module's strings in one language: surfaces, each a file.
type catalog struct {
	lang     string
	surfaces []*surface
}

type surface struct {
	name   string
	file   string
	bare   []*str   // keys written before any [table]
	groups []*group // one per [table]
	index  map[string]bool
}

type group struct {
	name    string
	strings []*str
}

// A str is one catalog entry, already reduced to the chunks it renders as.
type str struct {
	id     string
	key    string
	line   int
	chunks []chunk
	params []param
}

type param struct {
	name  string
	isInt bool
}

const (
	chunkText = iota
	chunkField
	chunkPlural
)

type chunk struct {
	kind  int
	text  string    // chunkText
	param string    // chunkField, chunkPlural
	forms [3]string // chunkPlural
}

var (
	ident = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// A parameter becomes an argument name in both Go and TypeScript, so it
	// must be an identifier neither language reserves — snake_case like a key.
	reserved = map[string]bool{
		"break": true, "case": true, "catch": true, "chan": true, "class": true,
		"const": true, "continue": true, "default": true, "defer": true, "delete": true,
		"do": true, "else": true, "enum": true, "export": true, "extends": true,
		"fallthrough": true, "false": true, "for": true, "func": true, "function": true,
		"go": true, "goto": true, "if": true, "import": true, "in": true,
		"instanceof": true, "interface": true, "map": true, "new": true, "null": true,
		"package": true, "range": true, "return": true, "select": true, "struct": true,
		"super": true, "switch": true, "this": true, "throw": true, "true": true,
		"try": true, "type": true, "typeof": true, "var": true, "void": true,
		"while": true, "with": true, "yield": true,
	}
)

// plural exists only so text/template/parse accepts the name; nothing runs it.
func plural(int, string, string, string) string { return "" }

func readCatalog(dir, lang string) (*catalog, error) {
	files, err := filepath.Glob(filepath.Join(dir, lang, "*.toml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	c := &catalog{lang: lang}
	for _, file := range files {
		s, err := readSurface(file)
		if err != nil {
			return nil, err
		}
		c.surfaces = append(c.surfaces, s)
	}
	return c, nil
}

func readSurface(file string) (*surface, error) {
	name := strings.TrimSuffix(filepath.Base(file), ".toml")
	if !ident.MatchString(name) {
		return nil, fmt.Errorf("%s: %q is not a snake_case surface name", file, name)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pairs, err := core.Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	s := &surface{name: name, file: file, index: map[string]bool{}}
	byTable := map[string]*group{}
	for _, p := range pairs {
		where := fmt.Sprintf("%s:%d", file, p.Line)
		if !ident.MatchString(p.Key) {
			return nil, fmt.Errorf("%s: %q is not a snake_case key", where, p.Key)
		}
		if p.Table != "" && !ident.MatchString(p.Table) {
			return nil, fmt.Errorf("%s: %q is not a snake_case table name", where, p.Table)
		}
		id := name + "." + p.Key
		if p.Table != "" {
			id = name + "." + p.Table + "." + p.Key
		}
		entry, err := compile(id, p.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", where, id, err)
		}
		entry.key, entry.line = p.Key, p.Line
		if s.index[id] {
			return nil, fmt.Errorf("%s: %s is defined twice", where, id)
		}
		s.index[id] = true
		if p.Table == "" {
			s.bare = append(s.bare, entry)
			continue
		}
		g := byTable[p.Table]
		if g == nil {
			g = &group{name: p.Table}
			byTable[p.Table] = g
			s.groups = append(s.groups, g)
		}
		g.strings = append(g.strings, entry)
	}
	sort.Slice(s.bare, func(i, j int) bool { return s.bare[i].key < s.bare[j].key })
	sort.Slice(s.groups, func(i, j int) bool { return s.groups[i].name < s.groups[j].name })
	for _, g := range s.groups {
		if hasBare(s.bare, g.name) {
			return nil, fmt.Errorf("%s: %s.%s is both a key and a table", file, name, g.name)
		}
		sort.Slice(g.strings, func(i, j int) bool { return g.strings[i].key < g.strings[j].key })
	}
	return s, nil
}

func hasBare(bare []*str, name string) bool {
	for _, b := range bare {
		if b.key == name {
			return true
		}
	}
	return false
}

// compile reduces a template to chunks, rejecting everything outside the
// subset: text, {{.field}} and {{plural .n "one" "few" "many"}}.
func compile(id, text string) (*str, error) {
	trees, err := parse.Parse(id, text, "", "", map[string]any{"plural": plural})
	if err != nil {
		return nil, err
	}
	e := &str{id: id}
	ints := map[string]bool{}
	seen := map[string]bool{}
	addParam := func(name string) error {
		switch {
		case !ident.MatchString(name):
			return fmt.Errorf("%q is not a snake_case parameter name", name)
		case reserved[name]:
			return fmt.Errorf("%q is a reserved word in Go or TypeScript", name)
		}
		if !seen[name] {
			seen[name] = true
			e.params = append(e.params, param{name: name})
		}
		return nil
	}
	for _, n := range trees[id].Root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			e.chunks = append(e.chunks, chunk{kind: chunkText, text: string(n.Text)})
		case *parse.ActionNode:
			c, err := action(n)
			if err != nil {
				return nil, err
			}
			if err := addParam(c.param); err != nil {
				return nil, err
			}
			if c.kind == chunkPlural {
				ints[c.param] = true
			}
			e.chunks = append(e.chunks, c)
		default:
			return nil, fmt.Errorf("%s is not allowed here (only text, {{.name}} and {{plural .n \"one\" \"few\" \"many\"}})", n)
		}
	}
	for i := range e.params {
		e.params[i].isInt = ints[e.params[i].name]
	}
	return e, nil
}

func action(n *parse.ActionNode) (chunk, error) {
	bad := func() (chunk, error) {
		return chunk{}, fmt.Errorf("%s is not allowed here (only {{.name}} and {{plural .n \"one\" \"few\" \"many\"}})", n)
	}
	if len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
		return bad()
	}
	args := n.Pipe.Cmds[0].Args
	if f, ok := args[0].(*parse.FieldNode); ok {
		if len(args) != 1 || len(f.Ident) != 1 {
			return bad()
		}
		return chunk{kind: chunkField, param: f.Ident[0]}, nil
	}
	fn, ok := args[0].(*parse.IdentifierNode)
	if !ok || fn.Ident != "plural" || len(args) != 5 {
		return bad()
	}
	count, ok := args[1].(*parse.FieldNode)
	if !ok || len(count.Ident) != 1 {
		return bad()
	}
	c := chunk{kind: chunkPlural, param: count.Ident[0]}
	for i := 0; i < 3; i++ {
		s, ok := args[2+i].(*parse.StringNode)
		if !ok {
			return bad()
		}
		c.forms[i] = s.Text
	}
	return c, nil
}

// ids lists every string id in the catalog, sorted.
func (c *catalog) ids() []string {
	var out []string
	for _, s := range c.surfaces {
		for id := range s.index {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (c *catalog) byID() map[string]*str {
	out := map[string]*str{}
	for _, s := range c.surfaces {
		for _, e := range s.bare {
			out[e.id] = e
		}
		for _, g := range s.groups {
			for _, e := range g.strings {
				out[e.id] = e
			}
		}
	}
	return out
}

// agrees reports why c differs from the reference catalog, or nil.
func (c *catalog) agrees(ref *catalog) error {
	got, want := c.byID(), ref.byID()
	for _, id := range c.ids() {
		if want[id] == nil {
			return fmt.Errorf("%s defines %s, which %s does not", c.lang, id, ref.lang)
		}
	}
	for _, id := range ref.ids() {
		e := got[id]
		if e == nil {
			return fmt.Errorf("%s does not define %s", c.lang, id)
		}
		if signature(e) != signature(want[id]) {
			return fmt.Errorf("%s takes %s in %s but %s in %s",
				id, signature(e), c.lang, signature(want[id]), ref.lang)
		}
	}
	return nil
}

func signature(e *str) string {
	var parts []string
	for _, p := range e.params {
		kind := "string"
		if p.isInt {
			kind = "int"
		}
		parts = append(parts, p.name+" "+kind)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
