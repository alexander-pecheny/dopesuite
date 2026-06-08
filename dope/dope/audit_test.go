package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// auditOpenDB opens a fresh DB so the audit schema, audit_ctx singleton, and
// triggers are all installed. Tests can then mutate audited tables and read
// audit_log to verify capture.
func auditOpenDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openFestDB(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// auditTestServer wraps a DB in a minimal server so we can exercise
// beginWriteTx / writeExec, which set audit_ctx from request context.
func auditTestServer(db *sql.DB) *server {
	return &server{
		db:              db,
		subscribers:     make(map[int64]map[chan event]bool),
		hostSubscribers: make(map[int64]map[chan hostPresenceEvent]struct{}),
	}
}

type auditRow struct {
	ID          int64
	Table       string
	RowPK       string
	Op          string
	BeforeJSON  sql.NullString
	AfterJSON   sql.NullString
	ActorUserID sql.NullInt64
	RequestID   sql.NullString
}

func loadAuditRows(t *testing.T, db *sql.DB, table string) []auditRow {
	t.Helper()
	rows, err := db.Query(
		`select id, table_name, row_pk, op, dope_unz(before_json), dope_unz(after_json), actor_user_id, request_id
		 from audit_log where table_name = ? order by id`, table)
	if err != nil {
		t.Fatalf("select audit_log: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.ID, &r.Table, &r.RowPK, &r.Op, &r.BeforeJSON, &r.AfterJSON, &r.ActorUserID, &r.RequestID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return out
}

func mustJSONField(t *testing.T, jsonStr string, field string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", jsonStr, err)
	}
	return m[field]
}

// TestAuditCapturesInsertUpdateDelete verifies the core invariant: every
// mutation against an audited table produces an audit_log row whose op,
// row_pk, and before/after JSON reflect the change accurately.
func TestAuditCapturesInsertUpdateDelete(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()

	// INSERT a fest. fests is audited; PK is `id` (single integer).
	now := utcNow()
	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, null, 1, ?, ?, 0)`, "audit-test", "Audit Test", now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	festID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}

	// UPDATE the fest.
	if _, err := srv.writeExec(ctx, `update fests set title = ?, updated_at = ? where id = ?`,
		"Audit Test Renamed", now, festID); err != nil {
		t.Fatalf("update fest: %v", err)
	}

	// DELETE the fest.
	if _, err := srv.writeExec(ctx, `delete from fests where id = ?`, festID); err != nil {
		t.Fatalf("delete fest: %v", err)
	}

	rows := loadAuditRows(t, db, "fests")
	// We expect exactly INSERT, UPDATE, DELETE for this fest (other tests in the
	// same DB don't exist — fresh tempdir per test).
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3: %+v", len(rows), rows)
	}

	ins, upd, del := rows[0], rows[1], rows[2]

	if ins.Op != "INSERT" || ins.RowPK == "" {
		t.Errorf("insert row: op=%q pk=%q, want INSERT and non-empty pk", ins.Op, ins.RowPK)
	}
	if ins.BeforeJSON.Valid {
		t.Errorf("insert before_json should be NULL, got %q", ins.BeforeJSON.String)
	}
	if !ins.AfterJSON.Valid {
		t.Fatal("insert after_json should not be NULL")
	}
	if title := mustJSONField(t, ins.AfterJSON.String, "title"); title != "Audit Test" {
		t.Errorf("insert after.title = %v, want %q", title, "Audit Test")
	}

	if upd.Op != "UPDATE" || upd.RowPK != ins.RowPK {
		t.Errorf("update row: op=%q pk=%q, want UPDATE and pk=%q", upd.Op, upd.RowPK, ins.RowPK)
	}
	if !upd.BeforeJSON.Valid || !upd.AfterJSON.Valid {
		t.Fatalf("update before/after json must both be set: %+v", upd)
	}
	if before := mustJSONField(t, upd.BeforeJSON.String, "title"); before != "Audit Test" {
		t.Errorf("update before.title = %v, want %q", before, "Audit Test")
	}
	if after := mustJSONField(t, upd.AfterJSON.String, "title"); after != "Audit Test Renamed" {
		t.Errorf("update after.title = %v, want %q", after, "Audit Test Renamed")
	}

	if del.Op != "DELETE" || del.RowPK != ins.RowPK {
		t.Errorf("delete row: op=%q pk=%q, want DELETE and pk=%q", del.Op, del.RowPK, ins.RowPK)
	}
	if !del.BeforeJSON.Valid {
		t.Fatal("delete before_json should not be NULL")
	}
	if del.AfterJSON.Valid {
		t.Errorf("delete after_json should be NULL, got %q", del.AfterJSON.String)
	}
}

// TestAuditUpdateStoresOnlyChangedColumns verifies the column-diff optimization:
// an UPDATE snapshot keeps the primary key plus columns that actually changed,
// and drops unchanged columns. This is what lets a single edit on a wide row
// (e.g. a fest with a big state-like field) stay small while remaining
// revert-safe, since reverseAuditEntry restores changed columns by PK.
func TestAuditUpdateStoresOnlyChangedColumns(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()

	res, err := srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, 'orig description', null, null, 1, ?, ?, 0)`, "diff-test", "Orig Title", now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	festID, _ := res.LastInsertId()

	// Change only `title`; description and most other columns stay the same.
	if _, err := srv.writeExec(ctx, `update fests set title = ? where id = ?`,
		"New Title", festID); err != nil {
		t.Fatalf("update fest: %v", err)
	}

	rows := loadAuditRows(t, db, "fests")
	var upd auditRow
	for _, r := range rows {
		if r.Op == "UPDATE" {
			upd = r
		}
	}
	if upd.Op != "UPDATE" {
		t.Fatalf("no UPDATE audit row found: %+v", rows)
	}

	var before, after map[string]any
	if err := json.Unmarshal([]byte(upd.BeforeJSON.String), &before); err != nil {
		t.Fatalf("before json %q: %v", upd.BeforeJSON.String, err)
	}
	if err := json.Unmarshal([]byte(upd.AfterJSON.String), &after); err != nil {
		t.Fatalf("after json %q: %v", upd.AfterJSON.String, err)
	}

	// PK + changed column present, unchanged columns dropped.
	if _, ok := before["id"]; !ok {
		t.Errorf("before is missing PK 'id': %v", before)
	}
	if before["title"] != "Orig Title" || after["title"] != "New Title" {
		t.Errorf("title not captured: before=%v after=%v", before["title"], after["title"])
	}
	if _, ok := after["description"]; ok {
		t.Errorf("unchanged column 'description' should be dropped from UPDATE diff: %v", after)
	}
	if _, ok := after["slug"]; ok {
		t.Errorf("unchanged column 'slug' should be dropped from UPDATE diff: %v", after)
	}
}

// TestImportSuppressesAuditChurn verifies a bulk import rebuilds all structural
// rows without logging any of them (it's recorded as an 'events' row and acts as
// a revert boundary), while a normal edit afterwards is still audited — proving
// the suppress flag resets per transaction.
func TestImportSuppressesAuditChurn(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)

	scheme := festScheme{
		SchemaVersion:     2,
		Slug:              "imp",
		Title:             "Imp",
		GameType:          "ek",
		RegularThemeCount: 3,
		Venues:            []schemeVenue{{Number: 1, Title: "Main"}},
		Teams: []schemeTeam{
			{Name: "Alpha", Basket: 1, Number: 1},
			{Name: "Beta", Basket: 1, Number: 2},
		},
		Stages: []schemeStage{{
			Code:      "r1",
			Title:     "R1",
			StageType: "matches",
			Position:  1,
			Matches: []schemeMatch{{
				Code:             "A",
				Title:            "A",
				Venue:            1,
				ParticipantCount: 2,
				Slots: []schemeSlot{
					{Seed: &schemeSeedRef{Basket: 1, Number: 1}},
					{Seed: &schemeSeedRef{Basket: 1, Number: 2}},
				},
			}},
		}},
	}
	if _, err := srv.importScheme(scheme); err != nil {
		t.Fatalf("import: %v", err)
	}

	var n int
	if err := db.QueryRow(`select count(*) from audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("import should be audit-suppressed; got %d audit rows", n)
	}

	// A normal edit after the import (new tx → suppress reset) is still captured.
	var festID int64
	if err := db.QueryRow(`select id from fests limit 1`).Scan(&festID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.writeExec(context.Background(),
		`update fests set title = ? where id = ?`, "Renamed", festID); err != nil {
		t.Fatal(err)
	}
	rows := loadAuditRows(t, db, "fests")
	if len(rows) != 1 || rows[0].Op != "UPDATE" {
		t.Fatalf("normal edit after import should be audited once; got %+v", rows)
	}
}

// TestAuditCompressRoundTrip exercises the dope_z/dope_unz storage codec
// directly: compressed values round-trip, and legacy plain-text rows (written
// before compression existed) pass through dope_unz untouched.
func TestAuditCompressRoundTrip(t *testing.T) {
	db := auditOpenDB(t)

	cases := []string{
		"",
		`{"id":1}`,
		`{"title":"hello","state_json":"` + strings.Repeat("x", 5000) + `"}`,
	}
	for _, in := range cases {
		var out string
		if err := db.QueryRow(`select dope_unz(dope_z(?))`, in).Scan(&out); err != nil {
			t.Fatalf("round-trip %q: %v", in, err)
		}
		if out != in {
			t.Errorf("round-trip mismatch: got %q want %q", out, in)
		}
	}

	// Legacy uncompressed JSON text (first byte '{') must pass through unchanged.
	var legacy string
	if err := db.QueryRow(`select dope_unz(?)`, `{"legacy":true}`).Scan(&legacy); err != nil {
		t.Fatalf("legacy passthrough: %v", err)
	}
	if legacy != `{"legacy":true}` {
		t.Errorf("legacy passthrough = %q", legacy)
	}

	// NULL stays NULL.
	var nullOut sql.NullString
	if err := db.QueryRow(`select dope_unz(dope_z(NULL))`).Scan(&nullOut); err != nil {
		t.Fatalf("null round-trip: %v", err)
	}
	if nullOut.Valid {
		t.Errorf("NULL should round-trip to NULL, got %q", nullOut.String)
	}
}

// TestAuditCapturesActorAndRequestID confirms that when ctx carries an actor
// + request_id (as the auditContextMiddleware sets per request), the trigger
// reads them out of audit_ctx and stamps them on every audit row in the tx.
func TestAuditCapturesActorAndRequestID(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	now := utcNow()

	// Create a user we can attribute writes to.
	res, err := srv.writeExec(context.Background(), `
insert into users(username, is_system, created_at, updated_at)
values(?, 0, ?, ?)`, "alice", now, now)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	actorID, _ := res.LastInsertId()

	ctx := withAuditActor(context.Background(), actorID)
	ctx = withAuditRequestID(ctx, "req-xyz")

	// One write tx that touches two audited tables — both rows should share
	// actor + request_id, as a Google-docs-style undo UI would want.
	tx, err := srv.beginWriteTx(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	festID, err := insertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 0)`, "multi-row", "Multi-Row", actorID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, 1, 'Main', ?, ?)`, festID, now, now); err != nil {
		t.Fatalf("insert venue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, table := range []string{"fests", "venues"} {
		rows := loadAuditRows(t, db, table)
		if len(rows) == 0 {
			t.Errorf("no audit rows for %s", table)
			continue
		}
		last := rows[len(rows)-1]
		if !last.ActorUserID.Valid || last.ActorUserID.Int64 != actorID {
			t.Errorf("%s: actor_user_id = %+v, want %d", table, last.ActorUserID, actorID)
		}
		if !last.RequestID.Valid || last.RequestID.String != "req-xyz" {
			t.Errorf("%s: request_id = %+v, want %q", table, last.RequestID, "req-xyz")
		}
	}
}

// TestAuditCompositeKeySerialization makes sure tables with composite PKs
// (here: fest_organizers (fest_id, user_id)) serialize their pk as
// "<fest_id>:<user_id>" so we can locate the right row later.
func TestAuditCompositeKeySerialization(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	ctx := context.Background()
	now := utcNow()

	res, err := srv.writeExec(ctx, `insert into users(username, is_system, created_at, updated_at) values(?, 0, ?, ?)`,
		"bob", now, now)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	userID, _ := res.LastInsertId()

	res, err = srv.writeExec(ctx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 0)`, "comp-key", "Composite Key", userID, now, now)
	if err != nil {
		t.Fatalf("fest: %v", err)
	}
	festID, _ := res.LastInsertId()

	if _, err := srv.writeExec(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'admin', ?)`, festID, userID, now); err != nil {
		t.Fatalf("organizer: %v", err)
	}

	rows := loadAuditRows(t, db, "fest_organizers")
	if len(rows) != 1 {
		t.Fatalf("organizer audit rows = %d, want 1", len(rows))
	}
	expected := serializeIntPair(festID, userID)
	if rows[0].RowPK != expected {
		t.Errorf("row_pk = %q, want %q", rows[0].RowPK, expected)
	}
}

// TestAuditTriggerRebuildOnSchemaChange confirms that adding a column to an
// audited table triggers a rebuild on the next ensureAuditTriggers call so the
// new column is included in the captured before/after JSON.
func TestAuditTriggerRebuildOnSchemaChange(t *testing.T) {
	db := auditOpenDB(t)

	// Force a schema change: add a synthetic column to an audited table.
	if _, err := db.Exec(`alter table fests add column audit_test_col text default 'x'`); err != nil {
		t.Fatalf("alter: %v", err)
	}

	// Rebuild triggers and verify they pick up the new column.
	if err := ensureAuditTriggers(db); err != nil {
		t.Fatalf("ensureAuditTriggers: %v", err)
	}

	srv := auditTestServer(db)
	now := utcNow()
	if _, err := srv.writeExec(context.Background(), `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public, audit_test_col)
