// Package realtime holds the SSE realtime layer's wire format and delta
// computation. It is a leaf — it depends only on the standard library, never on
// the server, database or HTTP layers — so the on-the-wire event shape and the
// delta diff have a single, independently-testable home.
//
// This is the first slice of the realtime extraction (ARCHITECTURE.md roadmap
// item 4): the pure encode/diff. The stateful publisher (subscriber registry,
// broadcast fan-out, per-scope sequencing and delta coalescing) follows behind
// a small interface, and will call into these helpers.
package realtime

import (
	"encoding/json"
	"time"
)

// Envelope is the unified SSE payload for every scope. Exactly one of Data
// (snapshot) or Ops (delta) is set.
type Envelope struct {
	Scope    string `json:"scope"`
	Revision int64  `json:"revision"`
	Seq      uint64 `json:"seq,omitempty"`
	PrevSeq  uint64 `json:"prevSeq,omitempty"`
	// Epoch is the server's per-process token. It changes on restart (when the
	// per-scope seq counter resets to 0), so a client that sees a new epoch
	// knows its lastSeq belongs to a dead seq space and must resync rather than
	// silently dropping the lower-numbered deltas.
	Epoch string          `json:"epoch,omitempty"`
	Ops   json.RawMessage `json:"ops,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	// EmitMs is the server's unix-millis emit time, stamped on deltas so a client
	// can log the server→render delivery leg (Date.now()-EmitMs). Cross-machine,
	// so it carries client/server clock skew — a rough delivery gauge, not exact.
	EmitMs int64 `json:"emitMs,omitempty"`
}

// EventSnapshotJSON wraps a full-state payload as a snapshot envelope.
func EventSnapshotJSON(scope, epoch string, revision int64, seq uint64, payload []byte) []byte {
	data, err := json.Marshal(Envelope{
		Scope:    scope,
		Revision: revision,
		Seq:      seq,
		Epoch:    epoch,
		Data:     json.RawMessage(payload),
	})
	if err != nil {
		return payload
	}
	return data
}

// EventDeltaJSON wraps an ops array as a delta envelope carrying (seq, prevSeq).
func EventDeltaJSON(scope, epoch string, revision int64, seq, prevSeq uint64, ops []byte) []byte {
	data, err := json.Marshal(Envelope{
		Scope:    scope,
		Revision: revision,
		Seq:      seq,
		PrevSeq:  prevSeq,
		Epoch:    epoch,
		Ops:      json.RawMessage(ops),
		EmitMs:   time.Now().UnixMilli(),
	})
	if err != nil {
		return ops
	}
	return data
}

// MergeOpsArrays concatenates several JSON op-arrays into one, preserving order
// (later ops override earlier ones for the same path on the client).
func MergeOpsArrays(arrays [][]byte) []byte {
	if len(arrays) == 1 {
		return arrays[0]
	}
	merged := make([]json.RawMessage, 0, len(arrays))
	for _, a := range arrays {
		var ops []json.RawMessage
		if err := json.Unmarshal(a, &ops); err != nil {
			continue
		}
		merged = append(merged, ops...)
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return arrays[len(arrays)-1]
	}
	return out
}
