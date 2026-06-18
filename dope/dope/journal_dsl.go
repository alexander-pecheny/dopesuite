package dopeserver

import "dope/dope/journal"

// Adapters onto the journal codec leaf (package dope/journal). The on-disk
// format — opcodes, row-arg and segment encode/decode — lives there and is
// tested in isolation; these aliases and thin wrappers keep the existing
// package-main call sites (triggers, replay, archive, convert) terse.
//
// zstdCompress/zstdDecompress stay here: they wrap the shared audit zstd coders
// (auditZEnc/auditZDec), which are package-main globals.

type (
	journalOp     = journal.Op
	journalRecord = journal.Record
	rowArgs       = journal.RowArgs
	colVal        = journal.ColVal
)

const journalDSLVersion = journal.DSLVersion

const (
	opInvalid     = journal.OpInvalid
	opRowIns      = journal.OpRowIns
	opRowSet      = journal.OpRowSet
	opRowDel      = journal.OpRowDel
	opMark        = journal.OpMark
	opPlace       = journal.OpPlace
	opThemePlayer = journal.OpThemePlayer
	opFinish      = journal.OpFinish
	opUnfinish    = journal.OpUnfinish
	opGamePatch   = journal.OpGamePatch
)

func encodeRowArgs(a rowArgs) []byte                  { return journal.EncodeRowArgs(a) }
func decodeRowArgs(b []byte) (rowArgs, error)         { return journal.DecodeRowArgs(b) }
func encodeSegment(recs []journalRecord) []byte       { return journal.EncodeSegment(recs) }
func decodeSegment(b []byte) ([]journalRecord, error) { return journal.DecodeSegment(b) }

func encodeGenericPayload(eventType string, payload []byte) []byte {
	return journal.EncodeGenericPayload(eventType, payload)
}

func decodeGenericPayload(b []byte) (eventType string, payload []byte, err error) {
	return journal.DecodeGenericPayload(b)
}

// zstdCompress / zstdDecompress delegate to the journal leaf's segment coder.
func zstdCompress(raw []byte) []byte { return journal.Compress(raw) }

func zstdDecompress(comp []byte) ([]byte, error) { return journal.Decompress(comp) }
