package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Audit-log retention. audit_log gains a few rows per edit and is otherwise
// never bounded, so without pruning it would eventually fill the disk on the
// small VPS and take the whole service down. Two independent bounds apply,
// whichever bites first:
//
//   - age: rows older than the retention window are dropped. This is the
//     revert/history horizon — an edit can be rolled back while it's inside the
//     window, not after.
//   - size: if the audit_log b-tree (+ its indexes) exceeds the byte cap, the
//     oldest rows are dropped until it's back under ~90% of the cap. A burst
//     (a large import, a revert storm) therefore can't blow past the disk
//     budget even when it's well inside the age window.
//
// Downsampling (keeping only every Nth old row) is deliberately NOT used: revert
// reverse-applies a contiguous chain of row deltas, and a chain with holes
// corrupts the rollback (e.g. dropping an INSERT but keeping its later UPDATE
// leaves an orphan row). So old history is dropped wholesale, never thinned.
//
// Deleted pages return to the freelist and are reused by new rows, so the DB
// file reaches a steady-state size and stops growing. It won't physically
// shrink below its high-water mark without a VACUUM, which we avoid here (it
// locks the DB and needs ~2× space — both bad on a 1-CPU box); bounding growth,
// not reclaiming, is the goal. audit_log has no AFTER triggers (it isn't itself
// audited), so these deletes are plain writes that don't recurse into auditing.
const (
	auditRetentionDaysDefault int64 = 7
	auditMaxBytesDefault      int64 = 1 << 30 // 1 GiB
	auditPruneIntervalDefault int64 = 3600    // seconds
	// auditPruneBatch caps how many rows one DELETE removes, so a large first-run
	// backlog is cleared over several short write transactions instead of one
	// long one that would hold the WAL writer lock and stall live edits.
	auditPruneBatch = 5000
)

// initAuditPrune launches the periodic retention sweep. Call once from main()
// after the schema is installed. Tunable via env: DOPE_AUDIT_RETENTION_DAYS,
// DOPE_AUDIT_MAX_BYTES, DOPE_AUDIT_PRUNE_INTERVAL (seconds). Set a bound to 0 to
// disable just that bound; disabling both skips the sweep entirely.
func (s *server) initAuditPrune() {
	if s.db == nil {
		return
	}
	retentionDays := envInt64("DOPE_AUDIT_RETENTION_DAYS", auditRetentionDaysDefault)
	maxBytes := envInt64("DOPE_AUDIT_MAX_BYTES", auditMaxBytesDefault)
	interval := time.Duration(envInt64("DOPE_AUDIT_PRUNE_INTERVAL", auditPruneIntervalDefault)) * time.Second
	if interval <= 0 {
		interval = time.Duration(auditPruneIntervalDefault) * time.Second
	}
	if retentionDays <= 0 && maxBytes <= 0 {
		return
	}
	go s.runAuditPrune(retentionDays, maxBytes, interval)
}

func (s *server) runAuditPrune(retentionDays, maxBytes int64, interval time.Duration) {
	// A first pass a minute after boot clears any backlog without waiting a full
	// interval; then it settles onto the steady cadence.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for range timer.C {
		if err := s.pruneAuditLog(context.Background(), retentionDays, maxBytes); err != nil {
			log.Printf("audit prune: %v", err)
		}
		timer.Reset(interval)
	}
}

// pruneAuditLog enforces the age and size bounds once. Safe to call repeatedly;
// it's a no-op when audit_log is already within both bounds.
func (s *server) pruneAuditLog(ctx context.Context, retentionDays, maxBytes int64) error {
	if s.db == nil {
		return nil
	}

	// 1) Age-based: drop everything past the retention horizon, batched so a big
	// first-run backlog doesn't hold the writer lock in one long delete. We resolve
	// the horizon to a cutoff id once (ts is monotonic with id) and then delete by
	// id — that keeps the deletes on the primary key and avoids needing an index on
	// ts. The single max(id) scan per cycle is cheap and held only briefly.
	if retentionDays > 0 {
		modifier := fmt.Sprintf("-%d days", retentionDays)
		var cutoff sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `
select max(id) from audit_log
where ts < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)`, modifier).Scan(&cutoff); err != nil {
			return fmt.Errorf("age prune cutoff: %w", err)
		}
		if cutoff.Valid {
			var removed int64
			for {
				res, err := s.db.ExecContext(ctx, `
delete from audit_log where id in (
  select id from audit_log where id <= ? order by id limit ?)`, cutoff.Int64, auditPruneBatch)
				if err != nil {
					return fmt.Errorf("age prune: %w", err)
				}
				n, _ := res.RowsAffected()
				removed += n
				if n < auditPruneBatch {
					break
				}
			}
			if removed > 0 {
				log.Printf("audit prune: removed %d rows older than %d days", removed, retentionDays)
			}
		}
	}

	// 2) Size-based safety net: if still over the byte cap, drop the oldest rows
	// down to ~90% of the cap (the margin avoids re-triggering every cycle).
	if maxBytes > 0 {
		bytes, rows, err := s.auditLogSize(ctx)
		if err != nil {
			return err
		}
		if bytes > maxBytes && rows > 0 {
			avg := bytes / rows
			if avg < 1 {
				avg = 1
			}
			toDelete := (bytes - maxBytes*9/10) / avg
			var removed int64
			for toDelete > 0 {
				batch := toDelete
				if batch > auditPruneBatch {
					batch = auditPruneBatch
				}
				res, err := s.db.ExecContext(ctx, `
delete from audit_log where id in (select id from audit_log order by id limit ?)`, batch)
				if err != nil {
					return fmt.Errorf("size prune: %w", err)
				}
				n, _ := res.RowsAffected()
				removed += n
				toDelete -= n
				if n == 0 {
					break
				}
			}
			if removed > 0 {
				log.Printf("audit prune: audit_log was %d bytes over the %d cap; removed %d oldest rows", bytes, maxBytes, removed)
			}
		}
	}
	return nil
}

// auditLogSize returns the on-disk byte size (table + its indexes) and the row
// count of audit_log. The byte count comes from the dbstat virtual table — page
// sizes, so it reflects freed pages immediately and needs no row-content reads.
func (s *server) auditLogSize(ctx context.Context) (bytes, rows int64, err error) {
	if err = s.db.QueryRowContext(ctx,
		`select coalesce(sum(pgsize), 0) from dbstat where name like 'audit_log%'`).Scan(&bytes); err != nil {
		return 0, 0, fmt.Errorf("audit size: %w", err)
	}
	if err = s.db.QueryRowContext(ctx, `select count(*) from audit_log`).Scan(&rows); err != nil {
		if err == sql.ErrNoRows {
			return bytes, 0, nil
		}
		return 0, 0, fmt.Errorf("audit rows: %w", err)
	}
	return bytes, rows, nil
}
