package pptx

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is pptx_config.toml. Every knob chgksuite reads is here, and reading
// one that is absent gives the fallback the Python does — which is why the
// getters take one rather than the struct carrying defaults.
type Config struct {
	values map[string]any
}

// DefaultConfig is the file chgksuite ships, embedded.
func DefaultConfig() (*Config, error) { return ParseConfig(defaultConfigTOML) }

// ParseConfig reads pptx_config.toml. It is a TOML subset — tables, strings,
// numbers, booleans, arrays (which may run over several lines) and the inline
// tables a size grid is written as — and it refuses what it does not understand
// rather than half-reading it.
func ParseConfig(src string) (*Config, error) {
	c := &Config{values: map[string]any{}}
	table := c.values
	lines := strings.Split(src, "\n")
	for n := 0; n < len(lines); n++ {
		line := strings.TrimSpace(stripTOMLComment(lines[n]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && !strings.Contains(line, "=") {
			name := strings.Trim(line, "[]")
			if name == "" || strings.ContainsAny(name, "[]") {
				return nil, fmt.Errorf("line %d: %q is not a table header", n+1, line)
			}
			sub := map[string]any{}
			c.values[name] = sub
			table = sub
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: %q is neither a table nor a key", n+1, line)
		}
		// A value whose brackets do not close on this line runs on to the next.
		start := n
		value = strings.TrimSpace(value)
		for unbalanced(value) {
			n++
			if n >= len(lines) {
				return nil, fmt.Errorf("line %d: the value is never closed", start+1)
			}
			value += " " + strings.TrimSpace(stripTOMLComment(lines[n]))
		}
		v, err := parseTOMLValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", start+1, err)
		}
		table[strings.TrimSpace(key)] = v
	}
	return c, nil
}

// unbalanced reports whether a value has brackets still open, ignoring any
// inside a string.
func unbalanced(s string) bool {
	depth, inQuotes := 0, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case inQuotes:
		case r == '[' || r == '{':
			depth++
		case r == ']' || r == '}':
			depth--
		}
	}
	return depth > 0
}

// stripTOMLComment drops a trailing # comment that is not inside a string.
func stripTOMLComment(s string) string {
	inQuotes := false
	for i, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == '#' && !inQuotes:
			return s[:i]
		}
	}
	return s
}

func parseTOMLValue(s string) (any, error) {
	switch {
	case s == "true":
		return true, nil
	case s == "false":
		return false, nil
	case strings.HasPrefix(s, `"`):
		if !strings.HasSuffix(s, `"`) || len(s) < 2 {
			return nil, fmt.Errorf("unterminated string %q", s)
		}
		return strconv.Unquote(s)
	case strings.HasPrefix(s, "["):
		return parseTOMLArray(s)
	case strings.HasPrefix(s, "{"):
		return parseInlineTable(s)
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(n), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("%q is not a value this reader knows", s)
	}
	return f, nil
}

func parseTOMLArray(s string) (any, error) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	if inner == "" {
		return []any{}, nil
	}
	var out []any
	for _, part := range splitTOMLItems(inner) {
		part = strings.TrimSpace(part)
		// A trailing comma leaves an empty last item, which TOML allows.
		if part == "" {
			continue
		}
		v, err := parseTOMLValue(part)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// parseInlineTable reads a {k = v, k = v}, which is how one band of a size grid
// is written.
func parseInlineTable(s string) (any, error) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}"))
	out := map[string]any{}
	if inner == "" {
		return out, nil
	}
	for _, part := range splitTOMLItems(inner) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not a key in an inline table", strings.TrimSpace(part))
		}
		v, err := parseTOMLValue(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		out[strings.TrimSpace(key)] = v
	}
	return out, nil
}

// splitTOMLItems splits on commas that are not inside a string or a nested
// bracket.
func splitTOMLItems(s string) []string {
	var out []string
	depth, inQuotes, start := 0, false, 0
	for i, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case inQuotes:
		case r == '[' || r == '{':
			depth++
		case r == ']' || r == '}':
			depth--
		case r == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ── reading ─────────────────────────────────────────────────────────────────

func (c *Config) table(name string) map[string]any {
	if t, ok := c.values[name].(map[string]any); ok {
		return t
	}
	return nil
}

func (c *Config) has(name string) bool { _, ok := c.values[name]; return ok }

func (c *Config) str(name, fallback string) string {
	if s, ok := c.values[name].(string); ok {
		return s
	}
	return fallback
}

func (c *Config) boolean(name string, fallback bool) bool {
	if b, ok := c.values[name].(bool); ok {
		return b
	}
	return fallback
}

func (c *Config) num(name string) (float64, bool) {
	f, ok := c.values[name].(float64)
	return f, ok
}

func tableStr(t map[string]any, key, fallback string) string {
	if s, ok := t[key].(string); ok {
		return s
	}
	return fallback
}

func tableBool(t map[string]any, key string, fallback bool) bool {
	if b, ok := t[key].(bool); ok {
		return b
	}
	return fallback
}

func tableNum(t map[string]any, key string) (float64, bool) {
	f, ok := t[key].(float64)
	return f, ok
}

func tableNumOr(t map[string]any, key string, fallback float64) float64 {
	if f, ok := tableNum(t, key); ok {
		return f
	}
	return fallback
}

// colorOf reads a [r, g, b] array as the "RRGGBB" a run wants.
func colorOf(t map[string]any, key string) string {
	list, ok := t[key].([]any)
	if !ok || len(list) != 3 {
		return ""
	}
	var rgb [3]int
	for i, v := range list {
		f, ok := v.(float64)
		if !ok {
			return ""
		}
		rgb[i] = int(f)
	}
	return fmt.Sprintf("%02X%02X%02X", rgb[0], rgb[1], rgb[2])
}
