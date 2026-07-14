package realtime

import (
	"bytes"
	"encoding/json"
)

// setOp is one "set value at path" operation in a scoped delta. It matches the
// shape the client applies (static/match-table.js applySetPatch): path segments
// are object keys (string) or array indices (number), and value is the new JSON
// subtree to assign there.
type setOp struct {
	Op    string          `json:"op"`
	Path  []any           `json:"path"`
	Value json.RawMessage `json:"value"`
}

// MatchDeltaOps computes the set-ops that turn oldJSON into newJSON, then
// decides whether broadcasting them as a delta is worthwhile. It returns the
// marshaled ops and ok=true only when a delta is both valid and meaningfully
// smaller than the full new state; otherwise ok=false and the caller should
// broadcast the full snapshot. Both inputs must be deterministic marshalings of
// the same Go type (identical key order), which holds for MatchView.
//
// The op set is "set"-only — the client cannot delete keys — so a removed key
// is encoded as set-to-null, which renders identically to absent for the match
// view's optional fields. Any structural change the diff can't express as a
// cheap set (array length/type change) collapses to one set of the whole value
// at that path, bounded by falling back to the full snapshot below.
func MatchDeltaOps(oldJSON, newJSON []byte) ([]byte, bool) {
	if len(oldJSON) == 0 || len(newJSON) == 0 {
		return nil, false
	}
	var ops []setOp
	diffJSONInto(nil, oldJSON, newJSON, &ops)
	if len(ops) == 0 {
		return nil, false
	}
	opsJSON, err := json.Marshal(ops)
	if err != nil {
		return nil, false
	}
	// Only worth a delta if it's clearly smaller than the full state. The 75%
	// gate keeps pathological diffs (e.g. a reordered array touching every
	// index) from being larger or barely smaller than just resending state.
	if len(opsJSON)*4 >= len(newJSON)*3 {
		return nil, false
	}
	return opsJSON, true
}

// diffJSONInto walks two JSON values (as raw bytes, byte-compared so unchanged
// subtrees are skipped without decoding) and appends set-ops for the leaves
// that differ. Operating on json.RawMessage avoids any value re-encoding drift:
// the emitted Value is exactly newRaw's bytes.
func diffJSONInto(path []any, oldRaw, newRaw json.RawMessage, ops *[]setOp) {
	if bytes.Equal(oldRaw, newRaw) {
		return
	}
	// Both objects: recurse per key.
	var oldObj, newObj map[string]json.RawMessage
	if json.Unmarshal(oldRaw, &oldObj) == nil && json.Unmarshal(newRaw, &newObj) == nil &&
		oldObj != nil && newObj != nil {
		for k, nv := range newObj {
			child := appendPath(path, k)
			if ov, ok := oldObj[k]; ok {
				diffJSONInto(child, ov, nv, ops)
			} else {
				*ops = append(*ops, setOp{Op: "set", Path: child, Value: nv})
			}
		}
		for k := range oldObj {
			if _, ok := newObj[k]; !ok {
				*ops = append(*ops, setOp{Op: "set", Path: appendPath(path, k), Value: json.RawMessage("null")})
			}
		}
		return
	}
	// Both arrays of equal length: recurse per index. A length change can't be
	// expressed with set-only ops, so it falls through to a whole-array set.
	var oldArr, newArr []json.RawMessage
	if json.Unmarshal(oldRaw, &oldArr) == nil && json.Unmarshal(newRaw, &newArr) == nil &&
		oldArr != nil && newArr != nil && len(oldArr) == len(newArr) {
		for i := range newArr {
			diffJSONInto(appendPath(path, i), oldArr[i], newArr[i], ops)
		}
		return
	}
	// Scalar change, type change, or array length change: replace the whole
	// value at this path. Path nil (root) means replace the entire state.
	*ops = append(*ops, setOp{Op: "set", Path: appendPath(path, nil), Value: newRaw})
}

// appendPath returns a fresh path slice with seg appended (or path unchanged
// when seg is nil, used for the root). A fresh slice per call avoids the
// classic shared-backing-array aliasing bug when the same parent path is
// extended for multiple children.
func appendPath(path []any, seg any) []any {
	out := make([]any, 0, len(path)+1)
	out = append(out, path...)
	if seg != nil {
		out = append(out, seg)
	}
	return out
}
