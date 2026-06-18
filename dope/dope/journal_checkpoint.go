package dopeserver

import (
	"context"
	"database/sql"

	"dope/dope/journal"
)

// Per-game checkpoint capture/restore lives in the leaf package dope/journal;
// these thin wrappers keep the revert/migration call sites terse.

// rowQuerier is the read surface the per-game history helpers accept; it matches
// (structurally) the journal leaf's own interface.
type rowQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type gameCheckpoint = journal.GameCheckpoint

func captureGameCheckpoint(ctx context.Context, q rowQuerier, gameID int64) (*gameCheckpoint, error) {
	return journal.CaptureGameCheckpoint(ctx, q, gameID)
}

func encodeGameCheckpoint(cp *gameCheckpoint) ([]byte, error) {
	return journal.EncodeGameCheckpoint(cp)
}

func decodeGameCheckpoint(blob []byte) (*gameCheckpoint, error) {
	return journal.DecodeGameCheckpoint(blob)
}

func restoreGameCheckpoint(ctx context.Context, tx *sql.Tx, gameID int64, cp *gameCheckpoint) error {
	return journal.RestoreGameCheckpoint(ctx, tx, gameID, cp)
}

func writeGameCheckpoint(ctx context.Context, tx *sql.Tx, gameID, seq int64) error {
	return journal.WriteGameCheckpoint(ctx, tx, gameID, seq)
}

func backfillGameCheckpoints(db *sql.DB) error {
	return journal.BackfillGameCheckpoints(db)
}
