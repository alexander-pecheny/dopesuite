// Package schemedsl is the simplified scheme DSL (docs/scheme-dsl.md,
// ADR-0006): Parse reads the sectioned key:value text a host authors, Compile
// macroexpands it into the detailed scheme document the runtime already speaks.
package schemedsl

import (
	"fmt"
	"strconv"
	"strings"

	dopestrings "dope/i18nstrings"
)

// Error is a parse or compile failure pinned to a 1-based source line.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return dopestrings.Default.Scheme.Error.LinePrefix(strconv.Itoa(e.Line), e.Msg)
	}
	return e.Msg
}

func errAt(line int, format string, args ...any) *Error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// Value is one raw key's value plus the line it came from; typing happens at
// the accessor so every complaint can cite the source line.
type Value struct {
	Line int
	Raw  string
}

// Section is one [header]'s key set (or one --- block inside [scheme]).
type Section struct {
	Line   int
	Values map[string]Value
}

// SortRule is one sorting comparator token: a metric plus asc/desc (desc is
// the default — every ranking metric here is better-when-bigger).
type SortRule struct {
	Metric string
	Dir    string
}

// Doc is a parsed DSL document.
type Doc struct {
	Defaults Section
	Init     Section
	Blocks   []Section
}

func newSection(line int) Section {
	return Section{Line: line, Values: map[string]Value{}}
}

// Parse reads DSL source: `[defaults]`/`[init]`/`[scheme]` sections of
// `key: value` lines, `---` separating blocks inside [scheme], `#` starting a
// comment. It returns the first error, pinned to its line.
func Parse(src string) (*Doc, error) {
	doc := &Doc{Defaults: newSection(0), Init: newSection(0)}
	const noSection = ""
	section := noSection
	var block *Section
	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := stripComment(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			switch name {
			case "defaults", "init", "scheme":
				section = name
				block = nil
			default:
				return nil, errAt(lineNo, "%s", dopestrings.Default.Scheme.Parse.UnknownSection(name))
			}
			continue
		}
		if line == "---" {
			if section != "scheme" {
				return nil, errAt(lineNo, "%s", dopestrings.Default.Scheme.Parse.Separator())
			}
			block = nil
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return nil, errAt(lineNo, "%s", dopestrings.Default.Scheme.Parse.UnparsedLine(line))
		}
		target, err := doc.sectionFor(section, &block, lineNo)
		if err != nil {
			return nil, err
		}
		if prev, dup := target.Values[key]; dup {
			return nil, errAt(lineNo, "%s", dopestrings.Default.Scheme.Parse.DuplicateKey(key, strconv.Itoa(prev.Line)))
		}
		target.Values[key] = Value{Line: lineNo, Raw: strings.TrimSpace(value)}
	}
	return doc, nil
}

func (doc *Doc) sectionFor(section string, block **Section, lineNo int) (*Section, error) {
	switch section {
	case "defaults":
		return &doc.Defaults, nil
	case "init":
		return &doc.Init, nil
	case "scheme":
		if *block == nil {
			doc.Blocks = append(doc.Blocks, newSection(lineNo))
			*block = &doc.Blocks[len(doc.Blocks)-1]
		}
		return *block, nil
	default:
		return nil, errAt(lineNo, "%s", dopestrings.Default.Scheme.Parse.KeyOutsideSection())
	}
}

// stripComment trims a `#`-to-EOL comment (at line start or after whitespace)
// and surrounding space.
func stripComment(line string) string {
	for pos := 0; pos < len(line); pos++ {
		if line[pos] != '#' {
			continue
		}
		if pos == 0 || line[pos-1] == ' ' || line[pos-1] == '\t' {
			line = line[:pos]
			break
		}
	}
	return strings.TrimSpace(line)
}

// --- typed accessors ------------------------------------------------------

func (s Section) value(key string) (Value, bool) {
	v, ok := s.Values[key]
	return v, ok
}

// Str returns a scalar string value ("" when absent).
func (s Section) Str(key string) (string, bool) {
	v, ok := s.value(key)
	return v.Raw, ok
}

// Int returns an int value; a malformed number reads as absent (use IntErr
// when the caller wants the complaint).
func (s Section) Int(key string) (int, bool) {
	v, ok := s.value(key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v.Raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// IntErr returns an int value or a line-pinned error.
func (s Section) IntErr(key string) (int, error) {
	v, ok := s.value(key)
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(v.Raw)
	if err != nil {
		return 0, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.IntExpected(key, v.Raw))
	}
	return n, nil
}

// Bool reads `true`/`false`. Any other raw (e.g. a round code in `reseed: r4`)
// reads as not-a-bool; callers that accept both consult Str too.
func (s Section) Bool(key string) (bool, bool) {
	v, ok := s.value(key)
	if !ok {
		return false, false
	}
	switch v.Raw {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// List returns a bracketed comma-separated list.
func (s Section) List(key string) ([]string, bool, error) {
	v, ok := s.value(key)
	if !ok {
		return nil, false, nil
	}
	if !strings.HasPrefix(v.Raw, "[") || !strings.HasSuffix(v.Raw, "]") {
		return nil, false, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.ListExpected(key))
	}
	inner := strings.TrimSpace(v.Raw[1 : len(v.Raw)-1])
	if inner == "" {
		return []string{}, true, nil
	}
	parts := strings.Split(inner, ",")
	items := make([]string, len(parts))
	for i, part := range parts {
		items[i] = strings.TrimSpace(part)
	}
	return items, true, nil
}

// IntList parses a numeric list like `points: [2, 1, 0]`.
func (s Section) IntList(key string) ([]int, bool, error) {
	items, ok, err := s.List(key)
	if !ok || err != nil {
		return nil, ok, err
	}
	v, _ := s.value(key)
	nums := make([]int, len(items))
	for i, item := range items {
		n, err := strconv.Atoi(item)
		if err != nil {
			return nil, true, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.NotANumber(key, item))
		}
		nums[i] = n
	}
	return nums, true, nil
}

// NumList parses a list that may carry fractions — `points: [1, 0.5, 0]`,
// which is what a format paying half a point for a draw writes.
func (s Section) NumList(key string) ([]float64, bool, error) {
	items, ok, err := s.List(key)
	if !ok || err != nil {
		return nil, ok, err
	}
	v, _ := s.value(key)
	nums := make([]float64, len(items))
	for i, item := range items {
		n, err := strconv.ParseFloat(item, 64)
		if err != nil {
			return nil, true, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.NotANumber(key, item))
		}
		nums[i] = n
	}
	return nums, true, nil
}

// Sorting parses comparator tokens with optional asc/desc suffixes.
func (s Section) Sorting(key string) ([]SortRule, bool, error) {
	items, ok, err := s.List(key)
	if !ok || err != nil {
		return nil, ok, err
	}
	v, _ := s.value(key)
	rules := make([]SortRule, len(items))
	for i, item := range items {
		fields := strings.Fields(item)
		// No direction unless the scheme writes one. Which way a metric reads is
		// a property of the metric — place and draw are lower-is-better, points
		// are higher — and defaulting to desc here silently overrode that.
		rule := SortRule{}
		switch len(fields) {
		case 1:
			rule.Metric = fields[0]
		case 2:
			rule.Metric = fields[0]
			if fields[1] != "asc" && fields[1] != "desc" {
				return nil, true, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.SortDirection(key, fields[1]))
			}
			rule.Dir = fields[1]
		default:
			return nil, true, errAt(v.Line, "%s", dopestrings.Default.Scheme.Parse.SortToken(key, item))
		}
		rules[i] = rule
	}
	return rules, true, nil
}
