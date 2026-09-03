// Package i18nstrings is the runtime half of the Catalog (root docs/adr/0006):
// the flat-TOML reader the catalogs and chgksuite's label files share, the
// plural rules, and the error type whose message a person may read.
package i18nstrings

import (
	"fmt"
	"strings"
)

// Pair is one `key = "value"` line, with the [table] it fell under ("" before
// the first header) and the 1-based line it was written on.
type Pair struct {
	Table string
	Key   string
	Value string
	Line  int
}

// Parse reads the TOML subset both users need: [table] headers, key = "string",
// # comments, the basic-string escapes and """ multi-line basic strings.
func Parse(text string) ([]Pair, error) {
	var out []Pair
	lines := strings.Split(text, "\n")
	table := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: not a key = value pair", i+1)
		}
		start := i
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"""`) {
			for !isMultiline(value) {
				i++
				if i >= len(lines) {
					return nil, fmt.Errorf(`line %d: unterminated """ string`, start+1)
				}
				value += "\n" + lines[i]
			}
		}
		s, err := unquote(value)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", start+1, err)
		}
		out = append(out, Pair{Table: table, Key: strings.TrimSpace(key), Value: s, Line: start + 1})
	}
	return out, nil
}

func isMultiline(v string) bool {
	return len(v) >= 6 && strings.HasPrefix(v, `"""`) && strings.HasSuffix(v, `"""`)
}

func unquote(v string) (string, error) {
	body := ""
	switch {
	case isMultiline(v):
		body = strings.TrimPrefix(v[3:len(v)-3], "\n")
	case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
		body = v[1 : len(v)-1]
	default:
		return "", fmt.Errorf("value %q is not a quoted string", v)
	}
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			out.WriteByte(body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			out.WriteByte('\n')
		case 't':
			out.WriteByte('\t')
		default:
			out.WriteByte(body[i])
		}
	}
	return out.String(), nil
}
