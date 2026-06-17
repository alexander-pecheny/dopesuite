package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type auditCtxKey string

const (
	auditCtxKeyActor     auditCtxKey = "audit.actor_user_id"
	auditCtxKeyRequestID auditCtxKey = "audit.request_id"
	auditCtxKeyFestID    auditCtxKey = "audit.fest_id"
	auditCtxKeyGameID    auditCtxKey = "audit.game_id"
)

func withAuditGameID(ctx context.Context, gameID int64) context.Context {
	if gameID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyGameID, gameID)
}

func gameIDFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyGameID).(int64); ok && v > 0 {
		return v, true
	}
	return 0, false
}

// auditGameIDFromPath extracts the numeric game id from a scoped API path like
// /api/fest/{tid}/games/{gid}/... (the live edit endpoints), so semantic event
// ops can be attributed to their game for the per-game history view.
func auditGameIDFromPath(path string) int64 {
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

func withAuditActor(ctx context.Context, userID int64) context.Context {
	if userID == 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyActor, userID)
}

func withAuditRequestID(ctx context.Context, reqID string) context.Context {
	if reqID == "" {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyRequestID, reqID)
}

// withAuditFestID stamps the fest a mutation belongs to so audit_log rows carry
// fest_id, which the admin revert page uses to scope a roll-back to one fest.
func withAuditFestID(ctx context.Context, festID int64) context.Context {
	if festID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, auditCtxKeyFestID, festID)
}

// writeTxTimeout bounds how long a write transaction may run while holding the
// global write mutex. A healthy commit here is sub-millisecond (synchronous=FULL
// measured ~0.8 ms) and edits peak at ~10/s, so 5 s is a generous ceiling for any
// legitimate write. Its real job is a safety valve: it caps the one operation that
// can otherwise block unbounded — waiting for a connection from the shared pool —
// so a starved pool can never pin s.mu and freeze the whole site (the write fails
// and releases the lock instead). On 2026-06-13 a single match-update write hung
// ~55 min on exactly this, jamming every other write behind it.
const writeTxTimeout = 5 * time.Second

// auditDetachedContext returns a context (with its CancelFunc — the caller MUST
// defer cancel()) carrying the audit attribution (actor + request_id) copied from
// src, with fest_id stamped from the explicit festID argument (falling back to
// whatever src carried). Write helpers that run under the global write mutex use
// this instead of the request context so their mutations stay attributed to the
// acting user/request/fest in audit_log and are not aborted by a client
// disconnect mid-write — but, unlike a bare context.Background(), it carries a
// writeTxTimeout deadline so the write can never hold s.mu indefinitely. Without
// the attribution, those rows get a null fest_id and never appear on the
// fest-scoped revert/audit page.
func auditDetachedContext(src context.Context, festID int64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTxTimeout)
	if v, ok := actorFromContext(src); ok {
		ctx = withAuditActor(ctx, v)
	}
	if v := requestIDFromContext(src); v != "" {
		ctx = withAuditRequestID(ctx, v)
	}
	if festID > 0 {
		ctx = withAuditFestID(ctx, festID)
	} else if v, ok := auditFestIDFromContext(src); ok {
		ctx = withAuditFestID(ctx, v)
	}
	if v, ok := gameIDFromContext(src); ok {
		ctx = withAuditGameID(ctx, v)
	}
	return ctx, cancel
}

// boundedReadContext bounds a DB read with writeTxTimeout so it can never hang
// indefinitely waiting for a pooled connection. It matters most for the
// post-commit view reloads in the *Locked loaders, which run while holding s.mu:
// an unbounded wait there would re-create the 2026-06-13 lock-pinning freeze on
// the read side. Harmless on the off-lock snapshot paths that share those
// readers. Caller MUST defer cancel().
func boundedReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), writeTxTimeout)
}

func actorFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyActor).(int64); ok && v != 0 {
		return v, true
	}
	return 0, false
}

