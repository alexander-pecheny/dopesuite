package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

const (
	devUserUsername      = "pecheny"
	devPasswordEnv       = "DOPE_DEV_PASSWORD"
	testTournamentSlug   = "test"
	testTournamentTitle  = "Тестовый турнир"
	testTournamentDesc   = "Тестовый турнир со всеми тремя играми (ЧГК, СИ, ЭК) для локальных проверок."
)

// ensureDevUser makes sure a local-only user "pecheny" exists and has a
// password set. If the user has no password yet, a random one is generated
// (or read from DOPE_DEV_PASSWORD) and logged to stdout so the developer can
// see it on bootstrap.
func ensureDevUser(ctx context.Context, db *sql.DB) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var (
		userID   int64
		hash     sql.NullString
		salt     sql.NullString
		username sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
select id, username, password_hash, password_salt from users where username = ?`, devUserUsername).Scan(
		&userID, &username, &hash, &salt)
	if errors.Is(err, sql.ErrNoRows) {
		now := utcNow()
		userID, err = insertReturningID(ctx, tx, `
insert into users(telegram_user_id, telegram_username, username, is_system, created_at, updated_at)
values(null, null, ?, 0, ?, ?)`, devUserUsername, now, now)
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if !hash.Valid || !salt.Valid {
		password := strings.TrimSpace(os.Getenv(devPasswordEnv))
		if password == "" {
			password, err = randomBase32(8)
			if err != nil {
				return 0, err
			}
		}
		newSalt, err := newPasswordSalt()
		if err != nil {
			return 0, err
		}
		pwHash := hashPassword(password, newSalt)
		if _, err := tx.ExecContext(ctx, `
update users set password_hash = ?, password_salt = ?, updated_at = ? where id = ?`,
			pwHash, newSalt, utcNow(), userID); err != nil {
			return 0, err
		}
		log.Printf("=== dev login: username=%s password=%s ===", devUserUsername, password)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return userID, nil
}

// ensureTestTournament creates a tournament with slug "test" containing three
// games (ChGK / СИ / ЭК) for local testing. If the tournament already exists,
// it is left as-is.
func ensureTestTournament(ctx context.Context, db *sql.DB, ownerID int64) error {
	var existing int64
	err := db.QueryRowContext(ctx, `select id from tournaments where slug = ?`, testTournamentSlug).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := utcNow()
	tournamentID, err := insertReturningID(ctx, tx, `
insert into tournaments(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, ?, null, ?, 1, ?, ?, 1)`,
		testTournamentSlug, testTournamentTitle, testTournamentDesc, ownerID, now, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into tournament_organizers(tournament_id, user_id, added_at)
values(?, ?, ?)`, tournamentID, ownerID, now); err != nil {
		return err
	}

	if err := seedTestChGKGame(ctx, tx, tournamentID, 1); err != nil {
		return fmt.Errorf("seed chgk: %w", err)
	}
	if err := seedTestSIGame(ctx, tx, tournamentID, 2); err != nil {
		return fmt.Errorf("seed si: %w", err)
	}
	if err := seedTestEKGame(ctx, tx, tournamentID, 3); err != nil {
		return fmt.Errorf("seed ek: %w", err)
	}

	return tx.Commit()
}

func seedTestChGKGame(ctx context.Context, tx *sql.Tx, tournamentID int64, position int) error {
	teams := []map[string]string{
		{"name": "Альфа", "city": "Москва"},
		{"name": "Бета", "city": "Санкт-Петербург"},
		{"name": "Гамма", "city": "Казань"},
		{"name": "Дельта", "city": "Новосибирск"},
		{"name": "Эпсилон", "city": "Екатеринбург"},
	}
	tourComp := []int{15, 15, 15, 15, 15, 15}
	totalQ := 0
	for _, n := range tourComp {
		totalQ += n
	}
	scheme := map[string]any{
		"title":    "ЧГК (тестовый)",
		"gameType": "od",
		"tourComp": tourComp,
		"nTeams":   len(teams),
		"teams":    teams,
	}
	entries := make([][]int, totalQ)
	for i := range entries {
		entries[i] = []int{}
	}
	completed := make([]bool, totalQ)
	state := map[string]any{
		"teams":     teams,
		"entries":   entries,
		"completed": completed,
	}
	return insertGameRow(ctx, tx, tournamentID, "chgk", "ЧГК (тестовый)", "od", position, scheme, state)
}

func seedTestSIGame(ctx context.Context, tx *sql.Tx, tournamentID int64, position int) error {
	participants := []string{"Иван Иванов", "Пётр Петров", "Сидор Сидоров", "Анна Аннова"}
	themesCount := 8
	scheme := map[string]any{
		"title":        "Своя игра (тестовый бой)",
		"gameType":     "si",
		"participants": participants,
		"themes":       themesCount,
	}
	themes := make([]map[string]any, themesCount)
	for t := 0; t < themesCount; t++ {
		rows := make([][]string, len(participants))
		for p := range rows {
			rows[p] = []string{"", "", "", "", ""}
		}
		themes[t] = map[string]any{"answers": rows}
	}
	state := map[string]any{
		"participants": participants,
		"themes":       themes,
		"finished":     false,
	}
	return insertGameRow(ctx, tx, tournamentID, "si", "СИ (тестовый бой)", "si", position, scheme, state)
}

