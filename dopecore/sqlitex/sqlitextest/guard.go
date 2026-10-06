package sqlitextest

import (
	"database/sql"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// importPath is this package, the import Guard looks for.
const importPath = "pecheny.me/dopecore/sqlitex/sqlitextest"

// syncFull is what pragma synchronous reads back as under synchronous(FULL).
const syncFull = 2

// poolProbes is how many times RequireDurable asks: every connection in the
// pool gets the pragma, so asking once would check only one of them.
const poolProbes = 3

// RequireDurable fails t unless open, the app's production opener, gives a
// database whose commits fsync (synchronous FULL). It is the other half of
// Guard: tests open without the fsyncs, production never does.
func RequireDurable(t testing.TB, open func(path string) (*sql.DB, error)) {
	t.Helper()
	db, err := open(filepath.Join(t.TempDir(), "prod.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range poolProbes {
		var sync int
		if err := db.QueryRow("pragma synchronous").Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if sync != syncFull {
			t.Fatalf("production synchronous = %d, want %d (FULL)", sync, syncFull)
		}
	}
}

// Guard fails t for every non-test Go file under moduleRoot that imports this
// package, except the files named in seams (paths as WalkDir prints them from
// moduleRoot, e.g. "../../dope/server/testapi.go"). A seam is the one exported
// test helper an app's external test package goes through.
func Guard(t testing.TB, moduleRoot string, seams ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, s := range seams {
		allowed[filepath.ToSlash(s)] = true
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name(), path, moduleRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[filepath.ToSlash(path)] {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); p == importPath {
				t.Errorf("%s imports %s; only tests may open SQLite without fsync", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func skipDir(name, path, root string) bool {
	return name == "node_modules" || name == "testdata" || (strings.HasPrefix(name, ".") && path != root)
}
