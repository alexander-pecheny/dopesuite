package main

import (
	"encoding/json"
	"testing"
)

// drainOne returns the next buffered event on ch, or fails if none is queued.
func drainOne(t *testing.T, ch chan event) eventEnvelope {
	t.Helper()
	select {
	case ev := <-ch:
		var env eventEnvelope
		if err := json.Unmarshal(ev.data, &env); err != nil {
			t.Fatalf("unmarshal event: %v (data=%s)", err, ev.data)
		}
		return env
	default:
		t.Fatal("expected a buffered event")
		return eventEnvelope{}
	}
}

func TestMergeOpsArrays(t *testing.T) {
	a := []byte(`[{"op":"set","path":["a"],"value":1}]`)
	b := []byte(`[{"op":"set","path":["b"],"value":2},{"op":"set","path":["a"],"value":3}]`)
	got := mergeOpsArrays([][]byte{a, b})
	var ops []map[string]any
	if err := json.Unmarshal(got, &ops); err != nil {
		t.Fatalf("merged not valid JSON: %v (%s)", err, got)
	}
	if len(ops) != 3 {
		t.Fatalf("merged op count = %d, want 3 (order preserved, no dedup)", len(ops))
	}
	// Single array passes through unchanged.
	if got := string(mergeOpsArrays([][]byte{a})); got != string(a) {
		t.Fatalf("single array merge = %s, want %s", got, a)
	}
}

// A window of delta broadcasts for one scope must buffer (no event until flush),
// collapse to a single seq increment, carry the merged ops, and chain prevSeq
// across windows.
func TestBroadcastStateDeltaCoalesces(t *testing.T) {
	srv := &server{}
	ch := make(chan event, 8)
	srv.addSubscriber(1, ch)

	scope := "game-state:5"
	seq1 := srv.broadcastStateDelta(1, scope, 10, []byte(`[{"op":"set","path":["x"],"value":1}]`))
	seq2 := srv.broadcastStateDelta(1, scope, 11, []byte(`[{"op":"set","path":["y"],"value":2}]`))
	// Both edits in the window resolve to the same (predicted) flush seq.
	if seq1 != 1 || seq2 != 1 {
		t.Fatalf("window seqs = %d,%d, want 1,1", seq1, seq2)
	}
	// Nothing fanned out yet — still buffered.
	select {
	case ev := <-ch:
		t.Fatalf("expected no broadcast before flush, got %s", ev.data)
	default:
	}

	srv.flushDelta(scope)
	env := drainOne(t, ch)
	if env.Seq != 1 || env.PrevSeq != 0 {
		t.Fatalf("flushed seq/prevSeq = %d/%d, want 1/0", env.Seq, env.PrevSeq)
	}
	var ops []map[string]any
	if err := json.Unmarshal(env.Ops, &ops); err != nil || len(ops) != 2 {
		t.Fatalf("merged ops = %s (err %v), want 2 ops", env.Ops, err)
	}

	// A second window chains: prevSeq is the prior flush's seq.
	srv.broadcastStateDelta(1, scope, 12, []byte(`[{"op":"set","path":["z"],"value":3}]`))
	srv.flushDelta(scope)
	env2 := drainOne(t, ch)
	if env2.Seq != 2 || env2.PrevSeq != 1 {
		t.Fatalf("second window seq/prevSeq = %d/%d, want 2/1", env2.Seq, env2.PrevSeq)
	}
}

// A snapshot must flush any buffered deltas first (lower seq) and then take the
// next seq, so viewers never receive the snapshot ahead of the deltas it
// supersedes.
func TestBroadcastStateFlushesBufferedDeltasFirst(t *testing.T) {
	srv := &server{}
	ch := make(chan event, 8)
	srv.addSubscriber(1, ch)
	scope := "game-state:5"

	srv.broadcastStateDelta(1, scope, 10, []byte(`[{"op":"set","path":["x"],"value":1}]`))
	snapSeq := srv.broadcastState(1, scope, 11, []byte(`{"full":true}`))

	// The snapshot takes the seq after the flushed delta's.
	if snapSeq != 2 {
		t.Fatalf("snapshot seq = %d, want 2 (after the flushed delta at 1)", snapSeq)
	}
	// First out is the flushed delta (envelope, seq 1, carries ops)...
	delta := drainOne(t, ch)
	if delta.Seq != 1 || len(delta.Ops) == 0 {
		t.Fatalf("first event = seq %d ops %s, want the buffered delta at seq 1", delta.Seq, delta.Ops)
	}
	// ...then the snapshot. (A bare test server has db==nil, so broadcastState
	// emits the raw payload rather than an envelope — assert on the raw bytes.)
	select {
	case ev := <-ch:
		if string(ev.data) != `{"full":true}` {
			t.Fatalf("second event = %s, want the raw snapshot payload", ev.data)
		}
	default:
		t.Fatal("expected the snapshot event after the flushed delta")
	}
}
