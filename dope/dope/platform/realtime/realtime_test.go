package realtime

import (
	"encoding/json"
	"testing"
)

func TestEventSnapshotJSONShape(t *testing.T) {
	out := EventSnapshotJSON("match:1", "ep0", 7, 3, []byte(`{"a":1}`))
	var env Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Scope != "match:1" || env.Revision != 7 || env.Seq != 3 || env.Epoch != "ep0" {
		t.Fatalf("snapshot fields wrong: %+v", env)
	}
	if string(env.Data) != `{"a":1}` {
		t.Fatalf("snapshot data = %s", env.Data)
	}
	// Snapshots carry no ops and no prevSeq.
	if len(env.Ops) != 0 || env.PrevSeq != 0 {
		t.Fatalf("snapshot should have no ops/prevSeq: %+v", env)
	}
}

func TestEventDeltaJSONShape(t *testing.T) {
	out := EventDeltaJSON("match:1", "ep0", 7, 5, 4, []byte(`[{"op":"set"}]`))
	var env Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Seq != 5 || env.PrevSeq != 4 || string(env.Ops) != `[{"op":"set"}]` {
		t.Fatalf("delta fields wrong: %+v", env)
	}
	if env.EmitMs == 0 {
		t.Fatalf("delta should stamp EmitMs")
	}
	if len(env.Data) != 0 {
		t.Fatalf("delta should have no data: %+v", env)
	}
}

func TestMergeOpsArrays(t *testing.T) {
	// Single array is returned verbatim.
	one := []byte(`[{"op":"set","path":["a"]}]`)
	if got := MergeOpsArrays([][]byte{one}); string(got) != string(one) {
		t.Fatalf("single passthrough = %s", got)
	}
	// Multiple arrays concatenate in order.
	got := MergeOpsArrays([][]byte{[]byte(`[1,2]`), []byte(`[3]`), []byte(`[4,5]`)})
	if string(got) != `[1,2,3,4,5]` {
		t.Fatalf("merge = %s", got)
	}
}
