package dopeserver

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestWithWriteTxCommitAndRollback verifies the hardened write helper commits on
// a nil return and rolls back (preserving the prior state) on an error, and that
// the error is returned verbatim so callers can still match sentinels.
func TestWithWriteTxCommitAndRollback(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	ctx := context.Background()

	if err := srv.withWriteTx(ctx, festID, "test-commit", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `update fests set title = ? where id = ?`, "committed", festID)
		return err
	}); err != nil {
		t.Fatalf("commit returned error: %v", err)
	}
	var title string
	if err := srv.db.QueryRow(`select title from fests where id = ?`, festID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "committed" {
		t.Fatalf("after commit title = %q, want committed", title)
	}

	sentinel := errors.New("boom")
	err := srv.withWriteTx(ctx, festID, "test-rollback", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `update fests set title = ? where id = ?`, "rolledback", festID); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if err := srv.db.QueryRow(`select title from fests where id = ?`, festID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "committed" {
		t.Fatalf("after rollback title = %q, want committed (rolled back)", title)
	}
}