func auditFestIDFromContext(ctx context.Context) (int64, bool) {
	if v, ok := ctx.Value(auditCtxKeyFestID).(int64); ok && v > 0 {
		return v, true
	}
	return 0, false
}

func requestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(auditCtxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// newRequestID returns a short opaque token used to group all mutations from
// a single HTTP request in audit_log.
func newRequestID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// auditContextMiddleware stamps every incoming request with a fresh
// request_id and, when a session cookie is present, the actor user ID, so
// beginWriteTx can attribute every mutation to a user + request without each
// handler having to thread that data through manually.
//
// Session lookup is restricted to potentially-mutating methods. Idempotent
// reads (GET/HEAD/OPTIONS) and static/SSE endpoints skip the lookup to keep
// the read path cheap; handlers that need the user still call lookupSession.
func (s *server) auditContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Load gauges for static mode (see static_mode.go). Exclude long-lived SSE
		// (would pin inFlight) and already-cheap static assets so the counts track
		// the page/API request rate the auto-trigger cares about.
		if p := r.URL.Path; p != "/events" && p != "/host-events" && !strings.HasPrefix(p, "/static/") {
			s.reqRate.Add(1)
			s.inFlight.Add(1)
			defer s.inFlight.Add(-1)
		}
		ctx := withAuditRequestID(r.Context(), newRequestID())
		if mayMutate(r) {
			if user, ok := s.lookupSession(r); ok {
				ctx = withAuditActor(ctx, user.UserID)
			}
			if festID := s.auditFestIDFromPath(r.Context(), r.URL.Path); festID > 0 {
				ctx = withAuditFestID(ctx, festID)
			}
			if gameID := auditGameIDFromPath(r.URL.Path); gameID > 0 {
				ctx = withAuditGameID(ctx, gameID)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// auditFestIDFromPath resolves the fest a mutating request targets from its URL
// path, covering the three fest-scoped prefixes. Returns 0 when the path is not
// fest-scoped or the ref doesn't resolve (those mutations get a null fest_id and
// are never touched by a fest-scoped revert).
func (s *server) auditFestIDFromPath(ctx context.Context, path string) int64 {
	if s.db == nil {
		return 0
	}
	var rest string
	switch {
	case strings.HasPrefix(path, "/api/fest/"):
		rest = strings.TrimPrefix(path, "/api/fest/")
	case strings.HasPrefix(path, "/host/fest/"):
		rest = strings.TrimPrefix(path, "/host/fest/")
	case strings.HasPrefix(path, "/fest/"):
		rest = strings.TrimPrefix(path, "/fest/")
	default:
		return 0
	}
	ref := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		ref = rest[:i]
	}
	if ref == "" || ref == "fest" {
		return 0
	}
	// Fast path: a numeric ref is the fest id itself, so skip the DB lookup that
	// would otherwise run on every mutating request (including frequent presence
	// POSTs). Stamping a non-existent id is harmless — handlers reject unknown
	// fests before any audited mutation, so it never reaches audit_log.
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		if id <= 0 {
			return 0
		}
		return id
	}
	id, err := resolveFestID(ctx, s.db, ref)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func mayMutate(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// beginWriteTx begins a transaction and seeds the audit_ctx row from request
// context so AFTER triggers can attribute mutations to the acting user. Use
// this instead of s.db.BeginTx for any tx that may mutate an audited table.
// Safe to use even for txes that do not touch audited tables — the extra
// upsert into audit_ctx is cheap.
func (s *server) beginWriteTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := seedAuditCtx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// beginWriteTxConn is beginWriteTx on a caller-supplied dedicated connection.
// Callers acquire conn via s.db.Conn(ctx) BEFORE taking s.mu, so the pool wait —
// the one step that can block unbounded under connection starvation — happens
// off-lock (and is bounded by ctx's writeTxTimeout). The transaction itself then
// runs under the lock on an already-acquired connection, so s.mu is only ever
// held for actual DB work, never for a pool wait. See the 2026-06-13 freeze.
func (s *server) beginWriteTxConn(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := seedAuditCtx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func seedAuditCtx(ctx context.Context, tx *sql.Tx) error {
	var actor any
	if v, ok := actorFromContext(ctx); ok {
		actor = v
	}
	var reqID any
	if v := requestIDFromContext(ctx); v != "" {
		reqID = v
	}
	var festID any
	if v, ok := auditFestIDFromContext(ctx); ok {
		festID = v
	}
	_, err := tx.ExecContext(ctx,
		`insert or replace into audit_ctx(id, actor_user_id, request_id, fest_id) values(1, ?, ?, ?)`,
		actor, reqID, festID)
	return err
}

// suppressAuditTx marks the current transaction's audit context so the AFTER
// triggers skip capture for the rest of the tx. Use it for bulk structural
// rebuilds (import/reseed) whose per-row churn carries no incremental-undo value
// and is already recorded at a higher level in the events log. The flag resets
// to 0 on the next beginWriteTx (seedAuditCtx replaces the row).
func suppressAuditTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `update audit_ctx set suppress = 1 where id = 1`)
	return err
}

// writeExec wraps a single non-transactional mutation in an implicit tx so
// audit_ctx is set before the mutation's AFTER triggers fire. Use this in
// place of s.db.ExecContext for any single-statement mutation against an
// audited table.
func (s *server) writeExec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx, err := s.beginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// installAuditSchema creates audit_ctx, the per-transaction context the row
// triggers read to attribute each edit (actor/request/fest). The old audit_log
// table + audit_trigger_state are retired — the journal (journal_triggers.go)
// is now the durable edit log. Idempotent — safe to run on every startup.
func installAuditSchema(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists audit_ctx(
  id integer primary key check(id = 1),
  actor_user_id integer,
  request_id text,
  fest_id integer,
  suppress integer not null default 0
);
insert or ignore into audit_ctx(id) values(1);
`)
	if err != nil {
		return err
	}
	if err := addColumnsIfMissing(db, "audit_ctx", []columnSpec{{Name: "fest_id", Type: "integer"}}); err != nil {
		return err
	}
	if err := addColumnsIfMissing(db, "audit_ctx", []columnSpec{{Name: "suppress", Type: "integer not null default 0"}}); err != nil {
		return err
	}
	return nil
}

// ensureAuditTriggers rebuilds AFTER triggers on every audited table when the
// schema fingerprint (columns + PK structure) differs from the cached one.
// Adding a column via addColumnsIfMissing therefore picks up automatically on
// the next startup.
// dropLegacyAuditTriggers removes any leftover audit_log AFTER triggers from a
// pre-journal database, so old DBs stop writing the retired audit_log. Forward
// edit capture is handled by the journal row-op triggers (ensureJournalTriggers).
func dropLegacyAuditTriggers(db *sql.DB) error {
	rows, err := db.Query(`select name from sqlite_master where type='trigger' and name like 'audit\_%' escape '\'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := db.Exec(`drop trigger if exists ` + quoteIdent(n)); err != nil {
			return err
		}
	}
	return nil
}

func auditTableShape(db *sql.DB, table string) (cols, pks []string, err error) {
	// table is from the hard-coded auditedTables whitelist; safe to interpolate.
	rows, err := db.Query(`select name, pk from pragma_table_info('` + table + `') order by cid`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type pkCol struct {
		name string
		rank int
	}
	var pkCols []pkCol
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return nil, nil, err
		}
		cols = append(cols, name)
		if pk > 0 {
			pkCols = append(pkCols, pkCol{name: name, rank: pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(pkCols, func(i, j int) bool { return pkCols[i].rank < pkCols[j].rank })
	for _, p := range pkCols {
		pks = append(pks, p.name)
	}
	return cols, pks, nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
