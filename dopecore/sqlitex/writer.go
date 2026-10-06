package sqlitex

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"
)

// WriteTimeout bounds a whole write transaction. A healthy commit is well under
// a millisecond, so 5 s is generous for any real write. The bound exists for
// one step that can otherwise wait forever: taking a connection from the pool.
// With the bound, a starved pool fails the write and frees the lock, instead of
// pinning it and freezing every other write behind it (dope, 2026-06-13: one
// write waited about 55 minutes on exactly that).
const WriteTimeout = 5 * time.Second

// SlowWrite is the point past which a write's pool wait, its wait for the lock
// or its time holding the lock is logged. The check is always on, because it
// costs a few clock reads per write, and a stall then shows up in the journal
// while it builds instead of leaving the server silent.
const SlowWrite = time.Second

// Writer runs a database's write transactions one at a time. Every app keeps
// one global write lock and passes it here; the Writer holds the discipline
// around it:
//
//   - the pooled connection is taken BEFORE the lock, so a pool wait never
//     happens while holding it;
//   - the transaction runs on a context detached from the request (a client
//     that hangs up does not abort a write half way) and bounded by Timeout;
//   - fn's error comes back as it is, so callers can still match sentinels.
//
// The zero Timeout means WriteTimeout. Begin, when set, runs right after BEGIN
// inside the transaction: dope seeds its audit context there.
type Writer struct {
	DB      *sql.DB
	Mu      sync.Locker
	Timeout time.Duration
	Begin   func(ctx context.Context, tx *sql.Tx) error
}

// Context is the context a write runs on: detached from the request's
// cancellation but keeping its values, and bounded by the Writer's timeout.
func (w Writer) Context(reqCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(reqCtx), w.timeout())
}

func (w Writer) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return WriteTimeout
}

// Tx runs fn in one write transaction on a context made by Context, and
// commits when fn returns nil.
func (w Writer) Tx(reqCtx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	ctx, cancel := w.Context(reqCtx)
	defer cancel()
	return w.Run(ctx, label, fn)
}

// Run is Tx on a context the caller has already detached and bounded. dope uses
// it, since its write context also carries the audit attribution.
func (w Writer) Run(ctx context.Context, label string, fn func(ctx context.Context, tx *sql.Tx) error) error {
	conn, err := w.Conn(ctx, label)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer w.Lock(label)()
	tx, err := w.BeginOn(ctx, conn)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Conn takes a pooled connection for one write. Call it before Lock, so the
// pool wait is bounded by ctx and never happens under the lock.
func (w Writer) Conn(ctx context.Context, label string) (*sql.Conn, error) {
	start := time.Now()
	conn, err := w.DB.Conn(ctx)
	if waited := time.Since(start); waited >= SlowWrite {
		log.Printf("slow write %s: pool-wait=%s err=%v", label, waited.Round(time.Millisecond), err)
	}
	return conn, err
}

// Lock takes the write lock and returns the func that releases it. A wait or a
// hold past SlowWrite is logged on release: a growing wait means writers queue
// behind a slow holder, a growing hold points at the holder itself.
func (w Writer) Lock(label string) (unlock func()) {
	waitStart := time.Now()
	w.Mu.Lock()
	acquired := time.Now()
	wait := acquired.Sub(waitStart)
	return func() {
		hold := time.Since(acquired)
		w.Mu.Unlock()
		if wait >= SlowWrite || hold >= SlowWrite {
			log.Printf("slow write %s: lock-wait=%s lock-hold=%s",
				label, wait.Round(time.Millisecond), hold.Round(time.Millisecond))
		}
	}
}

// BeginOn begins a write transaction on a held connection and runs Begin in it.
func (w Writer) BeginOn(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if w.Begin != nil {
		if err := w.Begin(ctx, tx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return tx, nil
}
