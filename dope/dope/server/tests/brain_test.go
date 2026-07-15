package tests

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	"dope/dope/web/hostpages"
)

// End-to-end брейн group stage: create the game (bracket materialises), draw
// teams via the shared seed import, then edit a бой and confirm the questions
// live in match_questions (not themes) and score into match_results.
func TestBrainGroupStageCreateSeedAndScore(t *testing.T) {
	db, err := dopeserver.OpenFestDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	festID, brainGameID := createBrainFixture(t, db)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	scope := dopeserver.FestScope{FestID: festID, GameID: brainGameID}

	// Draw the three KSI teams into the brain group's flat seeds.
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), t.Context(), scope); err != nil {
		t.Fatalf("import seeds: %v", err)
	}

	ctx := t.Context()
	// Seed provisioning must create question rows (брейн), not EK themes.
	if got := countRows(t, db, `select count(*) from match_questions mq join matches m on m.id = mq.match_id where m.game_id = ?`, brainGameID); got == 0 {
		t.Fatalf("no match_questions rows provisioned for brain game")
	}
	if got := countRows(t, db, `select count(*) from themes th join matches m on m.id = th.match_id where m.game_id = ?`, brainGameID); got != 0 {
		t.Fatalf("brain game created %d EK theme rows; brain has no themes", got)
	}

	// Edit бой gA-1: team 0 takes questions 1 and 2, team 1 takes none.
	bout, err := store.LoadBrainMatch(ctx, db, festID, "gA-1")
	if err != nil {
		t.Fatalf("load bout: %v", err)
	}
	if len(bout.Teams) != 2 || bout.QuestionCount != 3 {
		t.Fatalf("bout = %d teams / %d questions, want 2 / 3", len(bout.Teams), bout.QuestionCount)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	t0, t1 := bout.Teams[0].TeamID, bout.Teams[1].TeamID
	if err := store.SetBrainQuestionMarkTx(ctx, tx, bout.MatchID, t0, 0, "right"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := store.SetBrainQuestionMarkTx(ctx, tx, bout.MatchID, t0, 1, "right"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `update matches set status = 'finished' where id = ?`, bout.MatchID); err != nil {
		t.Fatalf("finish: %v", err)
	}
	scored, err := store.LoadBrainMatch(ctx, tx, festID, "gA-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := store.RecalculateBrainMatchResultsTx(ctx, tx, scored); err != nil {
		t.Fatalf("recalc: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// team 0: total 2, О 2, place 1; team 1: total 0, О 0, place 2.
	total0, plus0, place0 := matchResult(t, db, bout.MatchID, t0)
	total1, plus1, place1 := matchResult(t, db, bout.MatchID, t1)
	if total0 != 2 || plus0 != 2 || place0 != 1 {
		t.Fatalf("winner result = total %d / О %d / place %d, want 2 / 2 / 1", total0, plus0, place0)
	}
	if total1 != 0 || plus1 != 0 || place1 != 2 {
		t.Fatalf("loser result = total %d / О %d / place %d, want 0 / 0 / 2", total1, plus1, place1)
	}
}

func createBrainFixture(t *testing.T, db *sql.DB) (int64, int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	now := util.UtcNow()
	systemID, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(null, 'Brain fixture', '', null, ?, 1, ?, ?, 1)`, systemID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}

	// A finished KSI game supplies three ranked teams for the draw.
	answers := [][]string{
		{"right", "", "", "", ""},
		{"", "right", "", "", ""},
		{"", "", "right", "", ""},
	}
	if _, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "КСИ", "ksi", 1,
		map[string]any{"schemaVersion": 2, "title": "КСИ", "gameType": "ksi", "participants": []string{"A", "B", "C"}, "themes": 1},
		map[string]any{"participants": []string{"A", "B", "C"}, "themes": []map[string]any{{"answers": answers}}, "finished": true}); err != nil {
		t.Fatalf("insert ksi: %v", err)
	}

	// One group of three teams, three questions per бой.
	brainGameID, err := hostpages.CreateBrainGameTx(ctx, tx, festID, 1, 3, 3)
	if err != nil {
		t.Fatalf("create brain: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return festID, brainGameID
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func matchResult(t *testing.T, db *sql.DB, matchID, teamID int64) (total, plus, place int) {
	t.Helper()
	var placeF float64
	if err := db.QueryRowContext(context.Background(),
		`select total, plus, place from match_results where match_id = ? and team_id = ?`, matchID, teamID).
		Scan(&total, &plus, &placeF); err != nil {
		t.Fatalf("match_result: %v", err)
	}
	return total, plus, int(placeF)
}
