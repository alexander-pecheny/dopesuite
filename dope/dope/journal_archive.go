package dopeserver

import (
	"context"
	"database/sql"
	"log"
	"time"

	"dope/dope/journal"
)

// The hot->cold journal folding lives in the leaf package dope/journal; this
// file keeps only the server-side scheduler that drives it under the write lock.
// These thin wrappers keep the existing test call sites terse.
func archiveFestJournal(ctx context.Context, db *sql.DB, festID, throughSeq int64) (int, error) {
	return journal.ArchiveFest(ctx, db, festID, throughSeq)
}

func archiveStaleJournals(ctx context.Context, db *sql.DB) (int, error) {
	return journal.ArchiveStale(ctx, db)
}

// initJournalArchive launches the periodic hot->cold folding. Call once from
// Main() after the schema is installed. The archiver holds the global write
// lock only for the short insert/delete transaction (compression runs off-lock).
func (s *server) initJournalArchive() {
	if s.db == nil {
		return
	}
	go s.runJournalArchive(journal.ArchiveInterval)
}

func (s *server) runJournalArchive(interval time.Duration) {
	// First pass shortly after boot, then settle onto the steady cadence.
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	for range timer.C {
		// Bound the archive's DB work with writeTxTimeout so this background pass
		// can never pin the global write lock indefinitely (e.g. on a starved
		// connection pool) and freeze live edits — the 2026-06-13 failure mode.
		ctx, cancel := context.WithTimeout(context.Background(), writeTxTimeout)
		s.mu.Lock()
		n, err := journal.ArchiveStale(ctx, s.db)
		s.mu.Unlock()
		cancel()
		if err != nil {
			log.Printf("journal archive: %v", err)
		} else if n > 0 {
			log.Printf("journal archive: folded %d hot rows into cold segments", n)
		}
		timer.Reset(interval)
	}
}
