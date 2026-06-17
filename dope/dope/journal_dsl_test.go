package main

import (
	"bytes"
	"reflect"
	"testing"
)

func TestRowArgsRoundTrip(t *testing.T) {
	in := rowArgs{
		tableID: 7,
		cols: []colVal{
			{nameID: 1, val: int64(42)},
			{nameID: 2, val: "hello"},
			{nameID: 3, val: nil},
			{nameID: 4, val: 3.14},
			{nameID: 5, val: []byte{0x00, 0x01, 0xff}},
			{nameID: 6, val: int64(-9999)},
		},
	}
	out, err := decodeRowArgs(encodeRowArgs(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestValueRoundTripTypes(t *testing.T) {
	cases := []any{nil, int64(0), int64(-1), int64(1 << 40), 0.0, -2.5, "", "юникод", []byte{}, []byte("x")}
	for _, c := range cases {
		var w byteWriter
		w.value(c)
		r := byteReader{buf: w.buf}
		got, err := r.value()
		if err != nil {
			t.Fatalf("value %v: %v", c, err)
		}
		if !valuesEqual(c, got) {
			t.Fatalf("value mismatch: in=%#v out=%#v", c, got)
		}
		if !r.eof() {
			t.Fatalf("trailing bytes after value %#v", c)
		}
	}
}

func valuesEqual(a, b any) bool {
	ab, aok := a.([]byte)
	bb, bok := b.([]byte)
	if aok || bok {
		return aok && bok && bytes.Equal(ab, bb)
	}
	return a == b
}

func TestSegmentRoundTrip(t *testing.T) {
	recs := []journalRecord{
		{Seq: 1, Op: opRowIns, TSUnixMilli: 1700000000000, ActorID: 5, RequestID: 2,
			Args: encodeRowArgs(rowArgs{tableID: 1, cols: []colVal{{nameID: 1, val: int64(1)}}})},
		{Seq: 2, Op: opRowSet, TSUnixMilli: 1700000001000, ActorID: 0, RequestID: 0,
			Args: encodeRowArgs(rowArgs{tableID: 2, cols: []colVal{{nameID: 1, val: int64(2)}, {nameID: 3, val: "x"}}})},
		{Seq: 3, Op: opRowDel, TSUnixMilli: 1700000002000, ActorID: 9, RequestID: 2,
			Args: encodeRowArgs(rowArgs{tableID: 2, cols: []colVal{{nameID: 1, val: int64(2)}}})},
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
