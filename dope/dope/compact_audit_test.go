package main

import (
	"context"
	"database/sql"
	"testing"
)

// TestCompactAuditLog verifies the one-time compaction: legacy uncompressed
// rows get compressed, their JSON reads back identically via dope_unz, and a
// second run is a no-op (idempotent).
func TestCompactAuditLog(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()

	now := utcNow()
	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, 'd', null, null, 1, ?, ?, 0)`, "compact", "Compact", now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := srv.writeExec(ctx, `update fests set title=? where id=?`, "Compact2", id); err != nil {
		t.Fatal(err)
	}

	// Force the rows into legacy TEXT form so the compaction has real work to do.
	if _, err := db.Exec(`update audit_log set before_json=dope_unz(before_json), after_json=dope_unz(after_json)`); err != nil {
		t.Fatal(err)
	}

	want := decodedAuditContent(t, db)
	if len(want) == 0 {
		t.Fatal("expected some audit rows")
	}

	n, err := compactAuditLog(db, 1, nil)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n == 0 {
		t.Fatal("expected rows to be compressed")
	}

	// Content unchanged after compaction, and nothing left as uncompressed TEXT.
	assertAuditContent(t, db, want)
	var textLeft int
	if err := db.QueryRow(`select count(*) from audit_log where typeof(before_json)='text' or typeof(after_json)='text'`).Scan(&textLeft); err != nil {
		t.Fatal(err)
	}
	if textLeft != 0 {
		t.Fatalf("%d rows still uncompressed TEXT after compaction", textLeft)
	}

	// Idempotent: second run compresses nothing, content still intact.
	n2, err := compactAuditLog(db, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second compaction should be a no-op, compressed %d", n2)
	}
	assertAuditContent(t, db, want)
}

func decodedAuditContent(t *testing.T, db *sql.DB) map[int64][2]string {
	t.Helper()
	out := map[int64][2]string{}
	rows, err := db.Query(`select id, coalesce(dope_unz(before_json),''), coalesce(dope_unz(after_json),'') from audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var b, a string
		if err := rows.Scan(&id, &b, &a); err != nil {
			t.Fatal(err)
		}
		out[id] = [2]string{b, a}
	}
	return out
}

func assertAuditContent(t *testing.T, db *sql.DB, want map[int64][2]string) {
	t.Helper()
	got := decodedAuditContent(t, db)
	if len(got) != len(want) {
		t.Fatalf("row count changed: got %d want %d", len(got), len(want))
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("row %d content changed:\n  before/after got=%v\n  want=%v", id, got[id], w)
		}
	}
}
