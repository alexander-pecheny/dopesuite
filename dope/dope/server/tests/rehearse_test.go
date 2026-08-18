package tests

import (
	"os"
	"path/filepath"
	"testing"

	dopeserver "dope/dope/server"
)

// TestRehearseMigrations opens a copy of a real database — a prod snapshot,
// taken with sqlite3's online backup — with this build, so a release's
// startup migrations are walked before prod walks them. Set DOPE_REHEARSE_DB
// to the snapshot's path; the file is copied first and never written. Set
// DOPE_REHEARSE_KEEP to a path to keep the migrated copy for a look.
func TestRehearseMigrations(t *testing.T) {
	src := os.Getenv("DOPE_REHEARSE_DB")
	if src == "" {
		t.Skip("установите DOPE_REHEARSE_DB=<снимок базы>, чтобы прогнать миграции")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("DOPE_REHEARSE_KEEP")
	if path == "" {
		path = filepath.Join(t.TempDir(), "rehearse.db")
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatalf("миграции не прошли: %v", err)
	}
	defer db.Close()
	var versions string
	if err := db.QueryRow(`select group_concat(version, ' ') from (select version from schema_versions order by version)`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	t.Logf("schema_versions: %s", versions)
	var flat, ranked, letterless int
	if err := db.QueryRow(`select count(*) from stages where kind = 'flat'`).Scan(&flat); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select count(distinct s.id) from stages s join stage_standings st on st.stage_id = s.id where s.kind = 'flat'`).Scan(&ranked); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select count(distinct game_id) from matches where letter = '' and title like 'Бой %'`).Scan(&letterless); err != nil {
		t.Fatal(err)
	}
	t.Logf("flat Blocks: %d, of them ranked: %d; games with an unlettered бой: %d", flat, ranked, letterless)
}
