package core

import (
	"context"
	"database/sql"
	"dope/dope/storage/festwrite"
	"log"
	"time"
)

// slowWriteThreshold is the line above which a write's connection-pool wait, its
// wait to acquire the global write mutex, or the time it holds that mutex earns a
// WARN log line. It is ALWAYS on — the cost is a few time.Now() reads per write,
// negligible against any DB work — so the kind of silent stall that caused the
// 2026-06-13 hour-long freeze shows up in journalctl as it builds, instead of
// leaving the server mute (its only log that hour was the audit prune). A healthy
// write here is sub-millisecond, so 1s already means something is wrong.
const slowWriteThreshold = time.Second

// acquireWriteConn pulls a dedicated pooled connection for a write. Callers
// invoke it BEFORE taking e.Mu, so the pool wait — the exact step that stalled
// ~55 min on 2026-06-13 — happens off-lock and bounded by ctx (festwrite.WriteTxTimeout). A
// wait past slowWriteThreshold is logged as the pool-starvation canary.
func (e *Engine) AcquireWriteConn(ctx context.Context, label string) (*sql.Conn, error) {
	start := time.Now()
	conn, err := e.DB.Conn(ctx)
	if waited := time.Since(start); waited >= slowWriteThreshold {
		log.Printf("slow write %s: pool-wait=%s err=%v (threshold=%s)",
			label, waited.Round(time.Millisecond), err, slowWriteThreshold)
	}
	return conn, err
}

// lockWrite acquires the global write mutex and returns a release func that
// unlocks it and logs a WARN if either the wait to acquire or the hold exceeded
// slowWriteThreshold. Use as a drop-in for `e.Mu.Lock(); defer e.Mu.Unlock()` on
// write paths: `defer s.lockWrite("label")()`. lock-wait climbing flags writers
// queueing behind a slow holder; lock-hold climbing flags the holder itself.
func (e *Engine) LockWrite(label string) func() {
	waitStart := time.Now()
	e.Mu.Lock()
	acquired := time.Now()
	wait := acquired.Sub(waitStart)
	return func() {
		hold := time.Since(acquired)
		e.Mu.Unlock()
		if wait >= slowWriteThreshold || hold >= slowWriteThreshold {
			log.Printf("slow write %s: lock-wait=%s lock-hold=%s (threshold=%s)",
				label, wait.Round(time.Millisecond), hold.Round(time.Millisecond), slowWriteThreshold)
		}
	}
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

// BeginWriteTxConn begins a write transaction on a held connection.
func (e *Engine) BeginWriteTxConn(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := festwrite.SeedAuditCtx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// WithWriteTx runs fn in a bounded, audited write transaction: it pulls a pooled
// connection BEFORE taking the global write lock (so pool waits stay off-lock),
// then commits (or rolls back on error).
func (e *Engine) WithWriteTx(reqCtx context.Context, festID int64, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	ctx, cancel := festwrite.AuditDetachedContext(reqCtx, festID)
	defer cancel()
	conn, err := e.AcquireWriteConn(ctx, label)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer e.LockWrite(label)()
	tx, err := e.BeginWriteTxConn(ctx, conn)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
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
