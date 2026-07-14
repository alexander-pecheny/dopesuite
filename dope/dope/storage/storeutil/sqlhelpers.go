package storeutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Generic SQL/JSON helpers shared by the journal row codec, the converters and
// the replay engine. Pure string/value manipulation — no DB handle.

// QuoteIdent renders a double-quoted SQL identifier, escaping embedded quotes.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// SortedKeys returns a map's keys in sorted order, for deterministic SQL.
func SortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// PKWhere builds a "pk1 = ? and pk2 = ?" clause plus its args from a row's
// primary-key columns.
func PKWhere(pks []string, row map[string]any) (string, []any, error) {
	if len(pks) == 0 {
		return "", nil, errors.New("table has no primary key")
	}
	clauses := make([]string, len(pks))
	args := make([]any, len(pks))
	for i, p := range pks {
		v, ok := row[p]
		if !ok {
			return "", nil, fmt.Errorf("row json missing pk column %q", p)
		}
		clauses[i] = QuoteIdent(p) + " = ?"
		args[i] = v
	}
	return strings.Join(clauses, " and "), args, nil
}

// JSONToSQLValue coerces a json.Number (from a UseNumber decode) to int64 or
// float64 so it round-trips through SQLite as a number, not a string; other
// values pass through unchanged.
func JSONToSQLValue(v any) any {
	switch n := v.(type) {
	case nil:
		return nil
	case json.Number:
		str := n.String()
		if !strings.ContainsAny(str, ".eE") {
			if i, err := strconv.ParseInt(str, 10, 64); err == nil {
				return i
			}
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
		return str
	default:
		return v
	}
}
