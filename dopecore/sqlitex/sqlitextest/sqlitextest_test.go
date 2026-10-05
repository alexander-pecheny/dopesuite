package sqlitextest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"pecheny.me/dopecore/sqlitex"

	_ "modernc.org/sqlite"
)

func pragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var v string
	if err := db.QueryRow("pragma " + name).Scan(&v); err != nil {
		t.Fatalf("pragma %s: %v", name, err)
	}
	return v
}

// Production stays durable and tests do not, and both are still in WAL.
func TestOnlyTheTestOpenSkipsTheFsync(t *testing.T) {
	dir := t.TempDir()
	prod, err := sqlitex.Open(filepath.Join(dir, "prod.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	test, err := Open(filepath.Join(dir, "test.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer test.Close()

	// synchronous: 0 OFF, 1 NORMAL, 2 FULL.
	if got := pragma(t, prod, "synchronous"); got != "2" {
		t.Errorf("production synchronous = %s, want 2 (FULL)", got)
	}
	if got := pragma(t, test, "synchronous"); got != "0" {
		t.Errorf("test synchronous = %s, want 0 (OFF)", got)
	}
	for name, db := range map[string]*sql.DB{"production": prod, "test": test} {
		if got := pragma(t, db, "journal_mode"); got != "wal" {
			t.Errorf("%s journal_mode = %s, want wal", name, got)
		}
	}
}
