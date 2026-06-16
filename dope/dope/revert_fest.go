package main

import (
	"context"
	"flag"
	"log"
)

// runRevertFest is a maintenance subcommand that reverse-applies every
// fest-scoped audit_log row newer than --target (id > target), newest first,
// restoring the fest's audited tables to the state they had immediately after
// that audit row. It reuses the exact, tested revertFestToAudit path the host
// audit page uses (the reversal is itself audited, so it stays undoable).
//
// Run with the server STOPPED (it must be the sole DB writer):
//
//	dope-server revert-fest --db /var/lib/dope/fest.db --fest 6 --target 670353
//
// --dry-run reports how many rows would be reversed (and a per-table breakdown)
// without mutating anything.
func runRevertFest(args []string) {
	fs := flag.NewFlagSet("revert-fest", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to the sqlite database")
	festID := fs.Int64("fest", 0, "fest id to revert")
	target := fs.Int64("target", -1, "audit_log id to roll back to (reverts every fest row with id > target)")
	dryRun := fs.Bool("dry-run", false, "report what would be reverted without changing anything")
	_ = fs.Parse(args)
	if *dbPath == "" || *festID == 0 || *target < 0 {
		log.Fatal("revert-fest: --db, --fest and --target are required")
	}

	db, err := openFestDB(*dbPath)
	if err != nil {
		log.Fatalf("revert-fest: open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Always print the impact first so the operator can sanity-check the scope.
	var total int64
	if err := db.QueryRowContext(ctx,
		`select count(*) from audit_log where fest_id = ? and id > ?`, *festID, *target).Scan(&total); err != nil {
		log.Fatalf("revert-fest: count: %v", err)
	}
	log.Printf("revert-fest: fest=%d target=%d → %d audit rows in scope", *festID, *target, total)
	rows, err := db.QueryContext(ctx, `
select table_name, op, count(*) n
from audit_log where fest_id = ? and id > ?
group by table_name, op order by n desc`, *festID, *target)
	if err != nil {
		log.Fatalf("revert-fest: breakdown: %v", err)
	}
	for rows.Next() {
		var table, op string
		var n int64
		if err := rows.Scan(&table, &op, &n); err != nil {
			_ = rows.Close()
			log.Fatalf("revert-fest: scan breakdown: %v", err)
		}
		log.Printf("revert-fest:   %-16s %-6s %d", table, op, n)
	}
	_ = rows.Close()

	if *dryRun {
		log.Printf("revert-fest: dry-run, no changes made")
		return
	}

	srv := &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]bool),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}
	count, revision, err := srv.revertFestToAudit(ctx, *festID, *target)
	if err != nil {
		log.Fatalf("revert-fest: %v", err)
	}
	log.Printf("revert-fest: done — reversed %d rows, new fest revision %d", count, revision)
}
