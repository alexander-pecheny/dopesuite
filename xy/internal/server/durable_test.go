package server

import (
	"database/sql"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pecheny.me/dopecore/sqlitex/sqlitextest"
)

// openTestDB opens and migrates a database the way openDB does, but without
// the fsyncs: a test's database is thrown away when the test ends, and on a
// network disk the fsyncs were most of a test's time.
func openTestDB(path string) (*sql.DB, error) { return sqlitextest.Open(path, migrate) }

// Production opens with synchronous(FULL), so an acknowledged write survives a
// crash; only openTestDB skips the fsyncs.
func TestProductionDBIsDurable(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "prod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 3 {
		var sync int
		if err := db.QueryRow("pragma synchronous").Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if sync != 2 {
			t.Fatalf("production synchronous = %d, want 2 (FULL)", sync)
		}
	}
}

// sqlitextest turns the fsyncs off, so no production file may import it.
func TestOnlyTestCodeOpensWithoutFsync(t *testing.T) {
	const banned = "pecheny.me/dopecore/sqlitex/sqlitextest"
	root := filepath.Join("..", "..") // the module root
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "testdata" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); p == banned {
				t.Errorf("%s imports %s; only tests may open SQLite without fsync", path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
