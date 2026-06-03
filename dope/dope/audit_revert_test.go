package main

import (
	"context"
	"strconv"
	"testing"
)

// TestRevertFestToAudit verifies that reverse-applying fest-scoped audit rows
// restores the prior column values, leaves rows from other fests untouched, and
// itself records audit rows (so a revert is undoable).
func TestRevertFestToAudit(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()

	insertFest := func(title string) int64 {
		res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, ?, '', null, null, 1, ?, ?, 0)`, title, now, now)
		if err != nil {
			t.Fatalf("insert fest %q: %v", title, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId: %v", err)
		}
		return id
	}

	festID := insertFest("Original")
	otherFest := insertFest("Other")

	// Baseline: state we want to return to. All later edits are stamped with the
	// fest so the revert is scoped to them.
	var target int64
	if err := db.QueryRow(`select coalesce(max(id), 0) from audit_log`).Scan(&target); err != nil {
		t.Fatalf("max audit id: %v", err)
	}

	ctxF := withAuditFestID(withAuditActor(ctx, 0), festID)
	if _, err := srv.writeExec(ctxF, `update fests set title = ?, updated_at = ? where id = ?`, "Changed1", now, festID); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	if _, err := srv.writeExec(ctxF, `update fests set title = ?, updated_at = ? where id = ?`, "Changed2", now, festID); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	// An edit to a different fest that must NOT be reverted.
	ctxOther := withAuditFestID(ctx, otherFest)
	if _, err := srv.writeExec(ctxOther, `update fests set title = ?, updated_at = ? where id = ?`, "OtherChanged", now, otherFest); err != nil {
		t.Fatalf("update other: %v", err)
	}

	count, revision, err := srv.revertFestToAudit(ctxF, festID, target)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if count != 2 {
		t.Fatalf("reversed = %d, want 2 (the two fest edits, other fest excluded)", count)
	}
	if revision == 0 {
		t.Fatalf("expected a non-zero fest revision after revert")
	}

	var title string
	if err := db.QueryRow(`select title from fests where id = ?`, festID).Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	if title != "Original" {
		t.Fatalf("title = %q, want Original", title)
	}

	var otherTitle string
	if err := db.QueryRow(`select title from fests where id = ?`, otherFest).Scan(&otherTitle); err != nil {
		t.Fatalf("read other title: %v", err)
	}
	if otherTitle != "OtherChanged" {
		t.Fatalf("other fest title = %q, want OtherChanged (must not be reverted)", otherTitle)
	}

	// The reversal must itself be audited so it can be undone: two new UPDATE
	// rows on fests stamped with festID, newer than the revert target.
	var reversalRows int
	if err := db.QueryRow(
		`select count(*) from audit_log where fest_id = ? and table_name = 'fests' and op = 'UPDATE' and id > ?`,
		festID, target).Scan(&reversalRows); err != nil {
		t.Fatalf("count reversal rows: %v", err)
	}
	if reversalRows < 4 {
		t.Fatalf("expected the 2 edits + 2 reversal UPDATEs in audit_log, got %d", reversalRows)
	}
}

// TestRevertFestRecreatesDeletedRow verifies DELETE reversal re-inserts the row
// and INSERT reversal removes it.
func TestRevertFestRecreatesDeletedRow(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()

	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, 'Fest', '', null, null, 1, ?, ?, 0)`, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	festID, _ := res.LastInsertId()
	ctxF := withAuditFestID(ctx, festID)

	// Insert a venue, then capture the baseline (venue present), then delete it.
	if _, err := srv.writeExec(ctxF, `insert into venues(fest_id, number, title, created_at, updated_at) values(?, 1, 'Зал A', ?, ?)`, festID, now, now); err != nil {
		t.Fatalf("insert venue: %v", err)
	}
	var target int64
	if err := db.QueryRow(`select max(id) from audit_log`).Scan(&target); err != nil {
		t.Fatalf("max id: %v", err)
	}
	if _, err := srv.writeExec(ctxF, `delete from venues where fest_id = ? and number = 1`, festID); err != nil {
		t.Fatalf("delete venue: %v", err)
	}

	if _, _, err := srv.revertFestToAudit(ctxF, festID, target); err != nil {
		t.Fatalf("revert: %v", err)
	}

	var title string
	if err := db.QueryRow(`select title from venues where fest_id = ? and number = 1`, festID).Scan(&title); err != nil {
		t.Fatalf("venue should be restored: %v", err)
	}
	if title != "Зал A" {
		t.Fatalf("restored venue title = %q, want Зал A", title)
	}
}

// TestAuditFestIDFromPath checks the middleware path → fest resolution that
// stamps audit rows, including the prefixes that must NOT resolve to a fest.
func TestAuditFestIDFromPath(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()
	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('myfest', 'My Fest', '', null, null, 1, ?, ?, 1)`, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	festID, _ := res.LastInsertId()
	idStr := strconv.FormatInt(festID, 10)

	cases := []struct {
		path string
		want int64
	}{
		{"/api/fest/" + idStr + "/games/3/state", festID},
		{"/host/fest/myfest/audit", festID},
		{"/host/fest/myfest/audit/revert", festID},
		{"/fest/myfest/game/3/stats", festID},
		{"/host/fest", 0},
		{"/host/fest/", 0},
		{"/host/fest/unknown-slug/audit", 0},
		{"/login", 0},
		{"/static/styles.css", 0},
	}
	for _, c := range cases {
		if got := srv.auditFestIDFromPath(ctx, c.path); got != c.want {
			t.Errorf("auditFestIDFromPath(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}
