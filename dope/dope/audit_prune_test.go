package main

import (
	"context"
	"testing"
)

// TestPruneAuditLogByAge verifies the age bound drops rows past the retention
// horizon while keeping recent ones (and thus the ability to revert them).
func TestPruneAuditLogByAge(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()

	// Seed rows directly: ten "old" (30 days ago) and ten "fresh" (now). ts is
	// the only field the age bound reads.
	for i := 0; i < 10; i++ {
		if _, err := db.ExecContext(ctx, `
insert into audit_log(ts, table_name, row_pk, op)
values(strftime('%Y-%m-%dT%H:%M:%fZ','now','-30 days'), 'fests', ?, 'UPDATE')`, i); err != nil {
			t.Fatalf("insert old: %v", err)
		}
	}
	for i := 0; i < 10; i++ {
		if _, err := db.ExecContext(ctx, `
insert into audit_log(ts, table_name, row_pk, op)
values(strftime('%Y-%m-%dT%H:%M:%fZ','now'), 'fests', ?, 'UPDATE')`, i); err != nil {
			t.Fatalf("insert fresh: %v", err)
		}
	}

	if err := srv.pruneAuditLog(ctx, 7, 0); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var total, old int
	if err := db.QueryRow(`select count(*) from audit_log`).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if err := db.QueryRow(
		`select count(*) from audit_log where ts < strftime('%Y-%m-%dT%H:%M:%fZ','now','-7 days')`).Scan(&old); err != nil {
		t.Fatalf("count old: %v", err)
	}
	if old != 0 {
		t.Fatalf("%d rows older than 7 days survived the age prune", old)
	}
	if total != 10 {
		t.Fatalf("kept %d rows, want the 10 fresh ones", total)
	}
}

// TestPruneAuditLogBySize verifies the byte cap drops the oldest rows until
// audit_log is back under the cap, even when every row is inside the age window.
func TestPruneAuditLogBySize(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()

	// Write enough fat, fresh rows to comfortably exceed a small cap. Big JSON
	// blobs make the on-disk size grow fast without needing a huge row count.
	blob := make([]byte, 2048)
	for i := range blob {
		blob[i] = 'x'
	}
	for i := 0; i < 2000; i++ {
		if _, err := db.ExecContext(ctx, `
insert into audit_log(ts, table_name, row_pk, op, before_json, after_json)
values(strftime('%Y-%m-%dT%H:%M:%fZ','now'), 'fests', ?, 'UPDATE', ?, ?)`, i, string(blob), string(blob)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	before, rows, err := srv.auditLogSize(ctx)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if rows != 2000 {
		t.Fatalf("seeded %d rows, want 2000", rows)
	}

	// Cap at a quarter of the current size; age bound disabled so only size acts.
	cap := before / 4
	if err := srv.pruneAuditLog(ctx, 0, cap); err != nil {
		t.Fatalf("prune: %v", err)
	}

	after, remaining, err := srv.auditLogSize(ctx)
	if err != nil {
		t.Fatalf("size after: %v", err)
	}
	if after > cap {
		t.Fatalf("audit_log still %d bytes, over the %d cap", after, cap)
	}
	if remaining == 0 {
		t.Fatal("size prune deleted everything; should keep the newest rows")
	}
	// The survivors must be the newest rows: ids are 1..2000, so dropping the
	// oldest first means the smallest surviving id sits just past the dropped run.
	var minID, maxID int64
	_ = db.QueryRow(`select min(id), max(id) from audit_log`).Scan(&minID, &maxID)
	dropped := int64(2000) - remaining
	if minID != dropped+1 {
		t.Fatalf("oldest surviving id = %d, want %d (oldest %d rows should be dropped first)", minID, dropped+1, dropped)
	}
	if maxID != 2000 {
		t.Fatalf("newest row (id 2000) was dropped; size prune must keep the newest, got max id %d", maxID)
	}
}
