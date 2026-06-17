package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The journal is a forward, append-only edit log that replaces the old
// before/after-snapshot audit_log AND the write-only SSE events table. Each
// entry records ONE edit as a compact, self-describing DSL record: an opcode
// plus a varint-encoded argument blob. Tiny records are stored raw while a
// tournament is live; finished runs are concatenated and zstd-compressed into
// cold segments (see journal_archive.go). Replay of the records from a genesis
// checkpoint reconstructs state at any point, which is also how revert is
// derived. The log never expires.
//
// This file is the codec: the opcode registry plus the binary encode/decode of
// argument blobs and whole segment streams. Opcodes are append-only and never
// reused — a record written years ago must still decode, so the meaning of an
// opcode id is fixed forever.

// journalDSLVersion tags every cold segment. Bump only for an
// incompatible change to the stream framing (never for adding opcodes, which is
// backward-compatible by construction).
const journalDSLVersion = 1

type journalOp uint16

const (
	opInvalid journalOp = 0

	// Generic row ops — the escape hatch. They reference a table+columns
	// directly (via the interning dictionary) rather than a semantic action.
	// Used by the audit_log converter (the old log is row-level) and as a
	// coarse fallback for write paths without a dedicated semantic opcode.
	opRowIns journalOp = 1 // full row inserted
	opRowSet journalOp = 2 // primary key + changed columns (forward UPDATE delta)
	opRowDel journalOp = 3 // primary key only (row deleted)

	// Semantic ops (live-play edits) are added in a later phase starting at 16;
	// the gap leaves room for more generic ops without renumbering.
	opMark        journalOp = 16 // set an answer mark
	opPlace       journalOp = 17 // set a team's place in a match result
	opThemePlayer journalOp = 18 // assign a player to a theme
	opFinish      journalOp = 19 // finish a match
	opUnfinish    journalOp = 20 // unfinish a match
	opGamePatch   journalOp = 21 // JSON-pointer set/remove ops on a game's state_json
)

var journalOpNames = map[journalOp]string{
	opRowIns:      "ROWINS",
	opRowSet:      "ROWSET",
	opRowDel:      "ROWDEL",
	opMark:        "MARK",
	opPlace:       "PLACE",
	opThemePlayer: "THEME_PLAYER",
	opFinish:      "FINISH",
	opUnfinish:    "UNFINISH",
	opGamePatch:   "GPATCH",
}

func (op journalOp) String() string {
	if n, ok := journalOpNames[op]; ok {
		return n
	}
	return fmt.Sprintf("op(%d)", uint16(op))
}

// Value type tags for the encoded SQLite scalar values carried in row ops.
const (
	vNull byte = 0
	vInt  byte = 1
	vReal byte = 2
	vText byte = 3
	vBlob byte = 4
)

// colVal is one column name (already interned to a dictionary id) and its value.
// The value is a SQLite scalar: nil, int64, float64, string, or []byte.
type colVal struct {
	nameID uint64
	val    any
}

// rowArgs is the decoded payload of a generic row op (opRowIns/opRowSet/opRowDel).
type rowArgs struct {
	tableID uint64
	cols    []colVal
}

// --- low-level writer/reader over a byte buffer ----------------------------

type byteWriter struct{ buf []byte }

func (w *byteWriter) uvarint(v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	w.buf = append(w.buf, tmp[:n]...)
}

func (w *byteWriter) varint(v int64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutVarint(tmp[:], v)
	w.buf = append(w.buf, tmp[:n]...)
}

func (w *byteWriter) byte(b byte) { w.buf = append(w.buf, b) }

func (w *byteWriter) bytes(b []byte) {
	w.uvarint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}

// value encodes a SQLite scalar with a leading type tag.
func (w *byteWriter) value(v any) {
	switch n := v.(type) {
	case nil:
		w.byte(vNull)
	case int64:
		w.byte(vInt)
		w.varint(n)
	case int:
		w.byte(vInt)
		w.varint(int64(n))
	case float64:
		w.byte(vReal)
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], math.Float64bits(n))
		w.buf = append(w.buf, tmp[:]...)
	case string:
		w.byte(vText)
		w.bytes([]byte(n))
	case []byte:
		w.byte(vBlob)
		w.bytes(n)
	default:
		// Fall back to a text rendering rather than losing the value.
		w.byte(vText)
		w.bytes([]byte(fmt.Sprint(n)))
	}
}

type byteReader struct {
	buf []byte
	pos int
}

func (r *byteReader) eof() bool { return r.pos >= len(r.buf) }

