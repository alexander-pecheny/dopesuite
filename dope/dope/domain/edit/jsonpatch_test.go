package edit

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func rawPath(parts ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(parts))
	for i, p := range parts {
		out[i] = json.RawMessage(p)
	}
	return out
}

func TestParseJSONPatchPath(t *testing.T) {
	good, err := ParseJSONPatchPath(rawPath(`"entries"`, `3`, `"score"`))
	if err != nil {
		t.Fatal(err)
	}
	want := []JSONPathSegment{{Key: "entries"}, {Index: 3, IsIndex: true}, {Key: "score"}}
	if !reflect.DeepEqual(good, want) {
		t.Fatalf("got %+v", good)
	}
	bad := map[string][]json.RawMessage{
		"empty path":     nil,
		"empty key":      rawPath(`""`),
		"negative index": rawPath(`-1`),
		"float index":    rawPath(`1.5`),
		"huge index":     rawPath(`4097`),
		"object segment": rawPath(`{"a":1}`),
	}
	for name, parts := range bad {
		if _, err := ParseJSONPatchPath(parts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestApplyJSONSetCreatesIntermediates(t *testing.T) {
	path, _ := ParseJSONPatchPath(rawPath(`"entries"`, `2`, `"score"`))
	root, err := ApplyJSONSet(nil, path, 7.0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(root)
	if string(got) != `{"entries":[null,null,{"score":7}]}` {
		t.Fatalf("got %s", got)
	}
	// A second set lands in the same document without disturbing its neighbours.
	path2, _ := ParseJSONPatchPath(rawPath(`"entries"`, `0`))
	root, err = ApplyJSONSet(root, path2, "x")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(root)
	if string(got) != `{"entries":["x",null,{"score":7}]}` {
		t.Fatalf("got %s", got)
	}
}

func TestApplyJSONSetRefusesToCrossScalars(t *testing.T) {
	path, _ := ParseJSONPatchPath(rawPath(`"a"`, `"b"`))
	if _, err := ApplyJSONSet(map[string]any{"a": 1.0}, path, 2.0); err == nil || !strings.Contains(err.Error(), "non-object") {
		t.Fatalf("err = %v", err)
	}
	path, _ = ParseJSONPatchPath(rawPath(`"a"`, `0`))
	if _, err := ApplyJSONSet(map[string]any{"a": "s"}, path, 2.0); err == nil || !strings.Contains(err.Error(), "non-array") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodePatchValue(t *testing.T) {
	if _, err := DecodePatchValue(nil); err == nil {
		t.Fatal("missing value accepted")
	}
	v, err := DecodePatchValue(json.RawMessage(`{"n":[1,"a"]}`))
	if err != nil || !reflect.DeepEqual(v, map[string]any{"n": []any{1.0, "a"}}) {
		t.Fatalf("got %#v err %v", v, err)
	}
}

func TestPatchPathTouchesRatingRoster(t *testing.T) {
	// ОД's roster is imported from rating.chgk.info and lives under "teams".
	od := []JSONPathSegment{{Key: "teams"}, {Index: 0, IsIndex: true}}
	if !PatchPathTouchesRatingRoster("od", od) {
		t.Fatal("od teams path not protected")
	}
	if PatchPathTouchesRatingRoster("od", []JSONPathSegment{{Key: "entries"}}) {
		t.Fatal("od entries path protected")
	}
	if PatchPathTouchesRatingRoster("ek", od) {
		t.Fatal("ek has no rating roster")
	}
}
