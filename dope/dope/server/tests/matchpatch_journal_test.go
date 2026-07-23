package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	dopeserver "dope/dope/server"
	"dope/dope/storage/journal"
)

// A live mark edit journals as one semantic OpMatchPatch record; the matches
// row trigger stays silent about state_json. Reverting past the edit replays
// the patch and restores the pre-edit blob.
func TestMatchEditJournalsAsMatchPatch(t *testing.T) {
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
	scopeBase := dopeserver.FestScope{FestID: festID, GameID: gameID}
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scopeBase); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	var beforeID int64
	if err := db.QueryRow(`select coalesce(max(id), 0) from journal`).Scan(&beforeID); err != nil {
		t.Fatal(err)
	}
	cpTx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.WriteGameCheckpoint(context.Background(), cpTx, gameID, beforeID); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	if err := cpTx.Commit(); err != nil {
		t.Fatal(err)
	}

	theme, ans, right := 0, 4, "right"
	scope, err := srv.VerifyMatchInScope(t.Context(), scopeBase, "A")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if _, _, _, _, err := srv.ApplyScopedMatchUpdate(t.Context(), scope,
		[]dopeserver.UpdateRequest{{Team: 0, Theme: &theme, Answer: &ans, Mark: &right}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	rows, err := db.Query(`select op, payload from journal where id > ?`, beforeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	patches, stateRowOps := 0, 0
	for rows.Next() {
		var op int
		var payload string
		if err := rows.Scan(&op, &payload); err != nil {
			t.Fatal(err)
		}
		switch journal.Op(op) {
		case journal.OpMatchPatch:
			patches++
			if !strings.Contains(payload, `/answers/4`) || !strings.Contains(payload, `"right"`) {
				t.Fatalf("patch payload = %s", payload)
			}
		case journal.OpRowSet:
			if strings.Contains(payload, "state_json") {
				stateRowOps++
			}
		}
	}
	if patches != 1 || stateRowOps != 0 {
		t.Fatalf("journal after edit: %d match patches (want 1), %d state row-ops (want 0)", patches, stateRowOps)
	}

	// Replay parity: reconstructing at the pre-edit point clears the mark,
	// reconstructing at the edit reproduces the exact blob.
	var afterState string
	if err := db.QueryRow(`select state_json from matches where game_id = ? and code = 'A'`, gameID).Scan(&afterState); err != nil {
		t.Fatal(err)
	}
	var afterID int64
	if err := db.QueryRow(`select max(id) from journal`).Scan(&afterID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := core.ReconstructGameStateAt(context.Background(), tx, gameID, afterID); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	var replayed string
	if err := tx.QueryRow(`select state_json from matches where game_id = ? and code = 'A'`, gameID).Scan(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed != afterState {
		t.Fatalf("replayed blob differs:\n got %s\nwant %s", replayed, afterState)
	}
}
