package main

import (
	"context"
	"database/sql"
	"encoding/json"
)

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

// This file is the live write path into the unified journal table. Every
// mutation already funnels through bumpFestRevisionTx / bumpMatchRevisionTx with
// a semantic event type ("match:update", "game:state-patch", …) and a payload
// describing the change — that was previously dropped into the write-only
// events table. We now record it as a journal row: a stable opcode (so the type
// string isn't repeated as text on every row) plus the payload, which is both
// the durable edit content AND what is broadcast to viewers over SSE.

// Coarse live event-type opcodes — one per legacy event type. Stable forever
// (append-only); numbered from 64 to leave room below for fine semantic ops.
const (
	opEvImport             journalOp = 64
	opEvMatchUpdate        journalOp = 65
	opEvMatchVenue         journalOp = 66
	opEvVenuesUpdate       journalOp = 67
	opEvFestNumbers        journalOp = 68
	opEvAuditRevert        journalOp = 69
	opEvPlayerOverride     journalOp = 70
	opEvPlayerOverrideEdit journalOp = 71
	opEvGameState          journalOp = 72
	opEvGameStatePatch     journalOp = 73
	opEvFestAccess         journalOp = 74
	opEvGameDelete         journalOp = 75
	opEvGameClear          journalOp = 76
	opEvGameCreate         journalOp = 77
	opEvRatingImport       journalOp = 78
	opEvReseedCalculate    journalOp = 79
	opEvSeedImportKSI      journalOp = 80
	opEvSeedImportDecline  journalOp = 81
	opEvGameRevert         journalOp = 82

	// opEvGeneric carries an unmapped event type; its payload is prefixed with
	// the type string (see encodeGenericPayload) so the type survives.
	opEvGeneric journalOp = 127
)

var eventTypeToOp = map[string]journalOp{
	"import":                      opEvImport,
	"match:update":                opEvMatchUpdate,
	"match:venue":                 opEvMatchVenue,
	"venues:update":               opEvVenuesUpdate,
	"fest:numbers":                opEvFestNumbers,
	"audit:revert":                opEvAuditRevert,
	"roster:player-override":      opEvPlayerOverride,
	"roster:player-override-edit": opEvPlayerOverrideEdit,
	"game:state":                  opEvGameState,
	"game:state-patch":            opEvGameStatePatch,
	"fest:access":                 opEvFestAccess,
	"game:delete":                 opEvGameDelete,
	"game:clear":                  opEvGameClear,
	"game:create":                 opEvGameCreate,
	"rating:roster-import":        opEvRatingImport,
	"reseed:calculate":            opEvReseedCalculate,
	"seed-import:ksi":             opEvSeedImportKSI,
	"seed-import:decline":         opEvSeedImportDecline,
	"game:revert":                 opEvGameRevert,
}

var opToEventType = func() map[journalOp]string {
	m := make(map[journalOp]string, len(eventTypeToOp))
	for k, v := range eventTypeToOp {
		m[v] = k
	}
	return m
}()

func opForEventType(t string) journalOp {
	if op, ok := eventTypeToOp[t]; ok {
		return op
	}
	return opEvGeneric
}

// eventTypeForOp recovers the SSE event-type string for an opcode (for the
// resync/broadcast path and the history UI). Returns "" for non-event opcodes.
func eventTypeForOp(op journalOp) string {
	return opToEventType[op]
}

// encodeGenericPayload prefixes an unmapped event type onto its payload so a
// generic journal row stays self-describing.
func encodeGenericPayload(eventType string, payload []byte) []byte {
	var w byteWriter
	w.bytes([]byte(eventType))
	w.buf = append(w.buf, payload...)
	return w.buf
}

func decodeGenericPayload(b []byte) (eventType string, payload []byte, err error) {
	r := byteReader{buf: b}
	t, err := r.readBytes()
	if err != nil {
		return "", nil, err
	}
	return string(t), r.buf[r.pos:], nil
}

// journalEvent is one decoded edit ready to be replayed to a viewer: the SSE
// event type, the fest-scoped seq, and the payload that was broadcast.
type journalEvent struct {
	Seq       int64
	EventType string
	Payload   []byte
}

// journalEventsSince returns every edit for a fest after sinceSeq, in order,
// reading from BOTH the hot rows and any cold segments. This is what makes the
// journal the source of viewer events: a client that missed updates can be
// caught up by replaying these instead of refetching full state.
func (s *server) journalEventsSince(ctx context.Context, festID, sinceSeq int64) ([]journalEvent, error) {
	// Only semantic event ops are viewer events; row-ops (the replay/revert
	// backbone) are internal. eventOp reports whether an op is a viewer event.
	eventOp := func(op journalOp) bool { return op >= opEvImport }

	var out []journalEvent
	add := func(seq int64, op journalOp, payload []byte) {
		if seq <= sinceSeq || !eventOp(op) {
			return
		}
		et := eventTypeForOp(op)
		if op == opEvGeneric {
			if t, p, err := decodeGenericPayload(payload); err == nil {
				et, payload = t, p
			}
		}
		out = append(out, journalEvent{Seq: seq, EventType: et, Payload: payload})
	}

	// Cold segments that may contain seq > sinceSeq.
	segRows, err := s.db.QueryContext(ctx,
		`select blob from journal_segment where fest_id = ? and seq_end > ? order by seq_start`, festID, sinceSeq)
	if err != nil {
		return nil, err
	}
	for segRows.Next() {
		var blob []byte
		if err := segRows.Scan(&blob); err != nil {
			segRows.Close()
			return nil, err
		}
		raw, err := zstdDecompress(blob)
		if err != nil {
			segRows.Close()
			return nil, err
		}
		recs, err := decodeSegment(raw)
		if err != nil {
			segRows.Close()
			return nil, err
		}
		for _, rec := range recs {
			add(int64(rec.Seq), rec.Op, rec.Args)
		}
	}
	segRows.Close()
	if err := segRows.Err(); err != nil {
		return nil, err
	}

	// Hot rows.
	hotRows, err := s.db.QueryContext(ctx,
		`select seq, op, payload from journal where fest_id = ? and seq > ? order by seq`, festID, sinceSeq)
	if err != nil {
		return nil, err
	}
	defer hotRows.Close()
	for hotRows.Next() {
		var seq int64
		var op int
		var payload []byte
		if err := hotRows.Scan(&seq, &op, &payload); err != nil {
			return nil, err
		}
		add(seq, journalOp(op), payload)
	}
	return out, hotRows.Err()
}

// appendJournalTx writes one edit to the unified journal. seq is the fest
// revision assigned to this edit; actor and request id are read from the audit
// context so the log attributes every edit. Called in the same transaction as
// the mutation, so the durable log and the state change commit atomically.
func appendJournalTx(ctx context.Context, tx *sql.Tx, festID, seq int64, eventType string, payload []byte) error {
	op := opForEventType(eventType)
	if op == opEvGeneric {
		payload = encodeGenericPayload(eventType, payload)
	}
	var actor sql.NullInt64
	if a, ok := actorFromContext(ctx); ok {
		actor = sql.NullInt64{Int64: a, Valid: true}
	}
	var req sql.NullString
	if r := requestIDFromContext(ctx); r != "" {
		req = sql.NullString{String: r, Valid: true}
	}
	now := utcNow()
	_, err := tx.ExecContext(ctx, `
insert into journal(fest_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
		festID, seq, now, actor, req, int(op), payload, now)
	return err
}
