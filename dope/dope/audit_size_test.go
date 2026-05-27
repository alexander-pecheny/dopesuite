package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// TestAuditLogSize is a one-shot diagnostic, not a regression check. It runs a
// representative workload and prints how much space audit_log consumes,
// broken down by op, so we can reason about whether storing diffs would
// meaningfully shrink it. Run with `go test -run TestAuditLogSize -v`.
func TestAuditLogSize(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "size.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()

	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('sz','Size','',null,null,1,?,?,0)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	festID, _ := res.LastInsertId()

	// 50 teams: inserts + a couple of UPDATEs each (rename, then number).
	tx, _ := srv.beginWriteTx(ctx)
	teamIDs := make([]int64, 50)
	for i := 0; i < 50; i++ {
		r, err := tx.ExecContext(ctx, `insert into fest_teams(fest_id, rating_id, name, city, position) values(?, null, ?, '', ?)`,
			festID, fmt.Sprintf("Team %d", i), float64(i))
		if err != nil {
			t.Fatal(err)
		}
		teamIDs[i], _ = r.LastInsertId()
	}
	tx.Commit()

	// Rename each team (UPDATE one column).
	for _, id := range teamIDs {
		if _, err := srv.writeExec(ctx, `update fest_teams set name = ? where id = ?`, "Renamed", id); err != nil {
			t.Fatal(err)
		}
	}
	// Assign number to each (UPDATE one column).
	for i, id := range teamIDs {
		if _, err := srv.writeExec(ctx, `update fest_teams set number = ? where id = ?`, i+1, id); err != nil {
			t.Fatal(err)
		}
	}
	// Delete half of them.
	for _, id := range teamIDs[:25] {
		if _, err := srv.writeExec(ctx, `delete from fest_teams where id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}

	// Measure.
	type bucket struct {
		op    string
		count int
		bytes int64
		beforeBytes int64
		afterBytes  int64
	}
	rows, _ := db.Query(`
select op,
       count(*),
       sum(coalesce(length(before_json),0) + coalesce(length(after_json),0)),
       sum(coalesce(length(before_json),0)),
       sum(coalesce(length(after_json),0))
from audit_log where table_name = 'fest_teams'
group by op order by op`)
	defer rows.Close()
	var totalCount int
	var totalBytes int64
	var totalChanged int64
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.op, &b.count, &b.bytes, &b.beforeBytes, &b.afterBytes); err != nil {
			t.Fatal(err)
		}
		t.Logf("%-7s count=%-4d total=%-7d  before=%-7d  after=%-7d  avg/row=%d",
			b.op, b.count, b.bytes, b.beforeBytes, b.afterBytes, b.bytes/int64(b.count))
		totalCount += b.count
		totalBytes += b.bytes

		// Estimate diff size: per UPDATE, only fields that actually changed
		// in our workload (name OR number, plus pk). The other columns
		// stay constant, so a diff capture would skip them.
		if b.op == "UPDATE" {
			// Each row's full JSON is ~120-150 bytes (8 cols). The diff is
			// roughly {"id":N,"name":"..."} or {"id":N,"number":N} ~30-40 bytes.
			// We measure after-by-counting fields here using a sample row.
			var sampleAfter string
			db.QueryRow(`select after_json from audit_log where table_name='fest_teams' and op='UPDATE' limit 1`).Scan(&sampleAfter)
			fullLen := int64(len(sampleAfter))
			// Approximate diff: just pk + 1 changed col (id + name "Renamed").
			diffLen := int64(len(`{"id":12345,"name":"Renamed"}`))
			savedPerRow := 2*(fullLen-diffLen)
			t.Logf("        UPDATE diff estimate: full=%d, diff=%d, save/row=%d, save/op-total=%d",
				fullLen, diffLen, savedPerRow, savedPerRow*int64(b.count))
			totalChanged += savedPerRow * int64(b.count)
		}
	}
	t.Logf("totals: count=%d, bytes=%d, est. diff savings=%d (~%.0f%% smaller)",
		totalCount, totalBytes, totalChanged, 100*float64(totalChanged)/float64(totalBytes))

	// Also print the on-disk page count via dbstat / pragma.
	var pages int64
	_ = db.QueryRow(`select count(*) from dbstat where name = 'audit_log'`).Scan(&pages)
	t.Logf("audit_log pages on disk: %d (pages × ~4KB)", pages)
}
