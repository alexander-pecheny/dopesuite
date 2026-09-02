package auditmw

import (
	"context"
	"database/sql"
	"testing"

	"dope/dope/domain/core"

	_ "modernc.org/sqlite"
)

func TestAuditFestIDFromPath(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	eng := &core.Engine{DB: db}

	cases := []struct {
		path string
		want int64
	}{
		{"/api/fest/12/games/3/state", 12},
		{"/host/fest/12/numbers/auto", 12},
		{"/host/venue/12/slot/4", 12},
		{"/host/venue/12/numbers/auto", 12},
		{"/fest/12/game/3", 12},
		{"/host/venue", 0},
		{"/reg/abc", 0},
	}
	for _, c := range cases {
		if got := auditFestIDFromPath(eng, context.Background(), c.path); got != c.want {
			t.Errorf("%s: %d, want %d", c.path, got, c.want)
		}
	}
}
