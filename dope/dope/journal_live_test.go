package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/journal"
	"dope/dope/realtime"
	"path/filepath"
	"testing"
)

// TestJournalRecordsLiveEdits proves the unified journal is written on the live
// edit path (replacing the old events table): a seed-import edit lands a journal
// row with the right opcode, payload and fest-scoped seq, and no `events` table
// exists anymore.
func TestJournalRecordsLiveEdits(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// events table must be gone after migration.
	var n int
	if err := db.QueryRow(`select count(*) from sqlite_master where type='table' and name='events'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("events table should be dropped, found %d", n)
	}

	festID, ekGameID := createSeedImportFixture(t, db)
	srv := &server{
		db: db,
		rt: realtime.NewManager(),
	}

	// Attribute the edit to a user via the audit context, as the middleware does.
	ctx := withAuditRequestID(withAuditActor(context.Background(), 42), "req-test")
	scope := festScope{FestID: festID, GameID: ekGameID}
	if _, _, _, err := srv.importSeedsFromKSI(ctx, scope); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	// The seed-import edit must have produced a journal row with the KSI opcode.
	var (
		op      int
		seq     int64
		actor   sql.NullInt64
		req     sql.NullString
		payload []byte
	)
	err = db.QueryRow(`
select op, seq, actor_user_id, request_id, payload
from journal where fest_id = ? and op = ? order by seq desc limit 1`,
		festID, int(journal.OpEvSeedImportKSI)).Scan(&op, &seq, &actor, &req, &payload)
	if err != nil {
		t.Fatalf("expected seed-import journal row: %v", err)
	}
	if journal.Op(op) != journal.OpEvSeedImportKSI {
		t.Fatalf("op = %d, want %d", op, journal.OpEvSeedImportKSI)
	}
	if seq <= 0 {
		t.Fatalf("seq = %d, want > 0 (per-fest revision)", seq)
	}
	if !actor.Valid || actor.Int64 != 42 {
		t.Fatalf("actor = %v, want 42", actor)
	}
	if !req.Valid || req.String != "req-test" {
		t.Fatalf("request_id = %v, want req-test", req)
	}
	if len(payload) == 0 {
		t.Fatalf("payload should carry the edit content")
	}
	if journal.EventTypeForOp(journal.OpEvSeedImportKSI) != "seed-import:ksi" {
		t.Fatalf("event-type round-trip broken")
	}

	// The journal serves as the viewer event source: events-since returns the
	// edits in order with their event types and payloads.
	evs, err := srv.journalEventsSince(ctx, festID, 0)
	if err != nil {
		t.Fatalf("journalEventsSince: %v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("expected events from journal")
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatalf("events not ordered by seq: %d then %d", evs[i-1].Seq, evs[i].Seq)
		}
	}
	if evs[len(evs)-1].EventType != "seed-import:ksi" {
		t.Fatalf("last event type = %q, want seed-import:ksi", evs[len(evs)-1].EventType)
	}

	// After archiving the hot rows into a cold segment, events-since must still
	// return the same edits (now read from the segment) — proving the single
	// table feeds viewers across the hot/cold boundary.
	lastSeq := evs[len(evs)-1].Seq
	if _, err := journal.ArchiveFest(ctx, srv.db, festID, lastSeq); err != nil {
		t.Fatalf("archive: %v", err)
	}
	var hot int
	db.QueryRow(`select count(*) from journal where fest_id=?`, festID).Scan(&hot)
	if hot != 0 {
		t.Fatalf("expected hot rows folded, %d remain", hot)
	}
	evs2, err := srv.journalEventsSince(ctx, festID, 0)
	if err != nil {
		t.Fatalf("journalEventsSince after archive: %v", err)
	}
	if len(evs2) != len(evs) {
		t.Fatalf("events after archive = %d, want %d", len(evs2), len(evs))
	}
	if evs2[len(evs2)-1].EventType != "seed-import:ksi" {
		t.Fatalf("post-archive last event type = %q", evs2[len(evs2)-1].EventType)
	}
}