values(?, ?, '', null, null, 1, ?, ?, 0, 'hello')`, "with-extra", "With Extra", now, now); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows := loadAuditRows(t, db, "fests")
	if len(rows) == 0 {
		t.Fatal("no audit rows")
	}
	last := rows[len(rows)-1]
	if v := mustJSONField(t, last.AfterJSON.String, "audit_test_col"); v != "hello" {
		t.Errorf("after_json missing new column: %+v", last.AfterJSON.String)
	}
}

// TestAuditCtxIsReseededPerTx makes sure that audit_ctx leftover from a
// previous request is not attributed to a subsequent unauthed write — every
// beginWriteTx must overwrite the row.
func TestAuditCtxIsReseededPerTx(t *testing.T) {
	db := auditOpenDB(t)
	srv := auditTestServer(db)
	now := utcNow()

	// First write: as actor 42, request "first".
	ctx1 := withAuditRequestID(withAuditActor(context.Background(), 42), "first")
	if _, err := srv.writeExec(ctx1, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('a','A','',null,null,1,?,?,0)`, now, now); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second write: anonymous (no actor, no request).
	ctx2 := context.Background()
	if _, err := srv.writeExec(ctx2, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('b','B','',null,null,1,?,?,0)`, now, now); err != nil {
		t.Fatalf("second insert: %v", err)
	}

	rows := loadAuditRows(t, db, "fests")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !rows[0].ActorUserID.Valid || rows[0].ActorUserID.Int64 != 42 {
		t.Errorf("first row actor = %+v, want 42", rows[0].ActorUserID)
	}
	if rows[1].ActorUserID.Valid {
		t.Errorf("second row should have NULL actor, got %d", rows[1].ActorUserID.Int64)
	}
	if rows[1].RequestID.Valid {
		t.Errorf("second row should have NULL request_id, got %q", rows[1].RequestID.String)
	}
}

func serializeIntPair(a, b int64) string {
	return strconv.FormatInt(a, 10) + ":" + strconv.FormatInt(b, 10)
}
