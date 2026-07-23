package dopeserver

import (
	"context"
	"database/sql"
	"dope/dope/domain/games"
	"dope/dope/platform/util"
	"dope/dope/storage/festwrite"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	festID, err := store.ResolveFestID(r.Context(), s.eng.DB, strings.TrimSpace(r.URL.Query().Get("fest_id")))
	if err != nil || festID <= 0 {
		http.Error(w, "missing fest_id", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireFestAdmin(w, r, festID); !ok {
		return
	}
	defer r.Body.Close()

	var scheme store.FestScheme
	if err := json.NewDecoder(r.Body).Decode(&scheme); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	if err := s.importSchemeIntoFest(r.Context(), festID, scheme); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	gameID, err := defaultGameID(r.Context(), s.eng.DB, festID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := s.loadFestViewSnapshot(festID, gameID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, _ := json.Marshal(view)
	s.eng.BroadcastState(festID, "fest", view.Revision, data)
	writeJSON(w, data)
}

func (s *server) importScheme(scheme store.FestScheme) (store.FestView, error) {
	if s.eng.DB == nil {
		return store.FestView{}, errors.New("sqlite is not enabled")
	}
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return store.FestView{}, err
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return store.FestView{}, err
	}

	s.eng.Mu.Lock()
	defer s.eng.Mu.Unlock()

	ctx := context.Background()
	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return store.FestView{}, err
	}
	defer tx.Rollback()

	// A full import wipes and rebuilds every structural row; that churn carries no
	// incremental-undo value and is recorded as one 'import' event below, so skip
	// per-row audit capture (import is a revert boundary).
	if err := festwrite.SuppressAuditTx(ctx, tx); err != nil {
		return store.FestView{}, err
	}

	if err := clearImportedData(ctx, tx); err != nil {
		return store.FestView{}, err
	}

	now := util.UtcNow()
	systemID, err := ensureSystemUser(ctx, tx)
	if err != nil {
		return store.FestView{}, err
	}
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, scheme.Slug, scheme.Title, util.MaxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return store.FestView{}, err
	}
	festID, err := store.InsertReturningID(ctx, tx, `
insert into fests(slug, title, description, rating_id, created_by, revision, created_at, updated_at, is_public)
values(?, ?, '', null, ?, 1, ?, ?, 1)`, scheme.Slug, scheme.Title, systemID, now, now)
	if err != nil {
		return store.FestView{}, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into fest_organizers(fest_id, user_id, role, added_at)
values(?, ?, 'creator', ?)`, festID, systemID, now); err != nil {
		return store.FestView{}, err
	}
	gameType := scheme.GameType
	if gameType == "" {
		gameType = games.Default
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, defaultGameCode, scheme.Title, gameType, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return store.FestView{}, err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := store.InsertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)`, festID, venue.Number, venue.Title, now, now)
		if err != nil {
			return store.FestView{}, err
		}
		venueIDs[venue.Number] = venueID
	}

	assignmentTeams := make(map[[2]int]int64, len(scheme.Teams))
	for _, team := range scheme.Teams {
		teamID, err := store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, team.Name, team.City)
		if err != nil {
			return store.FestView{}, err
		}
		for rosterOrder, fullName := range team.Players {
			fullName = strings.TrimSpace(fullName)
			if fullName == "" {
				continue
			}
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := store.InsertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
			if err != nil {
				return store.FestView{}, err
			}
			if _, err := tx.ExecContext(ctx, `
insert into team_players(team_id, player_id, roster_order)
values(?, ?, ?)`, teamID, playerID, rosterOrder); err != nil {
				return store.FestView{}, err
			}
		}
		if _, err := tx.ExecContext(ctx, `
insert into game_assignments(game_id, basket, number, team_id, player_id)
values(?, ?, ?, ?, null)`, gameID, team.Basket, team.Number, teamID); err != nil {
			return store.FestView{}, err
		}
		assignmentTeams[[2]int{team.Basket, team.Number}] = teamID
	}

	firstMatchCode := ""
	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := storeutil.StageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return store.FestView{}, err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			if firstMatchCode == "" {
				firstMatchCode = match.Code
			}
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return store.FestView{}, err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := storeutil.SlotSource(slot)
				var resolvedTeamID int64
				if sourceType == "seed" && slot.Seed != nil {
					number := slot.Seed.Number
					if number == 0 {
						number = slot.Seed.Position
					}
					resolvedTeamID = assignmentTeams[[2]int{slot.Seed.Basket, number}]
				}
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, sourceType, sourceRef, util.NullableInt64(resolvedTeamID)); err != nil {
					return store.FestView{}, err
				}
			}
		}
	}

	if err := festwrite.AppendJournalTx(ctx, tx, festID, 1, "import", schemaJSON); err != nil {
		return store.FestView{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.FestView{}, err
	}

	s.eng.FestID = festID
	s.eng.ActiveGameID = gameID
	if firstMatchCode != "" {
		s.eng.ActiveMatchCode = firstMatchCode
	}
	return s.loadFestViewLocked(s.eng.FestID, s.eng.ActiveGameID)
}

