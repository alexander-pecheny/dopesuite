package dopeserver

import (
	"context"
	"database/sql"

	"dope/dope/journal"
)

// The journal replay engine lives in the leaf package dope/journal; these thin
// wrappers keep the revert/convert call sites terse.

func newJournalReplayer(dict map[uint64]string) *journal.Replayer {
	return journal.NewReplayer(dict)
}

func loadJournalDict(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[uint64]string, error) {
	return journal.LoadDict(ctx, q)
}

func decodeRowOpJSON(payload []byte) (string, map[string]any, error) {
	return journal.DecodeRowOpJSON(payload)
}
