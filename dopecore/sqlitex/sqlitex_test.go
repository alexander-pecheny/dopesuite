package sqlitex

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildDSN(t *testing.T) {
	cases := map[string]string{
		"/var/lib/x.db":        "file:/var/lib/x.db?_pragma=busy_timeout(5000)",
		"file:mem?mode=memory": "file:mem?mode=memory&_pragma=busy_timeout(5000)",
		"file:/tmp/x.db":       "file:/tmp/x.db?_pragma=busy_timeout(5000)",
	}
	for path, prefix := range cases {
		if got := BuildDSN(path); !strings.HasPrefix(got, prefix) || !strings.Contains(got, "journal_mode(WAL)") {
			t.Errorf("BuildDSN(%q) = %q", path, got)
		}
	}
}

func TestIsUniqueViolation(t *testing.T) {
	cases := map[error]bool{
		nil: false,
		errors.New("constraint failed: UNIQUE constraint failed: users.username (2067)"): true,
		errors.New("CONSTRAINT failed"):  true,
		errors.New("database is locked"): false,
	}
	for err, want := range cases {
		if got := IsUniqueViolation(err); got != want {
			t.Errorf("%v: got %v want %v", err, got, want)
		}
	}
}
