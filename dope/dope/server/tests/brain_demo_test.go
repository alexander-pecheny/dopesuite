package tests

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"dope/dope/domain/core"
	"dope/dope/domain/imports"
	"dope/dope/platform/realtime"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	"dope/dope/web/hostpages"

	"pecheny.me/dopecore/authcred"
)

// demoHostUsername / demoHostPassword are the credentials of the seeded host, so
// the demo DB can be edited (бой protocol) after logging in at /login.
const demoHostUsername = "host"
const demoHostPassword = "braindemo"

// TestSeedBrainDemo builds a browsable demo DB with a public fest whose брейн
// group stage (6 groups × 4 teams) is seeded and partly scored, so the cross-
// tables render live. It is skipped unless BRAIN_DEMO_DB names an output path:
//
//	BRAIN_DEMO_DB=$PWD/brain-demo.db go test ./dope/server/tests/ -run TestSeedBrainDemo -count=1
//	DOPE_DB=$PWD/brain-demo.db just dev-web-only   # then open /fest/brain-demo/game/brain-1
func TestSeedBrainDemo(t *testing.T) {
	dbPath := os.Getenv("BRAIN_DEMO_DB")
	if dbPath == "" {
		t.Skip("set BRAIN_DEMO_DB to an output path to build the demo DB")
	}
	_ = os.Remove(dbPath)
	db, err := dopeserver.OpenFestDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	const groups, perGroup, questions = 6, 4, 5
	teamCount := groups * perGroup

	festID, brainGameID := seedBrainDemoFixture(t, db, teamCount)
	srv := dopeserver.NewTestServer(func(e *core.Engine) {
		e.DB = db
		e.RT = realtime.NewManager()
	})
	if _, _, _, err := imports.ImportSeedsFromKSI(srv.Eng(), ctx, dopeserver.FestScope{FestID: festID, GameID: brainGameID}); err != nil {
		t.Fatalf("draw: %v", err)
	}
	addDemoRosters(t, db, festID)
	scoreBrainDemo(t, db, festID)

	var slug string
	_ = db.QueryRowContext(ctx, `select coalesce(slug, cast(id as text)) from fests where id = ?`, festID).Scan(&slug)
	var gameCode string
	_ = db.QueryRowContext(ctx, `select coalesce(slug, cast(id as text)) from games where id = ?`, brainGameID).Scan(&gameCode)
	t.Logf("demo DB ready: %s", dbPath)
	t.Logf("public (read-only): /fest/%s/game/%s", slug, gameCode)
	t.Logf("host  (editable):   log in at /login as %s / %s, then /host/fest/%s/game/%s",
		demoHostUsername, demoHostPassword, slug, gameCode)
}

func seedBrainDemoFixture(t *testing.T, db *sql.DB, teamCount int) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	now := util.UtcNow()
	systemID, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values('brain-demo', 'Демо: Брейн', '', null, ?, 1, ?, ?, 1)`, systemID, now, now)
	if err != nil {
		t.Fatalf("fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		t.Fatalf("organizer: %v", err)
	}

	// A password-loginable host user with edit rights on the fest.
	hash, err := authcred.HashPassword(demoHostPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	hostUserID, err := store.InsertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, password_hash, created_at, updated_at)
values(null, null, ?, 0, ?, ?, ?)`, demoHostUsername, hash, now, now)
	if err != nil {
		t.Fatalf("host user: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `insert into fest_organizers(fest_id, user_id, role, added_at) values(?, ?, 'admin', ?)`, festID, hostUserID, now); err != nil {
		t.Fatalf("host organizer: %v", err)
	}

	participants := make([]string, teamCount)
	answers := make([][]string, teamCount)
	for i := range participants {
		participants[i] = fmt.Sprintf("Команда %02d", i+1)
		answers[i] = []string{"right", "", "", "", ""}
	}
	if _, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "Посев (КСИ)", "ksi", 1,
		map[string]any{"schemaVersion": 2, "title": "Посев (КСИ)", "gameType": "ksi", "participants": participants, "themes": 1},
		map[string]any{"participants": participants, "themes": []map[string]any{{"answers": answers}}, "finished": true}); err != nil {
		t.Fatalf("ksi: %v", err)
	}
	brainGameID, err := hostpages.CreateBrainGameTx(ctx, tx, festID, 6, 4, 5)
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	// A slug gives the game a friendly URL (/fest/brain-demo/game/brain).
	if _, err := tx.ExecContext(ctx, `update games set slug = 'brain' where id = ?`, brainGameID); err != nil {
		t.Fatalf("slug: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return festID, brainGameID
}

// addDemoRosters gives every seeded team three players so the бой editor's player
// pickers are populated.
func addDemoRosters(t *testing.T, db *sql.DB, festID int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	teams, err := store.CollectRows(ctx, tx, `select id, name from teams where fest_id = ? order by id`, []any{festID},
		func(rows *sql.Rows) (struct {
			id   int64
			name string
		}, error) {
			var r struct {
				id   int64
				name string
			}
			return r, rows.Scan(&r.id, &r.name)
		})
	if err != nil {
		t.Fatalf("teams: %v", err)
	}
	for _, team := range teams {
		for p := 1; p <= 3; p++ {
			playerID, err := store.InsertReturningID(ctx, tx, `insert into players(fest_id, first_name, last_name) values(?, ?, ?)`,
				festID, "Игрок", fmt.Sprintf("%s·%d", team.name, p))
			if err != nil {
				t.Fatalf("player: %v", err)
			}
			if _, err := tx.ExecContext(ctx, `insert into team_players(team_id, player_id, roster_order) values(?, ?, ?)`, team.id, playerID, p); err != nil {
				t.Fatalf("team_player: %v", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// scoreBrainDemo fills a deterministic score into every бой and finishes it, so
// the cross-tables render populated standings.
func scoreBrainDemo(t *testing.T, db *sql.DB, festID int64) {
	t.Helper()
	ctx := context.Background()
	codes, err := store.CollectRows(ctx, db, `
select m.code from matches m join games g on g.id = m.game_id
where g.fest_id = ? and g.game_type = 'brain' order by m.id`, []any{festID},
		func(rows *sql.Rows) (string, error) {
			var code string
			return code, rows.Scan(&code)
		})
	if err != nil {
		t.Fatalf("codes: %v", err)
	}
	for i, code := range codes {
		bout, err := store.LoadBrainMatch(ctx, db, festID, code)
		if err != nil || len(bout.Teams) != 2 {
			t.Fatalf("load %s: %v", code, err)
		}
		// Alternate a 3:1 / 2:2 / 1:0 pattern so groups get varied standings.
		takeA := (i % 3) + 1
		takeB := i % 2
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		for q := 0; q < bout.QuestionCount; q++ {
			if q < takeA {
				_ = store.SetBrainQuestionMarkTx(ctx, tx, bout.MatchID, bout.Teams[0].TeamID, q, "right")
			}
			if q < takeB {
				_ = store.SetBrainQuestionMarkTx(ctx, tx, bout.MatchID, bout.Teams[1].TeamID, q, "right")
			}
		}
		if _, err := tx.ExecContext(ctx, `update matches set status = 'finished' where id = ?`, bout.MatchID); err != nil {
			t.Fatalf("finish: %v", err)
		}
		scored, err := store.LoadBrainMatch(ctx, tx, festID, code)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if err := store.RecalculateBrainMatchResultsTx(ctx, tx, scored); err != nil {
			t.Fatalf("recalc: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}
