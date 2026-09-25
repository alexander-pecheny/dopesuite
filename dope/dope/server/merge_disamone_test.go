package dopeserver

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeDisamoneAccounts(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "merge.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	// Prod's state from before v32, when both spellings could exist.
	if _, err := db.Exec(`drop index users_username_nocase`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	now := "2026-09-25T00:00:00Z"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`insert into users(id, username, password_hash, is_system, created_at, updated_at) values(1031, 'disamone', '$2a$old', 0, ?, ?)`, now, now)
	mustExec(`insert into users(id, username, telegram_user_id, telegram_username, is_system, created_at, updated_at) values(1095, 'Disamone', 777, 'Disamone', 0, ?, ?)`, now, now)
	mustExec(`insert into fests(id, title, created_by, revision, created_at, updated_at, is_public) values(5001, 'F', null, 0, ?, ?, 1)`, now, now)
	mustExec(`insert into fest_organizers(fest_id, user_id, role, added_at) values(5001, 1031, 'host', ?)`, now)

	if err := mergeDisamoneAccounts(db); err != nil {
		t.Fatalf("merge: %v", err)
	}
	var n int
	if err := db.QueryRow(`select count(*) from users where id = 1031`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old account still there: %d %v", n, err)
	}
	var role, hash string
	if err := db.QueryRow(`select role from fest_organizers where fest_id = 5001 and user_id = 1095`).Scan(&role); err != nil || role != "host" {
		t.Fatalf("role = %q %v, want host", role, err)
	}
	if err := db.QueryRow(`select password_hash from users where id = 1095`).Scan(&hash); err != nil || hash != "$2a$old" {
		t.Fatalf("hash = %q %v", hash, err)
	}
	// Run again: nothing left to merge.
	if err := mergeDisamoneAccounts(db); err != nil {
		t.Fatalf("second merge: %v", err)
	}
}

func TestUsernamesUniqueIgnoringCase(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "nocase.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	// Rebuild prod's state from before v32: no index, the Oleg pair.
	if _, err := db.Exec(`drop index users_username_nocase`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	now := "2026-09-25T00:00:00Z"
	for _, u := range [][2]string{{"Oleg", "osmiheev"}, {"oleg", "ohhhleeeeg"}} {
		if _, err := db.Exec(`insert into users(username, telegram_username, is_system, created_at, updated_at) values(?, ?, 0, ?, ?)`,
			u[0], u[1], now, now); err != nil {
			t.Fatalf("seed %s: %v", u[0], err)
		}
	}
	if err := usernamesUniqueIgnoringCase(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var name string
	if err := db.QueryRow(`select username from users where telegram_username = 'ohhhleeeeg'`).Scan(&name); err != nil || name != "ohhhleeeeg" {
		t.Fatalf("renamed to %q (%v), want ohhhleeeeg", name, err)
	}
	if _, err := db.Exec(`insert into users(username, is_system, created_at, updated_at) values('OLEG', 0, ?, ?)`, now, now); err == nil {
		t.Fatalf("OLEG was accepted next to Oleg")
	}
}

func TestUsernamesUniqueIgnoringCaseNamesAClash(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "clash.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`drop index users_username_nocase`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	now := "2026-09-25T00:00:00Z"
	for _, n := range []string{"Ivan", "ivan"} {
		if _, err := db.Exec(`insert into users(username, is_system, created_at, updated_at) values(?, 0, ?, ?)`, n, now, now); err != nil {
			t.Fatalf("seed %s: %v", n, err)
		}
	}
	err = usernamesUniqueIgnoringCase(db)
	if err == nil || !strings.Contains(err.Error(), "Ivan") {
		t.Fatalf("err = %v, want one naming the Ivan pair", err)
	}
}
