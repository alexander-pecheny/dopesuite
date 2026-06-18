package dopeserver

import (
	"database/sql"

	"dope/dope/journal"
)

// The journal row-op triggers live in the leaf package dope/journal; this thin
// wrapper keeps the migration call site terse.
func ensureJournalTriggers(db *sql.DB) error { return journal.EnsureTriggers(db) }
