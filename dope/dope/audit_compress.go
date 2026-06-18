package dopeserver

import (
	"database/sql/driver"
	"fmt"

	"github.com/klauspost/compress/zstd"
	sqlite "modernc.org/sqlite"
)

// The audit_log before_json/after_json columns hold the JSON snapshots captured
// by the AFTER triggers (PK + changed columns for UPDATEs, full row otherwise).
// On a fest with large state_json blobs these dominate the database — the same
// ~19 KB game document gets written on nearly every edit, twice (before + after).
// We store those snapshots zstd-compressed so the on-disk audit log shrinks
// ~5-10x. zstd is used over deflate for both a better ratio and faster
// encode/decode on this highly repetitive JSON.
//
// Compression happens inside the AFTER triggers (which can only call SQL), so it
// is exposed as two scalar SQL functions registered on every connection:
//
//	dope_z(text)   -> blob   compress a JSON snapshot for storage
//	dope_unz(blob) -> text   inverse, used by every read path
//
// Storage format: a 1-byte tag prefix.
//
//	0x00 + raw bytes     stored uncompressed (compression didn't pay off)
//	0x01 + zstd frame    compressed
//
// Legacy rows written before this change are plain JSON text starting with '{'
// (0x7b), which matches neither tag, so dope_unz returns them unchanged. That
// lets old and new rows coexist with no migration.
const (
	auditZRaw  = 0x00
	auditZZstd = 0x01
)

// EncodeAll/DecodeAll are safe for concurrent use, so a single shared
// encoder/decoder serves all connections without per-call allocation of the
// (expensive) coder state.
var (
	auditZEnc *zstd.Encoder
	auditZDec *zstd.Decoder
)

func init() {
	var err error
	auditZEnc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		panic(fmt.Sprintf("init zstd encoder: %v", err))
	}
	auditZDec, err = zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("init zstd decoder: %v", err))
	}
	if err := sqlite.RegisterScalarFunction("dope_z", 1, auditCompressFunc); err != nil {
		panic(fmt.Sprintf("register dope_z: %v", err))
	}
	if err := sqlite.RegisterScalarFunction("dope_unz", 1, auditDecompressFunc); err != nil {
		panic(fmt.Sprintf("register dope_unz: %v", err))
	}
}

// argBytes coerces a sqlite argument (string or []byte) to a byte slice. Returns
// (nil, false) for SQL NULL so callers can propagate NULL.
func argBytes(v driver.Value) ([]byte, bool) {
	switch s := v.(type) {
	case nil:
		return nil, false
	case []byte:
		return s, true
	case string:
		return []byte(s), true
	default:
		return []byte(fmt.Sprint(s)), true
	}
}

func auditCompressFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	raw, ok := argBytes(args[0])
	if !ok {
		return nil, nil
	}
	comp := auditZEnc.EncodeAll(raw, []byte{auditZZstd})
	// If compression didn't pay off (tiny or incompressible rows), store the raw
	// bytes with the raw tag so we never inflate storage by more than one byte.
	if len(comp) >= len(raw)+1 {
		return append([]byte{auditZRaw}, raw...), nil
	}
	return comp, nil
}

func auditDecompressFunc(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	raw, ok := argBytes(args[0])
	if !ok {
		return nil, nil
	}
	if len(raw) == 0 {
		return "", nil
	}
	switch raw[0] {
	case auditZRaw:
		return string(raw[1:]), nil
	case auditZZstd:
		out, err := auditZDec.DecodeAll(raw[1:], nil)
		if err != nil {
			return nil, err
		}
		return string(out), nil
	default:
		// Legacy uncompressed JSON text — return as-is.
		return string(raw), nil
	}
}
