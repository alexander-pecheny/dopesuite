// Package auditmw provides the HTTP middleware that stamps every request with
// audit attribution (request id, actor, fest/game) so the write-transaction
// machinery can record who made each mutation, plus the audit schema
// install/cleanup helpers. It is a leaf package: it depends on core, festwrite,
// and store, never on the dopeserver shell.
package auditmw

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"dope/dope/domain/core"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
)

// ContextMiddleware stamps every incoming request with a fresh request_id and,
// when a session cookie is present, the actor user ID, so beginWriteTx can
// attribute every mutation to a user + request without each handler having to
// thread that data through manually.
//
// Session lookup is restricted to potentially-mutating methods. Idempotent
// reads (GET/HEAD/OPTIONS) and static/SSE endpoints skip the lookup to keep
// the read path cheap; handlers that need the user still call LookupSession.
func ContextMiddleware(eng *core.Engine, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Load gauges for static mode (see static_mode.go). Exclude long-lived SSE
		// (would pin inFlight) and already-cheap static assets so the counts track
		// the page/API request rate the auto-trigger cares about.
		if p := r.URL.Path; p != "/events" && p != "/host-events" && !strings.HasPrefix(p, "/static/") {
			eng.ReqRate.Add(1)
			eng.InFlight.Add(1)
			defer eng.InFlight.Add(-1)
		}
		ctx := festwrite.WithAuditRequestID(r.Context(), festwrite.NewRequestID())
		if mayMutate(r) {
			if user, ok := eng.LookupSession(r); ok {
				ctx = festwrite.WithAuditActor(ctx, user.UserID)
			}
			if festID := auditFestIDFromPath(eng, r.Context(), r.URL.Path); festID > 0 {
				ctx = festwrite.WithAuditFestID(ctx, festID)
			}
			if gameID := festwrite.AuditGameIDFromPath(r.URL.Path); gameID > 0 {
				ctx = festwrite.WithAuditGameID(ctx, gameID)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// auditFestIDFromPath resolves the fest a mutating request targets from its URL
// path, covering the three fest-scoped prefixes. Returns 0 when the path is not
// fest-scoped or the ref doesn't resolve (those mutations get a null fest_id and
// are never touched by a fest-scoped revert).
func auditFestIDFromPath(eng *core.Engine, ctx context.Context, path string) int64 {
	if eng.DB == nil {
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
	id, err := store.ResolveFestID(ctx, eng.DB, ref)
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

// InstallAuditSchema creates audit_ctx, the per-transaction context the row
// triggers read to attribute each edit (actor/request/fest). The old audit_log
// table + audit_trigger_state are retired — the journal (journal_triggers.go)
// is now the durable edit log. Idempotent — safe to run on every startup.
func InstallAuditSchema(db *sql.DB) error {
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
	if err := store.AddColumnsIfMissing(db, "audit_ctx", []store.ColumnSpec{{Name: "fest_id", Type: "integer"}}); err != nil {
		return err
	}
	if err := store.AddColumnsIfMissing(db, "audit_ctx", []store.ColumnSpec{{Name: "suppress", Type: "integer not null default 0"}}); err != nil {
		return err
	}
	return nil
}

// DropLegacyAuditTriggers removes any leftover audit_log AFTER triggers from a
// pre-journal database, so old DBs stop writing the retired audit_log. Forward
// edit capture is handled by the journal row-op triggers (ensureJournalTriggers).
func DropLegacyAuditTriggers(db *sql.DB) error {
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
		if _, err := db.Exec(`drop trigger if exists ` + storeutil.QuoteIdent(n)); err != nil {
			return err
		}
	}
	return nil
}
