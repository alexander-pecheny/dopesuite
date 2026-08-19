package server

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The data half of schema v18: a test card becomes a Test Session, its comments
// follow it, every question it labelled gains a Playing, and whatever cannot
// dissolve stays put. Run against a migrated DB with legacy-shaped rows inserted
// by hand, so the assertions are about the rebinding rather than the DDL (which
// every other test exercises by simply booting).
func TestMigrateV18Sessions(t *testing.T) {
	db := migratedDB(t)

	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	testList := insertList(t, db, bid, "test")
	keepList := insertList(t, db, bid, "test")
	questions := insertList(t, db, bid, "normal")

	// schema v18 has already flattened labels, so the pre-migration marker table
	// is what says which labels were a session's own. Rebuild it by hand.
	mustExec(t, db, `create table if not exists _v18_test_labels(id integer)`)

	// A clean test card: only comments, so it should dissolve entirely.
	clean := insertCard(t, db, bid, testList, "test", "session-one-ciphertext")
	taken := insertLabel(t, db, bid, "взяли")
	missed := insertLabel(t, db, bid, "не взяли")
	mustExec(t, db, `insert into _v18_test_labels(id) values(?), (?)`, taken, missed)
	assignLabel(t, db, clean, taken)
	assignLabel(t, db, clean, missed)
	comment := insertLegacyEvent(t, db, bid, clean, "comment", "тест шёл дольше обычного")
	edit := insertLegacyEvent(t, db, bid, clean, "desc_edit", "diff")

	// Two questions: one the testers took, one they missed. Both were PLAYED.
	q1 := insertCard(t, db, bid, questions, "question", "q1")
	q2 := insertCard(t, db, bid, questions, "question", "q2")
	assignLabel(t, db, q1, taken)
	assignLabel(t, db, q2, missed)

	// A hand-made label that merely happens to sit on the test card must NOT drag
	// every card carrying it into the session.
	topic := insertLabel(t, db, bid, "поэзия")
	assignLabel(t, db, clean, topic)
	q3 := insertCard(t, db, bid, questions, "question", "q3")
	assignLabel(t, db, q3, topic)

	// A dirty test card: an attachment has nowhere to go, so the card survives.
	dirty := insertCard(t, db, bid, keepList, "test", "session-two-ciphertext")
	insertAttachment(t, db, bid, dirty)

	if err := migrateV18Sessions(db); err != nil {
		t.Fatalf("migrateV18Sessions: %v", err)
	}

	if got := scalarInt(t, db, `select count(*) from test_sessions where board_id = ?`, bid); got != 2 {
		t.Fatalf("sessions created = %d, want 2", got)
	}
	// The session carries the card's ciphertext verbatim — nothing decrypts here.
	session := scalarInt(t, db,
		`select id from test_sessions where meta_enc = ?`, []byte("session-one-ciphertext"))
	if session == 0 {
		t.Fatal("no session carries the clean card's ciphertext")
	}

	// Playings, not labels, are what «Видели» reads.
	for _, id := range []int64{q1, q2} {
		if got := scalarInt(t, db, `select count(*) from card_sessions where card_id = ? and session_id = ?`, id, session); got != 1 {
			t.Errorf("card %d has no playing for the session", id)
		}
	}
	if got := scalarInt(t, db, `select count(*) from card_sessions where card_id = ?`, q3); got != 0 {
		t.Error("a hand-made label dragged an unrelated card into the session")
	}

	// Labels survive 1↔1, keeping their names, and stay unscoped.
	if name := scalarStr(t, db, `select cast(name_enc as text) from labels where id = ?`, taken); name != "взяли" {
		t.Errorf("label lost its name: %q", name)
	}
	if got := scalarInt(t, db, `select count(*) from card_labels where card_id = ? and session_id is not null`, q1); got != 0 {
		t.Error("migrated assignment should stay unscoped")
	}

	// A comment on a test card was always a note about the session.
	if got := scalarInt(t, db, `select coalesce(session_id, 0) from timeline_events where id = ?`, comment); got != session {
		t.Errorf("comment session_id = %d, want %d", got, session)
	}
	if got := scalarInt(t, db, `select count(*) from timeline_events where id = ? and card_id is null`, comment); got != 1 {
		t.Error("comment kept its card_id")
	}
	if got := scalarInt(t, db, `select count(*) from timeline_events where id = ? and deleted_at is not null`, edit); got != 1 {
		t.Error("desc_edit on a dissolving card should be tombstoned")
	}

	if got := scalarInt(t, db, `select count(*) from cards where id = ? and deleted_at is not null`, clean); got != 1 {
		t.Error("clean test card should dissolve")
	}
	if kind := scalarStr(t, db, `select kind from cards where id = ?`, dirty); kind != "normal" {
		t.Errorf("card with attachments kind = %q, want normal", kind)
	}
	if got := scalarInt(t, db, `select count(*) from lists where id = ? and deleted_at is not null`, testList); got != 1 {
		t.Error("emptied test list should be tombstoned")
	}
	if got := scalarInt(t, db, `select count(*) from lists where type = 'test'`); got != 0 {
		t.Errorf("%d lists still typed test", got)
	}
}

