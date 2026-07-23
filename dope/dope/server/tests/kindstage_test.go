package tests

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"dope/dope/domain/resolver"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
)

// A kind='rr' stage ranks live into stage_standings on every resolve, but its
// rank refs fill downstream slots only once every bout of the stage is
// finished — provisional group order must not leak into the playoff.
func TestKindStageStandingsAndRankResolution(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mustExec := func(q string, args ...any) sql.Result {
		t.Helper()
		res, err := db.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return res
	}
	id := func(res sql.Result) int64 {
		n, _ := res.LastInsertId()
		return n
	}

	festID := id(mustExec(`insert into fests(slug, title, description, revision, created_at, updated_at) values('kf', 'KF', '', 1, 'now', 'now')`))
	gameID := id(mustExec(`insert into games(fest_id, code, title, game_type, position, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, 'g', 'G', 'ek', 1, '{}', '{}', 'active', 'fest', 'fest', 1, 'now', 'now')`, festID))
	teamA := id(mustExec(`insert into teams(fest_id, name, city) values(?, 'A', '')`, festID))
	teamB := id(mustExec(`insert into teams(fest_id, name, city) values(?, 'B', '')`, festID))
	teamC := id(mustExec(`insert into teams(fest_id, name, city) values(?, 'C', '')`, festID))

	groupID := id(mustExec(`insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json)
values(?, ?, 'grp', 'Группа', 'matches', 'rr', 1, 'active', '{"config":{}}')`, festID, gameID))
	finalStage := id(mustExec(`insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json)
values(?, ?, 'fin', 'Финал', 'matches', 'matches', 2, 'active', '{}')`, festID, gameID))

	bout := func(code string, position int, a, b int64) int64 {
		matchID := id(mustExec(`insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, status, revision)
values(?, ?, ?, ?, ?, ?, 2, 'active', 0)`, festID, gameID, groupID, code, code, position))
		mustExec(`insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id) values(?, 0, 'seed', '{}', ?)`, matchID, a)
		mustExec(`insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id) values(?, 1, 'seed', '{}', ?)`, matchID, b)
		return matchID
	}
	m1 := bout("grp-1", 1, teamA, teamB)
	m2 := bout("grp-2", 2, teamA, teamC)
	m3 := bout("grp-3", 3, teamB, teamC)

	finalID := id(mustExec(`insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, status, revision)
values(?, ?, ?, 'fin-1', 'Финал', 1, 2, 'active', 0)`, festID, gameID, finalStage))
	mustExec(`insert into match_slots(match_id, slot_index, source_type, source_ref_json) values(?, 0, 'reseed', '{"stage":"grp","rank":1}')`, finalID)
	mustExec(`insert into match_slots(match_id, slot_index, source_type, source_ref_json) values(?, 1, 'reseed', '{"stage":"grp","rank":2}')`, finalID)

	finish := func(matchID int64, takenA, takenB int, a, b int64) {
		t.Helper()
		mustExec(`insert into match_results(match_id, team_id, place, total) values(?, ?, ?, ?)
on conflict(match_id, team_id) do update set place = excluded.place, total = excluded.total`,
			matchID, a, boolPlace(takenA >= takenB), takenA)
		mustExec(`insert into match_results(match_id, team_id, place, total) values(?, ?, ?, ?)
on conflict(match_id, team_id) do update set place = excluded.place, total = excluded.total`,
			matchID, b, boolPlace(takenB >= takenA), takenB)
		mustExec(`update matches set status = 'finished' where id = ?`, matchID)
	}
	resolve := func() {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.ResolveGameSlotsTx(ctx, tx, gameID); err != nil {
			tx.Rollback()
			t.Fatalf("resolve: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	// A beats B; standings appear live, but the final's slots stay open while
	// grp-2/grp-3 are unplayed.
	finish(m1, 5, 2, teamA, teamB)
	resolve()
	if n := standingsCount(t, db, groupID); n == 0 {
		t.Fatal("no live standings after first finished bout")
	}
	if got := slotTeams(t, db, gameID, "fin-1"); !allZero(got) {
		t.Fatalf("final slots filled from provisional standings: %v", got)
	}

	// A beats C, C beats B → points A=4, C=2, B=0. All bouts done → rank refs fill.
	finish(m2, 5, 1, teamA, teamC)
	finish(m3, 1, 4, teamB, teamC)
	resolve()
	entries := stageStandings(t, db, groupID)
	if len(entries) != 3 || entries[0] != teamA || entries[1] != teamC || entries[2] != teamB {
		t.Fatalf("standings = %v, want [A C B] = [%d %d %d]", entries, teamA, teamC, teamB)
	}
	got := slotTeams(t, db, gameID, "fin-1")
	if len(got) != 2 || got[0] != teamA || got[1] != teamC {
		t.Fatalf("final slots = %v, want [%d %d]", got, teamA, teamC)
	}
}

func boolPlace(winner bool) float64 {
	if winner {
		return 1
	}
	return 2
}

func standingsCount(t *testing.T, db *sql.DB, stageID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`select count(*) from stage_standings where stage_id = ?`, stageID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stageStandings(t *testing.T, db *sql.DB, stageID int64) []int64 {
	t.Helper()
	out, err := store.CollectRows(context.Background(), db,
		`select participant_id from stage_standings where stage_id = ? order by rank`,
		[]any{stageID}, func(rows *sql.Rows) (int64, error) {
			var id int64
			return id, rows.Scan(&id)
		})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
