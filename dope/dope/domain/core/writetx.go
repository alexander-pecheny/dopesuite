package core

import (
	"context"
	"database/sql"

	"pecheny.me/dopecore/sqlitex"

	"dope/dope/storage/festwrite"
)

// writer is the shared write discipline (dopecore's sqlitex.Writer: the pool
// wait off the lock, the slow-write log, commit or rollback) over the engine's
// pool and its global write lock. What dope adds on top is the audit: every
// write transaction is seeded with the audit context as it begins, and
// WithWriteTx's context carries the actor, request and fest it is attributed to.
// A 2026-06-13 write once waited about 55 minutes for a pooled connection while
// holding the lock; taking the connection first is what stops that.
func (e *Engine) writer() sqlitex.Writer {
	return sqlitex.Writer{DB: e.DB, Mu: &e.Mu, Timeout: festwrite.WriteTxTimeout, Begin: festwrite.SeedAuditCtx}
}

// AcquireWriteConn pulls a dedicated pooled connection for a write. Callers
// invoke it BEFORE taking e.Mu, so the pool wait happens off the lock and is
// bounded by ctx (festwrite.WriteTxTimeout).
func (e *Engine) AcquireWriteConn(ctx context.Context, label string) (*sql.Conn, error) {
	return e.writer().Conn(ctx, label)
}

// LockWrite acquires the global write mutex and returns the func that releases
// it, logging a wait or a hold past sqlitex.SlowWrite. Use it as
// `defer e.LockWrite("label")()` on write paths.
func (e *Engine) LockWrite(label string) func() {
	return e.writer().Lock(label)
}

// BeginWriteTx begins a write transaction on the shared pool and seeds audit ctx.
func (e *Engine) BeginWriteTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := festwrite.SeedAuditCtx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// BeginWriteTxConn begins a write transaction on a held connection and seeds
// the audit context.
func (e *Engine) BeginWriteTxConn(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	return e.writer().BeginOn(ctx, conn)
}

// WithWriteTx runs fn in a bounded, audited write transaction: it pulls a pooled
// connection BEFORE taking the global write lock (so pool waits stay off-lock),
// then commits (or rolls back on error).
func (e *Engine) WithWriteTx(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	return e.writer().Run(ctx, label, fn)
}

// WriteExec runs a single audited write statement in an implicit transaction.
func (e *Engine) WriteExec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx, err := e.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}
