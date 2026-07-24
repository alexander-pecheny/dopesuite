package tests

import (
	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/journal"
	"dope/dope/storage/store"
	"path/filepath"
	"sync"
	"testing"
)

func newBatchTestServer(t *testing.T) (*dopeserver.Server, dopeserver.MatchScope) {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	festID, gameID := createBracketFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scopeBase := dopeserver.FestScope{FestID: festID, GameID: gameID}
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	scope, err := srv.VerifyMatchInScope(t.Context(), scopeBase, "A")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	return srv, scope
}

// TestBatchMatchUpdateAppliesAllEdits verifies that a batch of per-cell edits
// (a range clear/fill) lands in a single request: every mark is applied and the
// results are recomputed from the edited blob, not a pre-batch snapshot.
func TestBatchMatchUpdateAppliesAllEdits(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	ids := matchTeamIDs(t, srv, scope)
	view, err := srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{
		markOp(ids[0], false, 0, 0, "right"),
		markOp(ids[0], false, 0, 1, "wrong"),
		markOp(ids[1], false, 0, 0, "right"),
		markOp(ids[2], false, 0, 0, "wrong"),
	})
	if err != nil {
		t.Fatalf("batch update: %v", err)
	}

	// Every edit is reflected both in the returned view and on reload, and the
	// total is recomputed from the edited rows (team 1 scored a single right at
	// answer index 0). The recompute reading the edited rows is what the
	// reload-before-recalc guards: without it a batch would persist results that
	// lag every edit.
	reloaded, err := srv.LoadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for label, v := range map[string]store.MatchView{"returned": view, "reloaded": reloaded} {
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
		if got, want := v.Teams[1].Total, store.QuestionValues[0]; got != want {
			t.Fatalf("%s: team1 total = %d, want %d", label, got, want)
		}
	}
}

// TestBatchMatchUpdateIsAtomic verifies that one bad op rolls back the whole
// request — no partial state — since a request's ops share a savepoint.
func TestBatchMatchUpdateIsAtomic(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	ids := matchTeamIDs(t, srv, scope)
	if _, err := srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{
		markOp(ids[0], false, 0, 0, "right"),
		markOp(9999, false, 0, 0, "right"), // not a team of this match
	}); err == nil {
		t.Fatal("batch with a bad op should fail")
	}

	// The good edit that preceded the bad one must not have persisted.
	view, err := srv.LoadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := view.Teams[0].Themes[0].Answers[0]; got != "" {
		t.Fatalf("team0 ans0 = %q after failed batch, want empty (rolled back)", got)
	}
}

// TestScopedMatchUpdateStampsJournal guards that match edits record journal
// row-ops stamped with the scope's fest_id, the owning game_id, and the acting
// user from the request context (the regression was edits committed under a bare
// context.Background(), losing attribution).
func TestScopedMatchUpdateStampsJournal(t *testing.T) {
	srv, scope := newBatchTestServer(t)

	var target int64
	if err := srv.Eng().DB.QueryRow(`select coalesce(max(id), 0) from journal`).Scan(&target); err != nil {
		t.Fatalf("max journal id: %v", err)
	}

	const actor = int64(77)
	ctx := festwrite.WithAuditActor(t.Context(), actor)
	ids := matchTeamIDs(t, srv, scope)
	if _, err := srv.SubmitMatchEdit(ctx, scope, []edit.PatchOp{markOp(ids[0], false, 0, 0, "right")}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var rows, mismatched int
	if err := srv.Eng().DB.QueryRow(
		`select count(*), coalesce(sum(case when fest_id is ? and game_id is ? and actor_user_id is ? then 0 else 1 end), 0)
		   from journal where id > ? and op in (?, ?, ?)`,
		scope.FestID, scope.GameID, actor, target, int(journal.OpRowIns), int(journal.OpRowSet), int(journal.OpRowDel)).
		Scan(&rows, &mismatched); err != nil {
		t.Fatalf("scan journal rows: %v", err)
	}
	if rows == 0 {
		t.Fatal("match edit recorded no journal row-ops")
	}
	if mismatched != 0 {
		t.Fatalf("%d of %d new journal rows are not stamped with fest_id=%d game_id=%d actor=%d",
			mismatched, rows, scope.FestID, scope.GameID, actor)
	}
}

// TestPinBeatsComputedPlace guards the Pin: finishing a match computes places
// from the scores, but a host's pinned place must survive that recompute — it is
// Protocol state in the blob, and the scorer honours it (ADR-0005).
func TestPinBeatsComputedPlace(t *testing.T) {
	srv, scope := newBatchTestServer(t)
	ids := matchTeamIDs(t, srv, scope)

	// Slot 0 outscores everyone, so the scorer would place it first.
	if _, err := srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{
		markOp(ids[0], false, 0, 4, "right"),
		pinOp(ids[0], 4),
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	view, err := srv.SubmitMatchFinish(t.Context(), scope, true)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if got := view.Teams[0].Place; got != 4 {
		t.Fatalf("pinned place = %v after finish, want 4 (the scorer wanted 1)", got)
	}

	// Clearing the pin hands the place back to the scorer.
	if _, err := srv.SubmitMatchFinish(t.Context(), scope, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{
		blobOp("remove", nil, "teams", teamKey(ids[0]), "pin"),
	}); err != nil {
		t.Fatalf("clear pin: %v", err)
	}
	view, err = srv.SubmitMatchFinish(t.Context(), scope, true)
	if err != nil {
		t.Fatalf("re-finish: %v", err)
	}
	if got := view.Teams[0].Place; got != 1 {
		t.Fatalf("unpinned place = %v, want the scorer's 1", got)
	}
}

// TestBadEditDoesNotPoisonItsWindow guards the per-job savepoint: a per-match
// edit writes as it goes, so one that fails halfway must roll back alone and
// leave its co-editors' edits in the same window committed.
func TestBadEditDoesNotPoisonItsWindow(t *testing.T) {
	srv, scope := newBatchTestServer(t)
	ids := matchTeamIDs(t, srv, scope)

	var wg sync.WaitGroup
	var goodErr, badErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, goodErr = srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{markOp(ids[1], false, 1, 2, "right")})
	}()
	go func() {
		defer wg.Done()
		// A real write (so the savepoint has something to undo) followed by a path
		// the blob has no room for.
		_, badErr = srv.SubmitMatchEdit(t.Context(), scope, []edit.PatchOp{
			markOp(ids[0], false, 0, 0, "right"),
			markOp(ids[0], false, 99, 0, "right"),
		})
	}()
	wg.Wait()

	if goodErr != nil {
		t.Fatalf("the valid edit failed alongside a bad one: %v", goodErr)
	}
	if badErr == nil {
		t.Fatal("the out-of-range edit should have failed")
	}
	view, err := srv.LoadScopedMatchViewLocked(scope)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := view.Teams[1].Themes[1].Answers[2]; got != "right" {
		t.Fatalf("co-editor's mark = %q, want right", got)
	}
	if got := view.Teams[0].Themes[0].Answers[0]; got != "" {
		t.Fatalf("failed edit's first op persisted (%q); its savepoint did not roll back", got)
	}
}
