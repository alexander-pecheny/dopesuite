package main

import (
	"context"
	"database/sql"
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
// invoke it BEFORE taking s.mu, so the pool wait — the exact step that stalled
// ~55 min on 2026-06-13 — happens off-lock and bounded by ctx (writeTxTimeout). A
// wait past slowWriteThreshold is logged as the pool-starvation canary.
func (s *server) acquireWriteConn(ctx context.Context, label string) (*sql.Conn, error) {
	start := time.Now()
	conn, err := s.db.Conn(ctx)
	if waited := time.Since(start); waited >= slowWriteThreshold {
		log.Printf("slow write %s: pool-wait=%s err=%v (threshold=%s)",
			label, waited.Round(time.Millisecond), err, slowWriteThreshold)
	}
	return conn, err
}

// lockWrite acquires the global write mutex and returns a release func that
// unlocks it and logs a WARN if either the wait to acquire or the hold exceeded
// slowWriteThreshold. Use as a drop-in for `s.mu.Lock(); defer s.mu.Unlock()` on
// write paths: `defer s.lockWrite("label")()`. lock-wait climbing flags writers
// queueing behind a slow holder; lock-hold climbing flags the holder itself.
func (s *server) lockWrite(label string) func() {
	waitStart := time.Now()
	s.mu.Lock()
	acquired := time.Now()
	wait := acquired.Sub(waitStart)
	return func() {
		hold := time.Since(acquired)
		s.mu.Unlock()
		if wait >= slowWriteThreshold || hold >= slowWriteThreshold {
			log.Printf("slow write %s: lock-wait=%s lock-hold=%s (threshold=%s)",
				label, wait.Round(time.Millisecond), hold.Round(time.Millisecond), slowWriteThreshold)
		}
	}
}