func seedTestEKGame(ctx context.Context, tx *sql.Tx, tournamentID int64, position int) error {
	teams := []struct {
		Name    string
		City    string
		Basket  int
		Number  int
		Roster  []string
	}{
		{"Команда A", "Москва", 1, 1, []string{"Алексей Аверин", "Борис Белов", "Виктор Васильев", "Галина Горина"}},
		{"Команда B", "Санкт-Петербург", 1, 2, []string{"Денис Дроздов", "Елена Еремина", "Жанна Жукова", "Захар Зайцев"}},
		{"Команда C", "Казань", 1, 3, []string{"Игорь Ильин", "Ксения Карпова", "Леонид Львов", "Марина Морозова"}},
		{"Команда D", "Новосибирск", 1, 4, []string{"Никита Новиков", "Ольга Орлова", "Павел Петров", "Раиса Рябова"}},
	}
	venues := []map[string]any{{"number": 1, "title": "Москва-1"}}
	schemeTeams := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		schemeTeams = append(schemeTeams, map[string]any{
			"name":    t.Name,
			"city":    t.City,
			"basket":  t.Basket,
			"number":  t.Number,
			"players": t.Roster,
		})
	}
	schemeSlots := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		schemeSlots = append(schemeSlots, map[string]any{
			"seed": map[string]any{"basket": t.Basket, "number": t.Number},
		})
	}
	scheme := map[string]any{
		"schemaVersion":     2,
		"slug":              "test-ek",
		"title":             "Эрудит-квартет (тестовый бой)",
		"gameType":          "ek",
		"questionValues":    questionValues,
		"regularThemeCount": themeCount,
		"venues":            venues,
		"teams":             schemeTeams,
		"stages": []map[string]any{
			{
				"code":       "test",
				"title":      "Тестовый бой",
				"stage_type": "matches",
				"position":   1,
				"matches": []map[string]any{
					{
						"code":             defaultMatchCode,
						"title":            "Бой A",
						"venue":            1,
						"participantCount": len(teams),
						"slots":            schemeSlots,
					},
				},
			},
		},
	}

	now := utcNow()
	schemeJSON := mustJSON(scheme)
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, "test-ek", "Эрудит-квартет (тестовый бой)", schemeJSON, now)
	if err != nil {
		return err
	}
	gameID, err := insertReturningID(ctx, tx, `
insert into games(tournament_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, 'ek', ?, ?, ?, '{}', 'pending', 'tournament', 'tournament', 1, ?, ?)`,
		tournamentID, "ek", "Эрудит-квартет (тестовый бой)", position, schemeID, schemeJSON, now, now)
	if err != nil {
		return err
	}
	venueID, err := insertReturningID(ctx, tx, `
insert into venues(tournament_id, number, title, created_at, updated_at)
values(?, 1, ?, ?, ?)`, tournamentID, "Москва-1", now, now)
	if err != nil {
		return err
	}
	stageID, err := insertReturningID(ctx, tx, `
insert into stages(tournament_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, 'test', 'Тестовый бой', 'matches', 1, 'pending', '{}')`, tournamentID, gameID)
	if err != nil {
		return err
	}
	matchID, err := insertReturningID(ctx, tx, `
insert into matches(tournament_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, 1, ?, ?, 'active', 1)`,
		tournamentID, gameID, stageID, defaultMatchCode, "Бой A", len(teams), venueID)
	if err != nil {
		return err
	}

	assignmentTeams := make(map[[2]int]int64, len(teams))
	for slotIndex, team := range teams {
		teamID, err := insertReturningID(ctx, tx, `
insert into teams(tournament_id, name, city)
values(?, ?, ?)`, tournamentID, team.Name, team.City)
		if err != nil {
			return err
		}
		assignmentTeams[[2]int{team.Basket, team.Number}] = teamID
		for rosterOrder, fullName := range team.Roster {
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := insertReturningID(ctx, tx, `
insert into players(tournament_id, first_name, last_name)
values(?, ?, ?)`, tournamentID, firstName, lastName)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, ?, ?, ?, null)`, gameID, team.Basket, team.Number, teamID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, 'seed', ?, ?, 0)`, matchID, slotIndex,
			mustJSON(map[string]any{"basket": team.Basket, "number": team.Number}), teamID); err != nil {
			return err
		}
		for themeIndex := 0; themeIndex < themeCount; themeIndex++ {
			if err := insertTheme(ctx, tx, matchID, teamID, "regular", themeIndex, 0, [5]string{}); err != nil {
				return err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
insert into events(tournament_id, revision, type, payload_json, created_at)
values(?, 1, 'import', ?, ?)`, tournamentID, schemeJSON, now); err != nil {
		return err
	}
	return nil
}

func insertGameRow(ctx context.Context, tx *sql.Tx, tournamentID int64, code, title, gameType string, position int, scheme, state map[string]any) error {
	now := utcNow()
	schemeJSON := mustJSON(scheme)
	stateJSON := mustJSON(state)
	schemeID, err := insertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, fmt.Sprintf("test-%s", code), title, schemeJSON, now)
	if err != nil {
		return err
	}
	if _, err := insertReturningID(ctx, tx, `
insert into games(tournament_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, 'active', 'game', 'game', 1, ?, ?)`,
		tournamentID, code, title, gameType, position, schemeID, schemeJSON, stateJSON, now, now); err != nil {
		return err
	}
	return nil
}