// The partial unique index is the whole reason the key can stay nullable: SQLite
// compares NULLs as distinct, so without it the unscoped assignment duplicates.
func TestUnscopedLabelAssignmentIsUnique(t *testing.T) {
	db := migratedDB(t)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	list := insertList(t, db, bid, "normal")
	card := insertCard(t, db, bid, list, "question", "q")
	label := insertLabel(t, db, bid, "сложный")

	assignLabel(t, db, card, label)
	if _, err := db.Exec(`insert into card_labels(card_id, label_id, session_id) values(?, ?, null)`, card, label); err == nil {
		t.Fatal("a second unscoped assignment inserted; the partial index is missing")
	}

	// The same label scoped to a playing is a DIFFERENT claim and must be allowed.
	sid := execIns(t, db, `insert into test_sessions(board_id, meta_enc, created_at) values(?, ?, ?)`,
		bid, []byte("meta"), fixtureNow)
	if _, err := db.Exec(`insert into card_labels(card_id, label_id, session_id) values(?, ?, ?)`, card, label, sid); err != nil {
		t.Fatalf("scoped assignment rejected: %v", err)
	}
	if got := scalarInt(t, db, `select count(*) from card_labels where card_id = ? and label_id = ?`, card, label); got != 2 {
		t.Errorf("assignments = %d, want 2 (one yours, one the testers')", got)
	}
}

// Removing a Playing takes the labels scoped to it: a label scoped to a playing
// that no longer exists cannot be read (ADR-0004).
func TestRemovingAPlayingCascadesItsLabels(t *testing.T) {
	db := migratedDB(t)
	mustExec(t, db, `pragma foreign_keys = on`)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	list := insertList(t, db, bid, "normal")
	card := insertCard(t, db, bid, list, "question", "q")
	label := insertLabel(t, db, bid, "взяли")
	sid := execIns(t, db, `insert into test_sessions(board_id, meta_enc, created_at) values(?, ?, ?)`,
		bid, []byte("meta"), fixtureNow)
	mustExec(t, db, `insert into card_sessions(card_id, session_id) values(?, ?)`, card, sid)
	mustExec(t, db, `insert into card_labels(card_id, label_id, session_id) values(?, ?, ?)`, card, label, sid)

	mustExec(t, db, `delete from test_sessions where id = ?`, sid)
	if got := scalarInt(t, db, `select count(*) from card_labels where session_id = ?`, sid); got != 0 {
		t.Errorf("%d scoped assignments outlived their playing", got)
	}
	if got := scalarInt(t, db, `select count(*) from card_sessions where session_id = ?`, sid); got != 0 {
		t.Error("the playing itself outlived its session")
	}
}

