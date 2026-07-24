package server

import (
	"context"
	"database/sql"
	"time"
)

// tombstone is the one write path for deletes (ADR-0002): stamp deleted_at on
// whatever rows match, but only live ones — a re-delete (an offline-queue
// replay, a double click) is a no-op and never resets the 14-day reap clock.
// Reads filter `deleted_at is null`; expiry and destruction live in reap.go.
func tombstone(ctx context.Context, tx *sql.Tx, table, where string, args ...any) error {
	args = append([]any{rfc3339(time.Now())}, args...)
	_, err := tx.ExecContext(ctx,
		`update `+table+` set deleted_at = ? where deleted_at is null and (`+where+`)`, args...)
	return err
}
