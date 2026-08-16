// The formats that predate the DSL — ОД, КСИ and a pasted ЭК scheme — built
// the way they always were, behind the same Spec.
package gamebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"dope/dope/domain/games"
	"dope/dope/domain/roster"
	"dope/dope/platform/util"
	"dope/dope/storage/store"
	"dope/dope/storage/storeutil"
)

func createODGameTx(ctx context.Context, tx *sql.Tx, festID int64, tours, questions int) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "od", "ОД")
	if err != nil {
		return 0, err
	}
	tourComp := make([]int, tours)
	for i := range tourComp {
		tourComp[i] = questions
	}
	schemeJSON, stateJSON := games.ODEmptyGameJSON(identity.Code, identity.Title, tourComp)
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = roster.ApplyRosterToChGKScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = roster.ApplyRosterToChGKState(string(stateJSON), teams, nil)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "od", schemeJSON, stateJSON)
}

func createKSIGameTx(ctx context.Context, tx *sql.Tx, festID int64, themesCount int, stickers json.RawMessage) (int64, error) {
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ksi", "КСИ")
	if err != nil {
		return 0, err
	}
	schemeJSON, stateJSON := games.KSIStickersEmptyGameJSON(identity.Code, identity.Title, themesCount, stickers)
	teams, err := roster.LoadFestRosterImportTeamsTx(ctx, tx, festID)
	if err != nil {
		return 0, err
	}
	if len(teams) > 0 {
		schemeJSON, err = roster.ApplyRosterToKSIScheme(string(schemeJSON), teams)
		if err != nil {
			return 0, err
		}
		stateJSON, err = roster.ApplyRosterToKSIState(string(stateJSON), teams, themesCount)
		if err != nil {
			return 0, err
		}
	}
	return insertJSONGameTx(ctx, tx, festID, identity, "ksi", schemeJSON, stateJSON)
}

func insertJSONGameTx(ctx context.Context, tx *sql.Tx, festID int64, identity gameIdentity, gameType string, schemeJSON, stateJSON []byte) (int64, error) {
	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, 2, ?, ?)`, uniqueSchemeSlug(identity.Code), identity.Title, string(schemeJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'active', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, identity.Title, gameType, identity.Position, schemeID, string(schemeJSON), now, now)
	if err != nil {
		return 0, err
	}
	if err := insertFlatMatchTx(ctx, tx, festID, gameID, identity.Title, string(stateJSON), now); err != nil {
		return 0, err
	}
	return gameID, nil
}

// insertFlatMatchTx creates a flat game's unified structure: one 'main' stage
// (kind matches) holding one 'main' match that carries the whole game state.
func insertFlatMatchTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, title, stateJSON, now string) error {
	stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, kind, position, status, config_json, block_code, wave_index)
values(?, ?, 'main', '', 'matches', 'matches', 1, 'active', '{}', 'main', 1)`, festID, gameID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, status, revision, state_json)
values(?, ?, ?, 'main', ?, 1, 1, 1, 0, 'active', 0, ?)`, festID, gameID, stageID, title, stateJSON)
	return err
}

func createEKGameTx(ctx context.Context, tx *sql.Tx, festID int64, scheme store.FestScheme) (int64, error) {
	if scheme.GameType == "" {
		scheme.GameType = games.Default
	}
	if scheme.GameType != games.Default {
		return 0, errors.New("для ЭК нужна JSON-схема с gameType \"ek\"")
	}
	if err := storeutil.ValidateScheme(scheme); err != nil {
		return 0, err
	}
	if len(scheme.Teams) > 0 {
		return 0, errors.New("команды загружаются отдельным импортом посева; уберите teams из JSON-схемы")
	}
	schemaJSON, err := json.Marshal(scheme)
	if err != nil {
		return 0, err
	}
	title := strings.TrimSpace(scheme.Title)
	if title == "" {
		title = "ЭК"
	}
	identity, err := nextGameIdentityTx(ctx, tx, festID, "ek", title)
	if err != nil {
		return 0, err
	}
	identity.Title = title

	now := util.UtcNow()
	schemeID, err := store.InsertReturningID(ctx, tx, `
insert into schemes(slug, title, version, schema_json, created_at)
values(?, ?, ?, ?, ?)`, uniqueSchemeSlug(scheme.Slug), title, util.MaxInt(scheme.SchemaVersion, 2), string(schemaJSON), now)
	if err != nil {
		return 0, err
	}
	gameID, err := store.InsertReturningID(ctx, tx, `
insert into games(fest_id, code, title, game_type, position, scheme_id, scheme_json, state_json, status, team_list_source, roster_source, revision, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, '{}', 'pending', 'fest', 'fest', 1, ?, ?)`,
		festID, identity.Code, title, games.Default, identity.Position, schemeID, string(schemaJSON), now, now)
	if err != nil {
		return 0, err
	}

	if err := buildEKStructureTx(ctx, tx, festID, gameID, scheme, now); err != nil {
		return 0, err
	}
	return gameID, nil
}

// buildEKStructureTx materialises an EK game's bracket (venues, stages, matches
// and their unresolved seed slots) from the scheme. Shared by game creation and
// the "clear to pristine" path, which rebuilds the same empty bracket in place.
func buildEKStructureTx(ctx context.Context, tx *sql.Tx, festID, gameID int64, scheme store.FestScheme, now string) error {
	venueIDs := make(map[int]int64, len(scheme.Venues))
	for _, venue := range scheme.Venues {
		venueID, err := upsertVenueTx(ctx, tx, festID, venue, now)
		if err != nil {
			return err
		}
		venueIDs[venue.Number] = venueID
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
		grain := stage.Grain.Normalized()
		stageID, err := store.InsertReturningID(ctx, tx, `
insert into stages(fest_id, game_id, code, title, stage_type, position, status, config_json, block_code, wave_index, group_code)
values(?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`, festID, gameID, stage.Code, stage.Title, stageType, position, configJSON,
			grain.Block, grain.Wave, grain.Group)
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
insert into matches(fest_id, game_id, stage_id, code, title, position, round, wave, participant_count, venue_id, status, revision)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1)`, festID, gameID, stageID, match.Code, match.Title, matchIndex+1, match.Round, match.Wave, participantCount, venueID)
			if err != nil {
				return err
			}
			for slotIndex, slot := range match.Slots {
				sourceType, sourceRef := storeutil.SlotSource(slot)
				if _, err := tx.ExecContext(ctx, `
insert into match_slots(match_id, slot_index, source_type, source_ref_json, participant_id, locked)
values(?, ?, ?, ?, null, 0)`, matchID, slotIndex, sourceType, sourceRef); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func upsertVenueTx(ctx context.Context, tx *sql.Tx, festID int64, venue store.SchemeVenue, now string) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
insert into venues(fest_id, number, title, created_at, updated_at)
values(?, ?, ?, ?, ?)
on conflict(fest_id, number) do update set title = excluded.title, updated_at = excluded.updated_at`,
		festID, venue.Number, venue.Title, now, now); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `select id from venues where fest_id = ? and number = ?`, festID, venue.Number).Scan(&id)
	return id, err
}
