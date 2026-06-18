package dopeserver

import (
	"bytes"
	"reflect"
	"testing"
)

// The codec itself is tested in package dope/journal. This test exercises the
// package-main archive path: encode a segment through the journal codec facade,
// compress with the shared audit zstd coders, and round-trip back.
func TestSegmentZstdRoundTrip(t *testing.T) {
	recs := []journalRecord{
		{Seq: 1, Op: opRowIns, TSUnixMilli: 1700000000000, ActorID: 5, RequestID: 2,
			Args: encodeRowArgs(rowArgs{TableID: 1, Cols: []colVal{{NameID: 1, Val: int64(1)}}})},
		{Seq: 2, Op: opRowSet, TSUnixMilli: 1700000001000, ActorID: 0, RequestID: 0,
			Args: encodeRowArgs(rowArgs{TableID: 2, Cols: []colVal{{NameID: 1, Val: int64(2)}, {NameID: 3, Val: "x"}}})},
		{Seq: 3, Op: opRowDel, TSUnixMilli: 1700000002000, ActorID: 9, RequestID: 2,
			Args: encodeRowArgs(rowArgs{TableID: 2, Cols: []colVal{{NameID: 1, Val: int64(2)}}})},
	}
	raw := encodeSegment(recs)
	comp := zstdCompress(raw)
	deco, err := zstdDecompress(comp)
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if !bytes.Equal(raw, deco) {
		t.Fatalf("zstd round-trip mismatch")
	}
	out, err := decodeSegment(deco)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if !reflect.DeepEqual(recs, out) {
		t.Fatalf("segment round-trip mismatch:\n in=%+v\nout=%+v", recs, out)
	}
}
