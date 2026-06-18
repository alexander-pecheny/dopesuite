package tests

import (
	"bytes"
	"dope/dope/journal"
	"reflect"
	"testing"
)

// The codec itself is tested in package dope/journal. This test exercises the
// package-main archive path: encode a segment through the journal codec facade,
// compress with the shared audit zstd coders, and round-trip back.
func TestSegmentZstdRoundTrip(t *testing.T) {
	recs := []journal.Record{
		{Seq: 1, Op: journal.OpRowIns, TSUnixMilli: 1700000000000, ActorID: 5, RequestID: 2,
			Args: journal.EncodeRowArgs(journal.RowArgs{TableID: 1, Cols: []journal.ColVal{{NameID: 1, Val: int64(1)}}})},
		{Seq: 2, Op: journal.OpRowSet, TSUnixMilli: 1700000001000, ActorID: 0, RequestID: 0,
			Args: journal.EncodeRowArgs(journal.RowArgs{TableID: 2, Cols: []journal.ColVal{{NameID: 1, Val: int64(2)}, {NameID: 3, Val: "x"}}})},
		{Seq: 3, Op: journal.OpRowDel, TSUnixMilli: 1700000002000, ActorID: 9, RequestID: 2,
			Args: journal.EncodeRowArgs(journal.RowArgs{TableID: 2, Cols: []journal.ColVal{{NameID: 1, Val: int64(2)}}})},
	}
	raw := journal.EncodeSegment(recs)
	comp := journal.Compress(raw)
	deco, err := journal.Decompress(comp)
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	if !bytes.Equal(raw, deco) {
		t.Fatalf("zstd round-trip mismatch")
	}
	out, err := journal.DecodeSegment(deco)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if !reflect.DeepEqual(recs, out) {
		t.Fatalf("segment round-trip mismatch:\n in=%+v\nout=%+v", recs, out)
	}
}
