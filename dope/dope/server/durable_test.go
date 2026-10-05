package dopeserver

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Production opens the fest database with synchronous(FULL), so an
// acknowledged write survives a crash. Tests open it through OpenFestDB, which
// skips the fsyncs; this holds the production path to the durable setting.
func TestProductionFestDBIsDurable(t *testing.T) {
	db, err := openFestDB(filepath.Join(t.TempDir(), "prod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Every connection in the pool gets the pragma, so ask more than one.
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

// sqlitextest turns the fsyncs off, so no production file may import it. The
// test seam, testapi.go, is the one non-test file allowed to.
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
		if filepath.ToSlash(path) == "../../dope/server/testapi.go" {
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
