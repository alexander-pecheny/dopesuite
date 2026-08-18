package tests

import (
	"context"
	"database/sql"
	"reflect"
	"sync/atomic"
	"testing"

	"dope/dope/storage/store"
)

type countingQueryer struct {
	db *sql.DB
	n  atomic.Int64
}

func (c *countingQueryer) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.db.QueryContext(ctx, q, args...)
}

func (c *countingQueryer) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.n.Add(1)
	return c.db.QueryRowContext(ctx, q, args...)
}

// A whole game's бои load in a handful of statements, not a handful per бой:
// the Сетка and the export read every one, and the pool is shared with the
// write path.
func TestGameMatchesLoadInFourQueries(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 8)
	brainID := createSchemeGame(t, db, festID, "brain", "Брейн", "[scheme]\nkind: roundrobin\ngroup_size: 8\nmatch_size: 2\n")
	ekID := createSchemeGame(t, db, festID, "ek", "ЭК", "[scheme]\nkind: roundrobin\ngroup_size: 4\nmatch_size: 2\ngroups: 2\n")

	for _, game := range []int64{ekID, brainID} {
		q := &countingQueryer{db: db}
		all, err := store.LoadMatchStates(context.Background(), q, store.MatchSelector{FestID: festID, GameID: game})
		if err != nil {
			t.Fatal(err)
		}
		if len(all) < 2 {
			t.Fatalf("game %d: %d бои loaded", game, len(all))
		}
		if n := q.n.Load(); n > 4 {
			t.Errorf("game %d: %d бои took %d statements, want at most 4", game, len(all), n)
		}
		for _, one := range all {
			single, err := store.LoadMatchState(context.Background(), db, store.MatchSelector{FestID: festID, GameID: game, MatchID: one.MatchID})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(single, one) {
				t.Errorf("бой %s differs between the batch and the single loader:\n%+v\n%+v", one.Code, single, one)
			}
		}
	}
}
