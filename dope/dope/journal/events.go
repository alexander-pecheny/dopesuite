package journal

import (
	"context"
	"database/sql"

	"dope/dope/store"
)

// The live write path: every mutation funnels through a semantic event type
// ("match:update", "game:state-patch", …) with a payload describing the change.
// Each is recorded as a journal row — a stable opcode (so the type string isn't
// repeated as text on every row) plus the payload, which is both the durable
// edit content AND what is broadcast to viewers over SSE.

// Coarse live event-type opcodes — one per legacy event type. Stable forever
// (append-only); numbered from 64 to leave room below for fine semantic ops.
const (
	OpEvImport             Op = 64
	OpEvMatchUpdate        Op = 65
	OpEvMatchVenue         Op = 66
	OpEvVenuesUpdate       Op = 67
	OpEvFestNumbers        Op = 68
	OpEvAuditRevert        Op = 69
	OpEvPlayerOverride     Op = 70
	OpEvPlayerOverrideEdit Op = 71
	OpEvGameState          Op = 72
	OpEvGameStatePatch     Op = 73
	OpEvFestAccess         Op = 74
	OpEvGameDelete         Op = 75
	OpEvGameClear          Op = 76
	OpEvGameCreate         Op = 77
	OpEvRatingImport       Op = 78
	OpEvReseedCalculate    Op = 79
	OpEvSeedImportKSI      Op = 80
	OpEvSeedImportDecline  Op = 81
	OpEvGameRevert         Op = 82

	// OpEvGeneric carries an unmapped event type; its payload is prefixed with
	// the type string (see EncodeGenericPayload) so the type survives.
	OpEvGeneric Op = 127
)

var eventTypeToOp = map[string]Op{
	"import":                      OpEvImport,
	"match:update":                OpEvMatchUpdate,
	"match:venue":                 OpEvMatchVenue,
	"venues:update":               OpEvVenuesUpdate,
	"fest:numbers":                OpEvFestNumbers,
	"audit:revert":                OpEvAuditRevert,
	"roster:player-override":      OpEvPlayerOverride,
	"roster:player-override-edit": OpEvPlayerOverrideEdit,
	"game:state":                  OpEvGameState,
	"game:state-patch":            OpEvGameStatePatch,
	"fest:access":                 OpEvFestAccess,
	"game:delete":                 OpEvGameDelete,
	"game:clear":                  OpEvGameClear,
	"game:create":                 OpEvGameCreate,
	"rating:roster-import":        OpEvRatingImport,
	"reseed:calculate":            OpEvReseedCalculate,
	"seed-import:ksi":             OpEvSeedImportKSI,
	"seed-import:decline":         OpEvSeedImportDecline,
	"game:revert":                 OpEvGameRevert,
}

var opToEventType = func() map[Op]string {
	m := make(map[Op]string, len(eventTypeToOp))
	for k, v := range eventTypeToOp {
		m[v] = k
	}
	return m
}()

// OpForEventType maps a semantic event type to its opcode (OpEvGeneric if none).
func OpForEventType(t string) Op {
	if op, ok := eventTypeToOp[t]; ok {
		return op
	}
	return OpEvGeneric
}

// EventTypeForOp recovers the SSE event-type string for an opcode (for the
// resync/broadcast path and the history UI); "" for non-event opcodes.
func EventTypeForOp(op Op) string {
	return opToEventType[op]
}

// IsEventOp reports whether op is a viewer/event opcode (vs an internal row-op).
func IsEventOp(op Op) bool { return op >= OpEvImport }

// LiveEvent is one decoded edit ready to be replayed to a viewer: the SSE event
// type, the fest-scoped seq, and the payload that was broadcast.
type LiveEvent struct {
	Seq       int64
	EventType string
	Payload   []byte
}

// AppendTx writes one edit to the unified journal in the mutation's own
// transaction, so the durable log and the state change commit atomically.
// Attribution (actor/request/game; 0 / "" = none) is supplied by the caller,
// which reads it from the request's audit context.
func AppendTx(ctx context.Context, tx *sql.Tx, festID, seq int64, eventType string, payload []byte, actorID int64, requestID string, gameID int64, now string) error {
	op := OpForEventType(eventType)
	if op == OpEvGeneric {
		payload = EncodeGenericPayload(eventType, payload)
	}
	var actor sql.NullInt64
	if actorID != 0 {
		actor = sql.NullInt64{Int64: actorID, Valid: true}
	}
	var req sql.NullString
	if requestID != "" {
		req = sql.NullString{String: requestID, Valid: true}
	}
	var game sql.NullInt64
	if gameID > 0 {
		game = sql.NullInt64{Int64: gameID, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `
insert into journal(fest_id, game_id, seq, ts, actor_user_id, request_id, op, payload, created_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		festID, game, seq, now, actor, req, int(op), payload, now)
	return err
}

// EventsSince returns every viewer event for a fest after sinceSeq, in order,
// reading from BOTH the hot rows and any cold segments — the source for
// catching up a client that missed updates without refetching full state.
func EventsSince(ctx context.Context, q store.Queryer, festID, sinceSeq int64) ([]LiveEvent, error) {
	var out []LiveEvent
	add := func(seq int64, op Op, payload []byte) {
		if seq <= sinceSeq || !IsEventOp(op) {
			return
		}
		et := EventTypeForOp(op)
		if op == OpEvGeneric {
			if t, p, err := DecodeGenericPayload(payload); err == nil {
				et, payload = t, p
			}
		}
		out = append(out, LiveEvent{Seq: seq, EventType: et, Payload: payload})
	}

	// Cold segments that may contain seq > sinceSeq.
	segRows, err := q.QueryContext(ctx,
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
		raw, err := Decompress(blob)
		if err != nil {
			segRows.Close()
			return nil, err
		}
		recs, err := DecodeSegment(raw)
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
	hotRows, err := q.QueryContext(ctx,
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
		add(seq, Op(op), payload)
	}
	return out, hotRows.Err()
}
