package journal

import (
	"bytes"
	"reflect"
	"testing"
)

func TestRowArgsRoundTrip(t *testing.T) {
	in := RowArgs{
		TableID: 7,
		Cols: []ColVal{
			{NameID: 1, Val: int64(42)},
			{NameID: 2, Val: "hello"},
			{NameID: 3, Val: nil},
			{NameID: 4, Val: 3.14},
			{NameID: 5, Val: []byte{0x00, 0x01, 0xff}},
			{NameID: 6, Val: int64(-9999)},
		},
	}
	out, err := DecodeRowArgs(EncodeRowArgs(in))
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
	recs := []Record{
		{Seq: 1, Op: OpRowIns, TSUnixMilli: 1700000000000, ActorID: 5, RequestID: 2,
			Args: EncodeRowArgs(RowArgs{TableID: 1, Cols: []ColVal{{NameID: 1, Val: int64(1)}}})},
		{Seq: 2, Op: OpRowSet, TSUnixMilli: 1700000001000, ActorID: 0, RequestID: 0,
			Args: EncodeRowArgs(RowArgs{TableID: 2, Cols: []ColVal{{NameID: 1, Val: int64(2)}, {NameID: 3, Val: "x"}}})},
		{Seq: 3, Op: OpRowDel, TSUnixMilli: 1700000002000, ActorID: 9, RequestID: 2,
			Args: EncodeRowArgs(RowArgs{TableID: 2, Cols: []ColVal{{NameID: 1, Val: int64(2)}}})},
	}
	out, err := DecodeSegment(EncodeSegment(recs))
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if !reflect.DeepEqual(recs, out) {
		t.Fatalf("segment round-trip mismatch:\n in=%+v\nout=%+v", recs, out)
	}
}
