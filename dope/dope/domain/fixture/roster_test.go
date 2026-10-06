package fixture_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/fixture"
	"dope/dope/domain/imports"
	dopeserver "dope/dope/server"
)

// The fixture's teams have no rating id, so they must be teams a host made.
// The fest roster is the import plus the host's edits (ADR-0024), and every
// roster write drops a row that has no rating id and is not marked hand. When
// the fixture wrote plain rows, adding one hand team deleted all sixteen.
func TestAHandTeamKeepsTheFixtureTeams(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	eng := &core.Engine{DB: db}
	var owner int64
	if err := eng.WithWriteTx(ctx, 0, "test", func(ctx context.Context, tx *sql.Tx) (err error) {
		owner, err = dopeserver.EnsureSystemUser(ctx, tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	festID, err := fixture.Build(ctx, db, fixture.Options{Slug: "fixture", Owner: owner})
	if err != nil {
		t.Fatal(err)
	}
	count := func() (teams, players int) {
		t.Helper()
		if err := db.QueryRow(`select count(*) from fest_teams where fest_id = ? and deleted = 0`, festID).Scan(&teams); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`
select count(*) from fest_team_players ftp join fest_teams t on t.id = ftp.team_id
where t.fest_id = ? and t.deleted = 0`, festID).Scan(&players); err != nil {
			t.Fatal(err)
		}
		return teams, players
	}
	teamsBefore, playersBefore := count()
	if err := eng.WithWriteTx(ctx, festID, "test", func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := imports.CreateHandTeamTx(ctx, tx, festID, imports.TeamInput{Name: "Новая"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	teamsAfter, playersAfter := count()
	if teamsAfter != teamsBefore+1 || playersAfter != playersBefore {
		t.Fatalf("teams %d -> %d, players %d -> %d; want one team more and the same players",
			teamsBefore, teamsAfter, playersBefore, playersAfter)
	}
}
