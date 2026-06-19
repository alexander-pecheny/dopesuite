package tests

import (
	"context"
	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
	"dope/dope/storage/journal"
	"path/filepath"
	"testing"
)

// TestDerivedRevertReproducesGameState drives real edits through the live
// handlers (which fire the journal row-op triggers), then verifies that
// reconstructing the game's state at an earlier journal point via
// checkpoint+replay reproduces exactly the state the game had at that point.
// This is the per-game derived-revert correctness canary.
func TestDerivedRevertReproducesGameState(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	festID, gameID := createSeedImportFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scopeBase := dopeserver.FestScope{FestID: festID, GameID: gameID}
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), ctx, scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	// Genesis checkpoint after structural setup (the bracket import is suppressed
	// and emits no row-ops, so a checkpoint must anchor replay here).
	maxID := func() int64 {
		var id int64
		db.QueryRow(`select coalesce(max(id),0) from journal`).Scan(&id)
		return id
	}
	g0 := maxID()
	tx0, _ := db.Begin()
	if err := journal.WriteGameCheckpoint(ctx, tx0, gameID, g0); err != nil {
		t.Fatalf("genesis checkpoint: %v", err)
	}
	tx0.Commit()
	truth0 := mustCapture(t, db, gameID)

	apply := func(code string, req dopeserver.UpdateRequest) {
		t.Helper()
		scope, err := srv.VerifyMatchInScope(ctx, scopeBase, code)
		if err != nil {
			t.Fatalf("scope %s: %v", code, err)
		}
		if _, _, _, _, err := srv.ApplyScopedMatchUpdate(ctx, scope, []dopeserver.UpdateRequest{req}); err != nil {
			t.Fatalf("apply %s: %v", code, err)
		}
	}

	theme, answer := 0, 4
	right := "right"

	// Edit 1.
	apply("A", dopeserver.UpdateRequest{Team: 0, Theme: &theme, Answer: &answer, Mark: &right})
	p1 := maxID()
	truth1 := mustCapture(t, db, gameID)
	if checkpointKey(t, truth1) == checkpointKey(t, truth0) {
		t.Fatalf("first edit did not change game state")
	}

	// Edit 2.
	answer2 := 3
	apply("A", dopeserver.UpdateRequest{Team: 1, Theme: &theme, Answer: &answer2, Mark: &right})
	truth2 := mustCapture(t, db, gameID)
	if checkpointKey(t, truth2) == checkpointKey(t, truth1) {
		t.Fatalf("second edit did not change game state")
	}

	// Reconstruct the state at p1 (after edit 1) and compare to truth1.
	assertReconstruct := func(point int64, want *journal.GameCheckpoint, label string) {
		t.Helper()
		tx, _ := db.Begin()
		defer tx.Rollback()
		if err := core.ReconstructGameStateAt(ctx, tx, gameID, point); err != nil {
			t.Fatalf("%s: reconstruct: %v", label, err)
		}
		got, err := journal.CaptureGameCheckpoint(ctx, tx, gameID)
		if err != nil {
			t.Fatalf("%s: capture: %v", label, err)
		}
		if checkpointKey(t, got) != checkpointKey(t, want) {
			t.Fatalf("%s: reconstructed state != live state at that point", label)
		}
	}
	assertReconstruct(g0, truth0, "revert-to-genesis")
	assertReconstruct(p1, truth1, "revert-to-after-edit-1")

	// The public revert mutates live state: after reverting to p1 the live game
	// must equal truth1, and the revert itself is recorded as a journal entry.
	if _, err := srv.Eng().RevertGameToPoint(ctx, festID, gameID, p1); err != nil {
		t.Fatalf("revertGameToPoint: %v", err)
	}
	live := mustCapture(t, db, gameID)
	if checkpointKey(t, live) != checkpointKey(t, truth1) {
		t.Fatalf("live state after revert != state at p1")
	}
	var reverts int
	db.QueryRow(`select count(*) from journal where op = ?`, int(journal.OpEvGameRevert)).Scan(&reverts)
	if reverts == 0 {
		t.Fatalf("revert was not recorded in the journal")
	}
}