func (r *byteReader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.buf[r.pos:])
	if n <= 0 {
		return 0, errors.New("journal: bad uvarint")
	}
	r.pos += n
	return v, nil
}

func (r *byteReader) varint() (int64, error) {
	v, n := binary.Varint(r.buf[r.pos:])
	if n <= 0 {
		return 0, errors.New("journal: bad varint")
	}
	r.pos += n
	return v, nil
}

func (r *byteReader) readByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, errors.New("journal: unexpected eof")
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *byteReader) readBytes() ([]byte, error) {
	n, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	if r.pos+int(n) > len(r.buf) {
		return nil, errors.New("journal: bytes length overruns buffer")
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *byteReader) value() (any, error) {
	tag, err := r.readByte()
	if err != nil {
		return nil, err
	}
	switch tag {
	case vNull:
		return nil, nil
	case vInt:
		return r.varint()
	case vReal:
		if r.pos+8 > len(r.buf) {
			return nil, errors.New("journal: real overruns buffer")
		}
		bits := binary.LittleEndian.Uint64(r.buf[r.pos : r.pos+8])
		r.pos += 8
		return math.Float64frombits(bits), nil
	case vText:
		b, err := r.readBytes()
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case vBlob:
		b, err := r.readBytes()
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(b))
		copy(out, b)
		return out, nil
	default:
		return nil, fmt.Errorf("journal: unknown value tag %d", tag)
	}
}

// --- row-op args codec ------------------------------------------------------

func encodeRowArgs(a rowArgs) []byte {
	var w byteWriter
	w.uvarint(a.tableID)
	w.uvarint(uint64(len(a.cols)))
	for _, c := range a.cols {
		w.uvarint(c.nameID)
		w.value(c.val)
	}
	return w.buf
}

func decodeRowArgs(b []byte) (rowArgs, error) {
	r := byteReader{buf: b}
	tableID, err := r.uvarint()
	if err != nil {
		return rowArgs{}, err
	}
	n, err := r.uvarint()
	if err != nil {
		return rowArgs{}, err
	}
	cols := make([]colVal, 0, n)
	for i := uint64(0); i < n; i++ {
		nameID, err := r.uvarint()
		if err != nil {
			return rowArgs{}, err
		}
		val, err := r.value()
		if err != nil {
			return rowArgs{}, err
		}
		cols = append(cols, colVal{nameID: nameID, val: val})
	}
	return rowArgs{tableID: tableID, cols: cols}, nil
}

// --- segment stream codec ---------------------------------------------------

// journalRecord is one decoded journal entry within a segment stream.
type journalRecord struct {
	Seq         uint64
	Op          journalOp
	TSUnixMilli int64
	ActorID     int64  // 0 = none
	RequestID   uint64 // dictionary id, 0 = none
	Args        []byte
}

// encodeSegment serializes records into the pre-compression segment stream.
// Records must be ordered by Seq. The caller zstd-compresses the result.
func encodeSegment(records []journalRecord) []byte {
	var w byteWriter
	w.uvarint(uint64(len(records)))
	for _, rec := range records {
		w.uvarint(rec.Seq)
		w.uvarint(uint64(rec.Op))
		w.varint(rec.TSUnixMilli)
		w.varint(rec.ActorID)
		w.uvarint(rec.RequestID)
		w.bytes(rec.Args)
	}
	return w.buf
}

// decodeSegment parses a (decompressed) segment stream back into records.
func decodeSegment(b []byte) ([]journalRecord, error) {
	r := byteReader{buf: b}
	n, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]journalRecord, 0, n)
	for i := uint64(0); i < n; i++ {
		var rec journalRecord
		seq, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		rec.Seq = seq
		op, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		rec.Op = journalOp(op)
		if rec.TSUnixMilli, err = r.varint(); err != nil {
			return nil, err
		}
		if rec.ActorID, err = r.varint(); err != nil {
			return nil, err
		}
		if rec.RequestID, err = r.uvarint(); err != nil {
			return nil, err
		}
		args, err := r.readBytes()
		if err != nil {
			return nil, err
		}
		rec.Args = append([]byte(nil), args...)
		out = append(out, rec)
	}
	return out, nil
}

// zstdCompress / zstdDecompress reuse the shared audit zstd coders.
func zstdCompress(raw []byte) []byte {
	return auditZEnc.EncodeAll(raw, nil)
}

func zstdDecompress(comp []byte) ([]byte, error) {
	return auditZDec.DecodeAll(comp, nil)
}
