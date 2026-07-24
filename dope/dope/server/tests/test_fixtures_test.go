package tests

import (
	"context"
	"database/sql"
	"dope/dope/domain/games"
	"dope/dope/platform/util"
	dopeserver "dope/dope/server"
	"dope/dope/storage/store"
	"testing"
)

func createDefaultFestFixture(t *testing.T, db *sql.DB, state store.MatchState) int64 {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer tx.Rollback()

	now := util.UtcNow()
	systemID, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}

	schemaJSON := util.MustJSON(map[string]any{
		"schemaVersion":     2,
		"slug":              "fixture-ek",
		"title":             "Fixture EK",
		"gameType":          games.Default,
		"questionValues":    store.QuestionValues,
		"regularThemeCount": store.ThemeCount,
		"venues":            []store.VenueView{{Number: 1, Title: dopeserver.DefaultVenueTitle}},
		"stages": []map[string]any{
			{
				"code":       "r16",
				"title":      "1/16 финала",
				"stage_type": "matches",
				"position":   1,
				"matches": []map[string]any{
					{"code": dopeserver.DefaultMatchCode, "title": state.Title, "venue": 1, "participantCount": len(state.Teams)},
				},
			},
		},
	})

	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, "fixture-ek", "Fixture EK", schemaJSON, now)
	if err != nil {
		t.Fatalf("insert scheme: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, ?, null, ?, ?, ?, ?, 1)`, "fixture-ek", "Fixture EK", "", systemID, util.MaxInt64(state.Revision, 1), now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, dopeserver.DefaultGameCode, "Fixture EK", games.Default, schemeID, schemaJSON, now, now)
	if err != nil {
		t.Fatalf("insert game: %v", err)
	}
	venueID, err := store.InsertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, 1, ?, ?, ?)`, festID, dopeserver.DefaultVenueTitle, now, now)
	if err != nil {
		t.Fatalf("insert venue: %v", err)
	}
	stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, 'r16', '1/16 финала', 'matches', 1, 'active', '{}')`, festID, gameID)
	if err != nil {
		t.Fatalf("insert stage: %v", err)
	}
	status := "active"
	if state.Finished {
		status = "finished"
	}
	matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, festID, gameID, stageID, dopeserver.DefaultMatchCode, state.Title, len(state.Teams), venueID, status, util.MaxInt64(state.Revision, 1))
	if err != nil {
		t.Fatalf("insert match: %v", err)
	}

	for teamIndex, team := range state.Teams {
		teamID, err := store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, '')`, festID, team.Name)
		if err != nil {
			t.Fatalf("insert team: %v", err)
		}
		basket := 1
		number := teamIndex + 1
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, ?, ?, ?, null)`, gameID, basket, number, teamID); err != nil {
			t.Fatalf("insert assignment: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, 'seed', ?, ?, 0)`, matchID, teamIndex, util.MustJSON(map[string]any{"basket": basket, "number": number}), teamID); err != nil {
			t.Fatalf("insert match slot: %v", err)
		}

		playerIDs := make(map[string]int64, len(team.Roster))
		for rosterOrder, member := range team.Roster {
			fullName := member.Name
			firstName, lastName := dopeserver.SplitPlayerName(fullName)
			playerID, err := store.InsertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
			if err != nil {
				t.Fatalf("insert player: %v", err)
			}
			if _, err := tx.ExecContext(ctx, `
insert into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
				t.Fatalf("insert team player: %v", err)
			}
			playerIDs[fullName] = playerID
		}

		if _, err := store.MutateMatchBlobTx(ctx, tx, matchID, func(blob *store.MatchBlob) error {
			for themeIndex, theme := range team.Themes {
				if id := playerIDs[theme.Player]; id != 0 {
					blob.SetPlayer(teamID, "regular", themeIndex, id)
				}
				blob.EnsureTheme(teamID, "regular", themeIndex)
				for ai, mark := range theme.Answers {
					blob.SetAnswer(teamID, "regular", themeIndex, ai, mark)
				}
			}
			for themeIndex, theme := range team.ShootoutThemes {
				if id := playerIDs[theme.Player]; id != 0 {
					blob.SetPlayer(teamID, "shootout", themeIndex, id)
				}
				blob.EnsureTheme(teamID, "shootout", themeIndex)
				for ai, mark := range theme.Answers {
					blob.SetAnswer(teamID, "shootout", themeIndex, ai, mark)
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("write match blob: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place)
values(?, ?, ?)`, matchID, teamID, team.Place); err != nil {
			t.Fatalf("insert match result: %v", err)
		}
	}

	if err := dopeserver.RecalculateMatchResultsTx(ctx, tx, festID, dopeserver.DefaultMatchCode); err != nil {
		t.Fatalf("recalculate results: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture: %v", err)
	}
	return festID
}

func createRosterPropagationFixture(t *testing.T, db *sql.DB) (int64, int64, int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer tx.Rollback()

	now := util.UtcNow()
	ownerID, err := dopeserver.EnsureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 1)`, "roster-fixture", "Roster fixture", ownerID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, ownerID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}

	chgkGameID, err := insertJSONGameFixture(ctx, tx, festID, "chgk", "ЧГК fixture", "od", 1,
		map[string]any{
			"title":    "ЧГК fixture",
			"gameType": "od",
			"tourComp": []int{3},
			"nTeams":   0,
			"teams":    []map[string]string{},
		},
		map[string]any{
			"teams":     []map[string]string{},
			"entries":   [][]int{{}},
			"completed": []bool{false, false, false},
		})
	if err != nil {
		t.Fatalf("insert chgk game: %v", err)
	}
	ksiGameID, err := insertJSONGameFixture(ctx, tx, festID, "ksi", "КСИ fixture", "ksi", 2,
		map[string]any{
			"title":        "КСИ fixture",
			"gameType":     "ksi",
			"participants": []string{},
			"themes":       games.KSIThemeCount,
		},
		map[string]any{
			"participants": []string{},
			"themes":       []map[string]any{{"answers": [][]string{}}},
			"finished":     false,
		})
	if err != nil {
		t.Fatalf("insert ksi game: %v", err)
	}
	for _, name := range []string{"Команда A", "Команда B", "Команда C", "Команда D"} {
		if _, err := store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, '')`, festID, name); err != nil {
			t.Fatalf("insert existing team: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit fixture: %v", err)
	}
	return festID, chgkGameID, ksiGameID
}

func insertJSONGameFixture(ctx context.Context, tx *sql.Tx, festID int64, code, title, gameType string, position int, scheme, state map[string]any) (int64, error) {
	now := util.UtcNow()
	schemeJSON := util.MustJSON(scheme)
	stateJSON := util.MustJSON(state)
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, "fixture-"+code, title, schemeJSON, now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'game', 'game', 1, ?, ?)`,
		festID, code, title, gameType, position, schemeID, schemeJSON, now, now)
	if err != nil {
		return 0, err
	}
	stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json)
values(?, ?, 'main', '', 'matches', 'matches', 1, 'active', '{}')`, festID, gameID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, status, revision, state_json)
values(?, ?, ?, 'main', ?, 1, 0, 'active', 0, ?)`, festID, gameID, stageID, title, stateJSON); err != nil {
		return 0, err
	}
	return gameID, nil
}
