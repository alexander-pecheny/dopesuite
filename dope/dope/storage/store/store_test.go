package store

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBuildDSN(t *testing.T) {
	dsn := BuildDSN("fest.db")
	if !strings.HasPrefix(dsn, "file:fest.db?") {
		t.Fatalf("bare path should gain file: prefix and ? params: %s", dsn)
	}
	for _, want := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(1)"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN missing pragma %q: %s", want, dsn)
		}
	}
	// A file: URI without a query gets ? as the separator; with a query, &.
	if got := BuildDSN("file:x.db"); !strings.Contains(got, "x.db?_pragma") {
		t.Errorf("file: path without query should use ?: %s", got)
	}
	if got := BuildDSN("file:x.db?cache=shared"); !strings.Contains(got, "cache=shared&_pragma") {
		t.Errorf("file: path with query should use &: %s", got)
	}
}

func TestAddColumnsIfMissing(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table t (id integer primary key)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	if !ColumnExists(db, "t", "id") {
		t.Fatal("id column should exist")
	}
	if ColumnExists(db, "t", "name") {
		t.Fatal("name column should not exist yet")
	}

	cols := []ColumnSpec{{Name: "name", Type: "text"}, {Name: "score", Type: "integer not null default 0"}}
	if err := AddColumnsIfMissing(db, "t", cols); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !ColumnExists(db, "t", "name") || !ColumnExists(db, "t", "score") {
		t.Fatal("added columns should exist")
	}
	// Idempotent: a second call with the same (and an extra) spec is a no-op for
	// the existing columns and adds only the new one.
	if err := AddColumnsIfMissing(db, "t", append(cols, ColumnSpec{Name: "city", Type: "text"})); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if !ColumnExists(db, "t", "city") {
		t.Fatal("city column should have been added on the second call")
	}
}
