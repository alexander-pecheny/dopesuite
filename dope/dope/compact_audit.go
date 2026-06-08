package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"time"
)

// runCompactAudit is a one-time maintenance subcommand: it opens the fest DB
// (which on the new binary migrates the schema — drops the dead indexes, adds
// audit_ctx.suppress, rebuilds the v4 triggers), then zstd-compresses every
// existing uncompressed audit_log snapshot and VACUUMs to physically reclaim the
// freed pages.
//
// Run with the server STOPPED (it must be the sole DB writer):
//
//	dope-server compact-audit --db /var/lib/dope/fest.db
//
// Idempotent: already-compressed rows are stored as BLOBs, so the typeof('text')
// guard skips them — safe to re-run if interrupted. Column-diffing is left to the
// triggers for new writes; existing full-row snapshots just get compressed (they
// age out within the retention window regardless).
func runCompactAudit(args []string) {
	fs := flag.NewFlagSet("compact-audit", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the sqlite database")
	batch := fs.Int64("batch", 5000, "rows per update batch")
	_ = fs.Parse(args)
	if *dbPath == "" {
		log.Fatal("compact-audit: --db is required")
	}

	db, err := openFestDB(*dbPath)
	if err != nil {
		log.Fatalf("compact-audit: open db: %v", err)
	}
	defer db.Close()

	start := time.Now()
	compressed, err := compactAuditLog(db, *batch, func(pct int, scanned, done int64) {
		log.Printf("compact-audit: %3d%%  scanned<=%d  compressed=%d", pct, scanned, done)
	})
	if err != nil {
		log.Fatalf("compact-audit: %v", err)
	}
	log.Printf("compact-audit: done — compressed %d rows in %s", compressed, time.Since(start).Round(time.Second))
}

// compactAuditLog zstd-compresses every uncompressed audit_log snapshot in id
// batches, then VACUUMs. progress (may be nil) is called once per batch. Returns
// the number of rows compressed. Idempotent: already-compressed rows are BLOBs,
// so the typeof('text') guard skips them.
func compactAuditLog(db *sql.DB, batch int64, progress func(pct int, scanned, done int64)) (int64, error) {
	if batch <= 0 {
		batch = 5000
	}
	var maxID int64
	if err := db.QueryRow(`select coalesce(max(id),0) from audit_log`).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("max id: %w", err)
	}
	var compressed int64
	for lo := int64(1); lo <= maxID; lo += batch {
		hi := lo + batch - 1
		// dope_z(null) is null, so INSERT/DELETE rows keep their null side.
		res, err := db.Exec(`
update audit_log
set before_json = dope_z(before_json),
    after_json  = dope_z(after_json)
where id between ? and ?
  and (typeof(before_json) = 'text' or typeof(after_json) = 'text')`, lo, hi)
		if err != nil {
			return compressed, fmt.Errorf("batch [%d,%d]: %w", lo, hi, err)
		}
		n, _ := res.RowsAffected()
		compressed += n
		if progress != nil {
			pct := int(hi * 100 / maxID)
			if pct > 100 {
				pct = 100
			}
			progress(pct, hi, compressed)
		}
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return compressed, fmt.Errorf("vacuum: %w", err)
	}
	return compressed, nil
}
