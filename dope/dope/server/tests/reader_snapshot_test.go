package tests

import (
	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestSnapshotReadsDecoupledFromWriteLock verifies the read-path snapshot loaders
// (a) return the same data as the s.Eng().Mu-locked loaders, and (b) do NOT take s.Eng().Mu,
// so a viewer read completes even while a writer holds the global write lock —
// the whole point of moving cross-game reads onto a WAL snapshot transaction.
func TestSnapshotReadsDecoupledFromWriteLock(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scope := dopeserver.FestScope{FestID: festID, GameID: gameID}
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scope); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	// (a) snapshot fest view == locked fest view.
	srv.Eng().Mu.RLock()
	lockedFest, err := srv.LoadFestViewLocked(festID, gameID)
	srv.Eng().Mu.RUnlock()
	if err != nil {
		t.Fatalf("locked fest view: %v", err)
	}
	snapFest, err := srv.LoadFestViewSnapshot(festID, gameID)
	if err != nil {
		t.Fatalf("snapshot fest view: %v", err)
	}
	if !reflect.DeepEqual(lockedFest, snapFest) {
		t.Fatalf("snapshot fest view differs from locked fest view")
	}

	// (a) snapshot match view == locked match view.
	mscope, err := srv.VerifyMatchInScope(t.Context(), scope, "A")
	if err != nil {
		t.Fatalf("scope A: %v", err)
	}
	srv.Eng().Mu.RLock()
	lockedMatch, err := srv.LoadScopedMatchViewLocked(mscope)
	srv.Eng().Mu.RUnlock()
	if err != nil {
		t.Fatalf("locked match view: %v", err)
	}
	snapMatch, err := srv.LoadScopedMatchViewSnapshot(mscope)
	if err != nil {
		t.Fatalf("snapshot match view: %v", err)
	}
	if !reflect.DeepEqual(lockedMatch, snapMatch) {
		t.Fatalf("snapshot match view differs from locked match view")
	}

	// (b) reads run while a writer holds s.Eng().Mu — they must not block on it.
	srv.Eng().Mu.Lock()
	defer srv.Eng().Mu.Unlock()
	done := make(chan error, 2)
	go func() { _, e := srv.LoadFestViewSnapshot(festID, gameID); done <- e }()
	go func() { _, e := srv.LoadScopedMatchViewSnapshot(mscope); done <- e }()
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("snapshot read under held write lock: %v", e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("snapshot read blocked while a writer held s.Eng().Mu — reader not decoupled")
		}
	}
}
