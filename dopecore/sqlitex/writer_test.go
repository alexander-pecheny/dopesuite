package sqlitex_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"pecheny.me/dopecore/sqlitex"
	"pecheny.me/dopecore/sqlitex/sqlitextest"
)

func newWriter(t *testing.T) sqlitex.Writer {
	t.Helper()
	db, err := sqlitextest.Open(filepath.Join(t.TempDir(), "w.db"), func(db *sql.DB) error {
		_, err := db.Exec(`create table kv(k text primary key, v text not null)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlitex.Writer{DB: db, Mu: &sync.Mutex{}}
}

func value(t *testing.T, w sqlitex.Writer, k string) string {
	t.Helper()
	var v string
	err := w.DB.QueryRow(`select v from kv where k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func put(k, v string) func(ctx context.Context, tx *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `insert into kv(k, v) values(?, ?) on conflict(k) do update set v = excluded.v`, k, v)
		return err
	}
}

// A nil return commits, an error rolls back and comes back as it is, so a
// caller can still match its sentinel.
func TestWriterCommitsAndRollsBack(t *testing.T) {
	w := newWriter(t)
	ctx := context.Background()
	if err := w.Tx(ctx, "commit", put("a", "committed")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := value(t, w, "a"); got != "committed" {
		t.Fatalf("after commit a = %q", got)
	}

	sentinel := errors.New("boom")
	err := w.Tx(ctx, "rollback", func(ctx context.Context, tx *sql.Tx) error {
		if err := put("a", "rolled back")(ctx, tx); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the sentinel back, got %v", err)
	}
	if got := value(t, w, "a"); got != "committed" {
		t.Fatalf("after rollback a = %q, want committed", got)
	}
}

// The write runs on a context the request cannot cancel, and the Writer's own
// timeout still ends it.
func TestWriterDetachesFromTheRequestAndTimesOut(t *testing.T) {
	w := newWriter(t)
	w.Timeout = 50 * time.Millisecond
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := w.Tx(reqCtx, "slow", func(ctx context.Context, tx *sql.Tx) error {
		if err := put("b", "x")(ctx, tx); err != nil {
			t.Errorf("a cancelled request reached the write: %v", err)
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the deadline, got %v", err)
	}
	if got := value(t, w, "b"); got != "" {
		t.Fatalf("a timed-out write committed: b = %q", got)
	}
}

// Begin runs inside the transaction, and its failure fails the write.
func TestWriterBeginHook(t *testing.T) {
	w := newWriter(t)
	w.Begin = put("seeded", "yes")
	if err := w.Tx(context.Background(), "hook", put("c", "1")); err != nil {
		t.Fatal(err)
	}
	if value(t, w, "seeded") != "yes" || value(t, w, "c") != "1" {
		t.Fatal("Begin's write or fn's write is missing")
	}
	refuse := errors.New("no audit")
	w.Begin = func(context.Context, *sql.Tx) error { return refuse }
	if err := w.Tx(context.Background(), "hook", put("d", "1")); !errors.Is(err, refuse) {
		t.Fatalf("want Begin's error, got %v", err)
	}
	if value(t, w, "d") != "" {
		t.Fatal("fn ran after Begin failed")
	}
}

// Writes queue on the lock: none of them is lost.
func TestWriterSerialises(t *testing.T) {
	w := newWriter(t)
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			err := w.Tx(context.Background(), "inc", func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `insert into kv(k, v) values('n', '1')
on conflict(k) do update set v = cast(cast(v as integer) + 1 as text)`)
				return err
			})
			if err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	if got := value(t, w, "n"); got != "20" {
		t.Fatalf("n = %s, want %d", got, n)
	}
}
