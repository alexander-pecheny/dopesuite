package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// dropAuditTriggers removes every audit_* trigger so the next workload runs
// without capture overhead. Used to measure the cost the audit layer adds.
func dropAuditTriggers(t testing.TB, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`select name from sqlite_master where type='trigger' and name like 'audit_%'`)
	if err != nil {
		t.Fatalf("list triggers: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		if _, err := db.Exec(`drop trigger ` + n); err != nil {
			t.Fatalf("drop %s: %v", n, err)
		}
	}
	// Also disable trigger-restoration on next openFestDB by burning the
	// fingerprint sentinel — only used when reusing the same handle.
	if _, err := db.Exec(`update audit_trigger_state set fingerprint = 'disabled' where id = 1`); err != nil {
		t.Fatalf("disable fingerprint: %v", err)
	}
}

// runFestWorkload performs a representative write pattern: N fest INSERTs +
// one UPDATE + one DELETE per fest, all in their own tx (so each pays the
// beginWriteTx + audit_ctx cost). Mirrors what the host UI does when editing
// individual fest rows.
func runFestWorkload(b *testing.B, db *sql.DB, n int) {
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()
	for i := 0; i < n; i++ {
		res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, null, 1, ?, ?, 0)`, slug(i), "T", now, now)
		if err != nil {
			b.Fatalf("insert: %v", err)
		}
		id, _ := res.LastInsertId()
		if _, err := srv.writeExec(ctx, `update fests set title = ?, updated_at = ? where id = ?`,
			"T2", now, id); err != nil {
			b.Fatalf("update: %v", err)
		}
		if _, err := srv.writeExec(ctx, `delete from fests where id = ?`, id); err != nil {
			b.Fatalf("delete: %v", err)
		}
	}
}

// runBulkImportWorkload mimics rating_import: one tx with many inserts into
// audited tables. This is where audit overhead matters most.
func runBulkImportWorkload(b *testing.B, db *sql.DB, festID int64, rowsPerTx int) {
	srv := auditTestServer(db)
	ctx := context.Background()
	tx, err := srv.beginWriteTx(ctx)
	if err != nil {
		b.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for i := 0; i < rowsPerTx; i++ {
		if _, err := tx.ExecContext(ctx, `
insert into fest_players(fest_id, rating_id, first_name, last_name)
values(?, null, ?, ?)`, festID, "F", "L"); err != nil {
			b.Fatalf("insert player: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit: %v", err)
	}
}

func slug(i int) string {
	const a = "abcdefghijklmnop"
	return "f-" + string(rune('a'+i%26)) + "-" + string(rune('a'+(i/26)%26)) + a[i%len(a):i%len(a)+1]
}

func BenchmarkAuditWritesEnabled(b *testing.B) {
	db, err := openFestDB(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 100 tx per iteration: keeps the bench loop in a realistic range.
		runFestWorkload(b, db, 100)
	}
}

func BenchmarkAuditWritesDisabled(b *testing.B) {
	db, err := openFestDB(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()
	dropAuditTriggers(b, db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runFestWorkload(b, db, 100)
	}
}

func BenchmarkAuditBulkImportEnabled(b *testing.B) {
	db, err := openFestDB(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()
	now := utcNow()
	srv := auditTestServer(db)
	res, err := srv.writeExec(context.Background(), `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('bulk','Bulk','',null,null,1,?,?,0)`, now, now)
	if err != nil {
		b.Fatalf("fest: %v", err)
	}
	festID, _ := res.LastInsertId()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runBulkImportWorkload(b, db, festID, 500)
	}
}

func BenchmarkAuditBulkImportDisabled(b *testing.B) {
	db, err := openFestDB(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()
	now := utcNow()
	srv := auditTestServer(db)
	res, err := srv.writeExec(context.Background(), `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('bulk','Bulk','',null,null,1,?,?,0)`, now, now)
	if err != nil {
		b.Fatalf("fest: %v", err)
	}
	festID, _ := res.LastInsertId()
	dropAuditTriggers(b, db)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runBulkImportWorkload(b, db, festID, 500)
	}
}
