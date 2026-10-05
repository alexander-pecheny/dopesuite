package matchedit_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/edit"
	"dope/dope/domain/gamebuild"
	"dope/dope/domain/matchedit"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

// newBrainGame makes a fest of four teams and a брейн knockout of two
// semifinals and a final.
func newBrainGame(t *testing.T) (*core.Engine, core.FestScope) {
	t.Helper()
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	eng := &core.Engine{DB: db}
	var scope core.FestScope
	if _, err := eng.CommitFestWrite(t.Context(), 0, "test", func(ctx context.Context, tx *sql.Tx) (core.FestWrite, error) {
		userID, err := dopeserver.EnsureSystemUser(ctx, tx)
		if err != nil {
			return core.FestWrite{}, err
		}
		if scope.FestID, err = store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, created_by, created_at, updated_at) values('fest', 'Фест', ?, '2026-10-05', '2026-10-05')`, userID); err != nil {
			return core.FestWrite{}, err
		}
		for i := 1; i <= 4; i++ {
			if _, err := tx.Exec(`insert into fest_teams(fest_id, name, position, number) values(?, ?, ?, ?)`, scope.FestID, fmt.Sprintf("Команда %d", i), i, i); err != nil {
				return core.FestWrite{}, err
			}
			if _, err := tx.Exec(`insert into participants(fest_id, roster, name, number) values(?, 'team', ?, ?)`, scope.FestID, fmt.Sprintf("Команда %d", i), i); err != nil {
				return core.FestWrite{}, err
			}
		}
		scope.GameID, err = gamebuild.Create(ctx, tx, gamebuild.Spec{FestID: scope.FestID, Type: "brain", Label: "Брейн",
			DSL: "[scheme]\nkind: single_elimination\nparticipants: 4\n"})
		return core.FestWrite{}, err
	}); err != nil {
		t.Fatal(err)
	}
	return eng, scope
}

// A bout's edits, its finish and the settle are what a host's window of edits
// does: marks go into the document, the finish scores the bout into
// match_results, and the winner moves into the final. No batcher is needed.
func TestABoutIsPatchedFinishedAndSettled(t *testing.T) {
	eng, scope := newBrainGame(t)
	var matchID int64
	var code string
	if err := eng.DB.QueryRow(`select id, code from matches where game_id = ? order by position, id limit 1`, scope.GameID).Scan(&matchID, &code); err != nil {
		t.Fatal(err)
	}
	var cascaded []int64
	write := func(fn func(ctx context.Context, tx *sql.Tx) error) {
		t.Helper()
		if err := eng.WithWriteTx(t.Context(), scope.FestID, "test", fn); err != nil {
			t.Fatal(err)
		}
	}
	write(func(ctx context.Context, tx *sql.Tx) error {
		path, _ := json.Marshal([]any{"teams", 0, "rows", 0, "mark"})
		value, _ := json.Marshal("right")
		return matchedit.PatchTx(ctx, tx, scope, matchID, []edit.PatchOp{{Op: "set", Path: mustPath(t, path), Value: value}})
	})
	change := &matchedit.Change{MatchID: matchID, Code: code}
	write(func(ctx context.Context, tx *sql.Tx) (err error) {
		if err := matchedit.FinishTx(ctx, tx, matchID, true); err != nil {
			return err
		}
		finished := true
		change.FinishTo = &finished
		cascaded, err = matchedit.SettleTx(ctx, tx, scope, []*matchedit.Change{change})
		return err
	})
	var results int
	if err := eng.DB.QueryRow(`select count(*) from match_results where match_id = ? and place = 1`, matchID).Scan(&results); err != nil || results != 1 {
		t.Fatalf("%d winners in match_results (%v), want 1", results, err)
	}
	if change.Revision == 0 || len(cascaded) == 0 {
		t.Fatalf("revision %d, cascaded %v: the settle should record a revision and move the winner on", change.Revision, cascaded)
	}
}

func mustPath(t *testing.T, raw []byte) []json.RawMessage {
	t.Helper()
	var path []json.RawMessage
	if err := json.Unmarshal(raw, &path); err != nil {
		t.Fatal(err)
	}
	return path
}
