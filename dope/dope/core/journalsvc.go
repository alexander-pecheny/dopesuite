package core

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"dope/dope/festwrite"
	"dope/dope/journal"
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
		ctx, cancel := context.WithTimeout(context.Background(), festwrite.WriteTxTimeout)
		e.Mu.Lock()
		n, err := journal.ArchiveStale(ctx, e.DB)
		e.Mu.Unlock()
		cancel()
		if err != nil {
			log.Printf("journal archive: %v", err)
		} else if n > 0 {
			log.Printf("journal archive: folded %d hot rows into cold segments", n)
		}
		timer.Reset(interval)
	}
}
