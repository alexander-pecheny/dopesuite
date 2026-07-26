package server

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The data half of migrateV18: a test card becomes a Test Session, its labels
// and comments follow it, and whatever cannot dissolve stays put. Run against a
// migrated DB with legacy-shaped rows inserted by hand, so the assertions are
// about the rebinding rather than the DDL (which every other test exercises by
// simply booting).
func TestMigrateV18Sessions(t *testing.T) {
	db := migratedDB(t)

	uid := insertUser(t, db, "owner")
	bid := insertBoard(t, db, uid, "board")
	testList := insertList(t, db, bid, "test")
	keepList := insertList(t, db, bid, "test")

	// A clean test card: only comments, so it should dissolve entirely.
	clean := insertCard(t, db, bid, testList, "test", "session-one-ciphertext")
	taken := insertLabel(t, db, bid, "test", "taken", "взяли")
	missed := insertLabel(t, db, bid, "test", "missed", "не взяли")
	assignLabel(t, db, clean, taken)
	assignLabel(t, db, clean, missed)
	comment := insertEvent(t, db, bid, clean, "comment", "тест шёл дольше обычного")
	edit := insertEvent(t, db, bid, clean, "desc_edit", "diff")

	// A dirty one: an attachment has nowhere to go, so the card survives.
	dirty := insertCard(t, db, bid, keepList, "test", "session-two-ciphertext")
	insertAttachment(t, db, bid, dirty)

	// A Trello import's green label: test-kinded, but bound to no session.
	orphan := insertLabel(t, db, bid, "test", "taken", "поэзия")

	if err := migrateV18Sessions(db); err != nil {
		t.Fatalf("migrateV18Sessions: %v", err)
	}

	if got := scalarInt(t, db, `select count(*) from test_sessions where board_id = ?`, bid); got != 2 {
		t.Fatalf("sessions created = %d, want 2", got)
	}
	// The session carries the card's ciphertext verbatim — nothing decrypts here.
	cleanSession := scalarInt(t, db,
		`select id from test_sessions where meta_enc = ?`, []byte("session-one-ciphertext"))
	if cleanSession == 0 {
		t.Fatal("no session carries the clean card's ciphertext")
	}

	for _, id := range []int64{taken, missed} {
		if got := scalarInt(t, db, `select coalesce(session_id, 0) from labels where id = ?`, id); got != cleanSession {
			t.Errorf("label %d session_id = %d, want %d", id, got, cleanSession)
		}
	}
	if kind := scalarStr(t, db, `select kind from labels where id = ?`, orphan); kind != "normal" {
		t.Errorf("orphan test label kind = %q, want normal", kind)
	}
	if name := scalarStr(t, db, `select cast(name_enc as text) from labels where id = ?`, orphan); name != "поэзия" {
		t.Errorf("orphan label lost its name: %q", name)
	}

	// A comment on a test card was always a note about the session.
	if got := scalarInt(t, db, `select coalesce(session_id, 0) from timeline_events where id = ?`, comment); got != cleanSession {
		t.Errorf("comment session_id = %d, want %d", got, cleanSession)
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
	if got := scalarInt(t, db, `select count(*) from cards where id = ? and deleted_at is null`, dirty); got != 1 {
		t.Error("card with attachments should survive")
	}

	if got := scalarInt(t, db, `select count(*) from lists where id = ? and deleted_at is not null`, testList); got != 1 {
		t.Error("emptied test list should be tombstoned")
	}
	if got := scalarInt(t, db, `select count(*) from lists where type = 'test'`); got != 0 {
		t.Errorf("%d lists still typed test", got)
	}
	if got := scalarInt(t, db, `select count(*) from lists where id = ? and deleted_at is null`, keepList); got != 1 {
		t.Error("list with a surviving card should stay")
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

func insertLabel(t *testing.T, db *sql.DB, bid int64, kind, mark, name string) int64 {
	return execIns(t, db, `insert into labels(board_id, name_enc, color_enc, kind, mark, created_at) values(?, ?, ?, ?, ?, ?)`,
		bid, []byte(name), []byte("#3aa657"), kind, mark, fixtureNow)
}

func assignLabel(t *testing.T, db *sql.DB, cardID, labelID int64) {
	execIns(t, db, `insert into card_labels(card_id, label_id) values(?, ?)`, cardID, labelID)
}

func insertEvent(t *testing.T, db *sql.DB, bid, cardID int64, typ, payload string) int64 {
	return execIns(t, db, `insert into timeline_events(board_id, card_id, type, created_at, payload_enc) values(?, ?, ?, ?, ?)`,
		bid, cardID, typ, fixtureNow, []byte(payload))
}

func insertAttachment(t *testing.T, db *sql.DB, bid, cardID int64) int64 {
	return execIns(t, db, `insert into attachments(board_id, card_id, filename_enc, mime, size, blob_ref, created_at) values(?, ?, ?, 'image/png', 10, 'ref', ?)`,
		bid, cardID, []byte("f.png"), fixtureNow)
}
