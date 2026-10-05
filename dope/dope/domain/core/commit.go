package core

import (
	"context"
	"database/sql"

	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
)

// FestWrite is what one write to a fest did, as CommitFestWrite records it.
type FestWrite struct {
	// Event is what the fest revision records the write under, with Payload as
	// its JSON. Empty records nothing: either the write moved no revision, or
	// the domain call it made recorded its own and set Revision.
	Event   string
	Payload any
	// Revision is the fest revision the write reached, when the domain call
	// recorded one itself (gamebuild.Clear, gamebuild.Recompile).
	Revision int64
	// Settled runs once the transaction has committed, still under the write
	// lock and with the fest's cached view dropped, given the revision the
	// write reached: what the engine's own pointers learn of the write, such
	// as the active game moving off a deleted one, or a read that no other
	// write may come between.
	Settled func(e *Engine, revision int64)
}

// CommitFestWrite runs one write to a fest. It takes the pooled connection
// before the write lock, so a starved pool never holds the lock (the
// 2026-06-13 freeze), runs fn in one audited transaction bounded by
// festwrite.WriteTxTimeout, records the fest revision fn names, commits,
// drops the fest's cached view, and lets fn's Settled update the engine. It
// returns the revision the write reached, or 0 when it recorded none.
// Broadcasting is the caller's, since only the server can build a fest view.
func (e *Engine) CommitFestWrite(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) (FestWrite, error)) (int64, error) {
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := e.AcquireWriteConn(ctx, label)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	defer e.LockWrite(label)()
	tx, err := e.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	written, err := fn(ctx, tx)
	if err != nil {
		return 0, err
	}
	revision := written.Revision
	if written.Event != "" {
		payload := "{}"
		if written.Payload != nil {
			payload = util.MustJSON(written.Payload)
		}
		if revision, err = festwrite.BumpFestRevisionTx(ctx, tx, festID, written.Event, payload); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	e.InvalidateFestViewCache(festID)
	if written.Settled != nil {
		written.Settled(e, revision)
	}
	return revision, nil
}
