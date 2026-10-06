package sqlitextest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"pecheny.me/dopecore/sqlitex"
)

// recorder is a testing.TB that keeps the failures instead of failing.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGuardFlagsProductionImportsOnly(t *testing.T) {
	root := t.TempDir()
	imp := "package p\nimport _ \"" + importPath + "\"\n"
	write(t, filepath.Join(root, "app", "prod.go"), imp)
	write(t, filepath.Join(root, "app", "prod_test.go"), imp)
	write(t, filepath.Join(root, "app", "seam.go"), imp)
	write(t, filepath.Join(root, "app", "testdata", "x.go"), imp)
	write(t, filepath.Join(root, "app", "clean.go"), "package p\n")

	rec := &recorder{TB: t}
	Guard(rec, root, filepath.Join(root, "app", "seam.go"))
	if len(rec.errs) != 1 {
		t.Fatalf("want one failure (prod.go), got %q", rec.errs)
	}
}

func TestRequireDurableAcceptsProduction(t *testing.T) {
	RequireDurable(t, func(path string) (*sql.DB, error) { return sqlitex.Open(path, nil) })
}
