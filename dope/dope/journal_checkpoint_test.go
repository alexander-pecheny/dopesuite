package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"
)

// TestGameCheckpointRoundTrip captures a game's state, mutates it, restores the
// snapshot, and asserts the game returns to the captured state — the foundation
// for per-game derived revert.
func TestGameCheckpointRoundTrip(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	_, ekGameID := createSeedImportFixture(t, db)
	srv := &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]subInfo),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}

	cp0 := mustCapture(t, db, ekGameID)

	// Mutate: import seeds (writes match_slots, themes, results, state).
	scope := festScope{FestID: mustFestOfGame(t, db, ekGameID), GameID: ekGameID}
	if _, _, _, err := srv.importSeedsFromKSI(ctx, scope); err != nil {
		t.Fatalf("import seeds: %v", err)
	}
	cp1 := mustCapture(t, db, ekGameID)
	if checkpointKey(t, cp0) == checkpointKey(t, cp1) {
		t.Fatalf("expected import to change game state")
	}

	// Restore the original snapshot.
	tx, _ := db.Begin()
	if err := restoreGameCheckpoint(ctx, tx, ekGameID, cp0); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	cp2 := mustCapture(t, db, ekGameID)
	if checkpointKey(t, cp2) != checkpointKey(t, cp0) {
		t.Fatalf("restore did not return game to captured state")
	}
}

// TestBackfillGameCheckpoints verifies the migration step that gives every
// existing game a genesis checkpoint (so per-game revert has an anchor).
func TestBackfillGameCheckpoints(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, gameID := createSeedImportFixture(t, db)

	// Simulate a pre-checkpoint (migration) state.
	if _, err := db.Exec(`delete from journal_checkpoint`); err != nil {
		t.Fatal(err)
	}
	var games int
	db.QueryRow(`select count(*) from games`).Scan(&games)

	if err := backfillGameCheckpoints(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var cps int
	db.QueryRow(`select count(*) from journal_checkpoint`).Scan(&cps)
	if cps != games {
		t.Fatalf("backfill wrote %d checkpoints, want one per game (%d)", cps, games)
	}
	var forGame int
	db.QueryRow(`select count(*) from journal_checkpoint where game_id = ?`, gameID).Scan(&forGame)
	if forGame == 0 {
		t.Fatalf("game %d got no genesis checkpoint", gameID)
	}
	// Idempotent: re-running adds nothing.
	if err := backfillGameCheckpoints(db); err != nil {
		t.Fatal(err)
	}
	var cps2 int
	db.QueryRow(`select count(*) from journal_checkpoint`).Scan(&cps2)
	if cps2 != cps {
		t.Fatalf("backfill not idempotent: %d -> %d", cps, cps2)
	}
}

// TestGameCheckpointEncodeRoundTrip checks compress/serialize round-trips.
func TestGameCheckpointEncodeRoundTrip(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, ekGameID := createSeedImportFixture(t, db)
	cp := mustCapture(t, db, ekGameID)
	blob, err := encodeGameCheckpoint(cp)
	if err != nil {
		t.Fatal(err)
	}
	cp2, err := decodeGameCheckpoint(blob)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointKey(t, cp) != checkpointKey(t, cp2) {
		t.Fatalf("encode/decode changed checkpoint")
	}
}

func mustCapture(t *testing.T, q rowQuerier, gameID int64) *gameCheckpoint {
	t.Helper()
	cp, err := captureGameCheckpoint(context.Background(), q, gameID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	return cp
}

func mustFestOfGame(t *testing.T, db *sql.DB, gameID int64) int64 {
	t.Helper()
	var festID int64
	if err := db.QueryRow(`select fest_id from games where id = ?`, gameID).Scan(&festID); err != nil {
		t.Fatalf("fest of game: %v", err)
	}
	return festID
}

// checkpointKey produces an order-independent canonical string for comparison:
// rows within each table are sorted by their JSON encoding.
func checkpointKey(t *testing.T, cp *gameCheckpoint) string {
	t.Helper()
	type tbl struct {
		Name string   `json:"n"`
		Rows []string `json:"r"`
	}
	var tables []tbl
	for name, rows := range cp.Tables {
		encoded := make([]string, 0, len(rows))
		for _, r := range rows {
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, string(b))
		}
		sort.Strings(encoded)
		tables = append(tables, tbl{Name: name, Rows: encoded})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })
	out, _ := json.Marshal(struct {
		State  string `json:"s"`
		Tables []tbl  `json:"t"`
	}{State: cp.StateJSON, Tables: tables})
	return string(out)
}
