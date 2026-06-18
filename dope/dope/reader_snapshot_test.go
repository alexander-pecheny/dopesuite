package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestSnapshotReadsDecoupledFromWriteLock verifies the read-path snapshot loaders
// (a) return the same data as the s.mu-locked loaders, and (b) do NOT take s.mu,
// so a viewer read completes even while a writer holds the global write lock —
// the whole point of moving cross-game reads onto a WAL snapshot transaction.
func TestSnapshotReadsDecoupledFromWriteLock(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]subInfo),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}
	scope := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scope); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	// (a) snapshot fest view == locked fest view.
	srv.mu.RLock()
	lockedFest, err := srv.loadFestViewLocked(festID, gameID)
	srv.mu.RUnlock()
	if err != nil {
		t.Fatalf("locked fest view: %v", err)
	}
	snapFest, err := srv.loadFestViewSnapshot(festID, gameID)
	if err != nil {
		t.Fatalf("snapshot fest view: %v", err)
	}
	if !reflect.DeepEqual(lockedFest, snapFest) {
		t.Fatalf("snapshot fest view differs from locked fest view")
	}

	// (a) snapshot match view == locked match view.
	mscope, err := srv.verifyMatchInScope(t.Context(), scope, "A")
	if err != nil {
		t.Fatalf("scope A: %v", err)
	}
	srv.mu.RLock()
	lockedMatch, err := srv.loadScopedMatchViewLocked(mscope)
	srv.mu.RUnlock()
	if err != nil {
		t.Fatalf("locked match view: %v", err)
	}
	snapMatch, err := srv.loadScopedMatchViewSnapshot(mscope)
	if err != nil {
		t.Fatalf("snapshot match view: %v", err)
	}
	if !reflect.DeepEqual(lockedMatch, snapMatch) {
		t.Fatalf("snapshot match view differs from locked match view")
	}

	// (b) reads run while a writer holds s.mu — they must not block on it.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	done := make(chan error, 2)
	go func() { _, e := srv.loadFestViewSnapshot(festID, gameID); done <- e }()
	go func() { _, e := srv.loadScopedMatchViewSnapshot(mscope); done <- e }()
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("snapshot read under held write lock: %v", e)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("snapshot read blocked while a writer held s.mu — reader not decoupled")
		}
	}
}
