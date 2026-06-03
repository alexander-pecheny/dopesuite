package main

import (
	"path/filepath"
	"testing"
)

func newBatchTestServer(t *testing.T) (*server, matchScope) {
	t.Helper()
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	festID, gameID := createBracketFixture(t, db)
	srv := &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]struct{}),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}
	scopeBase := festScope{FestID: festID, GameID: gameID}
	if _, _, _, err := srv.importSeedsFromKSI(t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	scope, err := srv.verifyMatchInScope(t.Context(), scopeBase, "A")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	return srv, scope
}

// TestBatchMatchUpdateAppliesAllEdits verifies that a batch of per-cell edits
// (a range clear/fill) lands in a single request: every mark is applied and the
// results are recomputed from the edited rows, not a pre-batch snapshot.
func TestBatchMatchUpdateAppliesAllEdits(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	theme, ans0, ans1 := 0, 0, 1
	right, wrong := "right", "wrong"
	edits := []updateRequest{
		{Team: 0, Theme: &theme, Answer: &ans0, Mark: &right},
		{Team: 0, Theme: &theme, Answer: &ans1, Mark: &wrong},
		{Team: 1, Theme: &theme, Answer: &ans0, Mark: &right},
		{Team: 2, Theme: &theme, Answer: &ans0, Mark: &wrong},
	}
	view, _, _, _, err := srv.applyScopedMatchUpdate(scope, edits)
	if err != nil {
		t.Fatalf("batch update: %v", err)
	}

	// Every edit is reflected both in the returned view and on reload, and the
	// total is recomputed from the edited rows (team 1 scored a single right at
	// answer index 0). The recompute reading the edited rows is what the
	// reload-before-recalc guards: without it a batch would persist results that
	// lag every edit.
	reloaded, err := srv.loadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for label, v := range map[string]MatchView{"returned": view, "reloaded": reloaded} {
		if v.Teams[0].Themes[0].Answers[0] != "right" {
			t.Fatalf("%s: team0 ans0 = %q, want right", label, v.Teams[0].Themes[0].Answers[0])
		}
		if v.Teams[0].Themes[0].Answers[1] != "wrong" {
			t.Fatalf("%s: team0 ans1 = %q, want wrong", label, v.Teams[0].Themes[0].Answers[1])
		}
		if v.Teams[1].Themes[0].Answers[0] != "right" {
			t.Fatalf("%s: team1 ans0 = %q, want right", label, v.Teams[1].Themes[0].Answers[0])
		}
		if v.Teams[2].Themes[0].Answers[0] != "wrong" {
			t.Fatalf("%s: team2 ans0 = %q, want wrong", label, v.Teams[2].Themes[0].Answers[0])
		}
		if got, want := v.Teams[1].Total, questionValues[0]; got != want {
			t.Fatalf("%s: team1 total = %d, want %d", label, got, want)
		}
	}
}

// TestBatchMatchUpdateIsAtomic verifies that one bad edit rolls back the whole
// batch — no partial state — since all edits share a single transaction.
func TestBatchMatchUpdateIsAtomic(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	theme, ans0 := 0, 0
	right := "right"
	badTeam := 99 // out of range -> applyMatchEditTx errors mid-batch
	edits := []updateRequest{
		{Team: 0, Theme: &theme, Answer: &ans0, Mark: &right},
		{Team: badTeam, Theme: &theme, Answer: &ans0, Mark: &right},
	}
	if _, _, _, _, err := srv.applyScopedMatchUpdate(scope, edits); err == nil {
		t.Fatal("batch with a bad edit should fail")
	}

	// The good edit that preceded the bad one must not have persisted.
	view, err := srv.loadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := view.Teams[0].Themes[0].Answers[0]; got != "" {
		t.Fatalf("team0 ans0 = %q after failed batch, want empty (rolled back)", got)
	}
}
