// Package festwrite is the audited, journaled write layer shared by every fest
// mutation path. It owns:
//
//   - the audit-attribution context (actor / request-id / fest-id / game-id
//     stamped on a context and read back when a row is journaled),
//   - the write-transaction timeout and the detached/bounded contexts that keep
//     a write from pinning the global write mutex,
//   - the forward-journal append facade (AppendJournalTx) and the fest-revision
//     bump that records a semantic event (BumpFestRevisionTx),
//   - the audit-suppression toggle (SuppressAuditTx).
//
// It depends only on the journal/store/util leaves and the stdlib — no server
// coupling — so the server and the handler packages share one definition.
package festwrite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"dope/dope/platform/util"
	"dope/dope/storage/journal"
)

type auditCtxKey string

const (
	auditCtxKeyActor     auditCtxKey = "audit.actor_user_id"
	auditCtxKeyRequestID auditCtxKey = "audit.request_id"
	auditCtxKeyFestID    auditCtxKey = "audit.fest_id"
	auditCtxKeyGameID    auditCtxKey = "audit.game_id"
)

// WriteTxTimeout bounds how long a write transaction may run while holding the
// global write mutex. A healthy commit here is sub-millisecond (synchronous=FULL
// measured ~0.8 ms) and edits peak at ~10/s, so 5 s is a generous ceiling for any
// legitimate write. Its real job is a safety valve: it caps the one operation that
// can otherwise block unbounded — waiting for a connection from the shared pool —
// so a starved pool can never pin s.mu and freeze the whole site (the write fails
// and releases the lock instead). On 2026-06-13 a single match-update write hung
// ~55 min on exactly this, jamming every other write behind it.
const WriteTxTimeout = 5 * time.Second

func WithAuditGameID(ctx context.Context, gameID int64) context.Context {
	if gameID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyGameID, gameID)
}

func GameIDFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyGameID).(int64); ok && v > 0 {
		return v, true
	}
	return 0, false
}

// AuditGameIDFromPath extracts the numeric game id from a scoped API path like
// /api/fest/{tid}/games/{gid}/... (the live edit endpoints), so semantic event
// ops can be attributed to their game for the per-game history view.
func AuditGameIDFromPath(path string) int64 {
	const marker = "/games/"
	i := strings.Index(path, marker)
	if i < 0 {
		return 0
	}
	rest := path[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		rest = rest[:j]
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func WithAuditActor(ctx context.Context, userID int64) context.Context {
	if userID == 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyActor, userID)
}

func WithAuditRequestID(ctx context.Context, reqID string) context.Context {
	if reqID == "" {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyRequestID, reqID)
}

// WithAuditFestID stamps the fest a mutation belongs to so audit_log rows carry
// fest_id, which the admin revert page uses to scope a roll-back to one fest.
func WithAuditFestID(ctx context.Context, festID int64) context.Context {
	if festID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyFestID, festID)
}

// AuditDetachedContext returns a context (with its CancelFunc — the caller MUST
// defer cancel()) carrying the audit attribution (actor + request_id) copied from
// src, with fest_id stamped from the explicit festID argument (falling back to
// whatever src carried). Write helpers that run under the global write mutex use
// this instead of the request context so their mutations stay attributed to the
// acting user/request/fest in audit_log and are not aborted by a client
// disconnect mid-write — but, unlike a bare context.Background(), it carries a
// WriteTxTimeout deadline so the write can never hold s.mu indefinitely. Without
// the attribution, those rows get a null fest_id and never appear on the
// fest-scoped revert/audit page.
func AuditDetachedContext(src context.Context, festID int64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), WriteTxTimeout)
	if v, ok := ActorFromContext(src); ok {
		ctx = WithAuditActor(ctx, v)
	}
	if v := RequestIDFromContext(src); v != "" {
		ctx = WithAuditRequestID(ctx, v)
	}
	if festID > 0 {
		ctx = WithAuditFestID(ctx, festID)
	} else if v, ok := AuditFestIDFromContext(src); ok {
		ctx = WithAuditFestID(ctx, v)
	}
	if v, ok := GameIDFromContext(src); ok {
		ctx = WithAuditGameID(ctx, v)
	}
	return ctx, cancel
}

// BoundedReadContext bounds a DB read with WriteTxTimeout so it can never hang
// indefinitely waiting for a pooled connection. It matters most for the
// post-commit view reloads in the *Locked loaders, which run while holding s.mu:
// an unbounded wait there would re-create the 2026-06-13 lock-pinning freeze on
// the read side. Harmless on the off-lock snapshot paths that share those
// readers. Caller MUST defer cancel().
func BoundedReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), WriteTxTimeout)
}

func ActorFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyActor).(int64); ok && v != 0 {
		return v, true
	}
	return 0, false
}

func AuditFestIDFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyFestID).(int64); ok && v > 0 {
		return v, true
	}
	return 0, false
}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(auditCtxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// NewRequestID returns a short opaque token used to group all mutations from
// a single HTTP request in audit_log.
func NewRequestID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// SuppressAuditTx disables audit-row emission for the remainder of this tx.
func SuppressAuditTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `update audit_ctx set suppress = 1 where id = 1`)
	return err
}

// AppendJournalTx appends a forward-journal record for a fest mutation, pulling
// actor/request/game attribution from the context.
func AppendJournalTx(ctx context.Context, tx *sql.Tx, festID, seq int64, eventType string, payload []byte) error {
	actorID, _ := ActorFromContext(ctx)
	requestID := RequestIDFromContext(ctx)
	gameID, _ := GameIDFromContext(ctx)
	return journal.AppendTx(ctx, tx, festID, seq, eventType, payload, actorID, requestID, gameID, util.UtcNow())
}

// BumpFestRevisionTx increments a fest's revision, records a semantic journal
// event, and returns the new revision.
func BumpFestRevisionTx(ctx context.Context, tx *sql.Tx, festID int64, eventType, payload string) (int64, error) {
	now := util.UtcNow()
	if _, err := tx.ExecContext(ctx, `update fests set revision = revision + 1, updated_at = ? where id = ?`, now, festID); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `select revision from fests where id = ?`, festID).Scan(&revision); err != nil {
		return 0, err
	}
	if err := AppendJournalTx(ctx, tx, festID, revision, eventType, []byte(payload)); err != nil {
		return 0, err
	}
	return revision, nil
}

// SeedAuditCtx writes the request's audit attribution (actor/request/fest) into
// the per-connection audit_ctx row so the AFTER triggers stamp it onto audit_log.
// Call at the start of every write transaction.
func SeedAuditCtx(ctx context.Context, tx *sql.Tx) error {
	var actor any
	if v, ok := ActorFromContext(ctx); ok {
		actor = v
	}
	var reqID any
	if v := RequestIDFromContext(ctx); v != "" {
		reqID = v
	}
	var festID any
	if v, ok := AuditFestIDFromContext(ctx); ok {
		festID = v
	}
	_, err := tx.ExecContext(ctx,
		`insert or replace into audit_ctx(id, actor_user_id, request_id, fest_id) values(1, ?, ?, ?)`,
		actor, reqID, festID)
	return err
}
