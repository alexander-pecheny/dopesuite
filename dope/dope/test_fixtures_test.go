package main

import (
	"context"
	"database/sql"
	"testing"
)

func createDefaultFestFixture(t *testing.T, db *sql.DB, state MatchState) int64 {
	t.Helper()
	ctx := t.Context()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer tx.Rollback()

	now := utcNow()
	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}

	schemaJSON := mustJSON(map[string]any{
		"schemaVersion":     2,
		"slug":              "fixture-ek",
		"title":             "Fixture EK",
		"gameType":          defaultGameType,
		"questionValues":    questionValues,
		"regularThemeCount": themeCount,
		"venues":            []VenueView{{Number: 1, Title: defaultVenueTitle}},
		"stages": []map[string]any{
			{
				"code":       "r16",
				"title":      "1/16 финала",
				"stage_type": "matches",
				"position":   1,
				"matches": []map[string]any{
					{"code": defaultMatchCode, "title": state.Title, "venue": 1, "participantCount": len(state.Teams)},
				},
			},
		},
	})

	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, "fixture-ek", "Fixture EK", schemaJSON, now)
	if err != nil {
		t.Fatalf("insert scheme: %v", err)
	}
	festID, err := insertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, ?, null, ?, ?, ?, ?, 1)`, "fixture-ek", "Fixture EK", "", systemID, maxInt64(state.Revision, 1), now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, added_at)
values(?, ?, ?)`, festID, systemID, now); err != nil {
		t.Fatalf("insert organizer: %v", err)
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, defaultGameCode, "Fixture EK", defaultGameType, schemeID, schemaJSON, now, now)
	if err != nil {
		t.Fatalf("insert game: %v", err)
	}
	venueID, err := insertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, 1, ?, ?, ?)`, festID, defaultVenueTitle, now, now)
	if err != nil {
		t.Fatalf("insert venue: %v", err)
	}
	stageID, err := insertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, 'r16', '1/16 финала', 'matches', 1, 'active', '{}')`, festID, gameID)
	if err != nil {
		t.Fatalf("insert stage: %v", err)
	}
	status := "active"
	if state.Finished {
		status = "finished"
	}
	matchID, err := insertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, festID, gameID, stageID, defaultMatchCode, state.Title, len(state.Teams), venueID, status, maxInt64(state.Revision, 1))
	if err != nil {
		t.Fatalf("insert match: %v", err)
	}

	for teamIndex, team := range state.Teams {
		teamID, err := insertReturningID(ctx, tx, `
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
values(?, ?, 'seed', ?, ?, 0)`, matchID, teamIndex, mustJSON(map[string]any{"basket": basket, "number": number}), teamID); err != nil {
			t.Fatalf("insert match slot: %v", err)
		}

		playerIDs := make(map[string]int64, len(team.Roster))
		for rosterOrder, fullName := range team.Roster {
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := insertReturningID(ctx, tx, `
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

		for themeIndex, theme := range team.Themes {
			if err := insertTheme(ctx, tx, matchID, teamID, "regular", themeIndex, playerIDs[theme.Player], theme.Answers); err != nil {
				t.Fatalf("insert regular theme: %v", err)
			}
		}
		for themeIndex, theme := range team.ShootoutThemes {
			if err := insertTheme(ctx, tx, matchID, teamID, "shootout", themeIndex, playerIDs[theme.Player], theme.Answers); err != nil {
				t.Fatalf("insert shootout theme: %v", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_results(match_id, team_id, place)
values(?, ?, ?)`, matchID, teamID, team.Place); err != nil {
			t.Fatalf("insert match result: %v", err)
		}
	}

	if err := recalculateMatchResultsTx(ctx, tx, festID, defaultMatchCode); err != nil {
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

	now := utcNow()
	ownerID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		t.Fatalf("system user: %v", err)
	}
	festID, err := insertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 1)`, "roster-fixture", "Roster fixture", ownerID, now, now)
	if err != nil {
		t.Fatalf("insert fest: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, added_at)
values(?, ?, ?)`, festID, ownerID, now); err != nil {
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
			"themes":       ksiThemeCount,
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
		if _, err := insertReturningID(ctx, tx, `
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
	now := utcNow()
	schemeJSON := mustJSON(scheme)
	stateJSON := mustJSON(state)
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, "fixture-"+code, title, schemeJSON, now)
	if err != nil {
		return 0, err
	}
	return insertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, 'active', 'game', 'game', 1, ?, ?)`,
		festID, code, title, gameType, position, schemeID, schemeJSON, stateJSON, now, now)
}