// ---- fixtures ----

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "xy.db"))
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func execIns(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	res, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func scalarInt(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		if err == sql.ErrNoRows {
			return 0
		}
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func scalarStr(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}

const fixtureNow = "2026-07-26T00:00:00.000Z"

func insertUser(t *testing.T, db *sql.DB, name string) int64 {
	return execIns(t, db, `insert into users(username, created_at, updated_at) values(?, ?, ?)`,
		name, fixtureNow, fixtureNow)
}

func insertBoard(t *testing.T, db *sql.DB, uid int64, name string) int64 {
	return execIns(t, db, `
insert into boards(owner_user_id, name, name_enc, kdf_salt, kdf_params, wrapped_key, verify_token, created_at, updated_at, schema_version)
values(?, ?, ?, ?, '{}', ?, ?, ?, ?, 2)`,
		uid, name, []byte(name), []byte("salt"), []byte("dk"), []byte("v"), fixtureNow, fixtureNow)
}

func insertList(t *testing.T, db *sql.DB, bid int64, typ string) int64 {
	return execIns(t, db, `insert into lists(board_id, type, title_enc, rank, created_at, updated_at) values(?, ?, ?, 'a', ?, ?)`,
		bid, typ, []byte("list"), fixtureNow, fixtureNow)
}

func insertCard(t *testing.T, db *sql.DB, bid, listID int64, kind, desc string) int64 {
	return execIns(t, db, `insert into cards(board_id, list_id, kind, description_enc, rank, created_at, updated_at) values(?, ?, ?, ?, 'a', ?, ?)`,
		bid, listID, kind, []byte(desc), fixtureNow, fixtureNow)
}

func insertLabel(t *testing.T, db *sql.DB, bid int64, name string) int64 {
	return execIns(t, db, `insert into labels(board_id, name_enc, color_enc, created_at) values(?, ?, ?, ?)`,
		bid, []byte(name), []byte("#3aa657"), fixtureNow)
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func assignLabel(t *testing.T, db *sql.DB, cardID, labelID int64) {
	execIns(t, db, `insert into card_labels(card_id, label_id, session_id) values(?, ?, null)`, cardID, labelID)
}

func insertLegacyEvent(t *testing.T, db *sql.DB, bid, cardID int64, typ, payload string) int64 {
	return execIns(t, db, `insert into timeline_events(board_id, card_id, type, created_at, payload_enc) values(?, ?, ?, ?, ?)`,
		bid, cardID, typ, fixtureNow, []byte(payload))
}

func insertAttachment(t *testing.T, db *sql.DB, bid, cardID int64) int64 {
	return execIns(t, db, `insert into attachments(board_id, card_id, filename_enc, mime, size, blob_ref, created_at) values(?, ?, ?, 'image/png', 10, 'ref', ?)`,
		bid, cardID, []byte("f.png"), fixtureNow)
}

// A Declaration is a claim about a specific set of questions, so it dies when
// its tour stops being that tour: linking a list into a group, or dissolving
// one, drops the Declarations involved rather than carrying them across. The
// tempting alternative — union them — is what puts a tester who saw 9 of 12 in
// one tour into a merged 24-question preamble they should have been able to play.
func TestDeclarationDiesWithItsTour(t *testing.T) {
	db := migratedDB(t)
	mustExec(t, db, `pragma foreign_keys = on`)
	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	list := insertList(t, db, bid, "normal")
	sid := execIns(t, db, `insert into test_sessions(board_id, meta_enc, created_at) values(?, ?, ?)`,
		bid, []byte("meta"), fixtureNow)
	mustExec(t, db, `insert into tour_testers(board_id, list_id, session_id) values(?, ?, ?)`, bid, list, sid)

	// Exactly one scope: a row naming both, or neither, is not a tour.
	if _, err := db.Exec(`insert into tour_testers(board_id, list_id, group_id, session_id) values(?, ?, 1, ?)`,
		bid, list, sid); err == nil {
		t.Error("a Declaration naming both a list and a group was accepted")
	}
	if _, err := db.Exec(`insert into tour_testers(board_id, session_id) values(?, ?)`, bid, sid); err == nil {
		t.Error("a Declaration naming no tour was accepted")
	}
	// And one row per (tour, session).
	if _, err := db.Exec(`insert into tour_testers(board_id, list_id, session_id) values(?, ?, ?)`,
		bid, list, sid); err == nil {
		t.Error("the same session was declared twice for one tour")
	}

	// The session going takes its Declaration with it: naming a test that no
	// longer exists is not a state the model has.
	mustExec(t, db, `delete from test_sessions where id = ?`, sid)
	if got := scalarInt(t, db, `select count(*) from tour_testers where list_id = ?`, list); got != 0 {
		t.Errorf("%d Declarations outlived the session they named", got)
	}
}
