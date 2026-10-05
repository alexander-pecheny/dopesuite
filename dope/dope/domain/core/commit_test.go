package core_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

func newFest(t *testing.T) (*core.Engine, int64) {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	userID, err := dopeserver.EnsureSystemUser(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	festID, err := store.InsertReturningID(context.Background(), tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-10-05', '2026-10-05')`, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return &core.Engine{DB: db}, festID
}

func festRevision(t *testing.T, eng *core.Engine, festID int64) int64 {
	t.Helper()
	var revision int64
	if err := eng.DB.QueryRow(`select revision from fests where id = ?`, festID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// A write records one revision for its event, and Settled sees it after the
// commit; a write that fails leaves nothing, not even the revision.
func TestCommitFestWriteRecordsOnceAndSettlesAfterTheCommit(t *testing.T) {
	eng, festID := newFest(t)
	before := festRevision(t, eng, festID)
	var settled int64
	revision, err := eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		_, err := tx.ExecContext(ctx, `update fests set title = 'Новое' where id = ?`, festID)
		return core.FestWrite{Event: "fest:test", Settled: func(e *core.Engine, revision int64) {
			var title string
			if err := e.DB.QueryRow(`select title from fests where id = ?`, festID).Scan(&title); err != nil || title != "Новое" {
				t.Errorf("Settled read %q (%v) before the commit", title, err)
			}
			settled = revision
		}}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision != before+1 || settled != revision || festRevision(t, eng, festID) != revision {
		t.Fatalf("revision %d, settled %d, stored %d, want %d", revision, settled, festRevision(t, eng, festID), before+1)
	}

	refused := errors.New("refused")
	_, err = eng.CommitFestWrite(t.Context(), festID, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		if _, err := tx.ExecContext(ctx, `update fests set title = 'Другое' where id = ?`, festID); err != nil {
			return core.FestWrite{}, err
		}
		return core.FestWrite{Event: "fest:test", Settled: func(*core.Engine, int64) { t.Error("a failed write settled") }}, refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the write's own", err)
	}
	var title string
	if err := eng.DB.QueryRow(`select title from fests where id = ?`, festID).Scan(&title); err != nil || title != "Новое" {
		t.Fatalf("title after a failed write = %q (%v)", title, err)
	}
	if festRevision(t, eng, festID) != revision {
		t.Fatal("a failed write moved the revision")
	}
}
