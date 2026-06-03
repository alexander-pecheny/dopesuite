package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// auditedTables lists every table whose row-level mutations are recorded in
// audit_log via AFTER triggers. Skipped on purpose:
//   - audit_log / audit_ctx / audit_trigger_state: the audit machinery itself
//   - schema_versions: managed by migrateDB
//   - sessions / telegram_login_codes: high-churn auth state with no undo value
//   - events: already a higher-level fest revision log
var auditedTables = []string{
	"users",
	"invites",
	"schemes",
	"fests",
	"fest_organizers",
	"fest_teams",
	"fest_players",
	"fest_team_players",
	"game_player_team_overrides",
	"teams",
	"players",
	"team_players",
	"games",
	"game_teams",
	"game_players",
	"game_team_players",
	"game_assignments",
	"venues",
	"stages",
	"matches",
	"match_slots",
	"themes",
	"answers",
	"match_results",
	"reseed_entries",
}

type auditCtxKey string

const (
	auditCtxKeyActor     auditCtxKey = "audit.actor_user_id"
	auditCtxKeyRequestID auditCtxKey = "audit.request_id"
	auditCtxKeyFestID    auditCtxKey = "audit.fest_id"
)

// auditTriggerTemplateVersion is mixed into the trigger fingerprint so that a
// change to the trigger SQL (e.g. adding the fest_id column) forces a rebuild on
// the next startup, even when no audited table's shape changed.
const auditTriggerTemplateVersion = 2

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
		ctx := withAuditRequestID(r.Context(), newRequestID())
		if mayMutate(r) {
			if user, ok := s.lookupSession(r); ok {
				ctx = withAuditActor(ctx, user.UserID)
			}
			if festID := s.auditFestIDFromPath(r.Context(), r.URL.Path); festID > 0 {
				ctx = withAuditFestID(ctx, festID)
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

// installAuditSchema creates audit_log, audit_ctx, and audit_trigger_state.
// Idempotent — safe to run on every startup.
func installAuditSchema(db *sql.DB) error {
	_, err := db.Exec(`
create table if not exists audit_log(
  id integer primary key autoincrement,
  ts text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  table_name text not null,
  row_pk text not null,
  op text not null check (op in ('INSERT','UPDATE','DELETE')),
  before_json text,
  after_json text,
  actor_user_id integer,
  request_id text,
  fest_id integer
);
create index if not exists audit_log_table_pk_idx on audit_log(table_name, row_pk, id);
create index if not exists audit_log_ts_idx on audit_log(ts);
create index if not exists audit_log_request_idx on audit_log(request_id) where request_id is not null;

create table if not exists audit_ctx(
  id integer primary key check(id = 1),
  actor_user_id integer,
  request_id text,
  fest_id integer
);
insert or ignore into audit_ctx(id) values(1);

create table if not exists audit_trigger_state(
  id integer primary key check(id = 1),
  fingerprint text not null default ''
);
insert or ignore into audit_trigger_state(id, fingerprint) values(1, '');
`)
	if err != nil {
		return err
	}
	// fest_id was added after the initial release; backfill the column on
	// databases that predate it so the CREATEs above stay no-ops.
	if err := addColumnsIfMissing(db, "audit_log", []columnSpec{{Name: "fest_id", Type: "integer"}}); err != nil {
		return err
	}
	if err := addColumnsIfMissing(db, "audit_ctx", []columnSpec{{Name: "fest_id", Type: "integer"}}); err != nil {
		return err
	}
	if _, err := db.Exec(`create index if not exists audit_log_fest_idx on audit_log(fest_id, id) where fest_id is not null`); err != nil {
		return err
	}
	return nil
}

// ensureAuditTriggers rebuilds AFTER triggers on every audited table when the
// schema fingerprint (columns + PK structure) differs from the cached one.
// Adding a column via addColumnsIfMissing therefore picks up automatically on
// the next startup.
func ensureAuditTriggers(db *sql.DB) error {
	type tableShape struct {
		name string
		cols []string
		pks  []string
	}
	shapes := make([]tableShape, 0, len(auditedTables))
	for _, t := range auditedTables {
		cols, pks, err := auditTableShape(db, t)
		if err != nil {
			return fmt.Errorf("audit: read pragma %s: %w", t, err)
		}
		if len(cols) == 0 {
			// Table missing from this DB (legacy/empty) — skip silently.
			continue
		}
		shapes = append(shapes, tableShape{name: t, cols: cols, pks: pks})
	}

	h := sha256.New()
	fmt.Fprintf(h, "template-version=%d\n", auditTriggerTemplateVersion)
	for _, s := range shapes {
		sortedCols := append([]string(nil), s.cols...)
		sort.Strings(sortedCols)
		sortedPks := append([]string(nil), s.pks...)
		sort.Strings(sortedPks)
		fmt.Fprintf(h, "%s|cols=%s|pks=%s\n",
			s.name,
			strings.Join(sortedCols, ","),
			strings.Join(sortedPks, ","))
	}
	fingerprint := hex.EncodeToString(h.Sum(nil))

	var existing string
	err := db.QueryRow(`select fingerprint from audit_trigger_state where id = 1`).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existing == fingerprint {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, s := range shapes {
		for _, op := range []string{"insert", "update", "delete"} {
			name := fmt.Sprintf("audit_%s_%s", s.name, op)
			if _, err := tx.Exec(`drop trigger if exists ` + name); err != nil {
				return fmt.Errorf("drop trigger %s: %w", name, err)
			}
		}
		for _, op := range []string{"insert", "update", "delete"} {
			stmt := buildAuditTrigger(s.name, s.cols, s.pks, op)
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("create trigger audit_%s_%s: %w", s.name, op, err)
			}
		}
	}

	if _, err := tx.Exec(`update audit_trigger_state set fingerprint = ? where id = 1`, fingerprint); err != nil {
		return err
	}
	return tx.Commit()
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

func buildAuditTrigger(table string, cols, pks []string, op string) string {
	if len(pks) == 0 {
		// Tables without a declared PK fall back to rowid for row identity.
		pks = []string{"rowid"}
	}

	rowJSON := func(prefix string) string {
		parts := make([]string, 0, len(cols))
		for _, c := range cols {
			parts = append(parts, fmt.Sprintf("'%s', %s.%s", c, prefix, quoteIdent(c)))
		}
		return "json_object(" + strings.Join(parts, ", ") + ")"
	}
	pkExpr := func(prefix string) string {
		if len(pks) == 1 {
			return fmt.Sprintf("cast(%s.%s as text)", prefix, quoteIdent(pks[0]))
		}
		parts := make([]string, 0, len(pks))
		for _, p := range pks {
			parts = append(parts, fmt.Sprintf("cast(%s.%s as text)", prefix, quoteIdent(p)))
		}
		return strings.Join(parts, " || ':' || ")
	}

	const actorSel = `(select actor_user_id from audit_ctx where id = 1)`
	const reqSel = `(select request_id from audit_ctx where id = 1)`
	const festSel = `(select fest_id from audit_ctx where id = 1)`

	name := fmt.Sprintf("audit_%s_%s", table, op)
	switch op {
	case "insert":
		return fmt.Sprintf(`create trigger %s
after insert on %s
begin
  insert into audit_log(table_name, row_pk, op, before_json, after_json, actor_user_id, request_id, fest_id)
  values('%s', %s, 'INSERT', null, %s, %s, %s, %s);
end`,
			name, table,
			table, pkExpr("new"), rowJSON("new"), actorSel, reqSel, festSel)
	case "update":
		return fmt.Sprintf(`create trigger %s
after update on %s
begin
  insert into audit_log(table_name, row_pk, op, before_json, after_json, actor_user_id, request_id, fest_id)
  values('%s', %s, 'UPDATE', %s, %s, %s, %s, %s);
end`,
			name, table,
			table, pkExpr("new"), rowJSON("old"), rowJSON("new"), actorSel, reqSel, festSel)
	case "delete":
		return fmt.Sprintf(`create trigger %s
after delete on %s
begin
  insert into audit_log(table_name, row_pk, op, before_json, after_json, actor_user_id, request_id, fest_id)
  values('%s', %s, 'DELETE', %s, null, %s, %s, %s);
end`,
			name, table,
			table, pkExpr("old"), rowJSON("old"), actorSel, reqSel, festSel)
	}
	return ""
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
