package tests

import (
	"context"
	"database/sql"
	"testing"

	"dope/dope/web/hostpages"
)

// A scheme knows where each stage sits; these tests hold the database to the
// same standard. Without the coordinates in columns, everything downstream is
// back to parsing `s1-g7` out of a code string — and a replay fixture cannot
// address a бой at all.

type stageRow struct {
	code   string
	block  string
	wave   int
	group  string
	rounds []int
	waves  []int
}

func stageRows(t *testing.T, db *sql.DB, gameID int64) []stageRow {
	t.Helper()
	rows, err := db.Query(`
select s.code, s.block_code, s.wave_index, s.group_code from stages s
where s.game_id = ? order by s.position`, gameID)
	if err != nil {
		t.Fatalf("stages: %v", err)
	}
	defer rows.Close()
	var out []stageRow
	for rows.Next() {
		var row stageRow
		if err := rows.Scan(&row.code, &row.block, &row.wave, &row.group); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		matchRows, err := db.Query(`
select m.round, m.wave from matches m join stages s on s.id = m.stage_id
where s.game_id = ? and s.code = ? order by m.position`, gameID, out[i].code)
		if err != nil {
			t.Fatalf("matches of %s: %v", out[i].code, err)
		}
		for matchRows.Next() {
			var round, wave int
			if err := matchRows.Scan(&round, &wave); err != nil {
				matchRows.Close()
				t.Fatal(err)
			}
			out[i].rounds = append(out[i].rounds, round)
			out[i].waves = append(out[i].waves, wave)
		}
		matchRows.Close()
	}
	return out
}

// Личная СИ's групповой этап: six Groups of one Block, each playing four круги
// at its own стол. The Group is on the stage, the круг is on the бой.
func TestStageGrainOfGroups(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	seedFestPlayers(t, srv.Eng().DB, festID, 18)

	dsl := "[defaults]\nvenues: 2\n\n[scheme]\ntype: roundrobin\ngroups: 2\nteams_in_group: 9\nmatch_size: 3\nthemes: 2\n"
	gameID := createSchemeGame(t, srv.Eng().DB, festID, "si", "Личная СИ", dsl)

	stages := stageRows(t, srv.Eng().DB, gameID)
	if len(stages) != 2 {
		t.Fatalf("этапов = %d, want 2", len(stages))
	}
	for i, stage := range stages {
		if stage.block != "s1" {
			t.Errorf("%s: блок %q, want s1", stage.code, stage.block)
		}
		if want := []string{"1", "2"}[i]; stage.group != want {
			t.Errorf("%s: группа %q, want %q", stage.code, stage.group, want)
		}
		if stage.wave != 1 {
			t.Errorf("%s: заход %d, want 1", stage.code, stage.wave)
		}
		want := []int{1, 1, 1, 2, 2, 2, 3, 3, 3, 4, 4, 4}
		if len(stage.rounds) != len(want) {
			t.Fatalf("%s: боёв = %d, want %d", stage.code, len(stage.rounds), len(want))
		}
		for k, round := range want {
			if stage.rounds[k] != round {
				t.Fatalf("%s: круги = %v, want %v", stage.code, stage.rounds, want)
			}
		}
		// A Group holds one стол, so the three бои of a круг are played one
		// after another — three заходы, not three tables at once.
		wantWaves := []int{1, 2, 3, 1, 2, 3, 1, 2, 3, 1, 2, 3}
		for k, wave := range wantWaves {
			if stage.waves[k] != wave {
				t.Fatalf("%s: заходы = %v, want %v", stage.code, stage.waves, wantWaves)
			}
		}
	}
}

// ЭК's 1/16 финала on six столов is twelve бои in two заходов — two stage rows
// of the same Block and the same Round.
func TestStageGrainOfWaves(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	seedFestTeams(t, srv.Eng().DB, festID, 16)

	dsl := "[defaults]\nvenues: 2\n\n[scheme]\ntype: single_elimination\nteams: 16\nmatch_size: 4\nwinning_places: 2\ntitle.r1: 1/4 финала\n"
	gameID := createSchemeGame(t, srv.Eng().DB, festID, "ek", "ЭК", dsl)

	stages := stageRows(t, srv.Eng().DB, gameID)
	// Четыре боя на двух столах — два захода; потом два боя в один; потом финал.
	want := []struct {
		wave, round, bouts int
	}{{1, 1, 2}, {2, 1, 2}, {1, 2, 2}, {1, 3, 1}}
	if len(stages) != len(want) {
		t.Fatalf("этапов = %d (%v), want %d", len(stages), stages, len(want))
	}
	for i, w := range want {
		stage := stages[i]
		if stage.block != "s1" {
			t.Errorf("%s: блок %q, want s1", stage.code, stage.block)
		}
		if stage.wave != w.wave {
			t.Errorf("%s: заход %d, want %d", stage.code, stage.wave, w.wave)
		}
		if len(stage.rounds) != w.bouts {
			t.Fatalf("%s: боёв = %d, want %d", stage.code, len(stage.rounds), w.bouts)
		}
		for _, round := range stage.rounds {
			if round != w.round {
				t.Errorf("%s: круг боя %d, want %d", stage.code, round, w.round)
			}
		}
	}
}

// A recompile is how a game whose stages predate the coordinates acquires them.
// The update branch used to leave the grain untouched, so a game could never be
// repaired and a moved бой kept a stale круг.
func TestRecompileRefreshesGrain(t *testing.T) {
	srv := newAuthTestServer(t)
	festID, _ := scopedAPITestIDs(t, srv)
	db := srv.Eng().DB
	seedFestTeams(t, db, festID, 16)

	dsl := "[defaults]\nvenues: 4\n\n[scheme]\ntype: single_elimination\nteams: 16\nmatch_size: 4\nwinning_places: 2\n"
	gameID := createSchemeGame(t, db, festID, "ek", "ЭК", dsl)

	// Blank the coordinates the way a pre-v21 game carries them.
	if _, err := db.Exec(`
update stages set block_code = '', wave_index = 0, group_code = '' where game_id = ?`, gameID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
update matches set round = 0 where game_id = ?`, gameID); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := hostpages.RecompileSchemeGameTx(context.Background(), tx, festID, gameID, dsl+"themes: 3\n"); err != nil {
		tx.Rollback()
		t.Fatalf("пересборка: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var blank int
	if err := db.QueryRow(`
select count(*) from stages where game_id = ? and (block_code = '' or wave_index = 0)`, gameID).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	if blank != 0 {
		t.Errorf("после пересборки %d этапов без координат", blank)
	}
	if err := db.QueryRow(`select count(*) from matches where game_id = ? and round = 0`, gameID).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	if blank != 0 {
		t.Errorf("после пересборки %d боёв без круга", blank)
	}
}
