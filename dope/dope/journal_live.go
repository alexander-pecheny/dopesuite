package dopeserver

import (
	"context"
	"database/sql"
	"encoding/json"

	"dope/dope/journal"
)

// The live write path into the unified journal lives in the leaf package
// dope/journal (opcodes, append, viewer-event replay). This file keeps the
// server-side helpers that read attribution from the request's audit context
// and bridge those calls into the journal leaf.

// canonicalJSON re-marshals JSON so semantically-equal documents have identical
// bytes (sorted object keys, normalized numbers/whitespace). Used so the
// wholesale game-state PUT path stores the same representation as the PATCH
// path, which already round-trips through encoding/json.
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, err
	}
	return json.Marshal(v)
}

// journalEventsSince returns every viewer event for a fest after sinceSeq,
// reading hot rows and cold segments via the journal leaf.
func (s *server) journalEventsSince(ctx context.Context, festID, sinceSeq int64) ([]journal.LiveEvent, error) {
	return journal.EventsSince(ctx, s.db, festID, sinceSeq)
}

// appendJournalTx writes one edit to the unified journal, reading the actor /
// request / game attribution from the audit context and delegating the insert
// to the journal leaf. Called in the same transaction as the mutation.
func appendJournalTx(ctx context.Context, tx *sql.Tx, festID, seq int64, eventType string, payload []byte) error {
	actorID, _ := actorFromContext(ctx)
	requestID := requestIDFromContext(ctx)
	gameID, _ := gameIDFromContext(ctx)
	return journal.AppendTx(ctx, tx, festID, seq, eventType, payload, actorID, requestID, gameID, utcNow())
}
