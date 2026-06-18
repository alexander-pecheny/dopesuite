package tests

import (
	"context"
	"dope/dope/journal"
	"testing"
)

func TestArchiveFestJournal(t *testing.T) {
	db := openMemDB(t)
	ctx := context.Background()
	mustExec(t, db, `create table journal(
  id integer primary key, fest_id integer not null, seq integer not null, ts text not null,
  actor_user_id integer, request_id text, op integer not null, payload blob not null default x'', created_at text not null)`)
	if err := journal.CreateTables(db); err != nil {
		t.Fatal(err)
	}

	// Two fests' worth of hot rows.
	for i := 1; i <= 5; i++ {
		mustExec(t, db, `insert into journal(fest_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
values(1, ?, '2026-01-01T00:00:00.000Z', 7, 'r1', ?, ?, '2026-01-01T00:00:00.000Z')`,
			i, int(journal.OpEvMatchUpdate), []byte("p"+string(rune('0'+i))))
	}
	mustExec(t, db, `insert into journal(fest_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
values(2, 1, '2026-01-01T00:00:00.000Z', 7, 'r2', ?, ?, '2026-01-01T00:00:00.000Z')`,
		int(journal.OpEvGameStatePatch), []byte("other-fest"))

	n, err := journal.ArchiveFest(ctx, db, 1, 3)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n != 3 {
		t.Fatalf("archived %d rows, want 3", n)
	}

	// Hot rows 1..3 for fest 1 gone; 4,5 remain; fest 2 untouched.
	var hotFest1, hotFest2 int
	db.QueryRow(`select count(*) from journal where fest_id=1`).Scan(&hotFest1)
	db.QueryRow(`select count(*) from journal where fest_id=2`).Scan(&hotFest2)
	if hotFest1 != 2 || hotFest2 != 1 {
		t.Fatalf("hot rows fest1=%d fest2=%d, want 2 and 1", hotFest1, hotFest2)
	}

	// Segment decodes back to the 3 archived records, in order, with payloads.
	recs := decodeAllSegments(t, db)
	if len(recs) != 3 {
		t.Fatalf("segment has %d records, want 3", len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("record %d seq=%d", i, r.Seq)
		}
		if r.Op != journal.OpEvMatchUpdate {
			t.Fatalf("record %d op=%v", i, r.Op)
		}
		want := "p" + string(rune('0'+i+1))
		if string(r.Args) != want {
			t.Fatalf("record %d payload=%q want %q", i, r.Args, want)
		}
	}

	// Re-archiving the same range is a no-op.
	if n, err := journal.ArchiveFest(ctx, db, 1, 3); err != nil || n != 0 {
		t.Fatalf("re-archive: n=%d err=%v, want 0/nil", n, err)
	}
}
