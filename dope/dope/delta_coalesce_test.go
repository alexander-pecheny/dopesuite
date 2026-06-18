package dopeserver

import (
	"encoding/json"
	"testing"

	"dope/dope/realtime"
)

// drainOne returns the next buffered event on ch, or fails if none is queued.
func drainOne(t *testing.T, ch chan realtime.Event) realtime.Envelope {
	t.Helper()
	select {
	case ev := <-ch:
		var env realtime.Envelope
		if err := json.Unmarshal(ev.Data, &env); err != nil {
			t.Fatalf("unmarshal event: %v (data=%s)", err, ev.Data)
		}
		return env
	default:
		t.Fatal("expected a buffered event")
		return realtime.Envelope{}
	}
}

func TestMergeOpsArrays(t *testing.T) {
	a := []byte(`[{"op":"set","path":["a"],"value":1}]`)
	b := []byte(`[{"op":"set","path":["b"],"value":2},{"op":"set","path":["a"],"value":3}]`)
	got := realtime.MergeOpsArrays([][]byte{a, b})
	var ops []map[string]any
	if err := json.Unmarshal(got, &ops); err != nil {
		t.Fatalf("merged not valid JSON: %v (%s)", err, got)
	}
	if len(ops) != 3 {
		t.Fatalf("merged op count = %d, want 3 (order preserved, no dedup)", len(ops))
	}
	// Single array passes through unchanged.
	if got := string(realtime.MergeOpsArrays([][]byte{a})); got != string(a) {
		t.Fatalf("single array merge = %s, want %s", got, a)
	}
}

// Editors get every delta immediately with per-edit seqs; viewers get one merged
// delta per window. Seqs are per-edit (not collapsed), and the merged viewer
// delta spans [prevSeq, lastSeq].
func TestBroadcastStateDeltaCoalescesForViewersImmediateForEditors(t *testing.T) {
	srv := &server{rt: realtime.NewManager()}
	editor := make(chan realtime.Event, 8)
	viewer := make(chan realtime.Event, 8)
	srv.rt.AddSubscriber(1, editor, true, 0)
	srv.rt.AddSubscriber(1, viewer, false, 0)

	scope := "game-state:5"
	seq1 := srv.broadcastStateDelta(1, scope, 10, []byte(`[{"op":"set","path":["x"],"value":1}]`))
	seq2 := srv.broadcastStateDelta(1, scope, 11, []byte(`[{"op":"set","path":["y"],"value":2}]`))
	// Per-edit seqs, assigned immediately.
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("per-edit seqs = %d,%d, want 1,2", seq1, seq2)
	}

	// Editor saw both deltas immediately, each chaining per edit.
	e1 := drainOne(t, editor)
	if e1.Seq != 1 || e1.PrevSeq != 0 {
		t.Fatalf("editor delta 1 seq/prev = %d/%d, want 1/0", e1.Seq, e1.PrevSeq)
	}
	e2 := drainOne(t, editor)
	if e2.Seq != 2 || e2.PrevSeq != 1 {
		t.Fatalf("editor delta 2 seq/prev = %d/%d, want 2/1", e2.Seq, e2.PrevSeq)
	}

	// Viewer saw nothing yet — buffered until flush.
	select {
	case ev := <-viewer:
		t.Fatalf("expected no viewer broadcast before flush, got %s", ev.Data)
	default:
	}

	srv.flushDelta(scope)
	v := drainOne(t, viewer)
	if v.Seq != 2 || v.PrevSeq != 0 {
		t.Fatalf("merged viewer delta seq/prev = %d/%d, want 2/0", v.Seq, v.PrevSeq)
	}
	var ops []map[string]any
	if err := json.Unmarshal(v.Ops, &ops); err != nil || len(ops) != 2 {
		t.Fatalf("merged ops = %s (err %v), want 2 ops", v.Ops, err)
	}
	// Editor must NOT also receive the merged viewer delta.
	select {
	case ev := <-editor:
		t.Fatalf("editor unexpectedly got the coalesced viewer delta: %s", ev.Data)
	default:
	}
}

// A snapshot must flush any buffered deltas first (lower seq) and then take the
// next seq, so viewers never receive the snapshot ahead of the deltas it
// supersedes.
func TestBroadcastStateFlushesBufferedDeltasFirst(t *testing.T) {
	srv := &server{rt: realtime.NewManager()}
	ch := make(chan realtime.Event, 8) // a viewer, so it sees the coalesced delta + snapshot
	srv.rt.AddSubscriber(1, ch, false, 0)
	scope := "game-state:5"

	srv.broadcastStateDelta(1, scope, 10, []byte(`[{"op":"set","path":["x"],"value":1}]`))
	snapSeq := srv.broadcastState(1, scope, 11, []byte(`{"full":true}`))

	// The snapshot takes the seq after the flushed delta's.
	if snapSeq != 2 {
		t.Fatalf("snapshot seq = %d, want 2 (after the flushed delta at 1)", snapSeq)
	}
	// First out is the flushed viewer delta (envelope, seq 1, carries ops)...
	delta := drainOne(t, ch)
	if delta.Seq != 1 || len(delta.Ops) == 0 {
		t.Fatalf("first event = seq %d ops %s, want the buffered delta at seq 1", delta.Seq, delta.Ops)
	}
	// ...then the snapshot. (A bare test server has db==nil, so broadcastState
	// emits the raw payload rather than an envelope — assert on the raw bytes.)
	select {
	case ev := <-ch:
		if string(ev.Data) != `{"full":true}` {
			t.Fatalf("second event = %s, want the raw snapshot payload", ev.Data)
		}
	default:
		t.Fatal("expected the snapshot event after the flushed delta")
	}
}
