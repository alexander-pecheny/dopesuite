package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

// applySetOpsGo mirrors the client's applySetPatch (static/match-table.js): it
// applies "set" ops to a decoded JSON value and returns the result. The test
// uses it to prove that ops produced by matchDeltaOps reconstruct newJSON
// exactly when applied to oldJSON — the invariant the client relies on.
func applySetOpsGo(root any, ops []matchDeltaSetOp) any {
	for _, op := range ops {
		if op.Op != "" && op.Op != "set" {
			continue
		}
		var val any
		if err := json.Unmarshal(op.Value, &val); err != nil {
			panic(err)
		}
		root = setAtPath(root, op.Path, val)
	}
	return root
}

func setAtPath(root any, path []any, value any) any {
	if len(path) == 0 {
		return value
	}
	seg := path[0]
	rest := path[1:]
	switch s := seg.(type) {
	case float64: // JSON numbers decode to float64 (array index)
		idx := int(s)
		arr, _ := root.([]any)
		for len(arr) <= idx {
			arr = append(arr, nil)
		}
		arr[idx] = setAtPath(arr[idx], rest, value)
		return arr
	case string:
		obj, ok := root.(map[string]any)
		if !ok || obj == nil {
			obj = map[string]any{}
		}
		obj[s] = setAtPath(obj[s], rest, value)
		return obj
	default:
		return root
	}
}

// roundTrip diffs oldJSON→newJSON (the raw differ, independent of the size
// gate), then applies the ops to oldJSON and checks the result deep-equals
// newJSON. This proves the diff is correct; the gate is tested separately.
// Path segments re-decode through JSON so numeric indices arrive as float64.
func roundTrip(t *testing.T, oldJSON, newJSON string) []matchDeltaSetOp {
	t.Helper()
	var ops []matchDeltaSetOp
	diffJSONInto(nil, json.RawMessage(oldJSON), json.RawMessage(newJSON), &ops)
	// Re-encode/decode the ops so numeric path segments become float64, exactly
	// as they arrive on the client after JSON transport.
	opsJSON, err := json.Marshal(ops)
	if err != nil {
		t.Fatalf("marshal ops: %v", err)
	}
	var wireOps []matchDeltaSetOp
	if err := json.Unmarshal(opsJSON, &wireOps); err != nil {
		t.Fatalf("unmarshal ops: %v", err)
	}
	var oldV, newV any
	if err := json.Unmarshal([]byte(oldJSON), &oldV); err != nil {
		t.Fatalf("old: %v", err)
	}
	if err := json.Unmarshal([]byte(newJSON), &newV); err != nil {
		t.Fatalf("new: %v", err)
	}
	got := applySetOpsGo(oldV, wireOps)
	if !reflect.DeepEqual(got, newV) {
		t.Fatalf("round-trip mismatch\n ops:  %s\n got:  %#v\n want: %#v", opsJSON, got, newV)
	}
	return wireOps
}

func TestMatchDeltaOps_Identical(t *testing.T) {
	if _, ok := matchDeltaOps([]byte(`{"a":1}`), []byte(`{"a":1}`)); ok {
		t.Fatal("identical inputs should not produce a delta")
	}
}

func TestMatchDeltaOps_LeafChange(t *testing.T) {
	old := `{"finished":false,"teams":[{"name":"A","total":3},{"name":"B","total":5}],"revision":10}`
	neu := `{"finished":false,"teams":[{"name":"A","total":4},{"name":"B","total":5}],"revision":11}`
	ops := roundTrip(t, old, neu)
	// Two leaves changed: teams[0].total and revision.
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d: %+v", len(ops), ops)
	}
}

func TestMatchDeltaOps_NestedAndAddedKey(t *testing.T) {
	old := `{"teams":[{"name":"A","themes":[{"score":1}]}]}`
	neu := `{"teams":[{"name":"A","themes":[{"score":2}]}],"venue":{"number":3}}`
	roundTrip(t, old, neu)
}

func TestMatchDeltaOps_RemovedKeyBecomesNull(t *testing.T) {
	// Removed key encodes as set-null; round-trip yields null (renders as absent).
	old := `{"venue":{"number":3},"finished":true}`
	neu := `{"finished":true}`
	var ops []matchDeltaSetOp
	diffJSONInto(nil, json.RawMessage(old), json.RawMessage(neu), &ops)
	opsJSON, _ := json.Marshal(ops)
	var wireOps []matchDeltaSetOp
	_ = json.Unmarshal(opsJSON, &wireOps)
	var oldV any
	_ = json.Unmarshal([]byte(old), &oldV)
	got := applySetOpsGo(oldV, wireOps).(map[string]any)
	if v, present := got["venue"]; !present || v != nil {
		t.Fatalf("removed key should be null, got %#v", got["venue"])
	}
}

func TestMatchDeltaOps_ArrayLengthChangeReplacesArray(t *testing.T) {
	// Different array length can't be set-patched element-wise; whole-array set.
	old := `{"standings":[1,2]}`
	neu := `{"standings":[1,2,3]}`
	roundTrip(t, old, neu)
}

func TestMatchDeltaOps_GateAcceptsSmallChangeInLargeState(t *testing.T) {
	// Realistic MatchView size: a single-field change should be accepted as a
	// delta (the gate only rejects when ops aren't meaningfully smaller).
	pad := func(total int) string {
		s := `{"finished":false,"revision":1,"teams":[`
		for i := 0; i < 4; i++ {
			if i > 0 {
				s += ","
			}
			s += `{"name":"Team` + string(rune('A'+i)) + `","total":` + strconv.Itoa(total) + `,"roster":["p1","p2","p3","p4","p5","p6"],"themes":[{"player":"p1","answers":["1","1","1","1","1"],"score":5}]}`
		}
		return s + `]}`
	}
	old := pad(3)
	neu := pad(4) // every team's total flips 3->4: 4 small ops vs a ~600B state
	opsJSON, ok := matchDeltaOps([]byte(old), []byte(neu))
	if !ok {
		t.Fatalf("expected delta accepted for small change in large state (ops=%s, statelen=%d)", opsJSON, len(neu))
	}
}

func TestMatchDeltaOps_FallbackWhenNotSmaller(t *testing.T) {
	// A wholesale change (every field differs) should not be a delta — the ops
	// would be as big as just resending state.
	old := `{"a":"xxxxxxxxxx","b":"yyyyyyyyyy","c":"zzzzzzzzzz"}`
	neu := `{"a":"AAAAAAAAAA","b":"BBBBBBBBBB","c":"CCCCCCCCCC"}`
	if _, ok := matchDeltaOps([]byte(old), []byte(neu)); ok {
		t.Fatal("wholesale change should fall back to full state")
	}
}

func TestMatchDeltaOps_EmptyInputs(t *testing.T) {
	if _, ok := matchDeltaOps(nil, []byte(`{"a":1}`)); ok {
		t.Fatal("empty old should not delta")
	}
	if _, ok := matchDeltaOps([]byte(`{"a":1}`), nil); ok {
		t.Fatal("empty new should not delta")
	}
}
