package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"dope/dope/storage/journal"
)

// The live write path into the unified journal lives in the leaf package
// dope/journal; these are the engine-side helpers that bridge into it and run the
// background hot->cold archiver.

// CanonicalJSON re-marshals JSON so semantically-equal documents have identical
// bytes (sorted object keys, normalized numbers/whitespace).
func CanonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, err
	}
	return json.Marshal(v)
}

// JournalEventsSince returns every viewer event for a fest after sinceSeq.
func (e *Engine) JournalEventsSince(ctx context.Context, festID, sinceSeq int64) ([]journal.LiveEvent, error) {
	return journal.EventsSince(ctx, e.DB, festID, sinceSeq)
}

// InitJournalArchive launches the periodic hot->cold folding. Call once from
// Main() after the schema is installed.
func (e *Engine) InitJournalArchive() {
	if e.DB == nil {
		return
	}
	go e.runJournalArchive(journal.ArchiveInterval)
}

func (e *Engine) runJournalArchive(interval time.Duration) {
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	for range timer.C {
		if err := e.archiveIfQuiet(); err != nil {
			log.Printf("journal archive: %v", err)
		}
		timer.Reset(interval)
	}
}

// archiveIfQuiet folds settled hot rows into cold segments, but only when the
// whole system has been edit-free for journal.ArchiveQuiesce. Gating on global
// (not per-fest) quiescence is what lets the fold hold the write lock past the
// live 5s budget: if nothing has been edited anywhere for two hours, almost
// certainly nothing is mid-match, so a longer hold won't stall play. The
// quiescence check runs under the same lock as the fold so a write can't slip in
// between them; if not quiet it returns immediately, holding the lock only for
// the one cheap query.
func (e *Engine) archiveIfQuiet() error {
	ctx, cancel := context.WithTimeout(context.Background(), journal.ArchiveMaxHold)
	defer cancel()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	quiet, err := journal.GloballyQuiet(ctx, e.DB, journal.ArchiveQuiesce)
	if err != nil || !quiet {
		return err
	}
	n, err := journal.ArchiveStale(ctx, e.DB)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("journal archive: folded %d hot rows into cold segments", n)
	}
	return nil
}
