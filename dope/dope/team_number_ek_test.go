package dopeserver

import (
	"path/filepath"
	"testing"
)

// TestEnsureSeedTeamByNumberKeysByNumber covers the EK identity fix: teams are
// resolved by number, so two same-named teams stay distinct and re-seeding by
// number reuses (and refreshes) the existing row instead of duplicating it.
func TestEnsureSeedTeamByNumberKeysByNumber(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, _ := createRosterPropagationFixture(t, db)
	ctx := t.Context()
	if _, err := db.Exec(`delete from teams where fest_id = ?`, festID); err != nil {
		t.Fatalf("clear teams: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	id7, _, err := ensureSeedTeamByNumber(ctx, tx, festID, 7, "Дубль", "Москва", nil)
	if err != nil {
		t.Fatalf("seed #7: %v", err)
	}
	id8, _, err := ensureSeedTeamByNumber(ctx, tx, festID, 8, "Дубль", "Питер", nil)
	if err != nil {
		t.Fatalf("seed #8: %v", err)
	}
	if id7 == id8 {
		t.Fatalf("same-named teams with distinct numbers must be distinct rows (both %d)", id7)
	}

	// Re-seed #7 with a changed name: same row, name refreshed.
	id7b, _, err := ensureSeedTeamByNumber(ctx, tx, festID, 7, "Дубль-2", "", nil)
	if err != nil {
		t.Fatalf("re-seed #7: %v", err)
	}
	if id7b != id7 {
		t.Fatalf("re-seed by number must reuse the row, got %d want %d", id7b, id7)
	}
	var name string
	if err := tx.QueryRow(`select name from teams where id = ?`, id7).Scan(&name); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if name != "Дубль-2" {
		t.Fatalf("name should refresh to %q, got %q", "Дубль-2", name)
	}

	// A number-less call falls back to name-keyed creation.
	id0, _, err := ensureSeedTeamByNumber(ctx, tx, festID, 0, "Без номера", "", nil)
	if err != nil {
		t.Fatalf("number-less seed: %v", err)
	}
	if id0 == 0 || id0 == id7 || id0 == id8 {
		t.Fatalf("number-less fallback should create a fresh team, got %d", id0)
	}
}

// TestBackfillEKTeamNumbers covers the v14 backfill: unambiguous names get their
// fest_teams number; an ambiguous (duplicate) name is left null.
func TestBackfillEKTeamNumbers(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, _, _ := createRosterPropagationFixture(t, db)
	ctx := t.Context()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`delete from teams where fest_id = ?`, festID)
	mustExec(`delete from fest_teams where fest_id = ?`, festID)

	ftIns := `insert into fest_teams(fest_id, rating_id, name, city, position, number, deleted) values(?, ?, ?, '', ?, ?, 0)`
	mustExec(ftIns, festID, 1, "Альфа", 1, 1)
	mustExec(ftIns, festID, 2, "Бета", 2, 2)
	mustExec(ftIns, festID, 3, "Дубль", 3, 3)
	mustExec(ftIns, festID, 4, "Дубль", 4, 4) // ambiguous name
	teamIns := `insert into teams(fest_id, name, city) values(?, ?, '')`
	mustExec(teamIns, festID, "Альфа")
	mustExec(teamIns, festID, "Бета")
	mustExec(teamIns, festID, "Дубль")

	if err := backfillEKTeamNumbers(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	checkNum := func(name string, want any) {
		t.Helper()
		var num any
		if err := db.QueryRow(`select number from teams where fest_id = ? and name = ?`, festID, name).Scan(&num); err != nil {
			t.Fatalf("read %s number: %v", name, err)
		}
		var got any
		if n, ok := num.(int64); ok {
			got = n
		}
		if want == nil {
			if got != nil {
				t.Fatalf("%s number = %v, want null (ambiguous)", name, got)
			}
			return
		}
		if got != want {
			t.Fatalf("%s number = %v, want %v", name, got, want)
		}
	}
	checkNum("Альфа", int64(1))
	checkNum("Бета", int64(2))
	checkNum("Дубль", nil) // ambiguous → left unassigned
}
