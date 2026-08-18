package tests

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dopeserver "dope/dope/server"
)

// The schema is what the migration list makes of an empty file, and it is
// pinned: a change to any table, index, view or trigger shows up as a diff
// against testdata/schema.sql. Regenerate with DOPE_UPDATE_SCHEMA=1.
func TestSchemaIsPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh := dumpSchema(t, db)
	db.Close()

	golden := filepath.Join("testdata", "schema.sql")
	if os.Getenv("DOPE_UPDATE_SCHEMA") != "" {
		if err := os.WriteFile(golden, []byte(fresh), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v — run with DOPE_UPDATE_SCHEMA=1 to write it", err)
	}
	if string(want) != fresh {
		t.Errorf("schema differs from testdata/schema.sql; if the change is meant, rerun with DOPE_UPDATE_SCHEMA=1\n%s", diffLines(string(want), fresh))
	}

	// Opening the same file again applies nothing and changes nothing.
	db, err = dopeserver.OpenFestDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if again := dumpSchema(t, db); again != fresh {
		t.Errorf("a second open changed the schema:\n%s", diffLines(fresh, again))
	}
}

func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`select type, name, sql from sqlite_master where sql is not null and name not like 'sqlite_%' order by type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		b.WriteString("-- " + kind + " " + name + "\n" + ddl + ";\n\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	seen := map[string]bool{}
	for _, l := range w {
		seen[l] = true
	}
	var b strings.Builder
	for _, l := range g {
		if !seen[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	seen = map[string]bool{}
	for _, l := range g {
		seen[l] = true
	}
	for _, l := range w {
		if !seen[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	return b.String()
}
