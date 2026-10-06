package storeutil

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPKWhere(t *testing.T) {
	where, args, err := PKWhere([]string{"fest_id", `we"ird`}, map[string]any{"fest_id": 1, `we"ird`: "x", "other": 2})
	if err != nil || where != `"fest_id" = ? and "we""ird" = ?` || len(args) != 2 || args[0] != 1 || args[1] != "x" {
		t.Fatalf("got %q %v %v", where, args, err)
	}
	if _, _, err := PKWhere(nil, nil); err == nil {
		t.Fatal("no pk accepted")
	}
	if _, _, err := PKWhere([]string{"id"}, map[string]any{}); err == nil {
		t.Fatal("missing pk column accepted")
	}
}

func TestJSONToSQLValue(t *testing.T) {
	cases := []struct{ in, want any }{
		{nil, nil},
		{json.Number("42"), int64(42)},
		{json.Number("4.5"), 4.5},
		{json.Number("1e3"), 1000.0},
		{json.Number("99999999999999999999"), 1e20},
		{"s", "s"},
		{true, true},
	}
	for _, c := range cases {
		if got := JSONToSQLValue(c.in); got != c.want {
			t.Errorf("%v: got %#v want %#v", c.in, got, c.want)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]any{"b": 1, "a": 2, "c": 3})
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("got %v", got)
	}
}
