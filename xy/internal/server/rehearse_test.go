package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRehearseMigrations walks a copy of a real database — a prod snapshot,
// taken with sqlite3's online backup — through this build's migration list, so
// a release's startup migrations run before prod runs them. Set XY_REHEARSE_DB
// to the snapshot's path; the file is copied first and never written.
func TestRehearseMigrations(t *testing.T) {
	src := os.Getenv("XY_REHEARSE_DB")
	if src == "" {
		t.Skip("установите XY_REHEARSE_DB=<снимок базы>, чтобы прогнать миграции")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rehearse.db")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := openDB(path)
	if err != nil {
		t.Fatalf("миграции не прошли: %v", err)
	}
	defer db.Close()
	var versions string
	if err := db.QueryRow(`select group_concat(version, ' ') from (select version from schema_versions order by version)`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	t.Logf("schema_versions: %s", versions)
}