// importSchemeIntoFest wipes the fest's existing games (and
// dependent rows) and creates a single new game from the supplied scheme.
// The fest row itself stays intact.
func (s *server) importSchemeIntoFest(ctx context.Context, festID int64, scheme store.FestScheme) error {
	if s.eng.DB == nil {
		return errors.New("sqlite is not enabled")
	}
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return err
	}
	if len(scheme.Teams) > 0 {
		return errors.New("команды загружаются отдельным импортом посева; уберите teams из JSON-схемы")
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return err
	}

	s.eng.Mu.Lock()
	defer s.eng.Mu.Unlock()

	tx, err := s.eng.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Skip per-row audit churn for the wipe-and-rebuild; the import is recorded as
	// one 'import' event below and acts as a revert boundary. See festwrite.SuppressAuditTx.
	if err := festwrite.SuppressAuditTx(ctx, tx); err != nil {
		return err
	}

	if err := clearFestImportData(ctx, tx, festID); err != nil {
		return err
	}

	now := util.UtcNow()
	schemeSlug := scheme.Slug + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, schemeSlug, scheme.Title, util.MaxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return err
	}
	gameType := scheme.GameType
	if gameType == "" {
		gameType = games.Default
	}
	gameTitle := scheme.Title
	if strings.TrimSpace(gameTitle) == "" {
		gameTitle = "Игра"
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, 1, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, defaultGameCode, gameTitle, gameType, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return err
	}

	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := store.InsertReturningID(ctx, tx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)`, festID, venue.Number, venue.Title, now, now)
		if err != nil {
			return err
		}
		venueIDs[venue.Number] = venueID
	}

	assignmentTeams := make(map[[2]int]int64, len(scheme.Teams))
	for _, team := range scheme.Teams {
		teamID, err := store.InsertReturningID(ctx, tx, `
insert into teams(fest_id, name, city)
values(?, ?, ?)`, festID, team.Name, team.City)
		if err != nil {
			return err
		}
		for rosterOrder, fullName := range team.Players {
			fullName = strings.TrimSpace(fullName)
			if fullName == "" {
				continue
			}
			firstName, lastName := splitPlayerName(fullName)
			playerID, err := store.InsertReturningID(ctx, tx, `
insert into players(fest_id, first_name, last_name)
values(?, ?, ?)`, festID, firstName, lastName)
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
		assignmentTeams[[2]int{team.Basket, team.Number}] = teamID
	}

	for stageIndex, stage := range scheme.Stages {
		position := stage.Position
		if position == 0 {
			position = stageIndex + 1
		}
		configJSON := storeutil.StageConfigJSON(stage)
		stageType := stage.StageType
		if stageType == "" {
			stageType = "matches"
		}
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json)
values(?, ?, ?, ?, ?, ?, 'pending', ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON)
		if err != nil {
			return err
		}
		if stageType != "matches" {
			continue
		}
		for matchIndex, match := range stage.Matches {
			participantCount := match.ParticipantCount
			if participantCount == 0 {
				participantCount = len(match.Slots)
			}
			var venueID any
			if id, ok := venueIDs[match.Venue]; ok {
				venueID = id
			}
			matchID, err := store.InsertReturningID(ctx, tx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, participantCount, venueID)
			if err != nil {
				return err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := storeutil.SlotSource(slot)
				var resolvedTeamID int64
				if sourceType == "seed" && slot.Seed != nil {
					number := slot.Seed.Number
					if number == 0 {
						number = slot.Seed.Position
					}
					resolvedTeamID = assignmentTeams[[2]int{slot.Seed.Basket, number}]
				}
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, team_id, locked)
values(?, ?, ?, ?, ?, 0)`, matchID, slotIndex, sourceType, sourceRef, util.NullableInt64(resolvedTeamID)); err != nil {
					return err
				}
			}
		}
	}

	if _, err := festwrite.BumpFestRevisionTx(ctx, tx, festID, "import", string(schemaJSON)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// clearFestImportData drops all per-fest rows that an import would
// recreate (games, stages, matches, venues, teams, players, journal). The
// fest row and its organizers stay.
func clearFestImportData(ctx context.Context, tx *sql.Tx, festID int64) error {
	statements := []string{
		`delete from journal where fest_id = ?`,
		`delete from games where fest_id = ?`,
		`delete from team_players where team_id in (select id from teams where fest_id = ?)`,
		`delete from teams where fest_id = ?`,
		`delete from players where fest_id = ?`,
		`delete from venues where fest_id = ?`,
	}
	for _, sqlText := range statements {
		if _, err := tx.ExecContext(ctx, sqlText, festID); err != nil {
			return err
		}
	}
	return nil
}

// clearImportedData wipes fest-scoped data so importScheme can recreate
// the world. Auth tables (users, invites, telegram_login_codes, sessions) and
// schema_versions are intentionally untouched.
func clearImportedData(ctx context.Context, tx *sql.Tx) error {
	tables := []string{
		"journal",
		"reseed_entries",
		"match_results",
		"match_slots",
		"matches",
		"stages",
		"game_assignments",
		"game_team_players",
		"game_players",
		"game_teams",
		"games",
		"team_players",
		"players",
		"teams",
		"venues",
		"fest_organizers",
		"fests",
		"schemes",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "delete from "+table); err != nil {
			return err
		}
	}
	return nil
}
